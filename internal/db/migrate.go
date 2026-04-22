package db

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrate applies all pending SQL migration files embedded in migrationsFS.
// Migration files must be named with a numeric prefix followed by an underscore,
// e.g. "001_initial.sql". Files are applied in ascending lexicographic order.
// Each file is executed inside its own transaction. If the transaction succeeds,
// a row is inserted into schema_version before the transaction is committed.
//
// migrate is idempotent: calling it on an already-migrated database is a no-op.
// It is called automatically by Open and OpenMemory.
func migrate(db *sql.DB) error {
	// Ensure the schema_version table exists before we query it. This is the
	// bootstrap step that makes migrate safe to call on a brand-new database.
	const createVersion = `
CREATE TABLE IF NOT EXISTS schema_version (
    version    INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
)`
	if _, err := db.Exec(createVersion); err != nil {
		return fmt.Errorf("db: migrate: create schema_version: %w", err)
	}

	// Determine the highest version already applied.
	var current int
	row := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`)
	if err := row.Scan(&current); err != nil {
		return fmt.Errorf("db: migrate: read schema_version: %w", err)
	}

	// Collect all migration files from the embedded FS.
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("db: migrate: read embedded migrations: %w", err)
	}

	// Sort entries by name so migrations always run in order regardless of FS ordering.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		version, err := versionFromFilename(name)
		if err != nil {
			return fmt.Errorf("db: migrate: parse version from %q: %w", name, err)
		}

		if version <= current {
			// Already applied — skip.
			continue
		}

		content, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("db: migrate: read %q: %w", name, err)
		}

		if version == 5 {
			if err := migration005PreFlight(db); err != nil {
				return fmt.Errorf("db: migrate: apply %q: %w", name, err)
			}
		}

		if err := applyMigration(db, version, string(content)); err != nil {
			return fmt.Errorf("db: migrate: apply %q: %w", name, err)
		}
	}

	return nil
}

// applyMigration runs the SQL from a single migration file inside a transaction
// and records the version in schema_version on success. The version row is
// inserted as part of the same transaction so a partial failure leaves the
// database in a clean state.
func applyMigration(db *sql.DB, version int, sqlContent string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	// Always roll back if we return without committing.
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(sqlContent); err != nil {
		return fmt.Errorf("exec sql: %w", err)
	}

	const insertVersion = `
INSERT OR IGNORE INTO schema_version (version, applied_at)
VALUES (?, datetime('now'))`
	if _, err := tx.Exec(insertVersion, version); err != nil {
		return fmt.Errorf("record version %d: %w", version, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}

// versionFromFilename parses the leading numeric segment from a migration
// filename. For example "001_initial.sql" returns 1. The filename must start
// with at least one digit followed by an underscore; any other format is an
// error so that mislabeled files fail loudly rather than being silently skipped.
func versionFromFilename(name string) (int, error) {
	idx := strings.IndexByte(name, '_')
	if idx <= 0 {
		return 0, fmt.Errorf("filename %q does not match expected pattern NNN_*.sql", name)
	}

	n, err := strconv.Atoi(name[:idx])
	if err != nil {
		return 0, fmt.Errorf("filename %q: numeric prefix is not a valid integer: %w", name, err)
	}

	return n, nil
}

// migration005PreFlight checks that no spec ID appears in more than one project
// slug before the composite-PK migration runs. If collisions are found the
// migration is aborted with an actionable error message.
//
// Background: migration 005 changes the specs primary key from a single TEXT
// column (id) to a composite key (project, id). The INSERT … SELECT that moves
// data into the new table would silently succeed even with cross-project ID
// collisions because SQLite's composite PK only requires uniqueness within the
// same (project, id) pair. However, allowing duplicates would contradict the
// intended semantics, so we abort early with a clear message instead.
func migration005PreFlight(db *sql.DB) error {
	// The specs table may not exist yet (e.g. a fresh DB running migrations
	// 001–005 for the first time). Detect its presence before querying.
	const checkTable = `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='specs'`
	var tableCount int
	if err := db.QueryRow(checkTable).Scan(&tableCount); err != nil {
		return fmt.Errorf("migration 005: check specs table: %w", err)
	}
	if tableCount == 0 {
		// Nothing to check — the table doesn't exist yet.
		return nil
	}

	const q = `
SELECT COUNT(*) FROM (
    SELECT id
    FROM specs
    GROUP BY id
    HAVING COUNT(DISTINCT project) > 1
)`
	var n int
	if err := db.QueryRow(q).Scan(&n); err != nil {
		return fmt.Errorf("migration 005: collision check: %w", err)
	}
	if n > 0 {
		return fmt.Errorf(
			"migration 005: cannot apply: found %d spec ID(s) used across multiple project slugs. "+
				"Normalize slugs manually (see memory topic \"bug/spec-id-collision-cross-slug\") before upgrading",
			n,
		)
	}
	return nil
}
