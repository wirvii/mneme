package db

import (
	"os"
	"path/filepath"
	"strings"
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

	tables := []string{
		"memories", "memories_fts", "memory_files", "sessions", "schema_version",
		// Tables introduced by 002_knowledge_graph.sql.
		"entities", "relations", "memory_entities",
		// Tables introduced by 003_embeddings.sql.
		"embeddings",
		// Tables introduced by 004_sdd.sql.
		"backlog_items", "specs", "spec_history", "spec_pushbacks",
		// Tables introduced by 009_unresolved_references.sql.
		"unresolved_references",
		// Tables introduced by 010_communities.sql.
		"communities", "community_members",
		// Tables introduced by 012_add_spec_base_sha_and_audits.sql.
		"lane_audits",
		// Tables introduced by 013_memory_relations.sql (SPEC-039).
		"memory_relations",
	}
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

	t.Run("schema_version_is_16", func(t *testing.T) {
		var version int
		if err := db.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
			t.Fatalf("query schema_version: %v", err)
		}
		if version != 16 {
			t.Errorf("expected schema version 16, got %d", version)
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
	// Each migration file inserts one row with INSERT OR IGNORE, so there
	// should be exactly one row per applied migration — currently 16.
	// A second call to migrate must not insert duplicate rows.
	if count != 16 {
		t.Errorf("expected 16 rows in schema_version, got %d", count)
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

	// bm25() is the FTS5 ranking function used by internal/store/search.go and
	// internal/store/conflicts.go. Under modernc.org/sqlite this must remain
	// available and produce a real (non-zero) score, exactly as it did under
	// the previous CGO-based driver — this is the paridad-FTS5 guardrail
	// called out by AC4.
	var score float64
	err = db.QueryRow(
		`SELECT bm25(memories_fts) FROM memories_fts WHERE memories_fts MATCH 'test query'`,
	).Scan(&score)
	if err != nil {
		t.Fatalf("FTS5 bm25() query failed: %v", err)
	}
	if score == 0 {
		t.Errorf("expected a non-zero bm25() score, got %v", score)
	}
}

// TestPragmas_Effective verifies that the DSN pragma translation for modernc
// (`_pragma=<name>(<val>)`) actually takes effect on the connection, not just
// that sql.Open succeeds. journal_mode and foreign_keys are the two pragmas
// whose silent failure would be most dangerous (R2 in SPEC-070): a lost WAL
// mode changes locking behaviour, and lost foreign_keys silently drops
// referential integrity.
func TestPragmas_Effective(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "pragma-check.db")

	opened, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open(%q): %v", dbPath, err)
	}
	defer opened.Close()

	var journalMode string
	if err := opened.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		t.Errorf("expected journal_mode=WAL, got %q", journalMode)
	}

	var foreignKeys int
	if err := opened.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("expected foreign_keys=1 (ON), got %d", foreignKeys)
	}
}
