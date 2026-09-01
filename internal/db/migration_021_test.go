package db

import "testing"

// TestMigration021_EffectDefaultsToBlocks covers AC21's first half (SPEC-137
// etapa 1 de BL-221, D4): a row inserted into quality_checks WITHOUT
// specifying effect must read back as "blocks" — the exact behaviour of
// every row this mechanism emitted before this migration existed. The
// assertion queries the database, never the .sql file, so a change to the
// DEFAULT clause (the criterion's own mutation) is what this test is
// designed to catch.
func TestMigration021_EffectDefaultsToBlocks(t *testing.T) {
	t.Parallel()

	sqldb, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer sqldb.Close()

	_, err = sqldb.Exec(
		`INSERT INTO quality_certificates
			(id, project, spec_id, head_sha, constitution_hash, schema_version, verdict, started_at, finished_at, created_at)
		 VALUES ('cert-1', 'proj', 'SPEC-137', 'abc123', 'hash1', 1, 'pass', 't1', 't2', 't3')`,
	)
	if err != nil {
		t.Fatalf("insert certificate: %v", err)
	}

	_, err = sqldb.Exec(
		`INSERT INTO quality_checks (certificate_id, seq, kind, name, status, created_at)
		 VALUES ('cert-1', 1, 'gate', 'build', 'pass', 't1')`,
	)
	if err != nil {
		t.Fatalf("insert check without effect: %v", err)
	}

	var effect string
	if err := sqldb.QueryRow(`SELECT effect FROM quality_checks WHERE certificate_id = 'cert-1' AND seq = 1`).Scan(&effect); err != nil {
		t.Fatalf("select effect: %v", err)
	}
	if effect != "blocks" {
		t.Errorf("effect = %q, want %q (the DEFAULT this migration declares)", effect, "blocks")
	}
}

// TestMigration021_EvidenceDefaultsToEmptyString covers AC21's second half:
// a certificate inserted WITHOUT specifying evidence must read back as "",
// never a fabricated sentence — the value the three rendering channels
// (verify/status/report) translate to "sin linea de evidencia".
func TestMigration021_EvidenceDefaultsToEmptyString(t *testing.T) {
	t.Parallel()

	sqldb, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer sqldb.Close()

	_, err = sqldb.Exec(
		`INSERT INTO quality_certificates
			(id, project, spec_id, head_sha, constitution_hash, schema_version, verdict, started_at, finished_at, created_at)
		 VALUES ('cert-2', 'proj', 'SPEC-137', 'abc123', 'hash1', 1, 'pass', 't1', 't2', 't3')`,
	)
	if err != nil {
		t.Fatalf("insert certificate without evidence: %v", err)
	}

	var evidence string
	if err := sqldb.QueryRow(`SELECT evidence FROM quality_certificates WHERE id = 'cert-2'`).Scan(&evidence); err != nil {
		t.Fatalf("select evidence: %v", err)
	}
	if evidence != "" {
		t.Errorf("evidence = %q, want empty string (the DEFAULT this migration declares)", evidence)
	}
}
