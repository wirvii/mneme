package db

import (
	"context"
	"fmt"
)

// Column definitions the repair adds when they are missing. Each one is copied
// literally from the migration that introduces it, so the CREATE TABLE text
// SQLite keeps in sqlite_master ends up identical to a database that ran the
// migrations in order.
const (
	// specsLaneColumn is specs.lane as added by migration 011.
	specsLaneColumn = `lane  TEXT NOT NULL DEFAULT 'standard' CHECK (lane IN ('trivial','standard'))`
	// specsScopeColumn is specs.scope as added by migration 011.
	specsScopeColumn = `scope TEXT NOT NULL DEFAULT ''`
	// specsBaseSHAColumn is specs.base_sha as added by migration 012.
	specsBaseSHAColumn = `base_sha TEXT NOT NULL DEFAULT ''`
	// specsUUIDColumn is specs.uuid as added by migration 019.
	specsUUIDColumn = `uuid TEXT NOT NULL DEFAULT ''`
	// specsPreviousIDsColumn is specs.previous_ids as added by migration 020.
	specsPreviousIDsColumn = `previous_ids TEXT NOT NULL DEFAULT ''`
	// specsUUIDIndex is idx_specs_uuid as created by migration 019. The unique
	// index is partial, so a freshly added all-empty uuid column cannot make
	// it fail on duplicates.
	specsUUIDIndex = `CREATE UNIQUE INDEX IF NOT EXISTS idx_specs_uuid
    ON specs(uuid) WHERE uuid <> ''`
	// specHistoryReviewedSHAColumn is spec_history.reviewed_sha as added by
	// migration 022.
	specHistoryReviewedSHAColumn = `reviewed_sha TEXT NOT NULL DEFAULT ''`
)

// columnDef pairs a column name with the definition ALTER TABLE ADD COLUMN
// needs.
type columnDef struct {
	name       string
	definition string
}

// specsRepairColumns lists the specs columns added after migration 005, in
// the order the migrations add them, so a repaired table has the same column
// order as a new one.
var specsRepairColumns = []columnDef{
	{"lane", specsLaneColumn},
	{"scope", specsScopeColumn},
	{"base_sha", specsBaseSHAColumn},
	{"uuid", specsUUIDColumn},
	{"previous_ids", specsPreviousIDsColumn},
}

// tableExists reports whether the named table is present.
func tableExists(ctx context.Context, ex sqlExecutor, table string) (bool, error) {
	var n int
	err := ex.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check table %s: %w", table, err)
	}
	return n > 0, nil
}

// columnExists reports whether table has a column called column.
func columnExists(ctx context.Context, ex sqlExecutor, table, column string) (bool, error) {
	var n int
	err := ex.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check column %s.%s: %w", table, column, err)
	}
	return n > 0, nil
}

// ensureSpecColumns guarantees that specs has every column added after
// migration 005, plus idx_specs_uuid (SPEC-178 D5). Migration 005 rebuilds
// specs with a fixed column list, so any re-run of it -- the concurrent-open
// race this spec fixes -- silently drops lane, scope, base_sha, uuid and
// previous_ids and leaves schema_version claiming a later version; every
// later Open then failed in ensureSDDUUIDs.
//
// Like ensureMemoryReviewedAt it checks the columns themselves instead of
// trusting MAX(version), because databases touched by old betas hold version
// numbers that say nothing about their shape (BL-240). It only adds columns
// and an index, never drops or rebuilds anything, and does nothing when specs
// is absent or already complete. Existing rows take the column defaults;
// ensureSDDUUIDs then gives them real uuids.
func ensureSpecColumns(ex sqlExecutor) error {
	ctx := context.Background()

	exists, err := tableExists(ctx, ex, "specs")
	if err != nil {
		return fmt.Errorf("db: ensure spec columns: %w", err)
	}
	if !exists {
		return nil
	}

	for _, col := range specsRepairColumns {
		present, err := columnExists(ctx, ex, "specs", col.name)
		if err != nil {
			return fmt.Errorf("db: ensure spec columns: %w", err)
		}
		if present {
			continue
		}
		if _, err := ex.ExecContext(ctx, `ALTER TABLE specs ADD COLUMN `+col.definition); err != nil {
			return fmt.Errorf("db: ensure spec columns: add %s: %w", col.name, err)
		}
	}

	if _, err := ex.ExecContext(ctx, specsUUIDIndex); err != nil {
		return fmt.Errorf("db: ensure spec columns: create idx_specs_uuid: %w", err)
	}
	return nil
}

// ensureSpecHistoryReviewedSHA guarantees spec_history.reviewed_sha exists
// (SPEC-178 D5). A re-run of migration 005 rebuilds spec_history without it,
// and a database below version 22 may also carry it already (BL-272); the
// column check covers both.
func ensureSpecHistoryReviewedSHA(ex sqlExecutor) error {
	ctx := context.Background()

	exists, err := tableExists(ctx, ex, "spec_history")
	if err != nil {
		return fmt.Errorf("db: ensure spec_history reviewed_sha: %w", err)
	}
	if !exists {
		return nil
	}

	present, err := columnExists(ctx, ex, "spec_history", "reviewed_sha")
	if err != nil {
		return fmt.Errorf("db: ensure spec_history reviewed_sha: %w", err)
	}
	if present {
		return nil
	}
	if _, err := ex.ExecContext(ctx, `ALTER TABLE spec_history ADD COLUMN `+specHistoryReviewedSHAColumn); err != nil {
		return fmt.Errorf("db: ensure spec_history reviewed_sha: add column: %w", err)
	}
	return nil
}
