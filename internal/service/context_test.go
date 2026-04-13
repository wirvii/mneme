package service_test

import (
	"context"
	"testing"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/db"
	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/service"
	"github.com/juanftp/mneme/internal/store"
)

func TestContext_Basic(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// Save memories with different importance levels.
	imp09 := 0.9
	imp03 := 0.3

	_, err := svc.Save(ctx, model.SaveRequest{
		Title:      "High importance architecture decision",
		Content:    "The system uses event sourcing for audit trails.",
		Type:       model.TypeArchitecture,
		Importance: &imp09,
	})
	if err != nil {
		t.Fatalf("Save high importance: %v", err)
	}

	_, err = svc.Save(ctx, model.SaveRequest{
		Title:      "Low importance discovery",
		Content:    "The test suite takes 45 seconds to run.",
		Type:       model.TypeDiscovery,
		Importance: &imp03,
	})
	if err != nil {
		t.Fatalf("Save low importance: %v", err)
	}

	resp, err := svc.Context(ctx, model.ContextRequest{})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}

	if len(resp.Memories) == 0 {
		t.Fatal("expected at least one memory in context")
	}
	if resp.TotalAvailable < 2 {
		t.Errorf("expected TotalAvailable >= 2, got %d", resp.TotalAvailable)
	}
	// Higher importance (architecture) should come first.
	if resp.Memories[0].Type != model.TypeArchitecture {
		t.Errorf("expected architecture memory first, got type=%q", resp.Memories[0].Type)
	}
}

func TestContext_Budget(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// Save many memories with long content.
	for i := 0; i < 10; i++ {
		_, err := svc.Save(ctx, model.SaveRequest{
			Title:   "A lengthy memory title that takes tokens",
			Content: "This is a fairly long content body designed to consume a meaningful number of tokens when estimated using the rough chars-to-tokens formula.",
		})
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	// Request context with a very small budget (30 tokens).
	resp, err := svc.Context(ctx, model.ContextRequest{
		Budget: 30,
	})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}

	// TokenEstimate must not exceed the requested budget by a large margin.
	// The budget is a soft cap; we allow slight overage from the last packed item.
	if resp.TokenEstimate > 30+50 {
		t.Errorf("token estimate %d greatly exceeds budget 30", resp.TokenEstimate)
	}
	if resp.TotalAvailable < 10 {
		t.Errorf("expected TotalAvailable >= 10, got %d", resp.TotalAvailable)
	}
}

func TestContext_IncludesGlobal(t *testing.T) {
	// Use a separate service instance that shares the same DB so we can save
	// a global memory and verify it appears in a project context request.
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	s := store.NewMemoryStore(database)
	cfg := config.Default()
	cfg.Context.IncludeGlobal = true
	cfg.Context.GlobalMinImportance = 0.5
	svc := service.NewMemoryService(s, cfg, "test/project")

	ctx := context.Background()

	highImp := 0.9
	_, err = svc.Save(ctx, model.SaveRequest{
		Title:      "Global convention: never use globals",
		Content:    "All state must be passed explicitly; no global variables.",
		Type:       model.TypeConvention,
		Scope:      model.ScopeGlobal,
		Importance: &highImp,
	})
	if err != nil {
		t.Fatalf("Save global: %v", err)
	}

	resp, err := svc.Context(ctx, model.ContextRequest{
		Project: "test/project",
	})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}

	found := false
	for _, m := range resp.Memories {
		if m.Scope == model.ScopeGlobal {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected global memory to appear in project context")
	}
}

func TestContext_LastSession(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	sessResp, err := svc.SessionEnd(ctx, model.SessionEndRequest{
		Summary: "Implemented the authentication flow end-to-end.",
	})
	if err != nil {
		t.Fatalf("SessionEnd: %v", err)
	}
	if sessResp.SummaryMemoryID == "" {
		t.Fatal("expected SummaryMemoryID")
	}

	resp, err := svc.Context(ctx, model.ContextRequest{})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}

	if resp.LastSession == nil {
		t.Fatal("expected LastSession to be populated")
	}
	if resp.LastSession.Summary != "Implemented the authentication flow end-to-end." {
		t.Errorf("unexpected session summary: %q", resp.LastSession.Summary)
	}
}
