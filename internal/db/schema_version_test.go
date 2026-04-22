package db

import (
	"path/filepath"
	"testing"
)

// TestSchemaVersion_AfterMigrate verifies that SchemaVersion returns the
// correct version number after a fully migrated database has been opened.
func TestSchemaVersion_AfterMigrate(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	opened, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	opened.Close()

	version, err := SchemaVersion(dbPath)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}

	// The current schema has 5 migration files (001–005).
	const wantVersion = 5
	if version != wantVersion {
		t.Errorf("SchemaVersion = %d, want %d", version, wantVersion)
	}
}

// TestSchemaVersion_NonExistentPath verifies that SchemaVersion returns 0
// (not an error) when the database file does not exist yet.
func TestSchemaVersion_NonExistentPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "does-not-exist.db")

	version, err := SchemaVersion(dbPath)
	if err != nil {
		t.Fatalf("SchemaVersion with non-existent path: unexpected error: %v", err)
	}
	if version != 0 {
		t.Errorf("SchemaVersion = %d, want 0 for non-existent path", version)
	}
}
