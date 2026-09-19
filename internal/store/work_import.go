package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/wirvii/mneme/internal/model"
)

// CreateWorkFromRecord atomically restores a complete git-native aggregate,
// preserving its anchor, identifiers, timestamps, current definitions and
// audit rows. It never creates delivery certificates or checks.
func (s *SDDStore) CreateWorkFromRecord(ctx context.Context, agg *model.WorkAggregate) error {
	if err := s.ValidateWorkFromRecord(agg); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: create work from record: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := markImportedWorkSource(ctx, tx, agg.Contract); err != nil {
		return err
	}
	if err := insertImportedWorkContract(ctx, tx, agg.Contract); err != nil {
		return fmt.Errorf("store: create work from record: contract: %w", err)
	}
	if err := insertImportedDefinitions(ctx, tx, agg); err != nil {
		return fmt.Errorf("store: create work from record: definitions: %w", err)
	}
	if err := mergeImportedAudit(ctx, tx, agg); err != nil {
		return fmt.Errorf("store: create work from record: audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: create work from record: commit: %w", err)
	}
	return nil
}

// UpdateWorkFromRecord replaces current definitions and merges accumulated
// audit data for the existing row identified by the same UUID and correlative.
func (s *SDDStore) UpdateWorkFromRecord(ctx context.Context, agg *model.WorkAggregate) error {
	if err := s.ValidateWorkFromRecord(agg); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: update work from record: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := markImportedWorkSource(ctx, tx, agg.Contract); err != nil {
		return err
	}
	if err := updateImportedWorkContract(ctx, tx, agg.Contract); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM execution_criteria WHERE work_id=?`, agg.Contract.ID); err != nil {
		return fmt.Errorf("store: update work from record: delete criteria: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM execution_constraints WHERE work_id=?`, agg.Contract.ID); err != nil {
		return fmt.Errorf("store: update work from record: delete constraints: %w", err)
	}
	if err := insertImportedDefinitions(ctx, tx, agg); err != nil {
		return fmt.Errorf("store: update work from record: definitions: %w", err)
	}
	if err := mergeImportedAudit(ctx, tx, agg); err != nil {
		return fmt.Errorf("store: update work from record: audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: update work from record: commit: %w", err)
	}
	return nil
}

// ValidateWorkFromRecord completes only safe generated metadata and validates
// an imported aggregate without writing it. Import previews use the same
// validation path as applied imports so their decisions cannot disagree.
func (s *SDDStore) ValidateWorkFromRecord(agg *model.WorkAggregate) error {
	if err := completeImportedWorkMetadata(agg); err != nil {
		return err
	}
	return validateImportedWork(agg)
}

func completeImportedWorkMetadata(agg *model.WorkAggregate) error {
	if agg == nil || agg.Contract == nil {
		return nil
	}
	now := time.Now().UTC()
	mint := func(target *string) error {
		if *target != "" {
			return nil
		}
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("store: import work: mint metadata: %w", err)
		}
		*target = id.String()
		return nil
	}
	if err := mint(&agg.Contract.UUID); err != nil {
		return err
	}
	if agg.Contract.CreatedAt.IsZero() {
		agg.Contract.CreatedAt = now
	}
	if agg.Contract.UpdatedAt.IsZero() {
		agg.Contract.UpdatedAt = now
	}
	for i := range agg.Criteria {
		if err := mint(&agg.Criteria[i].ID); err != nil {
			return err
		}
		if agg.Criteria[i].CreatedAt.IsZero() {
			agg.Criteria[i].CreatedAt = now
		}
	}
	for i := range agg.Constraints {
		if err := mint(&agg.Constraints[i].ID); err != nil {
			return err
		}
		if agg.Constraints[i].CreatedAt.IsZero() {
			agg.Constraints[i].CreatedAt = now
		}
	}
	for i := range agg.Findings {
		if err := mint(&agg.Findings[i].ID); err != nil {
			return err
		}
		if agg.Findings[i].CreatedAt.IsZero() {
			agg.Findings[i].CreatedAt = now
		}
	}
	for i := range agg.History {
		if err := mint(&agg.History[i].ID); err != nil {
			return err
		}
		if agg.History[i].At.IsZero() {
			agg.History[i].At = now
		}
	}
	return nil
}

func validateImportedWork(agg *model.WorkAggregate) error {
	invalid := func(reason string) error {
		return fmt.Errorf("%w: imported work: %s", model.ErrInvalidContract, reason)
	}
	if agg == nil || agg.Contract == nil {
		return invalid("aggregate required")
	}
	c := agg.Contract
	if c.ID == "" || c.UUID == "" || c.Project == "" || c.CreatedAt.IsZero() || c.UpdatedAt.IsZero() {
		return invalid("contract identity and timestamps required")
	}
	if err := c.Validate(agg.Criteria, agg.Constraints); err != nil {
		return err
	}
	if c.Status != model.WorkStatusDraft {
		if c.BaseSHA == "" || c.ContractRevision < 1 || c.ContractHash == "" {
			return invalid("locked fields required")
		}
		if c.ContractHash != model.ContractHash(*c, agg.Criteria, agg.Constraints) {
			return invalid("contract hash mismatch")
		}
	}
	if c.DevEvidence != nil && c.DevEvidence.TakenAt.IsZero() {
		return invalid("red-test evidence timestamp required")
	}
	criterionSeq := map[int]bool{}
	constraintSeq := map[int]bool{}
	ids := map[string]bool{}
	for _, criterion := range agg.Criteria {
		if criterion.ID == "" || criterion.WorkID != c.ID || criterion.Seq <= 0 || criterionSeq[criterion.Seq] || ids[criterion.ID] || !criterion.Status.Valid() || criterion.CreatedAt.IsZero() {
			return invalid("invalid criterion")
		}
		criterionSeq[criterion.Seq], ids[criterion.ID] = true, true
	}
	for _, constraint := range agg.Constraints {
		if constraint.ID == "" || constraint.WorkID != c.ID || constraint.Seq <= 0 || constraintSeq[constraint.Seq] || ids[constraint.ID] || constraint.CreatedAt.IsZero() {
			return invalid("invalid constraint")
		}
		constraintSeq[constraint.Seq], ids[constraint.ID] = true, true
	}
	findingSeq := map[int]bool{}
	for _, finding := range agg.Findings {
		if finding.ID == "" || finding.WorkID != c.ID || finding.Seq <= 0 || findingSeq[finding.Seq] || ids[finding.ID] || !finding.Category.Valid() || !finding.Severity.Valid() || !finding.Origin.Valid() || !finding.ReviewPhase.Valid() || !finding.Status.Valid() || strings.TrimSpace(finding.Description) == "" || finding.CreatedAt.IsZero() {
			return invalid("invalid finding")
		}
		findingSeq[finding.Seq], ids[finding.ID] = true, true
	}
	for _, history := range agg.History {
		if history.ID == "" || history.WorkID != c.ID || ids[history.ID] || !history.FromStatus.Valid() || !history.ToStatus.Valid() || !model.CanTransitionWork(history.FromStatus, history.ToStatus) || history.ContractRevision < 0 || history.ContractRevision > c.ContractRevision || history.At.IsZero() {
			return invalid("invalid history")
		}
		ids[history.ID] = true
	}
	return nil
}

func markImportedWorkSource(ctx context.Context, tx *sql.Tx, c *model.WorkContract) error {
	if c.SourceType != model.WorkSourceSpec {
		return nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE specs SET execution_model=? WHERE id=? AND project=?`, model.ExecutionModelDeliveryV2, c.SourceID, c.Project)
	if err != nil {
		return fmt.Errorf("store: import work: mark source spec: %w", err)
	}
	if !oneRow(result) {
		return model.ErrSpecNotFound
	}
	return nil
}

func importedEvidence(c *model.WorkContract) (command string, exit int, tail, sha, at string, err error) {
	if c.DevEvidence == nil {
		return "", 0, "", "", "", nil
	}
	raw, err := json.Marshal(c.DevEvidence.Command)
	if err != nil {
		return "", 0, "", "", "", err
	}
	return string(raw), c.DevEvidence.ExitCode, c.DevEvidence.OutputTail, c.DevEvidence.CommitSHA, formatTime(c.DevEvidence.TakenAt), nil
}

func importedContractValues(c *model.WorkContract) ([]any, error) {
	scope, err := json.Marshal(c.Scope)
	if err != nil {
		return nil, err
	}
	verification, err := json.Marshal(c.Verification)
	if err != nil {
		return nil, err
	}
	command, evidenceExit, evidenceTail, evidenceSHA, evidenceAt, err := importedEvidence(c)
	if err != nil {
		return nil, err
	}
	locked, completed := "", ""
	if c.LockedAt != nil {
		locked = formatTime(*c.LockedAt)
	}
	if c.CompletedAt != nil {
		completed = formatTime(*c.CompletedAt)
	}
	return []any{c.ID, c.UUID, c.Project, c.SourceType, c.SourceID, c.Status, c.Goal, string(scope), string(verification), c.DevelopmentMethod, c.BaseSHA, c.ContractRevision, c.ContractHash, c.CorrectionRounds, c.MaxCorrectionRounds, command, evidenceExit, evidenceTail, evidenceSHA, evidenceAt, c.CreatedBy, formatTime(c.CreatedAt), formatTime(c.UpdatedAt), locked, completed}, nil
}

func insertImportedWorkContract(ctx context.Context, tx *sql.Tx, c *model.WorkContract) error {
	values, err := importedContractValues(c)
	if err != nil {
		return err
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")
	_, err = tx.ExecContext(ctx, `INSERT INTO execution_contracts(`+workColumns+`) VALUES(`+marks+`)`, values...)
	return err
}

func updateImportedWorkContract(ctx context.Context, tx *sql.Tx, c *model.WorkContract) error {
	values, err := importedContractValues(c)
	if err != nil {
		return err
	}
	// ID and UUID identify the row and are not assigned in the SET list.
	setValues := append([]any(nil), values[2:]...)
	setValues = append(setValues, c.UUID, c.ID)
	result, err := tx.ExecContext(ctx, `UPDATE execution_contracts SET project=?,source_type=?,source_id=?,status=?,goal=?,scope_json=?,verification_json=?,development_method=?,base_sha=?,contract_revision=?,contract_hash=?,correction_rounds=?,max_correction_rounds=?,dev_evidence_command=?,dev_evidence_exit=?,dev_evidence_tail=?,dev_evidence_sha=?,dev_evidence_at=?,created_by=?,created_at=?,updated_at=?,locked_at=?,completed_at=? WHERE uuid=? AND id=?`, setValues...)
	if err != nil {
		return fmt.Errorf("store: update work from record: contract: %w", err)
	}
	if !oneRow(result) {
		return model.ErrWorkNotFound
	}
	return nil
}

func insertImportedDefinitions(ctx context.Context, tx *sql.Tx, agg *model.WorkAggregate) error {
	for _, criterion := range agg.Criteria {
		checkedAt := ""
		if criterion.CheckedAt != nil {
			checkedAt = formatTime(*criterion.CheckedAt)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO execution_criteria(id,work_id,seq,criterion_key,declaration,status,evidence,checked_by,checked_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, criterion.ID, criterion.WorkID, criterion.Seq, criterion.Key, criterion.Declaration, criterion.Status, criterion.Evidence, criterion.CheckedBy, checkedAt, formatTime(criterion.CreatedAt)); err != nil {
			return err
		}
	}
	for _, constraint := range agg.Constraints {
		if _, err := tx.ExecContext(ctx, `INSERT INTO execution_constraints(id,work_id,seq,constraint_key,text,source,created_at) VALUES(?,?,?,?,?,?,?)`, constraint.ID, constraint.WorkID, constraint.Seq, constraint.Key, constraint.Text, constraint.Source, formatTime(constraint.CreatedAt)); err != nil {
			return err
		}
	}
	return nil
}

func mergeImportedAudit(ctx context.Context, tx *sql.Tx, agg *model.WorkAggregate) error {
	for _, finding := range agg.Findings {
		resolvedAt := ""
		if finding.ResolvedAt != nil {
			resolvedAt = formatTime(*finding.ResolvedAt)
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO execution_findings(id,work_id,seq,category,severity,description,location,evidence,origin,review_phase,status,backlog_id,resolution_reason,resolved_by,created_at,resolved_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET seq=excluded.seq,category=excluded.category,severity=excluded.severity,description=excluded.description,location=excluded.location,evidence=excluded.evidence,origin=excluded.origin,review_phase=excluded.review_phase,status=excluded.status,backlog_id=excluded.backlog_id,resolution_reason=excluded.resolution_reason,resolved_by=excluded.resolved_by,created_at=excluded.created_at,resolved_at=excluded.resolved_at WHERE execution_findings.work_id=excluded.work_id`, finding.ID, finding.WorkID, finding.Seq, finding.Category, finding.Severity, finding.Description, finding.Location, finding.Evidence, finding.Origin, finding.ReviewPhase, finding.Status, finding.BacklogID, finding.ResolutionReason, finding.ResolvedBy, formatTime(finding.CreatedAt), resolvedAt)
		if err != nil {
			return err
		}
		if !oneRow(result) {
			return model.ErrInvalidContract
		}
	}
	for _, history := range agg.History {
		result, err := tx.ExecContext(ctx, `INSERT INTO execution_history(id,work_id,from_status,to_status,contract_revision,by,reason,at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET from_status=excluded.from_status,to_status=excluded.to_status,contract_revision=excluded.contract_revision,by=excluded.by,reason=excluded.reason,at=excluded.at WHERE execution_history.work_id=excluded.work_id`, history.ID, history.WorkID, history.FromStatus, history.ToStatus, history.ContractRevision, history.By, history.Reason, formatTime(history.At))
		if err != nil {
			return err
		}
		if !oneRow(result) {
			return model.ErrInvalidContract
		}
	}
	return nil
}
