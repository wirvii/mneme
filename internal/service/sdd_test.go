package service

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
		Lane:        model.LaneStandard,
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
		Lane:     model.LaneStandard,
	})
	if !errors.Is(err, model.ErrInvalidPriority) {
		t.Errorf("expected ErrInvalidPriority, got %v", err)
	}
}

func TestBacklogAdd_DefaultProject(t *testing.T) {
	svc := newTestSDDService(t, "myproject")
	ctx := context.Background()

	// No Project in request — should use service project.
	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "Test", Lane: model.LaneStandard})
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

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "Test", Lane: model.LaneStandard})
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
		if _, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: title, Lane: model.LaneStandard}); err != nil {
			t.Fatalf("add %s: %v", title, err)
		}
	}
	itemC, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "C", Lane: model.LaneStandard})
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

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "Feature X", Lane: model.LaneStandard})
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

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "X", Lane: model.LaneStandard})
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

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "Feature Y", Lane: model.LaneStandard})
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

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "Raw item", Lane: model.LaneStandard})
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

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "Archive me", Lane: model.LaneStandard})
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

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "SDD Engine", Lane: model.LaneStandard})
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

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Full lifecycle", Lane: model.LaneStandard})
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

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Invalid advance", Lane: model.LaneStandard})
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

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Pushback test", Lane: model.LaneStandard})
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

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Impl pushback", Lane: model.LaneStandard})
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

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Draft pushback", Lane: model.LaneStandard})
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

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Resolve test", Lane: model.LaneStandard})
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

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "No grill", Lane: model.LaneStandard})
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

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Status test", Lane: model.LaneStandard})
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
	s1, _ := svc.SpecNew(ctx, model.SpecNewRequest{Title: "S1", Lane: model.LaneStandard})
	s2, _ := svc.SpecNew(ctx, model.SpecNewRequest{Title: "S2", Lane: model.LaneStandard})
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

// --- LANE TESTS ---

// TestBacklogAdd_LaneRequired verifies that omitting lane returns ErrLaneRequired.
func TestBacklogAdd_LaneRequired(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	_, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "Test"})
	if !errors.Is(err, model.ErrLaneRequired) {
		t.Errorf("expected ErrLaneRequired, got %v", err)
	}
}

// TestBacklogAdd_ScopeRequired verifies that trivial lane without scope returns ErrScopeRequired.
func TestBacklogAdd_ScopeRequired(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	_, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "Test", Lane: model.LaneTrivial})
	if !errors.Is(err, model.ErrScopeRequired) {
		t.Errorf("expected ErrScopeRequired, got %v", err)
	}
}

// TestSpecNew_LaneRequired verifies that omitting lane returns ErrLaneRequired.
func TestSpecNew_LaneRequired(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	_, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Test"})
	if !errors.Is(err, model.ErrLaneRequired) {
		t.Errorf("expected ErrLaneRequired, got %v", err)
	}
}

// TestSpecNew_ScopeRequired verifies that trivial lane without scope returns ErrScopeRequired.
func TestSpecNew_ScopeRequired(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	_, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Test", Lane: model.LaneTrivial})
	if !errors.Is(err, model.ErrScopeRequired) {
		t.Errorf("expected ErrScopeRequired, got %v", err)
	}
}

// TestSpecQuick_RejectsStandard verifies that spec_quick returns ErrLaneMismatch
// when called on a standard-lane spec.
func TestSpecQuick_RejectsStandard(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Standard spec", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	_, err = svc.SpecQuick(ctx, model.SpecQuickRequest{
		ID:        spec.ID,
		Rationale: "This is quick",
		By:        "orchestrator",
	})
	if !errors.Is(err, model.ErrLaneMismatch) {
		t.Errorf("expected ErrLaneMismatch, got %v", err)
	}
}

// TestSpecQuick_TrivialFlow verifies the happy path: trivial spec goes from
// draft to implementing after spec_quick.
func TestSpecQuick_TrivialFlow(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
		Title: "Tiny fix",
		Lane:  model.LaneTrivial,
		Scope: "internal/store/*.go",
	})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}
	if spec.Lane != model.LaneTrivial {
		t.Errorf("Lane: got %q, want trivial", spec.Lane)
	}

	implementing, err := svc.SpecQuick(ctx, model.SpecQuickRequest{
		ID:        spec.ID,
		Rationale: "One-line fix to a comment typo",
		By:        "orchestrator",
	})
	if err != nil {
		t.Fatalf("SpecQuick: %v", err)
	}
	if implementing.Status != model.SpecStatusImplementing {
		t.Errorf("Status: got %q, want implementing", implementing.Status)
	}
}

// TestBacklogPromote_PropagatesLane verifies that BacklogPromote copies lane and
// scope from the backlog item to the newly created spec.
func TestBacklogPromote_PropagatesLane(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{
		Title: "Tiny thing",
		Lane:  model.LaneTrivial,
		Scope: "internal/model/*.go",
	})
	if err != nil {
		t.Fatalf("BacklogAdd: %v", err)
	}
	if _, err := svc.BacklogRefine(ctx, model.BacklogRefineRequest{
		ID:         item.ID,
		Refinement: "One-line refactoring",
	}); err != nil {
		t.Fatalf("BacklogRefine: %v", err)
	}

	spec, err := svc.BacklogPromote(ctx, item.ID)
	if err != nil {
		t.Fatalf("BacklogPromote: %v", err)
	}
	if spec.Lane != model.LaneTrivial {
		t.Errorf("spec.Lane: got %q, want trivial", spec.Lane)
	}
	if spec.Scope != "internal/model/*.go" {
		t.Errorf("spec.Scope: got %q, want internal/model/*.go", spec.Scope)
	}
}

// TestSpecAdvance_TrivialPath verifies the trivial lane forward path:
// draft → rationale → implementing → audit → done (using manual store transitions
// to avoid git dependency on the audit step).
func TestSpecAdvance_TrivialPath(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
		Title: "Trivial fix",
		Lane:  model.LaneTrivial,
		Scope: "internal/model/*.go",
	})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	// draft → rationale
	advanced, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "orch"})
	if err != nil {
		t.Fatalf("advance draft->rationale: %v", err)
	}
	if advanced.Status != model.SpecStatusRationale {
		t.Errorf("Status: got %q, want rationale", advanced.Status)
	}

	// rationale → implementing
	advanced, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "orch"})
	if err != nil {
		t.Fatalf("advance rationale->implementing: %v", err)
	}
	if advanced.Status != model.SpecStatusImplementing {
		t.Errorf("Status: got %q, want implementing", advanced.Status)
	}

	// implementing → audit
	advanced, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "orch"})
	if err != nil {
		t.Fatalf("advance implementing->audit: %v", err)
	}
	if advanced.Status != model.SpecStatusAudit {
		t.Errorf("Status: got %q, want audit", advanced.Status)
	}
}

// TestLaneReclassify_TrivialToStandard verifies that a trivial spec in draft
// can be reclassified to standard and moves to speccing.
func TestLaneReclassify_TrivialToStandard(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
		Title: "Will become standard",
		Lane:  model.LaneTrivial,
		Scope: "internal/model/*.go",
	})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	reclassified, err := svc.LaneReclassify(ctx, model.LaneReclassifyRequest{
		ID:   spec.ID,
		Lane: model.LaneStandard,
		By:   "orchestrator",
	})
	if err != nil {
		t.Fatalf("LaneReclassify: %v", err)
	}
	if reclassified.Lane != model.LaneStandard {
		t.Errorf("Lane: got %q, want standard", reclassified.Lane)
	}
	if reclassified.Status != model.SpecStatusSpeccing {
		t.Errorf("Status: got %q, want speccing", reclassified.Status)
	}
}

// TestLaneOverride_RequiresReason verifies that LaneOverride returns ErrReasonRequired
// when no reason is provided.
func TestLaneOverride_RequiresReason(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	_, err := svc.LaneOverride(ctx, model.LaneOverrideRequest{ID: "SPEC-001"})
	if !errors.Is(err, model.ErrReasonRequired) {
		t.Errorf("expected ErrReasonRequired, got %v", err)
	}
}

// TestLaneStatus_AfterFailedAudit verifies that LaneStatus returns the correct
// breaches from the most recent audit failure recorded in the lane_audits table.
// Updated for SPEC-036: uses InsertLaneAudit instead of the removed
// InsertSpecHistoryEntry hack; breaches are stored newline-joined.
func TestLaneStatus_AfterFailedAudit(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	// Create a trivial-lane spec.
	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
		Title: "Regression: LaneStatus after failed audit",
		Lane:  model.LaneTrivial,
		Scope: "internal/model/*.go",
	})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	// Advance: draft → rationale → implementing.
	spec, err = svc.SpecQuick(ctx, model.SpecQuickRequest{
		ID:        spec.ID,
		Rationale: "Tiny fix",
		By:        "orchestrator",
	})
	if err != nil {
		t.Fatalf("SpecQuick: %v", err)
	}

	// Advance: implementing → audit.
	spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{
		ID:     spec.ID,
		By:     "backend",
		Reason: "implementation done",
	})
	if err != nil {
		t.Fatalf("SpecAdvance (implementing→audit): %v", err)
	}
	if spec.Status != model.SpecStatusAudit {
		t.Fatalf("expected audit status, got %s", spec.Status)
	}

	// Insert a structured audit failure into lane_audits (SPEC-036 approach).
	// Breaches are stored newline-joined, not "; "-joined.
	breaches := []string{"file count 5 exceeds trivial limit of 3", "line count 42 exceeds trivial limit of 20"}
	auditRec := &model.LaneAuditRecord{
		SpecID:       spec.ID,
		Passed:       false,
		FileCount:    5,
		LinesChanged: 42,
		Breaches:     strings.Join(breaches, "\n"),
		BaseSHA:      "test-sha",
	}
	if err := svc.store.InsertLaneAudit(ctx, auditRec); err != nil {
		t.Fatalf("InsertLaneAudit: %v", err)
	}

	// LaneStatus must reflect the AUDIT FAILURE (not the implementing→audit reason).
	status, err := svc.LaneStatus(ctx, spec.ID)
	if err != nil {
		t.Fatalf("LaneStatus: %v", err)
	}

	if status.LatestAudit == nil {
		t.Fatal("expected LatestAudit to be non-nil after a recorded audit failure")
	}
	if status.LatestAudit.Passed {
		t.Error("expected LatestAudit.Passed=false")
	}
	if len(status.LatestAudit.Breaches) == 0 {
		t.Error("expected non-empty Breaches in LatestAudit")
	}
	// Verify the specific breach messages are preserved correctly.
	for _, want := range breaches {
		found := false
		for _, got := range status.LatestAudit.Breaches {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("breach %q not found in LatestAudit.Breaches %v", want, status.LatestAudit.Breaches)
		}
	}
}

// TestLaneStatus_NoAuditRun verifies that LaneStatus returns nil LatestAudit when
// the spec has only been transitioned to audit status but no audit has run.
// This ensures we don't accidentally report the implementing→audit reason as breaches.
func TestLaneStatus_NoAuditRun(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
		Title: "No audit run yet",
		Lane:  model.LaneTrivial,
		Scope: "internal/model/*.go",
	})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	spec, err = svc.SpecQuick(ctx, model.SpecQuickRequest{
		ID:        spec.ID,
		Rationale: "Small change",
		By:        "orchestrator",
	})
	if err != nil {
		t.Fatalf("SpecQuick: %v", err)
	}

	spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{
		ID:     spec.ID,
		By:     "backend",
		Reason: "done implementing",
	})
	if err != nil {
		t.Fatalf("SpecAdvance (implementing→audit): %v", err)
	}

	// No audit has been run yet. LaneStatus must NOT report the
	// implementing→audit transition as an audit result.
	status, err := svc.LaneStatus(ctx, spec.ID)
	if err != nil {
		t.Fatalf("LaneStatus: %v", err)
	}

	if status.LatestAudit != nil {
		t.Errorf("expected LatestAudit=nil before any audit has run, got %+v", status.LatestAudit)
	}
}

// newTestGitRepo initialises a minimal git repository in a temp dir and returns
// the directory path. It creates an initial commit so HEAD is valid.
func newTestGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}

	dir := t.TempDir()

	gitRun := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	gitRun("init", "-b", "main")
	gitRun("config", "user.email", "test@test.com")
	gitRun("config", "user.name", "Test")

	fPath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(fPath, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	gitRun("add", ".")
	gitRun("commit", "-m", "initial")

	return dir
}

// TestSpecReject_StandardQAToImplementing verifies that a standard-lane spec in
// qa status can be rejected back to implementing.
func TestSpecReject_StandardQAToImplementing(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
		Title: "Standard reject test",
		Lane:  model.LaneStandard,
	})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	// Advance to qa: draft→speccing→specced→planning→planned→implementing→qa (6 steps).
	for _, by := range []string{"orch", "arch", "arch", "arch", "backend", "backend"} {
		spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: by})
		if err != nil {
			t.Fatalf("SpecAdvance (to qa, status=%s): %v", spec.Status, err)
		}
	}
	if spec.Status != model.SpecStatusQA {
		t.Fatalf("expected qa status, got %s", spec.Status)
	}

	rejected, err := svc.SpecReject(ctx, model.SpecRejectRequest{
		ID:     spec.ID,
		Reason: "tests fail on edge case",
		By:     "qa-agent",
	})
	if err != nil {
		t.Fatalf("SpecReject: %v", err)
	}
	if rejected.Status != model.SpecStatusImplementing {
		t.Errorf("expected implementing, got %s", rejected.Status)
	}
}

// TestSpecReject_TrivialAuditToImplementing verifies that a trivial-lane spec
// in audit status can be rejected back to implementing.
func TestSpecReject_TrivialAuditToImplementing(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
		Title: "Trivial reject test",
		Lane:  model.LaneTrivial,
		Scope: "internal/model/*.go",
	})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	spec, err = svc.SpecQuick(ctx, model.SpecQuickRequest{
		ID:        spec.ID,
		Rationale: "One-line fix",
		By:        "orchestrator",
	})
	if err != nil {
		t.Fatalf("SpecQuick: %v", err)
	}

	// implementing → audit.
	spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "backend"})
	if err != nil {
		t.Fatalf("SpecAdvance to audit: %v", err)
	}
	if spec.Status != model.SpecStatusAudit {
		t.Fatalf("expected audit, got %s", spec.Status)
	}

	rejected, err := svc.SpecReject(ctx, model.SpecRejectRequest{
		ID:     spec.ID,
		Reason: "scope exceeded",
		By:     "orchestrator",
	})
	if err != nil {
		t.Fatalf("SpecReject (trivial audit): %v", err)
	}
	if rejected.Status != model.SpecStatusImplementing {
		t.Errorf("expected implementing, got %s", rejected.Status)
	}
}

// TestSpecReject_EmptyReasonReturnsError verifies that SpecReject returns
// ErrReasonRequired when Reason is empty.
func TestSpecReject_EmptyReasonReturnsError(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	_, err := svc.SpecReject(ctx, model.SpecRejectRequest{
		ID:     "SPEC-001",
		Reason: "",
		By:     "orchestrator",
	})
	if !errors.Is(err, model.ErrReasonRequired) {
		t.Errorf("expected ErrReasonRequired, got %v", err)
	}
}

// TestSpecReject_InvalidStatusReturnsError verifies that SpecReject returns
// ErrInvalidTransition when the spec is in a status that does not allow
// a backward transition to implementing (e.g. draft).
func TestSpecReject_InvalidStatusReturnsError(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
		Title: "Reject invalid",
		Lane:  model.LaneStandard,
	})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	// Spec is in draft — cannot reject from draft.
	_, err = svc.SpecReject(ctx, model.SpecRejectRequest{
		ID:     spec.ID,
		Reason: "bad status",
		By:     "orchestrator",
	})
	if !errors.Is(err, model.ErrInvalidTransition) {
		t.Errorf("expected ErrInvalidTransition, got %v", err)
	}
}

// TestCaptureBaseSHA_ImplementingViaSpecAdvance verifies that when a standard
// spec enters implementing via SpecAdvance in a valid git repo, base_sha is set.
func TestCaptureBaseSHA_ImplementingViaSpecAdvance(t *testing.T) {
	repoDir := newTestGitRepo(t)

	svc := newTestSDDService(t, "project")
	svc.WithRepoDir(repoDir)
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
		Title: "SHA via advance",
		Lane:  model.LaneStandard,
	})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	// Advance to implementing: draft→speccing→specced→planning→planned→implementing (5 steps).
	for i := range 5 {
		spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "test"})
		if err != nil {
			t.Fatalf("SpecAdvance %d: %v", i, err)
		}
	}
	if spec.Status != model.SpecStatusImplementing {
		t.Fatalf("expected implementing, got %s", spec.Status)
	}

	// Reload to pick up the async captureBaseSHA write.
	got, err := svc.store.GetSpec(ctx, spec.ID)
	if err != nil {
		t.Fatalf("GetSpec: %v", err)
	}
	if got.BaseSHA == "" {
		t.Error("expected base_sha to be set after entering implementing, got empty string")
	}
	if len(got.BaseSHA) != 40 {
		t.Errorf("expected 40-char SHA, got %q", got.BaseSHA)
	}
}

// TestCaptureBaseSHA_ImplementingViaSpecQuick verifies that a trivial spec
// entering implementing via SpecQuick also captures base_sha.
func TestCaptureBaseSHA_ImplementingViaSpecQuick(t *testing.T) {
	repoDir := newTestGitRepo(t)

	svc := newTestSDDService(t, "project")
	svc.WithRepoDir(repoDir)
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
		Title: "SHA via quick",
		Lane:  model.LaneTrivial,
		Scope: "internal/model/*.go",
	})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	spec, err = svc.SpecQuick(ctx, model.SpecQuickRequest{
		ID:        spec.ID,
		Rationale: "Tiny fix",
		By:        "orchestrator",
	})
	if err != nil {
		t.Fatalf("SpecQuick: %v", err)
	}
	if spec.Status != model.SpecStatusImplementing {
		t.Fatalf("expected implementing, got %s", spec.Status)
	}

	got, err := svc.store.GetSpec(ctx, spec.ID)
	if err != nil {
		t.Fatalf("GetSpec: %v", err)
	}
	if got.BaseSHA == "" {
		t.Error("expected base_sha to be set after spec_quick, got empty string")
	}
}

// TestCaptureBaseSHA_NoGitNoBlock verifies that when repoDir is not a git
// repository, base_sha stays "" but the transition completes without error.
func TestCaptureBaseSHA_NoGitNoBlock(t *testing.T) {
	svc := newTestSDDService(t, "project")
	svc.WithRepoDir(t.TempDir()) // not a git repo
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
		Title: "No git, no block",
		Lane:  model.LaneTrivial,
		Scope: "internal/**",
	})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	spec, err = svc.SpecQuick(ctx, model.SpecQuickRequest{
		ID:        spec.ID,
		Rationale: "Should not fail",
		By:        "orchestrator",
	})
	if err != nil {
		t.Fatalf("SpecQuick must not fail when git is absent: %v", err)
	}
	if spec.Status != model.SpecStatusImplementing {
		t.Errorf("expected implementing, got %s", spec.Status)
	}

	got, err := svc.store.GetSpec(ctx, spec.ID)
	if err != nil {
		t.Fatalf("GetSpec: %v", err)
	}
	// base_sha should be empty — git failed silently.
	if got.BaseSHA != "" {
		t.Errorf("expected base_sha='', got %q", got.BaseSHA)
	}
}

// TestLaneStatus_RejectionCount verifies that RejectionCount reflects the
// number of qa/audit → implementing backward transitions in spec_history.
func TestLaneStatus_RejectionCount(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
		Title: "Rejection count test",
		Lane:  model.LaneTrivial,
		Scope: "internal/**",
	})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	spec, err = svc.SpecQuick(ctx, model.SpecQuickRequest{
		ID:        spec.ID,
		Rationale: "Small fix",
		By:        "orchestrator",
	})
	if err != nil {
		t.Fatalf("SpecQuick: %v", err)
	}

	// implementing → audit.
	spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "backend"})
	if err != nil {
		t.Fatalf("SpecAdvance to audit: %v", err)
	}

	// First rejection: audit → implementing.
	spec, err = svc.SpecReject(ctx, model.SpecRejectRequest{
		ID:     spec.ID,
		Reason: "first failure",
		By:     "orchestrator",
	})
	if err != nil {
		t.Fatalf("first SpecReject: %v", err)
	}

	// implementing → audit again.
	spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "backend"})
	if err != nil {
		t.Fatalf("SpecAdvance to audit (2nd): %v", err)
	}

	// Second rejection: audit → implementing.
	_, err = svc.SpecReject(ctx, model.SpecRejectRequest{
		ID:     spec.ID,
		Reason: "second failure",
		By:     "orchestrator",
	})
	if err != nil {
		t.Fatalf("second SpecReject: %v", err)
	}

	status, err := svc.LaneStatus(ctx, spec.ID)
	if err != nil {
		t.Fatalf("LaneStatus: %v", err)
	}
	if status.RejectionCount != 2 {
		t.Errorf("RejectionCount: got %d, want 2", status.RejectionCount)
	}
}

// TestLaneStats_Counts verifies that LaneStats returns correct counts for
// trivial specs, audit failures, overrides, and reclassifications.
func TestLaneStats_Counts(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	// Create 2 trivial specs.
	for i := range 2 {
		spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
			Title: "Trivial " + strings.Repeat("x", i),
			Lane:  model.LaneTrivial,
			Scope: "internal/**",
		})
		if err != nil {
			t.Fatalf("SpecNew trivial %d: %v", i, err)
		}

		spec, err = svc.SpecQuick(ctx, model.SpecQuickRequest{
			ID:        spec.ID,
			Rationale: "quick",
			By:        "orchestrator",
		})
		if err != nil {
			t.Fatalf("SpecQuick %d: %v", i, err)
		}

		// Advance to audit.
		spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "backend"})
		if err != nil {
			t.Fatalf("SpecAdvance to audit %d: %v", i, err)
		}

		// Insert a failing audit record for the first spec only.
		if i == 0 {
			if err := svc.store.InsertLaneAudit(ctx, &model.LaneAuditRecord{
				SpecID:       spec.ID,
				Passed:       false,
				FileCount:    6,
				LinesChanged: 50,
				Breaches:     "too many files",
				BaseSHA:      "sha0",
			}); err != nil {
				t.Fatalf("InsertLaneAudit: %v", err)
			}
		}
	}

	// Create 1 standard spec (should not count in trivial stats).
	_, err := svc.SpecNew(ctx, model.SpecNewRequest{
		Title: "Standard spec",
		Lane:  model.LaneStandard,
	})
	if err != nil {
		t.Fatalf("SpecNew standard: %v", err)
	}

	stats, err := svc.LaneStats(ctx, "project")
	if err != nil {
		t.Fatalf("LaneStats: %v", err)
	}

	if stats.TrivialCount != 2 {
		t.Errorf("TrivialCount: got %d, want 2", stats.TrivialCount)
	}
	if stats.AuditFailCount != 1 {
		t.Errorf("AuditFailCount: got %d, want 1", stats.AuditFailCount)
	}
	if stats.AuditFailRate == 0 {
		t.Error("AuditFailRate must be > 0")
	}
}
