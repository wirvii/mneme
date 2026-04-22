package db

import (
	"database/sql"
	"strings"
	"testing"
	"time"
)

// openRawMemory opens a raw in-memory SQLite DB *without* running migrations.
// Used to set up controlled schema states for migration tests.
func openRawMemory(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite3", "file::memory:?_foreign_keys=ON")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return sqlDB
}

// applyUpToVersion runs migrations 001–n on db.
func applyUpToVersion(t *testing.T, sqlDB *sql.DB, n int) {
	t.Helper()

	const createVersion = `
CREATE TABLE IF NOT EXISTS schema_version (
    version    INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
)`
	if _, err := sqlDB.Exec(createVersion); err != nil {
		t.Fatalf("create schema_version: %v", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		version, err := versionFromFilename(entry.Name())
		if err != nil {
			t.Fatalf("parse version from %q: %v", entry.Name(), err)
		}
		if version > n {
			continue
		}
		content, err := migrationsFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatalf("read %q: %v", entry.Name(), err)
		}
		if err := applyMigration(sqlDB, version, string(content)); err != nil {
			t.Fatalf("apply migration %d: %v", version, err)
		}
	}
}

// insertSpecV4 inserts a spec row using the v4 schema (single TEXT PK).
func insertSpecV4(t *testing.T, sqlDB *sql.DB, id, project string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := sqlDB.Exec(
		`INSERT INTO specs (id, title, status, project, assigned_agents, files_changed, created_at, updated_at)
		 VALUES (?, 'Test spec', 'draft', ?, '[]', '[]', ?, ?)`,
		id, project, now, now,
	)
	if err != nil {
		t.Fatalf("insert spec %s/%s: %v", project, id, err)
	}
}

// TestMigration005_EmptyDB verifies that migration 005 applies without error
// on a fresh database (no pre-existing specs).
func TestMigration005_EmptyDB(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 4)

	if err := migration005PreFlight(sqlDB); err != nil {
		t.Fatalf("pre-flight on empty DB should succeed: %v", err)
	}

	if err := applyMigration(sqlDB, 5, loadMigration005(t)); err != nil {
		t.Fatalf("migration 005 on empty DB: %v", err)
	}

	// Verify schema version reached 5.
	var version int
	if err := sqlDB.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("query schema_version: %v", err)
	}
	if version != 5 {
		t.Errorf("expected schema version 5, got %d", version)
	}
}

// TestMigration005_NoCollision verifies that when two projects have specs with
// distinct IDs, migration 005 applies cleanly and all rows survive.
func TestMigration005_NoCollision(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 4)

	// SPEC-001 in proj-a and SPEC-002 in proj-b — no ID collision.
	insertSpecV4(t, sqlDB, "SPEC-001", "proj-a")
	insertSpecV4(t, sqlDB, "SPEC-002", "proj-b")

	if err := migration005PreFlight(sqlDB); err != nil {
		t.Fatalf("pre-flight with no collision should succeed: %v", err)
	}

	if err := applyMigration(sqlDB, 5, loadMigration005(t)); err != nil {
		t.Fatalf("migration 005 with no collision: %v", err)
	}

	// Both specs should still exist after the table rebuild.
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM specs`).Scan(&count); err != nil {
		t.Fatalf("count specs: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 specs after migration, got %d", count)
	}
}

// TestMigration005_WithCollision verifies that migration 005 aborts with the
// expected error message when the same spec ID exists in two different projects.
//
// The v4 schema enforces a TEXT PRIMARY KEY on specs.id, which prevents two
// rows with the same ID even across different projects. Therefore, to simulate
// a collision we create a "specs" table without that strict PK constraint and
// insert the conflicting rows directly. This mirrors the theoretical scenario
// described in the spec (e.g. manual DB manipulation or future schema variants).
func TestMigration005_WithCollision(t *testing.T) {
	sqlDB := openRawMemory(t)

	// Bootstrap schema_version without any migration SQL so we control the
	// specs table shape exactly.
	const setup = `
CREATE TABLE IF NOT EXISTS schema_version (
    version    INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
);
-- Intentionally no PRIMARY KEY on id — simulates a scenario where two rows
-- with the same spec ID but different projects can coexist.
CREATE TABLE specs (
    id              TEXT NOT NULL,
    title           TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'draft',
    project         TEXT NOT NULL,
    backlog_id      TEXT,
    assigned_agents TEXT NOT NULL DEFAULT '[]',
    files_changed   TEXT NOT NULL DEFAULT '[]',
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);`
	if _, err := sqlDB.Exec(setup); err != nil {
		t.Fatalf("setup collision DB: %v", err)
	}

	now := "2024-01-01T00:00:00Z"
	for _, proj := range []string{"proj-a", "proj-b"} {
		_, err := sqlDB.Exec(
			`INSERT INTO specs (id, title, status, project, assigned_agents, files_changed, created_at, updated_at)
			 VALUES ('SPEC-001', 'Collision spec', 'draft', ?, '[]', '[]', ?, ?)`,
			proj, now, now,
		)
		if err != nil {
			t.Fatalf("insert collision row for %s: %v", proj, err)
		}
	}

	err := migration005PreFlight(sqlDB)
	if err == nil {
		t.Fatal("expected pre-flight to fail with collision error, got nil")
	}

	const wantSubstr = "migration 005: cannot apply: found 1 spec ID(s) used across multiple project slugs"
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Errorf("error message %q does not contain expected substring %q", err.Error(), wantSubstr)
	}
}

// TestMigration005_TwoProjectsSameID verifies the primary post-migration property:
// after migration 005, SPEC-001 can exist independently in two different projects.
func TestMigration005_TwoProjectsSameID(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 4)

	// Before migration: only one SPEC-001 exists (in proj-a only, no collision).
	insertSpecV4(t, sqlDB, "SPEC-001", "proj-a")

	if err := migration005PreFlight(sqlDB); err != nil {
		t.Fatalf("pre-flight: %v", err)
	}
	if err := applyMigration(sqlDB, 5, loadMigration005(t)); err != nil {
		t.Fatalf("migration 005: %v", err)
	}

	// After migration: insert SPEC-001 in proj-b — this should now succeed.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := sqlDB.Exec(
		`INSERT INTO specs (id, title, status, project, assigned_agents, files_changed, created_at, updated_at)
		 VALUES ('SPEC-001', 'Spec in proj-b', 'draft', 'proj-b', '[]', '[]', ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatalf("insert SPEC-001 in proj-b after migration: %v", err)
	}

	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM specs WHERE id = 'SPEC-001'`).Scan(&count); err != nil {
		t.Fatalf("count SPEC-001: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 SPEC-001 rows (one per project), got %d", count)
	}
}

// loadMigration005 reads the migration 005 SQL from the embedded FS.
func loadMigration005(t *testing.T) string {
	t.Helper()
	content, err := migrationsFS.ReadFile("migrations/005_spec_pk_by_project.sql")
	if err != nil {
		t.Fatalf("read migration 005: %v", err)
	}
	return string(content)
}
