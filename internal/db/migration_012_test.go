package db

import (
	"testing"
	"time"
)

// TestMigration012_EmptyDB verifies that migration 012 applies to a fresh
// database: schema_version reaches 12, the lane_audits table is created,
// and the specs table gains the base_sha column.
func TestMigration012_EmptyDB(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 11)

	if err := applyMigration(sqlDB, 12, loadMigration012(t)); err != nil {
		t.Fatalf("migration 012 on empty DB: %v", err)
	}

	var version int
	if err := sqlDB.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("query schema_version: %v", err)
	}
	if version != 12 {
		t.Errorf("expected schema version 12, got %d", version)
	}

	// Verify that specs.base_sha exists (insert and read back).
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := sqlDB.Exec(`
		INSERT INTO specs (id, title, status, project, assigned_agents, files_changed, lane, scope, base_sha, created_at, updated_at)
		VALUES ('SPEC-001', 'test', 'draft', 'proj', '[]', '[]', 'standard', '', 'abc123', ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatalf("insert spec with base_sha: %v", err)
	}
	var baseSHA string
	if err := sqlDB.QueryRow(`SELECT base_sha FROM specs WHERE id = 'SPEC-001'`).Scan(&baseSHA); err != nil {
		t.Fatalf("select base_sha: %v", err)
	}
	if baseSHA != "abc123" {
		t.Errorf("expected base_sha='abc123', got %q", baseSHA)
	}

	// Verify lane_audits table insert + select roundtrip.
	_, err = sqlDB.Exec(`
		INSERT INTO lane_audits (spec_id, passed, file_count, lines_changed, breaches, base_sha, created_at)
		VALUES ('SPEC-001', 0, 5, 42, 'file count exceeded', 'abc123', ?)`,
		now,
	)
	if err != nil {
		t.Fatalf("insert lane_audit: %v", err)
	}

	var specID string
	var passed, fileCount, linesChanged int
	var breaches, auditBaseSHA, createdAt string
	if err := sqlDB.QueryRow(`
		SELECT spec_id, passed, file_count, lines_changed, breaches, base_sha, created_at
		FROM lane_audits WHERE spec_id = 'SPEC-001'`).Scan(
		&specID, &passed, &fileCount, &linesChanged, &breaches, &auditBaseSHA, &createdAt,
	); err != nil {
		t.Fatalf("select lane_audit: %v", err)
	}
	if specID != "SPEC-001" {
		t.Errorf("spec_id: got %q, want SPEC-001", specID)
	}
	if passed != 0 {
		t.Errorf("passed: got %d, want 0", passed)
	}
	if fileCount != 5 {
		t.Errorf("file_count: got %d, want 5", fileCount)
	}
	if linesChanged != 42 {
		t.Errorf("lines_changed: got %d, want 42", linesChanged)
	}
	if breaches != "file count exceeded" {
		t.Errorf("breaches: got %q, want 'file count exceeded'", breaches)
	}
}

// TestMigration012_ExistingData verifies that migration 012 backfills existing
// specs with base_sha='' via DEFAULT.
func TestMigration012_ExistingData(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 11)

	// Insert a spec using the pre-012 schema (no base_sha column yet).
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := sqlDB.Exec(`
		INSERT INTO specs (id, title, status, project, assigned_agents, files_changed, lane, scope, created_at, updated_at)
		VALUES ('SPEC-OLD', 'existing spec', 'draft', 'proj', '[]', '[]', 'standard', '', ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatalf("insert pre-migration spec: %v", err)
	}

	if err := applyMigration(sqlDB, 12, loadMigration012(t)); err != nil {
		t.Fatalf("migration 012 on DB with existing data: %v", err)
	}

	// Existing spec should have base_sha='' (DEFAULT '').
	var baseSHA string
	if err := sqlDB.QueryRow(`SELECT base_sha FROM specs WHERE id = 'SPEC-OLD'`).Scan(&baseSHA); err != nil {
		t.Fatalf("select base_sha from existing spec: %v", err)
	}
	if baseSHA != "" {
		t.Errorf("expected base_sha='', got %q", baseSHA)
	}
}

// TestMigration012_LaneAuditsShape verifies that two lane_audit rows for the same
// spec can be inserted, and ORDER BY created_at DESC LIMIT 1 returns the latest.
func TestMigration012_LaneAuditsShape(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 12)

	t1 := "2026-01-01T00:00:00Z"
	t2 := "2026-06-01T00:00:00Z"

	_, err := sqlDB.Exec(`
		INSERT INTO lane_audits (spec_id, passed, file_count, lines_changed, breaches, base_sha, created_at)
		VALUES ('SPEC-002', 0, 4, 25, 'file count exceeded', 'sha1', ?)`, t1)
	if err != nil {
		t.Fatalf("insert first audit: %v", err)
	}

	_, err = sqlDB.Exec(`
		INSERT INTO lane_audits (spec_id, passed, file_count, lines_changed, breaches, base_sha, created_at)
		VALUES ('SPEC-002', 1, 2, 10, '', 'sha2', ?)`, t2)
	if err != nil {
		t.Fatalf("insert second audit: %v", err)
	}

	// The latest row (by created_at DESC) should be the passing one.
	var passed int
	var baseSHA string
	if err := sqlDB.QueryRow(`
		SELECT passed, base_sha FROM lane_audits
		WHERE spec_id = 'SPEC-002'
		ORDER BY created_at DESC LIMIT 1`).Scan(&passed, &baseSHA); err != nil {
		t.Fatalf("select latest audit: %v", err)
	}
	if passed != 1 {
		t.Errorf("expected latest audit passed=1, got %d", passed)
	}
	if baseSHA != "sha2" {
		t.Errorf("expected base_sha='sha2', got %q", baseSHA)
	}
}

// loadMigration012 reads the migration 012 SQL from the embedded FS.
func loadMigration012(t *testing.T) string {
	t.Helper()
	content, err := migrationsFS.ReadFile("migrations/012_add_spec_base_sha_and_audits.sql")
	if err != nil {
		t.Fatalf("read migration 012: %v", err)
	}
	return string(content)
}
