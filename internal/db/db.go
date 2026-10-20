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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	// Register the sqlite driver. modernc.org/sqlite is a pure-Go transpilation
	// of SQLite with FTS5 compiled in by default — no CGO, no C compiler, no
	// build tags required.
	_ "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const (
	// busyTimeoutMillis is the busy_timeout of every read-write connection.
	// It is a single constant so the DSN and openPingDeadline cannot drift
	// apart.
	busyTimeoutMillis = 5000

	// openPingDeadline bounds how long Open keeps retrying Ping on
	// SQLITE_BUSY. It equals busy_timeout: waiting longer than SQLite itself
	// would wait for any other lock would only hide a stuck writer.
	openPingDeadline = busyTimeoutMillis * time.Millisecond

	// openPingRetryInterval is the pause between Ping attempts. Short on
	// purpose: the contended step (switching a new file to WAL) takes
	// milliseconds, and the busy handler cannot do this waiting for us.
	openPingRetryInterval = 10 * time.Millisecond

	// sqliteResultCodeMask extracts the primary result code from an extended
	// one (SQLITE_BUSY_RECOVERY & mask == SQLITE_BUSY).
	sqliteResultCodeMask = 0xff
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
// Switching a brand-new file to WAL needs a brief exclusive lock that SQLite
// does not wait for with busy_timeout: it fails at once with SQLITE_BUSY if
// another process is creating or migrating the same file. Open therefore
// retries Ping while the error is SQLITE_BUSY, every 10 ms for at most the
// busy_timeout (SPEC-178 D1b); any other error returns immediately.
//
// Open also runs all pending schema migrations automatically. If any step fails
// the underlying sql.DB is closed before the error is returned.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("db: open: create directory: %w", err)
	}

	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(%d)&_pragma=synchronous(NORMAL)",
		path, busyTimeoutMillis,
	)

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("db: open: %w", err)
	}

	if err := pingWithRetry(sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("db: open: ping: %w", err)
	}

	if err := migrate(sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("db: open: %w", err)
	}

	return &DB{sqlDB}, nil
}

// pingWithRetry pings sqlDB, retrying while the driver reports SQLITE_BUSY
// until openPingDeadline has passed. A failed connect is not kept by the pool,
// so each attempt opens a fresh connection and re-runs the DSN pragmas. The
// last error is returned when the deadline is exhausted.
func pingWithRetry(sqlDB *sql.DB) error {
	deadline := time.Now().Add(openPingDeadline)
	for {
		err := sqlDB.Ping()
		if err == nil || !isBusy(err) || !time.Now().Before(deadline) {
			return err
		}
		time.Sleep(openPingRetryInterval)
	}
}

// isBusy reports whether err carries the SQLite primary result code
// SQLITE_BUSY, including extended codes such as SQLITE_BUSY_RECOVERY. It
// inspects the driver's result code (modernc's *sqlite.Error exposes Code())
// and never the message text, which is not a stable contract.
func isBusy(err error) bool {
	var coded interface{ Code() int }
	if !errors.As(err, &coded) {
		return false
	}
	return coded.Code()&sqliteResultCodeMask == sqlite3.SQLITE_BUSY
}

// OpenMemory opens an in-memory SQLite database. The database exists only for
// the lifetime of the returned *DB and is not shared between calls. It is
// intended for tests that need a fully migrated schema without touching the
// filesystem.
//
// Foreign key enforcement is enabled; WAL mode and busy_timeout are omitted
// because in-memory databases are inherently single-process.
func OpenMemory() (*DB, error) {
	sqlDB, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(ON)")
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

	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=foreign_keys(ON)", path)
	sqlDB, err := sql.Open("sqlite", dsn)
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
		"file:%s?mode=ro&_pragma=foreign_keys(ON)&_pragma=busy_timeout(1000)",
		path,
	)

	sqlDB, err := sql.Open("sqlite", dsn)
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
