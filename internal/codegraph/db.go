package codegraph

import (
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	// Register the sqlite driver. modernc.org/sqlite is a pure-Go transpilation
	// of SQLite with FTS5 compiled in by default — no CGO, no C compiler, no
	// build tags required.
	_ "modernc.org/sqlite"
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

// QuerylogPath returns the canonical filesystem path for a project's code graph
// adoption querylog (SPEC-083 W1). It lives alongside the codegraph database in
// the same projects directory, distinguished by the "-codegraph-querylog.jsonl"
// suffix, so both the MCP server and the ephemeral pre-tool-use hook resolve it
// identically from cfg.Storage.DataDir/projects + the detected slug.
//
// Example: QuerylogPath("/home/user/.mneme/projects", "myorg-myrepo")
// returns   "/home/user/.mneme/projects/myorg-myrepo-codegraph-querylog.jsonl"
func QuerylogPath(projectsDir, slug string) string {
	return filepath.Join(projectsDir, slug+"-codegraph-querylog.jsonl")
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
		dsn = "file::memory:?_pragma=foreign_keys(ON)"
	} else {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("codegraph: open db: create directory: %w", err)
		}
		dsn = fmt.Sprintf(
			"file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)",
			path,
		)
	}

	sqlDB, err := sql.Open("sqlite", dsn)
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

	// Idempotent column migrations for databases created before schema changes.
	// Each ALTER TABLE is executed individually so we can suppress the
	// "duplicate column name" error that SQLite raises when the column already
	// exists (e.g. because the DB was created by a newer binary with the column
	// in the CREATE TABLE, and the older ALTER path is now redundant).
	if err := applyAlterIdempotent(sqlDB, "nodes", "import_alias", "TEXT DEFAULT NULL"); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("codegraph: open db: alter nodes add import_alias: %w", err)
	}

	return &CodeGraphDB{DB: sqlDB, Path: path}, nil
}

// applyAlterIdempotent adds a column to tableName when it does not already
// exist. It uses PRAGMA table_info to check column existence before issuing the
// ALTER TABLE so that the operation is fully idempotent across all SQLite
// versions (including those that do not distinguish "duplicate column" errors).
func applyAlterIdempotent(db *sql.DB, tableName, columnName, columnDef string) error {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", tableName))
	if err != nil {
		return fmt.Errorf("pragma table_info: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		// PRAGMA table_info columns: cid, name, type, notnull, dflt_value, pk
		var cid int
		var name, colType string
		var notnull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notnull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("scan table_info: %w", err)
		}
		if name == columnName {
			// Column already exists — nothing to do.
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows error: %w", err)
	}

	// Column is absent — add it.
	_, err = db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", tableName, columnName, columnDef))
	if err != nil {
		return fmt.Errorf("alter table: %w", err)
	}
	return nil
}

// Close closes the underlying database connection and releases all associated
// resources. It is safe to call Close on a nil-valued CodeGraphDB receiver;
// subsequent calls return the error from the first close attempt.
func (c *CodeGraphDB) Close() error {
	return c.DB.Close()
}
