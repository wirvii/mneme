package store

import (
	"context"
	"fmt"
	"time"

	"github.com/wirvii/mneme/internal/model"
)

// MemoryRelation represents a directed edge in the memory_relations table.
// It is used for conflicts_with and unrelated pairs; supersedes reuses
// memories.superseded_by + SetSupersededBy.
type MemoryRelation struct {
	// ID is the autoincrement primary key.
	ID int

	// FromID is the lexicographically smaller memory ID of the pair.
	// Pairs are normalised so FromID < ToID before insertion.
	FromID string

	// ToID is the lexicographically larger memory ID of the pair.
	ToID string

	// Relation is one of "conflicts_with" or "unrelated".
	Relation string

	// JudgedBy is "cli" when the relation was determined by the Claude CLI,
	// or "manual" when set by the user. manual always wins on upsert.
	JudgedBy string

	// Rationale is the one-line explanation provided at judgment time.
	Rationale string

	// CreatedAt is the UTC timestamp of the first insert or last replace.
	CreatedAt time.Time
}

// MemoryRelationListOptions parameterises ListMemoryRelations.
type MemoryRelationListOptions struct {
	// Project restricts the results to memory pairs that belong to this project.
	// Both from_id and to_id must exist and be active in the given project.
	// Empty means no filter.
	Project string

	// Relation filters by edge type ("conflicts_with" or "unrelated").
	// Empty means both types are returned.
	Relation string

	// Limit caps the result set. Zero or negative means no cap.
	Limit int
}

// normalizePair ensures a canonical ordering of memory ID pairs so that
// (a,b) and (b,a) map to the same row. The lexicographically smaller ID
// is always placed first.
func normalizePair(a, b string) (from, to string) {
	if a <= b {
		return a, b
	}
	return b, a
}

// CreateMemoryRelation inserts or replaces a memory_relations row for the
// given pair. The pair is normalised (from_id ≤ to_id) before insertion so
// symmetric relations are deduplicated. manual always wins over cli because
// INSERT OR REPLACE unconditionally replaces the existing row — the caller
// should check IsJudged before overwriting a manual judgment.
func (s *MemoryStore) CreateMemoryRelation(ctx context.Context, a, b, relation, judgedBy, rationale string) error {
	from, to := normalizePair(a, b)
	now := time.Now().UTC().Format(time.RFC3339Nano)

	const q = `
		INSERT OR REPLACE INTO memory_relations (from_id, to_id, relation, judged_by, rationale, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`

	if _, err := s.db.ExecContext(ctx, q, from, to, relation, judgedBy, rationale, now); err != nil {
		return fmt.Errorf("store: create memory relation: %w", err)
	}
	return nil
}

// DeleteMemoryRelation removes the relation row for the given pair (in either
// direction). Returns model.ErrNotFound when no such row exists.
func (s *MemoryStore) DeleteMemoryRelation(ctx context.Context, a, b string) error {
	from, to := normalizePair(a, b)

	const q = `DELETE FROM memory_relations WHERE from_id = ? AND to_id = ?`
	res, err := s.db.ExecContext(ctx, q, from, to)
	if err != nil {
		return fmt.Errorf("store: delete memory relation: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete memory relation: rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("store: delete memory relation: %w", model.ErrNotFound)
	}
	return nil
}

// ListMemoryRelations returns memory relation rows filtered by opts.
// When opts.Project is non-empty, only pairs whose from_id exists as an active
// memory in that project are returned (avoids cross-project leaks).
func (s *MemoryStore) ListMemoryRelations(ctx context.Context, opts MemoryRelationListOptions) ([]MemoryRelation, error) {
	var args []any

	q := `
		SELECT mr.id, mr.from_id, mr.to_id, mr.relation, mr.judged_by, mr.rationale, mr.created_at
		FROM memory_relations mr`

	if opts.Project != "" {
		q += `
		JOIN memories m ON m.id = mr.from_id
		  AND m.project = ? AND m.deleted_at IS NULL`
		args = append(args, opts.Project)
	}

	q += ` WHERE 1=1`

	if opts.Relation != "" {
		q += ` AND mr.relation = ?`
		args = append(args, opts.Relation)
	}

	q += ` ORDER BY mr.created_at DESC`

	if opts.Limit > 0 {
		q += ` LIMIT ?`
		args = append(args, opts.Limit)
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list memory relations: %w", err)
	}
	defer rows.Close()

	var results []MemoryRelation
	for rows.Next() {
		var mr MemoryRelation
		var createdAt string
		if err := rows.Scan(&mr.ID, &mr.FromID, &mr.ToID, &mr.Relation, &mr.JudgedBy, &mr.Rationale, &createdAt); err != nil {
			return nil, fmt.Errorf("store: list memory relations: scan: %w", err)
		}
		if t, err := parseTime(createdAt); err == nil {
			mr.CreatedAt = t
		}
		results = append(results, mr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list memory relations: iterate: %w", err)
	}
	return results, nil
}

// GetMemoryConflicts returns the IDs of all memories that have a conflicts_with
// relation with memoryID (in either direction).
func (s *MemoryStore) GetMemoryConflicts(ctx context.Context, memoryID string) ([]string, error) {
	const q = `
		SELECT CASE WHEN from_id = ? THEN to_id ELSE from_id END AS other_id
		FROM memory_relations
		WHERE (from_id = ? OR to_id = ?) AND relation = 'conflicts_with'`

	rows, err := s.db.QueryContext(ctx, q, memoryID, memoryID, memoryID)
	if err != nil {
		return nil, fmt.Errorf("store: get memory conflicts: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: get memory conflicts: scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: get memory conflicts: iterate: %w", err)
	}
	return ids, nil
}

// IsJudged reports whether the pair (a,b) already has a row in memory_relations
// (in either direction). Used as a negative cache to skip already-judged pairs.
func (s *MemoryStore) IsJudged(ctx context.Context, a, b string) (bool, error) {
	from, to := normalizePair(a, b)

	const q = `SELECT COUNT(*) FROM memory_relations WHERE from_id = ? AND to_id = ?`
	var count int
	if err := s.db.QueryRowContext(ctx, q, from, to).Scan(&count); err != nil {
		return false, fmt.Errorf("store: is judged: %w", err)
	}
	return count > 0, nil
}

// FTS5Candidates returns up to limit candidate memory IDs that share salient
// terms with the source memory (identified by sourceID). The query uses the
// precomputed FTS5 candidate query string built by
// internal/conflicts.BuildCandidateQuery from the source's title+content.
//
// Results are scoped to project+global memories (active, non-superseded), self
// is excluded, and pairs already present in memory_relations are skipped
// (negative cache). Results are ordered by BM25 relevance.
func (s *MemoryStore) FTS5Candidates(ctx context.Context, sourceID, ftsQuery, project string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 5
	}
	if ftsQuery == "" {
		return nil, nil
	}

	// Build the WHERE clause dynamically to handle the project filter.
	// We query both project-scoped (matching project) and global-scoped memories
	// while excluding self, deleted, superseded, and already-judged pairs.
	// Positional args: ftsQuery, sourceID [, project], sourceID, sourceID, limit.
	whereProject := ""
	var projectArg []any
	if project != "" {
		whereProject = "AND (m.project = ? OR m.scope IN ('global','org'))"
		projectArg = []any{project}
	}

	q := fmt.Sprintf(`
		SELECT m.id
		FROM memories m
		JOIN memories_fts ON m.rowid = memories_fts.rowid
		WHERE m.deleted_at IS NULL
		  AND m.superseded_by IS NULL
		  AND memories_fts MATCH ?
		  AND m.id != ?
		  %s
		  AND NOT EXISTS (
			SELECT 1 FROM memory_relations mr
			WHERE (mr.from_id = ? AND mr.to_id = m.id)
			   OR (mr.from_id = m.id AND mr.to_id = ?)
		  )
		ORDER BY bm25(memories_fts) ASC
		LIMIT ?`,
		whereProject,
	)

	args := []any{ftsQuery, sourceID}
	args = append(args, projectArg...)
	args = append(args, sourceID, sourceID, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: fts5 candidates: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: fts5 candidates: scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: fts5 candidates: iterate: %w", err)
	}
	return ids, nil
}
