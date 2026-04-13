package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/scoring"
)

// FindDuplicateTitles returns pairs of memory IDs that share the same title
// within the same project. Only active, non-superseded memories are considered.
// Each pair is ordered so that the lexicographically smaller ID is always first
// (a.id < b.id), guaranteeing that every duplicate relationship appears exactly
// once.
//
// When project is non-empty, only memories in that project are considered.
// When project is empty, all memories in the store are examined regardless of
// their project field (useful when a single pipeline instance owns one store).
func (s *MemoryStore) FindDuplicateTitles(ctx context.Context, project string) ([][2]string, error) {
	var (
		rows *sql.Rows
		err  error
	)

	if project == "" {
		const q = `
			SELECT a.id, b.id
			FROM memories a
			JOIN memories b
			  ON a.title = b.title
			 AND a.project IS b.project
			 AND a.id < b.id
			WHERE a.deleted_at IS NULL
			  AND b.deleted_at IS NULL
			  AND a.superseded_by IS NULL
			  AND b.superseded_by IS NULL`
		rows, err = s.db.QueryContext(ctx, q)
	} else {
		const q = `
			SELECT a.id, b.id
			FROM memories a
			JOIN memories b
			  ON a.title = b.title
			 AND a.project IS b.project
			 AND a.id < b.id
			WHERE a.deleted_at IS NULL
			  AND b.deleted_at IS NULL
			  AND a.superseded_by IS NULL
			  AND b.superseded_by IS NULL
			  AND a.project = ?`
		rows, err = s.db.QueryContext(ctx, q, project)
	}

	if err != nil {
		return nil, fmt.Errorf("store: find duplicate titles: %w", err)
	}
	defer rows.Close()

	var pairs [][2]string
	for rows.Next() {
		var a, b string
		if err := rows.Scan(&a, &b); err != nil {
			return nil, fmt.Errorf("store: find duplicate titles: scan: %w", err)
		}
		pairs = append(pairs, [2]string{a, b})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: find duplicate titles: iterate: %w", err)
	}

	return pairs, nil
}

// SetSupersededBy marks memory id as superseded by supersededByID. The
// superseded_by column is set to supersededByID and updated_at is refreshed.
// Returns model.ErrNotFound when no active memory with that id exists.
func (s *MemoryStore) SetSupersededBy(ctx context.Context, id, supersededByID string) error {
	const q = `
		UPDATE memories
		SET superseded_by = ?, updated_at = datetime('now')
		WHERE id = ? AND deleted_at IS NULL`

	res, err := s.db.ExecContext(ctx, q, supersededByID, id)
	if err != nil {
		return fmt.Errorf("store: set superseded by: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: set superseded by: rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("store: set superseded by: %w", model.ErrNotFound)
	}

	return nil
}

// ListByEffectiveImportance returns up to limit active, non-superseded memories
// for the given project, ordered by their effective importance in ascending
// order (lowest effective importance first). The effective importance is
// computed in Go using scoring.EffectiveImportanceAt so the sort is consistent
// with the rest of the scoring subsystem.
//
// An empty project matches all records regardless of project. When limit <= 0
// all matching memories are returned.
func (s *MemoryStore) ListByEffectiveImportance(ctx context.Context, project string, limit int) ([]*model.Memory, error) {
	// Use a large limit to load all active memories, then sort and slice in Go.
	all, err := s.List(ctx, ListOptions{
		Project:           project,
		IncludeSuperseded: false,
		Limit:             1_000_000,
		OrderBy:           "importance ASC",
	})
	if err != nil {
		return nil, fmt.Errorf("store: list by effective importance: %w", err)
	}

	now := time.Now().UTC()

	// Compute effective importance for each memory and sort ascending.
	type scored struct {
		m     *model.Memory
		score float64
	}
	entries := make([]scored, len(all))
	for i, m := range all {
		ref := m.UpdatedAt
		if m.LastAccessed != nil {
			ref = *m.LastAccessed
		}
		entries[i] = scored{m: m, score: scoring.EffectiveImportanceAt(m.Importance, m.DecayRate, ref, now)}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].score < entries[j].score
	})

	if limit > 0 && limit < len(entries) {
		entries = entries[:limit]
	}

	result := make([]*model.Memory, len(entries))
	for i, e := range entries {
		result[i] = e.m
	}

	return result, nil
}
