package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/wirvii/mneme/internal/model"
)

// SynthesisCounts measures the synthesis memories of this database, one row
// per memories.project ordered by project, for `mneme mem synthesis-sweep`
// (SPEC-187). It counts every synthesis that is not soft-deleted, superseded
// ones included, because those still occupy the database and still count
// for nothing a person wrote. Bytes uses CAST(... AS BLOB) so multibyte text
// is measured in bytes, the unit a context window pays. Database is left
// empty: only the caller knows which file this store was opened on.
func (s *MemoryStore) SynthesisCounts(ctx context.Context) ([]model.SynthesisSweepRow, error) {
	const q = `
		SELECT COALESCE(project, ''),
		       COUNT(*),
		       SUM(CASE WHEN access_count = 0 THEN 1 ELSE 0 END),
		       COALESCE(SUM(LENGTH(CAST(title AS BLOB)) + LENGTH(CAST(content AS BLOB))), 0)
		FROM memories
		WHERE type = 'synthesis' AND deleted_at IS NULL
		GROUP BY COALESCE(project, '')
		ORDER BY COALESCE(project, '')`

	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("store: synthesis counts: %w", err)
	}
	defer rows.Close()

	var out []model.SynthesisSweepRow
	for rows.Next() {
		var r model.SynthesisSweepRow
		if err := rows.Scan(&r.Project, &r.Synthesis, &r.NeverAccessed, &r.Bytes); err != nil {
			return nil, fmt.Errorf("store: synthesis counts: scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: synthesis counts: rows: %w", err)
	}
	return out, nil
}

// SoftDeleteSyntheses retires the active synthesis memories of this
// database in one UPDATE inside a transaction, and returns how many rows it
// touched. It writes deleted_at (the same datetime('now') value SoftDelete
// writes) and nothing else, never on another type, and never deletes a row:
// consolidation removes the retired rows for good only after retention_days.
// projects limits the UPDATE to those memories.project values; an empty
// list means the whole database.
func (s *MemoryStore) SoftDeleteSyntheses(ctx context.Context, projects []string) (int, error) {
	q := `
		UPDATE memories SET deleted_at = datetime('now')
		WHERE type = 'synthesis' AND deleted_at IS NULL`
	args := make([]any, 0, len(projects))
	if len(projects) > 0 {
		q += " AND COALESCE(project, '') IN (" + strings.TrimSuffix(strings.Repeat("?,", len(projects)), ",") + ")"
		for _, p := range projects {
			args = append(args, p)
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: soft delete syntheses: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("store: soft delete syntheses: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: soft delete syntheses: rows affected: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: soft delete syntheses: commit: %w", err)
	}
	return int(n), nil
}

// CountActiveSyntheses counts synthesis memories the way CountActive counts
// memories (not soft-deleted and not superseded), because mneme status uses
// it to warn about the pressure syntheses put on project_budget, which is
// measured with CountActive. The sweep measure, by contrast, includes the
// superseded ones. An empty project counts the whole database.
func (s *MemoryStore) CountActiveSyntheses(ctx context.Context, project string) (int, error) {
	q := `
		SELECT COUNT(*)
		FROM memories
		WHERE type = 'synthesis' AND deleted_at IS NULL AND superseded_by IS NULL`
	args := []any{}
	if project != "" {
		q += " AND project = ?"
		args = append(args, project)
	}

	var n int
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count active syntheses: %w", err)
	}
	return n, nil
}
