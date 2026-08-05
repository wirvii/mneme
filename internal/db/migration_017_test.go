package db

import (
	"testing"
	"time"
)

// TestMigration017_CreatesBacklogRefinements verifies that migration 017
// applies on top of a fully migrated v16 database and creates the
// backlog_refinements table with the expected columns.
func TestMigration017_CreatesBacklogRefinements(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 16)

	if err := applyMigration(sqlDB, 17, loadMigration017(t)); err != nil {
		t.Fatalf("migration 017 up: %v", err)
	}

	var version int
	if err := sqlDB.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("query schema_version: %v", err)
	}
	if version != 17 {
		t.Errorf("expected schema version 17, got %d", version)
	}

	// Verify a row can be inserted with the 5 expected columns.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := sqlDB.Exec(
		`INSERT INTO backlog_items (id, title, description, status, priority, project, created_at, updated_at)
		 VALUES ('BL-1', 'Test item', 'desc', 'raw', 'medium', 'proj', ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatalf("insert backlog item: %v", err)
	}
	_, err = sqlDB.Exec(
		`INSERT INTO backlog_refinements (item_id, seq, body, by, at) VALUES ('BL-1', 1, 'r1', 'architect', ?)`,
		now,
	)
	if err != nil {
		t.Fatalf("insert backlog refinement: %v", err)
	}

	var itemID, body, by string
	var seq int
	err = sqlDB.QueryRow(
		`SELECT item_id, seq, body, by FROM backlog_refinements WHERE item_id = 'BL-1'`,
	).Scan(&itemID, &seq, &body, &by)
	if err != nil {
		t.Fatalf("select backlog refinement: %v", err)
	}
	if itemID != "BL-1" || seq != 1 || body != "r1" || by != "architect" {
		t.Errorf("unexpected row: item_id=%q seq=%d body=%q by=%q", itemID, seq, body, by)
	}
}

// TestMigration017_HasCompositePKIndexAndNoExtraIndex verifies the schema
// relies solely on the sqlite_autoindex backing the composite primary key
// (item_id, seq) — no additional CREATE INDEX (D9).
func TestMigration017_HasCompositePKIndexAndNoExtraIndex(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 16)

	if err := applyMigration(sqlDB, 17, loadMigration017(t)); err != nil {
		t.Fatalf("migration 017 up: %v", err)
	}

	rows, err := sqlDB.Query(`PRAGMA index_list(backlog_refinements)`)
	if err != nil {
		t.Fatalf("PRAGMA index_list: %v", err)
	}
	defer rows.Close()

	type indexRow struct {
		seq     int
		name    string
		unique  int
		origin  string
		partial int
	}
	var indexes []indexRow
	for rows.Next() {
		var r indexRow
		if err := rows.Scan(&r.seq, &r.name, &r.unique, &r.origin, &r.partial); err != nil {
			t.Fatalf("scan index_list row: %v", err)
		}
		indexes = append(indexes, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate index_list: %v", err)
	}

	if len(indexes) != 1 {
		t.Fatalf("expected exactly 1 index on backlog_refinements (the PK autoindex), got %d: %+v", len(indexes), indexes)
	}
	if indexes[0].origin != "pk" {
		t.Errorf("expected the single index to originate from the PK, got origin=%q name=%q", indexes[0].origin, indexes[0].name)
	}
	if indexes[0].unique != 1 {
		t.Errorf("expected the PK autoindex to be unique, got unique=%d", indexes[0].unique)
	}
}

// TestMigration017_IsIdempotent verifies that applying migration 017 twice
// leaves exactly one schema_version row for version 17.
func TestMigration017_IsIdempotent(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 16)

	if err := applyMigration(sqlDB, 17, loadMigration017(t)); err != nil {
		t.Fatalf("migration 017 first apply: %v", err)
	}
	if err := applyMigration(sqlDB, 17, loadMigration017(t)); err != nil {
		t.Fatalf("migration 017 second apply: %v", err)
	}

	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM schema_version WHERE version = 17`).Scan(&count); err != nil {
		t.Fatalf("count schema_version rows for v17: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 schema_version row for v17, got %d", count)
	}
}

// TestMigration017_DoesNotTouchLegacyItems verifies the migration is
// conservative (D2/D10): existing backlog_items rows — across every status,
// including one whose description already contains an internal "\n\n" from a
// pre-SPEC-110 concatenated refinement — survive byte-identical, with zero
// rows created in backlog_refinements.
func TestMigration017_DoesNotTouchLegacyItems(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 16)

	now := time.Now().UTC().Format(time.RFC3339Nano)
	legacy := []struct {
		id, status, description string
	}{
		{"BL-raw", "raw", "initial description"},
		{"BL-refined", "refined", "initial description\n\nfirst refinement, concatenated pre-SPEC-110"},
		{"BL-promoted", "promoted", "promoted item description"},
		{"BL-archived", "archived", "archived item description"},
	}
	for _, l := range legacy {
		_, err := sqlDB.Exec(
			`INSERT INTO backlog_items (id, title, description, status, priority, project, created_at, updated_at)
			 VALUES (?, 'Legacy item', ?, ?, 'medium', 'proj', ?, ?)`,
			l.id, l.description, l.status, now, now,
		)
		if err != nil {
			t.Fatalf("insert legacy item %s: %v", l.id, err)
		}
	}

	if err := applyMigration(sqlDB, 17, loadMigration017(t)); err != nil {
		t.Fatalf("migration 017: %v", err)
	}

	for _, l := range legacy {
		var gotDescription, gotStatus string
		err := sqlDB.QueryRow(
			`SELECT description, status FROM backlog_items WHERE id = ?`, l.id,
		).Scan(&gotDescription, &gotStatus)
		if err != nil {
			t.Fatalf("select legacy item %s: %v", l.id, err)
		}
		if gotDescription != l.description {
			t.Errorf("item %s: description changed: got %q, want %q", l.id, gotDescription, l.description)
		}
		if gotStatus != l.status {
			t.Errorf("item %s: status changed: got %q, want %q", l.id, gotStatus, l.status)
		}
	}

	var refinementCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM backlog_refinements`).Scan(&refinementCount); err != nil {
		t.Fatalf("count backlog_refinements: %v", err)
	}
	if refinementCount != 0 {
		t.Errorf("expected 0 rows in backlog_refinements after migrating legacy items, got %d", refinementCount)
	}
}

// loadMigration017 reads the migration 017 SQL from the embedded FS.
func loadMigration017(t *testing.T) string {
	t.Helper()
	content, err := migrationsFS.ReadFile("migrations/017_backlog_refinements.sql")
	if err != nil {
		t.Fatalf("read migration 017: %v", err)
	}
	return string(content)
}
