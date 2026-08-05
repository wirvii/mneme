package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/model"
)

// newTestSDDStore opens a fresh in-memory SQLite database, applies all migrations,
// and returns an SDDStore backed by it. The database is closed when the test ends.
func newTestSDDStore(t *testing.T) *SDDStore {
	t.Helper()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })
	return NewSDDStore(database)
}

// --- BACKLOG TESTS ---

func TestCreateBacklogItem(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	item := &model.BacklogItem{
		ID:          "BL-001",
		Title:       "Test feature",
		Description: "Detailed description",
		Status:      model.BacklogStatusRaw,
		Priority:    model.PriorityHigh,
		Project:     "testproject",
		Position:    0,
		Lane:        model.LaneStandard,
	}

	if err := s.CreateBacklogItem(ctx, item); err != nil {
		t.Fatalf("CreateBacklogItem: %v", err)
	}

	got, err := s.GetBacklogItem(ctx, "BL-001")
	if err != nil {
		t.Fatalf("GetBacklogItem: %v", err)
	}

	if got.Title != item.Title {
		t.Errorf("Title: got %q, want %q", got.Title, item.Title)
	}
	if got.Status != model.BacklogStatusRaw {
		t.Errorf("Status: got %q, want raw", got.Status)
	}
	if got.Priority != model.PriorityHigh {
		t.Errorf("Priority: got %q, want high", got.Priority)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt must not be zero")
	}
}

func TestNextBacklogID(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	project := "testproject"

	// No items — must return BL-001.
	id, err := s.NextBacklogID(ctx, project)
	if err != nil {
		t.Fatalf("NextBacklogID (empty): %v", err)
	}
	if id != "BL-001" {
		t.Errorf("got %q, want BL-001", id)
	}

	// Create BL-001.
	if err := s.CreateBacklogItem(ctx, &model.BacklogItem{
		ID: "BL-001", Title: "first", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: project, Lane: model.LaneStandard,
	}); err != nil {
		t.Fatalf("create BL-001: %v", err)
	}

	// Should return BL-002.
	id, err = s.NextBacklogID(ctx, project)
	if err != nil {
		t.Fatalf("NextBacklogID (after BL-001): %v", err)
	}
	if id != "BL-002" {
		t.Errorf("got %q, want BL-002", id)
	}

	// Sequentiality: create BL-002 and verify next is BL-003.
	if err := s.CreateBacklogItem(ctx, &model.BacklogItem{
		ID: "BL-002", Title: "second", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: project, Lane: model.LaneStandard,
	}); err != nil {
		t.Fatalf("create BL-002: %v", err)
	}
	id, err = s.NextBacklogID(ctx, project)
	if err != nil {
		t.Fatalf("NextBacklogID (after BL-002): %v", err)
	}
	if id != "BL-003" {
		t.Errorf("got %q, want BL-003", id)
	}
}

func TestListBacklogItems(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	project := "listtest"

	items := []*model.BacklogItem{
		{ID: "BL-001", Title: "Critical item", Status: model.BacklogStatusRaw, Priority: model.PriorityCritical, Project: project, Lane: model.LaneStandard},
		{ID: "BL-002", Title: "Low item", Status: model.BacklogStatusRefined, Priority: model.PriorityLow, Project: project, Lane: model.LaneStandard},
		{ID: "BL-003", Title: "Medium item", Status: model.BacklogStatusRaw, Priority: model.PriorityMedium, Project: project, Lane: model.LaneStandard},
	}
	for _, item := range items {
		if err := s.CreateBacklogItem(ctx, item); err != nil {
			t.Fatalf("create %s: %v", item.ID, err)
		}
	}

	// Filter by status=raw: expect BL-001 and BL-003.
	rawItems, rawTotal, err := s.ListBacklogItems(ctx, project, model.BacklogStatusRaw, 0)
	if err != nil {
		t.Fatalf("ListBacklogItems(raw): %v", err)
	}
	if rawTotal != 2 {
		t.Errorf("expected total=2 for raw items, got %d", rawTotal)
	}
	if len(rawItems) != 2 {
		t.Errorf("expected 2 raw items, got %d", len(rawItems))
	}

	// No filter: expect all 3 items, ordered by PRIORITY RANK (critical,
	// medium, low) — NOT the lexicographic order of the priority column
	// ('critical' < 'high' < 'low' < 'medium', which would put the low item
	// before the medium one). SPEC-109 D20/AC27: this is the fix, verified
	// by asserting exact order instead of only membership.
	all, allTotal, err := s.ListBacklogItems(ctx, project, "", 0)
	if err != nil {
		t.Fatalf("ListBacklogItems(all): %v", err)
	}
	if allTotal != 3 {
		t.Errorf("expected total=3, got %d", allTotal)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 items, got %d", len(all))
	}
	wantOrder := []string{"BL-001", "BL-003", "BL-002"} // critical, medium, low
	for i, want := range wantOrder {
		if i >= len(all) {
			t.Fatalf("missing item at position %d, want %s", i, want)
		}
		if all[i].ID != want {
			t.Errorf("position %d: got %s, want %s (order: %v)", i, all[i].ID, want, idsOf(all))
		}
	}
}

// idsOf extracts the IDs of a backlog item slice, for readable test failure
// messages.
func idsOf(items []*model.BacklogItem) []string {
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	return ids
}

// TestListBacklogItems_PriorityRankOrder is AC27: with one item of each
// priority, ListBacklogItems must return critical, high, medium, low — NOT
// the lexicographic order of the priority TEXT column ('critical' < 'high'
// < 'low' < 'medium', SPEC-109 D20), which would incorrectly place low
// before medium.
func TestListBacklogItems_PriorityRankOrder(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	project := "rankordertest"

	// Inserted in an order that would NOT coincidentally match either the
	// correct or the buggy ordering, so the assertion is meaningful.
	items := []*model.BacklogItem{
		{ID: "BL-001", Title: "low", Status: model.BacklogStatusRaw, Priority: model.PriorityLow, Project: project, Lane: model.LaneStandard},
		{ID: "BL-002", Title: "medium", Status: model.BacklogStatusRaw, Priority: model.PriorityMedium, Project: project, Lane: model.LaneStandard},
		{ID: "BL-003", Title: "critical", Status: model.BacklogStatusRaw, Priority: model.PriorityCritical, Project: project, Lane: model.LaneStandard},
		{ID: "BL-004", Title: "high", Status: model.BacklogStatusRaw, Priority: model.PriorityHigh, Project: project, Lane: model.LaneStandard},
	}
	for _, item := range items {
		if err := s.CreateBacklogItem(ctx, item); err != nil {
			t.Fatalf("create %s: %v", item.ID, err)
		}
	}

	got, _, err := s.ListBacklogItems(ctx, project, "", 0)
	if err != nil {
		t.Fatalf("ListBacklogItems: %v", err)
	}

	want := []string{"BL-003", "BL-004", "BL-002", "BL-001"} // critical, high, medium, low
	if gotIDs := idsOf(got); !equalStrings(gotIDs, want) {
		t.Errorf("order = %v, want %v (critical, high, medium, low)", gotIDs, want)
	}
}

// TestListBacklogItems_DeterministicTieBreak is AC16: with ≥3 items tied on
// priority and position (BacklogAdd always sets Position=0 in production —
// see D7), two consecutive ListBacklogItems calls must return the exact same
// order. Without the created_at/id tie-break this would be a lottery.
func TestListBacklogItems_DeterministicTieBreak(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	project := "tiebreaktest"

	for _, id := range []string{"BL-001", "BL-002", "BL-003", "BL-004", "BL-005"} {
		item := &model.BacklogItem{
			ID: id, Title: "tied " + id, Status: model.BacklogStatusRaw,
			Priority: model.PriorityMedium, Project: project, Position: 0,
			Lane: model.LaneStandard,
		}
		if err := s.CreateBacklogItem(ctx, item); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	first, _, err := s.ListBacklogItems(ctx, project, "", 0)
	if err != nil {
		t.Fatalf("ListBacklogItems (first): %v", err)
	}
	second, _, err := s.ListBacklogItems(ctx, project, "", 0)
	if err != nil {
		t.Fatalf("ListBacklogItems (second): %v", err)
	}

	if !equalStrings(idsOf(first), idsOf(second)) {
		t.Errorf("non-deterministic order: first=%v second=%v", idsOf(first), idsOf(second))
	}
	// The tie-break is rowid — SQLite's own monotonic insertion counter,
	// which always matches insertion order here regardless of how close
	// together (in wall-clock time) the five CreateBacklogItem calls land.
	want := []string{"BL-001", "BL-002", "BL-003", "BL-004", "BL-005"}
	if gotIDs := idsOf(first); !equalStrings(gotIDs, want) {
		t.Errorf("order = %v, want %v", gotIDs, want)
	}
}

// TestListBacklogItems_OrderSurvivesInsertBursts is the repeated/burst test
// QA required after rejecting the first cut of D7/AC27: a single insert
// followed by a single comparison would never have caught the original bug
// (created_at ASC, id ASC assumed time.RFC3339Nano is lexicographically
// chronological, which it is not — Format trims trailing zeros from the
// fractional-second component, so two timestamps microseconds apart can
// compare in the WRONG direction as plain text). QA reproduced the
// resulting misordering in 23 of 300 iterations of 5-item insert bursts;
// this test runs the same shape of burst hundreds of times so a
// reintroduction of a created_at/timestamp-text tie-break would be caught
// with very high probability, not just "usually".
//
// Each iteration: create 5 items back-to-back (no delay — maximises the
// chance two land within the same or adjacent nanoseconds, the exact
// condition that triggered the trailing-zero-trim bug), then assert BOTH
// that two consecutive listings agree with each other AND that the order
// exactly matches insertion order. rowid guarantees this unconditionally;
// the old created_at-based tie-break could not.
func TestListBacklogItems_OrderSurvivesInsertBursts(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	const iterations = 300
	const burstSize = 5

	for iter := 0; iter < iterations; iter++ {
		// backlog_items.id is a GLOBAL primary key (no project scoping,
		// unlike specs' composite (project, id) — migration 005), so IDs
		// must be unique across iterations, not just within one.
		project := fmt.Sprintf("burst-order-test-%d", iter)
		wantOrder := make([]string, 0, burstSize)
		for i := 0; i < burstSize; i++ {
			id := fmt.Sprintf("BL-%d-%03d", iter, i)
			item := &model.BacklogItem{
				ID: id, Title: "burst item", Status: model.BacklogStatusRaw,
				Priority: model.PriorityMedium, Project: project, Position: 0,
				Lane: model.LaneStandard,
			}
			if err := s.CreateBacklogItem(ctx, item); err != nil {
				t.Fatalf("iteration %d: create %s: %v", iter, id, err)
			}
			wantOrder = append(wantOrder, id)
		}

		first, _, err := s.ListBacklogItems(ctx, project, "", 0)
		if err != nil {
			t.Fatalf("iteration %d: ListBacklogItems (first): %v", iter, err)
		}
		second, _, err := s.ListBacklogItems(ctx, project, "", 0)
		if err != nil {
			t.Fatalf("iteration %d: ListBacklogItems (second): %v", iter, err)
		}

		gotFirst := idsOf(first)
		if !equalStrings(gotFirst, idsOf(second)) {
			t.Fatalf("iteration %d: non-deterministic order between two calls: first=%v second=%v",
				iter, gotFirst, idsOf(second))
		}
		if !equalStrings(gotFirst, wantOrder) {
			t.Fatalf("iteration %d: order = %v, want insertion order %v", iter, gotFirst, wantOrder)
		}
	}
}

// equalStrings reports whether two string slices have the same elements in
// the same order.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestBacklogListPredicate_SharedByCountAndPage is AC9's textual guard
// (precedent SPEC-108 AC8): the COUNT and page queries for backlog listing
// must embed the exact same predicate. Proven by construction here —
// backlogListWhereStatus EXTENDS backlogListWhere rather than being an
// independently written string, and neither SELECT constant carries its own
// WHERE, so the shared where variable built in ListBacklogItems is the only
// source of filtering for both queries. If a future edit gave either query
// its own filter, this test would catch the divergence.
func TestBacklogListPredicate_SharedByCountAndPage(t *testing.T) {
	if !strings.HasPrefix(backlogListWhereStatus, backlogListWhere) {
		t.Error("backlogListWhereStatus does not extend backlogListWhere — COUNT and page could diverge")
	}
	if strings.Contains(strings.ToUpper(backlogCountSelect), "WHERE") {
		t.Error("backlogCountSelect must not embed its own WHERE clause")
	}
	if strings.Contains(strings.ToUpper(backlogListSelect), "WHERE") {
		t.Error("backlogListSelect must not embed its own WHERE clause")
	}
}

// TestSpecListPredicate_SharedByCountAndPage mirrors the guard above for specs.
func TestSpecListPredicate_SharedByCountAndPage(t *testing.T) {
	if !strings.HasPrefix(specListWhereStatus, specListWhere) {
		t.Error("specListWhereStatus does not extend specListWhere — COUNT and page could diverge")
	}
	if strings.Contains(strings.ToUpper(specCountSelect), "WHERE") {
		t.Error("specCountSelect must not embed its own WHERE clause")
	}
	if strings.Contains(strings.ToUpper(specListSelect), "WHERE") {
		t.Error("specListSelect must not embed its own WHERE clause")
	}
}

// TestListSpecs_TotalBehavioralGuard mirrors
// TestListBacklogItems_TotalBehavioralGuard (AC8) for specs.
func TestListSpecs_TotalBehavioralGuard(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	project := "specstotalguardtest"

	statuses := []model.SpecStatus{
		model.SpecStatusDraft, model.SpecStatusImplementing, model.SpecStatusDone,
	}
	n := 0
	for _, status := range statuses {
		for i := 0; i < 3; i++ {
			n++
			spec := &model.Spec{
				ID: fmt.Sprintf("SPEC-%03d", n), Title: fmt.Sprintf("spec %d", n),
				Status: status, Project: project, Lane: model.LaneStandard,
			}
			if err := s.CreateSpec(ctx, spec); err != nil {
				t.Fatalf("create %s: %v", spec.ID, err)
			}
		}
	}

	cases := append([]model.SpecStatus{""}, statuses...)
	for _, status := range cases {
		name := string(status)
		if name == "" {
			name = "(no filter)"
		}
		t.Run(name, func(t *testing.T) {
			unwindowed, _, err := s.ListSpecs(ctx, project, status, 0)
			if err != nil {
				t.Fatalf("ListSpecs(limit=0): %v", err)
			}
			_, total, err := s.ListSpecs(ctx, project, status, 1)
			if err != nil {
				t.Fatalf("ListSpecs(limit=1): %v", err)
			}
			if total != len(unwindowed) {
				t.Errorf("total=%d, want %d (len of unwindowed list)", total, len(unwindowed))
			}
		})
	}
}

// TestListSpecs_LimitPagesAndReportsTrueTotal mirrors
// TestListBacklogItems_LimitPagesAndReportsTrueTotal (AC6) for specs.
func TestListSpecs_LimitPagesAndReportsTrueTotal(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	project := "specslimitpagetest"

	for i := 1; i <= 25; i++ {
		spec := &model.Spec{
			ID: fmt.Sprintf("SPEC-%03d", i), Title: fmt.Sprintf("spec %d", i),
			Status: model.SpecStatusDraft, Project: project, Lane: model.LaneStandard,
		}
		if err := s.CreateSpec(ctx, spec); err != nil {
			t.Fatalf("create %s: %v", spec.ID, err)
		}
	}

	specs, total, err := s.ListSpecs(ctx, project, "", 10)
	if err != nil {
		t.Fatalf("ListSpecs: %v", err)
	}
	if len(specs) != 10 {
		t.Errorf("expected 10 specs with limit=10, got %d", len(specs))
	}
	if total != 25 {
		t.Errorf("total=%d, want 25 (the real match count, not the limit)", total)
	}
}

// TestListSpecs_OrderSurvivesInsertBursts mirrors
// TestListBacklogItems_OrderSurvivesInsertBursts for specListOrder, which had
// the exact same created_at-lexicographic-vs-chronological defect (QA
// rejection on the first cut of D7/AC27).
func TestListSpecs_OrderSurvivesInsertBursts(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	const iterations = 300
	const burstSize = 5

	for iter := 0; iter < iterations; iter++ {
		project := fmt.Sprintf("burst-spec-order-test-%d", iter)
		wantOrder := make([]string, 0, burstSize)
		for i := 0; i < burstSize; i++ {
			id := fmt.Sprintf("SPEC-%03d", i)
			spec := &model.Spec{
				ID: id, Title: "burst spec", Status: model.SpecStatusDraft,
				Project: project, Lane: model.LaneStandard,
			}
			if err := s.CreateSpec(ctx, spec); err != nil {
				t.Fatalf("iteration %d: create %s: %v", iter, id, err)
			}
			wantOrder = append(wantOrder, id)
		}

		first, _, err := s.ListSpecs(ctx, project, "", 0)
		if err != nil {
			t.Fatalf("iteration %d: ListSpecs (first): %v", iter, err)
		}
		second, _, err := s.ListSpecs(ctx, project, "", 0)
		if err != nil {
			t.Fatalf("iteration %d: ListSpecs (second): %v", iter, err)
		}

		gotFirst := make([]string, len(first))
		for i, spec := range first {
			gotFirst[i] = spec.ID
		}
		gotSecond := make([]string, len(second))
		for i, spec := range second {
			gotSecond[i] = spec.ID
		}

		if !equalStrings(gotFirst, gotSecond) {
			t.Fatalf("iteration %d: non-deterministic order between two calls: first=%v second=%v",
				iter, gotFirst, gotSecond)
		}
		if !equalStrings(gotFirst, wantOrder) {
			t.Fatalf("iteration %d: order = %v, want insertion order %v", iter, gotFirst, wantOrder)
		}
	}
}

// TestListBacklogItems_TotalBehavioralGuard is AC8: for every combination of
// (status filter × no filter), the total returned by a limit=1 call must
// equal len(items) from a limit=0 call. This is the test that fails if the
// COUNT and page queries ever diverge in their WHERE clause.
func TestListBacklogItems_TotalBehavioralGuard(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	project := "totalguardtest"

	statuses := []model.BacklogStatus{
		model.BacklogStatusRaw, model.BacklogStatusRefined,
		model.BacklogStatusPromoted, model.BacklogStatusArchived,
	}
	n := 0
	for _, status := range statuses {
		for i := 0; i < 3; i++ {
			n++
			item := &model.BacklogItem{
				ID: fmt.Sprintf("BL-%03d", n), Title: fmt.Sprintf("item %d", n),
				Status: status, Priority: model.PriorityMedium,
				Project: project, Lane: model.LaneStandard,
			}
			if err := s.CreateBacklogItem(ctx, item); err != nil {
				t.Fatalf("create %s: %v", item.ID, err)
			}
		}
	}

	cases := append([]model.BacklogStatus{""}, statuses...)
	for _, status := range cases {
		name := string(status)
		if name == "" {
			name = "(no filter)"
		}
		t.Run(name, func(t *testing.T) {
			unwindowed, _, err := s.ListBacklogItems(ctx, project, status, 0)
			if err != nil {
				t.Fatalf("ListBacklogItems(limit=0): %v", err)
			}
			_, total, err := s.ListBacklogItems(ctx, project, status, 1)
			if err != nil {
				t.Fatalf("ListBacklogItems(limit=1): %v", err)
			}
			if total != len(unwindowed) {
				t.Errorf("total=%d, want %d (len of unwindowed list)", total, len(unwindowed))
			}
		})
	}
}

// TestListBacklogItems_LimitZeroIsUnwindowed is AC12: limit<=0 returns every
// matching item and total equals the returned count.
func TestListBacklogItems_LimitZeroIsUnwindowed(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	project := "limitzerotest"

	for i := 1; i <= 25; i++ {
		item := &model.BacklogItem{
			ID: fmt.Sprintf("BL-%03d", i), Title: fmt.Sprintf("item %d", i),
			Status: model.BacklogStatusRaw, Priority: model.PriorityMedium,
			Project: project, Lane: model.LaneStandard,
		}
		if err := s.CreateBacklogItem(ctx, item); err != nil {
			t.Fatalf("create %s: %v", item.ID, err)
		}
	}

	items, total, err := s.ListBacklogItems(ctx, project, "", 0)
	if err != nil {
		t.Fatalf("ListBacklogItems: %v", err)
	}
	if len(items) != 25 {
		t.Errorf("expected 25 items with limit=0, got %d", len(items))
	}
	if total != len(items) {
		t.Errorf("total=%d, want %d (== len(items))", total, len(items))
	}
}

// TestListBacklogItems_LimitPagesAndReportsTrueTotal is AC6 at store level:
// 25 items, limit=10 returns exactly 10 items but total is the real count of
// 25 — the case that was impossible before SPEC-109 (total could never
// exceed limit).
func TestListBacklogItems_LimitPagesAndReportsTrueTotal(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	project := "limitpagetest"

	for i := 1; i <= 25; i++ {
		item := &model.BacklogItem{
			ID: fmt.Sprintf("BL-%03d", i), Title: fmt.Sprintf("item %d", i),
			Status: model.BacklogStatusRaw, Priority: model.PriorityMedium,
			Project: project, Lane: model.LaneStandard,
		}
		if err := s.CreateBacklogItem(ctx, item); err != nil {
			t.Fatalf("create %s: %v", item.ID, err)
		}
	}

	items, total, err := s.ListBacklogItems(ctx, project, "", 10)
	if err != nil {
		t.Fatalf("ListBacklogItems: %v", err)
	}
	if len(items) != 10 {
		t.Errorf("expected 10 items with limit=10, got %d", len(items))
	}
	if total != 25 {
		t.Errorf("total=%d, want 25 (the real match count, not the limit)", total)
	}
}

func TestUpdateBacklogItem(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	project := "updatetest"

	item := &model.BacklogItem{
		ID: "BL-001", Title: "Original", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: project, Lane: model.LaneStandard,
	}
	if err := s.CreateBacklogItem(ctx, item); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Refine the item.
	item.Status = model.BacklogStatusRefined
	item.Description = "Refined description"
	item.Priority = model.PriorityHigh
	if err := s.UpdateBacklogItem(ctx, item); err != nil {
		t.Fatalf("UpdateBacklogItem: %v", err)
	}

	got, err := s.GetBacklogItem(ctx, "BL-001")
	if err != nil {
		t.Fatalf("GetBacklogItem after update: %v", err)
	}
	if got.Status != model.BacklogStatusRefined {
		t.Errorf("Status: got %q, want refined", got.Status)
	}
	if got.Priority != model.PriorityHigh {
		t.Errorf("Priority: got %q, want high", got.Priority)
	}
}

// --- SPEC TESTS ---

func TestCreateSpec(t *testing.T) {
	tests := []struct {
		name      string
		backlogID string
	}{
		{name: "without backlog_id", backlogID: ""},
		{name: "with backlog_id", backlogID: "BL-001"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestSDDStore(t)
			ctx := context.Background()

			spec := &model.Spec{
				ID:        "SPEC-001",
				Title:     "Test spec",
				Status:    model.SpecStatusDraft,
				Project:   "testproject",
				BacklogID: tc.backlogID,
				Lane:      model.LaneStandard,
			}
			if err := s.CreateSpec(ctx, spec); err != nil {
				t.Fatalf("CreateSpec: %v", err)
			}

			got, err := s.GetSpec(ctx, "SPEC-001")
			if err != nil {
				t.Fatalf("GetSpec: %v", err)
			}
			if got.Title != spec.Title {
				t.Errorf("Title: got %q, want %q", got.Title, spec.Title)
			}
			if got.Status != model.SpecStatusDraft {
				t.Errorf("Status: got %q, want draft", got.Status)
			}
			if got.BacklogID != tc.backlogID {
				t.Errorf("BacklogID: got %q, want %q", got.BacklogID, tc.backlogID)
			}
		})
	}
}

func TestNextSpecID(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	project := "specproject"

	// No specs — must return SPEC-001.
	id, err := s.NextSpecID(ctx, project)
	if err != nil {
		t.Fatalf("NextSpecID (empty): %v", err)
	}
	if id != "SPEC-001" {
		t.Errorf("got %q, want SPEC-001", id)
	}

	if err := s.CreateSpec(ctx, &model.Spec{
		ID: "SPEC-001", Title: "first", Status: model.SpecStatusDraft, Project: project, Lane: model.LaneStandard,
	}); err != nil {
		t.Fatalf("create SPEC-001: %v", err)
	}

	id, err = s.NextSpecID(ctx, project)
	if err != nil {
		t.Fatalf("NextSpecID (after SPEC-001): %v", err)
	}
	if id != "SPEC-002" {
		t.Errorf("got %q, want SPEC-002", id)
	}
}

func TestUpdateSpecStatus(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	if err := s.CreateSpec(ctx, &model.Spec{
		ID: "SPEC-001", Title: "status test", Status: model.SpecStatusDraft, Project: "proj", Lane: model.LaneStandard,
	}); err != nil {
		t.Fatalf("create spec: %v", err)
	}

	// Valid transition: draft -> speccing.
	if err := s.UpdateSpecStatus(ctx, "SPEC-001", model.SpecStatusDraft, model.SpecStatusSpeccing, "orchestrator", "starting"); err != nil {
		t.Fatalf("UpdateSpecStatus (draft->speccing): %v", err)
	}

	got, err := s.GetSpec(ctx, "SPEC-001")
	if err != nil {
		t.Fatalf("GetSpec after update: %v", err)
	}
	if got.Status != model.SpecStatusSpeccing {
		t.Errorf("Status: got %q, want speccing", got.Status)
	}

	// Wrong 'from' status: current is speccing, we claim draft.
	err = s.UpdateSpecStatus(ctx, "SPEC-001", model.SpecStatusDraft, model.SpecStatusSpecced, "architect", "")
	if !errors.Is(err, model.ErrInvalidTransition) {
		t.Errorf("expected ErrInvalidTransition, got %v", err)
	}

	// Spec not found.
	err = s.UpdateSpecStatus(ctx, "SPEC-999", model.SpecStatusDraft, model.SpecStatusSpeccing, "x", "")
	if !errors.Is(err, model.ErrSpecNotFound) {
		t.Errorf("expected ErrSpecNotFound, got %v", err)
	}
}

func TestGetSpecHistory(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	if err := s.CreateSpec(ctx, &model.Spec{
		ID: "SPEC-001", Title: "history test", Status: model.SpecStatusDraft, Project: "proj", Lane: model.LaneStandard,
	}); err != nil {
		t.Fatalf("create spec: %v", err)
	}

	// Apply three transitions and verify chronological order.
	transitions := []struct{ from, to model.SpecStatus }{
		{model.SpecStatusDraft, model.SpecStatusSpeccing},
		{model.SpecStatusSpeccing, model.SpecStatusNeedsGrill},
		{model.SpecStatusNeedsGrill, model.SpecStatusSpeccing},
	}
	for _, tr := range transitions {
		// Small sleep to ensure distinct timestamps.
		time.Sleep(2 * time.Millisecond)
		if err := s.UpdateSpecStatus(ctx, "SPEC-001", tr.from, tr.to, "test", ""); err != nil {
			t.Fatalf("transition %s->%s: %v", tr.from, tr.to, err)
		}
	}

	history, err := s.GetSpecHistory(ctx, "SPEC-001")
	if err != nil {
		t.Fatalf("GetSpecHistory: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("expected 3 history entries, got %d", len(history))
	}

	// Verify ascending order.
	for i := 1; i < len(history); i++ {
		if history[i].At.Before(history[i-1].At) {
			t.Errorf("history entry %d has timestamp before entry %d", i, i-1)
		}
	}

	// Verify correct transition recording.
	if history[0].FromStatus != model.SpecStatusDraft || history[0].ToStatus != model.SpecStatusSpeccing {
		t.Errorf("first history entry: got %s->%s, want draft->speccing",
			history[0].FromStatus, history[0].ToStatus)
	}
}

// --- PUSHBACK TESTS ---

func TestCreatePushback(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	if err := s.CreateSpec(ctx, &model.Spec{
		ID: "SPEC-001", Title: "pb test", Status: model.SpecStatusSpeccing, Project: "proj", Lane: model.LaneStandard,
	}); err != nil {
		t.Fatalf("create spec: %v", err)
	}

	pb := &model.SpecPushback{
		SpecID:    "SPEC-001",
		FromAgent: "architect",
		Questions: []string{"What is the auth model?", "Dependency on user service?"},
	}
	if err := s.CreatePushback(ctx, pb); err != nil {
		t.Fatalf("CreatePushback: %v", err)
	}

	if pb.ID == "" {
		t.Error("expected ID to be set after creation")
	}

	pushbacks, err := s.GetUnresolvedPushbacks(ctx, "SPEC-001")
	if err != nil {
		t.Fatalf("GetUnresolvedPushbacks: %v", err)
	}
	if len(pushbacks) != 1 {
		t.Fatalf("expected 1 unresolved pushback, got %d", len(pushbacks))
	}
	if len(pushbacks[0].Questions) != 2 {
		t.Errorf("expected 2 questions, got %d", len(pushbacks[0].Questions))
	}
	if pushbacks[0].Questions[0] != "What is the auth model?" {
		t.Errorf("unexpected question: %q", pushbacks[0].Questions[0])
	}
}

func TestResolvePushback(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	if err := s.CreateSpec(ctx, &model.Spec{
		ID: "SPEC-001", Title: "resolve test", Status: model.SpecStatusNeedsGrill, Project: "proj", Lane: model.LaneStandard,
	}); err != nil {
		t.Fatalf("create spec: %v", err)
	}

	pb := &model.SpecPushback{
		SpecID:    "SPEC-001",
		FromAgent: "backend",
		Questions: []string{"Can we use Redis?"},
	}
	if err := s.CreatePushback(ctx, pb); err != nil {
		t.Fatalf("CreatePushback: %v", err)
	}

	if err := s.ResolvePushback(ctx, pb.ID, "Yes, Redis is approved"); err != nil {
		t.Fatalf("ResolvePushback: %v", err)
	}

	// Unresolved list should now be empty.
	unresolved, err := s.GetUnresolvedPushbacks(ctx, "SPEC-001")
	if err != nil {
		t.Fatalf("GetUnresolvedPushbacks after resolve: %v", err)
	}
	if len(unresolved) != 0 {
		t.Errorf("expected 0 unresolved, got %d", len(unresolved))
	}

	// All pushbacks should still contain the resolved one.
	all, err := s.GetAllPushbacks(ctx, "SPEC-001")
	if err != nil {
		t.Fatalf("GetAllPushbacks: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 total pushback, got %d", len(all))
	}
	if !all[0].Resolved {
		t.Error("expected pushback to be marked resolved")
	}
	if all[0].Resolution != "Yes, Redis is approved" {
		t.Errorf("unexpected resolution: %q", all[0].Resolution)
	}
	if all[0].ResolvedAt == nil {
		t.Error("expected ResolvedAt to be set")
	}
}

func TestGetUnresolvedPushbacks(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	if err := s.CreateSpec(ctx, &model.Spec{
		ID: "SPEC-001", Title: "multi pb", Status: model.SpecStatusNeedsGrill, Project: "proj", Lane: model.LaneStandard,
	}); err != nil {
		t.Fatalf("create spec: %v", err)
	}

	// Create two pushbacks.
	pb1 := &model.SpecPushback{SpecID: "SPEC-001", FromAgent: "backend", Questions: []string{"Q1"}}
	pb2 := &model.SpecPushback{SpecID: "SPEC-001", FromAgent: "qa", Questions: []string{"Q2"}}
	if err := s.CreatePushback(ctx, pb1); err != nil {
		t.Fatalf("create pb1: %v", err)
	}
	if err := s.CreatePushback(ctx, pb2); err != nil {
		t.Fatalf("create pb2: %v", err)
	}

	// Resolve first one.
	if err := s.ResolvePushback(ctx, pb1.ID, "resolved"); err != nil {
		t.Fatalf("resolve pb1: %v", err)
	}

	// Only pb2 should remain unresolved.
	unresolved, err := s.GetUnresolvedPushbacks(ctx, "SPEC-001")
	if err != nil {
		t.Fatalf("GetUnresolvedPushbacks: %v", err)
	}
	if len(unresolved) != 1 {
		t.Errorf("expected 1 unresolved, got %d", len(unresolved))
	}
	if unresolved[0].ID != pb2.ID {
		t.Errorf("expected pb2 unresolved, got %s", unresolved[0].ID)
	}
}

// --- AGGREGATE TESTS ---

func TestBacklogCounts(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	project := "counttest"

	items := []*model.BacklogItem{
		{ID: "BL-001", Status: model.BacklogStatusRaw, Priority: model.PriorityMedium, Project: project, Title: "a", Lane: model.LaneStandard},
		{ID: "BL-002", Status: model.BacklogStatusRaw, Priority: model.PriorityMedium, Project: project, Title: "b", Lane: model.LaneStandard},
		{ID: "BL-003", Status: model.BacklogStatusRefined, Priority: model.PriorityMedium, Project: project, Title: "c", Lane: model.LaneStandard},
		{ID: "BL-004", Status: model.BacklogStatusArchived, Priority: model.PriorityMedium, Project: project, Title: "d", Lane: model.LaneStandard},
	}
	for _, item := range items {
		if err := s.CreateBacklogItem(ctx, item); err != nil {
			t.Fatalf("create %s: %v", item.ID, err)
		}
	}

	counts, err := s.BacklogCounts(ctx, project)
	if err != nil {
		t.Fatalf("BacklogCounts: %v", err)
	}
	if counts[model.BacklogStatusRaw] != 2 {
		t.Errorf("raw: got %d, want 2", counts[model.BacklogStatusRaw])
	}
	if counts[model.BacklogStatusRefined] != 1 {
		t.Errorf("refined: got %d, want 1", counts[model.BacklogStatusRefined])
	}
	if counts[model.BacklogStatusArchived] != 1 {
		t.Errorf("archived: got %d, want 1", counts[model.BacklogStatusArchived])
	}
}

func TestSpecCounts(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	project := "speccounttest"

	specs := []*model.Spec{
		{ID: "SPEC-001", Status: model.SpecStatusDraft, Project: project, Title: "a", Lane: model.LaneStandard},
		{ID: "SPEC-002", Status: model.SpecStatusDraft, Project: project, Title: "b", Lane: model.LaneStandard},
		{ID: "SPEC-003", Status: model.SpecStatusImplementing, Project: project, Title: "c", Lane: model.LaneStandard},
		{ID: "SPEC-004", Status: model.SpecStatusDone, Project: project, Title: "d", Lane: model.LaneStandard},
	}
	for _, spec := range specs {
		if err := s.CreateSpec(ctx, spec); err != nil {
			t.Fatalf("create %s: %v", spec.ID, err)
		}
	}

	counts, err := s.SpecCounts(ctx, project)
	if err != nil {
		t.Fatalf("SpecCounts: %v", err)
	}
	if counts[model.SpecStatusDraft] != 2 {
		t.Errorf("draft: got %d, want 2", counts[model.SpecStatusDraft])
	}
	if counts[model.SpecStatusImplementing] != 1 {
		t.Errorf("implementing: got %d, want 1", counts[model.SpecStatusImplementing])
	}
	if counts[model.SpecStatusDone] != 1 {
		t.Errorf("done: got %d, want 1", counts[model.SpecStatusDone])
	}
}

func TestRecentlyCompletedSpecs(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	project := "donetest"

	// Create 3 done specs and 1 non-done.
	for i, id := range []string{"SPEC-001", "SPEC-002", "SPEC-003", "SPEC-004"} {
		status := model.SpecStatusDone
		if i == 3 {
			status = model.SpecStatusImplementing
		}
		if err := s.CreateSpec(ctx, &model.Spec{
			ID: id, Status: status, Project: project, Title: "spec " + id, Lane: model.LaneStandard,
		}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
		time.Sleep(2 * time.Millisecond) // ensure distinct updated_at
	}

	// Limit to 2.
	recent, err := s.RecentlyCompletedSpecs(ctx, project, 2)
	if err != nil {
		t.Fatalf("RecentlyCompletedSpecs: %v", err)
	}
	if len(recent) != 2 {
		t.Errorf("expected 2 results, got %d", len(recent))
	}
	// All should be done.
	for _, spec := range recent {
		if spec.Status != model.SpecStatusDone {
			t.Errorf("spec %s has status %q, want done", spec.ID, spec.Status)
		}
	}
}

// TestUpdateSpecLaneScope verifies that lane and scope can be updated independently
// of status transitions.
func TestUpdateSpecLaneScope(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	spec := &model.Spec{
		ID: "SPEC-001", Title: "reclassify test", Status: model.SpecStatusDraft,
		Project: "proj", Lane: model.LaneTrivial, Scope: "internal/foo/**",
	}
	if err := s.CreateSpec(ctx, spec); err != nil {
		t.Fatalf("CreateSpec: %v", err)
	}

	// Reclassify from trivial to standard.
	if err := s.UpdateSpecLaneScope(ctx, "SPEC-001", model.LaneStandard, ""); err != nil {
		t.Fatalf("UpdateSpecLaneScope: %v", err)
	}

	got, err := s.GetSpec(ctx, "SPEC-001")
	if err != nil {
		t.Fatalf("GetSpec after lane update: %v", err)
	}
	if got.Lane != model.LaneStandard {
		t.Errorf("Lane: got %q, want standard", got.Lane)
	}
	if got.Scope != "" {
		t.Errorf("Scope: got %q, want empty", got.Scope)
	}

	// Status must be unchanged.
	if got.Status != model.SpecStatusDraft {
		t.Errorf("Status changed unexpectedly: %q", got.Status)
	}

	// Non-existent spec returns ErrSpecNotFound.
	err = s.UpdateSpecLaneScope(ctx, "SPEC-999", model.LaneStandard, "")
	if !errors.Is(err, model.ErrSpecNotFound) {
		t.Errorf("expected ErrSpecNotFound, got %v", err)
	}
}

// TestCreateBacklogItemTrivialLane verifies that lane and scope are stored and
// retrieved correctly for trivial-lane backlog items.
func TestCreateBacklogItemTrivialLane(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	item := &model.BacklogItem{
		ID:       "BL-001",
		Title:    "Tiny fix",
		Status:   model.BacklogStatusRaw,
		Priority: model.PriorityMedium,
		Project:  "proj",
		Lane:     model.LaneTrivial,
		Scope:    "internal/store/*.go",
	}
	if err := s.CreateBacklogItem(ctx, item); err != nil {
		t.Fatalf("CreateBacklogItem: %v", err)
	}

	got, err := s.GetBacklogItem(ctx, "BL-001")
	if err != nil {
		t.Fatalf("GetBacklogItem: %v", err)
	}
	if got.Lane != model.LaneTrivial {
		t.Errorf("Lane: got %q, want trivial", got.Lane)
	}
	if got.Scope != "internal/store/*.go" {
		t.Errorf("Scope: got %q, want internal/store/*.go", got.Scope)
	}
}

// TestCreateSpecWithLane verifies that lane and scope round-trip correctly for specs.
func TestCreateSpecWithLane(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	spec := &model.Spec{
		ID:      "SPEC-001",
		Title:   "Trivial fix",
		Status:  model.SpecStatusDraft,
		Project: "proj",
		Lane:    model.LaneTrivial,
		Scope:   "internal/model/*.go",
	}
	if err := s.CreateSpec(ctx, spec); err != nil {
		t.Fatalf("CreateSpec: %v", err)
	}

	got, err := s.GetSpec(ctx, "SPEC-001")
	if err != nil {
		t.Fatalf("GetSpec: %v", err)
	}
	if got.Lane != model.LaneTrivial {
		t.Errorf("Lane: got %q, want trivial", got.Lane)
	}
	if got.Scope != "internal/model/*.go" {
		t.Errorf("Scope: got %q, want internal/model/*.go", got.Scope)
	}
}

// TestUpdateSpecBaseSHA verifies that UpdateSpecBaseSHA sets the base_sha and that
// GetSpec and ListSpecs both return the updated value. Also verifies ErrSpecNotFound
// for a non-existent spec ID.
func TestUpdateSpecBaseSHA(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	spec := &model.Spec{
		ID: "SPEC-001", Title: "SHA test", Status: model.SpecStatusDraft,
		Project: "proj", Lane: model.LaneStandard,
	}
	if err := s.CreateSpec(ctx, spec); err != nil {
		t.Fatalf("CreateSpec: %v", err)
	}

	const wantSHA = "abc0123456789def0123456789abcdef01234567"
	if err := s.UpdateSpecBaseSHA(ctx, "SPEC-001", wantSHA); err != nil {
		t.Fatalf("UpdateSpecBaseSHA: %v", err)
	}

	got, err := s.GetSpec(ctx, "SPEC-001")
	if err != nil {
		t.Fatalf("GetSpec after SHA update: %v", err)
	}
	if got.BaseSHA != wantSHA {
		t.Errorf("BaseSHA: got %q, want %q", got.BaseSHA, wantSHA)
	}

	// ListSpecs must also return the SHA.
	list, _, err := s.ListSpecs(ctx, "proj", "", 0)
	if err != nil {
		t.Fatalf("ListSpecs: %v", err)
	}
	if len(list) == 0 || list[0].BaseSHA != wantSHA {
		t.Errorf("ListSpecs BaseSHA: got %q, want %q", func() string {
			if len(list) > 0 {
				return list[0].BaseSHA
			}
			return "(empty list)"
		}(), wantSHA)
	}

	// Non-existent spec returns ErrSpecNotFound.
	err = s.UpdateSpecBaseSHA(ctx, "SPEC-999", wantSHA)
	if !errors.Is(err, model.ErrSpecNotFound) {
		t.Errorf("expected ErrSpecNotFound, got %v", err)
	}
}

// TestInsertLaneAuditAndLatestLaneAudit verifies the round-trip for lane audit records.
// Two inserts for the same spec: LatestLaneAudit must return the newer one.
// Also verifies that LatestLaneAudit returns nil when no rows exist.
func TestInsertLaneAuditAndLatestLaneAudit(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	spec := &model.Spec{
		ID: "SPEC-001", Title: "audit test", Status: model.SpecStatusAudit,
		Project: "proj", Lane: model.LaneTrivial, Scope: "internal/**",
	}
	if err := s.CreateSpec(ctx, spec); err != nil {
		t.Fatalf("CreateSpec: %v", err)
	}

	// No rows yet — LatestLaneAudit must return nil, nil.
	rec, err := s.LatestLaneAudit(ctx, "SPEC-001")
	if err != nil {
		t.Fatalf("LatestLaneAudit (no rows): %v", err)
	}
	if rec != nil {
		t.Errorf("expected nil record before any audits, got %+v", rec)
	}

	// Insert first (failing) audit.
	first := &model.LaneAuditRecord{
		SpecID:       "SPEC-001",
		Passed:       false,
		FileCount:    5,
		LinesChanged: 42,
		Breaches:     "file count 5 exceeds limit\nline count 42 exceeds limit",
		BaseSHA:      "sha1sha1sha1",
	}
	if err := s.InsertLaneAudit(ctx, first); err != nil {
		t.Fatalf("InsertLaneAudit (fail): %v", err)
	}
	if first.ID == 0 {
		t.Error("InsertLaneAudit did not populate ID")
	}

	// Insert second (passing) audit slightly later (time.Sleep not needed;
	// RFC3339Nano includes nanoseconds and the second insert runs after).
	second := &model.LaneAuditRecord{
		SpecID:       "SPEC-001",
		Passed:       true,
		FileCount:    2,
		LinesChanged: 8,
		Breaches:     "",
		BaseSHA:      "sha2sha2sha2",
	}
	if err := s.InsertLaneAudit(ctx, second); err != nil {
		t.Fatalf("InsertLaneAudit (pass): %v", err)
	}

	// LatestLaneAudit must return the passing (second) row.
	latest, err := s.LatestLaneAudit(ctx, "SPEC-001")
	if err != nil {
		t.Fatalf("LatestLaneAudit: %v", err)
	}
	if latest == nil {
		t.Fatal("expected non-nil latest audit record")
	}
	if !latest.Passed {
		t.Error("expected latest audit Passed=true")
	}
	if latest.FileCount != 2 {
		t.Errorf("FileCount: got %d, want 2", latest.FileCount)
	}
	if latest.BaseSHA != "sha2sha2sha2" {
		t.Errorf("BaseSHA: got %q, want sha2sha2sha2", latest.BaseSHA)
	}
	if latest.CreatedAt.IsZero() {
		t.Error("CreatedAt must not be zero")
	}
}

// --- SPEC-110: backlog_refinements counter (Paso 3) ---

// insertRawRefinement inserts a row directly into backlog_refinements via SQL,
// bypassing AppendBacklogRefinement (introduced in Paso 4). Used here only to
// set up fixtures for the counter/projection tests below.
func insertRawRefinement(t *testing.T, s *SDDStore, itemID string, seq int, body string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(context.Background(),
		`INSERT INTO backlog_refinements (item_id, seq, body, by, at) VALUES (?, ?, ?, '', ?)`,
		itemID, seq, body, now,
	)
	if err != nil {
		t.Fatalf("insert raw refinement for %s seq %d: %v", itemID, seq, err)
	}
}

// TestGetBacklogItem_RefinementCount is AC7: GetBacklogItem populates
// RefinementCount correctly, both when zero and when non-zero.
func TestGetBacklogItem_RefinementCount(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	zero := &model.BacklogItem{
		ID: "BL-001", Title: "no refinements", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: "proj", Lane: model.LaneStandard,
	}
	three := &model.BacklogItem{
		ID: "BL-002", Title: "three refinements", Status: model.BacklogStatusRefined,
		Priority: model.PriorityMedium, Project: "proj", Lane: model.LaneStandard,
	}
	if err := s.CreateBacklogItem(ctx, zero); err != nil {
		t.Fatalf("create BL-001: %v", err)
	}
	if err := s.CreateBacklogItem(ctx, three); err != nil {
		t.Fatalf("create BL-002: %v", err)
	}
	for seq := 1; seq <= 3; seq++ {
		insertRawRefinement(t, s, "BL-002", seq, fmt.Sprintf("refinement %d", seq))
	}

	gotZero, err := s.GetBacklogItem(ctx, "BL-001")
	if err != nil {
		t.Fatalf("GetBacklogItem BL-001: %v", err)
	}
	if gotZero.RefinementCount != 0 {
		t.Errorf("BL-001 RefinementCount = %d, want 0", gotZero.RefinementCount)
	}

	gotThree, err := s.GetBacklogItem(ctx, "BL-002")
	if err != nil {
		t.Fatalf("GetBacklogItem BL-002: %v", err)
	}
	if gotThree.RefinementCount != 3 {
		t.Errorf("BL-002 RefinementCount = %d, want 3", gotThree.RefinementCount)
	}
}

// TestListBacklogItems_RefinementCount is AC8: ListBacklogItems populates
// RefinementCount correctly for each item on the page, in a single page query.
func TestListBacklogItems_RefinementCount(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	project := "refcounttest"

	items := []*model.BacklogItem{
		{ID: "BL-001", Title: "zero", Status: model.BacklogStatusRaw, Priority: model.PriorityMedium, Project: project, Lane: model.LaneStandard},
		{ID: "BL-002", Title: "one", Status: model.BacklogStatusRefined, Priority: model.PriorityMedium, Project: project, Lane: model.LaneStandard},
		{ID: "BL-003", Title: "three", Status: model.BacklogStatusRefined, Priority: model.PriorityMedium, Project: project, Lane: model.LaneStandard},
	}
	for _, item := range items {
		if err := s.CreateBacklogItem(ctx, item); err != nil {
			t.Fatalf("create %s: %v", item.ID, err)
		}
	}
	insertRawRefinement(t, s, "BL-002", 1, "r1")
	for seq := 1; seq <= 3; seq++ {
		insertRawRefinement(t, s, "BL-003", seq, fmt.Sprintf("r%d", seq))
	}

	got, _, err := s.ListBacklogItems(ctx, project, "", 0)
	if err != nil {
		t.Fatalf("ListBacklogItems: %v", err)
	}

	counts := make(map[string]int, len(got))
	for _, item := range got {
		counts[item.ID] = item.RefinementCount
	}
	want := map[string]int{"BL-001": 0, "BL-002": 1, "BL-003": 3}
	for id, wantCount := range want {
		if counts[id] != wantCount {
			t.Errorf("item %s RefinementCount = %d, want %d", id, counts[id], wantCount)
		}
	}
}

// TestListBacklogItems_JoinDoesNotMultiplyRows is AC9: the LEFT JOIN against
// the derived, already-aggregated table must not multiply rows. Each item
// appears exactly once in the page regardless of how many refinements it has,
// and for every status filter (and no filter), total (limit=1) still equals
// len(unwindowed list) — the relation SPEC-109 D3/D6 built.
func TestListBacklogItems_JoinDoesNotMultiplyRows(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	project := "joinnomultiplytest"

	statuses := []model.BacklogStatus{model.BacklogStatusRaw, model.BacklogStatusRefined}
	n := 0
	for _, status := range statuses {
		for i := 0; i < 3; i++ {
			n++
			item := &model.BacklogItem{
				ID: fmt.Sprintf("BL-%03d", n), Title: fmt.Sprintf("item %d", n),
				Status: status, Priority: model.PriorityMedium,
				Project: project, Lane: model.LaneStandard,
			}
			if err := s.CreateBacklogItem(ctx, item); err != nil {
				t.Fatalf("create %s: %v", item.ID, err)
			}
		}
	}
	// Give items varying refinement counts: 0, 1, and 3.
	insertRawRefinement(t, s, "BL-001", 1, "r1")
	for seq := 1; seq <= 3; seq++ {
		insertRawRefinement(t, s, "BL-004", seq, fmt.Sprintf("r%d", seq))
	}

	unwindowed, _, err := s.ListBacklogItems(ctx, project, "", 0)
	if err != nil {
		t.Fatalf("ListBacklogItems(limit=0): %v", err)
	}
	if len(unwindowed) != 6 {
		t.Fatalf("expected 6 items (one row each, no multiplication), got %d", len(unwindowed))
	}
	seen := make(map[string]int)
	for _, item := range unwindowed {
		seen[item.ID]++
	}
	for id, count := range seen {
		if count != 1 {
			t.Errorf("item %s appeared %d times in the page, want exactly 1", id, count)
		}
	}

	cases := append([]model.BacklogStatus{""}, statuses...)
	for _, status := range cases {
		name := string(status)
		if name == "" {
			name = "(no filter)"
		}
		t.Run(name, func(t *testing.T) {
			list, _, err := s.ListBacklogItems(ctx, project, status, 0)
			if err != nil {
				t.Fatalf("ListBacklogItems(limit=0): %v", err)
			}
			_, total, err := s.ListBacklogItems(ctx, project, status, 1)
			if err != nil {
				t.Fatalf("ListBacklogItems(limit=1): %v", err)
			}
			if total != len(list) {
				t.Errorf("total=%d, want %d (len of unwindowed list)", total, len(list))
			}
		})
	}
}

// TestBacklogRefinementCount_GetAndListAgree is AC10: for the same item with N
// refinements, GetBacklogItem and ListBacklogItems must report the exact same
// RefinementCount — both paths derive their projection from the same const
// (backlogSelectColumns/backlogSelectFrom), so this symmetry is structural.
func TestBacklogRefinementCount_GetAndListAgree(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	project := "getlistagreetest"

	item := &model.BacklogItem{
		ID: "BL-001", Title: "agree test", Status: model.BacklogStatusRefined,
		Priority: model.PriorityMedium, Project: project, Lane: model.LaneStandard,
	}
	if err := s.CreateBacklogItem(ctx, item); err != nil {
		t.Fatalf("create BL-001: %v", err)
	}
	for seq := 1; seq <= 4; seq++ {
		insertRawRefinement(t, s, "BL-001", seq, fmt.Sprintf("r%d", seq))
	}

	got, err := s.GetBacklogItem(ctx, "BL-001")
	if err != nil {
		t.Fatalf("GetBacklogItem: %v", err)
	}
	list, _, err := s.ListBacklogItems(ctx, project, "", 0)
	if err != nil {
		t.Fatalf("ListBacklogItems: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected exactly 1 item in list, got %d", len(list))
	}
	if got.RefinementCount != list[0].RefinementCount {
		t.Errorf("GetBacklogItem.RefinementCount=%d, ListBacklogItems.RefinementCount=%d — they must agree",
			got.RefinementCount, list[0].RefinementCount)
	}
	if got.RefinementCount != 4 {
		t.Errorf("RefinementCount = %d, want 4", got.RefinementCount)
	}
}

// TestBacklogListSelect_UsesDerivedJoinNotCorrelatedSubquery is AC11a: the
// counter must come from a LEFT JOIN against an already-aggregated derived
// table, never a correlated subquery (which would embed a WHERE and break
// TestBacklogListPredicate_SharedByCountAndPage — precisely what that guard
// exists to catch), and backlogCountSelect must never gain the join (Total
// counts items, not refinements — SPEC-109 D3).
func TestBacklogListSelect_UsesDerivedJoinNotCorrelatedSubquery(t *testing.T) {
	upper := strings.ToUpper(backlogListSelect)
	if !strings.Contains(upper, "LEFT JOIN") {
		t.Error("backlogListSelect must contain a LEFT JOIN to project the refinement counter")
	}
	if !strings.Contains(upper, "GROUP BY") {
		t.Error("backlogListSelect must join against a GROUP BY-aggregated derived table")
	}
	if strings.Contains(upper, "WHERE") {
		t.Error("backlogListSelect must not contain WHERE — a correlated subquery would break the shared predicate guard")
	}
	if strings.Contains(strings.ToUpper(backlogCountSelect), "LEFT JOIN") {
		t.Error("backlogCountSelect must never gain the join — total counts items, not refinements")
	}
}

// TestListBacklogItems_EmitsExactlyTwoQueries is AC11a (zero N+1): the source
// of ListBacklogItems must call the database exactly twice — one COUNT and one
// page query — regardless of how many items are on the page. Textual guard,
// precedent internal/service/profile_default_readonce_test.go.
func TestListBacklogItems_EmitsExactlyTwoQueries(t *testing.T) {
	src, err := os.ReadFile("sdd.go")
	if err != nil {
		t.Fatalf("read sdd.go: %v", err)
	}

	start := bytes.Index(src, []byte("func (s *SDDStore) ListBacklogItems("))
	if start == -1 {
		t.Fatal("ListBacklogItems not found in sdd.go")
	}
	// The function body ends at the next top-level "\n}\n" after start.
	end := bytes.Index(src[start:], []byte("\n}\n"))
	if end == -1 {
		t.Fatal("could not locate end of ListBacklogItems body")
	}
	body := string(src[start : start+end])

	count := strings.Count(body, "s.db.QueryRowContext") +
		strings.Count(body, "s.db.QueryContext") +
		strings.Count(body, "s.db.ExecContext")
	if count != 2 {
		t.Errorf("ListBacklogItems body issues %d db calls, want exactly 2 (one COUNT, one page): body=%s", count, body)
	}
}

// TestBacklogListOrder_QualifiesEveryColumn is AC11b, the R3 silence-trap
// guard: backlogListOrder must qualify every column with the b alias — an
// unqualified `rowid` breaks the deterministic tie-break in total silence the
// moment a second FROM source (the derived table) exists.
func TestBacklogListOrder_QualifiesEveryColumn(t *testing.T) {
	if !strings.Contains(backlogListOrder, "b.rowid") {
		t.Error("backlogListOrder must qualify rowid as b.rowid")
	}
	if !strings.Contains(backlogListOrder, "b.priority") {
		t.Error("backlogListOrder must qualify priority as b.priority")
	}
	if !strings.Contains(backlogListOrder, "b.position") {
		t.Error("backlogListOrder must qualify position as b.position")
	}
	// A bare " rowid" (preceded by whitespace, not "b.rowid") would mean the
	// qualification was lost or only partially applied.
	for _, token := range strings.Fields(backlogListOrder) {
		if token == "rowid," || token == "rowid" {
			t.Errorf("backlogListOrder contains a bare, unqualified rowid token: %q", token)
		}
	}
}

// --- SPEC-110: AppendBacklogRefinement / ListBacklogRefinements (Paso 4) ---

// TestAppendBacklogRefinement_SequenceIsPerItem is AC4: seq is 1,2,3... per
// item, independent between items, and rows are read back by
// ListBacklogRefinements in seq-ascending order.
func TestAppendBacklogRefinement_SequenceIsPerItem(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	itemA := &model.BacklogItem{ID: "BL-001", Title: "a", Status: model.BacklogStatusRaw, Priority: model.PriorityMedium, Project: "proj", Lane: model.LaneStandard}
	itemB := &model.BacklogItem{ID: "BL-002", Title: "b", Status: model.BacklogStatusRaw, Priority: model.PriorityMedium, Project: "proj", Lane: model.LaneStandard}
	if err := s.CreateBacklogItem(ctx, itemA); err != nil {
		t.Fatalf("create BL-001: %v", err)
	}
	if err := s.CreateBacklogItem(ctx, itemB); err != nil {
		t.Fatalf("create BL-002: %v", err)
	}

	status := model.BacklogStatusRaw
	for i := 0; i < 3; i++ {
		r, err := s.AppendBacklogRefinement(ctx, "BL-001", status, model.BacklogStatusRefined, fmt.Sprintf("a-r%d", i+1), "architect")
		if err != nil {
			t.Fatalf("append refinement %d to BL-001: %v", i+1, err)
		}
		if r.Seq != i+1 {
			t.Errorf("BL-001 refinement %d: seq = %d, want %d", i+1, r.Seq, i+1)
		}
		status = model.BacklogStatusRefined
	}

	if _, err := s.AppendBacklogRefinement(ctx, "BL-002", model.BacklogStatusRaw, model.BacklogStatusRefined, "b-r1", "backend"); err != nil {
		t.Fatalf("append refinement to BL-002: %v", err)
	}

	refsA, err := s.ListBacklogRefinements(ctx, "BL-001")
	if err != nil {
		t.Fatalf("ListBacklogRefinements BL-001: %v", err)
	}
	if len(refsA) != 3 {
		t.Fatalf("BL-001: expected 3 refinements, got %d", len(refsA))
	}
	for i, r := range refsA {
		if r.Seq != i+1 {
			t.Errorf("BL-001 refinement[%d].Seq = %d, want %d", i, r.Seq, i+1)
		}
		if r.Body != fmt.Sprintf("a-r%d", i+1) {
			t.Errorf("BL-001 refinement[%d].Body = %q, want %q", i, r.Body, fmt.Sprintf("a-r%d", i+1))
		}
	}

	refsB, err := s.ListBacklogRefinements(ctx, "BL-002")
	if err != nil {
		t.Fatalf("ListBacklogRefinements BL-002: %v", err)
	}
	if len(refsB) != 1 || refsB[0].Seq != 1 {
		t.Errorf("BL-002: expected 1 refinement with seq=1, got %+v", refsB)
	}
}

// TestAppendBacklogRefinement_RollsBackOnInsertFailure is AC5: if the row
// cannot be inserted, the item's status is left unmodified — the whole
// operation is one transaction (D14). The failure is forced by dropping
// backlog_refinements out from under the call, which fails the transaction
// before its COMMIT (whether the exact failing statement is the seq lookup or
// the INSERT itself, the guarantee under test is the same: no partial write).
func TestAppendBacklogRefinement_RollsBackOnInsertFailure(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	item := &model.BacklogItem{ID: "BL-001", Title: "x", Status: model.BacklogStatusRaw, Priority: model.PriorityMedium, Project: "proj", Lane: model.LaneStandard}
	if err := s.CreateBacklogItem(ctx, item); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := s.db.ExecContext(ctx, `DROP TABLE backlog_refinements`); err != nil {
		t.Fatalf("drop backlog_refinements to force a failure: %v", err)
	}

	_, err := s.AppendBacklogRefinement(ctx, "BL-001", model.BacklogStatusRaw, model.BacklogStatusRefined, "collides", "")
	if err == nil {
		t.Fatal("expected AppendBacklogRefinement to fail when backlog_refinements is unavailable, got nil error")
	}

	// Reconstruct the schema so GetBacklogItem's shared projection (which
	// joins backlog_refinements) can run.
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE backlog_refinements (
		    item_id  TEXT    NOT NULL REFERENCES backlog_items(id) ON DELETE CASCADE,
		    seq      INTEGER NOT NULL,
		    body     TEXT    NOT NULL,
		    by       TEXT    NOT NULL DEFAULT '',
		    at       TEXT    NOT NULL,
		    PRIMARY KEY (item_id, seq)
		)`); err != nil {
		t.Fatalf("recreate backlog_refinements: %v", err)
	}

	got, err := s.GetBacklogItem(ctx, "BL-001")
	if err != nil {
		t.Fatalf("GetBacklogItem: %v", err)
	}
	if got.Status != model.BacklogStatusRaw {
		t.Errorf("status changed to %q despite rolled-back transaction, want raw", got.Status)
	}
}

// TestAppendBacklogRefinement_OptimisticLockRejectsStatusDrift is AC6: when
// expected no longer matches the item's real status, AppendBacklogRefinement
// returns ErrBacklogNotRefinable and inserts nothing.
func TestAppendBacklogRefinement_OptimisticLockRejectsStatusDrift(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	item := &model.BacklogItem{ID: "BL-001", Title: "x", Status: model.BacklogStatusPromoted, Priority: model.PriorityMedium, Project: "proj", Lane: model.LaneStandard}
	if err := s.CreateBacklogItem(ctx, item); err != nil {
		t.Fatalf("create: %v", err)
	}

	// The service believes the item is still raw; the store finds promoted.
	_, err := s.AppendBacklogRefinement(ctx, "BL-001", model.BacklogStatusRaw, model.BacklogStatusRefined, "drifted", "")
	if !errors.Is(err, model.ErrBacklogNotRefinable) {
		t.Fatalf("expected ErrBacklogNotRefinable, got %v", err)
	}

	refs, err := s.ListBacklogRefinements(ctx, "BL-001")
	if err != nil {
		t.Fatalf("ListBacklogRefinements: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("expected 0 refinements after rejected drift, got %d", len(refs))
	}

	got, err := s.GetBacklogItem(ctx, "BL-001")
	if err != nil {
		t.Fatalf("GetBacklogItem: %v", err)
	}
	if got.Status != model.BacklogStatusPromoted {
		t.Errorf("status changed to %q, want unchanged promoted", got.Status)
	}
}

// TestAppendBacklogRefinement_NeverTouchesDescription verifies description is
// byte-identical before and after appending refinements (D15).
func TestAppendBacklogRefinement_NeverTouchesDescription(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	const originalDescription = "the original description, untouched"
	item := &model.BacklogItem{
		ID: "BL-001", Title: "x", Description: originalDescription,
		Status: model.BacklogStatusRaw, Priority: model.PriorityMedium, Project: "proj", Lane: model.LaneStandard,
	}
	if err := s.CreateBacklogItem(ctx, item); err != nil {
		t.Fatalf("create: %v", err)
	}

	status := model.BacklogStatusRaw
	for i := 0; i < 3; i++ {
		if _, err := s.AppendBacklogRefinement(ctx, "BL-001", status, model.BacklogStatusRefined, fmt.Sprintf("r%d", i+1), ""); err != nil {
			t.Fatalf("append refinement %d: %v", i+1, err)
		}
		status = model.BacklogStatusRefined
	}

	got, err := s.GetBacklogItem(ctx, "BL-001")
	if err != nil {
		t.Fatalf("GetBacklogItem: %v", err)
	}
	if got.Description != originalDescription {
		t.Errorf("description changed: got %q, want %q", got.Description, originalDescription)
	}
}

// TestBacklogRefinements_CascadeOnItemDelete is AC12: with foreign_keys=ON,
// deleting the parent backlog_items row cascades to its backlog_refinements.
func TestBacklogRefinements_CascadeOnItemDelete(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	item := &model.BacklogItem{ID: "BL-001", Title: "x", Status: model.BacklogStatusRefined, Priority: model.PriorityMedium, Project: "proj", Lane: model.LaneStandard}
	if err := s.CreateBacklogItem(ctx, item); err != nil {
		t.Fatalf("create: %v", err)
	}
	insertRawRefinement(t, s, "BL-001", 1, "r1")
	insertRawRefinement(t, s, "BL-001", 2, "r2")

	if _, err := s.db.ExecContext(ctx, `DELETE FROM backlog_items WHERE id = ?`, "BL-001"); err != nil {
		t.Fatalf("delete backlog item: %v", err)
	}

	refs, err := s.ListBacklogRefinements(ctx, "BL-001")
	if err != nil {
		t.Fatalf("ListBacklogRefinements: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("expected 0 refinements after cascading delete, got %d", len(refs))
	}
}
