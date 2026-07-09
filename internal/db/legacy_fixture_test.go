package db

import (
	"path/filepath"
	"testing"
)

// TestLegacyFixture_OpensWithModernc verifies AC5 of SPEC-070: a .db file
// written by the previous driver generation is opened and queried correctly
// by the modernc.org/sqlite driver, without recreating the schema or
// erroring. testdata/legacy-driver-fixture.db was produced with the system
// sqlite3 CLI (the same reference SQLite C engine the previous CGO-based
// driver bound to) by replaying every migration file in internal/db/migrations
// exactly as migrate() would, then inserting one memories row — reproducing
// the on-disk state a pre-modernc binary would have left behind. The file
// format is standard SQLite, identical across both drivers, so no data
// migration is required (D1/AC5).
func TestLegacyFixture_OpensWithModernc(t *testing.T) {
	fixture := filepath.Join("testdata", "legacy-driver-fixture.db")

	// SchemaVersion opens the fixture read-only and must report the version
	// already recorded in schema_version, without running any migrations.
	version, err := SchemaVersion(fixture)
	if err != nil {
		t.Fatalf("SchemaVersion(%q): %v", fixture, err)
	}
	if version != 14 {
		t.Errorf("expected schema version 14 in fixture, got %d", version)
	}

	// OpenReadOnly must open the fixture without touching its schema and
	// allow querying the pre-existing row.
	roDB, err := OpenReadOnly(fixture)
	if err != nil {
		t.Fatalf("OpenReadOnly(%q): %v", fixture, err)
	}
	defer roDB.Close()

	var title string
	err = roDB.QueryRow(
		`SELECT title FROM memories WHERE id = 'legacy-fixture-id'`,
	).Scan(&title)
	if err != nil {
		t.Fatalf("query legacy row: %v", err)
	}
	if title != "Legacy fixture title" {
		t.Errorf("unexpected title from legacy fixture: %q", title)
	}

	// The FTS5 index built by the fixture's writer must still be queryable —
	// this exercises bm25()/MATCH against content that predates the driver
	// swap, not just content written by modernc itself.
	var ftsTitle string
	err = roDB.QueryRow(
		`SELECT title FROM memories_fts WHERE memories_fts MATCH 'legacy fixture'`,
	).Scan(&ftsTitle)
	if err != nil {
		t.Fatalf("FTS5 MATCH on legacy fixture: %v", err)
	}
	if ftsTitle != "Legacy fixture title" {
		t.Errorf("unexpected FTS5 match title: %q", ftsTitle)
	}
}
