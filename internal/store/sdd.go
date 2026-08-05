// Package store implements the data access layer for mneme using SQLite.
// This file provides the SDDStore which handles all SDD (Spec-Driven Development)
// persistence: backlog items, specs, spec history, and spec pushbacks.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/model"
)

// SDDStore provides CRUD and query operations for the SDD engine:
// backlog items, specs, spec history, and spec pushbacks.
// All operations use real SQL against SQLite — no ORM, no mocks.
type SDDStore struct {
	db *db.DB
}

// NewSDDStore constructs an SDDStore backed by the given database.
// The caller retains ownership of database and is responsible for closing it.
func NewSDDStore(database *db.DB) *SDDStore {
	return &SDDStore{db: database}
}

// backlogListOrder is the TOTAL order for backlog listings.
//
// The CASE replicates model.Priority.Rank() (critical=0, high=1, medium=2,
// low=3) because priority is a TEXT column and ordering it directly yields
// the lexicographic order 'critical' < 'high' < 'low' < 'medium' — i.e. low
// BEFORE medium (SPEC-109 D20). With 139 active items and 72 in medium, a
// LIMIT applied over the lexicographic order could leave the entire medium
// bucket outside the window.
//
// created_at (TEXT ISO-8601, whose lexicographic order IS chronological) and
// id close the tie-break: position is 0 on every item, so without them the
// order is partial and a LIMIT would be a lottery, not a window (D7).
const backlogListOrder = ` ORDER BY
	CASE priority
	    WHEN 'critical' THEN 0
	    WHEN 'high'     THEN 1
	    WHEN 'medium'   THEN 2
	    WHEN 'low'      THEN 3
	    ELSE 99
	END ASC,
	position ASC, created_at ASC, id ASC`

// specListOrder is the TOTAL order for spec listings. created_at is not
// unique; id closes the tie-break (D7).
const specListOrder = ` ORDER BY created_at ASC, id ASC`

// backlogListWhere / backlogListWhereStatus are the ONLY definition of the
// backlog listing predicate. The COUNT and the page query consume the SAME
// variable and the SAME args slice, so they cannot diverge: the symmetry is
// structural, not a convention a future editor could break by accident. If
// they diverge, Total lies — and a Total that lies is worse than no Total at
// all. Precedent and same requirement as sessionWorkWhere (store/session.go)
// and HardDeleteBySource ("MUST stay byte-identical").
const backlogListWhere = ` WHERE project = ?`
const backlogListWhereStatus = backlogListWhere + ` AND status = ?`

// backlogListSelect / backlogCountSelect share backlogListWhere so the page
// and the COUNT can never select from a different row set.
const backlogListSelect = `
	SELECT id, title, description, status, priority, project,
	       COALESCE(spec_id, ''), archive_reason, position, lane, scope,
	       created_at, updated_at
	FROM backlog_items`

const backlogCountSelect = `SELECT COUNT(*) FROM backlog_items`

// specListWhere / specListWhereStatus mirror backlogListWhere for specs (D6).
const specListWhere = ` WHERE project = ?`
const specListWhereStatus = specListWhere + ` AND status = ?`

const specListSelect = `
	SELECT id, title, status, project, COALESCE(backlog_id, ''),
	       lane, scope, COALESCE(base_sha, ''), assigned_agents, files_changed,
	       created_at, updated_at
	FROM specs`

const specCountSelect = `SELECT COUNT(*) FROM specs`

// --- BACKLOG OPERATIONS ---

// NextBacklogID returns the next sequential backlog ID for the project.
// Format: "BL-NNN" where NNN is zero-padded to 3 digits.
// Uses the maximum existing ID to avoid collisions when items have been archived.
func (s *SDDStore) NextBacklogID(ctx context.Context, project string) (string, error) {
	const q = `SELECT id FROM backlog_items WHERE project = ? ORDER BY id DESC LIMIT 1`
	var lastID string
	err := s.db.QueryRowContext(ctx, q, project).Scan(&lastID)
	if err != nil && err != sql.ErrNoRows {
		return "", fmt.Errorf("store: next backlog id: %w", err)
	}
	if lastID == "" {
		return "BL-001", nil
	}
	var n int
	if _, err := fmt.Sscanf(lastID, "BL-%d", &n); err != nil {
		return "", fmt.Errorf("store: next backlog id: parse %q: %w", lastID, err)
	}
	return fmt.Sprintf("BL-%03d", n+1), nil
}

// CreateBacklogItem inserts a new backlog item. The item's ID must be pre-set
// by the caller (typically via NextBacklogID).
func (s *SDDStore) CreateBacklogItem(ctx context.Context, item *model.BacklogItem) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	item.CreatedAt = time.Now().UTC()
	item.UpdatedAt = item.CreatedAt

	const q = `
		INSERT INTO backlog_items
			(id, title, description, status, priority, project, spec_id, archive_reason, position, lane, scope, created_at, updated_at)
		VALUES
			(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	specID := toNullString(item.SpecID)
	_, err := s.db.ExecContext(ctx, q,
		item.ID, item.Title, item.Description,
		string(item.Status), string(item.Priority),
		item.Project, specID, item.ArchiveReason,
		item.Position, string(item.Lane), item.Scope, now, now,
	)
	if err != nil {
		return fmt.Errorf("store: create backlog item: %w", err)
	}
	return nil
}

// GetBacklogItem retrieves a backlog item by ID.
// Returns model.ErrBacklogNotFound when no matching item exists.
func (s *SDDStore) GetBacklogItem(ctx context.Context, id string) (*model.BacklogItem, error) {
	const q = `
		SELECT id, title, description, status, priority, project,
		       COALESCE(spec_id, ''), archive_reason, position, lane, scope, created_at, updated_at
		FROM backlog_items WHERE id = ?`

	row := s.db.QueryRowContext(ctx, q, id)
	item, err := scanBacklogItem(row)
	if err == sql.ErrNoRows {
		return nil, model.ErrBacklogNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get backlog item: %w", err)
	}
	return item, nil
}

// ListBacklogItems returns items filtered by project and optionally by
// status, ordered by priority rank (ascending, via backlogListOrder — NOT
// the lexicographic order of the priority column, which used to disagree
// with this godoc: SPEC-109 D20), then position, then a deterministic
// created_at/id tie-break (D7). Pass an empty status to list all statuses
// for the project.
//
// limit <= 0 means no window — every matching row is returned (the CLI's
// path). limit > 0 caps the page via SQL LIMIT; the service layer is
// responsible for applying model.ListMaxLimit (D5), this method applies
// whatever limit it is given verbatim.
//
// The second return value is total: the number of matches BEFORE limit was
// applied, computed by a COUNT query that shares the exact same where/args
// as the page query (D6) — they cannot diverge, so total can never lie about
// what a limit=0 call would have returned.
func (s *SDDStore) ListBacklogItems(ctx context.Context, project string, status model.BacklogStatus, limit int) ([]*model.BacklogItem, int, error) {
	where := backlogListWhere
	args := []any{project}
	if status != "" {
		where = backlogListWhereStatus
		args = append(args, string(status))
	}

	var total int
	if err := s.db.QueryRowContext(ctx, backlogCountSelect+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: count backlog items: %w", err)
	}

	q := backlogListSelect + where + backlogListOrder
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("store: list backlog items: %w", err)
	}
	defer rows.Close()

	items, err := collectBacklogItems(rows)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// UpdateBacklogItem updates the mutable fields of a backlog item.
// The updated_at timestamp is set to the current UTC time.
func (s *SDDStore) UpdateBacklogItem(ctx context.Context, item *model.BacklogItem) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	item.UpdatedAt = time.Now().UTC()

	const q = `
		UPDATE backlog_items
		SET title = ?, description = ?, status = ?, priority = ?,
		    spec_id = ?, archive_reason = ?, position = ?, lane = ?, scope = ?, updated_at = ?
		WHERE id = ?`

	specID := toNullString(item.SpecID)
	res, err := s.db.ExecContext(ctx, q,
		item.Title, item.Description, string(item.Status), string(item.Priority),
		specID, item.ArchiveReason, item.Position, string(item.Lane), item.Scope, now,
		item.ID,
	)
	if err != nil {
		return fmt.Errorf("store: update backlog item: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update backlog item: rows affected: %w", err)
	}
	if n == 0 {
		return model.ErrBacklogNotFound
	}
	return nil
}

// BacklogCounts returns the number of backlog items per status for a project.
func (s *SDDStore) BacklogCounts(ctx context.Context, project string) (map[model.BacklogStatus]int, error) {
	const q = `SELECT status, COUNT(*) FROM backlog_items WHERE project = ? GROUP BY status`
	rows, err := s.db.QueryContext(ctx, q, project)
	if err != nil {
		return nil, fmt.Errorf("store: backlog counts: %w", err)
	}
	defer rows.Close()

	counts := make(map[model.BacklogStatus]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("store: backlog counts: scan: %w", err)
		}
		counts[model.BacklogStatus(status)] = count
	}
	return counts, rows.Err()
}

// --- SPEC OPERATIONS ---

// NextSpecID returns the next sequential spec ID for the given project.
// Format: "SPEC-NNN" where NNN is zero-padded to 3 digits.
//
// IDs are per-project: two different projects can each have a SPEC-001 without
// conflict. This is enforced at the schema level by the composite primary key
// (project, id) introduced in migration 005.
func (s *SDDStore) NextSpecID(ctx context.Context, project string) (string, error) {
	const q = `SELECT id FROM specs WHERE project = ? ORDER BY id DESC LIMIT 1`
	var lastID string
	err := s.db.QueryRowContext(ctx, q, project).Scan(&lastID)
	if err != nil && err != sql.ErrNoRows {
		return "", fmt.Errorf("store: next spec id: %w", err)
	}
	if lastID == "" {
		return "SPEC-001", nil
	}
	var n int
	if _, err := fmt.Sscanf(lastID, "SPEC-%d", &n); err != nil {
		return "", fmt.Errorf("store: next spec id: parse %q: %w", lastID, err)
	}
	return fmt.Sprintf("SPEC-%03d", n+1), nil
}

// CreateSpec inserts a new spec. The spec's ID must be pre-set by the caller
// (typically via NextSpecID). Status must be set before calling.
//
// The primary key is the composite (project, id) pair (migration 005). The
// same spec ID may exist in multiple projects without conflict.
func (s *SDDStore) CreateSpec(ctx context.Context, spec *model.Spec) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	spec.CreatedAt = time.Now().UTC()
	spec.UpdatedAt = spec.CreatedAt

	agents, err := marshalStringSlice(spec.AssignedAgents)
	if err != nil {
		return fmt.Errorf("store: create spec: marshal assigned_agents: %w", err)
	}
	files, err := marshalStringSlice(spec.FilesChanged)
	if err != nil {
		return fmt.Errorf("store: create spec: marshal files_changed: %w", err)
	}

	const q = `
		INSERT INTO specs
			(id, title, status, project, backlog_id, lane, scope, base_sha, assigned_agents, files_changed, created_at, updated_at)
		VALUES
			(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	backlogID := toNullString(spec.BacklogID)
	_, err = s.db.ExecContext(ctx, q,
		spec.ID, spec.Title, string(spec.Status), spec.Project,
		backlogID, string(spec.Lane), spec.Scope, spec.BaseSHA, agents, files, now, now,
	)
	if err != nil {
		return fmt.Errorf("store: create spec: %w", err)
	}
	return nil
}

// GetSpec retrieves a spec by ID.
// Returns model.ErrSpecNotFound when no matching spec exists.
func (s *SDDStore) GetSpec(ctx context.Context, id string) (*model.Spec, error) {
	const q = `
		SELECT id, title, status, project, COALESCE(backlog_id, ''),
		       lane, scope, COALESCE(base_sha, ''), assigned_agents, files_changed, created_at, updated_at
		FROM specs WHERE id = ?`

	row := s.db.QueryRowContext(ctx, q, id)
	spec, err := scanSpec(row)
	if err == sql.ErrNoRows {
		return nil, model.ErrSpecNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get spec: %w", err)
	}
	return spec, nil
}

// ListSpecs returns specs filtered by project and optionally by status,
// ordered by created_at then a deterministic id tie-break (D7 — created_at
// alone is not unique). Pass an empty status to list all statuses for the
// project.
//
// limit <= 0 means no window (every matching row is returned); limit > 0
// caps the page via SQL LIMIT. The second return value, total, is the number
// of matches BEFORE limit was applied, computed by a COUNT query sharing the
// exact same where/args as the page query (D6) so it cannot diverge.
func (s *SDDStore) ListSpecs(ctx context.Context, project string, status model.SpecStatus, limit int) ([]*model.Spec, int, error) {
	where := specListWhere
	args := []any{project}
	if status != "" {
		where = specListWhereStatus
		args = append(args, string(status))
	}

	var total int
	if err := s.db.QueryRowContext(ctx, specCountSelect+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: count specs: %w", err)
	}

	q := specListSelect + where + specListOrder
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("store: list specs: %w", err)
	}
	defer rows.Close()

	specs, err := collectSpecs(rows)
	if err != nil {
		return nil, 0, err
	}
	return specs, total, nil
}

// UpdateSpecStatus changes the status of a spec and records the transition
// in spec_history. Both operations run in a single transaction to ensure
// consistency. An optimistic check verifies the current status matches `from`
// before updating — if it does not match, ErrInvalidTransition is returned.
func (s *SDDStore) UpdateSpecStatus(ctx context.Context, specID string, from, to model.SpecStatus, by, reason string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: update spec status: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Verify current status matches expected 'from' (optimistic lock).
	var currentStatus string
	err = tx.QueryRowContext(ctx, `SELECT status FROM specs WHERE id = ?`, specID).Scan(&currentStatus)
	if err != nil {
		if err == sql.ErrNoRows {
			return model.ErrSpecNotFound
		}
		return fmt.Errorf("store: update spec status: read current: %w", err)
	}
	if model.SpecStatus(currentStatus) != from {
		return fmt.Errorf("store: update spec status: expected %s but found %s: %w",
			from, currentStatus, model.ErrInvalidTransition)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)

	_, err = tx.ExecContext(ctx,
		`UPDATE specs SET status = ?, updated_at = ? WHERE id = ?`,
		string(to), now, specID)
	if err != nil {
		return fmt.Errorf("store: update spec status: update: %w", err)
	}

	historyID, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("store: update spec status: gen history id: %w", err)
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO spec_history (id, spec_id, from_status, to_status, by, reason, at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		historyID.String(), specID, string(from), string(to), by, reason, now)
	if err != nil {
		return fmt.Errorf("store: update spec status: insert history: %w", err)
	}

	return tx.Commit()
}

// UpdateSpecFields updates the mutable non-status fields of a spec
// (title, assigned_agents, files_changed). Use UpdateSpecStatus for status transitions.
func (s *SDDStore) UpdateSpecFields(ctx context.Context, spec *model.Spec) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	spec.UpdatedAt = time.Now().UTC()

	agents, err := marshalStringSlice(spec.AssignedAgents)
	if err != nil {
		return fmt.Errorf("store: update spec fields: marshal assigned_agents: %w", err)
	}
	files, err := marshalStringSlice(spec.FilesChanged)
	if err != nil {
		return fmt.Errorf("store: update spec fields: marshal files_changed: %w", err)
	}

	const q = `
		UPDATE specs
		SET title = ?, assigned_agents = ?, files_changed = ?, updated_at = ?
		WHERE id = ?`

	res, err := s.db.ExecContext(ctx, q, spec.Title, agents, files, now, spec.ID)
	if err != nil {
		return fmt.Errorf("store: update spec fields: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update spec fields: rows affected: %w", err)
	}
	if n == 0 {
		return model.ErrSpecNotFound
	}
	return nil
}

// UpdateSpecLaneScope updates only the lane and scope of a spec. This is used
// by LaneReclassify to change the lane without triggering a status transition.
// Lane immutability rules must be enforced by the caller before this is invoked.
func (s *SDDStore) UpdateSpecLaneScope(ctx context.Context, specID string, lane model.Lane, scope string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)

	const q = `UPDATE specs SET lane = ?, scope = ?, updated_at = ? WHERE id = ?`
	res, err := s.db.ExecContext(ctx, q, string(lane), scope, now, specID)
	if err != nil {
		return fmt.Errorf("store: update spec lane scope: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update spec lane scope: rows affected: %w", err)
	}
	if n == 0 {
		return model.ErrSpecNotFound
	}
	return nil
}

// UpdateSpecBaseSHA sets the base_sha column for a spec. Called when the spec
// enters implementing status to bind it to the current HEAD commit. This SHA
// is later used by the lane auditor to produce a per-spec diff.
// Returns ErrSpecNotFound when the spec does not exist.
func (s *SDDStore) UpdateSpecBaseSHA(ctx context.Context, specID, sha string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)

	const q = `UPDATE specs SET base_sha = ?, updated_at = ? WHERE id = ?`
	res, err := s.db.ExecContext(ctx, q, sha, now, specID)
	if err != nil {
		return fmt.Errorf("store: update spec base sha: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update spec base sha: rows affected: %w", err)
	}
	if n == 0 {
		return model.ErrSpecNotFound
	}
	return nil
}

// InsertLaneAudit writes a structured audit record to the lane_audits table.
// One row is inserted per auditor run — both passes and failures — so that
// LaneStatus can read the latest outcome without parsing spec_history text.
// The created_at field is set to the current UTC time in RFC3339Nano format.
func (s *SDDStore) InsertLaneAudit(ctx context.Context, rec *model.LaneAuditRecord) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)

	passed := 0
	if rec.Passed {
		passed = 1
	}

	const q = `
		INSERT INTO lane_audits (spec_id, passed, file_count, lines_changed, breaches, base_sha, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`

	res, err := s.db.ExecContext(ctx, q,
		rec.SpecID, passed, rec.FileCount, rec.LinesChanged, rec.Breaches, rec.BaseSHA, now,
	)
	if err != nil {
		return fmt.Errorf("store: insert lane audit: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: insert lane audit: last insert id: %w", err)
	}
	rec.ID = id
	rec.CreatedAt, _ = parseTime(now)
	return nil
}

// LatestLaneAudit returns the most recent lane_audits row for specID.
// Returns nil, nil when no audit has been recorded yet (sql.ErrNoRows).
func (s *SDDStore) LatestLaneAudit(ctx context.Context, specID string) (*model.LaneAuditRecord, error) {
	const q = `
		SELECT id, spec_id, passed, file_count, lines_changed, breaches, base_sha, created_at
		FROM lane_audits
		WHERE spec_id = ?
		ORDER BY created_at DESC LIMIT 1`

	row := s.db.QueryRowContext(ctx, q, specID)

	rec := &model.LaneAuditRecord{}
	var passedInt int
	var createdAtStr string
	err := row.Scan(
		&rec.ID, &rec.SpecID, &passedInt, &rec.FileCount, &rec.LinesChanged,
		&rec.Breaches, &rec.BaseSHA, &createdAtStr,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: latest lane audit: %w", err)
	}
	rec.Passed = passedInt == 1
	t, err := parseTime(createdAtStr)
	if err != nil {
		return nil, fmt.Errorf("store: latest lane audit: parse created_at: %w", err)
	}
	rec.CreatedAt = t
	return rec, nil
}

// GetSpecHistory returns all history entries for a spec, ordered by timestamp ascending.
func (s *SDDStore) GetSpecHistory(ctx context.Context, specID string) ([]*model.SpecHistory, error) {
	const q = `
		SELECT id, spec_id, from_status, to_status, by, reason, at
		FROM spec_history WHERE spec_id = ? ORDER BY at ASC`

	rows, err := s.db.QueryContext(ctx, q, specID)
	if err != nil {
		return nil, fmt.Errorf("store: get spec history: %w", err)
	}
	defer rows.Close()

	var history []*model.SpecHistory
	for rows.Next() {
		h := &model.SpecHistory{}
		var atStr string
		if err := rows.Scan(&h.ID, &h.SpecID, (*string)(&h.FromStatus), (*string)(&h.ToStatus), &h.By, &h.Reason, &atStr); err != nil {
			return nil, fmt.Errorf("store: get spec history: scan: %w", err)
		}
		t, err := parseTime(atStr)
		if err != nil {
			return nil, fmt.Errorf("store: get spec history: parse at: %w", err)
		}
		h.At = t
		history = append(history, h)
	}
	return history, rows.Err()
}

// InsertSpecHistoryEntry writes a single history row without the optimistic-lock
// check of UpdateSpecStatus. It is used to record events (e.g. a failed audit
// run that leaves the spec in the same status) that are not status transitions.
// from and to may be equal to signal a same-status annotation.
func (s *SDDStore) InsertSpecHistoryEntry(ctx context.Context, specID string, from, to model.SpecStatus, by, reason string) error {
	historyID, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("store: insert spec history: gen id: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO spec_history (id, spec_id, from_status, to_status, by, reason, at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		historyID.String(), specID, string(from), string(to), by, reason, now)
	if err != nil {
		return fmt.Errorf("store: insert spec history: %w", err)
	}
	return nil
}

// SpecCounts returns the number of specs per status for a project.
func (s *SDDStore) SpecCounts(ctx context.Context, project string) (map[model.SpecStatus]int, error) {
	const q = `SELECT status, COUNT(*) FROM specs WHERE project = ? GROUP BY status`
	rows, err := s.db.QueryContext(ctx, q, project)
	if err != nil {
		return nil, fmt.Errorf("store: spec counts: %w", err)
	}
	defer rows.Close()

	counts := make(map[model.SpecStatus]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("store: spec counts: scan: %w", err)
		}
		counts[model.SpecStatus(status)] = count
	}
	return counts, rows.Err()
}

// RecentlyCompletedSpecs returns specs with status "done" ordered by
// updated_at descending, limited to n results.
func (s *SDDStore) RecentlyCompletedSpecs(ctx context.Context, project string, n int) ([]*model.Spec, error) {
	const q = `
		SELECT id, title, status, project, COALESCE(backlog_id, ''),
		       lane, scope, COALESCE(base_sha, ''), assigned_agents, files_changed, created_at, updated_at
		FROM specs WHERE project = ? AND status = 'done'
		ORDER BY updated_at DESC LIMIT ?`

	rows, err := s.db.QueryContext(ctx, q, project, n)
	if err != nil {
		return nil, fmt.Errorf("store: recently completed specs: %w", err)
	}
	defer rows.Close()

	return collectSpecs(rows)
}

// --- PUSHBACK OPERATIONS ---

// CreatePushback inserts a new pushback for a spec.
// A UUIDv7 ID is generated automatically.
func (s *SDDStore) CreatePushback(ctx context.Context, pb *model.SpecPushback) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("store: create pushback: gen id: %w", err)
	}
	pb.ID = id.String()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	pb.CreatedAt = time.Now().UTC()

	questions, err := marshalStringSlice(pb.Questions)
	if err != nil {
		return fmt.Errorf("store: create pushback: marshal questions: %w", err)
	}

	const q = `
		INSERT INTO spec_pushbacks
			(id, spec_id, from_agent, questions, resolved, resolution, created_at, resolved_at)
		VALUES
			(?, ?, ?, ?, 0, '', ?, NULL)`

	_, err = s.db.ExecContext(ctx, q, pb.ID, pb.SpecID, pb.FromAgent, questions, now)
	if err != nil {
		return fmt.Errorf("store: create pushback: %w", err)
	}
	return nil
}

// GetUnresolvedPushbacks returns all unresolved pushbacks for a spec,
// ordered by created_at ascending.
func (s *SDDStore) GetUnresolvedPushbacks(ctx context.Context, specID string) ([]*model.SpecPushback, error) {
	const q = `
		SELECT id, spec_id, from_agent, questions, resolved, resolution, created_at, resolved_at
		FROM spec_pushbacks WHERE spec_id = ? AND resolved = 0 ORDER BY created_at ASC`
	return s.queryPushbacks(ctx, q, specID)
}

// GetAllPushbacks returns all pushbacks for a spec (resolved and unresolved),
// ordered by created_at ascending.
func (s *SDDStore) GetAllPushbacks(ctx context.Context, specID string) ([]*model.SpecPushback, error) {
	const q = `
		SELECT id, spec_id, from_agent, questions, resolved, resolution, created_at, resolved_at
		FROM spec_pushbacks WHERE spec_id = ? ORDER BY created_at ASC`
	return s.queryPushbacks(ctx, q, specID)
}

// ResolvePushback marks a pushback as resolved with the given resolution text
// and sets resolved_at to the current UTC time.
func (s *SDDStore) ResolvePushback(ctx context.Context, pushbackID, resolution string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	const q = `UPDATE spec_pushbacks SET resolved = 1, resolution = ?, resolved_at = ? WHERE id = ?`
	res, err := s.db.ExecContext(ctx, q, resolution, now, pushbackID)
	if err != nil {
		return fmt.Errorf("store: resolve pushback: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: resolve pushback: rows affected: %w", err)
	}
	if n == 0 {
		return model.ErrPushbackNotFound
	}
	return nil
}

// --- HELPERS ---

// queryPushbacks is the shared scanning logic for pushback queries.
func (s *SDDStore) queryPushbacks(ctx context.Context, q, specID string) ([]*model.SpecPushback, error) {
	rows, err := s.db.QueryContext(ctx, q, specID)
	if err != nil {
		return nil, fmt.Errorf("store: query pushbacks: %w", err)
	}
	defer rows.Close()

	var pushbacks []*model.SpecPushback
	for rows.Next() {
		pb := &model.SpecPushback{}
		var questionsJSON string
		var resolvedInt int
		var createdStr string
		var resolvedAtStr sql.NullString

		if err := rows.Scan(&pb.ID, &pb.SpecID, &pb.FromAgent, &questionsJSON,
			&resolvedInt, &pb.Resolution, &createdStr, &resolvedAtStr); err != nil {
			return nil, fmt.Errorf("store: query pushbacks: scan: %w", err)
		}

		pb.Resolved = resolvedInt == 1

		if err := json.Unmarshal([]byte(questionsJSON), &pb.Questions); err != nil {
			return nil, fmt.Errorf("store: query pushbacks: unmarshal questions: %w", err)
		}

		t, err := parseTime(createdStr)
		if err != nil {
			return nil, fmt.Errorf("store: query pushbacks: parse created_at: %w", err)
		}
		pb.CreatedAt = t

		if resolvedAtStr.Valid && resolvedAtStr.String != "" {
			rt, err := parseTime(resolvedAtStr.String)
			if err != nil {
				return nil, fmt.Errorf("store: query pushbacks: parse resolved_at: %w", err)
			}
			pb.ResolvedAt = &rt
		}

		pushbacks = append(pushbacks, pb)
	}
	return pushbacks, rows.Err()
}

// scanBacklogItem scans a single row into a BacklogItem.
// The SELECT must include columns in this order: id, title, description,
// status, priority, project, spec_id, archive_reason, position, lane, scope,
// created_at, updated_at.
func scanBacklogItem(row *sql.Row) (*model.BacklogItem, error) {
	item := &model.BacklogItem{}
	var createdStr, updatedStr string
	err := row.Scan(
		&item.ID, &item.Title, &item.Description,
		(*string)(&item.Status), (*string)(&item.Priority),
		&item.Project, &item.SpecID, &item.ArchiveReason,
		&item.Position, (*string)(&item.Lane), &item.Scope,
		&createdStr, &updatedStr,
	)
	if err != nil {
		return nil, err
	}
	var parseErr error
	item.CreatedAt, parseErr = parseTime(createdStr)
	if parseErr != nil {
		return nil, fmt.Errorf("parse created_at: %w", parseErr)
	}
	item.UpdatedAt, parseErr = parseTime(updatedStr)
	if parseErr != nil {
		return nil, fmt.Errorf("parse updated_at: %w", parseErr)
	}
	return item, nil
}

// collectBacklogItems reads all rows into a BacklogItem slice.
func collectBacklogItems(rows *sql.Rows) ([]*model.BacklogItem, error) {
	var items []*model.BacklogItem
	for rows.Next() {
		item := &model.BacklogItem{}
		var createdStr, updatedStr string
		if err := rows.Scan(
			&item.ID, &item.Title, &item.Description,
			(*string)(&item.Status), (*string)(&item.Priority),
			&item.Project, &item.SpecID, &item.ArchiveReason,
			&item.Position, (*string)(&item.Lane), &item.Scope,
			&createdStr, &updatedStr,
		); err != nil {
			return nil, fmt.Errorf("scan backlog item: %w", err)
		}
		var err error
		item.CreatedAt, err = parseTime(createdStr)
		if err != nil {
			return nil, fmt.Errorf("parse created_at: %w", err)
		}
		item.UpdatedAt, err = parseTime(updatedStr)
		if err != nil {
			return nil, fmt.Errorf("parse updated_at: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// scanSpec scans a single row into a Spec.
// The SELECT must include columns in this order: id, title, status, project,
// backlog_id, lane, scope, base_sha, assigned_agents, files_changed, created_at, updated_at.
func scanSpec(row *sql.Row) (*model.Spec, error) {
	spec := &model.Spec{}
	var createdStr, updatedStr string
	var agentsJSON, filesJSON string
	err := row.Scan(
		&spec.ID, &spec.Title, (*string)(&spec.Status),
		&spec.Project, &spec.BacklogID,
		(*string)(&spec.Lane), &spec.Scope, &spec.BaseSHA,
		&agentsJSON, &filesJSON,
		&createdStr, &updatedStr,
	)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(agentsJSON), &spec.AssignedAgents); err != nil {
		return nil, fmt.Errorf("unmarshal assigned_agents: %w", err)
	}
	if err := json.Unmarshal([]byte(filesJSON), &spec.FilesChanged); err != nil {
		return nil, fmt.Errorf("unmarshal files_changed: %w", err)
	}
	var parseErr error
	spec.CreatedAt, parseErr = parseTime(createdStr)
	if parseErr != nil {
		return nil, fmt.Errorf("parse created_at: %w", parseErr)
	}
	spec.UpdatedAt, parseErr = parseTime(updatedStr)
	if parseErr != nil {
		return nil, fmt.Errorf("parse updated_at: %w", parseErr)
	}
	return spec, nil
}

// collectSpecs reads all rows into a Spec slice.
func collectSpecs(rows *sql.Rows) ([]*model.Spec, error) {
	var specs []*model.Spec
	for rows.Next() {
		spec := &model.Spec{}
		var createdStr, updatedStr string
		var agentsJSON, filesJSON string
		if err := rows.Scan(
			&spec.ID, &spec.Title, (*string)(&spec.Status),
			&spec.Project, &spec.BacklogID,
			(*string)(&spec.Lane), &spec.Scope, &spec.BaseSHA,
			&agentsJSON, &filesJSON,
			&createdStr, &updatedStr,
		); err != nil {
			return nil, fmt.Errorf("scan spec: %w", err)
		}
		if err := json.Unmarshal([]byte(agentsJSON), &spec.AssignedAgents); err != nil {
			return nil, fmt.Errorf("unmarshal assigned_agents: %w", err)
		}
		if err := json.Unmarshal([]byte(filesJSON), &spec.FilesChanged); err != nil {
			return nil, fmt.Errorf("unmarshal files_changed: %w", err)
		}
		var err error
		spec.CreatedAt, err = parseTime(createdStr)
		if err != nil {
			return nil, fmt.Errorf("parse created_at: %w", err)
		}
		spec.UpdatedAt, err = parseTime(updatedStr)
		if err != nil {
			return nil, fmt.Errorf("parse updated_at: %w", err)
		}
		specs = append(specs, spec)
	}
	return specs, rows.Err()
}

// marshalStringSlice serialises a string slice to JSON. Returns "[]" for nil
// or empty slices so that the database column always contains valid JSON.
func marshalStringSlice(s []string) (string, error) {
	if len(s) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
