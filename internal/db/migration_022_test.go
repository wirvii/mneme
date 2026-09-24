package db

import "testing"

// TestMigration022_ReviewedSHADefaultsToEmptyString covers SPEC-157 AC1: a
// row inserted into spec_history WITHOUT specifying reviewed_sha must read
// back as "" — the exact behaviour of every row this table held before this
// migration existed. The assertion queries the database, never the .sql
// file, so a change to the DEFAULT clause (this criterion's own mutation
// target) is what this test is designed to catch.
func TestMigration022_ReviewedSHADefaultsToEmptyString(t *testing.T) {
	t.Parallel()

	sqldb, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer sqldb.Close()

	_, err = sqldb.Exec(
		`INSERT INTO specs (id, title, status, project, lane, created_at, updated_at)
		 VALUES ('SPEC-999', 'fixture', 'draft', 'proj', 'standard', 't0', 't0')`,
	)
	if err != nil {
		t.Fatalf("insert spec: %v", err)
	}

	_, err = sqldb.Exec(
		`INSERT INTO spec_history (id, spec_id, from_status, to_status, by, reason, at)
		 VALUES ('h-1', 'SPEC-999', '', 'draft', 'system', 'created', 't0')`,
	)
	if err != nil {
		t.Fatalf("insert history without reviewed_sha: %v", err)
	}

	var reviewedSHA string
	if err := sqldb.QueryRow(`SELECT reviewed_sha FROM spec_history WHERE id = 'h-1'`).Scan(&reviewedSHA); err != nil {
		t.Fatalf("select reviewed_sha: %v", err)
	}
	if reviewedSHA != "" {
		t.Errorf("reviewed_sha = %q, want empty string (the DEFAULT this migration declares)", reviewedSHA)
	}
}
