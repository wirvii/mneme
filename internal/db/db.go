// Package db manages SQLite database connections and schema migrations for mneme.
// It provides a thin wrapper around database/sql that ensures proper configuration
// of SQLite pragmas (WAL mode, foreign keys, busy timeout) and handles automatic
// schema migrations on startup.
//
// This package deliberately has no dependency on internal/model or internal/config
// so that it can be used as a low-level building block without pulling in domain
// types.
package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	// Register the sqlite3 driver. CGO_ENABLED=1 is required; mattn/go-sqlite3
	// is the only SQLite driver that reliably supports FTS5 and JSON1 extensions.
	_ "github.com/mattn/go-sqlite3"
)

// DB is a thin wrapper around *sql.DB that guarantees the connection was opened
// with the project-standard SQLite pragmas and that all schema migrations have
// been applied before the caller receives the handle.
type DB struct {
	*sql.DB
}

// Open opens (or creates) a SQLite database at path. The parent directory is
// created with 0755 permissions if it does not exist. The connection is
// configured with the following pragmas before any application code runs:
//
//   - journal_mode=WAL — concurrent readers do not block writers.
//   - foreign_keys=ON — referential integrity is enforced at the DB level.
//   - busy_timeout=5000 — wait up to 5 seconds when the DB is locked.
//   - synchronous=NORMAL — durable enough for a developer tool, faster than FULL.
//
// Open also runs all pending schema migrations automatically. If any step fails
// the underlying sql.DB is closed before the error is returned.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("db: open: create directory: %w", err)
	}

	dsn := fmt.Sprintf(
		"file:%s?_journal_mode=WAL&_foreign_keys=ON&_busy_timeout=5000&_synchronous=NORMAL",
		path,
	)

	sqlDB, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("db: open: %w", err)
	}

	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("db: open: ping: %w", err)
	}

	if err := migrate(sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("db: open: %w", err)
	}

	return &DB{sqlDB}, nil
}

// OpenMemory opens an in-memory SQLite database. The database exists only for
// the lifetime of the returned *DB and is not shared between calls. It is
// intended for tests that need a fully migrated schema without touching the
// filesystem.
//
// Foreign key enforcement is enabled; WAL mode and busy_timeout are omitted
// because in-memory databases are inherently single-process.
func OpenMemory() (*DB, error) {
	sqlDB, err := sql.Open("sqlite3", "file::memory:?_foreign_keys=ON")
	if err != nil {
		return nil, fmt.Errorf("db: open memory: %w", err)
	}

	// SQLite in-memory databases are private to each physical connection.
	// The database/sql pool can open multiple physical connections, each with
	// its own empty database. Restrict the pool to a single connection so that
	// all operations see the migrated schema.
	sqlDB.SetMaxOpenConns(1)

	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("db: open memory: ping: %w", err)
	}

	if err := migrate(sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("db: open memory: %w", err)
	}

	return &DB{sqlDB}, nil
}

// Close closes the underlying database connection and releases all associated
// resources. It is safe to call Close multiple times; subsequent calls return
// the error from the first close attempt.
func (db *DB) Close() error {
	return db.DB.Close()
}

// SchemaVersion returns the highest migration version recorded in the
// schema_version table of the database at path. It opens a read-only
// connection, queries the table, and closes the connection immediately without
// running any migrations.
//
// Returns 0 (not an error) when:
//   - The file at path does not exist.
//   - The schema_version table has not been created yet.
//
// Any other failure (e.g. the file is not a valid SQLite database) is returned
// as an error.
func SchemaVersion(path string) (int, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return 0, nil
	}

	dsn := fmt.Sprintf("file:%s?mode=ro&_foreign_keys=ON", path)
	sqlDB, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return 0, fmt.Errorf("db: schema version: open: %w", err)
	}
	defer sqlDB.Close()

	var version int
	row := sqlDB.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`)
	if err := row.Scan(&version); err != nil {
		// The table might not exist yet; treat that as version 0.
		if isNoSuchTable(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("db: schema version: query: %w", err)
	}
	return version, nil
}

// OpenReadOnly opens an existing SQLite database at path in read-only mode.
// Unlike Open it does NOT create the directory, run migrations, or enable WAL
// mode — this is intentional: read-only connections are used by short-lived
// processes (e.g. hook handlers) where write setup overhead would hurt latency.
//
// The connection is configured with:
//   - mode=ro — SQLite read-only URI parameter; returns an error if the file
//     does not exist instead of creating it.
//   - _foreign_keys=ON — referential integrity is still enforced for SELECT
//     queries that trigger cascades.
//   - _busy_timeout=1000 — wait at most 1 second when the DB is locked by a
//     concurrent writer, then fail fast so the hook can fall back to allow.
//
// The caller must call Close() on the returned *DB when done.
func OpenReadOnly(path string) (*DB, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("db: open read-only: file does not exist: %s", path)
	}

	dsn := fmt.Sprintf(
		"file:%s?mode=ro&_foreign_keys=ON&_busy_timeout=1000",
		path,
	)

	sqlDB, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("db: open read-only: %w", err)
	}

	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("db: open read-only: ping: %w", err)
	}

	return &DB{sqlDB}, nil
}

// isNoSuchTable reports whether err is a SQLite "no such table" error.
func isNoSuchTable(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "no such table")
}
