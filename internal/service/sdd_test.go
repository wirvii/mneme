package service

import (
	"context"
	"errors"
	"testing"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/db"
	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/store"
)

// newTestSDDService creates a real SDDService backed by an in-memory SQLite
// database with all migrations applied. No mocks — tests run against real SQL.
func newTestSDDService(t *testing.T, project string) *SDDService {
	t.Helper()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })

	sddStore := store.NewSDDStore(database)
	cfg := config.Default()
	// Pass nil memorySvc — completion memory saving is not exercised in unit tests.
	return NewSDDService(sddStore, cfg, project, nil)
}

// --- BACKLOG TESTS ---

func TestBacklogAdd_Success(t *testing.T) {
	svc := newTestSDDService(t, "testproject")
	ctx := context.Background()

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{
		Title:       "Add notifications",
		Description: "Push notification support",
		Priority:    model.PriorityHigh,
	})
	if err != nil {
		t.Fatalf("BacklogAdd: %v", err)
	}

	if item.ID != "BL-001" {
		t.Errorf("ID: got %q, want BL-001", item.ID)
	}
	if item.Status != model.BacklogStatusRaw {
		t.Errorf("Status: got %q, want raw", item.Status)
	}
	if item.Priority != model.PriorityHigh {
		t.Errorf("Priority: got %q, want high", item.Priority)
	}
	if item.Title != "Add notifications" {
		t.Errorf("Title: got %q", item.Title)
	}
	if item.CreatedAt.IsZero() {
		t.Error("CreatedAt must not be zero")
	}
}

func TestBacklogAdd_EmptyTitle(t *testing.T) {
	svc := newTestSDDService(t, "testproject")
	ctx := context.Background()

	_, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: ""})
	if !errors.Is(err, model.ErrTitleRequired) {
		t.Errorf("expected ErrTitleRequired, got %v", err)
	}
}

func TestBacklogAdd_InvalidPriority(t *testing.T) {
	svc := newTestSDDService(t, "testproject")
	ctx := context.Background()

	_, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{
		Title:    "Test",
		Priority: "urgent",
	})
	if !errors.Is(err, model.ErrInvalidPriority) {
		t.Errorf("expected ErrInvalidPriority, got %v", err)
	}
}

func TestBacklogAdd_DefaultProject(t *testing.T) {
	svc := newTestSDDService(t, "myproject")
	ctx := context.Background()

	// No Project in request — should use service project.
	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "Test"})
	if err != nil {
		t.Fatalf("BacklogAdd: %v", err)
	}
	if item.Project != "myproject" {
		t.Errorf("Project: got %q, want myproject", item.Project)
	}
}

func TestBacklogAdd_DefaultPriority(t *testing.T) {
	svc := newTestSDDService(t, "p")
	ctx := context.Background()

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "Test"})
	if err != nil {
		t.Fatalf("BacklogAdd: %v", err)
	}
	if item.Priority != model.PriorityMedium {
		t.Errorf("Priority: got %q, want medium", item.Priority)
	}
}

func TestBacklogList_FilterByStatus(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	// Create 3 items: 2 raw, 1 refined.
	for _, title := range []string{"A", "B"} {
		if _, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: title}); err != nil {
			t.Fatalf("add %s: %v", title, err)
		}
	}
	itemC, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "C"})
	if err != nil {
		t.Fatalf("add C: %v", err)
	}
	if _, err := svc.BacklogRefine(ctx, model.BacklogRefineRequest{ID: itemC.ID, Refinement: "details"}); err != nil {
		t.Fatalf("refine C: %v", err)
	}

	rawItems, err := svc.BacklogList(ctx, model.BacklogListRequest{Status: model.BacklogStatusRaw})
	if err != nil {
		t.Fatalf("BacklogList(raw): %v", err)
	}
	if len(rawItems) != 2 {
		t.Errorf("expected 2 raw items, got %d", len(rawItems))
	}

	refined, err := svc.BacklogList(ctx, model.BacklogListRequest{Status: model.BacklogStatusRefined})
	if err != nil {
		t.Fatalf("BacklogList(refined): %v", err)
	}
	if len(refined) != 1 {
		t.Errorf("expected 1 refined item, got %d", len(refined))
	}
}

func TestBacklogRefine_Success(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "Feature X"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	refined, err := svc.BacklogRefine(ctx, model.BacklogRefineRequest{
		ID:         item.ID,
		Refinement: "This feature does Y and Z",
	})
	if err != nil {
		t.Fatalf("BacklogRefine: %v", err)
	}

	if refined.Status != model.BacklogStatusRefined {
		t.Errorf("Status: got %q, want refined", refined.Status)
	}
	if refined.Description != "This feature does Y and Z" {
		t.Errorf("Description: got %q", refined.Description)
	}
}

func TestBacklogRefine_NotRaw(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "X"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	// Refine once.
	if _, err := svc.BacklogRefine(ctx, model.BacklogRefineRequest{ID: item.ID, Refinement: "r"}); err != nil {
		t.Fatalf("first refine: %v", err)
	}
	// Second refine should fail because item is now refined, not raw.
	_, err = svc.BacklogRefine(ctx, model.BacklogRefineRequest{ID: item.ID, Refinement: "r2"})
	if !errors.Is(err, model.ErrInvalidBacklogStatus) {
		t.Errorf("expected ErrInvalidBacklogStatus, got %v", err)
	}
}

func TestBacklogPromote_Success(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "Feature Y"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := svc.BacklogRefine(ctx, model.BacklogRefineRequest{ID: item.ID, Refinement: "details"}); err != nil {
		t.Fatalf("refine: %v", err)
	}

	spec, err := svc.BacklogPromote(ctx, item.ID)
	if err != nil {
		t.Fatalf("BacklogPromote: %v", err)
	}

	if spec.BacklogID != item.ID {
		t.Errorf("spec.BacklogID: got %q, want %q", spec.BacklogID, item.ID)
	}
	if spec.Status != model.SpecStatusDraft {
		t.Errorf("spec.Status: got %q, want draft", spec.Status)
	}
	if spec.Title != "Feature Y" {
		t.Errorf("spec.Title: got %q, want Feature Y", spec.Title)
	}

	// Verify backlog item is marked promoted.
	items, err := svc.BacklogList(ctx, model.BacklogListRequest{Status: model.BacklogStatusPromoted})
	if err != nil {
		t.Fatalf("list promoted: %v", err)
	}
	if len(items) != 1 || items[0].SpecID != spec.ID {
		t.Errorf("expected 1 promoted item with spec_id=%q", spec.ID)
	}
}

func TestBacklogPromote_NotRefined(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "Raw item"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	_, err = svc.BacklogPromote(ctx, item.ID)
	if !errors.Is(err, model.ErrBacklogNotRefined) {
		t.Errorf("expected ErrBacklogNotRefined, got %v", err)
	}
}

func TestBacklogArchive(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "Archive me"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	if err := svc.BacklogArchive(ctx, item.ID, "not needed anymore"); err != nil {
		t.Fatalf("BacklogArchive: %v", err)
	}

	archived, err := svc.BacklogList(ctx, model.BacklogListRequest{Status: model.BacklogStatusArchived})
	if err != nil {
		t.Fatalf("list archived: %v", err)
	}
	if len(archived) != 1 {
		t.Fatalf("expected 1 archived item, got %d", len(archived))
	}
	if archived[0].ArchiveReason != "not needed anymore" {
		t.Errorf("ArchiveReason: got %q", archived[0].ArchiveReason)
	}
}

// --- SPEC TESTS ---

func TestSpecNew(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "SDD Engine"})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	if spec.ID != "SPEC-001" {
		t.Errorf("ID: got %q, want SPEC-001", spec.ID)
	}
	if spec.Status != model.SpecStatusDraft {
		t.Errorf("Status: got %q, want draft", spec.Status)
	}
	if spec.Project != "project" {
		t.Errorf("Project: got %q, want project", spec.Project)
	}
}

func TestSpecAdvance_ValidTransitions(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Full lifecycle"})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	// Happy path: draft -> speccing -> specced -> planning -> planned -> implementing -> qa -> done
	path := []model.SpecStatus{
		model.SpecStatusSpeccing,
		model.SpecStatusSpecced,
		model.SpecStatusPlanning,
		model.SpecStatusPlanned,
		model.SpecStatusImplementing,
		model.SpecStatusQA,
		model.SpecStatusDone,
	}

	for _, expectedNext := range path {
		t.Run("advance to "+string(expectedNext), func(t *testing.T) {
			advanced, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{
				ID:  spec.ID,
				By:  "orchestrator",
			})
			if err != nil {
				t.Fatalf("SpecAdvance: %v", err)
			}
			if advanced.Status != expectedNext {
				t.Errorf("Status: got %q, want %q", advanced.Status, expectedNext)
			}
			// Use updated spec for next iteration.
			spec = advanced
		})
	}
}

func TestSpecAdvance_InvalidTransition(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Invalid advance"})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	// Advance to speccing (valid).
	if _, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "test"}); err != nil {
		t.Fatalf("advance to speccing: %v", err)
	}

	// Advance to specced (valid).
	if _, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "test"}); err != nil {
		t.Fatalf("advance to specced: %v", err)
	}

	// Now spec is in specced. Advance forward succeeds (to planning).
	// But done from specced is invalid — SpecAdvance would try to go to planning,
	// which is correct. Instead test that from done nothing can advance.
	// Advance all the way to done first.
	remaining := []string{"planning", "planned", "implementing", "qa", "done"}
	for _, s := range remaining {
		if _, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "test"}); err != nil {
			t.Fatalf("advance to %s: %v", s, err)
		}
	}

	// Now at done — advancing should fail.
	_, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "test"})
	if !errors.Is(err, model.ErrInvalidTransition) {
		t.Errorf("expected ErrInvalidTransition from done state, got %v", err)
	}
}

func TestSpecPushback_FromSpeccing(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Pushback test"})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}
	// Advance to speccing.
	if _, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "orch"}); err != nil {
		t.Fatalf("advance: %v", err)
	}

	pushed, err := svc.SpecPushback(ctx, model.SpecPushbackRequest{
		ID:        spec.ID,
		FromAgent: "architect",
		Questions: []string{"Hook in Go or shell?", "Auth model?"},
	})
	if err != nil {
		t.Fatalf("SpecPushback: %v", err)
	}

	if pushed.Status != model.SpecStatusNeedsGrill {
		t.Errorf("Status: got %q, want needs_grill", pushed.Status)
	}
}

func TestSpecPushback_FromImplementing(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Impl pushback"})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}
	// Advance through: speccing, specced, planning, planned, implementing.
	for range []int{0, 1, 2, 3, 4} {
		if _, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "test"}); err != nil {
			t.Fatalf("advance: %v", err)
		}
	}

	pushed, err := svc.SpecPushback(ctx, model.SpecPushbackRequest{
		ID:        spec.ID,
		FromAgent: "backend",
		Questions: []string{"Missing auth contract"},
	})
	if err != nil {
		t.Fatalf("SpecPushback from implementing: %v", err)
	}
	if pushed.Status != model.SpecStatusNeedsGrill {
		t.Errorf("Status: got %q, want needs_grill", pushed.Status)
	}
}

func TestSpecPushback_InvalidState(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Draft pushback"})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	// Draft cannot transition to needs_grill.
	_, err = svc.SpecPushback(ctx, model.SpecPushbackRequest{
		ID:        spec.ID,
		FromAgent: "architect",
		Questions: []string{"Q?"},
	})
	if !errors.Is(err, model.ErrInvalidTransition) {
		t.Errorf("expected ErrInvalidTransition from draft, got %v", err)
	}
}

func TestSpecResolve(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Resolve test"})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}
	// Advance to speccing.
	if _, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "orch"}); err != nil {
		t.Fatalf("advance: %v", err)
	}
	// Push back.
	if _, err := svc.SpecPushback(ctx, model.SpecPushbackRequest{
		ID: spec.ID, FromAgent: "architect", Questions: []string{"Q1"},
	}); err != nil {
		t.Fatalf("pushback: %v", err)
	}

	resolved, err := svc.SpecResolve(ctx, model.SpecResolveRequest{
		ID:         spec.ID,
		Resolution: "Use Go hooks, not shell",
	})
	if err != nil {
		t.Fatalf("SpecResolve: %v", err)
	}
	if resolved.Status != model.SpecStatusSpeccing {
		t.Errorf("Status: got %q, want speccing", resolved.Status)
	}

	// Verify pushback is now resolved.
	sr, err := svc.SpecStatus(ctx, spec.ID)
	if err != nil {
		t.Fatalf("SpecStatus: %v", err)
	}
	if len(sr.Pushbacks) != 1 || !sr.Pushbacks[0].Resolved {
		t.Error("expected pushback to be marked resolved")
	}
}

func TestSpecResolve_NotNeedsGrill(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "No grill"})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	_, err = svc.SpecResolve(ctx, model.SpecResolveRequest{ID: spec.ID, Resolution: "N/A"})
	if !errors.Is(err, model.ErrInvalidTransition) {
		t.Errorf("expected ErrInvalidTransition, got %v", err)
	}
}

func TestSpecStatus(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Status test"})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}
	// Advance to speccing.
	if _, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "orch"}); err != nil {
		t.Fatalf("advance: %v", err)
	}
	// Push back.
	if _, err := svc.SpecPushback(ctx, model.SpecPushbackRequest{
		ID: spec.ID, FromAgent: "architect", Questions: []string{"Q1"},
	}); err != nil {
		t.Fatalf("pushback: %v", err)
	}

	sr, err := svc.SpecStatus(ctx, spec.ID)
	if err != nil {
		t.Fatalf("SpecStatus: %v", err)
	}

	if sr.Spec == nil {
		t.Fatal("Spec must not be nil")
	}
	if sr.Spec.Status != model.SpecStatusNeedsGrill {
		t.Errorf("Spec.Status: got %q, want needs_grill", sr.Spec.Status)
	}
	if len(sr.History) == 0 {
		t.Error("History must not be empty")
	}
	if len(sr.Pushbacks) != 1 {
		t.Errorf("expected 1 pushback, got %d", len(sr.Pushbacks))
	}
}

func TestSpecList_FilterByStatus(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	// Create 2 specs in draft, advance 1 to speccing.
	s1, _ := svc.SpecNew(ctx, model.SpecNewRequest{Title: "S1"})
	s2, _ := svc.SpecNew(ctx, model.SpecNewRequest{Title: "S2"})
	if _, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: s1.ID, By: "test"}); err != nil {
		t.Fatalf("advance s1: %v", err)
	}
	_ = s2

	drafts, err := svc.SpecList(ctx, model.SpecListRequest{Status: model.SpecStatusDraft})
	if err != nil {
		t.Fatalf("SpecList(draft): %v", err)
	}
	if len(drafts) != 1 {
		t.Errorf("expected 1 draft, got %d", len(drafts))
	}

	speccing, err := svc.SpecList(ctx, model.SpecListRequest{Status: model.SpecStatusSpeccing})
	if err != nil {
		t.Fatalf("SpecList(speccing): %v", err)
	}
	if len(speccing) != 1 {
		t.Errorf("expected 1 speccing, got %d", len(speccing))
	}
}
