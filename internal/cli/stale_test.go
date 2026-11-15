package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/model"
)

// staleProjectDBPath is the database runBacklogCmd's --data-dir/--project
// pair resolves to.
func staleProjectDBPath(dataDir, project string) string {
	return filepath.Join(dataDir, "projects", project+".db")
}

// ageSpecAt makes spec id look days whole days old: it writes that instant
// into specs.updated_at AND every spec_history.at row of the spec, because
// the age is the later of the two (a `spec advance` writes a fresh history
// row that would cancel the aging). It fails when no specs row matched, so
// a wrong id or database fails here instead of far from the cause.
func ageSpecAt(t *testing.T, dbPath, id string, days int) {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("close db: %v", closeErr)
		}
	}()
	ctx := context.Background()
	at := time.Now().Add(-time.Duration(days) * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
	res, err := database.ExecContext(ctx, `UPDATE specs SET updated_at = ? WHERE id = ?`, at, id)
	if err != nil {
		t.Fatalf("age spec %s: %v", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		t.Fatalf("age spec %s: no specs row matched in %s", id, dbPath)
	}
	if _, err := database.ExecContext(ctx, `UPDATE spec_history SET at = ? WHERE spec_id = ?`, at, id); err != nil {
		t.Fatalf("age history of %s: %v", id, err)
	}
}

// setSpecStatusAt sets specs.status of id by SQL, to fabricate qa or
// specced without walking the lifecycle (fixture by SQL, the same
// criterion as SPEC-133's unreadable rows).
func setSpecStatusAt(t *testing.T, dbPath, id string, status model.SpecStatus) {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("close db: %v", closeErr)
		}
	}()
	res, err := database.ExecContext(context.Background(), `UPDATE specs SET status = ? WHERE id = ?`, string(status), id)
	if err != nil {
		t.Fatalf("set status of %s: %v", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		t.Fatalf("set status of %s: no specs row matched in %s", id, dbPath)
	}
}

// mustRunCLI runs argv through runBacklogCmd and fails on error.
func mustRunCLI(t *testing.T, dataDir, project string, argv ...string) string {
	t.Helper()
	stdout, stderr, err := runBacklogCmd(t, dataDir, project, argv...)
	if err != nil {
		t.Fatalf("%v: %v (stderr=%s)", argv, err, stderr)
	}
	return stdout
}

// seedFrozenSpecCLI creates a backlog item, promotes it to spec specID,
// advances it once and archives the item: a frozen spec, 0 days old.
func seedFrozenSpecCLI(t *testing.T, dataDir, project, itemID, specID string) {
	t.Helper()
	mustRunCLI(t, dataDir, project, "backlog", "add", "To archive", "--lane", "standard")
	mustRunCLI(t, dataDir, project, "backlog", "refine", itemID, "--refinement", "details")
	mustRunCLI(t, dataDir, project, "backlog", "promote", itemID)
	mustRunCLI(t, dataDir, project, "spec", "advance", specID, "--by", "orchestrator")
	mustRunCLI(t, dataDir, project, "backlog", "archive", itemID, "--reason", "abandoned")
}

// staleSectionLines returns the lines of the STALE section of a status
// output, header excluded, up to the next section header.
func staleSectionLines(stdout string) []string {
	lines := strings.Split(stdout, "\n")
	var section []string
	inSection := false
	for _, line := range lines {
		if strings.HasPrefix(line, "--- ") {
			if inSection {
				break
			}
			inSection = strings.HasPrefix(line, "--- STALE")
			continue
		}
		if inSection {
			section = append(section, line)
		}
	}
	return section
}

// TestStatus_Stale pins SPEC-186 C6: the STALE section of `mneme status`
// and the stale_specs key of `status --json`.
func TestStatus_Stale(t *testing.T) {
	t.Run("SectionAndJSON", func(t *testing.T) {
		dataDir := t.TempDir()
		project := "test-status-stale"
		dbPath := staleProjectDBPath(dataDir, project)

		mustRunCLI(t, dataDir, project, "backlog", "add", "Live item", "--lane", "standard")
		for i := 1; i <= 12; i++ {
			mustRunCLI(t, dataDir, project, "spec", "new", fmt.Sprintf("Stale spec %d", i), "--lane", "standard")
		}
		for i := 1; i <= 12; i++ {
			id := fmt.Sprintf("SPEC-%03d", i)
			switch {
			case i <= 5:
				setSpecStatusAt(t, dbPath, id, model.SpecStatusQA)
			case i <= 10:
				setSpecStatusAt(t, dbPath, id, model.SpecStatusSpecced)
			}
			ageSpecAt(t, dbPath, id, 101-i)
		}
		seedFrozenSpecCLI(t, dataDir, project, "BL-002", "SPEC-013")
		insertRawUnreadableSpecInProgressCLI(t, dataDir, project, "SPEC-901")

		stdout := mustRunCLI(t, dataDir, project, "status")
		staleIdx := strings.Index(stdout, "--- STALE")
		if staleIdx < 0 {
			t.Fatalf("no --- STALE section:\n%s", stdout)
		}
		for _, later := range []string{"--- BACKLOG", "--- SPECS IN PROGRESS"} {
			idx := strings.Index(stdout, later)
			if idx < 0 || idx < staleIdx {
				t.Errorf("%s at %d, want it present and after --- STALE at %d", later, idx, staleIdx)
			}
		}
		const headline = "12 specs have not moved for more than 14 days (qa 5, specced 5, draft 2):"
		if !strings.Contains(stdout, "  "+headline+"\n") {
			t.Errorf("headline %q missing:\n%s", headline, stdout)
		}

		var rows []string
		for _, line := range staleSectionLines(stdout) {
			if strings.HasPrefix(line, "  SPEC-") {
				rows = append(rows, line)
			}
		}
		if len(rows) != 10 {
			t.Fatalf("STALE has %d spec rows, want 10:\n%s", len(rows), strings.Join(rows, "\n"))
		}
		for i, row := range rows {
			wantID := fmt.Sprintf("SPEC-%03d", i+1)
			wantDays := fmt.Sprintf("%4d d", 100-i)
			if !strings.HasPrefix(row, "  "+wantID) || !strings.Contains(row, wantDays) {
				t.Errorf("row %d = %q, want %s at %s (days descending)", i, row, wantID, strings.TrimSpace(wantDays))
			}
		}
		for _, want := range []string{
			"  SPEC-001    [qa]               100 d  Stale spec 1\n",
			"  and 2 more (mneme spec list marks them all)\n",
			"  1 frozen spec (backlog item archived or missing) is not counted here: it can never move.\n",
			"  1 spec row could not be read and was not classified (see UNREADABLE above).\n",
		} {
			if !strings.Contains(stdout, want) {
				t.Errorf("status lacks %q:\n%s", want, stdout)
			}
		}

		jsonOut := mustRunCLI(t, dataDir, project, "status", "--json")
		var out struct {
			StaleSpecs *model.StaleReport `json:"stale_specs"`
		}
		if err := json.Unmarshal([]byte(jsonOut), &out); err != nil {
			t.Fatalf("status --json did not decode: %v\n%s", err, jsonOut)
		}
		rep := out.StaleSpecs
		if rep == nil {
			t.Fatalf("status --json lacks stale_specs:\n%s", jsonOut)
		}
		wantBy := map[model.SpecStatus]int{model.SpecStatusQA: 5, model.SpecStatusSpecced: 5, model.SpecStatusDraft: 2}
		if rep.Count != 12 || len(rep.Specs) != 12 || rep.FrozenCount != 1 || rep.UnreadableCount != 1 {
			t.Errorf("stale_specs = count %d, specs %d, frozen %d, unreadable %d; want 12, 12, 1, 1",
				rep.Count, len(rep.Specs), rep.FrozenCount, rep.UnreadableCount)
		}
		if fmt.Sprint(rep.ByStatus) != fmt.Sprint(wantBy) {
			t.Errorf("by_status = %v, want %v", rep.ByStatus, wantBy)
		}
	})

	t.Run("FrozenOnlyOpensNoSection", func(t *testing.T) {
		dataDir := t.TempDir()
		project := "test-status-stale-frozen-only"
		seedFrozenSpecCLI(t, dataDir, project, "BL-001", "SPEC-001")

		if stdout := mustRunCLI(t, dataDir, project, "status"); strings.Contains(stdout, "--- STALE") {
			t.Errorf("a frozen spec alone opened the STALE section:\n%s", stdout)
		}
		var out map[string]any
		jsonOut := mustRunCLI(t, dataDir, project, "status", "--json")
		if err := json.Unmarshal([]byte(jsonOut), &out); err != nil {
			t.Fatalf("status --json did not decode: %v", err)
		}
		if _, present := out["stale_specs"]; present {
			t.Errorf("stale_specs present with nothing stale: %v", out["stale_specs"])
		}
	})
}

// TestSpecList_StaleSuffixAndJSON pins SPEC-186 C5: `mneme spec list
// --json` stays a bare list whose elements gain stale and
// days_since_last_move, and the table marks a stale row and explains the
// mark in a closing note.
func TestSpecList_StaleSuffixAndJSON(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-spec-list-stale"
	mustRunCLI(t, dataDir, project, "spec", "new", "Fresh", "--lane", "standard")
	mustRunCLI(t, dataDir, project, "spec", "new", "Forgotten", "--lane", "standard")
	ageSpecAt(t, staleProjectDBPath(dataDir, project), "SPEC-002", 20)

	jsonOut := mustRunCLI(t, dataDir, project, "spec", "list", "--json")
	var list []any
	if err := json.Unmarshal([]byte(jsonOut), &list); err != nil {
		t.Fatalf("spec list --json is not a bare list: %v\n%s", err, jsonOut)
	}
	if len(list) != 2 {
		t.Fatalf("len = %d, want 2", len(list))
	}
	for i, raw := range list {
		obj, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("element %d is %T", i, raw)
		}
		for _, key := range []string{"stale", "days_since_last_move"} {
			if _, present := obj[key]; !present {
				t.Errorf("element %d lacks %q: %v", i, key, obj)
			}
		}
		if obj["id"] == "SPEC-002" && (obj["stale"] != true || obj["days_since_last_move"] != float64(20)) {
			t.Errorf("SPEC-002 = stale %v, days %v; want true, 20", obj["stale"], obj["days_since_last_move"])
		}
		if obj["id"] == "SPEC-001" && obj["stale"] != false {
			t.Errorf("SPEC-001 stale = %v, want false", obj["stale"])
		}
	}

	table := mustRunCLI(t, dataDir, project, "spec", "list")
	if !strings.Contains(table, "Forgotten  — stale, 20 d\n") {
		t.Errorf("stale row not marked:\n%s", table)
	}
	if strings.Contains(table, "Fresh  — stale") {
		t.Errorf("fresh row marked stale:\n%s", table)
	}
	if !strings.Contains(table, "\n1 spec has not moved for more than 14 days") {
		t.Errorf("closing note missing:\n%s", table)
	}
}
