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
