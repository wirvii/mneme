package db

import (
	"testing"
	"time"
)

// TestMigration014_EmptyDB verifies that migration 014 applies to a fully
// migrated v13 database and adds the shared/author columns to memories with
// the expected defaults.
func TestMigration014_EmptyDB(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 13)

	if err := applyMigration(sqlDB, 14, loadMigration014(t)); err != nil {
		t.Fatalf("migration 014 up: %v", err)
	}

	var version int
	if err := sqlDB.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("query schema_version: %v", err)
	}
	if version != 14 {
		t.Errorf("expected schema version 14, got %d", version)
	}

	// Verify that a memory can be inserted with an explicit shared/author pair.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := sqlDB.Exec(`
		INSERT INTO memories (id, type, scope, title, content, created_at, updated_at, shared, author)
		VALUES ('mem-1', 'decision', 'project', 'test', 'content', ?, ?, 1, 'Jane Doe <jane@example.com>')`,
		now, now,
	)
	if err != nil {
		t.Fatalf("insert memory with shared=1: %v", err)
	}

	var shared int
	var author string
	if err := sqlDB.QueryRow(`SELECT shared, author FROM memories WHERE id = 'mem-1'`).Scan(&shared, &author); err != nil {
		t.Fatalf("select shared/author: %v", err)
	}
	if shared != 1 {
		t.Errorf("expected shared=1, got %d", shared)
	}
	if author != "Jane Doe <jane@example.com>" {
		t.Errorf("expected author='Jane Doe <jane@example.com>', got %q", author)
	}
}

// TestMigration014_ExistingData verifies that migration 014 backfills existing
// memory rows with shared=0 and author="" via DEFAULT — the inert state that
// keeps current mem_save behaviour unchanged.
func TestMigration014_ExistingData(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 13)

	// Insert a row using the pre-014 schema (no shared/author columns yet).
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := sqlDB.Exec(`
		INSERT INTO memories (id, type, scope, title, content, created_at, updated_at)
		VALUES ('mem-old', 'discovery', 'project', 'existing memory', 'content', ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatalf("insert pre-migration memory: %v", err)
	}

	if err := applyMigration(sqlDB, 14, loadMigration014(t)); err != nil {
		t.Fatalf("migration 014 on DB with existing data: %v", err)
	}

	var shared int
	var author string
	if err := sqlDB.QueryRow(`SELECT shared, author FROM memories WHERE id = 'mem-old'`).Scan(&shared, &author); err != nil {
		t.Fatalf("query backfilled memory: %v", err)
	}
	if shared != 0 {
		t.Errorf("expected shared=0, got %d", shared)
	}
	if author != "" {
		t.Errorf("expected author='', got %q", author)
	}
}

// TestMigration014_CheckConstraint verifies that the CHECK constraint on
// shared rejects values outside {0,1,2}.
func TestMigration014_CheckConstraint(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 14)

	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := sqlDB.Exec(`
		INSERT INTO memories (id, type, scope, title, content, created_at, updated_at, shared)
		VALUES ('mem-bad', 'discovery', 'project', 'bad', 'content', ?, ?, 3)`,
		now, now,
	)
	if err == nil {
		t.Fatal("expected CHECK constraint to reject shared=3, but insert succeeded")
	}
}

// TestMigration014_RunnerIdempotent verifies that a fully migrated database
// (through 014) can run migrate() a second time without error and without
// duplicating the schema_version row.
func TestMigration014_RunnerIdempotent(t *testing.T) {
	dbConn, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer dbConn.Close()

	if err := migrate(dbConn.DB); err != nil {
		t.Fatalf("second migrate call: %v", err)
	}

	var count int
	if err := dbConn.QueryRow(`SELECT COUNT(*) FROM schema_version WHERE version = 14`).Scan(&count); err != nil {
		t.Fatalf("count schema_version rows for v14: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 schema_version row for v14, got %d", count)
	}
}

// loadMigration014 reads the migration 014 SQL from the embedded FS.
func loadMigration014(t *testing.T) string {
	t.Helper()
	content, err := migrationsFS.ReadFile("migrations/014_team_memory.sql")
	if err != nil {
		t.Fatalf("read migration 014: %v", err)
	}
	return string(content)
}
