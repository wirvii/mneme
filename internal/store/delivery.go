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
	if status == model.FindingAccepted && model.FindingCategory(category).Blocks() {
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

// InsertDeliveryCertificate atomically writes a certificate and its ordered checks.
func (s *SDDStore) InsertDeliveryCertificate(ctx context.Context, cert *model.DeliveryCertificate, checks []*model.DeliveryCheck) error {
	if !cert.Verdict.Valid() {
		return model.ErrInvalidContract
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	cert.ID = id.String()
	cert.CreatedAt = time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	dirty := 0
	if cert.Dirty {
		dirty = 1
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO delivery_certificates(id,project,work_id,contract_revision,contract_hash,head_sha,base_sha,verdict,dirty,evidence,mneme_version,started_at,finished_at,duration_ms,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, cert.ID, cert.Project, cert.WorkID, cert.ContractRevision, cert.ContractHash, cert.HeadSHA, cert.BaseSHA, cert.Verdict, dirty, cert.Evidence, cert.MnemeVersion, formatTime(cert.StartedAt), formatTime(cert.FinishedAt), cert.DurationMs, formatTime(cert.CreatedAt))
	if err != nil {
		return fmt.Errorf("store: insert delivery certificate: %w", err)
	}
	for i, check := range checks {
		if !check.Status.Valid() || !check.Effect.Valid() {
			return model.ErrInvalidContract
		}
		check.CertificateID = cert.ID
		check.Seq = i + 1
		check.CreatedAt = cert.CreatedAt
		res, err := tx.ExecContext(ctx, `INSERT INTO delivery_checks(certificate_id,seq,kind,name,status,effect,detail,duration_ms,output_sha256,output_tail,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, check.CertificateID, check.Seq, check.Kind, check.Name, check.Status, check.Effect, check.Detail, check.DurationMs, check.OutputSHA256, check.OutputTail, formatTime(check.CreatedAt))
		if err != nil {
			return fmt.Errorf("store: insert delivery certificate: check %d: %w", i+1, err)
		}
		check.ID, _ = res.LastInsertId()
	}
	return tx.Commit()
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
