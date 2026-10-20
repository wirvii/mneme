package db

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// openRawFile opens a file-backed SQLite database WITHOUT running migrations,
// so a test can build an exact, possibly damaged, schema by hand.
func openRawFile(t *testing.T, path string) *sql.DB {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("sql.Open(%s): %v", path, err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	return raw
}

// schemaFingerprint returns a sorted, comparable description of the shape of
// every non-internal table (PRAGMA table_info: name, type, notnull, default,
// pk, in column order) and every index (name, unique, partial, columns and
// its SQL with whitespace collapsed). Two databases with equal fingerprints
// accept the same statements, which is what "same shape" means for SPEC-178.
func schemaFingerprint(t *testing.T, q interface {
	Query(query string, args ...any) (*sql.Rows, error)
}) []string {
	t.Helper()
	var out []string

	tables, err := q.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	var tableNames []string
	for tables.Next() {
		var n string
		if err := tables.Scan(&n); err != nil {
			t.Fatalf("scan table: %v", err)
		}
		tableNames = append(tableNames, n)
	}
	if err := tables.Err(); err != nil {
		t.Fatalf("iterate tables: %v", err)
	}
	_ = tables.Close()

	for _, table := range tableNames {
		cols, err := q.Query(`SELECT name, type, "notnull", COALESCE(dflt_value, '<null>'), pk FROM pragma_table_info(?) ORDER BY cid`, table)
		if err != nil {
			t.Fatalf("table_info %s: %v", table, err)
		}
		for cols.Next() {
			var name, typ, dflt string
			var notnull, pk int
			if err := cols.Scan(&name, &typ, &notnull, &dflt, &pk); err != nil {
				t.Fatalf("scan column of %s: %v", table, err)
			}
			out = append(out, fmt.Sprintf("table %s col %s %s notnull=%d dflt=%s pk=%d", table, name, typ, notnull, dflt, pk))
		}
		if err := cols.Err(); err != nil {
			t.Fatalf("iterate columns of %s: %v", table, err)
		}
		_ = cols.Close()
	}

	idx, err := q.Query(`SELECT m.tbl_name, l.name, l."unique", l.partial,
		COALESCE((SELECT group_concat(i.name, ',') FROM pragma_index_info(l.name) i), ''),
		COALESCE(m.sql, '')
		FROM sqlite_master m JOIN pragma_index_list(m.tbl_name) l ON l.name = m.name
		WHERE m.type = 'index' ORDER BY m.tbl_name, l.name`)
	if err != nil {
		t.Fatalf("list indexes: %v", err)
	}
	for idx.Next() {
		var tbl, name, cols, ddl string
		var unique, partial int
		if err := idx.Scan(&tbl, &name, &unique, &partial, &cols, &ddl); err != nil {
			t.Fatalf("scan index: %v", err)
		}
		out = append(out, fmt.Sprintf("index %s.%s unique=%d partial=%d cols=%s sql=%s",
			tbl, name, unique, partial, cols, strings.Join(strings.Fields(ddl), " ")))
	}
	if err := idx.Err(); err != nil {
		t.Fatalf("iterate indexes: %v", err)
	}
	_ = idx.Close()

	sort.Strings(out)
	return out
}

// requireSameFingerprint fails the test with the differing lines when two
// fingerprints are not identical.
func requireSameFingerprint(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") == strings.Join(want, "\n") {
		return
	}
	inWant := make(map[string]bool, len(want))
	for _, l := range want {
		inWant[l] = true
	}
	inGot := make(map[string]bool, len(got))
	for _, l := range got {
		inGot[l] = true
	}
	for _, l := range got {
		if !inWant[l] {
			t.Errorf("only in database under test: %s", l)
		}
	}
	for _, l := range want {
		if !inGot[l] {
			t.Errorf("only in fresh database:      %s", l)
		}
	}
}

// freshFingerprint opens a brand-new database and returns its fingerprint.
func freshFingerprint(t *testing.T) []string {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatalf("Open fresh: %v", err)
	}
	defer d.Close()
	return schemaFingerprint(t, d)
}

// dump returns every row of query joined into one string, for before/after
// comparisons of data.
func dump(t *testing.T, d *DB, query string) string {
	t.Helper()
	rows, err := d.Query(query)
	if err != nil {
		t.Fatalf("dump %q: %v", query, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns: %v", err)
	}
	var b strings.Builder
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scan: %v", err)
		}
		fmt.Fprintln(&b, vals...)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	return b.String()
}

// TestOpen_RepairsSpecsDamagedByRerun005 reproduces the reported database: a
// v24 database whose specs/spec_history/spec_pushbacks were rebuilt by a
// re-run of migration 005, losing every column added later. Open must heal it
// with additive changes only, keep the rows, and leave the same shape a new
// database has.
func TestOpen_RepairsSpecsDamagedByRerun005(t *testing.T) {
	path := filepath.Join(t.TempDir(), "damaged.db")

	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO specs (id, title, status, project, assigned_agents, files_changed, created_at, updated_at, uuid)
		VALUES ('SPEC-001', 'T', 'draft', 'p', '[]', '[]', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 'u-1')`); err != nil {
		t.Fatalf("insert spec: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO spec_history (id, spec_id, from_status, to_status, by, reason, at, reviewed_sha)
		VALUES ('h1', 'SPEC-001', 'draft', 'speccing', 'o', '', '2026-01-01T00:00:00Z', 'sha1')`); err != nil {
		t.Fatalf("insert history: %v", err)
	}
	if _, err := d.Exec(loadMigration005(t)); err != nil {
		t.Fatalf("re-run 005 by hand: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	healed, err := Open(path)
	if err != nil {
		t.Fatalf("Open damaged database: %v", err)
	}
	defer healed.Close()

	requireSameFingerprint(t, schemaFingerprint(t, healed), freshFingerprint(t))

	var uuid string
	if err := healed.QueryRow(`SELECT uuid FROM specs WHERE id = 'SPEC-001'`).Scan(&uuid); err != nil {
		t.Fatalf("spec row lost: %v", err)
	}
	if uuid == "" {
		t.Error("spec uuid is empty after repair")
	}
	var historyRows int
	if err := healed.QueryRow(`SELECT COUNT(*) FROM spec_history WHERE id = 'h1'`).Scan(&historyRows); err != nil {
		t.Fatalf("count history: %v", err)
	}
	if historyRows != 1 {
		t.Errorf("spec_history rows = %d, want 1", historyRows)
	}

	if _, err := healed.Exec(`UPDATE specs SET lane = 'bogus' WHERE id = 'SPEC-001'`); err == nil {
		t.Error("lane = 'bogus' was accepted, the CHECK constraint is missing")
	}

	const specsData = `SELECT * FROM specs ORDER BY project, id`
	const historyData = `SELECT * FROM spec_history ORDER BY id`
	beforeSpecs, beforeHistory := dump(t, healed, specsData), dump(t, healed, historyData)
	beforeShape := schemaFingerprint(t, healed)
	if err := healed.Close(); err != nil {
		t.Fatalf("close healed: %v", err)
	}

	again, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer again.Close()
	requireSameFingerprint(t, schemaFingerprint(t, again), beforeShape)
	if got := dump(t, again, specsData); got != beforeSpecs {
		t.Errorf("specs data changed on second Open:\n%s\nvs\n%s", got, beforeSpecs)
	}
	if got := dump(t, again, historyData); got != beforeHistory {
		t.Errorf("spec_history data changed on second Open:\n%s\nvs\n%s", got, beforeHistory)
	}
}

// TestEnsureSpecRepairs_NoOpCases covers the two situations where the repair
// functions must leave the database alone: the tables do not exist yet, and
// the database is already healthy.
func TestEnsureSpecRepairs_NoOpCases(t *testing.T) {
	repairs := map[string]func(sqlExecutor) error{
		"ensureSpecColumns":            ensureSpecColumns,
		"ensureSpecHistoryReviewedSHA": ensureSpecHistoryReviewedSHA,
	}
	for name, repair := range repairs {
		t.Run(name+"/tables absent", func(t *testing.T) {
			raw := openRawMemory(t)
			if err := repair(raw); err != nil {
				t.Fatalf("%s on an empty database: %v", name, err)
			}
			var n int
			if err := raw.QueryRow(`SELECT COUNT(*) FROM sqlite_master`).Scan(&n); err != nil {
				t.Fatalf("count schema objects: %v", err)
			}
			if n != 0 {
				t.Errorf("%s created %d schema objects on an empty database", name, n)
			}
		})
		t.Run(name+"/healthy database", func(t *testing.T) {
			d, err := OpenMemory()
			if err != nil {
				t.Fatalf("OpenMemory: %v", err)
			}
			defer d.Close()
			before := schemaFingerprint(t, d)
			if err := repair(d.DB); err != nil {
				t.Fatalf("%s on a healthy database: %v", name, err)
			}
			requireSameFingerprint(t, schemaFingerprint(t, d), before)
		})
	}
}

// latestEmbeddedVersion is the highest version registered by the embedded
// migrations, computed from their file names so no number is hard-coded.
func latestEmbeddedVersion(t *testing.T) int {
	t.Helper()
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	latest := 0
	for _, e := range entries {
		v, err := versionFromFilename(e.Name())
		if err != nil {
			t.Fatalf("version of %q: %v", e.Name(), err)
		}
		if v > latest {
			latest = v
		}
	}
	return latest
}

// TestOpen_V21WithReviewedSHAAlreadyPresent is the BL-272 case: a database
// below version 22 that already has spec_history.reviewed_sha (added by hand
// or by an old beta) must open instead of failing migration 022 with
// "duplicate column name", and keep the value already stored in the column.
func TestOpen_V21WithReviewedSHAAlreadyPresent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v21.db")

	raw := openRawFile(t, path)
	applyUpToVersion(t, raw, 21)
	if _, err := raw.Exec(`ALTER TABLE spec_history ADD COLUMN reviewed_sha TEXT NOT NULL DEFAULT ''`); err != nil {
		t.Fatalf("add reviewed_sha by hand: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO spec_history (id, spec_id, from_status, to_status, by, reason, at, reviewed_sha)
		VALUES ('h1', 'SPEC-001', 'qa', 'done', 'qa', '', '2026-01-01T00:00:00Z', 'abc123')`); err != nil {
		t.Fatalf("insert history row: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	var version int
	if err := d.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if want := latestEmbeddedVersion(t); version != want {
		t.Errorf("MAX(version) = %d, want %d", version, want)
	}

	var columns int
	if err := d.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('spec_history') WHERE name = 'reviewed_sha'`).Scan(&columns); err != nil {
		t.Fatalf("count column: %v", err)
	}
	if columns != 1 {
		t.Errorf("reviewed_sha appears %d times, want 1", columns)
	}

	var sha string
	if err := d.QueryRow(`SELECT reviewed_sha FROM spec_history WHERE id = 'h1'`).Scan(&sha); err != nil {
		t.Fatalf("read value: %v", err)
	}
	if sha != "abc123" {
		t.Errorf("reviewed_sha = %q, want %q", sha, "abc123")
	}
}
