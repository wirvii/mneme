package store

import (
	"context"
	"fmt"
	"time"
)

// LatestSpecHistoryAt returns, for every spec of project with at least one
// spec_history row, the most recent `at` of that history (SPEC-186 D1).
//
// The maximum is computed in Go after parsing, never with MAX(at) in SQL:
// `at` is RFC3339Nano text, which is not lexicographically chronological
// (Format trims trailing zeros, and an imported row may carry another UTC
// offset). A row whose `at` cannot be parsed is skipped, not reported: the
// worst outcome is a spec shown as older than it is, a visible false
// positive rather than a hidden failure. A spec without history is absent
// from the map (not mapped to the zero time).
//
// spec_history has no project column, so the filter goes through the specs
// table; that inherits GetSpecHistory's limit (one database per project
// makes a correlative shared by two projects impossible today). Read-only:
// it never modifies a row.
func (s *SDDStore) LatestSpecHistoryAt(ctx context.Context, project string) (map[string]time.Time, error) {
	const q = `
		SELECT h.spec_id, h.at FROM spec_history h
		WHERE h.spec_id IN (SELECT id FROM specs WHERE project = ?)`

	rows, err := s.db.QueryContext(ctx, q, project)
	if err != nil {
		return nil, fmt.Errorf("store: latest spec history at: %w", err)
	}
	defer rows.Close()

	latest := make(map[string]time.Time)
	for rows.Next() {
		var specID, atStr string
		if err := rows.Scan(&specID, &atStr); err != nil {
			return nil, fmt.Errorf("store: latest spec history at: scan: %w", err)
		}
		at, err := parseTime(atStr)
		if err != nil {
			// Skipped by design (SPEC-186 §1): the remaining rows and
			// updated_at still date the spec.
			continue
		}
		if prev, ok := latest[specID]; !ok || at.After(prev) {
			latest[specID] = at
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: latest spec history at: %w", err)
	}
	return latest, nil
}
