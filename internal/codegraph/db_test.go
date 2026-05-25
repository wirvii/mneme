package codegraph

import (
	"path/filepath"
	"testing"
)

// TestOpenDB_CreatesSchema verifies that OpenDB creates all expected tables and
// the FTS5 virtual table when called on a fresh in-memory database.
func TestOpenDB_CreatesSchema(t *testing.T) {
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	tables := []string{
		"nodes",
		"edges",
		"files",
		"unresolved_refs",
		"project_metadata",
		"schema_versions",
		"nodes_fts",
	}

	for _, tbl := range tables {
		t.Run("table_"+tbl, func(t *testing.T) {
			var name string
			row := db.DB.QueryRow(
				`SELECT name FROM sqlite_master WHERE type IN ('table','view') AND name = ?`, tbl,
			)
			if err := row.Scan(&name); err != nil {
				t.Errorf("table %q not found in schema: %v", tbl, err)
			}
		})
	}
}

// TestOpenDB_Idempotent verifies that opening, closing, and re-opening the same
// database path does not return an error and the schema is still intact.
func TestOpenDB_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "codegraph.db")

	db1, err := OpenDB(path)
	if err != nil {
		t.Fatalf("first OpenDB: %v", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db2, err := OpenDB(path)
	if err != nil {
		t.Fatalf("second OpenDB: %v", err)
	}
	defer db2.Close()

	// The schema_versions table should still hold the initial row.
	var count int
	row := db2.DB.QueryRow(`SELECT COUNT(*) FROM schema_versions`)
	if err := row.Scan(&count); err != nil {
		t.Fatalf("query schema_versions: %v", err)
	}
	if count == 0 {
		t.Error("schema_versions is empty after re-open; expected at least one row")
	}
}

// TestOpenDB_InMemory verifies that the special path ":memory:" opens an
// in-memory database (no filesystem side-effects) with the full schema applied.
func TestOpenDB_InMemory(t *testing.T) {
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB(:memory:): %v", err)
	}
	defer db.Close()

	// A simple INSERT/SELECT round-trip proves the schema is functional.
	_, err = db.DB.Exec(`
		INSERT INTO project_metadata (key, value, updated_at)
		VALUES ('test_key', 'test_value', 1234567890)
	`)
	if err != nil {
		t.Fatalf("INSERT into project_metadata: %v", err)
	}

	var val string
	if err := db.DB.QueryRow(`SELECT value FROM project_metadata WHERE key = 'test_key'`).Scan(&val); err != nil {
		t.Fatalf("SELECT from project_metadata: %v", err)
	}
	if val != "test_value" {
		t.Errorf("got value %q; want %q", val, "test_value")
	}
}

// TestDBPath_ForProject verifies that DBPath constructs the expected filesystem
// path from a projects directory and a project slug.
func TestDBPath_ForProject(t *testing.T) {
	cases := []struct {
		projectsDir string
		slug        string
		want        string
	}{
		{"/home/user/.mneme/projects", "myorg-myrepo", "/home/user/.mneme/projects/myorg-myrepo-codegraph.db"},
		{"/tmp/mneme", "simple", "/tmp/mneme/simple-codegraph.db"},
		{"/mneme", "a-b-c", "/mneme/a-b-c-codegraph.db"},
	}

	for _, tc := range cases {
		got := DBPath(tc.projectsDir, tc.slug)
		if got != tc.want {
			t.Errorf("DBPath(%q, %q) = %q; want %q", tc.projectsDir, tc.slug, got, tc.want)
		}
	}
}
