package db

import (
	"database/sql"
	"testing"
	"time"
)

func TestMigration022_DeliverySchemaAndLegacySpecs(t *testing.T) {
	db := openRawMemory(t)
	applyUpToVersion(t, db, 21)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := db.Exec(`INSERT INTO specs (id,title,status,project,lane,assigned_agents,files_changed,created_at,updated_at) VALUES ('SPEC-OLD','old','draft','p','standard','[]','[]',?,?)`, now, now)
	if err != nil {
		t.Fatalf("seed spec: %v", err)
	}
	if err := applyMigration(db, 22, loadMigration022(t)); err != nil {
		t.Fatalf("apply: %v", err)
	}

	var version int
	if err := db.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil || version != 22 {
		t.Fatalf("version=%d err=%v", version, err)
	}
	tables := []string{"execution_contracts", "execution_criteria", "execution_constraints", "execution_findings", "execution_history", "delivery_certificates", "delivery_checks"}
	for _, table := range tables {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s count=%d err=%v", table, n, err)
		}
	}
	var executionModel string
	if err := db.QueryRow(`SELECT execution_model FROM specs WHERE id='SPEC-OLD'`).Scan(&executionModel); err != nil || executionModel != "legacy" {
		t.Fatalf("legacy model=%q err=%v", executionModel, err)
	}
	assertColumnAbsent(t, db, "execution_findings", "blocking")
	assertColumnAbsent(t, db, "delivery_checks", "acked_by")
	assertColumnAbsent(t, db, "delivery_checks", "justification")
	var fk int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys=%d err=%v", fk, err)
	}
	if _, err := db.Exec(`INSERT INTO execution_criteria(id,work_id,seq,criterion_key,declaration,created_at) VALUES('x','missing',1,'AC1','x',?)`, now); err == nil {
		t.Fatal("orphan criterion inserted")
	}
	seedExecutionContract(t, db, "WORK-001", now)
	if _, err := db.Exec(`INSERT INTO execution_criteria(id,work_id,seq,criterion_key,declaration,created_at) VALUES('criterion','WORK-001',1,'AC1','x',?)`, now); err != nil {
		t.Fatalf("child: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM execution_contracts WHERE id='WORK-001'`); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var children int
	if err := db.QueryRow(`SELECT COUNT(*) FROM execution_criteria WHERE work_id='WORK-001'`).Scan(&children); err != nil || children != 0 {
		t.Fatalf("cascade count=%d err=%v", children, err)
	}
}

func TestMigration022_RunnerDoesNotReapply(t *testing.T) {
	database, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := migrate(database.DB); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM schema_version WHERE version=22`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows=%d err=%v", n, err)
	}
}

func TestMigration022_Indexes(t *testing.T) {
	db := openRawMemory(t)
	applyUpToVersion(t, db, 21)
	if err := applyMigration(db, 22, loadMigration022(t)); err != nil {
		t.Fatal(err)
	}
	indexes := []string{"idx_execution_contracts_uuid", "idx_execution_contracts_status", "idx_execution_contracts_source", "idx_execution_criteria_key", "idx_execution_criteria_work", "idx_execution_constraints_key", "idx_execution_constraints_work", "idx_execution_findings_work", "idx_execution_findings_status", "idx_execution_history_work", "idx_delivery_certs_work", "idx_delivery_checks_cert"}
	for _, name := range indexes {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, name).Scan(&n); err != nil || n != 1 {
			t.Errorf("index %s count=%d err=%v", name, n, err)
		}
	}
}

func loadMigration022(t *testing.T) string {
	t.Helper()
	b, err := migrationsFS.ReadFile("migrations/022_delivery_workflow_v2.sql")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func assertColumnAbsent(t *testing.T, db *sql.DB, table, column string) {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var def any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &def, &pk); err != nil {
			t.Fatal(err)
		}
		if name == column {
			t.Errorf("%s unexpectedly has %s", table, column)
		}
	}
}
func seedExecutionContract(t *testing.T, db *sql.DB, id, now string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO execution_contracts(id,project,source_type,status,goal,scope_json,verification_json,development_method,created_at,updated_at) VALUES(?, 'p','organic','draft','goal','["x"]','["build"]','standard',?,?)`, id, now, now)
	if err != nil {
		t.Fatalf("seed contract: %v", err)
	}
}
