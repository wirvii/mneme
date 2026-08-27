package db

import (
	"database/sql"
	"testing"
	"time"
)

// TestMigration019 is AC1: migration 019 applies on top of a fully migrated
// v18 database, the two uuid columns appear, memory_sdd_refs and
// sdd_reference_backfill exist, and the marker row is present.
func TestMigration019(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 18)
	seedLegacyBacklogAndSpec(t, sqlDB, "BL-001", "SPEC-001")

	if err := applyMigration(sqlDB, 19, loadMigration019(t)); err != nil {
		t.Fatalf("migration 019 up: %v", err)
	}

	var version int
	if err := sqlDB.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("query schema_version: %v", err)
	}
	if version != 19 {
		t.Errorf("expected schema version 19, got %d", version)
	}

	// The columns exist and default to '' — ensureSDDUUIDs, not this
	// migration, is what fills them (D7).
	var backlogUUID, specUUID string
	if err := sqlDB.QueryRow(`SELECT uuid FROM backlog_items WHERE id = 'BL-001'`).Scan(&backlogUUID); err != nil {
		t.Fatalf("select backlog_items.uuid: %v", err)
	}
	if backlogUUID != "" {
		t.Errorf("expected backlog_items.uuid = '', got %q", backlogUUID)
	}
	if err := sqlDB.QueryRow(`SELECT uuid FROM specs WHERE id = 'SPEC-001'`).Scan(&specUUID); err != nil {
		t.Fatalf("select specs.uuid: %v", err)
	}
	if specUUID != "" {
		t.Errorf("expected specs.uuid = '', got %q", specUUID)
	}

	for _, table := range []string{"memory_sdd_refs", "sdd_reference_backfill"} {
		var name string
		err := sqlDB.QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q not found: %v", table, err)
		}
	}

	var markerCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM sdd_reference_backfill WHERE id = 1`).Scan(&markerCount); err != nil {
		t.Fatalf("query sdd_reference_backfill: %v", err)
	}
	if markerCount != 1 {
		t.Errorf("expected exactly 1 marker row, got %d", markerCount)
	}
}

// TestEnsureSDDUUIDs_LegacyFixture is AC2: opening a database that carries
// rows from before migration 019 (the state a real upgrade leaves) results
// in every backlog_items/specs row holding a non-empty anchor — the
// "self-repairs on every Open" half of D7 mitad A, exercised through the
// exact call migrate() makes rather than through Open() directly, so the
// fixture never has to touch a real file.
func TestEnsureSDDUUIDs_LegacyFixture(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 18)
	seedLegacyBacklogAndSpec(t, sqlDB, "BL-001", "SPEC-001")
	seedLegacyBacklogAndSpec(t, sqlDB, "BL-002", "SPEC-002")

	if err := applyMigration(sqlDB, 19, loadMigration019(t)); err != nil {
		t.Fatalf("migration 019 up: %v", err)
	}

	if err := ensureSDDUUIDs(sqlDB); err != nil {
		t.Fatalf("ensureSDDUUIDs: %v", err)
	}

	var backlogMissing, specMissing int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM backlog_items WHERE uuid = ''`).Scan(&backlogMissing); err != nil {
		t.Fatalf("count backlog_items missing uuid: %v", err)
	}
	if backlogMissing != 0 {
		t.Errorf("expected 0 backlog_items without uuid, got %d", backlogMissing)
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM specs WHERE uuid = ''`).Scan(&specMissing); err != nil {
		t.Fatalf("count specs missing uuid: %v", err)
	}
	if specMissing != 0 {
		t.Errorf("expected 0 specs without uuid, got %d", specMissing)
	}

	// Idempotence: a second call performs zero writes — anchors already
	// assigned must not change.
	anchorsBefore := readAllUUIDs(t, sqlDB)
	if err := ensureSDDUUIDs(sqlDB); err != nil {
		t.Fatalf("ensureSDDUUIDs (second call): %v", err)
	}
	anchorsAfter := readAllUUIDs(t, sqlDB)
	if len(anchorsBefore) != len(anchorsAfter) {
		t.Fatalf("anchor count changed: before=%d after=%d", len(anchorsBefore), len(anchorsAfter))
	}
	for k, v := range anchorsBefore {
		if anchorsAfter[k] != v {
			t.Errorf("anchor for %q changed across idempotent calls: %q -> %q", k, v, anchorsAfter[k])
		}
	}
}

// TestEnsureSDDUUIDs_SelfHealing is AC3: an anchor cleared by hand (uuid="")
// on an otherwise-anchored row comes back on the next call — the invariant
// is permanent, not a one-time migration side effect.
func TestEnsureSDDUUIDs_SelfHealing(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 18)
	seedLegacyBacklogAndSpec(t, sqlDB, "BL-001", "SPEC-001")
	if err := applyMigration(sqlDB, 19, loadMigration019(t)); err != nil {
		t.Fatalf("migration 019 up: %v", err)
	}
	if err := ensureSDDUUIDs(sqlDB); err != nil {
		t.Fatalf("ensureSDDUUIDs (first fill): %v", err)
	}

	if _, err := sqlDB.Exec(`UPDATE backlog_items SET uuid = '' WHERE id = 'BL-001'`); err != nil {
		t.Fatalf("clear backlog_items.uuid by hand: %v", err)
	}
	if _, err := sqlDB.Exec(`UPDATE specs SET uuid = '' WHERE id = 'SPEC-001'`); err != nil {
		t.Fatalf("clear specs.uuid by hand: %v", err)
	}

	if err := ensureSDDUUIDs(sqlDB); err != nil {
		t.Fatalf("ensureSDDUUIDs (self-heal): %v", err)
	}

	var backlogUUID, specUUID string
	if err := sqlDB.QueryRow(`SELECT uuid FROM backlog_items WHERE id = 'BL-001'`).Scan(&backlogUUID); err != nil {
		t.Fatalf("select backlog_items.uuid: %v", err)
	}
	if backlogUUID == "" {
		t.Error("expected backlog_items.uuid to be re-filled after being cleared by hand")
	}
	if err := sqlDB.QueryRow(`SELECT uuid FROM specs WHERE id = 'SPEC-001'`).Scan(&specUUID); err != nil {
		t.Fatalf("select specs.uuid: %v", err)
	}
	if specUUID == "" {
		t.Error("expected specs.uuid to be re-filled after being cleared by hand")
	}
}

// TestSDDUUIDUniqueIndex is AC4: two rows cannot share an anchor, but any
// number of rows may transiently share the empty string (the partial index
// predicate uuid <> "" is what makes that legal).
func TestSDDUUIDUniqueIndex(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 18)
	seedLegacyBacklogAndSpec(t, sqlDB, "BL-001", "SPEC-001")
	seedLegacyBacklogAndSpec(t, sqlDB, "BL-002", "SPEC-002")
	seedLegacyBacklogAndSpec(t, sqlDB, "BL-003", "SPEC-003")
	if err := applyMigration(sqlDB, 19, loadMigration019(t)); err != nil {
		t.Fatalf("migration 019 up: %v", err)
	}

	// Three rows sharing '' must coexist: the partial index only covers
	// uuid <> ''.
	var missingCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM backlog_items WHERE uuid = ''`).Scan(&missingCount); err != nil {
		t.Fatalf("count backlog_items with empty uuid: %v", err)
	}
	if missingCount != 3 {
		t.Fatalf("expected 3 backlog_items sharing uuid='', got %d", missingCount)
	}

	if _, err := sqlDB.Exec(`UPDATE backlog_items SET uuid = 'dup-anchor' WHERE id = 'BL-001'`); err != nil {
		t.Fatalf("assign first anchor: %v", err)
	}
	_, err := sqlDB.Exec(`UPDATE backlog_items SET uuid = 'dup-anchor' WHERE id = 'BL-002'`)
	if err == nil {
		t.Fatal("expected the unique partial index to reject a duplicate non-empty uuid")
	}

	// Same check on specs.
	if _, err := sqlDB.Exec(`UPDATE specs SET uuid = 'dup-anchor-spec' WHERE id = 'SPEC-001'`); err != nil {
		t.Fatalf("assign first spec anchor: %v", err)
	}
	_, err = sqlDB.Exec(`UPDATE specs SET uuid = 'dup-anchor-spec' WHERE id = 'SPEC-002'`)
	if err == nil {
		t.Fatal("expected the unique partial index to reject a duplicate non-empty spec uuid")
	}
}

// TestEnsureSDDUUIDs_BelowSchemaVersion19_NoOp verifies the cheap
// schema-version guard: called against a database that hasn't reached
// migration 19 yet (so the uuid columns don't exist), ensureSDDUUIDs must
// return nil without attempting to touch either table.
func TestEnsureSDDUUIDs_BelowSchemaVersion19_NoOp(t *testing.T) {
	sqlDB := openRawMemory(t)
	applyUpToVersion(t, sqlDB, 18)
	seedLegacyBacklogAndSpec(t, sqlDB, "BL-001", "SPEC-001")

	if err := ensureSDDUUIDs(sqlDB); err != nil {
		t.Fatalf("ensureSDDUUIDs on a pre-019 schema: %v", err)
	}
}

// seedLegacyBacklogAndSpec inserts one backlog_items row and one specs row
// with the columns that existed before migration 019 — reproducing the
// on-disk shape a real database carries between the ALTER TABLE and the
// self-healing fill.
func seedLegacyBacklogAndSpec(t *testing.T, sqlDB *sql.DB, backlogID, specID string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)

	_, err := sqlDB.Exec(
		`INSERT INTO backlog_items (id, title, status, priority, project, position, created_at, updated_at)
		 VALUES (?, ?, 'raw', 'medium', 'proj', 0, ?, ?)`,
		backlogID, "Legacy item "+backlogID, now, now,
	)
	if err != nil {
		t.Fatalf("seed backlog_items %s: %v", backlogID, err)
	}

	_, err = sqlDB.Exec(
		`INSERT INTO specs (id, title, status, project, created_at, updated_at)
		 VALUES (?, ?, 'draft', 'proj', ?, ?)`,
		specID, "Legacy spec "+specID, now, now,
	)
	if err != nil {
		t.Fatalf("seed specs %s: %v", specID, err)
	}
}

// readAllUUIDs snapshots every backlog_items/specs anchor, keyed by
// "backlog:<id>" / "spec:<project>/<id>", so a test can assert idempotence
// without caring about row order.
func readAllUUIDs(t *testing.T, sqlDB *sql.DB) map[string]string {
	t.Helper()
	out := map[string]string{}

	rows, err := sqlDB.Query(`SELECT id, uuid FROM backlog_items`)
	if err != nil {
		t.Fatalf("select backlog_items: %v", err)
	}
	for rows.Next() {
		var id, anchor string
		if err := rows.Scan(&id, &anchor); err != nil {
			rows.Close()
			t.Fatalf("scan backlog_items: %v", err)
		}
		out["backlog:"+id] = anchor
	}
	rows.Close()

	rows, err = sqlDB.Query(`SELECT project, id, uuid FROM specs`)
	if err != nil {
		t.Fatalf("select specs: %v", err)
	}
	for rows.Next() {
		var project, id, anchor string
		if err := rows.Scan(&project, &id, &anchor); err != nil {
			rows.Close()
			t.Fatalf("scan specs: %v", err)
		}
		out["spec:"+project+"/"+id] = anchor
	}
	rows.Close()

	return out
}

// loadMigration019 reads the migration 019 SQL from the embedded FS.
func loadMigration019(t *testing.T) string {
	t.Helper()
	content, err := migrationsFS.ReadFile("migrations/019_sdd_anchors.sql")
	if err != nil {
		t.Fatalf("read migration 019: %v", err)
	}
	return string(content)
}
