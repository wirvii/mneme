package db

import (
	"context"
	"fmt"
)

// ensureMemoryReviewedAt guarantees that memories.reviewed_at exists after
// every Open (SPEC-167 D2). It is independent of schema_version on purpose:
// the migration runner skips any file at or below MAX(version), and databases
// touched by old delivery-v2 betas (BL-240) hold versions that would make a
// plain ALTER TABLE migration either never run or run against a column that
// already exists -- and SQLite has no ADD COLUMN IF NOT EXISTS. Checking the
// column itself is the only reliable condition.
//
// When the column is missing it is added and back-filled with updated_at; the
// caller (migrate) already holds the write transaction, so both steps commit
// or roll back together and two processes can no longer both pass the check, so every existing memory starts its review clock at its
// last modification. The column is nullable with no DEFAULT (SQLite does not
// accept a non-constant default on ADD COLUMN); readers use
// COALESCE(reviewed_at, updated_at), so a NULL never breaks anything.
//
// The common case (column already present) costs two cheap catalog queries.
// OpenReadOnly does not migrate and therefore never reaches this.
func ensureMemoryReviewedAt(ex sqlExecutor) error {
	ctx := context.Background()
	var tables int
	if err := ex.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'memories'`).Scan(&tables); err != nil {
		return fmt.Errorf("db: ensure reviewed_at: check memories table: %w", err)
	}
	if tables == 0 {
		return nil
	}

	var columns int
	if err := ex.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('memories') WHERE name = 'reviewed_at'`).Scan(&columns); err != nil {
		return fmt.Errorf("db: ensure reviewed_at: check column: %w", err)
	}
	if columns > 0 {
		return nil
	}

	if _, err := ex.ExecContext(ctx, `ALTER TABLE memories ADD COLUMN reviewed_at TEXT`); err != nil {
		return fmt.Errorf("db: ensure reviewed_at: add column: %w", err)
	}
	if _, err := ex.ExecContext(ctx, `UPDATE memories SET reviewed_at = updated_at WHERE reviewed_at IS NULL`); err != nil {
		return fmt.Errorf("db: ensure reviewed_at: backfill: %w", err)
	}
	return nil
}
