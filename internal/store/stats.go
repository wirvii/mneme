package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/juanftp/mneme/internal/model"
)

// CountByType returns the number of active (non-deleted, non-superseded) memories
// in the store grouped by MemoryType. The project parameter restricts results to a
// specific project slug; an empty string matches all records.
func (s *MemoryStore) CountByType(ctx context.Context, project string) (map[model.MemoryType]int, error) {
	q := `
		SELECT type, COUNT(*) AS n
		FROM memories
		WHERE deleted_at IS NULL AND superseded_by IS NULL`

	args := []any{}
	if project != "" {
		q += " AND project = ?"
		args = append(args, project)
	}
	q += " GROUP BY type"

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: count by type: %w", err)
	}
	defer rows.Close()

	result := make(map[model.MemoryType]int)
	for rows.Next() {
		var t string
		var n int
		if err := rows.Scan(&t, &n); err != nil {
			return nil, fmt.Errorf("store: count by type: scan: %w", err)
		}
		result[model.MemoryType(t)] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: count by type: iterate: %w", err)
	}

	return result, nil
}

// CountByScope returns the number of active (non-deleted, non-superseded) memories
// in the store grouped by Scope. The project parameter restricts results to a
// specific project slug; an empty string matches all records.
func (s *MemoryStore) CountByScope(ctx context.Context, project string) (map[model.Scope]int, error) {
	q := `
		SELECT scope, COUNT(*) AS n
		FROM memories
		WHERE deleted_at IS NULL AND superseded_by IS NULL`

	args := []any{}
	if project != "" {
		q += " AND project = ?"
		args = append(args, project)
	}
	q += " GROUP BY scope"

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: count by scope: %w", err)
	}
	defer rows.Close()

	result := make(map[model.Scope]int)
	for rows.Next() {
		var sc string
		var n int
		if err := rows.Scan(&sc, &n); err != nil {
			return nil, fmt.Errorf("store: count by scope: scan: %w", err)
		}
		result[model.Scope(sc)] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: count by scope: iterate: %w", err)
	}

	return result, nil
}

// CountSuperseded returns the number of memories that have been superseded
// (superseded_by IS NOT NULL) for the given project. An empty project matches
// all records.
func (s *MemoryStore) CountSuperseded(ctx context.Context, project string) (int, error) {
	q := `
		SELECT COUNT(*)
		FROM memories
		WHERE superseded_by IS NOT NULL AND deleted_at IS NULL`

	args := []any{}
	if project != "" {
		q += " AND project = ?"
		args = append(args, project)
	}

	var n int
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count superseded: %w", err)
	}

	return n, nil
}

// CountForgotten returns the number of soft-deleted memories (deleted_at IS NOT
// NULL) for the given project. An empty project matches all records.
func (s *MemoryStore) CountForgotten(ctx context.Context, project string) (int, error) {
	q := `
		SELECT COUNT(*)
		FROM memories
		WHERE deleted_at IS NOT NULL`

	args := []any{}
	if project != "" {
		q += " AND project = ?"
		args = append(args, project)
	}

	var n int
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count forgotten: %w", err)
	}

	return n, nil
}

// OldestNewest returns the creation timestamps of the oldest and newest active
// (non-deleted, non-superseded) memories for the given project. Both values are
// nil when there are no active memories. An empty project matches all records.
func (s *MemoryStore) OldestNewest(ctx context.Context, project string) (*time.Time, *time.Time, error) {
	q := `
		SELECT MIN(created_at), MAX(created_at)
		FROM memories
		WHERE deleted_at IS NULL AND superseded_by IS NULL`

	args := []any{}
	if project != "" {
		q += " AND project = ?"
		args = append(args, project)
	}

	var minRaw, maxRaw sql.NullString
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&minRaw, &maxRaw); err != nil {
		return nil, nil, fmt.Errorf("store: oldest newest: %w", err)
	}

	var oldest, newest *time.Time
	if minRaw.Valid && minRaw.String != "" {
		if t, err := parseTime(minRaw.String); err == nil {
			oldest = &t
		}
	}
	if maxRaw.Valid && maxRaw.String != "" {
		if t, err := parseTime(maxRaw.String); err == nil {
			newest = &t
		}
	}

	return oldest, newest, nil
}

// AvgImportance returns the mean importance score of all active
// (non-deleted, non-superseded) memories for the given project. Returns 0.0
// when there are no active memories. An empty project matches all records.
func (s *MemoryStore) AvgImportance(ctx context.Context, project string) (float64, error) {
	q := `
		SELECT COALESCE(AVG(importance), 0.0)
		FROM memories
		WHERE deleted_at IS NULL AND superseded_by IS NULL`

	args := []any{}
	if project != "" {
		q += " AND project = ?"
		args = append(args, project)
	}

	var avg float64
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&avg); err != nil {
		return 0, fmt.Errorf("store: avg importance: %w", err)
	}

	return avg, nil
}

// CountActive returns the number of active (non-deleted, non-superseded)
// memories for the given project. An empty project matches all records.
func (s *MemoryStore) CountActive(ctx context.Context, project string) (int, error) {
	q := `
		SELECT COUNT(*)
		FROM memories
		WHERE deleted_at IS NULL AND superseded_by IS NULL`

	args := []any{}
	if project != "" {
		q += " AND project = ?"
		args = append(args, project)
	}

	var n int
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count active: %w", err)
	}

	return n, nil
}

// CountTotal returns the total number of memories (active + superseded +
// forgotten) excluding hard-deleted records for the given project. An empty
// project matches all records.
func (s *MemoryStore) CountTotal(ctx context.Context, project string) (int, error) {
	q := `SELECT COUNT(*) FROM memories`

	args := []any{}
	if project != "" {
		q += " WHERE project = ?"
		args = append(args, project)
	}

	var n int
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count total: %w", err)
	}

	return n, nil
}
