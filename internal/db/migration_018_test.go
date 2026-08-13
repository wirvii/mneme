package db

import (
	"testing"
	"time"
)

// TestMigration018_CreatesQualityTables verifies that migration 018 applies
// on top of a fully migrated v17 database and creates quality_certificates
// and quality_checks with the columns SPEC-115 D4 declares.
func TestMigration018_CreatesQualityTables(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 17)

	if err := applyMigration(sqlDB, 18, loadMigration018(t)); err != nil {
		t.Fatalf("migration 018 up: %v", err)
	}

	var version int
	if err := sqlDB.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("query schema_version: %v", err)
	}
	if version != 18 {
		t.Errorf("expected schema version 18, got %d", version)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := sqlDB.Exec(
		`INSERT INTO quality_certificates
			(id, project, spec_id, head_sha, base_sha, constitution_hash, schema_version,
			 verdict, dirty, mneme_version, started_at, finished_at, duration_ms, created_at)
		 VALUES
			('cert-1', 'proj', 'SPEC-115', 'abc123', 'base123', 'hash123', 1,
			 'pass', 0, 'v1.0.0', ?, ?, 1200, ?)`,
		now, now, now,
	)
	if err != nil {
		t.Fatalf("insert quality_certificates row: %v", err)
	}

	_, err = sqlDB.Exec(
		`INSERT INTO quality_checks
			(certificate_id, seq, kind, name, status, exit_code, duration_ms,
			 output_sha256, output_bytes, output_tail, summary, detail,
			 acked_by, acked_at, justification, created_at)
		 VALUES
			('cert-1', 1, 'gate', 'build', 'pass', 0, 500, 'deadbeef', 10, 'ok', '', '{}',
			 '', '', '', ?)`,
		now,
	)
	if err != nil {
		t.Fatalf("insert quality_checks row: %v", err)
	}

	var certID, verdict string
	var dirty int
	err = sqlDB.QueryRow(
		`SELECT id, verdict, dirty FROM quality_certificates WHERE id = 'cert-1'`,
	).Scan(&certID, &verdict, &dirty)
	if err != nil {
		t.Fatalf("select quality_certificates: %v", err)
	}
	if certID != "cert-1" || verdict != "pass" || dirty != 0 {
		t.Errorf("unexpected certificate row: id=%q verdict=%q dirty=%d", certID, verdict, dirty)
	}

	var checkName, checkStatus string
	err = sqlDB.QueryRow(
		`SELECT name, status FROM quality_checks WHERE certificate_id = 'cert-1' AND seq = 1`,
	).Scan(&checkName, &checkStatus)
	if err != nil {
		t.Fatalf("select quality_checks: %v", err)
	}
	if checkName != "build" || checkStatus != "pass" {
		t.Errorf("unexpected check row: name=%q status=%q", checkName, checkStatus)
	}
}

// TestMigration018_HasExpectedIndexes verifies the three declared indexes
// exist on the new tables — the mutation this test guards (G8) is dropping
// idx_quality_checks_cert from the .sql file.
func TestMigration018_HasExpectedIndexes(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 17)

	if err := applyMigration(sqlDB, 18, loadMigration018(t)); err != nil {
		t.Fatalf("migration 018 up: %v", err)
	}

	wantIndexes := map[string][]string{
		"quality_certificates": {"idx_quality_certs_spec", "idx_quality_certs_sha"},
		"quality_checks":       {"idx_quality_checks_cert"},
	}

	for table, want := range wantIndexes {
		rows, err := sqlDB.Query(`PRAGMA index_list(` + table + `)`)
		if err != nil {
			t.Fatalf("PRAGMA index_list(%s): %v", table, err)
		}
		got := map[string]bool{}
		for rows.Next() {
			var seq int
			var name string
			var unique int
			var origin string
			var partial int
			if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
				rows.Close()
				t.Fatalf("scan index_list row: %v", err)
			}
			got[name] = true
		}
		rows.Close()

		for _, name := range want {
			if !got[name] {
				t.Errorf("table %s: expected index %q, got %+v", table, name, got)
			}
		}
	}
}

// TestMigration018_IsIdempotent verifies that applying migration 018 twice
// leaves exactly one schema_version row for version 18.
func TestMigration018_IsIdempotent(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 17)

	if err := applyMigration(sqlDB, 18, loadMigration018(t)); err != nil {
		t.Fatalf("migration 018 first apply: %v", err)
	}
	if err := applyMigration(sqlDB, 18, loadMigration018(t)); err != nil {
		t.Fatalf("migration 018 second apply: %v", err)
	}

	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM schema_version WHERE version = 18`).Scan(&count); err != nil {
		t.Fatalf("count schema_version rows for v18: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 schema_version row for v18, got %d", count)
	}
}

// loadMigration018 reads the migration 018 SQL from the embedded FS.
func loadMigration018(t *testing.T) string {
	t.Helper()
	content, err := migrationsFS.ReadFile("migrations/018_quality_certificates.sql")
	if err != nil {
		t.Fatalf("read migration 018: %v", err)
	}
	return string(content)
}
