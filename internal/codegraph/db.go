package codegraph

import (
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	// Register the sqlite3 driver. CGO_ENABLED=1 is required; mattn/go-sqlite3
	// is the only SQLite driver that reliably supports FTS5 and JSON1 extensions.
	_ "github.com/mattn/go-sqlite3"
)

//go:embed schema.sql
var schemaSQL string

// CodeGraphDB is a thin wrapper around *sql.DB for the codegraph-specific
// SQLite database. It holds the filesystem path alongside the connection so
// callers can derive sibling paths (e.g. for file-size stats) without
// needing to track the path separately.
type CodeGraphDB struct {
	// DB is the underlying database connection. Callers may use it directly
	// for queries not yet abstracted by the store layer.
	DB *sql.DB

	// Path is the absolute filesystem path of the database file.
	// It is set to ":memory:" for in-memory databases.
	Path string
}

// DBPath returns the canonical filesystem path for a project's codegraph
// database. The database lives alongside the project memory database in the
// same projects directory, distinguished by the "-codegraph" suffix.
//
// Example: DBPath("/home/user/.mneme/projects", "myorg-myrepo")
// returns   "/home/user/.mneme/projects/myorg-myrepo-codegraph.db"
func DBPath(projectsDir, slug string) string {
	return filepath.Join(projectsDir, slug+"-codegraph.db")
}

// OpenDB opens (or creates) the codegraph SQLite database at path. The parent
// directory is created with 0755 permissions if it does not exist. Pass
// ":memory:" to open a transient in-memory database (useful for tests).
//
// The connection is configured with:
//   - journal_mode=WAL     — concurrent readers do not block writers.
//   - foreign_keys=ON      — referential integrity is enforced at the DB level.
//   - busy_timeout=5000    — wait up to 5 seconds when the DB is locked.
//   - synchronous=NORMAL   — durable enough for a developer tool, faster than FULL.
//
// The embedded schema.sql is executed after the connection is established.
// All CREATE TABLE / CREATE INDEX / CREATE TRIGGER statements use IF NOT EXISTS
// / INSERT OR IGNORE so the schema application is fully idempotent.
func OpenDB(path string) (*CodeGraphDB, error) {
	var dsn string

	if path == ":memory:" {
		// In-memory databases need the foreign_keys pragma but not WAL mode or
		// busy_timeout (no concurrent access is possible for in-memory DBs).
		dsn = "file::memory:?_foreign_keys=ON"
	} else {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("codegraph: open db: create directory: %w", err)
		}
		dsn = fmt.Sprintf(
			"file:%s?_journal_mode=WAL&_foreign_keys=ON&_busy_timeout=5000&_synchronous=NORMAL",
			path,
		)
	}

	sqlDB, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("codegraph: open db: %w", err)
	}

	if path == ":memory:" {
		// In-memory databases are private to each physical connection. Without
		// this cap the database/sql pool may open multiple connections, each
		// seeing an empty schema. Cap to one connection so all operations share
		// the migrated schema.
		sqlDB.SetMaxOpenConns(1)
	}

	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("codegraph: open db: ping: %w", err)
	}

	if _, err := sqlDB.Exec(schemaSQL); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("codegraph: open db: apply schema: %w", err)
	}

	return &CodeGraphDB{DB: sqlDB, Path: path}, nil
}

// Close closes the underlying database connection and releases all associated
// resources. It is safe to call Close on a nil-valued CodeGraphDB receiver;
// subsequent calls return the error from the first close attempt.
func (c *CodeGraphDB) Close() error {
	return c.DB.Close()
}
