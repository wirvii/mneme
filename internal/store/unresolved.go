package store

import (
	"context"
	"fmt"
	"time"

	"github.com/gofrs/uuid/v5"

	"github.com/wirvii/mneme/internal/model"
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

// ListGaps returns aggregated knowledge gaps from the unresolved_references
// table, grouped by target_topic_key. Results are ordered by total_mentions
// descending, source_count descending as a tiebreaker.
//
// When project is empty, all rows in the database are included regardless of
// project. When minMentions is greater than zero, gaps with a total_mentions
// sum below the threshold are excluded. When limit is zero or negative, 20
// results are returned.
//
// Returns the page of Gap structs (without Samples — callers load those via
// ListGapSamples) and the total count of distinct target_topic_keys before the
// limit was applied.
func (s *MemoryStore) ListGaps(ctx context.Context, project string, limit, minMentions int) ([]model.Gap, int, error) {
	if limit <= 0 {
		limit = 20
	}
	if minMentions <= 0 {
		minMentions = 1
	}

	// Count distinct gaps before applying limit.
	const countQ = `
		SELECT COUNT(DISTINCT target_topic_key)
		FROM unresolved_references
		WHERE project IS ?
		  AND (
		      SELECT SUM(u2.mention_count)
		      FROM unresolved_references u2
		      WHERE u2.target_topic_key = unresolved_references.target_topic_key
		        AND u2.project IS ?
		  ) >= ?`

	var total int
	if err := s.db.QueryRowContext(ctx, countQ, toNullString(project), toNullString(project), minMentions).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: list gaps: count total: %w", err)
	}

	if total == 0 {
		return []model.Gap{}, 0, nil
	}

	const q = `
		SELECT
		    target_topic_key,
		    SUM(mention_count)           AS total_mentions,
		    COUNT(DISTINCT source_memory_id) AS source_count,
		    MIN(first_seen_at)           AS first_seen_at,
		    MAX(last_seen_at)            AS last_seen_at
		FROM unresolved_references
		WHERE project IS ?
		GROUP BY target_topic_key
		HAVING SUM(mention_count) >= ?
		ORDER BY total_mentions DESC, source_count DESC
		LIMIT ?`

	rows, err := s.db.QueryContext(ctx, q, toNullString(project), minMentions, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("store: list gaps: %w", err)
	}
	defer rows.Close()

	var gaps []model.Gap
	for rows.Next() {
		g, scanErr := scanGap(rows)
		if scanErr != nil {
			return nil, 0, fmt.Errorf("store: list gaps: scan: %w", scanErr)
		}
		gaps = append(gaps, g)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("store: list gaps: rows: %w", err)
	}
	if gaps == nil {
		gaps = []model.Gap{}
	}
	return gaps, total, nil
}

// ListGapSamples returns up to maxSamples source memories that reference the
// given target_topic_key, ordered by mention_count descending. Source memories
// that have been soft-deleted (deleted_at IS NOT NULL) are excluded so the
// sample list only contains accessible content.
//
// When maxSamples is zero or negative, 3 samples are returned. When project is
// empty, all projects in the database are searched.
func (s *MemoryStore) ListGapSamples(ctx context.Context, targetTopicKey, project string, maxSamples int) ([]model.GapSample, error) {
	if maxSamples <= 0 {
		maxSamples = 3
	}

	const q = `
		SELECT m.id, m.title, COALESCE(m.topic_key, '')
		FROM unresolved_references ur
		JOIN memories m ON m.id = ur.source_memory_id
		WHERE ur.target_topic_key = ?
		  AND ur.project IS ?
		  AND m.deleted_at IS NULL
		ORDER BY ur.mention_count DESC
		LIMIT ?`

	rows, err := s.db.QueryContext(ctx, q, targetTopicKey, toNullString(project), maxSamples)
	if err != nil {
		return nil, fmt.Errorf("store: list gap samples: %w", err)
	}
	defer rows.Close()

	var samples []model.GapSample
	for rows.Next() {
		var gs model.GapSample
		if scanErr := rows.Scan(&gs.MemoryID, &gs.Title, &gs.TopicKey); scanErr != nil {
			return nil, fmt.Errorf("store: list gap samples: scan: %w", scanErr)
		}
		samples = append(samples, gs)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list gap samples: rows: %w", err)
	}
	if samples == nil {
		samples = []model.GapSample{}
	}
	return samples, nil
}

// CountDistinctGaps returns the total number of distinct target_topic_key
// values in the unresolved_references table for the given project. Pass an
// empty project string to count across all projects in the database.
func (s *MemoryStore) CountDistinctGaps(ctx context.Context, project string) (int, error) {
	const q = `SELECT COUNT(DISTINCT target_topic_key) FROM unresolved_references WHERE project IS ?`
	var n int
	if err := s.db.QueryRowContext(ctx, q, toNullString(project)).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count distinct gaps: %w", err)
	}
	return n, nil
}

// scanGap scans a single row produced by the ListGaps aggregate query into a
// Gap. The SELECT must include columns in this order:
// target_topic_key, total_mentions, source_count, first_seen_at, last_seen_at
func scanGap(row scannerRows) (model.Gap, error) {
	var (
		g            model.Gap
		firstSeenStr string
		lastSeenStr  string
	)
	if err := row.Scan(
		&g.TargetTopicKey,
		&g.TotalMentions,
		&g.SourceCount,
		&firstSeenStr,
		&lastSeenStr,
	); err != nil {
		return model.Gap{}, err
	}

	var parseErr error
	g.FirstSeenAt, parseErr = time.Parse(time.RFC3339Nano, firstSeenStr)
	if parseErr != nil {
		g.FirstSeenAt, parseErr = time.Parse(time.RFC3339, firstSeenStr)
		if parseErr != nil {
			return model.Gap{}, fmt.Errorf("parse first_seen_at %q: %w", firstSeenStr, parseErr)
		}
	}
	g.LastSeenAt, parseErr = time.Parse(time.RFC3339Nano, lastSeenStr)
	if parseErr != nil {
		g.LastSeenAt, parseErr = time.Parse(time.RFC3339, lastSeenStr)
		if parseErr != nil {
			return model.Gap{}, fmt.Errorf("parse last_seen_at %q: %w", lastSeenStr, parseErr)
		}
	}
	return g, nil
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
