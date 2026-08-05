package db

import "testing"

// TestMigration016_Up verifies that migration 016 applies on top of a fully
// migrated v15 database and creates the partial index on memories.session_id.
func TestMigration016_Up(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 15)

	if err := applyMigration(sqlDB, 16, loadMigration016(t)); err != nil {
		t.Fatalf("migration 016 up: %v", err)
	}

	var version int
	if err := sqlDB.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("query schema_version: %v", err)
	}
	if version != 16 {
		t.Errorf("expected schema version 16, got %d", version)
	}
}

// TestMigration016_IndexExists verifies the partial index
// idx_memories_session_id was created by the migration.
func TestMigration016_IndexExists(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 15)

	if err := applyMigration(sqlDB, 16, loadMigration016(t)); err != nil {
		t.Fatalf("migration 016 up: %v", err)
	}

	var count int
	if err := sqlDB.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_memories_session_id'`,
	).Scan(&count); err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	if count != 1 {
		t.Errorf("expected idx_memories_session_id to exist, count=%d", count)
	}
}

// TestMigration016_RunnerIdempotent verifies that a fully migrated database
// (through 016) can run migrate() a second time without error and without
// duplicating the schema_version row.
func TestMigration016_RunnerIdempotent(t *testing.T) {
	dbConn, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer dbConn.Close()

	if err := migrate(dbConn.DB); err != nil {
		t.Fatalf("second migrate call: %v", err)
	}

	var count int
	if err := dbConn.QueryRow(`SELECT COUNT(*) FROM schema_version WHERE version = 16`).Scan(&count); err != nil {
		t.Fatalf("count schema_version rows for v16: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 schema_version row for v16, got %d", count)
	}
}

// loadMigration016 reads the migration 016 SQL from the embedded FS.
func loadMigration016(t *testing.T) string {
	t.Helper()
	content, err := migrationsFS.ReadFile("migrations/016_session_id_index.sql")
	if err != nil {
		t.Fatalf("read migration 016: %v", err)
	}
	return string(content)
}
