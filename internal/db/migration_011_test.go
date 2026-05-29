package db

import (
	"testing"
	"time"
)

// TestMigration011_EmptyDB verifies that migration 011 applies to a fresh
// database and sets schema_version to 11.
func TestMigration011_EmptyDB(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 10)

	if err := applyMigration(sqlDB, 11, loadMigration011(t)); err != nil {
		t.Fatalf("migration 011 on empty DB: %v", err)
	}

	var version int
	if err := sqlDB.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("query schema_version: %v", err)
	}
	if version != 11 {
		t.Errorf("expected schema version 11, got %d", version)
	}

	// Verify that a backlog item with lane='trivial' can be inserted.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := sqlDB.Exec(`
		INSERT INTO backlog_items (id, title, description, status, priority, project,
			spec_id, archive_reason, position, lane, scope, created_at, updated_at)
		VALUES ('BL-001','test','','raw','medium','proj','','',0,'trivial','internal/**',?,?)`,
		now, now,
	)
	if err != nil {
		t.Fatalf("insert backlog item with lane=trivial: %v", err)
	}
}

// TestMigration011_ExistingData verifies that migration 011 backfills existing
// rows with lane='standard' and scope='' via DEFAULT values.
func TestMigration011_ExistingData(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 10)

	// Insert rows using the pre-011 schema (no lane/scope columns yet).
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := sqlDB.Exec(`
		INSERT INTO backlog_items (id, title, description, status, priority, project,
			spec_id, archive_reason, position, created_at, updated_at)
		VALUES ('BL-001','existing item','','raw','medium','proj','','',0,?,?)`,
		now, now,
	)
	if err != nil {
		t.Fatalf("insert pre-migration backlog item: %v", err)
	}

	_, err = sqlDB.Exec(`
		INSERT INTO specs (id, title, status, project, assigned_agents, files_changed, created_at, updated_at)
		VALUES ('SPEC-001','existing spec','draft','proj','[]','[]',?,?)`,
		now, now,
	)
	if err != nil {
		t.Fatalf("insert pre-migration spec: %v", err)
	}

	if err := applyMigration(sqlDB, 11, loadMigration011(t)); err != nil {
		t.Fatalf("migration 011 on DB with existing data: %v", err)
	}

	// Existing backlog item should have been backfilled with lane='standard', scope=''.
	var lane, scope string
	if err := sqlDB.QueryRow(`SELECT lane, scope FROM backlog_items WHERE id='BL-001'`).Scan(&lane, &scope); err != nil {
		t.Fatalf("query backfilled backlog item: %v", err)
	}
	if lane != "standard" {
		t.Errorf("expected lane='standard', got %q", lane)
	}
	if scope != "" {
		t.Errorf("expected scope='', got %q", scope)
	}

	// Existing spec should have been backfilled with lane='standard', scope=''.
	if err := sqlDB.QueryRow(`SELECT lane, scope FROM specs WHERE id='SPEC-001'`).Scan(&lane, &scope); err != nil {
		t.Fatalf("query backfilled spec: %v", err)
	}
	if lane != "standard" {
		t.Errorf("expected lane='standard' for spec, got %q", lane)
	}
	if scope != "" {
		t.Errorf("expected scope='' for spec, got %q", scope)
	}
}

// TestMigration011_CheckConstraint verifies that the CHECK constraint on lane
// rejects values outside {trivial, standard}.
func TestMigration011_CheckConstraint(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 11)

	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := sqlDB.Exec(`
		INSERT INTO backlog_items (id, title, description, status, priority, project,
			spec_id, archive_reason, position, lane, scope, created_at, updated_at)
		VALUES ('BL-BAD','bad','','raw','medium','proj','','',0,'invalid','',?,?)`,
		now, now,
	)
	if err == nil {
		t.Fatal("expected CHECK constraint to reject lane='invalid', but insert succeeded")
	}
}

// loadMigration011 reads the migration 011 SQL from the embedded FS.
func loadMigration011(t *testing.T) string {
	t.Helper()
	content, err := migrationsFS.ReadFile("migrations/011_add_lane.sql")
	if err != nil {
		t.Fatalf("read migration 011: %v", err)
	}
	return string(content)
}
