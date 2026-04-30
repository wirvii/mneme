package db

import (
	"testing"
	"time"
)

// TestMigration007_Up applies migration 007 on top of a fully migrated v6
// database and verifies that last_traversed_at exists and accepts data.
func TestMigration007_Up(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 6)

	if err := applyMigration(sqlDB, 7, loadMigration007(t)); err != nil {
		t.Fatalf("migration 007 up: %v", err)
	}

	// Verify schema version reached 7.
	var version int
	if err := sqlDB.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("query schema_version: %v", err)
	}
	if version != 7 {
		t.Errorf("expected schema version 7, got %d", version)
	}

	// Verify last_traversed_at column exists by inserting a relation that uses it.
	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Insert entities first (FK constraint).
	if _, err := sqlDB.Exec(`
		INSERT INTO entities (id, name, kind, created_at, updated_at)
		VALUES ('e1', 'source', 'module', ?, ?), ('e2', 'target', 'module', ?, ?)`,
		now, now, now, now,
	); err != nil {
		t.Fatalf("insert entities: %v", err)
	}

	// Insert relation with last_traversed_at set.
	if _, err := sqlDB.Exec(`
		INSERT INTO relations (id, source_id, target_id, type, weight, created_at, last_traversed_at)
		VALUES ('r1', 'e1', 'e2', 'depends_on', 0.9, ?, ?)`,
		now, now,
	); err != nil {
		t.Fatalf("insert relation with last_traversed_at: %v", err)
	}

	// Roundtrip.
	var weight float64
	var lastTraversed string
	if err := sqlDB.QueryRow(
		`SELECT weight, last_traversed_at FROM relations WHERE id = 'r1'`,
	).Scan(&weight, &lastTraversed); err != nil {
		t.Fatalf("read back relation: %v", err)
	}
	if weight != 0.9 {
		t.Errorf("weight = %v, want 0.9", weight)
	}
	if lastTraversed == "" {
		t.Error("expected non-empty last_traversed_at")
	}
}

// TestMigration007_BackfillWeights verifies that existing relations receive
// type-appropriate weights after migration 007 is applied.
func TestMigration007_BackfillWeights(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 6)

	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Insert entities and relations before the migration with default weight 1.0.
	if _, err := sqlDB.Exec(`
		INSERT INTO entities (id, name, kind, created_at, updated_at)
		VALUES ('e1', 'src', 'module', ?, ?), ('e2', 'tgt', 'module', ?, ?)`,
		now, now, now, now,
	); err != nil {
		t.Fatalf("insert entities: %v", err)
	}

	cases := []struct {
		id       string
		relType  string
		wantWeight float64
	}{
		{"r-depends", "depends_on", 0.9},
		{"r-implements", "implements", 0.8},
		{"r-part-of", "part_of", 0.85},
		{"r-uses", "uses", 0.7},
		{"r-supersedes", "supersedes", 0.6},
		{"r-related", "related_to", 0.5},
		{"r-conflicts", "conflicts_with", 0.7},
		{"r-references", "references", 0.4},
	}

	for _, tc := range cases {
		if _, err := sqlDB.Exec(`
			INSERT INTO relations (id, source_id, target_id, type, weight, created_at)
			VALUES (?, 'e1', 'e2', ?, 1.0, ?)`,
			tc.id, tc.relType, now,
		); err != nil {
			t.Fatalf("insert relation %s: %v", tc.id, err)
		}
	}

	// Apply migration 007.
	if err := applyMigration(sqlDB, 7, loadMigration007(t)); err != nil {
		t.Fatalf("migration 007 up: %v", err)
	}

	// Verify each relation has the correct backfilled weight.
	for _, tc := range cases {
		tc := tc
		t.Run(tc.relType, func(t *testing.T) {
			var weight float64
			if err := sqlDB.QueryRow(
				`SELECT weight FROM relations WHERE id = ?`, tc.id,
			).Scan(&weight); err != nil {
				t.Fatalf("read weight for %s: %v", tc.id, err)
			}
			if weight != tc.wantWeight {
				t.Errorf("weight = %v, want %v", weight, tc.wantWeight)
			}
		})
	}
}

// TestMigration007_BackfillUnknownType verifies that relations with an
// unrecognised type receive the 0.5 fallback weight via the ELSE clause.
func TestMigration007_BackfillUnknownType(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 6)

	now := time.Now().UTC().Format(time.RFC3339Nano)

	if _, err := sqlDB.Exec(`
		INSERT INTO entities (id, name, kind, created_at, updated_at)
		VALUES ('e1', 'src', 'concept', ?, ?), ('e2', 'tgt', 'concept', ?, ?)`,
		now, now, now, now,
	); err != nil {
		t.Fatalf("insert entities: %v", err)
	}
	if _, err := sqlDB.Exec(`
		INSERT INTO relations (id, source_id, target_id, type, weight, created_at)
		VALUES ('r-unknown', 'e1', 'e2', 'invented_type', 1.0, ?)`,
		now,
	); err != nil {
		t.Fatalf("insert relation with unknown type: %v", err)
	}

	if err := applyMigration(sqlDB, 7, loadMigration007(t)); err != nil {
		t.Fatalf("migration 007 up: %v", err)
	}

	var weight float64
	if err := sqlDB.QueryRow(
		`SELECT weight FROM relations WHERE id = 'r-unknown'`,
	).Scan(&weight); err != nil {
		t.Fatalf("read weight: %v", err)
	}
	const want = 0.5
	if weight != want {
		t.Errorf("weight = %v, want %v (ELSE fallback)", weight, want)
	}
}

// TestMigration007_EmptyTable verifies that migration 007 applies cleanly on a
// database with zero relations (the UPDATE CASE is a no-op).
func TestMigration007_EmptyTable(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 6)

	if err := applyMigration(sqlDB, 7, loadMigration007(t)); err != nil {
		t.Fatalf("migration 007 on empty relations table: %v", err)
	}

	var version int
	if err := sqlDB.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("query schema_version: %v", err)
	}
	if version != 7 {
		t.Errorf("expected schema version 7, got %d", version)
	}
}

// TestMigration007_Indices verifies that the two new indices exist after the migration.
func TestMigration007_Indices(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 6)

	if err := applyMigration(sqlDB, 7, loadMigration007(t)); err != nil {
		t.Fatalf("migration 007 up: %v", err)
	}

	for _, idx := range []string{"idx_relations_weight", "idx_relations_last_traversed"} {
		idx := idx
		t.Run(idx, func(t *testing.T) {
			var name string
			if err := sqlDB.QueryRow(
				`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, idx,
			).Scan(&name); err != nil {
				t.Errorf("index %q not found after migration 007: %v", idx, err)
			}
		})
	}
}

// TestMigration007_RunnerIdempotent verifies that the migration runner skips
// migration 007 when the database is already at version 7.
func TestMigration007_RunnerIdempotent(t *testing.T) {
	dbConn, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer dbConn.Close()

	// Run a second migration pass — should be a no-op.
	if err := migrate(dbConn.DB); err != nil {
		t.Fatalf("second migrate call: %v", err)
	}

	var count int
	if err := dbConn.QueryRow(`SELECT COUNT(*) FROM schema_version WHERE version = 7`).Scan(&count); err != nil {
		t.Fatalf("count schema_version rows for v7: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 schema_version row for v7, got %d", count)
	}
}

// loadMigration007 reads the migration 007 SQL from the embedded FS.
func loadMigration007(t *testing.T) string {
	t.Helper()
	content, err := migrationsFS.ReadFile("migrations/007_weighted_relations.sql")
	if err != nil {
		t.Fatalf("read migration 007: %v", err)
	}
	return string(content)
}
