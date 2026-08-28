// Package store implements the data access layer for mneme using SQLite.
// This file provides the SDDStore which handles all SDD (Spec-Driven Development)
// persistence: backlog items, specs, spec history, and spec pushbacks.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
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
// rowid closes the tie-break: position is 0 on every item, so without a
// final tie-break the order is partial and a LIMIT would be a lottery, not a
// window (D7).
//
// rowid, NOT created_at (QA rejection on the first cut of D7/AC27): the
// original tie-break used `created_at ASC, id ASC`, on the false premise
// that time.RFC3339Nano is lexicographically chronological. It is not —
// Format trims trailing zeros from the fractional-second component, so e.g.
// "...52.77Z" (an exact .770000000s) and "...52.770018Z" (18µs later) compare
// as "...52.770018Z" < "...52.77Z" byte-for-byte ('0' < 'Z' at the first
// point of divergence) even though the first string denotes the LATER
// instant. QA reproduced the resulting misordering in 23 of 300 iterations
// of 5-item insert bursts, and it also flipped this package's own
// TestListBacklogItems_DeterministicTieBreak in 1 of 3 repeated runs. rowid
// is SQLite's own monotonically-increasing insertion counter — immune to any
// text-formatting artefact — and is safe here because backlog_items rows are
// archived (status update), never hard-deleted, so rowid is never reused.
//
// Every column is qualified with the b alias since SPEC-110 D11 added a
// LEFT JOIN: an unqualified `rowid` with more than one source in the FROM is
// fragile — the derived table exposes no rowid, so SQLite may resolve it today
// and stop tomorrow. Losing this tie-break breaks NOTHING VISIBLY: the list
// still returns the same items and Total still adds up. What breaks is that a
// LIMIT goes back to being a lottery instead of a window — the exact defect QA
// rejected in SPEC-109, which showed up in only 23 of 300 iterations.
const backlogListOrder = ` ORDER BY
	CASE b.priority
	    WHEN 'critical' THEN 0
	    WHEN 'high'     THEN 1
	    WHEN 'medium'   THEN 2
	    WHEN 'low'      THEN 3
	    ELSE 99
	END ASC,
	b.position ASC, b.rowid ASC`

// specListOrder is the TOTAL order for spec listings: rowid alone, since it
// is both unique (no further tie-break needed) and monotonic by insertion —
// see backlogListOrder's godoc for why created_at was rejected as a sort key
// (QA rejection on the first cut of D7/AC27: time.RFC3339Nano is not
// lexicographically chronological). specs rows are never hard-deleted
// either, so rowid reuse is not a concern.
const specListOrder = ` ORDER BY rowid ASC`

// backlogListWhere / backlogListWhereStatus are the ONLY definition of the
// backlog listing predicate. The COUNT and the page query consume the SAME
// variable and the SAME args slice, so they cannot diverge: the symmetry is
// structural, not a convention a future editor could break by accident. If
// they diverge, Total lies — and a Total that lies is worse than no Total at
// all. Precedent and same requirement as sessionWorkWhere (store/session.go)
// and HardDeleteBySource ("MUST stay byte-identical").
const backlogListWhere = ` WHERE project = ?`
const backlogListWhereStatus = backlogListWhere + ` AND status = ?`

// backlogSelectColumns / backlogSelectFrom are the ONE projection every read
// path of a BacklogItem uses (SPEC-110 D11/D12): GetBacklogItem and
// ListBacklogItems both build their query from these, so neither can return an
// item whose RefinementCount is silently zero.
//
// The count comes from a LEFT JOIN against an ALREADY-AGGREGATED derived table.
// Three properties make this the only admissible form:
//
//  1. No N+1: one page query covers N items. There is no per-item COUNT.
//  2. No row multiplication: the derived table is unique by item_id, so the
//     LEFT JOIN yields exactly ONE row per item. Joining the RAW table instead
//     would return one row per refinement — an item with 3 refinements would
//     appear 3 times — and since backlogCountSelect counts backlog_items
//     WITHOUT the join, Total would stop matching len(items) of an unwindowed
//     list, breaking the relation SPEC-109 D3/D6 built.
//  3. The derived table carries NO WHERE, so the textual guard
//     TestBacklogListPredicate_SharedByCountAndPage still passes unchanged.
//     This is why a correlated subquery — (SELECT COUNT(*) ... WHERE r.item_id
//     = b.id) — is FORBIDDEN: it embeds a WHERE in backlogListSelect, which is
//     precisely what that guard exists to catch.
//
// backlogListWhere stays byte-identical (" WHERE project = ?", unqualified):
// the derived table exposes neither project nor status, so the same string
// resolves both in the joined page query and in the un-joined COUNT.
const backlogSelectColumns = `
	SELECT b.id, b.title, b.description, b.status, b.priority, b.project,
	       COALESCE(b.spec_id, ''), b.archive_reason, b.position, b.lane, b.scope,
	       b.created_at, b.updated_at, COALESCE(r.n, 0), b.uuid, b.previous_ids`

const backlogSelectFrom = `
	FROM backlog_items b
	LEFT JOIN (
	    SELECT item_id, COUNT(*) AS n
	    FROM backlog_refinements
	    GROUP BY item_id
	) r ON r.item_id = b.id`

// backlogListSelect / backlogCountSelect share backlogListWhere so the page
// and the COUNT can never select from a different row set.
const backlogListSelect = backlogSelectColumns + backlogSelectFrom

// backlogCountSelect counts ITEMS and must NEVER gain the join: Total is a
// count of items, not of refinements (SPEC-109 D3).
const backlogCountSelect = `SELECT COUNT(*) FROM backlog_items`

// backlogStatusIndexSelect is BacklogStatusIndex's own projection (SPEC-126
// DD4): only the three columns the freeze decision needs. Deliberately NOT
// built from backlogSelectColumns/backlogListWhere — no description (the
// whole reason this method exists instead of reusing ListBacklogItems), and
// no WHERE at all (see BacklogStatusIndex's godoc for why).
const backlogStatusIndexSelect = `SELECT id, status, archive_reason FROM backlog_items`

// specListWhere / specListWhereStatus mirror backlogListWhere for specs (D6).
const specListWhere = ` WHERE project = ?`
const specListWhereStatus = specListWhere + ` AND status = ?`

const specListSelect = `
	SELECT id, title, status, project, COALESCE(backlog_id, ''),
	       lane, scope, COALESCE(base_sha, ''), assigned_agents, files_changed,
	       created_at, updated_at, uuid, previous_ids
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
//
// A UUIDv7 anchor (SPEC-128 D1/D11) is minted here and written into item.UUID
// before the INSERT — no backlog item is ever created without one. The
// anchor is immutable: there is no verb anywhere that updates this column.
func (s *SDDStore) CreateBacklogItem(ctx context.Context, item *model.BacklogItem) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	item.CreatedAt = time.Now().UTC()
	item.UpdatedAt = item.CreatedAt

	anchor, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("store: create backlog item: gen uuid: %w", err)
	}
	item.UUID = anchor.String()

	const q = `
		INSERT INTO backlog_items
			(id, title, description, status, priority, project, spec_id, archive_reason, position, lane, scope, uuid, created_at, updated_at)
		VALUES
			(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	specID := toNullString(item.SpecID)
	_, err = s.db.ExecContext(ctx, q,
		item.ID, item.Title, item.Description,
		string(item.Status), string(item.Priority),
		item.Project, specID, item.ArchiveReason,
		item.Position, string(item.Lane), item.Scope, item.UUID, now, now,
	)
	if err != nil {
		return fmt.Errorf("store: create backlog item: %w", err)
	}
	return nil
}

// GetBacklogItem retrieves a backlog item by ID.
// Returns model.ErrBacklogNotFound when no matching item exists.
func (s *SDDStore) GetBacklogItem(ctx context.Context, id string) (*model.BacklogItem, error) {
	q := backlogSelectColumns + backlogSelectFrom + ` WHERE b.id = ?`

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

// BacklogStatusIndex returns the status and archive reason of EVERY backlog
// item in the database, keyed by item ID (SPEC-126 DD4). It is the
// set-shaped counterpart of GetBacklogItem: one query, three narrow columns,
// no description.
//
// There is deliberately NO project filter and NO WHERE clause at all.
// backlog_items.id is a single-column TEXT PRIMARY KEY (migration 004,
// never altered), so an ID identifies at most ONE row in the whole file:
// this index resolves EXACTLY the same relation GetBacklogItem resolves —
// which also does not filter by project — and that is what makes a spec
// listing structurally unable to contradict loadMutableSpec's refusal.
// Adding "WHERE project = ?" would reintroduce that disagreement for a spec
// whose BacklogID names an item of a DIFFERENT project. Do not "fix" this.
//
// The store does not decide what counts as archived: it returns raw
// statuses, and the comparison against BacklogStatusArchived lives only in
// service.specFreeze (SPEC-126 DD3) — comparing here would create a third
// place that knows what an archived item is, outside that decision's
// structural guardian.
func (s *SDDStore) BacklogStatusIndex(ctx context.Context) (map[string]model.BacklogIndexEntry, error) {
	rows, err := s.db.QueryContext(ctx, backlogStatusIndexSelect)
	if err != nil {
		return nil, fmt.Errorf("store: backlog status index: query: %w", err)
	}
	defer rows.Close()

	index := make(map[string]model.BacklogIndexEntry)
	for rows.Next() {
		var id string
		var entry model.BacklogIndexEntry
		if err := rows.Scan(&id, &entry.Status, &entry.ArchiveReason); err != nil {
			return nil, fmt.Errorf("store: backlog status index: scan: %w", err)
		}
		index[id] = entry
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: backlog status index: rows: %w", err)
	}
	return index, nil
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

// AppendBacklogRefinement appends one refinement row to itemID and leaves the
// item in status next, atomically (SPEC-110 D14).
//
// expected is an OPTIMISTIC LOCK, not policy: the service has already decided
// which statuses admit refinement (D3), and this method only enforces that the
// state the service validated is still the state found inside the transaction.
// It is the same shape as UpdateSpecStatus (store/sdd.go). Without the
// transaction two concurrent refinements would compute the same seq.
//
// Returns model.ErrBacklogNotRefinable when the status drifted, and
// model.ErrBacklogNotFound when the item does not exist.
func (s *SDDStore) AppendBacklogRefinement(
	ctx context.Context, itemID string, expected, next model.BacklogStatus, body, by string,
) (*model.BacklogRefinement, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: append backlog refinement: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Optimistic lock: the status the service validated must still be the
	// status found here, inside the transaction.
	var currentStatus string
	err = tx.QueryRowContext(ctx, `SELECT status FROM backlog_items WHERE id = ?`, itemID).Scan(&currentStatus)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, model.ErrBacklogNotFound
		}
		return nil, fmt.Errorf("store: append backlog refinement: read current status: %w", err)
	}
	if model.BacklogStatus(currentStatus) != expected {
		return nil, fmt.Errorf("store: append backlog refinement: expected %s but found %s: %w",
			expected, currentStatus, model.ErrBacklogNotRefinable)
	}

	var lastSeq sql.NullInt64
	err = tx.QueryRowContext(ctx,
		`SELECT MAX(seq) FROM backlog_refinements WHERE item_id = ?`, itemID,
	).Scan(&lastSeq)
	if err != nil {
		return nil, fmt.Errorf("store: append backlog refinement: next seq: %w", err)
	}
	seq := int(lastSeq.Int64) + 1

	now := time.Now().UTC().Format(time.RFC3339Nano)

	_, err = tx.ExecContext(ctx,
		`INSERT INTO backlog_refinements (item_id, seq, body, by, at) VALUES (?, ?, ?, ?, ?)`,
		itemID, seq, body, by, now,
	)
	if err != nil {
		return nil, fmt.Errorf("store: append backlog refinement: insert: %w", err)
	}

	// Only status and updated_at change. description is NEVER touched here
	// (D15 — description is write-once, written by CreateBacklogItem only).
	_, err = tx.ExecContext(ctx,
		`UPDATE backlog_items SET status = ?, updated_at = ? WHERE id = ?`,
		string(next), now, itemID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: append backlog refinement: update item: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: append backlog refinement: commit: %w", err)
	}

	at, err := parseTime(now)
	if err != nil {
		return nil, fmt.Errorf("store: append backlog refinement: parse at: %w", err)
	}

	return &model.BacklogRefinement{
		ItemID: itemID,
		Seq:    seq,
		Body:   body,
		By:     by,
		At:     at,
	}, nil
}

// ListBacklogRefinements returns every refinement of itemID ordered by seq
// ascending — never by at, see BacklogRefinement's godoc (D21). No limit: this
// is the full-fidelity path (D6).
func (s *SDDStore) ListBacklogRefinements(ctx context.Context, itemID string) ([]*model.BacklogRefinement, error) {
	const q = `SELECT item_id, seq, body, by, at FROM backlog_refinements WHERE item_id = ? ORDER BY seq ASC`

	rows, err := s.db.QueryContext(ctx, q, itemID)
	if err != nil {
		return nil, fmt.Errorf("store: list backlog refinements: %w", err)
	}
	defer rows.Close()

	var refinements []*model.BacklogRefinement
	for rows.Next() {
		r := &model.BacklogRefinement{}
		var atStr string
		if err := rows.Scan(&r.ItemID, &r.Seq, &r.Body, &r.By, &atStr); err != nil {
			return nil, fmt.Errorf("store: list backlog refinements: scan: %w", err)
		}
		at, err := parseTime(atStr)
		if err != nil {
			return nil, fmt.Errorf("store: list backlog refinements: parse at: %w", err)
		}
		r.At = at
		refinements = append(refinements, r)
	}
	return refinements, rows.Err()
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
//
// A UUIDv7 anchor (SPEC-128 D1/D11) is minted here and written into
// spec.UUID before the INSERT — no spec is ever created without one. The
// anchor is immutable: there is no verb anywhere that updates this column.
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

	anchor, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("store: create spec: gen uuid: %w", err)
	}
	spec.UUID = anchor.String()

	const q = `
		INSERT INTO specs
			(id, title, status, project, backlog_id, lane, scope, base_sha, assigned_agents, files_changed, uuid, created_at, updated_at)
		VALUES
			(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	backlogID := toNullString(spec.BacklogID)
	_, err = s.db.ExecContext(ctx, q,
		spec.ID, spec.Title, string(spec.Status), spec.Project,
		backlogID, string(spec.Lane), spec.Scope, spec.BaseSHA, agents, files, spec.UUID, now, now,
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
		       lane, scope, COALESCE(base_sha, ''), assigned_agents, files_changed, created_at, updated_at, uuid, previous_ids
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

// GetSpecHistory returns all history entries for a spec, ordered by
// timestamp ascending, with `, id ASC` as a deterministic tie-break
// (SPEC-130 D36/C2): `at` is text formatted with time.RFC3339Nano, which is
// NOT lexicographically chronological (Format trims trailing zeros from the
// fractional second — the same defect SPEC-110 D21 found), so two rows with
// an identical `at` string need a second key or their relative order is a
// coin flip. sddfile's write-through (SPEC-130 §9.2) needs the file to be
// byte-stable for the same DB state, or every re-export would produce a
// spurious diff. `id` (a UUIDv7, monotonic by insertion) is exactly that
// second key — this mirrors backlogListOrder's own rowid tie-break above,
// substituting `id` because spec_history has no rowid exposed here.
func (s *SDDStore) GetSpecHistory(ctx context.Context, specID string) ([]*model.SpecHistory, error) {
	const q = `
		SELECT id, spec_id, from_status, to_status, by, reason, at
		FROM spec_history WHERE spec_id = ? ORDER BY at ASC, id ASC`

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
	// previous_ids is selected here too, even though it is not one of the
	// "three read projections" the spec names (backlogSelectColumns,
	// specListSelect, GetSpec's inline query): this query feeds the SAME
	// shared scanner, collectSpecs, so its column list must match whatever
	// that scanner expects or every row here would fail to scan (SPEC-130
	// implementation correction — noted in changes.md).
	const q = `
		SELECT id, title, status, project, COALESCE(backlog_id, ''),
		       lane, scope, COALESCE(base_sha, ''), assigned_agents, files_changed, created_at, updated_at, uuid, previous_ids
		FROM specs WHERE project = ? AND status = 'done'
		ORDER BY updated_at DESC LIMIT ?`

	rows, err := s.db.QueryContext(ctx, q, project, n)
	if err != nil {
		return nil, fmt.Errorf("store: recently completed specs: %w", err)
	}
	defer rows.Close()

	return collectSpecs(rows)
}

// UUIDsForRefs resolves a batch of normalized SDD correlatives (e.g.
// "SPEC-125", "BL-001") to their anchor in THIS database — the lookup
// bakeSDDRefs uses when a newly-written mention needs a target_uuid
// (SPEC-128 D5).
//
// refIDs is split by model.SDDRefKind and each bucket is resolved with one
// query against its own table (an IN clause), never more than two queries
// regardless of how many refIDs are passed. An empty refIDs returns an
// empty map without touching the database. A correlative that doesn't exist
// locally is simply absent from the result — never an error, since "not
// anchored here" is an expected, common outcome (D5/D8), not a failure.
func (s *SDDStore) UUIDsForRefs(ctx context.Context, refIDs []string) (map[string]string, error) {
	out := make(map[string]string, len(refIDs))
	if len(refIDs) == 0 {
		return out, nil
	}

	var backlogIDs, specIDs []string
	for _, refID := range refIDs {
		switch model.SDDRefKind(refID) {
		case "backlog":
			backlogIDs = append(backlogIDs, refID)
		case "spec":
			specIDs = append(specIDs, refID)
		}
	}

	if len(backlogIDs) > 0 {
		q, args := inClauseQuery(`SELECT id, uuid FROM backlog_items WHERE uuid <> '' AND id IN (%s)`, backlogIDs)
		if err := collectRefUUIDPairs(ctx, s.db, q, args, out); err != nil {
			return nil, fmt.Errorf("store: uuids for refs: backlog_items: %w", err)
		}
	}
	if len(specIDs) > 0 {
		q, args := inClauseQuery(`SELECT id, uuid FROM specs WHERE uuid <> '' AND id IN (%s)`, specIDs)
		if err := collectRefUUIDPairs(ctx, s.db, q, args, out); err != nil {
			return nil, fmt.Errorf("store: uuids for refs: specs: %w", err)
		}
	}
	return out, nil
}

// RefsForUUIDs resolves a batch of anchors to their CURRENT correlative in
// THIS database — the lookup MemoryService.Get uses to populate local_id
// for a reference whose Status resolves to SDDRefLocal (D8).
//
// Unlike UUIDsForRefs, an anchor's table can't be told apart from the
// string alone, so both backlog_items and specs are queried — always
// exactly two queries when uuids is non-empty, never more regardless of how
// many anchors are passed. An empty uuids returns an empty map without
// touching the database. An anchor that resolves nowhere in this database
// is simply absent from the result — that absence IS the "foreign" case
// D8 exists to represent honestly.
func (s *SDDStore) RefsForUUIDs(ctx context.Context, uuids []string) (map[string]string, error) {
	out := make(map[string]string, len(uuids))
	if len(uuids) == 0 {
		return out, nil
	}

	q, args := inClauseQuery(`SELECT uuid, id FROM backlog_items WHERE uuid IN (%s)`, uuids)
	if err := collectRefUUIDPairs(ctx, s.db, q, args, out); err != nil {
		return nil, fmt.Errorf("store: refs for uuids: backlog_items: %w", err)
	}

	q, args = inClauseQuery(`SELECT uuid, id FROM specs WHERE uuid IN (%s)`, uuids)
	if err := collectRefUUIDPairs(ctx, s.db, q, args, out); err != nil {
		return nil, fmt.Errorf("store: refs for uuids: specs: %w", err)
	}
	return out, nil
}

// inClauseQuery expands a "%s" placeholder in template into the right
// number of "?" markers for an IN clause and returns the matching args
// slice, so every caller builds its IN clause the same way.
func inClauseQuery(template string, values []string) (string, []any) {
	placeholders := make([]string, len(values))
	args := make([]any, len(values))
	for i, v := range values {
		placeholders[i] = "?"
		args[i] = v
	}
	return fmt.Sprintf(template, strings.Join(placeholders, ", ")), args
}

// collectRefUUIDPairs runs q (a two-column SELECT of (key, value) pairs)
// and writes every row into out, sharing the exact scan-and-merge shape
// UUIDsForRefs and RefsForUUIDs both need.
func collectRefUUIDPairs(ctx context.Context, database *db.DB, q string, args []any, out map[string]string) error {
	rows, err := database.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		out[key] = value
	}
	return rows.Err()
}

// IsSDDReferenceBackfillComplete reports whether the one-shot SDD reference
// backfill (SPEC-128 D7 mitad B) has already run against this database —
// the cheap guard MemoryService.BackfillSDDRefs checks first, mirroring
// ensureSDDUUIDs' own "microsegundos en el caso normal" posture (D7 mitad
// A): this runs from initService on every invocation, so the common case
// (already done) must cost one indexed row read, never a memory scan.
func (s *SDDStore) IsSDDReferenceBackfillComplete(ctx context.Context) (bool, error) {
	var completedAt sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT completed_at FROM sdd_reference_backfill WHERE id = 1`,
	).Scan(&completedAt)
	if err != nil {
		return false, fmt.Errorf("store: is sdd reference backfill complete: %w", err)
	}
	return completedAt.Valid, nil
}

// MarkSDDReferenceBackfillComplete records that the one-shot SDD reference
// backfill finished, along with the totals it produced — the single row
// AC8 checks for.
func (s *SDDStore) MarkSDDReferenceBackfillComplete(ctx context.Context, scanned, created int) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx,
		`UPDATE sdd_reference_backfill SET completed_at = ?, memories_scanned = ?, refs_created = ? WHERE id = 1`,
		now, scanned, created,
	)
	if err != nil {
		return fmt.Errorf("store: mark sdd reference backfill complete: %w", err)
	}
	return nil
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

// GetAllPushbacks returns all pushbacks for a spec (resolved and
// unresolved), ordered by created_at ascending, with `, id ASC` as a
// deterministic tie-break — same reasoning as GetSpecHistory above
// (SPEC-130 D36/C2): this is the query sddfile's write-through reads for
// the archived record, and it needs a byte-stable order.
//
// GetUnresolvedPushbacks, below, deliberately does NOT gain this tie-break:
// SpecResolve uses it to pick the OLDEST unresolved pushback, and adding a
// second sort key would change WHICH pushback wins a created_at tie — a
// visible behaviour change outside this spec's scope (SPEC-130 C2).
func (s *SDDStore) GetAllPushbacks(ctx context.Context, specID string) ([]*model.SpecPushback, error) {
	const q = `
		SELECT id, spec_id, from_agent, questions, resolved, resolution, created_at, resolved_at
		FROM spec_pushbacks WHERE spec_id = ? ORDER BY created_at ASC, id ASC`
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
// created_at, updated_at, refinement_count, uuid, previous_ids
// (backlogSelectColumns).
func scanBacklogItem(row *sql.Row) (*model.BacklogItem, error) {
	item := &model.BacklogItem{}
	var createdStr, updatedStr, previousIDsRaw string
	err := row.Scan(
		&item.ID, &item.Title, &item.Description,
		(*string)(&item.Status), (*string)(&item.Priority),
		&item.Project, &item.SpecID, &item.ArchiveReason,
		&item.Position, (*string)(&item.Lane), &item.Scope,
		&createdStr, &updatedStr, &item.RefinementCount, &item.UUID, &previousIDsRaw,
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
	item.PreviousIDs = unmarshalPreviousIDs(previousIDsRaw)
	return item, nil
}

// collectBacklogItems reads all rows into a BacklogItem slice.
func collectBacklogItems(rows *sql.Rows) ([]*model.BacklogItem, error) {
	var items []*model.BacklogItem
	for rows.Next() {
		item := &model.BacklogItem{}
		var createdStr, updatedStr, previousIDsRaw string
		if err := rows.Scan(
			&item.ID, &item.Title, &item.Description,
			(*string)(&item.Status), (*string)(&item.Priority),
			&item.Project, &item.SpecID, &item.ArchiveReason,
			&item.Position, (*string)(&item.Lane), &item.Scope,
			&createdStr, &updatedStr, &item.RefinementCount, &item.UUID, &previousIDsRaw,
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
		item.PreviousIDs = unmarshalPreviousIDs(previousIDsRaw)
		items = append(items, item)
	}
	return items, rows.Err()
}

// scanSpec scans a single row into a Spec.
// The SELECT must include columns in this order: id, title, status, project,
// backlog_id, lane, scope, base_sha, assigned_agents, files_changed,
// created_at, updated_at, uuid, previous_ids.
func scanSpec(row *sql.Row) (*model.Spec, error) {
	spec := &model.Spec{}
	var createdStr, updatedStr, previousIDsRaw string
	var agentsJSON, filesJSON string
	err := row.Scan(
		&spec.ID, &spec.Title, (*string)(&spec.Status),
		&spec.Project, &spec.BacklogID,
		(*string)(&spec.Lane), &spec.Scope, &spec.BaseSHA,
		&agentsJSON, &filesJSON,
		&createdStr, &updatedStr, &spec.UUID, &previousIDsRaw,
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
	spec.PreviousIDs = unmarshalPreviousIDs(previousIDsRaw)
	return spec, nil
}

// collectSpecs reads all rows into a Spec slice.
func collectSpecs(rows *sql.Rows) ([]*model.Spec, error) {
	var specs []*model.Spec
	for rows.Next() {
		spec := &model.Spec{}
		var createdStr, updatedStr, previousIDsRaw string
		var agentsJSON, filesJSON string
		if err := rows.Scan(
			&spec.ID, &spec.Title, (*string)(&spec.Status),
			&spec.Project, &spec.BacklogID,
			(*string)(&spec.Lane), &spec.Scope, &spec.BaseSHA,
			&agentsJSON, &filesJSON,
			&createdStr, &updatedStr, &spec.UUID, &previousIDsRaw,
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
		spec.PreviousIDs = unmarshalPreviousIDs(previousIDsRaw)
		specs = append(specs, spec)
	}
	return specs, rows.Err()
}

// unmarshalPreviousIDs parses the previous_ids column (SPEC-130 D32) into
// PreviousID values. The column defaults to '' (migration 020), not '[]'
// like assigned_agents/files_changed — an empty string reads as an empty
// list without attempting to JSON-decode it. Any entry that fails to parse
// (model.ParsePreviousID) is silently skipped, mirroring
// vault.ParseSDDRefLines' tolerance for a hand-edited or malformed line:
// this column is inert in §2a (nothing writes to it yet), so a parse
// failure here can only come from manual tampering, never from mneme's own
// writer.
func unmarshalPreviousIDs(raw string) []model.PreviousID {
	if raw == "" {
		return nil
	}
	var lines []string
	if err := json.Unmarshal([]byte(raw), &lines); err != nil {
		return nil
	}
	if len(lines) == 0 {
		return nil
	}
	out := make([]model.PreviousID, 0, len(lines))
	for _, line := range lines {
		p, ok := model.ParsePreviousID(line)
		if !ok {
			continue
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
