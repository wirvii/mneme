package service_test

import (
	"context"
	"testing"

	"github.com/wirvii/mneme/internal/model"
)

// TestService_Start_InitializesWorkerPool verifies that Start can be called
// without panicking and that the service remains functional afterwards.
func TestService_Start_InitializesWorkerPool(t *testing.T) {
	svc := newTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Should not panic.
	svc.Start(ctx)

	// Service must still work after Start.
	resp, err := svc.Save(ctx, model.SaveRequest{
		Title:   "test memory",
		Content: "content",
		Type:    model.TypeDiscovery,
		Scope:   model.ScopeProject,
	})
	if err != nil {
		t.Fatalf("Save after Start: %v", err)
	}
	if resp.ID == "" {
		t.Error("expected non-empty ID after Save")
	}
}

// TestService_DrainHebbian_DoesNotPanic verifies that DrainHebbian can be
// called safely even when no events have been enqueued.
func TestService_DrainHebbian_DoesNotPanic(t *testing.T) {
	svc := newTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc.Start(ctx)
	// Should not block or panic when no events are pending.
	svc.DrainHebbian()
}

// TestService_Get_RuleNotTracked verifies that retrieving a rule-type memory
// does not cause a panic or error (the tracker silently ignores rules).
func TestService_Get_RuleNotTracked(t *testing.T) {
	svc := newTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.Start(ctx)

	// Save a rule memory.
	resp, err := svc.Save(ctx, model.SaveRequest{
		Title:     "test rule",
		Content:   "do not do X",
		Type:      model.TypeRule,
		Scope:     model.ScopeProject,
		AppliesTo: []string{"**"},
		Severity:  model.SeverityWarn,
	})
	if err != nil {
		t.Fatalf("Save rule: %v", err)
	}

	// Get should succeed without error — tracker silently ignores the rule type.
	got, err := svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Get rule: %v", err)
	}
	if got.Type != model.TypeRule {
		t.Errorf("type = %s, want rule", got.Type)
	}
}

// TestService_Get_TriggersTracker verifies that Get does not panic when the
// Hebbian tracker is active and a regular memory is retrieved.
func TestService_Get_TriggersTracker(t *testing.T) {
	svc := newTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.Start(ctx)

	resp, err := svc.Save(ctx, model.SaveRequest{
		Title:   "architecture decision",
		Content: "use hexagonal architecture",
		Type:    model.TypeDecision,
		Scope:   model.ScopeProject,
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Get should succeed and internally call tracker.Record.
	got, err := svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != resp.ID {
		t.Errorf("ID = %s, want %s", got.ID, resp.ID)
	}
}

// TestService_Search_TriggersTracker verifies that Search does not panic when
// the Hebbian tracker is active.
func TestService_Search_TriggersTracker(t *testing.T) {
	svc := newTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.Start(ctx)

	// Save a couple of memories to search.
	for _, title := range []string{"auth service decision", "jwt library usage"} {
		if _, err := svc.Save(ctx, model.SaveRequest{
			Title:   title,
			Content: title + " content",
			Type:    model.TypeDecision,
			Scope:   model.ScopeProject,
		}); err != nil {
			t.Fatalf("Save %q: %v", title, err)
		}
	}

	// Search should not panic; tracker records top-3 results.
	resp, err := svc.Search(ctx, model.SearchRequest{
		Query: "auth",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil SearchResponse")
	}
}
