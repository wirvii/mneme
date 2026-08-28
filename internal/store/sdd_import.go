// Package store — this file is SPEC-131 §2b's write surface for the SDD
// importer (D49): seven methods that PRESERVE the identity and timestamps a
// caller supplies, instead of minting/stamping them like their five
// neighbours in sdd.go (CreateBacklogItem, UpdateBacklogItem, CreateSpec,
// UpdateSpecStatus, UpdateSpecFields all stamp `now` and/or generate a fresh
// UUIDv7 unconditionally).
//
// The identity and dates travel VERBATIM; the importer NEVER stamps `now`.
// This is deliberate and has a concrete consequence if it is ever "fixed"
// to match the neighbours: if the importer stamped `now` into updated_at,
// the local row would stop matching the file it just came from, and the
// next export would rewrite that file with a new date — a spurious git
// diff on every `git pull`, on every machine, forever. Preserving verbatim
// is the only convergent choice (SPEC-131 D49).
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/wirvii/mneme/internal/model"
)

// CreateBacklogItemFromRecord inserts a backlog item using the id, uuid,
// created_at, updated_at and previous_ids the CALLER supplies — never
// minted or stamped by this method, unlike CreateBacklogItem.
//
// The anchor is minted ONLY when item.UUID arrives empty (the same
// uuid.NewV7() rule CreateBacklogItem uses), and written back into
// item.UUID so the caller can index the new row by its anchor immediately.
// created_at/updated_at are stamped to now ONLY when they arrive at the
// zero time — a hand-authored record that never had a timestamp gets one;
// a record that already has one (from a previous mneme's export) keeps it
// verbatim.
func (s *SDDStore) CreateBacklogItemFromRecord(ctx context.Context, item *model.BacklogItem) error {
	if item.UUID == "" {
		anchor, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("store: create backlog item from record: gen uuid: %w", err)
		}
		item.UUID = anchor.String()
	}

	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = now
	}

	previousIDs, err := marshalPreviousIDs(item.PreviousIDs)
	if err != nil {
		return fmt.Errorf("store: create backlog item from record: marshal previous_ids: %w", err)
	}

	const q = `
		INSERT INTO backlog_items
			(id, title, description, status, priority, project, spec_id, archive_reason, position, lane, scope, uuid, previous_ids, created_at, updated_at)
		VALUES
			(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	specID := toNullString(item.SpecID)
	_, err = s.db.ExecContext(ctx, q,
		item.ID, item.Title, item.Description,
		string(item.Status), string(item.Priority),
		item.Project, specID, item.ArchiveReason,
		item.Position, string(item.Lane), item.Scope, item.UUID, previousIDs,
		formatTime(item.CreatedAt), formatTime(item.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("store: create backlog item from record: %w", err)
	}
	return nil
}

// UpdateBacklogItemFromRecord updates a backlog item's mutable columns,
// located by ANCHOR (item.UUID) rather than by id — the importer's own
// decision rule (SPEC-131 D50) resolves identity by anchor, never by
// correlative, so the write path matches. updated_at is written VERBATIM
// from item.UpdatedAt, never stamped to now (see the package doc for why).
// id and uuid are never part of the SET clause: an anchor's own identity
// and its correlative at creation time are immutable here.
//
// Returns model.ErrBacklogNotFound when no row carries item.UUID.
func (s *SDDStore) UpdateBacklogItemFromRecord(ctx context.Context, item *model.BacklogItem) error {
	previousIDs, err := marshalPreviousIDs(item.PreviousIDs)
	if err != nil {
		return fmt.Errorf("store: update backlog item from record: marshal previous_ids: %w", err)
	}

	const q = `
		UPDATE backlog_items
		SET title = ?, description = ?, status = ?, priority = ?,
		    spec_id = ?, archive_reason = ?, position = ?, lane = ?, scope = ?,
		    previous_ids = ?, updated_at = ?
		WHERE uuid = ?`

	specID := toNullString(item.SpecID)
	res, err := s.db.ExecContext(ctx, q,
		item.Title, item.Description, string(item.Status), string(item.Priority),
		specID, item.ArchiveReason, item.Position, string(item.Lane), item.Scope,
		previousIDs, formatTime(item.UpdatedAt),
		item.UUID,
	)
	if err != nil {
		return fmt.Errorf("store: update backlog item from record: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update backlog item from record: rows affected: %w", err)
	}
	if n == 0 {
		return model.ErrBacklogNotFound
	}
	return nil
}

// MergeBacklogRefinements merges refs into itemID's refinement rows, keyed
// by (item_id, seq): a seq already present is UPDATED (body/by/at), a seq
// not yet present is INSERTED. NEVER DELETEs — a refinement present in the
// database and absent from refs (because it was written locally and never
// exported, or the file simply omits it) is left untouched (SPEC-131 D51).
// Runs inside a single transaction so a partial merge can never be observed.
func (s *SDDStore) MergeBacklogRefinements(ctx context.Context, itemID string, refs []*model.BacklogRefinement) error {
	if len(refs) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: merge backlog refinements: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, r := range refs {
		var exists int
		err := tx.QueryRowContext(ctx,
			`SELECT 1 FROM backlog_refinements WHERE item_id = ? AND seq = ?`, itemID, r.Seq,
		).Scan(&exists)
		switch {
		case err == sql.ErrNoRows:
			_, insErr := tx.ExecContext(ctx,
				`INSERT INTO backlog_refinements (item_id, seq, body, by, at) VALUES (?, ?, ?, ?, ?)`,
				itemID, r.Seq, r.Body, r.By, formatTime(r.At),
			)
			if insErr != nil {
				return fmt.Errorf("store: merge backlog refinements: insert seq %d: %w", r.Seq, insErr)
			}
		case err != nil:
			return fmt.Errorf("store: merge backlog refinements: probe seq %d: %w", r.Seq, err)
		default:
			_, updErr := tx.ExecContext(ctx,
				`UPDATE backlog_refinements SET body = ?, by = ?, at = ? WHERE item_id = ? AND seq = ?`,
				r.Body, r.By, formatTime(r.At), itemID, r.Seq,
			)
			if updErr != nil {
				return fmt.Errorf("store: merge backlog refinements: update seq %d: %w", r.Seq, updErr)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: merge backlog refinements: commit: %w", err)
	}
	return nil
}

// CreateSpecFromRecord is CreateBacklogItemFromRecord's sibling for specs:
// inserts using the id, uuid, created_at, updated_at and previous_ids the
// caller supplies. The anchor is minted only when spec.UUID is empty.
func (s *SDDStore) CreateSpecFromRecord(ctx context.Context, spec *model.Spec) error {
	if spec.UUID == "" {
		anchor, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("store: create spec from record: gen uuid: %w", err)
		}
		spec.UUID = anchor.String()
	}

	now := time.Now().UTC()
	if spec.CreatedAt.IsZero() {
		spec.CreatedAt = now
	}
	if spec.UpdatedAt.IsZero() {
		spec.UpdatedAt = now
	}

	agents, err := marshalStringSlice(spec.AssignedAgents)
	if err != nil {
		return fmt.Errorf("store: create spec from record: marshal assigned_agents: %w", err)
	}
	files, err := marshalStringSlice(spec.FilesChanged)
	if err != nil {
		return fmt.Errorf("store: create spec from record: marshal files_changed: %w", err)
	}
	previousIDs, err := marshalPreviousIDs(spec.PreviousIDs)
	if err != nil {
		return fmt.Errorf("store: create spec from record: marshal previous_ids: %w", err)
	}

	const q = `
		INSERT INTO specs
			(id, title, status, project, backlog_id, lane, scope, base_sha, assigned_agents, files_changed, uuid, previous_ids, created_at, updated_at)
		VALUES
			(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	backlogID := toNullString(spec.BacklogID)
	_, err = s.db.ExecContext(ctx, q,
		spec.ID, spec.Title, string(spec.Status), spec.Project,
		backlogID, string(spec.Lane), spec.Scope, spec.BaseSHA, agents, files, spec.UUID, previousIDs,
		formatTime(spec.CreatedAt), formatTime(spec.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("store: create spec from record: %w", err)
	}
	return nil
}

// UpdateSpecFromRecord updates a spec's mutable columns, located by ANCHOR
// (spec.UUID). Unlike UpdateSpecStatus it carries NO optimistic lock (there
// is no "expected from" the importer could supply — the file names only
// the target state) and NEVER writes to spec_history: a spec's history
// arrives from the file's own history section and is merged separately via
// MergeSpecHistory (W4 — the store's own insertHistory is a declared
// no-op, so nothing here may synthesize a row in its place).
// updated_at is written verbatim from spec.UpdatedAt.
//
// Returns model.ErrSpecNotFound when no row carries spec.UUID.
func (s *SDDStore) UpdateSpecFromRecord(ctx context.Context, spec *model.Spec) error {
	agents, err := marshalStringSlice(spec.AssignedAgents)
	if err != nil {
		return fmt.Errorf("store: update spec from record: marshal assigned_agents: %w", err)
	}
	files, err := marshalStringSlice(spec.FilesChanged)
	if err != nil {
		return fmt.Errorf("store: update spec from record: marshal files_changed: %w", err)
	}
	previousIDs, err := marshalPreviousIDs(spec.PreviousIDs)
	if err != nil {
		return fmt.Errorf("store: update spec from record: marshal previous_ids: %w", err)
	}

	const q = `
		UPDATE specs
		SET title = ?, status = ?, backlog_id = ?, lane = ?, scope = ?, base_sha = ?,
		    assigned_agents = ?, files_changed = ?, previous_ids = ?, updated_at = ?
		WHERE uuid = ?`

	backlogID := toNullString(spec.BacklogID)
	res, err := s.db.ExecContext(ctx, q,
		spec.Title, string(spec.Status), backlogID, string(spec.Lane), spec.Scope, spec.BaseSHA,
		agents, files, previousIDs, formatTime(spec.UpdatedAt),
		spec.UUID,
	)
	if err != nil {
		return fmt.Errorf("store: update spec from record: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update spec from record: rows affected: %w", err)
	}
	if n == 0 {
		return model.ErrSpecNotFound
	}
	return nil
}

// MergeSpecHistory merges rows into specID's spec_history, keyed by id (a
// UUIDv7 the file's own history marker carries — D36 put it there
// precisely for this). A row whose id already exists is left UNTOUCHED: a
// history entry is an immutable record of a past event, never updated.
// A row whose id does not exist yet is INSERTED. NEVER DELETEs.
func (s *SDDStore) MergeSpecHistory(ctx context.Context, specID string, rows []*model.SpecHistory) error {
	if len(rows) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: merge spec history: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, h := range rows {
		var exists int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM spec_history WHERE id = ?`, h.ID).Scan(&exists)
		if err == nil {
			continue // already present — a history row is immutable, never updated
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("store: merge spec history: probe %s: %w", h.ID, err)
		}
		_, insErr := tx.ExecContext(ctx,
			`INSERT INTO spec_history (id, spec_id, from_status, to_status, by, reason, at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			h.ID, specID, string(h.FromStatus), string(h.ToStatus), h.By, h.Reason, formatTime(h.At),
		)
		if insErr != nil {
			return fmt.Errorf("store: merge spec history: insert %s: %w", h.ID, insErr)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: merge spec history: commit: %w", err)
	}
	return nil
}

// MergeSpecPushbacks merges rows into specID's spec_pushbacks, keyed by id.
// A row not yet present is INSERTED; a row already present is UPDATED —
// unlike history, a pushback genuinely changes over its lifetime
// (Resolved/Resolution/ResolvedAt move from unresolved to resolved).
// NEVER DELETEs.
func (s *SDDStore) MergeSpecPushbacks(ctx context.Context, specID string, rows []*model.SpecPushback) error {
	if len(rows) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: merge spec pushbacks: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, pb := range rows {
		questions, err := marshalStringSlice(pb.Questions)
		if err != nil {
			return fmt.Errorf("store: merge spec pushbacks: marshal questions %s: %w", pb.ID, err)
		}
		resolved := 0
		if pb.Resolved {
			resolved = 1
		}
		var resolvedAt sql.NullString
		if pb.ResolvedAt != nil {
			resolvedAt = sql.NullString{String: formatTime(*pb.ResolvedAt), Valid: true}
		}

		var exists int
		probeErr := tx.QueryRowContext(ctx, `SELECT 1 FROM spec_pushbacks WHERE id = ?`, pb.ID).Scan(&exists)
		switch {
		case probeErr == sql.ErrNoRows:
			_, insErr := tx.ExecContext(ctx,
				`INSERT INTO spec_pushbacks (id, spec_id, from_agent, questions, resolved, resolution, created_at, resolved_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				pb.ID, specID, pb.FromAgent, questions, resolved, pb.Resolution, formatTime(pb.CreatedAt), resolvedAt,
			)
			if insErr != nil {
				return fmt.Errorf("store: merge spec pushbacks: insert %s: %w", pb.ID, insErr)
			}
		case probeErr != nil:
			return fmt.Errorf("store: merge spec pushbacks: probe %s: %w", pb.ID, probeErr)
		default:
			_, updErr := tx.ExecContext(ctx,
				`UPDATE spec_pushbacks SET resolved = ?, resolution = ?, resolved_at = ?, questions = ? WHERE id = ?`,
				resolved, pb.Resolution, resolvedAt, questions, pb.ID,
			)
			if updErr != nil {
				return fmt.Errorf("store: merge spec pushbacks: update %s: %w", pb.ID, updErr)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: merge spec pushbacks: commit: %w", err)
	}
	return nil
}

// formatTime renders t as RFC3339Nano in UTC — the same wire format every
// other write method in this package uses (store/sdd.go's own inline
// `time.Now().UTC().Format(time.RFC3339Nano)`, factored out here because
// this file formats caller-supplied timestamps, not time.Now(), at every
// call site).
func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// marshalPreviousIDs serialises a []model.PreviousID into the same JSON
// array-of-strings shape unmarshalPreviousIDs (sdd.go) reads back — each
// entry rendered via PreviousID.String() (D44's fixed wire format). Returns
// "" for an empty slice, matching the column's own default (migration 020:
// previous_ids TEXT NOT NULL DEFAULT ''), not "[]" like
// assigned_agents/files_changed.
func marshalPreviousIDs(ids []model.PreviousID) (string, error) {
	if len(ids) == 0 {
		return "", nil
	}
	lines := make([]string, len(ids))
	for i, p := range ids {
		lines[i] = p.String()
	}
	b, err := json.Marshal(lines)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
