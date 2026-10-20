package db

import (
	"database/sql"
	"testing"
)

// TestMigration023_DropsQualityTablesKeepsTheRest covers SPEC-158 AC13: a
// database built with ONLY migrations 001..022 (never 023) that already
// holds a certificate and a check row must, after migrate() runs to
// completion, have neither quality_checks nor quality_certificates (nor
// their three indexes) in sqlite_master, MAX(version) = 23, and the rest of
// the schema — a specs row inserted alongside the certificate — completely
// untouched. A second call to migrate() proves idempotency: dropping an
// already-absent table via IF EXISTS must not error.
func TestMigration023_DropsQualityTablesKeepsTheRest(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 22)

	// Seed a specs row — the "rest of the schema" this migration must
	// leave completely alone.
	if _, err := sqlDB.Exec(
		`INSERT INTO specs (id, title, project, created_at, updated_at)
		 VALUES ('SPEC-158', 'Retirar certificacion', 'wirvii/mneme', 't1', 't2')`,
	); err != nil {
		t.Fatalf("insert specs row: %v", err)
	}

	// Seed a certificate and a check row — the data this migration
	// deliberately, irreversibly discards.
	if _, err := sqlDB.Exec(
		`INSERT INTO quality_certificates
			(id, project, spec_id, head_sha, base_sha, constitution_hash, schema_version,
			 verdict, dirty, mneme_version, started_at, finished_at, duration_ms, created_at)
		 VALUES
			('cert-1', 'wirvii/mneme', 'SPEC-158', 'abc123', 'base123', 'hash123', 1,
			 'pass', 0, 'v1.0.0', 't1', 't2', 1200, 't3')`,
	); err != nil {
		t.Fatalf("insert quality_certificates row: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO quality_checks (certificate_id, seq, kind, name, status, created_at)
		 VALUES ('cert-1', 1, 'gate', 'build', 'pass', 't1')`,
	); err != nil {
		t.Fatalf("insert quality_checks row: %v", err)
	}

	if err := migrate(sqlDB); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	assertMigration023State(t, sqlDB)

	// Idempotency: a second call must not error even though both tables
	// (and their DROP TABLE IF EXISTS statements) are already gone.
	if err := migrate(sqlDB); err != nil {
		t.Fatalf("second migrate call: %v", err)
	}
	assertMigration023State(t, sqlDB)
}

// assertMigration023State asserts the post-migration invariants shared by
// both calls to migrate() in the test above: neither quality table nor its
// indexes exist, MAX(version) is 24 (SPEC-167 added 024 on top), and the specs row survived intact.
func assertMigration023State(t *testing.T, sqlDB *sql.DB) {
	t.Helper()

	for _, table := range []string{"quality_checks", "quality_certificates"} {
		var name string
		err := sqlDB.QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table,
		).Scan(&name)
		if err != sql.ErrNoRows {
			t.Errorf("table %q still exists in sqlite_master (err=%v)", table, err)
		}
	}

	for _, index := range []string{"idx_quality_certs_spec", "idx_quality_certs_sha", "idx_quality_checks_cert"} {
		var name string
		err := sqlDB.QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, index,
		).Scan(&name)
		if err != sql.ErrNoRows {
			t.Errorf("index %q still exists in sqlite_master (err=%v)", index, err)
		}
	}

	var version int
	if err := sqlDB.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("query schema_version: %v", err)
	}
	if version != 24 {
		t.Errorf("MAX(version) = %d, want 24", version)
	}

	var title, project string
	if err := sqlDB.QueryRow(`SELECT title, project FROM specs WHERE id = 'SPEC-158'`).Scan(&title, &project); err != nil {
		t.Fatalf("select specs row: %v", err)
	}
	if title != "Retirar certificacion" || project != "wirvii/mneme" {
		t.Errorf("specs row = (%q, %q), want unchanged", title, project)
	}
}
