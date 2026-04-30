package db

import (
	"strings"
	"testing"
	"time"
)

// TestMigration006_Up applies migration 006 on top of a fully migrated v5
// database and verifies that the new columns exist and accept valid data.
func TestMigration006_Up(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 5)

	if err := applyMigration(sqlDB, 6, loadMigration006(t)); err != nil {
		t.Fatalf("migration 006 up: %v", err)
	}

	// Verify schema version reached 6.
	var version int
	if err := sqlDB.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("query schema_version: %v", err)
	}
	if version != 6 {
		t.Errorf("expected schema version 6, got %d", version)
	}

	// Verify the new columns exist by inserting a row that uses them.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := sqlDB.Exec(`
		INSERT INTO memories
		    (id, type, scope, title, content, created_at, updated_at,
		     importance, confidence, decay_rate, applies_to, severity)
		VALUES
		    ('test-rule-1', 'rule', 'project', 'Test rule', 'Rule content',
		     ?, ?, 0.95, 0.8, 0.0, '["internal/**/*.go"]', 'warn')`,
		now, now,
	)
	if err != nil {
		t.Fatalf("insert rule row after migration 006: %v", err)
	}

	// Roundtrip the new columns.
	var appliesTo, severity string
	if err := sqlDB.QueryRow(
		`SELECT applies_to, severity FROM memories WHERE id = 'test-rule-1'`,
	).Scan(&appliesTo, &severity); err != nil {
		t.Fatalf("read back rule row: %v", err)
	}
	if appliesTo != `["internal/**/*.go"]` {
		t.Errorf("applies_to = %q, want %q", appliesTo, `["internal/**/*.go"]`)
	}
	if severity != "warn" {
		t.Errorf("severity = %q, want %q", severity, "warn")
	}
}

// TestMigration006_ExistingRowsHaveDefaults verifies that memories created before
// migration 006 receive the correct default values for the new columns.
func TestMigration006_ExistingRowsHaveDefaults(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 5)

	// Insert a pre-migration row (without applies_to or severity).
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := sqlDB.Exec(`
		INSERT INTO memories
		    (id, type, scope, title, content, created_at, updated_at,
		     importance, confidence, decay_rate)
		VALUES
		    ('pre-migration-id', 'decision', 'project', 'Old memory', 'Old content',
		     ?, ?, 0.7, 0.8, 0.01)`,
		now, now,
	)
	if err != nil {
		t.Fatalf("insert pre-migration row: %v", err)
	}

	// Apply migration 006.
	if err := applyMigration(sqlDB, 6, loadMigration006(t)); err != nil {
		t.Fatalf("migration 006 up: %v", err)
	}

	// Verify the pre-existing row has correct defaults.
	var appliesTo, severity string
	if err := sqlDB.QueryRow(
		`SELECT applies_to, severity FROM memories WHERE id = 'pre-migration-id'`,
	).Scan(&appliesTo, &severity); err != nil {
		t.Fatalf("read back pre-migration row: %v", err)
	}
	if appliesTo != "[]" {
		t.Errorf("pre-migration row applies_to = %q, want %q", appliesTo, "[]")
	}
	if severity != "" {
		t.Errorf("pre-migration row severity = %q, want empty string", severity)
	}
}

// TestMigration006_CheckConstraint verifies that the SQLite CHECK constraint on
// the severity column rejects values outside ('', 'info', 'warn', 'block').
func TestMigration006_CheckConstraint(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 6)

	now := time.Now().UTC().Format(time.RFC3339Nano)

	cases := []struct {
		name     string
		severity string
		wantErr  bool
	}{
		{"empty", "", false},
		{"info", "info", false},
		{"warn", "warn", false},
		{"block", "block", false},
		{"critical", "critical", true},
		{"error", "error", true},
		{"high", "high", true},
	}

	for i, tc := range cases {
		tc := tc
		i := i
		t.Run(tc.name, func(t *testing.T) {
			id := "check-test-" + string(rune('a'+i))
			_, err := sqlDB.Exec(`
				INSERT INTO memories
				    (id, type, scope, title, content, created_at, updated_at,
				     importance, confidence, decay_rate, severity)
				VALUES
				    (?, 'rule', 'project', 'Test', 'Content',
				     ?, ?, 0.95, 0.8, 0.0, ?)`,
				id, now, now, tc.severity,
			)
			if tc.wantErr && err == nil {
				t.Errorf("expected CHECK constraint error for severity=%q, got nil", tc.severity)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error for severity=%q: %v", tc.severity, err)
			}
			if tc.wantErr && err != nil && !strings.Contains(err.Error(), "CHECK") {
				t.Errorf("expected CHECK constraint error for severity=%q, got: %v", tc.severity, err)
			}
		})
	}
}

// TestMigration006_RunnerIdempotent verifies that the migrate runner skips
// migration 006 when the database is already at version 6. The runner uses
// version-based skipping (version <= current), so calling migrate twice must
// not return an error or insert duplicate schema_version rows.
func TestMigration006_RunnerIdempotent(t *testing.T) {
	db, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	// Run a second migration pass — should be a no-op.
	if err := migrate(db.DB); err != nil {
		t.Fatalf("second migrate call: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_version WHERE version = 6`).Scan(&count); err != nil {
		t.Fatalf("count schema_version rows for v6: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 schema_version row for v6, got %d", count)
	}
}

// loadMigration006 reads the migration 006 SQL from the embedded FS.
func loadMigration006(t *testing.T) string {
	t.Helper()
	content, err := migrationsFS.ReadFile("migrations/006_rule_fields.sql")
	if err != nil {
		t.Fatalf("read migration 006: %v", err)
	}
	return string(content)
}
