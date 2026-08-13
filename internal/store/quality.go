// Package store — this file implements the CRUD layer for quality
// certificates and their checks (SPEC-115 EPIC-calidad S1), on the same
// SDDStore and the same project database as specs and lane_audits.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/wirvii/mneme/internal/model"
)

// InsertCertificate writes cert plus its checks inside a single transaction:
// cert.ID is generated here (UUIDv7), each check's CertificateID and Seq are
// assigned from its position in the slice (1-based, matching D6's "execution
// order" contract), and cert.CreatedAt is set to the insert time. Both the
// certificate and every check land atomically — a certificate can never
// exist with a partial set of checks.
func (s *SDDStore) InsertCertificate(ctx context.Context, cert *model.QualityCertificate, checks []*model.QualityCheck) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("store: insert certificate: gen id: %w", err)
	}
	cert.ID = id.String()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	cert.CreatedAt, _ = parseTime(now)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: insert certificate: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	dirty := 0
	if cert.Dirty {
		dirty = 1
	}

	const certQ = `
		INSERT INTO quality_certificates
			(id, project, spec_id, head_sha, base_sha, constitution_hash, schema_version,
			 verdict, dirty, mneme_version, started_at, finished_at, duration_ms, created_at)
		VALUES
			(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err = tx.ExecContext(ctx, certQ,
		cert.ID, cert.Project, cert.SpecID, cert.HeadSHA, cert.BaseSHA, cert.ConstitutionHash,
		cert.SchemaVersion, string(cert.Verdict), dirty, cert.MnemeVersion,
		cert.StartedAt.UTC().Format(time.RFC3339Nano), cert.FinishedAt.UTC().Format(time.RFC3339Nano),
		cert.DurationMs, now,
	)
	if err != nil {
		return fmt.Errorf("store: insert certificate: %w", err)
	}

	const checkQ = `
		INSERT INTO quality_checks
			(certificate_id, seq, kind, name, status, exit_code, duration_ms,
			 output_sha256, output_bytes, output_tail, summary, detail,
			 acked_by, acked_at, justification, created_at)
		VALUES
			(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	for i, chk := range checks {
		chk.CertificateID = cert.ID
		chk.Seq = i + 1
		chk.CreatedAt, _ = parseTime(now)

		_, err = tx.ExecContext(ctx, checkQ,
			chk.CertificateID, chk.Seq, chk.Kind, chk.Name, chk.Status, chk.ExitCode, chk.DurationMs,
			chk.OutputSHA256, chk.OutputBytes, chk.OutputTail, chk.Summary, chk.Detail,
			chk.AckedBy, "", chk.Justification, now,
		)
		if err != nil {
			return fmt.Errorf("store: insert certificate: insert check %d: %w", chk.Seq, err)
		}
	}

	return tx.Commit()
}

// GetLatestCertificate returns the most recently created certificate for
// (project, specID). Returns model.ErrCertificateNotFound when none exists.
func (s *SDDStore) GetLatestCertificate(ctx context.Context, project, specID string) (*model.QualityCertificate, error) {
	const q = `
		SELECT id, project, spec_id, head_sha, base_sha, constitution_hash, schema_version,
		       verdict, dirty, mneme_version, started_at, finished_at, duration_ms, created_at
		FROM quality_certificates
		WHERE project = ? AND spec_id = ?
		ORDER BY created_at DESC LIMIT 1`

	row := s.db.QueryRowContext(ctx, q, project, specID)
	cert, err := scanQualityCertificate(row)
	if err == sql.ErrNoRows {
		return nil, model.ErrCertificateNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get latest certificate: %w", err)
	}
	return cert, nil
}

// GetCertificate returns a certificate by its ID. Returns
// model.ErrCertificateNotFound when no matching row exists.
func (s *SDDStore) GetCertificate(ctx context.Context, id string) (*model.QualityCertificate, error) {
	const q = `
		SELECT id, project, spec_id, head_sha, base_sha, constitution_hash, schema_version,
		       verdict, dirty, mneme_version, started_at, finished_at, duration_ms, created_at
		FROM quality_certificates
		WHERE id = ?`

	row := s.db.QueryRowContext(ctx, q, id)
	cert, err := scanQualityCertificate(row)
	if err == sql.ErrNoRows {
		return nil, model.ErrCertificateNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get certificate: %w", err)
	}
	return cert, nil
}

// ListChecks returns every check of certificateID, ordered by seq ascending
// — the exact execution order D6 requires.
func (s *SDDStore) ListChecks(ctx context.Context, certificateID string) ([]*model.QualityCheck, error) {
	const q = `
		SELECT id, certificate_id, seq, kind, name, status, exit_code, duration_ms,
		       output_sha256, output_bytes, output_tail, summary, detail,
		       acked_by, acked_at, justification, created_at
		FROM quality_checks
		WHERE certificate_id = ?
		ORDER BY seq ASC`

	rows, err := s.db.QueryContext(ctx, q, certificateID)
	if err != nil {
		return nil, fmt.Errorf("store: list checks: %w", err)
	}
	defer rows.Close()

	var checks []*model.QualityCheck
	for rows.Next() {
		chk, err := scanQualityCheckRow(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list checks: scan: %w", err)
		}
		checks = append(checks, chk)
	}
	return checks, rows.Err()
}

// AckCheck converts the "finding" at (certificateID, seq) into "acked",
// recording by/justification/acked_at, then RECALCULATES and persists the
// certificate's verdict from ALL of its checks in the same transaction
// (D10) — this is what keeps SpecAdvance's usability check a cheap SELECT
// instead of ever having to recompute the verdict itself.
//
// Returns model.ErrCertificateNotFound when (certificateID, seq) does not
// identify an existing "finding" row (either the row does not exist, or it
// is not currently a finding).
func (s *SDDStore) AckCheck(ctx context.Context, certificateID string, seq int, by, justification string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: ack check: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC().Format(time.RFC3339Nano)

	res, err := tx.ExecContext(ctx,
		`UPDATE quality_checks SET status = 'acked', acked_by = ?, acked_at = ?, justification = ?
		 WHERE certificate_id = ? AND seq = ? AND status = 'finding'`,
		by, now, justification, certificateID, seq,
	)
	if err != nil {
		return fmt.Errorf("store: ack check: update: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: ack check: rows affected: %w", err)
	}
	if n == 0 {
		return model.ErrCertificateNotFound
	}

	rows, err := tx.QueryContext(ctx, `SELECT status FROM quality_checks WHERE certificate_id = ?`, certificateID)
	if err != nil {
		return fmt.Errorf("store: ack check: read statuses: %w", err)
	}
	var any, hasFail, hasFinding bool
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			rows.Close()
			return fmt.Errorf("store: ack check: scan status: %w", err)
		}
		any = true
		switch status {
		case "fail":
			hasFail = true
		case "finding":
			hasFinding = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("store: ack check: iterate statuses: %w", err)
	}
	rows.Close()

	verdict := "pass"
	switch {
	case !any || hasFail:
		verdict = "fail"
	case hasFinding:
		verdict = "findings"
	}

	if _, err := tx.ExecContext(ctx, `UPDATE quality_certificates SET verdict = ? WHERE id = ?`, verdict, certificateID); err != nil {
		return fmt.Errorf("store: ack check: update verdict: %w", err)
	}

	return tx.Commit()
}

// scanQualityCertificate scans a single *sql.Row into a QualityCertificate.
func scanQualityCertificate(row *sql.Row) (*model.QualityCertificate, error) {
	cert := &model.QualityCertificate{}
	var verdict string
	var dirty int
	var startedStr, finishedStr, createdStr string

	err := row.Scan(
		&cert.ID, &cert.Project, &cert.SpecID, &cert.HeadSHA, &cert.BaseSHA, &cert.ConstitutionHash,
		&cert.SchemaVersion, &verdict, &dirty, &cert.MnemeVersion,
		&startedStr, &finishedStr, &cert.DurationMs, &createdStr,
	)
	if err != nil {
		return nil, err
	}
	cert.Verdict = model.QualityVerdict(verdict)
	cert.Dirty = dirty == 1

	var parseErr error
	cert.StartedAt, parseErr = parseTime(startedStr)
	if parseErr != nil {
		return nil, fmt.Errorf("parse started_at: %w", parseErr)
	}
	cert.FinishedAt, parseErr = parseTime(finishedStr)
	if parseErr != nil {
		return nil, fmt.Errorf("parse finished_at: %w", parseErr)
	}
	cert.CreatedAt, parseErr = parseTime(createdStr)
	if parseErr != nil {
		return nil, fmt.Errorf("parse created_at: %w", parseErr)
	}
	return cert, nil
}

// qualityCheckScanner is satisfied by both *sql.Row and *sql.Rows — letting
// scanQualityCheckRow serve ListChecks' row-by-row loop without a second,
// duplicated copy of the same Scan call.
type qualityCheckScanner interface {
	Scan(dest ...any) error
}

// scanQualityCheckRow scans one row into a QualityCheck.
func scanQualityCheckRow(row qualityCheckScanner) (*model.QualityCheck, error) {
	chk := &model.QualityCheck{}
	var ackedAtStr, createdStr string

	err := row.Scan(
		&chk.ID, &chk.CertificateID, &chk.Seq, &chk.Kind, &chk.Name, &chk.Status,
		&chk.ExitCode, &chk.DurationMs, &chk.OutputSHA256, &chk.OutputBytes, &chk.OutputTail,
		&chk.Summary, &chk.Detail, &chk.AckedBy, &ackedAtStr, &chk.Justification, &createdStr,
	)
	if err != nil {
		return nil, err
	}

	if ackedAtStr != "" {
		t, err := parseTime(ackedAtStr)
		if err != nil {
			return nil, fmt.Errorf("parse acked_at: %w", err)
		}
		chk.AckedAt = &t
	}

	createdAt, err := parseTime(createdStr)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	chk.CreatedAt = createdAt
	return chk, nil
}
