package db

import (
	"testing"
	"time"
)

// TestMigration013_Up applies migration 013 on top of a fully migrated v12
// database and verifies that the memory_relations table is created with the
// correct schema and constraints.
func TestMigration013_Up(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 12)

	if err := applyMigration(sqlDB, 13, loadMigration013(t)); err != nil {
		t.Fatalf("migration 013 up: %v", err)
	}

	// Verify schema version reached 13.
	var version int
	if err := sqlDB.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("query schema_version: %v", err)
	}
	if version != 13 {
		t.Errorf("expected schema version 13, got %d", version)
	}

	// Verify the memory_relations table exists by inserting a valid row.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := sqlDB.Exec(`
		INSERT INTO memory_relations (from_id, to_id, relation, judged_by, rationale, created_at)
		VALUES ('mem-a', 'mem-b', 'conflicts_with', 'manual', 'contradicts each other', ?)`, now,
	); err != nil {
		t.Fatalf("insert into memory_relations: %v", err)
	}

	// Verify round-trip.
	var fromID, toID, relation, judgedBy, rationale string
	err := sqlDB.QueryRow(
		`SELECT from_id, to_id, relation, judged_by, rationale FROM memory_relations WHERE from_id = 'mem-a'`,
	).Scan(&fromID, &toID, &relation, &judgedBy, &rationale)
	if err != nil {
		t.Fatalf("read back memory_relation: %v", err)
	}
	if fromID != "mem-a" || toID != "mem-b" || relation != "conflicts_with" || judgedBy != "manual" {
		t.Errorf("unexpected values: from=%q to=%q rel=%q judgedBy=%q", fromID, toID, relation, judgedBy)
	}
}

// TestMigration013_CheckConstraint verifies that inserting a row with an
// invalid relation value fails with a constraint error.
func TestMigration013_CheckConstraint(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 12)

	if err := applyMigration(sqlDB, 13, loadMigration013(t)); err != nil {
		t.Fatalf("migration 013 up: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := sqlDB.Exec(`
		INSERT INTO memory_relations (from_id, to_id, relation, judged_by, created_at)
		VALUES ('x', 'y', 'supersedes', 'manual', ?)`, now,
	)
	if err == nil {
		t.Error("expected CHECK constraint violation for relation='supersedes', got nil error")
	}
}

// TestMigration013_UniqueConstraint verifies that inserting duplicate (from_id,
// to_id) pairs fails, ensuring the idempotency invariant.
func TestMigration013_UniqueConstraint(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 12)

	if err := applyMigration(sqlDB, 13, loadMigration013(t)); err != nil {
		t.Fatalf("migration 013 up: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := sqlDB.Exec(`
		INSERT INTO memory_relations (from_id, to_id, relation, judged_by, created_at)
		VALUES ('p', 'q', 'unrelated', 'cli', ?)`, now,
	); err != nil {
		t.Fatalf("first insert: %v", err)
	}

	_, err := sqlDB.Exec(`
		INSERT INTO memory_relations (from_id, to_id, relation, judged_by, created_at)
		VALUES ('p', 'q', 'conflicts_with', 'manual', ?)`, now,
	)
	if err == nil {
		t.Error("expected UNIQUE constraint violation for duplicate (from_id, to_id), got nil error")
	}
}

// TestMigration013_Indices verifies that the three indices are created.
func TestMigration013_Indices(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 12)

	if err := applyMigration(sqlDB, 13, loadMigration013(t)); err != nil {
		t.Fatalf("migration 013 up: %v", err)
	}

	for _, idx := range []string{
		"idx_memory_relations_from",
		"idx_memory_relations_to",
		"idx_memory_relations_relation",
	} {
		idx := idx
		t.Run(idx, func(t *testing.T) {
			var name string
			if err := sqlDB.QueryRow(
				`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, idx,
			).Scan(&name); err != nil {
				t.Errorf("index %q not found after migration 013: %v", idx, err)
			}
		})
	}
}

// TestMigration013_RunnerIdempotent verifies that a fully migrated database
// (through 013) can run migrate() a second time without error.
func TestMigration013_RunnerIdempotent(t *testing.T) {
	dbConn, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer dbConn.Close()

	if err := migrate(dbConn.DB); err != nil {
		t.Fatalf("second migrate call: %v", err)
	}

	var count int
	if err := dbConn.QueryRow(`SELECT COUNT(*) FROM schema_version WHERE version = 13`).Scan(&count); err != nil {
		t.Fatalf("count schema_version rows for v13: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 schema_version row for v13, got %d", count)
	}
}

// loadMigration013 reads the migration 013 SQL from the embedded FS.
func loadMigration013(t *testing.T) string {
	t.Helper()
	content, err := migrationsFS.ReadFile("migrations/013_memory_relations.sql")
	if err != nil {
		t.Fatalf("read migration 013: %v", err)
	}
	return string(content)
}
