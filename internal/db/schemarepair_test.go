package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// openRawFile opens a file-backed SQLite database WITHOUT running migrations,
// so a test can build an exact, possibly damaged, schema by hand.
func openRawFile(t *testing.T, path string) *sql.DB {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("sql.Open(%s): %v", path, err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	return raw
}

// latestEmbeddedVersion is the highest version registered by the embedded
// migrations, computed from their file names so no number is hard-coded.
func latestEmbeddedVersion(t *testing.T) int {
	t.Helper()
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	latest := 0
	for _, e := range entries {
		v, err := versionFromFilename(e.Name())
		if err != nil {
			t.Fatalf("version of %q: %v", e.Name(), err)
		}
		if v > latest {
			latest = v
		}
	}
	return latest
}

// TestOpen_V21WithReviewedSHAAlreadyPresent is the BL-272 case: a database
// below version 22 that already has spec_history.reviewed_sha (added by hand
// or by an old beta) must open instead of failing migration 022 with
// "duplicate column name", and keep the value already stored in the column.
func TestOpen_V21WithReviewedSHAAlreadyPresent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v21.db")

	raw := openRawFile(t, path)
	applyUpToVersion(t, raw, 21)
	if _, err := raw.Exec(`ALTER TABLE spec_history ADD COLUMN reviewed_sha TEXT NOT NULL DEFAULT ''`); err != nil {
		t.Fatalf("add reviewed_sha by hand: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO spec_history (id, spec_id, from_status, to_status, by, reason, at, reviewed_sha)
		VALUES ('h1', 'SPEC-001', 'qa', 'done', 'qa', '', '2026-01-01T00:00:00Z', 'abc123')`); err != nil {
		t.Fatalf("insert history row: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	var version int
	if err := d.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if want := latestEmbeddedVersion(t); version != want {
		t.Errorf("MAX(version) = %d, want %d", version, want)
	}

	var columns int
	if err := d.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('spec_history') WHERE name = 'reviewed_sha'`).Scan(&columns); err != nil {
		t.Fatalf("count column: %v", err)
	}
	if columns != 1 {
		t.Errorf("reviewed_sha appears %d times, want 1", columns)
	}

	var sha string
	if err := d.QueryRow(`SELECT reviewed_sha FROM spec_history WHERE id = 'h1'`).Scan(&sha); err != nil {
		t.Fatalf("read value: %v", err)
	}
	if sha != "abc123" {
		t.Errorf("reviewed_sha = %q, want %q", sha, "abc123")
	}
}
