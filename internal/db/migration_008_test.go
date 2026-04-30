package db

import (
	"testing"
)

// TestMigration008_Up applies migration 008 on top of a fully migrated v7
// database and verifies that the idx_memory_entities_entity index exists.
func TestMigration008_Up(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 7)

	if err := applyMigration(sqlDB, 8, loadMigration008(t)); err != nil {
		t.Fatalf("migration 008 up: %v", err)
	}

	// Verify schema version reached 8.
	var version int
	if err := sqlDB.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("query schema_version: %v", err)
	}
	if version != 8 {
		t.Errorf("expected schema version 8, got %d", version)
	}
}

// TestMigration008_IndexExists verifies that the new index is present after
// migration 008 is applied.
func TestMigration008_IndexExists(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 7)

	if err := applyMigration(sqlDB, 8, loadMigration008(t)); err != nil {
		t.Fatalf("migration 008 up: %v", err)
	}

	var name string
	err := sqlDB.QueryRow(
		`SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'idx_memory_entities_entity'`,
	).Scan(&name)
	if err != nil {
		t.Errorf("index idx_memory_entities_entity not found after migration 008: %v", err)
	}
}

// TestMigration008_Idempotent verifies that applying migration 008 twice
// is a no-op (CREATE INDEX IF NOT EXISTS).
func TestMigration008_Idempotent(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 7)

	sql008 := loadMigration008(t)

	if err := applyMigration(sqlDB, 8, sql008); err != nil {
		t.Fatalf("first migration 008: %v", err)
	}
	// Second application should be a no-op due to CREATE INDEX IF NOT EXISTS
	// and INSERT OR IGNORE.
	if err := applyMigration(sqlDB, 8, sql008); err != nil {
		t.Fatalf("second migration 008 (idempotency check): %v", err)
	}

	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM schema_version WHERE version = 8`).Scan(&count); err != nil {
		t.Fatalf("count schema_version rows for v8: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 schema_version row for v8, got %d", count)
	}
}

// TestMigration008_RunnerIdempotent verifies that the migration runner skips
// migration 008 when the database is already at version 8.
func TestMigration008_RunnerIdempotent(t *testing.T) {
	dbConn, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer dbConn.Close()

	// Run a second migration pass — should be a no-op.
	if err := migrate(dbConn.DB); err != nil {
		t.Fatalf("second migrate call: %v", err)
	}

	var count int
	if err := dbConn.QueryRow(`SELECT COUNT(*) FROM schema_version WHERE version = 8`).Scan(&count); err != nil {
		t.Fatalf("count schema_version rows for v8: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 schema_version row for v8, got %d", count)
	}
}

// loadMigration008 reads the migration 008 SQL from the embedded FS.
func loadMigration008(t *testing.T) string {
	t.Helper()
	content, err := migrationsFS.ReadFile("migrations/008_graph_expansion.sql")
	if err != nil {
		t.Fatalf("read migration 008: %v", err)
	}
	return string(content)
}
