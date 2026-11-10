package db

import (
	"path/filepath"
	"strings"
	"testing"
)

// reviewRequiredColumn returns the PRAGMA table_info row of
// specs.review_required, or ok=false when the column is absent.
func reviewRequiredColumn(t *testing.T, d *DB) (colType string, notNull int, dflt string, ok bool) {
	t.Helper()
	var dflNull *string
	err := d.QueryRow(`SELECT type, "notnull", dflt_value FROM pragma_table_info('specs') WHERE name = 'review_required'`).
		Scan(&colType, &notNull, &dflNull)
	if err != nil {
		return "", 0, "", false
	}
	if dflNull != nil {
		dflt = *dflNull
	}
	return colType, notNull, dflt, true
}

// TestMigration025 covers SPEC-181 D3: a new database has
// specs.review_required as INTEGER NOT NULL DEFAULT 0 and version 25; a
// database that lost the column and the version row gets both back with the
// existing specs at 0; and the migration file carries no ALTER TABLE.
func TestMigration025(t *testing.T) {
	t.Run("fresh database", func(t *testing.T) {
		d, err := OpenMemory()
		if err != nil {
			t.Fatalf("OpenMemory: %v", err)
		}
		defer d.Close()

		colType, notNull, dflt, ok := reviewRequiredColumn(t, d)
		if !ok {
			t.Fatal("specs.review_required missing on a fresh database")
		}
		if colType != "INTEGER" || notNull != 1 || dflt != "0" {
			t.Errorf("review_required = %s notnull=%d default=%s, want INTEGER notnull=1 default=0", colType, notNull, dflt)
		}
		var n int
		if err := d.QueryRow(`SELECT COUNT(*) FROM schema_version WHERE version = 25`).Scan(&n); err != nil {
			t.Fatalf("schema_version: %v", err)
		}
		if n != 1 {
			t.Errorf("version 25 rows = %d, want 1", n)
		}
	})

	t.Run("legacy database with specs", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "legacy.db")
		d, err := Open(path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if _, err := d.Exec(`INSERT INTO specs (id, title, status, project, assigned_agents, files_changed, created_at, updated_at, uuid)
			VALUES ('SPEC-001', 'T', 'draft', 'p', '[]', '[]', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 'u-1')`); err != nil {
			t.Fatalf("insert spec: %v", err)
		}
		if _, err := d.Exec(`ALTER TABLE specs DROP COLUMN review_required`); err != nil {
			t.Fatalf("drop column: %v", err)
		}
		if _, err := d.Exec(`DELETE FROM schema_version WHERE version = 25`); err != nil {
			t.Fatalf("delete 25: %v", err)
		}
		d.Close()

		d, err = Open(path)
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		defer d.Close()
		if _, _, _, ok := reviewRequiredColumn(t, d); !ok {
			t.Fatal("review_required not restored")
		}
		var value, versions int
		if err := d.QueryRow(`SELECT review_required FROM specs WHERE id = 'SPEC-001'`).Scan(&value); err != nil {
			t.Fatalf("select review_required: %v", err)
		}
		if value != 0 {
			t.Errorf("existing spec review_required = %d, want 0", value)
		}
		if err := d.QueryRow(`SELECT COUNT(*) FROM schema_version WHERE version = 25`).Scan(&versions); err != nil {
			t.Fatalf("count: %v", err)
		}
		if versions != 1 {
			t.Errorf("version 25 rows = %d, want 1 (re-registered)", versions)
		}
	})

	t.Run("migration file has no ALTER TABLE", func(t *testing.T) {
		sqlText, err := migrationsFS.ReadFile("migrations/025_spec_review_required.sql")
		if err != nil {
			t.Fatalf("read migration: %v", err)
		}
		for _, line := range strings.Split(string(sqlText), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "--") {
				continue
			}
			if strings.Contains(strings.ToLower(line), "alter table") {
				t.Errorf("025 must not contain ALTER TABLE: %q", line)
			}
		}
	})
}
