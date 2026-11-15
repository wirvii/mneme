package service

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/store"
)

// staleTestDay is one whole day, the unit every staleness fixture is
// expressed in.
const staleTestDay = 24 * time.Hour

// staleTestNow is the fixed clock every staleness test measures against.
var staleTestNow = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

// daysAgo returns the instant n whole days before staleTestNow.
func daysAgo(n int) time.Time {
	return staleTestNow.Add(-time.Duration(n) * staleTestDay)
}

// TestClassifyStaleness pins SPEC-186 C1: the pure classifier with an
// injected clock and threshold 14 — the strict whole-day boundary, the
// future clamp, done and frozen specs never stale, a spec without a
// BacklogID never frozen, the report order and ByStatus, and 0 disabling
// everything.
func TestClassifyStaleness(t *testing.T) {
	const threshold = 14
	specs := []*model.Spec{
		{ID: "SPEC-010", Title: "fifteen", Status: model.SpecStatusQA, UpdatedAt: daysAgo(15)},
		{ID: "SPEC-011", Title: "thirteen", Status: model.SpecStatusDraft, UpdatedAt: daysAgo(13)},
		{ID: "SPEC-012", Title: "edge", Status: model.SpecStatusDraft, UpdatedAt: daysAgo(15).Add(time.Nanosecond)},
		{ID: "SPEC-013", Title: "future", Status: model.SpecStatusDraft, UpdatedAt: staleTestNow.Add(2 * staleTestDay)},
		{ID: "SPEC-014", Title: "done", Status: model.SpecStatusDone, UpdatedAt: daysAgo(90)},
		{ID: "SPEC-015", Title: "frozen", Status: model.SpecStatusSpecced, BacklogID: "BL-001", UpdatedAt: daysAgo(90)},
		{ID: "SPEC-017", Title: "no backlog b", Status: model.SpecStatusSpecced, UpdatedAt: daysAgo(90)},
		{ID: "SPEC-016", Title: "no backlog a", Status: model.SpecStatusSpecced, UpdatedAt: daysAgo(90)},
		{ID: "SPEC-018", Title: "qa twenty", Status: model.SpecStatusQA, UpdatedAt: daysAgo(20)},
	}
	frozen := map[string]model.SpecFreeze{
		"SPEC-015": {State: model.SpecFreezeArchived, BacklogID: "BL-001"},
	}

	ages, report := classifyStaleness(specs, nil, frozen, staleTestNow, threshold)

	cases := []struct {
		id    string
		stale bool
		days  int
	}{
		{"SPEC-010", true, 15},
		{"SPEC-011", false, 13},
		{"SPEC-012", false, 14},
		{"SPEC-013", false, 0},
		{"SPEC-014", false, 90},
		{"SPEC-015", false, 90},
		{"SPEC-016", true, 90},
		{"SPEC-017", true, 90},
		{"SPEC-018", true, 20},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			got, ok := ages[tc.id]
			if !ok {
				t.Fatalf("no age for %s", tc.id)
			}
			want := model.SpecAge{DaysSinceLastMove: tc.days, Stale: tc.stale}
			if got != want {
				t.Errorf("age = %+v, want %+v", got, want)
			}
		})
	}
	if len(ages) != len(specs) {
		t.Errorf("len(ages) = %d, want one per spec (%d)", len(ages), len(specs))
	}

	t.Run("Report", func(t *testing.T) {
		wantIDs := []string{"SPEC-016", "SPEC-017", "SPEC-018", "SPEC-010"}
		var gotIDs []string
		for _, s := range report.Specs {
			gotIDs = append(gotIDs, s.ID)
		}
		if !reflect.DeepEqual(gotIDs, wantIDs) {
			t.Errorf("report order = %v, want %v (days desc, then ID asc)", gotIDs, wantIDs)
		}
		if report.Count != 4 || report.Count != len(report.Specs) {
			t.Errorf("Count = %d (len %d), want 4", report.Count, len(report.Specs))
		}
		wantBy := map[model.SpecStatus]int{model.SpecStatusSpecced: 2, model.SpecStatusQA: 2}
		if !reflect.DeepEqual(report.ByStatus, wantBy) {
			t.Errorf("ByStatus = %v, want %v", report.ByStatus, wantBy)
		}
		if report.FrozenCount != 1 {
			t.Errorf("FrozenCount = %d, want 1", report.FrozenCount)
		}
		if report.ThresholdDays != threshold {
			t.Errorf("ThresholdDays = %d, want %d", report.ThresholdDays, threshold)
		}
		if len(report.Specs) == 0 {
			t.Fatal("report has no stale specs")
		}
		first := report.Specs[0]
		if first.Title != "no backlog a" || first.Status != model.SpecStatusSpecced || first.DaysSinceLastMove != 90 {
			t.Errorf("first row = %+v, want SPEC-016 specced 90 d", first)
		}
	})

	t.Run("ThresholdZeroDisables", func(t *testing.T) {
		ages0, report0 := classifyStaleness(specs, nil, frozen, staleTestNow, 0)
		for id, age := range ages0 {
			if age.Stale {
				t.Errorf("%s stale with threshold 0", id)
			}
		}
		if report0.Count != 0 || len(report0.Specs) != 0 || len(report0.ByStatus) != 0 {
			t.Errorf("report with threshold 0 = %+v, want nothing stale", report0)
		}
		if report0.ThresholdDays != 0 {
			t.Errorf("ThresholdDays = %d, want 0", report0.ThresholdDays)
		}
		if ages0["SPEC-014"].DaysSinceLastMove != 90 {
			t.Errorf("ages are still computed with threshold 0: got %+v", ages0["SPEC-014"])
		}
	})

	t.Run("UsesLatestHistoryOverUpdatedAt", func(t *testing.T) {
		one := []*model.Spec{{ID: "SPEC-020", Status: model.SpecStatusQA, UpdatedAt: daysAgo(40)}}
		history := map[string]time.Time{"SPEC-020": daysAgo(3)}
		agesH, reportH := classifyStaleness(one, history, nil, staleTestNow, threshold)
		if got := agesH["SPEC-020"]; got != (model.SpecAge{DaysSinceLastMove: 3}) {
			t.Errorf("age = %+v, want 3 days, not stale (history is the later mark)", got)
		}
		if reportH.Count != 0 {
			t.Errorf("Count = %d, want 0", reportH.Count)
		}
	})
}

// TestStaleAfterDays pins where the threshold comes from: the service's
// config, 0 respected as "disabled", and the default when a test service
// was built without a config.
func TestStaleAfterDays(t *testing.T) {
	svc := newTestSDDService(t, "stale-threshold")
	if got := svc.staleAfterDays(); got != 14 {
		t.Errorf("default config threshold = %d, want 14", got)
	}
	svc.config.Spec.StaleAfterDays = 0
	if got := svc.staleAfterDays(); got != 0 {
		t.Errorf("configured 0 = %d, want 0 (disabled, not replaced by the default)", got)
	}
	bare := &SDDService{}
	if got := bare.staleAfterDays(); got != 14 {
		t.Errorf("nil config threshold = %d, want the default 14", got)
	}
}

// newTestSDDServiceWithDB is newTestSDDService that also returns the
// database, so a staleness test can age rows by SQL, and pins the service
// clock to staleTestNow.
func newTestSDDServiceWithDB(t *testing.T, project string) (*SDDService, *db.DB) {
	t.Helper()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })

	cfg := config.Default()
	cfg.Workflow.Dir = t.TempDir()
	svc := NewSDDService(store.NewSDDStore(database), cfg, project, nil)
	svc.now = func() time.Time { return staleTestNow }
	return svc, database
}

// setSpecUpdatedAt fixes specs.updated_at of id, in RFC3339Nano like the
// store writes it.
func setSpecUpdatedAt(t *testing.T, database *db.DB, id string, at time.Time) {
	t.Helper()
	res, err := database.ExecContext(context.Background(),
		`UPDATE specs SET updated_at = ? WHERE id = ?`, at.UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		t.Fatalf("set updated_at of %s: %v", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		t.Fatalf("set updated_at of %s: no row matched", id)
	}
}

// setSpecStatus sets specs.status of id by SQL, to fabricate a status
// without walking the lifecycle.
func setSpecStatus(t *testing.T, database *db.DB, id string, status model.SpecStatus) {
	t.Helper()
	res, err := database.ExecContext(context.Background(),
		`UPDATE specs SET status = ? WHERE id = ?`, string(status), id)
	if err != nil {
		t.Fatalf("set status of %s: %v", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		t.Fatalf("set status of %s: no row matched", id)
	}
}

// insertSpecHistoryAt inserts ONE spec_history row for specID at the given
// instant. A freshly created spec has no history (SpecNew's insertHistory is
// a no-op, plan E6): this is the only way these tests fabricate it.
func insertSpecHistoryAt(t *testing.T, database *db.DB, specID string, at time.Time) {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	if _, err := database.ExecContext(context.Background(),
		`INSERT INTO spec_history (id, spec_id, from_status, to_status, by, reason, at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id.String(), specID, "draft", "speccing", "test", "", at.UTC().Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("insert history for %s: %v", specID, err)
	}
}

// newStaleTestSpec creates one standard spec through the service.
func newStaleTestSpec(t *testing.T, svc *SDDService, title string) *model.Spec {
	t.Helper()
	spec, err := svc.SpecNew(context.Background(), model.SpecNewRequest{Title: title, Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("spec new %q: %v", title, err)
	}
	return spec
}

// promoteStaleTestSpec creates a backlog item, refines and promotes it, and
// returns the spec plus the item's ID (so the caller may archive it).
func promoteStaleTestSpec(t *testing.T, svc *SDDService, title string) (*model.Spec, string) {
	t.Helper()
	ctx := context.Background()
	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: title, Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("backlog add %q: %v", title, err)
	}
	if _, err := svc.BacklogRefine(ctx, model.BacklogRefineRequest{ID: item.ID, Refinement: "refined for the stale test"}); err != nil {
		t.Fatalf("backlog refine %s: %v", item.ID, err)
	}
	spec, err := svc.BacklogPromote(ctx, item.ID)
	if err != nil {
		t.Fatalf("backlog promote %s: %v", item.ID, err)
	}
	return spec, item.ID
}

// archiveStaleTestItem archives a backlog item, freezing its spec.
func archiveStaleTestItem(t *testing.T, svc *SDDService, itemID string) {
	t.Helper()
	if _, err := svc.BacklogArchive(context.Background(), model.BacklogArchiveRequest{ID: itemID, Reason: "freeze for the stale test"}); err != nil {
		t.Fatalf("backlog archive %s: %v", itemID, err)
	}
}

// staleReportOf runs StaleSpecs on svc's own project and fails on error.
func staleReportOf(t *testing.T, svc *SDDService) model.StaleReport {
	t.Helper()
	report, err := svc.StaleSpecs(context.Background(), "")
	if err != nil {
		t.Fatalf("StaleSpecs: %v", err)
	}
	return report
}

// specAgeOf returns the age SpecList computes for id.
func specAgeOf(t *testing.T, svc *SDDService, id string) model.SpecAge {
	t.Helper()
	resp, err := svc.SpecList(context.Background(), model.SpecListRequest{})
	if err != nil {
		t.Fatalf("SpecList: %v", err)
	}
	age, ok := resp.Ages[id]
	if !ok {
		t.Fatalf("SpecList has no age for %s", id)
	}
	return age
}

// TestStaleSpecs pins SPEC-186 C2 against real SQLite with an injected
// clock: the last move is the later of updated_at and the newest history
// row, a frozen spec is counted apart and never stale, a draft without
// history counts, and the threshold comes from the service's config. Each
// subtest owns its own service and project so no case leaks into another's
// counts.
func TestStaleSpecs(t *testing.T) {
	t.Run("HistoryNewerThanUpdatedAt", func(t *testing.T) {
		svc, database := newTestSDDServiceWithDB(t, "stale-history-newer")
		spec := newStaleTestSpec(t, svc, "history newer")
		setSpecUpdatedAt(t, database, spec.ID, daysAgo(20))
		insertSpecHistoryAt(t, database, spec.ID, daysAgo(2))

		if got := specAgeOf(t, svc, spec.ID); got != (model.SpecAge{DaysSinceLastMove: 2}) {
			t.Errorf("age = %+v, want 2 days, not stale", got)
		}
		if report := staleReportOf(t, svc); report.Count != 0 {
			t.Errorf("Count = %d, want 0", report.Count)
		}
	})

	t.Run("UpdatedAtNewerThanHistory", func(t *testing.T) {
		svc, database := newTestSDDServiceWithDB(t, "stale-updated-newer")
		spec := newStaleTestSpec(t, svc, "updated newer")
		setSpecUpdatedAt(t, database, spec.ID, daysAgo(2))
		insertSpecHistoryAt(t, database, spec.ID, daysAgo(20))

		if got := specAgeOf(t, svc, spec.ID); got != (model.SpecAge{DaysSinceLastMove: 2}) {
			t.Errorf("age = %+v, want 2 days, not stale", got)
		}
		if report := staleReportOf(t, svc); report.Count != 0 {
			t.Errorf("Count = %d, want 0", report.Count)
		}
	})

	t.Run("BothOld", func(t *testing.T) {
		svc, database := newTestSDDServiceWithDB(t, "stale-both-old")
		spec := newStaleTestSpec(t, svc, "both old")
		setSpecUpdatedAt(t, database, spec.ID, daysAgo(20))
		insertSpecHistoryAt(t, database, spec.ID, daysAgo(20))

		if got := specAgeOf(t, svc, spec.ID); got != (model.SpecAge{DaysSinceLastMove: 20, Stale: true}) {
			t.Errorf("age = %+v, want 20 days, stale", got)
		}
		report := staleReportOf(t, svc)
		if report.Count != 1 || len(report.Specs) != 1 || report.Specs[0].DaysSinceLastMove != 20 {
			t.Errorf("report = %+v, want one stale spec at 20 days", report)
		}
	})

	t.Run("FrozenNeverStale", func(t *testing.T) {
		svc, database := newTestSDDServiceWithDB(t, "stale-frozen")
		spec, itemID := promoteStaleTestSpec(t, svc, "frozen")
		archiveStaleTestItem(t, svc, itemID)
		setSpecUpdatedAt(t, database, spec.ID, daysAgo(20))

		if got := specAgeOf(t, svc, spec.ID); got.Stale {
			t.Errorf("frozen spec reported stale: %+v", got)
		}
		report := staleReportOf(t, svc)
		if report.Count != 0 || report.FrozenCount != 1 {
			t.Errorf("Count = %d, FrozenCount = %d; want 0 and 1", report.Count, report.FrozenCount)
		}
	})

	t.Run("DraftWithoutHistoryCounts", func(t *testing.T) {
		svc, database := newTestSDDServiceWithDB(t, "stale-draft")
		spec := newStaleTestSpec(t, svc, "draft")
		setSpecUpdatedAt(t, database, spec.ID, daysAgo(20))

		if got := specAgeOf(t, svc, spec.ID); got != (model.SpecAge{DaysSinceLastMove: 20, Stale: true}) {
			t.Errorf("age = %+v, want 20 days, stale", got)
		}
		report := staleReportOf(t, svc)
		if report.Count != 1 || report.ByStatus[model.SpecStatusDraft] != 1 {
			t.Errorf("report = %+v, want one stale draft", report)
		}
	})

	t.Run("ConfiguredThreshold30", func(t *testing.T) {
		svc, database := newTestSDDServiceWithDB(t, "stale-threshold-30")
		svc.config.Spec.StaleAfterDays = 30
		spec := newStaleTestSpec(t, svc, "both old")
		setSpecUpdatedAt(t, database, spec.ID, daysAgo(20))
		insertSpecHistoryAt(t, database, spec.ID, daysAgo(20))

		if got := specAgeOf(t, svc, spec.ID); got.Stale {
			t.Errorf("20-day spec stale with threshold 30: %+v", got)
		}
		report := staleReportOf(t, svc)
		if report.Count != 0 || report.ThresholdDays != 30 {
			t.Errorf("report = %+v, want Count 0, ThresholdDays 30", report)
		}
	})

	t.Run("ThresholdZeroDisables", func(t *testing.T) {
		svc, database := newTestSDDServiceWithDB(t, "stale-threshold-0")
		svc.config.Spec.StaleAfterDays = 0
		spec := newStaleTestSpec(t, svc, "both old")
		setSpecUpdatedAt(t, database, spec.ID, daysAgo(20))
		insertSpecHistoryAt(t, database, spec.ID, daysAgo(20))

		report := staleReportOf(t, svc)
		if report.Count != 0 || report.ThresholdDays != 0 {
			t.Errorf("report = %+v, want Count 0, ThresholdDays 0", report)
		}
	})
}

// TestSpecList_StaleCountOverAllMatches pins that StaleCount counts over
// every match of the status filter, never the page, and that Ages and
// Frozen are cut to the page. Fixture: SPEC-001..003 plain, SPEC-004
// promoted, SPEC-005 promoted then archived (frozen, last); SPEC-001,
// SPEC-002 and SPEC-005 aged 20 days; SPEC-002 in qa, the rest draft.
func TestSpecList_StaleCountOverAllMatches(t *testing.T) {
	ctx := context.Background()
	svc, database := newTestSDDServiceWithDB(t, "stale-count")
	s1 := newStaleTestSpec(t, svc, "one")
	s2 := newStaleTestSpec(t, svc, "two")
	newStaleTestSpec(t, svc, "three")
	promoteStaleTestSpec(t, svc, "four")
	s5, item5 := promoteStaleTestSpec(t, svc, "five")
	archiveStaleTestItem(t, svc, item5)
	for _, id := range []string{s1.ID, s2.ID, s5.ID} {
		setSpecUpdatedAt(t, database, id, daysAgo(20))
	}
	setSpecStatus(t, database, s2.ID, model.SpecStatusQA)

	list := func(t *testing.T, req model.SpecListRequest) model.SpecListResponse {
		t.Helper()
		resp, err := svc.SpecList(ctx, req)
		if err != nil {
			t.Fatalf("SpecList(%+v): %v", req, err)
		}
		return resp
	}
	pageIDs := func(resp model.SpecListResponse) map[string]bool {
		ids := make(map[string]bool, len(resp.Specs))
		for _, s := range resp.Specs {
			ids[s.ID] = true
		}
		return ids
	}

	t.Run("WindowedNoFilter", func(t *testing.T) {
		resp := list(t, model.SpecListRequest{Limit: 4})
		if resp.StaleCount != 2 {
			t.Errorf("StaleCount = %d, want 2", resp.StaleCount)
		}
		if resp.Frozen != nil {
			t.Errorf("Frozen = %v, want nil (the frozen spec is off the page)", resp.Frozen)
		}
		ids := pageIDs(resp)
		if len(resp.Ages) != 4 {
			t.Errorf("len(Ages) = %d, want 4", len(resp.Ages))
		}
		for id := range resp.Ages {
			if !ids[id] {
				t.Errorf("Ages carries %s, which is not on the page", id)
			}
		}
	})

	t.Run("WindowOfOneStillCountsAll", func(t *testing.T) {
		resp := list(t, model.SpecListRequest{Limit: 1})
		if resp.StaleCount != 2 {
			t.Errorf("StaleCount = %d, want 2 (the page holds one spec, the project two stale)", resp.StaleCount)
		}
	})

	t.Run("CompleteNoFilter", func(t *testing.T) {
		resp := list(t, model.SpecListRequest{})
		if resp.StaleCount != 2 {
			t.Errorf("StaleCount = %d, want 2", resp.StaleCount)
		}
		if len(resp.Frozen) != 1 {
			t.Errorf("Frozen = %v, want exactly %s", resp.Frozen, s5.ID)
		}
		if _, ok := resp.Frozen[s5.ID]; !ok {
			t.Errorf("Frozen lacks %s: %v", s5.ID, resp.Frozen)
		}
		if len(resp.Ages) != 5 {
			t.Errorf("len(Ages) = %d, want 5", len(resp.Ages))
		}
		if resp.Stale.Count != 2 || resp.Stale.FrozenCount != 1 {
			t.Errorf("Stale = %+v, want Count 2, FrozenCount 1", resp.Stale)
		}
		staleAges := 0
		for _, age := range resp.Ages {
			if age.Stale {
				staleAges++
			}
		}
		if staleAges != resp.Stale.Count {
			t.Errorf("%d ages marked stale, Stale.Count = %d", staleAges, resp.Stale.Count)
		}
	})

	t.Run("DraftFilterWindowed", func(t *testing.T) {
		resp := list(t, model.SpecListRequest{Status: model.SpecStatusDraft, Limit: 1})
		if resp.StaleCount != 1 {
			t.Errorf("StaleCount = %d, want 1 (only %s among drafts)", resp.StaleCount, s1.ID)
		}
		if len(resp.Ages) != 1 {
			t.Errorf("len(Ages) = %d, want 1", len(resp.Ages))
		}
	})

	t.Run("QAFilter", func(t *testing.T) {
		resp := list(t, model.SpecListRequest{Status: model.SpecStatusQA})
		if resp.StaleCount != 1 {
			t.Errorf("StaleCount = %d, want 1 (%s)", resp.StaleCount, s2.ID)
		}
	})
}

// TestSpecAges_ListSpecsOnlyWhenPageIncomplete_Structural proves, from the
// syntax tree, that specAges reads the full spec list only when the page is
// incomplete: exactly one ListSpecs call in its body, and it sits inside an
// `if !pageIsComplete` block. No query counter exists in store or db, and
// none is added to production for this.
func TestSpecAges_ListSpecsOnlyWhenPageIncomplete_Structural(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	path := filepath.Join(filepath.Dir(thisFile), "sdd_stale.go")
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var fn *ast.FuncDecl
	for _, decl := range f.Decls {
		if d, ok := decl.(*ast.FuncDecl); ok && d.Name.Name == "specAges" {
			fn = d
		}
	}
	if fn == nil {
		t.Fatal("specAges not found in sdd_stale.go")
	}

	isListSpecs := func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return false
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		return ok && sel.Sel.Name == "ListSpecs"
	}
	isNotPageIsComplete := func(e ast.Expr) bool {
		u, ok := e.(*ast.UnaryExpr)
		if !ok || u.Op != token.NOT {
			return false
		}
		id, ok := u.X.(*ast.Ident)
		return ok && id.Name == "pageIsComplete"
	}

	total, guarded := 0, 0
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if isListSpecs(n) {
			total++
		}
		ifStmt, ok := n.(*ast.IfStmt)
		if !ok || !isNotPageIsComplete(ifStmt.Cond) {
			return true
		}
		ast.Inspect(ifStmt.Body, func(inner ast.Node) bool {
			if isListSpecs(inner) {
				guarded++
			}
			return true
		})
		return true
	})
	if total != 1 {
		t.Errorf("specAges has %d ListSpecs calls, want exactly one", total)
	}
	if guarded != total {
		t.Errorf("%d of %d ListSpecs calls in specAges sit outside an `if !pageIsComplete` block", total-guarded, total)
	}
}

// TestStaleSpecs_ReadOnly pins SPEC-186 C8 (D3): computing staleness never
// writes — the spec_history row count and every spec's status/updated_at
// are identical before and after StaleSpecs and SpecList.
func TestStaleSpecs_ReadOnly(t *testing.T) {
	ctx := context.Background()
	svc, database := newTestSDDServiceWithDB(t, "stale-read-only")
	spec := newStaleTestSpec(t, svc, "both old")
	setSpecUpdatedAt(t, database, spec.ID, daysAgo(20))
	insertSpecHistoryAt(t, database, spec.ID, daysAgo(20))
	frozenSpec, itemID := promoteStaleTestSpec(t, svc, "frozen")
	archiveStaleTestItem(t, svc, itemID)
	setSpecUpdatedAt(t, database, frozenSpec.ID, daysAgo(30))

	snapshot := func() (int, map[string]string) {
		var historyRows int
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM spec_history`).Scan(&historyRows); err != nil {
			t.Fatalf("count history: %v", err)
		}
		rows, err := database.QueryContext(ctx, `SELECT id, status, updated_at FROM specs`)
		if err != nil {
			t.Fatalf("read specs: %v", err)
		}
		defer rows.Close()
		state := make(map[string]string)
		for rows.Next() {
			var id, status, updatedAt string
			if err := rows.Scan(&id, &status, &updatedAt); err != nil {
				t.Fatalf("scan spec: %v", err)
			}
			state[id] = status + "|" + updatedAt
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("rows: %v", err)
		}
		return historyRows, state
	}

	beforeRows, beforeState := snapshot()
	if report := staleReportOf(t, svc); report.Count != 1 {
		t.Fatalf("fixture: Count = %d, want 1", report.Count)
	}
	if _, err := svc.SpecList(ctx, model.SpecListRequest{}); err != nil {
		t.Fatalf("SpecList: %v", err)
	}
	if _, err := svc.SpecList(ctx, model.SpecListRequest{Status: model.SpecStatusDraft, Limit: 1}); err != nil {
		t.Fatalf("SpecList filtered: %v", err)
	}
	afterRows, afterState := snapshot()

	if beforeRows != afterRows {
		t.Errorf("spec_history rows %d -> %d: staleness wrote history", beforeRows, afterRows)
	}
	if !reflect.DeepEqual(beforeState, afterState) {
		t.Errorf("specs changed:\nbefore %v\nafter  %v", beforeState, afterState)
	}
}
