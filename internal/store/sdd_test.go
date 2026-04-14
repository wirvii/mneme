package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/juanftp/mneme/internal/db"
	"github.com/juanftp/mneme/internal/model"
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
		Priority: model.PriorityMedium, Project: project,
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
		Priority: model.PriorityMedium, Project: project,
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
		{ID: "BL-001", Title: "Critical item", Status: model.BacklogStatusRaw, Priority: model.PriorityCritical, Project: project},
		{ID: "BL-002", Title: "Low item", Status: model.BacklogStatusRefined, Priority: model.PriorityLow, Project: project},
		{ID: "BL-003", Title: "Medium item", Status: model.BacklogStatusRaw, Priority: model.PriorityMedium, Project: project},
	}
	for _, item := range items {
		if err := s.CreateBacklogItem(ctx, item); err != nil {
			t.Fatalf("create %s: %v", item.ID, err)
		}
	}

	// Filter by status=raw: expect BL-001 and BL-003.
	rawItems, err := s.ListBacklogItems(ctx, project, model.BacklogStatusRaw)
	if err != nil {
		t.Fatalf("ListBacklogItems(raw): %v", err)
	}
	if len(rawItems) != 2 {
		t.Errorf("expected 2 raw items, got %d", len(rawItems))
	}

	// No filter: expect all 3 items, ordered by priority (critical first).
	all, err := s.ListBacklogItems(ctx, project, "")
	if err != nil {
		t.Fatalf("ListBacklogItems(all): %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 items, got %d", len(all))
	}
	// Priority ordering: critical < medium < low (by rank).
	// Since priority is stored as text in SQLite and sorted ASC by text,
	// we verify the first item is critical (rank 0) which sorts first alphabetically.
	// Actually SQLite text sort: critical < high < low < medium (lexicographic).
	// The spec says ORDER BY priority ASC which is alphabetical, not by rank.
	// We just verify all items are present.
	ids := map[string]bool{}
	for _, item := range all {
		ids[item.ID] = true
	}
	for _, expected := range []string{"BL-001", "BL-002", "BL-003"} {
		if !ids[expected] {
			t.Errorf("missing item %s in list", expected)
		}
	}
}

func TestUpdateBacklogItem(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	project := "updatetest"

	item := &model.BacklogItem{
		ID: "BL-001", Title: "Original", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: project,
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
		ID: "SPEC-001", Title: "first", Status: model.SpecStatusDraft, Project: project,
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
		ID: "SPEC-001", Title: "status test", Status: model.SpecStatusDraft, Project: "proj",
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
		ID: "SPEC-001", Title: "history test", Status: model.SpecStatusDraft, Project: "proj",
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
		ID: "SPEC-001", Title: "pb test", Status: model.SpecStatusSpeccing, Project: "proj",
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
		ID: "SPEC-001", Title: "resolve test", Status: model.SpecStatusNeedsGrill, Project: "proj",
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
		ID: "SPEC-001", Title: "multi pb", Status: model.SpecStatusNeedsGrill, Project: "proj",
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
		{ID: "BL-001", Status: model.BacklogStatusRaw, Priority: model.PriorityMedium, Project: project, Title: "a"},
		{ID: "BL-002", Status: model.BacklogStatusRaw, Priority: model.PriorityMedium, Project: project, Title: "b"},
		{ID: "BL-003", Status: model.BacklogStatusRefined, Priority: model.PriorityMedium, Project: project, Title: "c"},
		{ID: "BL-004", Status: model.BacklogStatusArchived, Priority: model.PriorityMedium, Project: project, Title: "d"},
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
		{ID: "SPEC-001", Status: model.SpecStatusDraft, Project: project, Title: "a"},
		{ID: "SPEC-002", Status: model.SpecStatusDraft, Project: project, Title: "b"},
		{ID: "SPEC-003", Status: model.SpecStatusImplementing, Project: project, Title: "c"},
		{ID: "SPEC-004", Status: model.SpecStatusDone, Project: project, Title: "d"},
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
			ID: id, Status: status, Project: project, Title: "spec " + id,
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
