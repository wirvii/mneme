package db

import (
	"path/filepath"
	"strings"
	"testing"
)

// hasReviewedAtColumn reports whether memories.reviewed_at exists.
func hasReviewedAtColumn(t *testing.T, d *DB) bool {
	t.Helper()
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('memories') WHERE name = 'reviewed_at'`).Scan(&n); err != nil {
		t.Fatalf("pragma_table_info: %v", err)
	}
	return n > 0
}

// TestMigration024_FreshDB covers SPEC-167 AC1: a new database has the column
// and version 24, and the migration file itself carries no ALTER TABLE.
func TestMigration024_FreshDB(t *testing.T) {
	d, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer d.Close()

	if !hasReviewedAtColumn(t, d) {
		t.Error("memories.reviewed_at missing on a fresh database")
	}
	var version int
	if err := d.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("schema_version: %v", err)
	}
	if version != 24 {
		t.Errorf("MAX(version) = %d, want 24", version)
	}

	sqlText, err := migrationsFS.ReadFile("migrations/024_memory_reviewed_at.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	// Only executable statements matter: comments may (and do) explain why
	// there is no ALTER.
	for _, line := range strings.Split(string(sqlText), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		if strings.Contains(strings.ToLower(line), "alter table") {
			t.Errorf("024 must not contain ALTER TABLE: %q", line)
		}
	}
}

// TestEnsureMemoryReviewedAt_LegacyVersions covers SPEC-167 AC2: databases
// whose schema_version was written by old betas must still open, gain the
// column exactly once, and never lose or rewrite review dates on reopen.
func TestEnsureMemoryReviewedAt_LegacyVersions(t *testing.T) {
	const insertMemory = `INSERT INTO memories (id, type, title, content, created_at, updated_at)
		VALUES ('m1', 'discovery', 'T', 'C', '2026-01-01T00:00:00Z', '2026-02-02T00:00:00Z')`

	reviewedAt := func(t *testing.T, d *DB) (string, bool) {
		t.Helper()
		var v *string
		if err := d.QueryRow(`SELECT reviewed_at FROM memories WHERE id = 'm1'`).Scan(&v); err != nil {
			t.Fatalf("select reviewed_at: %v", err)
		}
		if v == nil {
			return "", false
		}
		return *v, true
	}

	t.Run("column dropped and a beta registered version 30", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "legacy.db")
		d, err := Open(path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if _, err := d.Exec(insertMemory); err != nil {
			t.Fatalf("insert: %v", err)
		}
		if _, err := d.Exec(`ALTER TABLE memories DROP COLUMN reviewed_at`); err != nil {
			t.Fatalf("drop column: %v", err)
		}
		if _, err := d.Exec(`DELETE FROM schema_version WHERE version = 24`); err != nil {
			t.Fatalf("delete 24: %v", err)
		}
		if _, err := d.Exec(`INSERT INTO schema_version (version, applied_at) VALUES (30, datetime('now'))`); err != nil {
			t.Fatalf("insert 30: %v", err)
		}
		d.Close()

		d, err = Open(path)
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		defer d.Close()
		if !hasReviewedAtColumn(t, d) {
			t.Fatal("column not restored")
		}
		got, ok := reviewedAt(t, d)
		if !ok || got != "2026-02-02T00:00:00Z" {
			t.Errorf("reviewed_at = %q (set=%v), want updated_at", got, ok)
		}
	})

	t.Run("column present and version 24 row missing", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "legacy.db")
		d, err := Open(path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if _, err := d.Exec(`DELETE FROM schema_version WHERE version = 24`); err != nil {
			t.Fatalf("delete 24: %v", err)
		}
		d.Close()

		d, err = Open(path)
		if err != nil {
			t.Fatalf("reopen must not fail: %v", err)
		}
		defer d.Close()
		var n int
		if err := d.QueryRow(`SELECT COUNT(*) FROM schema_version WHERE version = 24`).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		if n != 1 {
			t.Errorf("version 24 rows = %d, want 1 (re-registered)", n)
		}
	})

	t.Run("reopening twice never changes reviewed_at", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "legacy.db")
		d, err := Open(path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if _, err := d.Exec(insertMemory); err != nil {
			t.Fatalf("insert: %v", err)
		}
		if _, err := d.Exec(`UPDATE memories SET reviewed_at = '2026-03-03T00:00:00Z' WHERE id = 'm1'`); err != nil {
			t.Fatalf("set reviewed_at: %v", err)
		}
		d.Close()

		for i := 0; i < 2; i++ {
			d, err = Open(path)
			if err != nil {
				t.Fatalf("reopen %d: %v", i, err)
			}
			got, _ := reviewedAt(t, d)
			d.Close()
			if got != "2026-03-03T00:00:00Z" {
				t.Errorf("reopen %d: reviewed_at = %q, want unchanged", i, got)
			}
		}
	})

	t.Run("no memories table is a no-op", func(t *testing.T) {
		raw := openRawMemory(t)
		if err := ensureMemoryReviewedAt(raw); err != nil {
			t.Errorf("ensureMemoryReviewedAt on empty db: %v", err)
		}
	})
}
