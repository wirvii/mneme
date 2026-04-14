package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/db"
	"github.com/juanftp/mneme/internal/embed"
	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/service"
	"github.com/juanftp/mneme/internal/store"
)

func TestSave_Success(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.Save(ctx, model.SaveRequest{
		Title:   "Auth uses JWT",
		Content: "All API endpoints require a signed JWT in the Authorization header.",
		Type:    model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save: unexpected error: %v", err)
	}
	if resp.ID == "" {
		t.Error("expected non-empty ID")
	}
	if resp.Action != "created" {
		t.Errorf("expected action=created, got %q", resp.Action)
	}
	if resp.Title != "Auth uses JWT" {
		t.Errorf("expected title echoed back, got %q", resp.Title)
	}
}

func TestSave_Defaults(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.Save(ctx, model.SaveRequest{
		Title:   "Some title",
		Content: "Some content",
		// Type and Scope intentionally omitted
	})
	if err != nil {
		t.Fatalf("Save: unexpected error: %v", err)
	}

	mem, err := svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Get: unexpected error: %v", err)
	}
	if mem.Type != model.TypeDiscovery {
		t.Errorf("expected default type=discovery, got %q", mem.Type)
	}
	if mem.Scope != model.ScopeProject {
		t.Errorf("expected default scope=project, got %q", mem.Scope)
	}
}

func TestSave_Validation(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	tests := []struct {
		name    string
		req     model.SaveRequest
		wantErr error
	}{
		{
			name:    "empty title",
			req:     model.SaveRequest{Content: "content"},
			wantErr: model.ErrTitleRequired,
		},
		{
			name:    "empty content",
			req:     model.SaveRequest{Title: "title"},
			wantErr: model.ErrContentRequired,
		},
		{
			name: "invalid type",
			req: model.SaveRequest{
				Title:   "title",
				Content: "content",
				Type:    model.MemoryType("invalid"),
			},
			wantErr: model.ErrInvalidType,
		},
		{
			name: "invalid scope",
			req: model.SaveRequest{
				Title:   "title",
				Content: "content",
				Scope:   model.Scope("invalid"),
			},
			wantErr: model.ErrInvalidScope,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Save(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("expected error %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestSave_Upsert(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	req := model.SaveRequest{
		Title:    "Original title",
		Content:  "Original content",
		TopicKey: "discovery/auth-model",
	}

	resp1, err := svc.Save(ctx, req)
	if err != nil {
		t.Fatalf("first Save: %v", err)
	}
	if resp1.Action != "created" {
		t.Errorf("expected first save action=created, got %q", resp1.Action)
	}

	req.Title = "Updated title"
	req.Content = "Updated content"

	resp2, err := svc.Save(ctx, req)
	if err != nil {
		t.Fatalf("second Save: %v", err)
	}
	if resp2.Action != "updated" {
		t.Errorf("expected second save action=updated, got %q", resp2.Action)
	}
	if resp2.RevisionCount < 1 {
		t.Errorf("expected revision_count >= 1, got %d", resp2.RevisionCount)
	}
	// Both saves should resolve to the same underlying record.
	if resp1.ID != resp2.ID {
		t.Errorf("expected same ID on upsert, got %q and %q", resp1.ID, resp2.ID)
	}
}

func TestGet_Success(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	saved, err := svc.Save(ctx, model.SaveRequest{
		Title:   "Cache invalidation strategy",
		Content: "Use versioned cache keys prefixed by env.",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	mem, err := svc.Get(ctx, saved.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if mem.Title != "Cache invalidation strategy" {
		t.Errorf("unexpected title: %q", mem.Title)
	}

	// The first Get triggers IncrementAccess. A second Get reflects the updated
	// counter because IncrementAccess is applied before the second read.
	mem2, err := svc.Get(ctx, saved.ID)
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if mem2.AccessCount < 1 {
		t.Errorf("expected access_count >= 1 after Get, got %d", mem2.AccessCount)
	}
}

func TestGet_NotFound(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.Get(ctx, "00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, model.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestUpdate_Success(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	saved, err := svc.Save(ctx, model.SaveRequest{
		Title:   "Old title",
		Content: "Old content",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	newTitle := "New title"
	resp, err := svc.Update(ctx, saved.ID, model.UpdateRequest{
		Title: &newTitle,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if resp.Title != "New title" {
		t.Errorf("expected updated title, got %q", resp.Title)
	}
	if resp.Action != "updated" {
		t.Errorf("expected action=updated, got %q", resp.Action)
	}
}

func TestUpdate_NotFound(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	title := "irrelevant"
	_, err := svc.Update(ctx, "00000000-0000-0000-0000-000000000000", model.UpdateRequest{
		Title: &title,
	})
	if !errors.Is(err, model.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// TestSave_GlobalScope_UsesGlobalStore is the regression test for the bug where
// scope=global memories were written to the project database instead of the
// dedicated global.db. It verifies:
//
//  1. A global memory is findable via Search with an explicit global scope filter.
//  2. A global memory appears in Context (mixed in from globalStore).
//  3. A global memory does NOT appear when searching with an explicit project scope.
func TestSave_GlobalScope_UsesGlobalStore(t *testing.T) {
	// Build a service with separate in-memory databases so we can assert that
	// the memory lands in globalStore and not in projectStore.
	projectDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open project db: %v", err)
	}
	globalDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open global db: %v", err)
	}
	t.Cleanup(func() { projectDB.Close(); globalDB.Close() })

	projectStore := store.NewMemoryStore(projectDB)
	globalStore := store.NewMemoryStore(globalDB)
	cfg := config.Default()
	cfg.Context.IncludeGlobal = true
	cfg.Context.GlobalMinImportance = 0.0 // include all global memories in context
	svc := service.NewMemoryService(projectStore, globalStore, cfg, "test/project", embed.NopEmbedder{})

	ctx := context.Background()

	// 1. Save a memory with scope=global.
	highImp := 0.9
	saveResp, err := svc.Save(ctx, model.SaveRequest{
		Title:      "universal coding convention",
		Content:    "Always write tests before shipping code to production.",
		Type:       model.TypeConvention,
		Scope:      model.ScopeGlobal,
		Importance: &highImp,
	})
	if err != nil {
		t.Fatalf("Save global: %v", err)
	}

	// 2. Findable via Search with explicit global scope filter.
	globalScope := model.ScopeGlobal
	searchResp, err := svc.Search(ctx, model.SearchRequest{
		Query: "universal coding convention tests",
		Scope: &globalScope,
	})
	if err != nil {
		t.Fatalf("Search global scope: %v", err)
	}
	found := false
	for _, r := range searchResp.Results {
		if r.ID == saveResp.ID {
			found = true
		}
	}
	if !found {
		t.Error("global memory not found when searching with scope=global")
	}

	// 3. Appears in Context (global store mixed in).
	ctxResp, err := svc.Context(ctx, model.ContextRequest{Project: "test/project"})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}
	foundInCtx := false
	for _, m := range ctxResp.Memories {
		if m.ID == saveResp.ID {
			foundInCtx = true
		}
	}
	if !foundInCtx {
		t.Error("global memory did not appear in project context")
	}

	// 4. Does NOT appear when filtering by project scope only.
	projectScope := model.ScopeProject
	projectSearchResp, err := svc.Search(ctx, model.SearchRequest{
		Query: "universal coding convention tests",
		Scope: &projectScope,
	})
	if err != nil {
		t.Fatalf("Search project scope: %v", err)
	}
	for _, r := range projectSearchResp.Results {
		if r.ID == saveResp.ID {
			t.Error("global memory incorrectly appeared in project-scope search")
		}
	}
}
