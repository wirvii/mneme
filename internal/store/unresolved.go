package store

import (
	"context"
	"fmt"
	"time"

	"github.com/gofrs/uuid/v5"

	"github.com/juanftp/mneme/internal/model"
)

// RegisterUnresolved records or updates an unresolved wikilink reference.
//
// If a row for (source_memory_id, target_topic_key) already exists, its
// mention_count is incremented by one and last_seen_at is refreshed. Otherwise
// a new row is inserted with mention_count=1. The operation is atomic via
// INSERT ... ON CONFLICT DO UPDATE, preventing duplicates even under concurrent
// saves. The generated ID is discarded on conflict — the existing row retains
// its original ID.
func (s *MemoryStore) RegisterUnresolved(ctx context.Context, ref *model.UnresolvedReference) error {
	const q = `
		INSERT INTO unresolved_references
			(id, source_memory_id, target_topic_key, project, mention_count, first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?, 1, ?, ?)
		ON CONFLICT(source_memory_id, target_topic_key)
		DO UPDATE SET
			mention_count = mention_count + 1,
			last_seen_at  = excluded.last_seen_at`

	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("store: register unresolved: generate id: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx, q,
		id.String(), ref.SourceMemoryID, ref.TargetTopicKey,
		toNullString(ref.Project), now, now,
	)
	if err != nil {
		return fmt.Errorf("store: register unresolved: %w", err)
	}
	return nil
}

// FindUnresolvedByTarget returns all unresolved references whose target_topic_key
// matches topicKey and whose project matches project. Rows are ordered by
// mention_count descending so the most critical gaps appear first.
//
// An empty slice (not nil) is returned when no rows match.
func (s *MemoryStore) FindUnresolvedByTarget(ctx context.Context, topicKey, project string) ([]*model.UnresolvedReference, error) {
	const q = `
		SELECT id, source_memory_id, target_topic_key, COALESCE(project, ''), mention_count, first_seen_at, last_seen_at
		FROM unresolved_references
		WHERE target_topic_key = ?
		  AND project IS ?
		ORDER BY mention_count DESC`

	rows, err := s.db.QueryContext(ctx, q, topicKey, toNullString(project))
	if err != nil {
		return nil, fmt.Errorf("store: find unresolved by target: %w", err)
	}
	defer rows.Close()

	var refs []*model.UnresolvedReference
	for rows.Next() {
		ref, scanErr := scanUnresolvedReference(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("store: find unresolved by target: scan: %w", scanErr)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: find unresolved by target: rows: %w", err)
	}
	if refs == nil {
		refs = []*model.UnresolvedReference{}
	}
	return refs, nil
}

// DeleteUnresolved removes an unresolved reference by its ID. It is a no-op
// (zero rows affected) when no row with that ID exists — callers need not
// treat this as an error.
func (s *MemoryStore) DeleteUnresolved(ctx context.Context, id string) error {
	const q = `DELETE FROM unresolved_references WHERE id = ?`
	if _, err := s.db.ExecContext(ctx, q, id); err != nil {
		return fmt.Errorf("store: delete unresolved: %w", err)
	}
	return nil
}

// DeleteUnresolvedBySourceAndTarget removes a specific (source, target) pair
// without requiring the row's ID. Used when processWikilinks re-runs during an
// Update and the target now exists — the unresolved ref must be cleaned up.
//
// It is a no-op when no matching row exists.
func (s *MemoryStore) DeleteUnresolvedBySourceAndTarget(ctx context.Context, sourceMemoryID, targetTopicKey string) error {
	const q = `
		DELETE FROM unresolved_references
		WHERE source_memory_id = ? AND target_topic_key = ?`
	if _, err := s.db.ExecContext(ctx, q, sourceMemoryID, targetTopicKey); err != nil {
		return fmt.Errorf("store: delete unresolved by source and target: %w", err)
	}
	return nil
}

// ListUnresolved returns all unresolved references for the given project,
// ordered by mention_count descending then last_seen_at descending. When limit
// is zero or negative, 50 results are returned. Pass an empty project string to
// query across all projects in the database.
//
// This method is intended for use by the mem_gaps MCP tool (SPEC-W3).
func (s *MemoryStore) ListUnresolved(ctx context.Context, project string, limit int) ([]*model.UnresolvedReference, error) {
	if limit <= 0 {
		limit = 50
	}

	const q = `
		SELECT id, source_memory_id, target_topic_key, COALESCE(project, ''), mention_count, first_seen_at, last_seen_at
		FROM unresolved_references
		WHERE project IS ?
		ORDER BY mention_count DESC, last_seen_at DESC
		LIMIT ?`

	rows, err := s.db.QueryContext(ctx, q, toNullString(project), limit)
	if err != nil {
		return nil, fmt.Errorf("store: list unresolved: %w", err)
	}
	defer rows.Close()

	var refs []*model.UnresolvedReference
	for rows.Next() {
		ref, scanErr := scanUnresolvedReference(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("store: list unresolved: scan: %w", scanErr)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list unresolved: rows: %w", err)
	}
	if refs == nil {
		refs = []*model.UnresolvedReference{}
	}
	return refs, nil
}

// CountUnresolved returns the total number of unresolved references for the
// given project. Pass an empty project string to count across all projects.
//
// This method is intended for use by the mem_gaps MCP tool (SPEC-W3).
func (s *MemoryStore) CountUnresolved(ctx context.Context, project string) (int, error) {
	const q = `SELECT COUNT(*) FROM unresolved_references WHERE project IS ?`
	var n int
	if err := s.db.QueryRowContext(ctx, q, toNullString(project)).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count unresolved: %w", err)
	}
	return n, nil
}

// scannerRows is satisfied by *sql.Rows for the shared scan helper.
type scannerRows interface {
	Scan(dest ...any) error
}

// scanUnresolvedReference scans a single row into an UnresolvedReference. The
// SELECT must include columns in this order:
// id, source_memory_id, target_topic_key, project, mention_count, first_seen_at, last_seen_at
func scanUnresolvedReference(row scannerRows) (*model.UnresolvedReference, error) {
	var (
		ref          model.UnresolvedReference
		firstSeenStr string
		lastSeenStr  string
	)
	if err := row.Scan(
		&ref.ID,
		&ref.SourceMemoryID,
		&ref.TargetTopicKey,
		&ref.Project,
		&ref.MentionCount,
		&firstSeenStr,
		&lastSeenStr,
	); err != nil {
		return nil, err
	}

	var parseErr error
	ref.FirstSeenAt, parseErr = time.Parse(time.RFC3339Nano, firstSeenStr)
	if parseErr != nil {
		// Fallback to RFC3339 without nanoseconds (stored by older rows).
		ref.FirstSeenAt, parseErr = time.Parse(time.RFC3339, firstSeenStr)
		if parseErr != nil {
			return nil, fmt.Errorf("parse first_seen_at %q: %w", firstSeenStr, parseErr)
		}
	}
	ref.LastSeenAt, parseErr = time.Parse(time.RFC3339Nano, lastSeenStr)
	if parseErr != nil {
		ref.LastSeenAt, parseErr = time.Parse(time.RFC3339, lastSeenStr)
		if parseErr != nil {
			return nil, fmt.Errorf("parse last_seen_at %q: %w", lastSeenStr, parseErr)
		}
	}

	return &ref, nil
}
