package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// ErrMigrationAlreadyRecorded is returned by applyMigration when schema_version
// already holds the version it was asked to apply. Migrations are not
// idempotent (an ALTER TABLE ADD COLUMN fails the second time, migration 005
// rebuilds tables), so a version that is recorded must never run again; the
// runner reports it instead of recording it silently (SPEC-178 D3).
var ErrMigrationAlreadyRecorded = errors.New("db: migration version already recorded")

// sqlExecutor is the subset of database/sql the migration helpers need. It is
// satisfied by *sql.DB, *sql.Conn and *sql.Tx, so the same helper can run
// against the pool (tests, read-only checks) or against the single dedicated
// connection that holds the migration write lock (SPEC-178 D1). Accepting the
// interface instead of *sql.DB is what lets migrate guarantee that every
// statement of one migration pass travels through the same physical
// connection and therefore the same transaction.
type sqlExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// migrate applies all pending SQL migration files embedded in migrationsFS.
// Migration files must be named with a numeric prefix followed by an underscore,
// e.g. "001_initial.sql". Files are applied in ascending lexicographic order.
//
// The whole pass -- reading the current version, applying every pending file,
// recording their versions and the self-healing checks -- runs on one
// dedicated connection inside a single BEGIN IMMEDIATE transaction (SPEC-178
// D1). IMMEDIATE takes the write lock when the transaction starts, so a second
// process opening the same database waits (up to busy_timeout) and, once
// inside, reads a version that already includes the first process's work
// instead of a stale one read outside any lock. If any step fails the whole
// pass is rolled back and the database is left exactly as it was.
//
// migrate is idempotent: calling it on an already-migrated database is a no-op.
// It is called automatically by Open and OpenMemory.
func migrate(db *sql.DB) error {
	return migrateFS(context.Background(), db, migrationsFS)
}

// migrateFS is migrate with the migration source injected, so tests can feed
// it a fstest.MapFS holding a deliberately broken migration.
//
// Nothing inside may touch db directly: BEGIN IMMEDIATE only protects
// statements that travel through the same physical connection, and
// OpenMemory pins the pool to a single connection, so a stray db.Exec while
// conn is checked out would wait forever.
func migrateFS(ctx context.Context, db *sql.DB, fsys fs.FS) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("db: migrate: take connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("db: migrate: begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			// Best effort: the original error is the one worth reporting.
			_, _ = conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
		}
	}()

	if err := migrateLocked(ctx, conn, fsys); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("db: migrate: commit: %w", err)
	}
	committed = true
	return nil
}

// migrateLocked is the body of a migration pass; the caller holds the write
// lock and owns the transaction on ex.
func migrateLocked(ctx context.Context, ex sqlExecutor, fsys fs.FS) error {
	// Ensure the schema_version table exists before we query it. This is the
	// bootstrap step that makes migrate safe to call on a brand-new database.
	const createVersion = `
CREATE TABLE IF NOT EXISTS schema_version (
    version    INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
)`
	if _, err := ex.ExecContext(ctx, createVersion); err != nil {
		return fmt.Errorf("db: migrate: create schema_version: %w", err)
	}

	// Determine the highest version already applied. Read inside the write
	// lock: this is the value that decides what to apply.
	var current int
	row := ex.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_version`)
	if err := row.Scan(&current); err != nil {
		return fmt.Errorf("db: migrate: read schema_version: %w", err)
	}

	if err := applyPending(ctx, ex, fsys, current); err != nil {
		return err
	}

	// SPEC-178 D5: put back the columns a re-run of migration 005 may have
	// dropped. Must precede ensureSDDUUIDs, which reads specs.uuid.
	if err := ensureSpecColumns(ex); err != nil {
		return fmt.Errorf("db: migrate: %w", err)
	}
	if err := ensureSpecHistoryReviewedSHA(ex); err != nil {
		return fmt.Errorf("db: migrate: %w", err)
	}

	// SPEC-128 D7 mitad A: the SDD anchor invariant is self-healing, not a
	// one-shot migration side effect. See ensureSDDUUIDs' own godoc.
	if err := ensureSDDUUIDs(ex); err != nil {
		return fmt.Errorf("db: migrate: ensure sdd uuids: %w", err)
	}

	// SPEC-167 D2: the reviewed_at column is guaranteed by its own check, not
	// by a migration -- see ensureMemoryReviewedAt's godoc.
	if err := ensureMemoryReviewedAt(ex); err != nil {
		return fmt.Errorf("db: migrate: ensure reviewed_at: %w", err)
	}

	return nil
}

// applyPending applies, in filename order, every migration in fsys whose
// version is above current.
func applyPending(ctx context.Context, ex sqlExecutor, fsys fs.FS, current int) error {
	// Collect all migration files from the embedded FS.
	entries, err := fs.ReadDir(fsys, "migrations")
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

		content, err := fs.ReadFile(fsys, "migrations/"+name)
		if err != nil {
			return fmt.Errorf("db: migrate: read %q: %w", name, err)
		}

		if version == 5 {
			if err := migration005PreFlight(ex); err != nil {
				return fmt.Errorf("db: migrate: apply %q: %w", name, err)
			}
		}

		if version == 22 {
			present, err := migration022PreFlight(ctx, ex)
			if err != nil {
				return fmt.Errorf("db: migrate: apply %q: %w", name, err)
			}
			if present {
				// The file's only effect already exists: register the
				// version without running it.
				if err := recordVersion(ctx, ex, version); err != nil {
					return fmt.Errorf("db: migrate: apply %q: %w", name, err)
				}
				continue
			}
		}

		if err := applyMigration(ex, version, string(content)); err != nil {
			return fmt.Errorf("db: migrate: apply %q: %w", name, err)
		}
	}
	return nil
}

// migration022PreFlight reports whether spec_history.reviewed_sha already
// exists before migration 022 runs. The file's only effect is that ALTER
// TABLE ADD COLUMN, which SQLite cannot make conditional, so on a database
// below version 22 that already holds the column (BL-272: added by hand or by
// an old beta whose version numbers the runner no longer trusts, BL-240) it
// would abort Open with "duplicate column name". When the column is present
// the runner records the version without executing the file, which is left
// untouched because migration files are history.
func migration022PreFlight(ctx context.Context, ex sqlExecutor) (bool, error) {
	var columns int
	err := ex.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('spec_history') WHERE name = 'reviewed_sha'`).Scan(&columns)
	if err != nil {
		return false, fmt.Errorf("migration 022: check reviewed_sha column: %w", err)
	}
	return columns > 0, nil
}

// applyMigration runs the SQL from a single migration file and records the
// version in schema_version. It does not open a transaction: it runs inside
// the one migrateFS holds, so a failure anywhere in the pass undoes every
// file applied before it.
//
// A version that is already recorded is never executed again: the function
// returns ErrMigrationAlreadyRecorded. The shipped files record their own
// version with an idempotent insert of their own; when a file did not (or the
// caller passes SQL that does not), the version is recorded here with a plain
// INSERT, so a duplicate surfaces as an error instead of being ignored.
func applyMigration(ex sqlExecutor, version int, sqlContent string) error {
	ctx := context.Background()

	recorded, err := versionRecorded(ctx, ex, version)
	if err != nil {
		return err
	}
	if recorded {
		return fmt.Errorf("version %d: %w", version, ErrMigrationAlreadyRecorded)
	}

	if _, err := ex.ExecContext(ctx, sqlContent); err != nil {
		return fmt.Errorf("exec sql: %w", err)
	}

	// The file may have recorded its own version inside this same
	// transaction; that counts as recorded by the transaction that applied it.
	recorded, err = versionRecorded(ctx, ex, version)
	if err != nil {
		return err
	}
	if recorded {
		return nil
	}
	return recordVersion(ctx, ex, version)
}

// versionRecorded reports whether schema_version holds a row for version.
func versionRecorded(ctx context.Context, ex sqlExecutor, version int) (bool, error) {
	var recorded bool
	err := ex.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_version WHERE version = ?)`, version).Scan(&recorded)
	if err != nil {
		return false, fmt.Errorf("check version %d: %w", version, err)
	}
	return recorded, nil
}

// recordVersion inserts the schema_version row for version. It is a plain
// INSERT on purpose: a primary-key clash means the version was recorded
// without being applied by this transaction, which must not pass silently.
func recordVersion(ctx context.Context, ex sqlExecutor, version int) error {
	const insertVersion = `INSERT INTO schema_version (version, applied_at) VALUES (?, datetime('now'))`
	if _, err := ex.ExecContext(ctx, insertVersion, version); err != nil {
		return fmt.Errorf("record version %d: %w", version, err)
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
func migration005PreFlight(ex sqlExecutor) error {
	// The specs table may not exist yet (e.g. a fresh DB running migrations
	// 001–005 for the first time). Detect its presence before querying.
	const checkTable = `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='specs'`
	var tableCount int
	if err := ex.QueryRowContext(context.Background(), checkTable).Scan(&tableCount); err != nil {
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
	if err := ex.QueryRowContext(context.Background(), q).Scan(&n); err != nil {
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
