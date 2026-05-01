package db

import (
	"testing"
)

// TestMigration009_Up applies migration 009 on top of a fully migrated v8
// database and verifies that the unresolved_references table exists.
func TestMigration009_Up(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 8)

	if err := applyMigration(sqlDB, 9, loadMigration009(t)); err != nil {
		t.Fatalf("migration 009 up: %v", err)
	}

	var version int
	if err := sqlDB.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("query schema_version: %v", err)
	}
	if version != 9 {
		t.Errorf("expected schema version 9, got %d", version)
	}
}

// TestMigration009_TableExists verifies that the unresolved_references table
// is present after migration 009.
func TestMigration009_TableExists(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 8)

	if err := applyMigration(sqlDB, 9, loadMigration009(t)); err != nil {
		t.Fatalf("migration 009 up: %v", err)
	}

	var name string
	err := sqlDB.QueryRow(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'unresolved_references'`,
	).Scan(&name)
	if err != nil {
		t.Errorf("table unresolved_references not found after migration 009: %v", err)
	}
}

// TestMigration009_IndicesExist verifies that all three indices are created
// after migration 009.
func TestMigration009_IndicesExist(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 8)

	if err := applyMigration(sqlDB, 9, loadMigration009(t)); err != nil {
		t.Fatalf("migration 009 up: %v", err)
	}

	indices := []string{
		"idx_unresolved_source_target",
		"idx_unresolved_target_key",
		"idx_unresolved_project",
	}
	for _, idx := range indices {
		var idxName string
		err := sqlDB.QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, idx,
		).Scan(&idxName)
		if err != nil {
			t.Errorf("index %q not found after migration 009: %v", idx, err)
		}
	}
}

// TestMigration009_Idempotent verifies that applying migration 009 twice is a
// no-op (CREATE TABLE IF NOT EXISTS, CREATE INDEX IF NOT EXISTS, INSERT OR IGNORE).
func TestMigration009_Idempotent(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 8)

	sql009 := loadMigration009(t)

	if err := applyMigration(sqlDB, 9, sql009); err != nil {
		t.Fatalf("first migration 009: %v", err)
	}
	if err := applyMigration(sqlDB, 9, sql009); err != nil {
		t.Fatalf("second migration 009 (idempotency check): %v", err)
	}

	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM schema_version WHERE version = 9`).Scan(&count); err != nil {
		t.Fatalf("count schema_version rows for v9: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 schema_version row for v9, got %d", count)
	}
}

// TestMigration009_RunnerIdempotent verifies that the migration runner skips
// migration 009 when the database is already fully migrated.
func TestMigration009_RunnerIdempotent(t *testing.T) {
	dbConn, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer dbConn.Close()

	if err := migrate(dbConn.DB); err != nil {
		t.Fatalf("second migrate call: %v", err)
	}

	var count int
	if err := dbConn.QueryRow(`SELECT COUNT(*) FROM schema_version WHERE version = 9`).Scan(&count); err != nil {
		t.Fatalf("count schema_version rows for v9: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 schema_version row for v9, got %d", count)
	}
}

// loadMigration009 reads the migration 009 SQL from the embedded FS.
func loadMigration009(t *testing.T) string {
	t.Helper()
	content, err := migrationsFS.ReadFile("migrations/009_unresolved_references.sql")
	if err != nil {
		t.Fatalf("read migration 009: %v", err)
	}
	return string(content)
}
