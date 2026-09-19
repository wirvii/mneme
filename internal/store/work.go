package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/wirvii/mneme/internal/model"
)

const workColumns = `id,uuid,project,source_type,source_id,status,goal,scope_json,verification_json,development_method,base_sha,contract_revision,contract_hash,correction_rounds,max_correction_rounds,dev_evidence_command,dev_evidence_exit,dev_evidence_tail,dev_evidence_sha,dev_evidence_at,created_by,created_at,updated_at,locked_at,completed_at`

// NextWorkID returns the next project-local work identifier by numeric suffix.
func (s *SDDStore) NextWorkID(ctx context.Context, project string) (string, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(CAST(substr(id,6) AS INTEGER)),0) FROM execution_contracts WHERE project=?`, project).Scan(&n)
	if err != nil {
		return "", fmt.Errorf("store: next work id: %w", err)
	}
	return fmt.Sprintf("WORK-%03d", n+1), nil
}

// CreateWork atomically inserts a draft contract and all declared children.
func (s *SDDStore) CreateWork(ctx context.Context, w *model.WorkContract, criteria []model.WorkCriterion, constraints []model.WorkConstraint) error {
	if err := w.Validate(criteria, constraints); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: create work: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if w.SourceType == model.WorkSourceSpec {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM specs WHERE id=? AND project=?`, w.SourceID, w.Project).Scan(&n); err != nil {
			return fmt.Errorf("store: create work: source spec: %w", err)
		}
		if n == 0 {
			return model.ErrSpecNotFound
		}
		result, err := tx.ExecContext(ctx, `UPDATE specs SET execution_model=? WHERE id=? AND project=?`, model.ExecutionModelDeliveryV2, w.SourceID, w.Project)
		if err != nil {
			return fmt.Errorf("store: create work: mark source spec: %w", err)
		}
		if !oneRow(result) {
			return fmt.Errorf("store: create work: mark source spec: %w", model.ErrSpecNotFound)
		}
	}
	anchor, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("store: create work: uuid: %w", err)
	}
	w.UUID = anchor.String()
	now := time.Now().UTC()
	w.CreatedAt = now
	w.UpdatedAt = now
	scope, err := json.Marshal(w.Scope)
	if err != nil {
		return fmt.Errorf("store: create work: scope: %w", err)
	}
	verification, err := json.Marshal(w.Verification)
	if err != nil {
		return fmt.Errorf("store: create work: verification: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO execution_contracts(id,uuid,project,source_type,source_id,status,goal,scope_json,verification_json,development_method,max_correction_rounds,created_by,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, w.ID, w.UUID, w.Project, w.SourceType, w.SourceID, w.Status, w.Goal, string(scope), string(verification), w.DevelopmentMethod, w.MaxCorrectionRounds, w.CreatedBy, formatTime(now), formatTime(now))
	if err != nil {
		return fmt.Errorf("store: create work: insert contract: %w", err)
	}
	if err := replaceWorkChildren(ctx, tx, w.ID, criteria, constraints, now); err != nil {
		return fmt.Errorf("store: create work: %w", err)
	}
	return tx.Commit()
}

func replaceWorkChildren(ctx context.Context, tx *sql.Tx, workID string, criteria []model.WorkCriterion, constraints []model.WorkConstraint, now time.Time) error {
	for i := range criteria {
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		criteria[i].ID = id.String()
		criteria[i].WorkID = workID
		criteria[i].Seq = i + 1
		criteria[i].Status = model.CriterionPending
		criteria[i].CreatedAt = now
		_, err = tx.ExecContext(ctx, `INSERT INTO execution_criteria(id,work_id,seq,criterion_key,declaration,status,created_at) VALUES(?,?,?,?,?,?,?)`, criteria[i].ID, workID, criteria[i].Seq, criteria[i].Key, criteria[i].Declaration, criteria[i].Status, formatTime(now))
		if err != nil {
			return fmt.Errorf("insert criterion %d: %w", i+1, err)
		}
	}
	for i := range constraints {
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		constraints[i].ID = id.String()
		constraints[i].WorkID = workID
		constraints[i].Seq = i + 1
		constraints[i].CreatedAt = now
		_, err = tx.ExecContext(ctx, `INSERT INTO execution_constraints(id,work_id,seq,constraint_key,text,source,created_at) VALUES(?,?,?,?,?,?,?)`, constraints[i].ID, workID, constraints[i].Seq, constraints[i].Key, constraints[i].Text, constraints[i].Source, formatTime(now))
		if err != nil {
			return fmt.Errorf("insert constraint %d: %w", i+1, err)
		}
	}
	return nil
}

type workScanner interface{ Scan(...any) error }

// GetWork reads one work contract without loading its child collections.
func (s *SDDStore) GetWork(ctx context.Context, id string) (*model.WorkContract, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+workColumns+` FROM execution_contracts WHERE id=?`, id)
	w, err := scanWorkDirect(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, model.ErrWorkNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get work: %w", err)
	}
	return w, nil
}
func scanWorkDirect(row workScanner) (*model.WorkContract, error) {
	w := &model.WorkContract{}
	var source, status, method, scopeRaw, verificationRaw, commandRaw, created, updated, locked, completed, evidenceAt string
	var evidenceExit int
	var evidenceTail, evidenceSHA string
	err := row.Scan(&w.ID, &w.UUID, &w.Project, &source, &w.SourceID, &status, &w.Goal, &scopeRaw, &verificationRaw, &method, &w.BaseSHA, &w.ContractRevision, &w.ContractHash, &w.CorrectionRounds, &w.MaxCorrectionRounds, &commandRaw, &evidenceExit, &evidenceTail, &evidenceSHA, &evidenceAt, &w.CreatedBy, &created, &updated, &locked, &completed)
	if err != nil {
		return w, err
	}
	w.SourceType = model.WorkSourceType(source)
	w.Status = model.WorkStatus(status)
	w.DevelopmentMethod = model.DevelopmentMethod(method)
	if err := json.Unmarshal([]byte(scopeRaw), &w.Scope); err != nil {
		return w, fmt.Errorf("scope_json: %w", err)
	}
	if err := json.Unmarshal([]byte(verificationRaw), &w.Verification); err != nil {
		return w, fmt.Errorf("verification_json: %w", err)
	}
	var e error
	if w.CreatedAt, e = parseTime(created); e != nil {
		return w, e
	}
	if w.UpdatedAt, e = parseTime(updated); e != nil {
		return w, e
	}
	if locked != "" {
		v, err := parseTime(locked)
		if err != nil {
			return w, err
		}
		w.LockedAt = &v
	}
	if completed != "" {
		v, err := parseTime(completed)
		if err != nil {
			return w, err
		}
		w.CompletedAt = &v
	}
	if evidenceAt != "" {
		v, err := parseTime(evidenceAt)
		if err != nil {
			return w, err
		}
		var command []string
		if commandRaw != "" {
			if err := json.Unmarshal([]byte(commandRaw), &command); err != nil {
				return w, err
			}
		}
		w.DevEvidence = &model.RedTestEvidence{Command: command, ExitCode: evidenceExit, OutputTail: evidenceTail, CommitSHA: evidenceSHA, TakenAt: v}
	}
	return w, nil
}

// GetWorkAggregate reads the complete work aggregate in deterministic child order.
func (s *SDDStore) GetWorkAggregate(ctx context.Context, id string) (*model.WorkAggregate, error) {
	w, err := s.GetWork(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("store: get work aggregate: contract: %w", err)
	}
	criteria, err := s.listCriteria(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("store: get work aggregate: %w", err)
	}
	constraints, err := s.listConstraints(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("store: get work aggregate: %w", err)
	}
	findings, err := s.ListFindings(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("store: get work aggregate: findings: %w", err)
	}
	history, err := s.GetWorkHistory(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("store: get work aggregate: history: %w", err)
	}
	return &model.WorkAggregate{Contract: w, Criteria: criteria, Constraints: constraints, Findings: findings, History: history}, nil
}

func (s *SDDStore) listCriteria(ctx context.Context, id string) ([]model.WorkCriterion, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,work_id,seq,criterion_key,declaration,status,evidence,checked_by,checked_at,created_at FROM execution_criteria WHERE work_id=? ORDER BY seq,rowid`, id)
	if err != nil {
		return nil, fmt.Errorf("store: list criteria: %w", err)
	}
	defer rows.Close()
	var out []model.WorkCriterion
	for rows.Next() {
		var c model.WorkCriterion
		var checked, created string
		if err := rows.Scan(&c.ID, &c.WorkID, &c.Seq, &c.Key, &c.Declaration, (*string)(&c.Status), &c.Evidence, &c.CheckedBy, &checked, &created); err != nil {
			return nil, err
		}
		c.CreatedAt, err = parseTime(created)
		if err != nil {
			return nil, fmt.Errorf("criterion created_at: %w", err)
		}
		if checked != "" {
			v, e := parseTime(checked)
			if e != nil {
				return nil, fmt.Errorf("criterion checked_at: %w", e)
			}
			c.CheckedAt = &v
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *SDDStore) listConstraints(ctx context.Context, id string) ([]model.WorkConstraint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,work_id,seq,constraint_key,text,source,created_at FROM execution_constraints WHERE work_id=? ORDER BY seq,rowid`, id)
	if err != nil {
		return nil, fmt.Errorf("store: list constraints: %w", err)
	}
	defer rows.Close()
	var out []model.WorkConstraint
	for rows.Next() {
		var c model.WorkConstraint
		var created string
		if err := rows.Scan(&c.ID, &c.WorkID, &c.Seq, &c.Key, &c.Text, &c.Source, &created); err != nil {
			return nil, err
		}
		c.CreatedAt, err = parseTime(created)
		if err != nil {
			return nil, fmt.Errorf("constraint created_at: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListWorks returns readable work rows, total matches before limit, and identified unreadable rows.
func (s *SDDStore) ListWorks(ctx context.Context, project string, status model.WorkStatus, limit int) ([]*model.WorkContract, int, []model.UnreadableRow, error) {
	where := ` WHERE project=?`
	args := []any{project}
	if status != "" {
		where += ` AND status=?`
		args = append(args, status)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM execution_contracts`+where, args...).Scan(&total); err != nil {
		return nil, 0, nil, err
	}
	q := `SELECT ` + workColumns + ` FROM execution_contracts` + where + ` ORDER BY rowid`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, nil, err
	}
	defer rows.Close()
	var items []*model.WorkContract
	var unreadable []model.UnreadableRow
	for rows.Next() {
		w, err := scanWorkDirect(rows)
		if err != nil {
			unreadable = append(unreadable, model.UnreadableRow{Kind: "work", ID: w.ID, Column: "content", Reason: err.Error()})
			continue
		}
		items = append(items, w)
	}
	return items, total, unreadable, rows.Err()
}

// LockWork freezes a draft against persisted children and records the first revision.
func (s *SDDStore) LockWork(ctx context.Context, id, baseSHA string) error {
	if strings.TrimSpace(baseSHA) == "" {
		return fmt.Errorf("%w: base_sha: required", model.ErrInvalidContract)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := lockWorkTx(ctx, tx, id, baseSHA, ""); err != nil {
		return err
	}
	return tx.Commit()
}

func lockWorkTx(ctx context.Context, tx *sql.Tx, id, baseSHA, by string) (time.Time, error) {
	w, criteria, constraints, err := loadWorkForMutation(ctx, tx, id)
	if err != nil {
		return time.Time{}, err
	}
	if w.Status != model.WorkStatusDraft {
		return time.Time{}, model.ErrInvalidWorkTransition
	}
	now := time.Now().UTC()
	w.Status = model.WorkStatusLocked
	w.BaseSHA = baseSHA
	w.ContractRevision = 1
	w.LockedAt = &now
	w.ContractHash = model.ContractHash(*w, criteria, constraints)
	res, err := tx.ExecContext(ctx, `UPDATE execution_contracts SET status=?,base_sha=?,contract_revision=?,contract_hash=?,locked_at=?,updated_at=? WHERE id=? AND status=?`, w.Status, w.BaseSHA, w.ContractRevision, w.ContractHash, formatTime(now), formatTime(now), id, model.WorkStatusDraft)
	if err != nil {
		return time.Time{}, err
	}
	if !oneRow(res) {
		return time.Time{}, model.ErrInvalidWorkTransition
	}
	if err := insertWorkHistory(ctx, tx, id, model.WorkStatusDraft, model.WorkStatusLocked, 1, by, "", now); err != nil {
		return time.Time{}, err
	}
	return now, nil
}

// LockWorkAndStart locks a draft and starts implementation atomically.
func (s *SDDStore) LockWorkAndStart(ctx context.Context, id, baseSHA, by string) error {
	if strings.TrimSpace(baseSHA) == "" {
		return fmt.Errorf("%w: base_sha: required", model.ErrInvalidContract)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now, err := lockWorkTx(ctx, tx, id, baseSHA, by)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE execution_contracts SET status=?,updated_at=? WHERE id=? AND status=?`, model.WorkStatusImplementing, formatTime(now), id, model.WorkStatusLocked)
	if err != nil {
		return err
	}
	if !oneRow(result) {
		return model.ErrInvalidWorkTransition
	}
	if err := insertWorkHistory(ctx, tx, id, model.WorkStatusLocked, model.WorkStatusImplementing, 1, by, "", now); err != nil {
		return err
	}
	return tx.Commit()
}

func loadWorkForMutation(ctx context.Context, tx *sql.Tx, id string) (*model.WorkContract, []model.WorkCriterion, []model.WorkConstraint, error) {
	w, err := scanWorkDirect(tx.QueryRowContext(ctx, `SELECT `+workColumns+` FROM execution_contracts WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil, model.ErrWorkNotFound
	}
	if err != nil {
		return nil, nil, nil, err
	}
	criteria, err := listCriteriaTx(ctx, tx, id)
	if err != nil {
		return nil, nil, nil, err
	}
	constraints, err := listConstraintsTx(ctx, tx, id)
	return w, criteria, constraints, err
}
func listCriteriaTx(ctx context.Context, tx *sql.Tx, id string) ([]model.WorkCriterion, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,work_id,seq,criterion_key,declaration,status,evidence,checked_by,checked_at,created_at FROM execution_criteria WHERE work_id=? ORDER BY seq,rowid`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.WorkCriterion
	for rows.Next() {
		var c model.WorkCriterion
		var checked, created string
		if err := rows.Scan(&c.ID, &c.WorkID, &c.Seq, &c.Key, &c.Declaration, (*string)(&c.Status), &c.Evidence, &c.CheckedBy, &checked, &created); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func listConstraintsTx(ctx context.Context, tx *sql.Tx, id string) ([]model.WorkConstraint, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,work_id,seq,constraint_key,text,source,created_at FROM execution_constraints WHERE work_id=? ORDER BY seq,rowid`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.WorkConstraint
	for rows.Next() {
		var c model.WorkConstraint
		var created string
		if err := rows.Scan(&c.ID, &c.WorkID, &c.Seq, &c.Key, &c.Text, &c.Source, &created); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// TransitionWork atomically applies one declared edge with optimistic status matching.
func (s *SDDStore) TransitionWork(ctx context.Context, id string, from, to model.WorkStatus, by, reason string) error {
	if !model.CanTransitionWork(from, to) {
		return model.ErrInvalidWorkTransition
	}
	if (to == model.WorkStatusAbandoned || (from == model.WorkStatusEscalated && to == model.WorkStatusImplementing)) && strings.TrimSpace(reason) == "" {
		return model.ErrReasonRequired
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var current string
	var rounds, max int
	if err := tx.QueryRowContext(ctx, `SELECT status,correction_rounds,max_correction_rounds FROM execution_contracts WHERE id=?`, id).Scan(&current, &rounds, &max); errors.Is(err, sql.ErrNoRows) {
		return model.ErrWorkNotFound
	} else if err != nil {
		return err
	}
	if model.WorkStatus(current) != from {
		return model.ErrInvalidWorkTransition
	}
	if to == model.WorkStatusCorrecting {
		if rounds >= max {
			return model.ErrCorrectionBudgetExhausted
		}
		rounds++
	}
	if from == model.WorkStatusEscalated && to == model.WorkStatusImplementing {
		rounds = 0
	}
	now := time.Now().UTC()
	completed := ""
	if to == model.WorkStatusDone {
		completed = formatTime(now)
	}
	res, err := tx.ExecContext(ctx, `UPDATE execution_contracts SET status=?,correction_rounds=?,completed_at=?,updated_at=? WHERE id=? AND status=?`, to, rounds, completed, formatTime(now), id, from)
	if err != nil {
		return err
	}
	if !oneRow(res) {
		return model.ErrInvalidWorkTransition
	}
	var revision int
	if err := tx.QueryRowContext(ctx, `SELECT contract_revision FROM execution_contracts WHERE id=?`, id).Scan(&revision); err != nil {
		return err
	}
	if err := insertWorkHistory(ctx, tx, id, from, to, revision, by, reason, now); err != nil {
		return err
	}
	return tx.Commit()
}

// ResumeWork atomically records the coordinator's decision and resets only the correction budget.
func (s *SDDStore) ResumeWork(ctx context.Context, id, by, reason string) error {
	if strings.TrimSpace(by) == "" {
		return fmt.Errorf("%w: by: required", model.ErrInvalidContract)
	}
	if strings.TrimSpace(reason) == "" {
		return model.ErrReasonRequired
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: resume work: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var status string
	var revision int
	if err := tx.QueryRowContext(ctx, `SELECT status,contract_revision FROM execution_contracts WHERE id=?`, id).Scan(&status, &revision); errors.Is(err, sql.ErrNoRows) {
		return model.ErrWorkNotFound
	} else if err != nil {
		return fmt.Errorf("store: resume work: load: %w", err)
	}
	if model.WorkStatus(status) != model.WorkStatusEscalated {
		return model.ErrInvalidWorkTransition
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `UPDATE execution_contracts SET status=?,correction_rounds=0,updated_at=? WHERE id=? AND status=?`, model.WorkStatusImplementing, formatTime(now), id, model.WorkStatusEscalated)
	if err != nil {
		return fmt.Errorf("store: resume work: update: %w", err)
	}
	if !oneRow(result) {
		return model.ErrInvalidWorkTransition
	}
	if err := insertWorkHistory(ctx, tx, id, model.WorkStatusEscalated, model.WorkStatusImplementing, revision, by, reason, now); err != nil {
		return fmt.Errorf("store: resume work: history: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: resume work: commit: %w", err)
	}
	return nil
}

// AmendWork replaces normative content, increments revision, and returns work to implementing.
func (s *SDDStore) AmendWork(ctx context.Context, req model.AmendWorkRequest) error {
	if strings.TrimSpace(req.Reason) == "" {
		return model.ErrReasonRequired
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	w, _, _, err := loadWorkForMutation(ctx, tx, req.WorkID)
	if err != nil {
		return err
	}
	if w.Status.Terminal() || w.Status == model.WorkStatusDraft || w.Status == model.WorkStatusEscalated {
		return model.ErrInvalidWorkTransition
	}
	from := w.Status
	w.Goal = req.Goal
	w.Scope = req.Scope
	w.Verification = req.Verification
	w.DevelopmentMethod = req.DevelopmentMethod
	w.Status = model.WorkStatusImplementing
	w.ContractRevision++
	if err := w.Validate(req.Criteria, req.Constraints); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM execution_criteria WHERE work_id=?`, req.WorkID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM execution_constraints WHERE work_id=?`, req.WorkID); err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := replaceWorkChildren(ctx, tx, req.WorkID, req.Criteria, req.Constraints, now); err != nil {
		return err
	}
	criteria, constraints, err := func() ([]model.WorkCriterion, []model.WorkConstraint, error) {
		c, e := listCriteriaTx(ctx, tx, req.WorkID)
		if e != nil {
			return nil, nil, e
		}
		r, e := listConstraintsTx(ctx, tx, req.WorkID)
		return c, r, e
	}()
	if err != nil {
		return err
	}
	w.ContractHash = model.ContractHash(*w, criteria, constraints)
	scope, _ := json.Marshal(w.Scope)
	verification, _ := json.Marshal(w.Verification)
	_, err = tx.ExecContext(ctx, `UPDATE execution_contracts SET goal=?,scope_json=?,verification_json=?,development_method=?,contract_revision=?,contract_hash=?,status=?,updated_at=? WHERE id=?`, w.Goal, string(scope), string(verification), w.DevelopmentMethod, w.ContractRevision, w.ContractHash, w.Status, formatTime(now), req.WorkID)
	if err != nil {
		return err
	}
	if err := insertWorkHistory(ctx, tx, req.WorkID, from, w.Status, w.ContractRevision, req.By, req.Reason, now); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateCriterionResult changes only the latest observation fields after locking.
func (s *SDDStore) UpdateCriterionResult(ctx context.Context, criterionID string, status model.CriterionStatus, evidence, by string, at time.Time) error {
	if !status.Valid() {
		return model.ErrInvalidContract
	}
	res, err := s.db.ExecContext(ctx, `UPDATE execution_criteria SET status=?,evidence=?,checked_by=?,checked_at=? WHERE id=? AND work_id IN (SELECT id FROM execution_contracts WHERE status<>'draft')`, status, evidence, by, formatTime(at), criterionID)
	if err != nil {
		return err
	}
	if !oneRow(res) {
		return model.ErrContractNotLocked
	}
	return nil
}

// SetRedTestEvidence persists method evidence while treating TakenAt as its presence marker.
func (s *SDDStore) SetRedTestEvidence(ctx context.Context, id string, ev model.RedTestEvidence) error {
	var method string
	if err := s.db.QueryRowContext(ctx, `SELECT development_method FROM execution_contracts WHERE id=?`, id).Scan(&method); errors.Is(err, sql.ErrNoRows) {
		return model.ErrWorkNotFound
	} else if err != nil {
		return err
	}
	if model.DevelopmentMethod(method) != model.DevelopmentMethodTDD {
		return model.ErrEvidenceNotApplicable
	}
	command, _ := json.Marshal(ev.Command)
	_, err := s.db.ExecContext(ctx, `UPDATE execution_contracts SET dev_evidence_command=?,dev_evidence_exit=?,dev_evidence_tail=?,dev_evidence_sha=?,dev_evidence_at=?,updated_at=? WHERE id=?`, string(command), ev.ExitCode, ev.OutputTail, ev.CommitSHA, formatTime(ev.TakenAt), formatTime(time.Now().UTC()), id)
	return err
}

func insertWorkHistory(ctx context.Context, tx *sql.Tx, id string, from, to model.WorkStatus, revision int, by, reason string, at time.Time) error {
	historyID, err := uuid.NewV7()
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO execution_history(id,work_id,from_status,to_status,contract_revision,by,reason,at) VALUES(?,?,?,?,?,?,?,?)`, historyID.String(), id, from, to, revision, by, reason, formatTime(at))
	return err
}
func oneRow(res sql.Result) bool { n, err := res.RowsAffected(); return err == nil && n == 1 }

// GetWorkHistory returns transition history in timestamp and insertion order.
func (s *SDDStore) GetWorkHistory(ctx context.Context, id string) ([]model.WorkHistoryEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,work_id,from_status,to_status,contract_revision,by,reason,at FROM execution_history WHERE work_id=? ORDER BY at,rowid`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.WorkHistoryEntry
	for rows.Next() {
		var h model.WorkHistoryEntry
		var from, to, at string
		if err := rows.Scan(&h.ID, &h.WorkID, &from, &to, &h.ContractRevision, &h.By, &h.Reason, &at); err != nil {
			return nil, err
		}
		h.FromStatus = model.WorkStatus(from)
		h.ToStatus = model.WorkStatus(to)
		h.At, err = parseTime(at)
		if err != nil {
			return nil, fmt.Errorf("history at: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
