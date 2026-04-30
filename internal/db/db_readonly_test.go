package db

import (
	"path/filepath"
	"testing"
)

// TestOpenReadOnly_Success verifies that OpenReadOnly can open an existing,
// fully migrated database file and execute a SELECT query.
func TestOpenReadOnly_Success(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Create and fully migrate the database using the regular Open path.
	rw, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	rw.Close()

	// Open the same file read-only.
	ro, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer ro.Close()

	// A simple SELECT must succeed.
	var count int
	if err := ro.QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&count); err != nil {
		t.Fatalf("SELECT on read-only DB: %v", err)
	}
}

// TestOpenReadOnly_FileNotExist verifies that OpenReadOnly returns an error when
// the target path does not exist, instead of silently creating a new file.
func TestOpenReadOnly_FileNotExist(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "nonexistent.db")

	_, err := OpenReadOnly(dbPath)
	if err == nil {
		t.Fatal("OpenReadOnly should return an error for a missing file, got nil")
	}
}

// TestOpenReadOnly_NoWrite verifies that write operations are rejected on a
// read-only connection. This confirms the mode=ro DSN parameter is effective.
func TestOpenReadOnly_NoWrite(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Create the database first.
	rw, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	rw.Close()

	ro, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer ro.Close()

	// An INSERT must fail on a read-only connection.
	_, insertErr := ro.Exec(
		`INSERT INTO memories (id, type, scope, title, content, created_at, updated_at, importance, confidence, decay_rate)
		 VALUES ('ro-test', 'discovery', 'project', 'T', 'C', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 0.5, 0.8, 0.01)`,
	)
	if insertErr == nil {
		t.Fatal("INSERT should fail on a read-only connection, got nil error")
	}
}
