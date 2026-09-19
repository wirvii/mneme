package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/wirvii/mneme/internal/model"
)

// AddFinding records an immutable review observation only in its matching review phase.
func (s *SDDStore) AddFinding(ctx context.Context, f *model.WorkFinding) error {
	if !f.Category.Valid() || !f.Severity.Valid() || !f.Origin.Valid() || !f.ReviewPhase.Valid() || strings.TrimSpace(f.Description) == "" {
		return model.ErrInvalidContract
	}
	var status string
	if err := s.db.QueryRowContext(ctx, `SELECT status FROM execution_contracts WHERE id=?`, f.WorkID).Scan(&status); errors.Is(err, sql.ErrNoRows) {
		return model.ErrWorkNotFound
	} else if err != nil {
		return err
	}
	if (f.ReviewPhase == model.ReviewPhaseInitial && model.WorkStatus(status) != model.WorkStatusVerifying) || (f.ReviewPhase == model.ReviewPhaseTargeted && model.WorkStatus(status) != model.WorkStatusTargetedVerifying) {
		return model.ErrInvalidWorkTransition
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0)+1 FROM execution_findings WHERE work_id=?`, f.WorkID).Scan(&f.Seq); err != nil {
		return err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	f.ID = id.String()
	f.Status = model.FindingOpen
	f.CreatedAt = time.Now().UTC()
	_, err = tx.ExecContext(ctx, `INSERT INTO execution_findings(id,work_id,seq,category,severity,description,location,evidence,origin,review_phase,status,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, f.ID, f.WorkID, f.Seq, f.Category, f.Severity, f.Description, f.Location, f.Evidence, f.Origin, f.ReviewPhase, f.Status, formatTime(f.CreatedAt))
	if err != nil {
		return fmt.Errorf("store: add finding: %w", err)
	}
	return tx.Commit()
}

// ResolveFinding changes only resolution fields, preserving the finding's original facts.
func (s *SDDStore) ResolveFinding(ctx context.Context, id string, status model.FindingStatus, by, reason, backlogID string) error {
	if !status.Valid() || status == model.FindingOpen {
		return model.ErrInvalidContract
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var category, current string
	if err := tx.QueryRowContext(ctx, `SELECT category,status FROM execution_findings WHERE id=?`, id).Scan(&category, &current); errors.Is(err, sql.ErrNoRows) {
		return model.ErrFindingNotFound
	} else if err != nil {
		return err
	}
	if model.FindingStatus(current) != model.FindingOpen {
		return model.ErrInvalidContract
	}
	if model.FindingCategory(category).Blocks() && status != model.FindingFixed && status != model.FindingInvalid {
		return model.ErrCannotAcceptBlockingFinding
	}
	if (status == model.FindingAccepted || status == model.FindingInvalid) && strings.TrimSpace(reason) == "" {
		return model.ErrReasonRequired
	}
	if status == model.FindingBacklogged && strings.TrimSpace(backlogID) == "" {
		return model.ErrInvalidContract
	}
	now := time.Now().UTC()
	_, err = tx.ExecContext(ctx, `UPDATE execution_findings SET status=?,backlog_id=?,resolution_reason=?,resolved_by=?,resolved_at=? WHERE id=? AND status='open'`, status, backlogID, reason, by, formatTime(now), id)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// ListFindings returns findings by their stable per-work sequence.
func (s *SDDStore) ListFindings(ctx context.Context, workID string) ([]model.WorkFinding, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,work_id,seq,category,severity,description,location,evidence,origin,review_phase,status,backlog_id,resolution_reason,resolved_by,created_at,resolved_at FROM execution_findings WHERE work_id=? ORDER BY seq,rowid`, workID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.WorkFinding
	for rows.Next() {
		var f model.WorkFinding
		var category, severity, origin, phase, status, created, resolved string
		if err := rows.Scan(&f.ID, &f.WorkID, &f.Seq, &category, &severity, &f.Description, &f.Location, &f.Evidence, &origin, &phase, &status, &f.BacklogID, &f.ResolutionReason, &f.ResolvedBy, &created, &resolved); err != nil {
			return nil, err
		}
		f.Category = model.FindingCategory(category)
		f.Severity = model.Priority(severity)
		f.Origin = model.FindingOrigin(origin)
		f.ReviewPhase = model.ReviewPhase(phase)
		f.Status = model.FindingStatus(status)
		f.CreatedAt, err = parseTime(created)
		if err != nil {
			return nil, fmt.Errorf("finding created_at: %w", err)
		}
		if resolved != "" {
			v, e := parseTime(resolved)
			if e != nil {
				return nil, fmt.Errorf("finding resolved_at: %w", e)
			}
			f.ResolvedAt = &v
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// CountOpenBlockingFindings counts open rows using categories derived from model.Blocks.
func (s *SDDStore) CountOpenBlockingFindings(ctx context.Context, workID string) (int, error) {
	categories := model.BlockingFindingCategories()
	args := []any{workID}
	marks := make([]string, len(categories))
	for i, c := range categories {
		marks[i] = "?"
		args = append(args, c)
	}
	q := `SELECT COUNT(*) FROM execution_findings WHERE work_id=? AND status='open' AND category IN (` + strings.Join(marks, ",") + `)`
	var n int
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// CompleteWorkResult returns the exact persisted evidence consumed by completion.
type CompleteWorkResult struct {
	Certificate *model.DeliveryCertificate
	Checks      []model.DeliveryCheck
}

// CompleteWork atomically validates the latest evidence and closes one work item.
func (s *SDDStore) CompleteWork(ctx context.Context, workID, headSHA, by string) (CompleteWorkResult, error) {
	if strings.TrimSpace(by) == "" {
		return CompleteWorkResult{}, fmt.Errorf("%w: by: required", model.ErrInvalidContract)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CompleteWorkResult{}, fmt.Errorf("store: complete work: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	work, err := scanWorkDirect(tx.QueryRowContext(ctx, `SELECT `+workColumns+` FROM execution_contracts WHERE id=?`, workID))
	if errors.Is(err, sql.ErrNoRows) {
		return CompleteWorkResult{}, model.ErrWorkNotFound
	}
	if err != nil {
		return CompleteWorkResult{}, fmt.Errorf("store: complete work: load work: %w", err)
	}
	cert, err := scanDeliveryCertificate(tx.QueryRowContext(ctx, `SELECT id,project,work_id,contract_revision,contract_hash,head_sha,base_sha,verdict,dirty,evidence,mneme_version,started_at,finished_at,duration_ms,created_at FROM delivery_certificates WHERE project=? AND work_id=? ORDER BY rowid DESC LIMIT 1`, work.Project, workID))
	if errors.Is(err, sql.ErrNoRows) {
		cert = nil
	} else if err != nil {
		return CompleteWorkResult{}, fmt.Errorf("store: complete work: load certificate: %w", err)
	}
	var checks []model.DeliveryCheck
	if cert != nil {
		checks, err = listDeliveryChecksTx(ctx, tx, cert.ID)
		if err != nil {
			return CompleteWorkResult{}, fmt.Errorf("store: complete work: load checks: %w", err)
		}
	}
	blocking, err := countOpenBlockingFindingsTx(ctx, tx, workID)
	if err != nil {
		return CompleteWorkResult{}, fmt.Errorf("store: complete work: count findings: %w", err)
	}
	if ok, reason := model.CanComplete(model.CompletionInput{
		Status: work.Status, ContractRevision: work.ContractRevision, ContractHash: work.ContractHash,
		HeadSHA: headSHA, BaseSHA: work.BaseSHA, Certificate: cert, OpenBlockingFindings: blocking,
	}); !ok {
		return CompleteWorkResult{}, fmt.Errorf("%w: %s", model.ErrInvalidWorkTransition, reason)
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `UPDATE execution_contracts SET status=?,completed_at=?,updated_at=? WHERE id=? AND status=?`, model.WorkStatusDone, formatTime(now), formatTime(now), workID, work.Status)
	if err != nil {
		return CompleteWorkResult{}, fmt.Errorf("store: complete work: update: %w", err)
	}
	if !oneRow(result) {
		return CompleteWorkResult{}, model.ErrInvalidWorkTransition
	}
	if err := insertWorkHistory(ctx, tx, workID, work.Status, model.WorkStatusDone, work.ContractRevision, by, "", now); err != nil {
		return CompleteWorkResult{}, fmt.Errorf("store: complete work: history: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CompleteWorkResult{}, fmt.Errorf("store: complete work: commit: %w", err)
	}
	return CompleteWorkResult{Certificate: cert, Checks: checks}, nil
}

func listDeliveryChecksTx(ctx context.Context, tx *sql.Tx, certificateID string) ([]model.DeliveryCheck, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,certificate_id,seq,kind,name,status,effect,detail,duration_ms,output_sha256,output_tail,created_at FROM delivery_checks WHERE certificate_id=? ORDER BY seq,rowid`, certificateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var checks []model.DeliveryCheck
	for rows.Next() {
		var check model.DeliveryCheck
		var status, effect, created string
		if err := rows.Scan(&check.ID, &check.CertificateID, &check.Seq, &check.Kind, &check.Name, &status, &effect, &check.Detail, &check.DurationMs, &check.OutputSHA256, &check.OutputTail, &created); err != nil {
			return nil, err
		}
		check.Status = model.DeliveryCheckStatus(status)
		check.Effect = model.DeliveryCheckEffect(effect)
		check.CreatedAt, _ = parseTime(created)
		checks = append(checks, check)
	}
	return checks, rows.Err()
}

// InsertDeliveryCertificate atomically writes a certificate and its ordered checks.
func (s *SDDStore) InsertDeliveryCertificate(ctx context.Context, cert *model.DeliveryCertificate, checks []*model.DeliveryCheck) error {
	return s.InsertDeliveryEvaluation(ctx, cert, checks, nil)
}

// InsertDeliveryEvaluation atomically writes one factual delivery result and
// the automatic criterion observations produced by that same evaluation.
func (s *SDDStore) InsertDeliveryEvaluation(ctx context.Context, cert *model.DeliveryCertificate, checks []*model.DeliveryCheck, observations []model.CriterionObservation) error {
	if err := validateDeliveryEvaluation(cert, checks, observations); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var status, project, contractHash, baseSHA string
	var revision int
	if err := tx.QueryRowContext(ctx, `SELECT status,project,contract_revision,contract_hash,base_sha FROM execution_contracts WHERE id=?`, cert.WorkID).Scan(&status, &project, &revision, &contractHash, &baseSHA); errors.Is(err, sql.ErrNoRows) {
		return model.ErrWorkNotFound
	} else if err != nil {
		return fmt.Errorf("store: insert delivery evaluation: load work: %w", err)
	}
	if model.WorkStatus(status) != model.WorkStatusVerifying && model.WorkStatus(status) != model.WorkStatusTargetedVerifying {
		return model.ErrInvalidWorkTransition
	}
	if cert.Project != project || cert.ContractRevision != revision || cert.ContractHash != contractHash || cert.BaseSHA != baseSHA {
		return model.ErrInvalidContract
	}
	if err := insertDeliveryEvaluationTx(ctx, tx, cert, checks, observations); err != nil {
		return err
	}
	return tx.Commit()
}

// InitialReviewWrite contains every row committed by the single initial-review transaction.
type InitialReviewWrite struct {
	Certificate  *model.DeliveryCertificate
	Checks       []*model.DeliveryCheck
	Observations []model.CriterionObservation
	Findings     []*model.WorkFinding
	Resolutions  []model.WorkFindingResolutionInput
	By           string
}

// InitialReviewResult reports the status and correction counter committed with an initial review.
type InitialReviewResult struct {
	Status           model.WorkStatus
	CorrectionRounds int
}

// TargetedReviewWrite contains every row committed by one targeted-review transaction.
type TargetedReviewWrite struct {
	Certificate           *model.DeliveryCertificate
	Checks                []*model.DeliveryCheck
	Observations          []model.CriterionObservation
	Resolutions           []model.WorkFindingResolutionInput
	Findings              []*model.WorkFinding
	ExpectedCertificateID string
	By                    string
}

// InsertInitialReview atomically records initial findings, factual evidence, and implementing-to-verifying.
func (s *SDDStore) InsertInitialReview(ctx context.Context, in InitialReviewWrite) (InitialReviewResult, error) {
	if err := validateDeliveryEvaluation(in.Certificate, in.Checks, in.Observations); err != nil {
		return InitialReviewResult{}, err
	}
	if strings.TrimSpace(in.By) == "" {
		return InitialReviewResult{}, model.ErrInvalidContract
	}
	for _, finding := range in.Findings {
		if finding == nil || !finding.Category.Valid() || !finding.Severity.Valid() || strings.TrimSpace(finding.Description) == "" || strings.TrimSpace(finding.Evidence) == "" {
			return InitialReviewResult{}, model.ErrInvalidContract
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return InitialReviewResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	cert := in.Certificate
	var status, project, contractHash, baseSHA string
	var revision, correctionRounds, maxCorrectionRounds int
	if err := tx.QueryRowContext(ctx, `SELECT status,project,contract_revision,contract_hash,base_sha,correction_rounds,max_correction_rounds FROM execution_contracts WHERE id=?`, cert.WorkID).Scan(&status, &project, &revision, &contractHash, &baseSHA, &correctionRounds, &maxCorrectionRounds); errors.Is(err, sql.ErrNoRows) {
		return InitialReviewResult{}, model.ErrWorkNotFound
	} else if err != nil {
		return InitialReviewResult{}, fmt.Errorf("store: insert initial review: load work: %w", err)
	}
	if model.WorkStatus(status) != model.WorkStatusImplementing {
		return InitialReviewResult{}, model.ErrInvalidWorkTransition
	}
	if cert.Project != project || cert.ContractRevision != revision || cert.ContractHash != contractHash || cert.BaseSHA != baseSHA || strings.TrimSpace(cert.HeadSHA) == "" {
		return InitialReviewResult{}, model.ErrInvalidContract
	}
	type initialFindingSnapshot struct {
		id       string
		category model.FindingCategory
		status   model.FindingStatus
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,seq,category,status FROM execution_findings WHERE work_id=? ORDER BY seq`, cert.WorkID)
	if err != nil {
		return InitialReviewResult{}, err
	}
	all := map[int]initialFindingSnapshot{}
	eligible := map[int]initialFindingSnapshot{}
	maxSeq := 0
	for rows.Next() {
		var seq int
		var snapshot initialFindingSnapshot
		if err := rows.Scan(&snapshot.id, &seq, &snapshot.category, &snapshot.status); err != nil {
			_ = rows.Close()
			return InitialReviewResult{}, err
		}
		all[seq] = snapshot
		if seq > maxSeq {
			maxSeq = seq
		}
		if snapshot.status == model.FindingOpen && snapshot.category.Blocks() {
			eligible[seq] = snapshot
		}
	}
	if err := rows.Close(); err != nil {
		return InitialReviewResult{}, err
	}
	if len(in.Resolutions) != len(eligible) {
		return InitialReviewResult{}, model.ErrInvalidContract
	}
	seen := make(map[int]bool, len(in.Resolutions))
	for _, resolution := range in.Resolutions {
		_, ok := eligible[resolution.FindingSeq]
		if !ok || seen[resolution.FindingSeq] || (resolution.Status != model.FindingFixed && resolution.Status != model.FindingInvalid) || strings.TrimSpace(resolution.Evidence) == "" || (resolution.Status == model.FindingInvalid && strings.TrimSpace(resolution.Reason) == "") {
			return InitialReviewResult{}, model.ErrInvalidContract
		}
		seen[resolution.FindingSeq] = true
	}
	now := time.Now().UTC()
	for _, resolution := range in.Resolutions {
		snapshot := all[resolution.FindingSeq]
		result, err := tx.ExecContext(ctx, `UPDATE execution_findings SET status=?,resolution_reason=?,resolved_by=?,resolved_at=? WHERE id=? AND status='open'`, resolution.Status, resolution.Reason, in.By, formatTime(now), snapshot.id)
		if err != nil {
			return InitialReviewResult{}, fmt.Errorf("store: insert initial review: resolve finding %d: %w", resolution.FindingSeq, err)
		}
		if !oneRow(result) {
			return InitialReviewResult{}, model.ErrInvalidContract
		}
	}
	for i, finding := range in.Findings {
		id, idErr := uuid.NewV7()
		if idErr != nil {
			return InitialReviewResult{}, idErr
		}
		finding.ID = id.String()
		finding.WorkID = cert.WorkID
		finding.Seq = maxSeq + i + 1
		finding.Origin = model.FindingOriginReview
		finding.ReviewPhase = model.ReviewPhaseInitial
		finding.Status = model.FindingOpen
		finding.CreatedAt = now
		if _, err := tx.ExecContext(ctx, `INSERT INTO execution_findings(id,work_id,seq,category,severity,description,location,evidence,origin,review_phase,status,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, finding.ID, finding.WorkID, finding.Seq, finding.Category, finding.Severity, finding.Description, finding.Location, finding.Evidence, finding.Origin, finding.ReviewPhase, finding.Status, formatTime(finding.CreatedAt)); err != nil {
			return InitialReviewResult{}, fmt.Errorf("store: insert initial review: finding %d: %w", i+1, err)
		}
	}
	if err := insertDeliveryEvaluationTx(ctx, tx, cert, in.Checks, in.Observations); err != nil {
		return InitialReviewResult{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE execution_contracts SET status=?,updated_at=? WHERE id=? AND status=?`, model.WorkStatusVerifying, formatTime(now), cert.WorkID, model.WorkStatusImplementing)
	if err != nil {
		return InitialReviewResult{}, fmt.Errorf("store: insert initial review: transition: %w", err)
	}
	if !oneRow(result) {
		return InitialReviewResult{}, model.ErrInvalidWorkTransition
	}
	if err := insertWorkHistory(ctx, tx, cert.WorkID, model.WorkStatusImplementing, model.WorkStatusVerifying, revision, in.By, "", now); err != nil {
		return InitialReviewResult{}, fmt.Errorf("store: insert initial review: history: %w", err)
	}
	finalStatus := model.WorkStatusVerifying
	blocking := cert.Verdict != model.DeliveryVerdictPass
	for _, finding := range in.Findings {
		blocking = blocking || finding.Category.Blocks()
	}
	if blocking {
		if correctionRounds < maxCorrectionRounds {
			finalStatus = model.WorkStatusCorrecting
			correctionRounds++
		} else {
			finalStatus = model.WorkStatusEscalated
		}
		result, err = tx.ExecContext(ctx, `UPDATE execution_contracts SET status=?,correction_rounds=?,updated_at=? WHERE id=? AND status=?`, finalStatus, correctionRounds, formatTime(now), cert.WorkID, model.WorkStatusVerifying)
		if err != nil {
			return InitialReviewResult{}, fmt.Errorf("store: insert initial review: final transition: %w", err)
		}
		if !oneRow(result) {
			return InitialReviewResult{}, model.ErrInvalidWorkTransition
		}
		if err := insertWorkHistory(ctx, tx, cert.WorkID, model.WorkStatusVerifying, finalStatus, revision, in.By, "", now); err != nil {
			return InitialReviewResult{}, fmt.Errorf("store: insert initial review: final history: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return InitialReviewResult{}, err
	}
	return InitialReviewResult{Status: finalStatus, CorrectionRounds: correctionRounds}, nil
}

// InsertTargetedReview atomically resolves the initial mandate, records the new review, and ends the automatic cycle.
func (s *SDDStore) InsertTargetedReview(ctx context.Context, in TargetedReviewWrite) (InitialReviewResult, error) {
	if err := validateDeliveryEvaluation(in.Certificate, in.Checks, in.Observations); err != nil {
		return InitialReviewResult{}, err
	}
	if strings.TrimSpace(in.By) == "" || strings.TrimSpace(in.ExpectedCertificateID) == "" {
		return InitialReviewResult{}, model.ErrInvalidContract
	}
	for _, finding := range in.Findings {
		if finding == nil || !finding.Category.Valid() || !finding.Severity.Valid() || strings.TrimSpace(finding.Description) == "" || strings.TrimSpace(finding.Evidence) == "" {
			return InitialReviewResult{}, model.ErrInvalidContract
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return InitialReviewResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	cert := in.Certificate
	var status, project, contractHash, baseSHA string
	var revision, correctionRounds int
	if err := tx.QueryRowContext(ctx, `SELECT status,project,contract_revision,contract_hash,base_sha,correction_rounds FROM execution_contracts WHERE id=?`, cert.WorkID).Scan(&status, &project, &revision, &contractHash, &baseSHA, &correctionRounds); errors.Is(err, sql.ErrNoRows) {
		return InitialReviewResult{}, model.ErrWorkNotFound
	} else if err != nil {
		return InitialReviewResult{}, fmt.Errorf("store: insert targeted review: load work: %w", err)
	}
	if model.WorkStatus(status) != model.WorkStatusCorrecting {
		return InitialReviewResult{}, model.ErrInvalidWorkTransition
	}
	if cert.Project != project || cert.ContractRevision != revision || cert.ContractHash != contractHash || cert.BaseSHA != baseSHA || strings.TrimSpace(cert.HeadSHA) == "" {
		return InitialReviewResult{}, model.ErrInvalidContract
	}
	var previousID, previousHead, previousBase, previousHash string
	var previousRevision int
	if err := tx.QueryRowContext(ctx, `SELECT id,head_sha,base_sha,contract_revision,contract_hash FROM delivery_certificates WHERE work_id=? ORDER BY rowid DESC LIMIT 1`, cert.WorkID).Scan(&previousID, &previousHead, &previousBase, &previousRevision, &previousHash); err != nil {
		return InitialReviewResult{}, fmt.Errorf("store: insert targeted review: load mandate certificate: %w", err)
	}
	if previousID != in.ExpectedCertificateID || cert.HeadSHA == previousHead || previousBase != baseSHA || previousRevision != revision || previousHash != contractHash {
		return InitialReviewResult{}, model.ErrInvalidContract
	}

	type findingSnapshot struct {
		id       string
		category model.FindingCategory
		status   model.FindingStatus
		phase    model.ReviewPhase
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,seq,category,status,review_phase FROM execution_findings WHERE work_id=? ORDER BY seq`, cert.WorkID)
	if err != nil {
		return InitialReviewResult{}, err
	}
	all := map[int]findingSnapshot{}
	eligible := map[int]findingSnapshot{}
	maxSeq := 0
	for rows.Next() {
		var snapshot findingSnapshot
		var seq int
		if err := rows.Scan(&snapshot.id, &seq, &snapshot.category, &snapshot.status, &snapshot.phase); err != nil {
			_ = rows.Close()
			return InitialReviewResult{}, err
		}
		all[seq] = snapshot
		if seq > maxSeq {
			maxSeq = seq
		}
		if snapshot.status == model.FindingOpen && snapshot.phase == model.ReviewPhaseInitial && snapshot.category.Blocks() {
			eligible[seq] = snapshot
		}
	}
	if err := rows.Close(); err != nil {
		return InitialReviewResult{}, err
	}
	if len(in.Resolutions) != len(eligible) {
		return InitialReviewResult{}, model.ErrInvalidContract
	}
	seen := make(map[int]bool, len(in.Resolutions))
	for _, resolution := range in.Resolutions {
		snapshot, ok := eligible[resolution.FindingSeq]
		if !ok || seen[resolution.FindingSeq] || (resolution.Status != model.FindingFixed && resolution.Status != model.FindingInvalid) || strings.TrimSpace(resolution.Evidence) == "" || (resolution.Status == model.FindingInvalid && strings.TrimSpace(resolution.Reason) == "") {
			return InitialReviewResult{}, model.ErrInvalidContract
		}
		seen[resolution.FindingSeq] = true
		_ = snapshot
	}
	now := time.Now().UTC()
	for _, resolution := range in.Resolutions {
		snapshot := all[resolution.FindingSeq]
		result, err := tx.ExecContext(ctx, `UPDATE execution_findings SET status=?,resolution_reason=?,resolved_by=?,resolved_at=? WHERE id=? AND status='open'`, resolution.Status, resolution.Reason, in.By, formatTime(now), snapshot.id)
		if err != nil {
			return InitialReviewResult{}, fmt.Errorf("store: insert targeted review: resolve finding %d: %w", resolution.FindingSeq, err)
		}
		if !oneRow(result) {
			return InitialReviewResult{}, model.ErrInvalidContract
		}
	}
	for i, finding := range in.Findings {
		id, idErr := uuid.NewV7()
		if idErr != nil {
			return InitialReviewResult{}, idErr
		}
		finding.ID = id.String()
		finding.WorkID = cert.WorkID
		finding.Seq = maxSeq + i + 1
		finding.Origin = model.FindingOriginReview
		finding.ReviewPhase = model.ReviewPhaseTargeted
		finding.Status = model.FindingOpen
		finding.CreatedAt = now
		if _, err := tx.ExecContext(ctx, `INSERT INTO execution_findings(id,work_id,seq,category,severity,description,location,evidence,origin,review_phase,status,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, finding.ID, finding.WorkID, finding.Seq, finding.Category, finding.Severity, finding.Description, finding.Location, finding.Evidence, finding.Origin, finding.ReviewPhase, finding.Status, formatTime(now)); err != nil {
			return InitialReviewResult{}, fmt.Errorf("store: insert targeted review: finding %d: %w", finding.Seq, err)
		}
	}
	if err := insertDeliveryEvaluationTx(ctx, tx, cert, in.Checks, in.Observations); err != nil {
		return InitialReviewResult{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE execution_contracts SET status=?,updated_at=? WHERE id=? AND status=?`, model.WorkStatusTargetedVerifying, formatTime(now), cert.WorkID, model.WorkStatusCorrecting)
	if err != nil || !oneRow(result) {
		if err != nil {
			return InitialReviewResult{}, fmt.Errorf("store: insert targeted review: transition: %w", err)
		}
		return InitialReviewResult{}, model.ErrInvalidWorkTransition
	}
	if err := insertWorkHistory(ctx, tx, cert.WorkID, model.WorkStatusCorrecting, model.WorkStatusTargetedVerifying, revision, in.By, "", now); err != nil {
		return InitialReviewResult{}, fmt.Errorf("store: insert targeted review: history: %w", err)
	}
	blocking, err := countOpenBlockingFindingsTx(ctx, tx, cert.WorkID)
	if err != nil {
		return InitialReviewResult{}, err
	}
	finalStatus := model.WorkStatusTargetedVerifying
	if cert.Verdict != model.DeliveryVerdictPass || blocking > 0 {
		finalStatus = model.WorkStatusEscalated
		result, err = tx.ExecContext(ctx, `UPDATE execution_contracts SET status=?,updated_at=? WHERE id=? AND status=?`, finalStatus, formatTime(now), cert.WorkID, model.WorkStatusTargetedVerifying)
		if err != nil || !oneRow(result) {
			if err != nil {
				return InitialReviewResult{}, fmt.Errorf("store: insert targeted review: escalation: %w", err)
			}
			return InitialReviewResult{}, model.ErrInvalidWorkTransition
		}
		if err := insertWorkHistory(ctx, tx, cert.WorkID, model.WorkStatusTargetedVerifying, model.WorkStatusEscalated, revision, in.By, "", now); err != nil {
			return InitialReviewResult{}, fmt.Errorf("store: insert targeted review: escalation history: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return InitialReviewResult{}, err
	}
	return InitialReviewResult{Status: finalStatus, CorrectionRounds: correctionRounds}, nil
}

func countOpenBlockingFindingsTx(ctx context.Context, tx *sql.Tx, workID string) (int, error) {
	categories := model.BlockingFindingCategories()
	args := []any{workID}
	marks := make([]string, len(categories))
	for i, category := range categories {
		marks[i] = "?"
		args = append(args, category)
	}
	var count int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM execution_findings WHERE work_id=? AND status='open' AND category IN (`+strings.Join(marks, ",")+`)`, args...).Scan(&count)
	return count, err
}

func validateDeliveryEvaluation(cert *model.DeliveryCertificate, checks []*model.DeliveryCheck, observations []model.CriterionObservation) error {
	if cert == nil || !cert.Verdict.Valid() {
		return model.ErrInvalidContract
	}
	for _, check := range checks {
		if check == nil || !check.Status.Valid() || !check.Effect.Valid() {
			return model.ErrInvalidContract
		}
	}
	for _, observation := range observations {
		if observation.CriterionID == "" || (observation.Status != model.CriterionPass && observation.Status != model.CriterionFail && observation.Status != model.CriterionVacuous) || observation.CheckedAt.IsZero() {
			return model.ErrInvalidContract
		}
	}
	return nil
}

func insertDeliveryEvaluationTx(ctx context.Context, tx *sql.Tx, cert *model.DeliveryCertificate, checks []*model.DeliveryCheck, observations []model.CriterionObservation) error {
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	cert.ID = id.String()
	cert.CreatedAt = time.Now().UTC()
	dirty := 0
	if cert.Dirty {
		dirty = 1
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO delivery_certificates(id,project,work_id,contract_revision,contract_hash,head_sha,base_sha,verdict,dirty,evidence,mneme_version,started_at,finished_at,duration_ms,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, cert.ID, cert.Project, cert.WorkID, cert.ContractRevision, cert.ContractHash, cert.HeadSHA, cert.BaseSHA, cert.Verdict, dirty, cert.Evidence, cert.MnemeVersion, formatTime(cert.StartedAt), formatTime(cert.FinishedAt), cert.DurationMs, formatTime(cert.CreatedAt))
	if err != nil {
		return fmt.Errorf("store: insert delivery certificate: %w", err)
	}
	for i, check := range checks {
		check.CertificateID = cert.ID
		check.Seq = i + 1
		check.CreatedAt = cert.CreatedAt
		res, err := tx.ExecContext(ctx, `INSERT INTO delivery_checks(certificate_id,seq,kind,name,status,effect,detail,duration_ms,output_sha256,output_tail,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, check.CertificateID, check.Seq, check.Kind, check.Name, check.Status, check.Effect, check.Detail, check.DurationMs, check.OutputSHA256, check.OutputTail, formatTime(check.CreatedAt))
		if err != nil {
			return fmt.Errorf("store: insert delivery certificate: check %d: %w", i+1, err)
		}
		check.ID, _ = res.LastInsertId()
	}
	for i, observation := range observations {
		var current string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM execution_criteria WHERE id=? AND work_id=?`, observation.CriterionID, cert.WorkID).Scan(&current); errors.Is(err, sql.ErrNoRows) {
			return model.ErrInvalidContract
		} else if err != nil {
			return fmt.Errorf("store: insert delivery evaluation: observation %d: %w", i+1, err)
		}
		if model.CriterionStatus(current) == model.CriterionSigned {
			continue
		}
		result, err := tx.ExecContext(ctx, `UPDATE execution_criteria SET status=?,evidence=?,checked_by=?,checked_at=? WHERE id=? AND work_id=?`, observation.Status, observation.Evidence, observation.CheckedBy, formatTime(observation.CheckedAt), observation.CriterionID, cert.WorkID)
		if err != nil {
			return fmt.Errorf("store: insert delivery evaluation: observation %d: %w", i+1, err)
		}
		if !oneRow(result) {
			return model.ErrInvalidContract
		}
	}
	return nil
}

// GetLatestDeliveryCertificate returns the last inserted certificate for one work item.
func (s *SDDStore) GetLatestDeliveryCertificate(ctx context.Context, project, workID string) (*model.DeliveryCertificate, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,project,work_id,contract_revision,contract_hash,head_sha,base_sha,verdict,dirty,evidence,mneme_version,started_at,finished_at,duration_ms,created_at FROM delivery_certificates WHERE project=? AND work_id=? ORDER BY rowid DESC LIMIT 1`, project, workID)
	cert, err := scanDeliveryCertificate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, model.ErrNotFound
	}
	return cert, err
}
func scanDeliveryCertificate(row workScanner) (*model.DeliveryCertificate, error) {
	c := &model.DeliveryCertificate{}
	var verdict, started, finished, created string
	var dirty int
	if err := row.Scan(&c.ID, &c.Project, &c.WorkID, &c.ContractRevision, &c.ContractHash, &c.HeadSHA, &c.BaseSHA, &verdict, &dirty, &c.Evidence, &c.MnemeVersion, &started, &finished, &c.DurationMs, &created); err != nil {
		return nil, err
	}
	c.Verdict = model.DeliveryVerdict(verdict)
	c.Dirty = dirty != 0
	c.StartedAt, _ = parseTime(started)
	c.FinishedAt, _ = parseTime(finished)
	c.CreatedAt, _ = parseTime(created)
	return c, nil
}

// ListDeliveryChecks returns certificate checks in execution sequence.
func (s *SDDStore) ListDeliveryChecks(ctx context.Context, certificateID string) ([]model.DeliveryCheck, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,certificate_id,seq,kind,name,status,effect,detail,duration_ms,output_sha256,output_tail,created_at FROM delivery_checks WHERE certificate_id=? ORDER BY seq,rowid`, certificateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.DeliveryCheck
	for rows.Next() {
		var c model.DeliveryCheck
		var status, effect, created string
		if err := rows.Scan(&c.ID, &c.CertificateID, &c.Seq, &c.Kind, &c.Name, &status, &effect, &c.Detail, &c.DurationMs, &c.OutputSHA256, &c.OutputTail, &created); err != nil {
			return nil, err
		}
		c.Status = model.DeliveryCheckStatus(status)
		c.Effect = model.DeliveryCheckEffect(effect)
		c.CreatedAt, _ = parseTime(created)
		out = append(out, c)
	}
	return out, rows.Err()
}
