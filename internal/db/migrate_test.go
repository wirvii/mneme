package db

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestMigrate_Fresh opens an in-memory database and verifies that migrate
// creates every table and FTS5 virtual table defined in 001_initial.sql.
func TestMigrate_Fresh(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	tables := []string{"memories", "memories_fts", "memory_files", "sessions", "schema_version"}
	for _, table := range tables {
		t.Run("table_"+table, func(t *testing.T) {
			var name string
			err := db.QueryRow(
				`SELECT name FROM sqlite_master WHERE type IN ('table','shadow') AND name = ?`,
				table,
			).Scan(&name)
			if err != nil {
				t.Errorf("table %q not found in sqlite_master: %v", table, err)
			}
		})
	}

	t.Run("schema_version_is_1", func(t *testing.T) {
		var version int
		if err := db.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
			t.Fatalf("query schema_version: %v", err)
		}
		if version != 1 {
			t.Errorf("expected schema version 1, got %d", version)
		}
	})
}

// TestMigrate_Idempotent verifies that calling migrate twice on the same
// database neither returns an error nor inserts duplicate version rows.
func TestMigrate_Idempotent(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Run a second migration pass directly on the underlying *sql.DB.
	if err := migrate(db.DB); err != nil {
		t.Fatalf("second migrate call: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&count); err != nil {
		t.Fatalf("query schema_version count: %v", err)
	}
	// 001_initial.sql inserts version 1 with INSERT OR IGNORE, so even if
	// applyMigration were called twice the row would not be duplicated.
	// With our version-gate logic it should be exactly 1.
	if count != 1 {
		t.Errorf("expected 1 row in schema_version, got %d", count)
	}
}

// TestOpen_CreatesDirectory verifies that Open creates intermediate directories
// in the provided path when they do not yet exist.
func TestOpen_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "nested", "subdir", "mneme.db")

	// The nested directories do not exist yet.
	if _, err := os.Stat(filepath.Dir(dbPath)); !os.IsNotExist(err) {
		t.Fatalf("expected nested dir to be absent before Open")
	}

	opened, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open(%q): %v", dbPath, err)
	}
	defer opened.Close()

	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("expected database file to exist at %q after Open: %v", dbPath, err)
	}
}

// TestOpenMemory verifies that OpenMemory returns a usable database with the
// full schema applied, without touching the filesystem.
func TestOpenMemory(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		t.Fatalf("Ping after OpenMemory: %v", err)
	}

	// Spot-check that the memories table is accessible.
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&count); err != nil {
		t.Fatalf("SELECT COUNT(*) FROM memories: %v", err)
	}
}

// TestFTS5_Works inserts a row into memories and then verifies that the FTS5
// virtual table can find it via a full-text MATCH query. This exercises the
// INSERT trigger (memories_ai) that keeps memories_fts in sync.
func TestFTS5_Works(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	now := time.Now().UTC().Format(time.RFC3339)

	const insert = `
INSERT INTO memories
    (id, type, scope, title, content, created_at, updated_at, importance, confidence, decay_rate)
VALUES
    ('test-id-1', 'discovery', 'project',
     'Test query title for FTS',
     'This content contains the words test and query for full-text search verification.',
     ?, ?, 0.5, 0.8, 0.01)`

	if _, err := db.Exec(insert, now, now); err != nil {
		t.Fatalf("INSERT INTO memories: %v", err)
	}

	var title string
	err = db.QueryRow(
		`SELECT title FROM memories_fts WHERE memories_fts MATCH 'test query'`,
	).Scan(&title)
	if err != nil {
		t.Fatalf("FTS5 MATCH query failed: %v", err)
	}

	if title != "Test query title for FTS" {
		t.Errorf("unexpected title returned by FTS5: %q", title)
	}
}
