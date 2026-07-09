package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
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

// TestSave_SharedAuthorDefaults_Inert verifies that Save leaves shared/author
// at their inert zero values (0, "") even for an auto-shareable type like
// TypeDecision. This is a regression guard for SPEC-061 SS-A: the write-through
// resolution logic that bakes shared/author from team-memory state is SS-B
// scope and must not exist yet — Save's model.Memory{} literal must not set
// either field.
func TestSave_SharedAuthorDefaults_Inert(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.Save(ctx, model.SaveRequest{
		Title:   "A decision worth sharing",
		Content: "Some content",
		Type:    model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save: unexpected error: %v", err)
	}

	mem, err := svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Get: unexpected error: %v", err)
	}
	if mem.Shared != 0 {
		t.Errorf("expected Shared=0 (inert, SS-B not wired), got %d", mem.Shared)
	}
	if mem.Author != "" {
		t.Errorf("expected Author=%q (inert, SS-B not wired), got %q", "", mem.Author)
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

// TestService_Save_RuleValidation verifies the full truth table from SPEC-001 §6.
func TestService_Save_RuleValidation(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	tests := []struct {
		name      string
		req       model.SaveRequest
		wantErr   error
		wantOK    bool
	}{
		{
			name: "rule with valid applies_to and severity",
			req: model.SaveRequest{
				Title:     "No time.Now",
				Content:   "Use injected clock.",
				Type:      model.TypeRule,
				AppliesTo: []string{"internal/**/*.go"},
				Severity:  model.SeverityWarn,
			},
			wantOK: true,
		},
		{
			name: "rule with omitted severity defaults to warn",
			req: model.SaveRequest{
				Title:     "No time.Now default severity",
				Content:   "Use injected clock.",
				Type:      model.TypeRule,
				AppliesTo: []string{"internal/**/*.go"},
				// Severity omitted
			},
			wantOK: true,
		},
		{
			name: "rule with block severity",
			req: model.SaveRequest{
				Title:     "No vendor edits block",
				Content:   "Use go mod vendor.",
				Type:      model.TypeRule,
				AppliesTo: []string{"vendor/**"},
				Severity:  model.SeverityBlock,
			},
			wantOK: true,
		},
		{
			name: "rule with info severity",
			req: model.SaveRequest{
				Title:     "Prefer server components info",
				Content:   "Advisory rule.",
				Type:      model.TypeRule,
				AppliesTo: []string{"**/*.tsx"},
				Severity:  model.SeverityInfo,
			},
			wantOK: true,
		},
		{
			name: "rule with invalid severity",
			req: model.SaveRequest{
				Title:     "Bad severity",
				Content:   "Content.",
				Type:      model.TypeRule,
				AppliesTo: []string{"**"},
				Severity:  "critical",
			},
			wantErr: model.ErrInvalidSeverity,
		},
		{
			name: "rule with empty applies_to slice",
			req: model.SaveRequest{
				Title:     "No applies_to",
				Content:   "Content.",
				Type:      model.TypeRule,
				AppliesTo: []string{},
				Severity:  model.SeverityWarn,
			},
			wantErr: model.ErrAppliesToRequired,
		},
		{
			name: "rule with nil applies_to",
			req: model.SaveRequest{
				Title:    "Nil applies_to",
				Content:  "Content.",
				Type:     model.TypeRule,
				Severity: model.SeverityWarn,
			},
			wantErr: model.ErrAppliesToRequired,
		},
		{
			name: "rule with empty string in applies_to",
			req: model.SaveRequest{
				Title:     "Empty pattern",
				Content:   "Content.",
				Type:      model.TypeRule,
				AppliesTo: []string{"", "internal/**"},
				Severity:  model.SeverityWarn,
			},
			wantErr: model.ErrEmptyPattern,
		},
		{
			name: "architecture with applies_to forbidden",
			req: model.SaveRequest{
				Title:     "Architecture with applies_to",
				Content:   "Content.",
				Type:      model.TypeArchitecture,
				AppliesTo: []string{"internal/**"},
			},
			wantErr: model.ErrAppliesToForbidden,
		},
		{
			name: "decision with applies_to forbidden",
			req: model.SaveRequest{
				Title:     "Decision with applies_to",
				Content:   "Content.",
				Type:      model.TypeDecision,
				AppliesTo: []string{"x"},
			},
			wantErr: model.ErrAppliesToForbidden,
		},
		{
			name: "discovery ignores severity gracefully",
			req: model.SaveRequest{
				Title:    "Discovery with severity",
				Content:  "Content.",
				Type:     model.TypeDiscovery,
				Severity: model.SeverityWarn,
				// No applies_to — should succeed; severity is ignored
			},
			wantOK: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			resp, err := svc.Save(ctx, tc.req)
			if tc.wantErr != nil {
				if err == nil {
					t.Fatalf("expected error %v, got nil", tc.wantErr)
				}
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("expected error %v, got %v", tc.wantErr, err)
				}
				return
			}
			if tc.wantOK && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp == nil {
				t.Fatal("expected non-nil response")
			}
		})
	}
}

// TestService_Save_RuleDefaults verifies that a rule saved with minimal params
// gets importance=0.95, decay_rate=0, and severity=warn applied.
func TestService_Save_RuleDefaults(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.Save(ctx, model.SaveRequest{
		Title:     "Minimal rule",
		Content:   "Rule content.",
		Type:      model.TypeRule,
		AppliesTo: []string{"**"},
		// Importance and Severity intentionally omitted
	})
	if err != nil {
		t.Fatalf("Save rule: %v", err)
	}

	mem, err := svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if mem.Importance != 0.95 {
		t.Errorf("Importance = %v, want 0.95", mem.Importance)
	}
	if mem.DecayRate != 0.0 {
		t.Errorf("DecayRate = %v, want 0.0", mem.DecayRate)
	}
	if mem.Severity != model.SeverityWarn {
		t.Errorf("Severity = %q, want %q", mem.Severity, model.SeverityWarn)
	}
}

// saveRuleScoped is a test helper that persists a rule with explicit scope.
func saveRuleScoped(t *testing.T, svc *service.MemoryService, title string, severity model.Severity, scope model.Scope) string {
	t.Helper()
	resp, err := svc.Save(context.Background(), model.SaveRequest{
		Title:     title,
		Content:   title + " content.",
		Type:      model.TypeRule,
		AppliesTo: []string{"**/*.go"},
		Severity:  severity,
		Scope:     scope,
	})
	if err != nil {
		t.Fatalf("saveRuleScoped %q: %v", title, err)
	}
	return resp.ID
}

func TestListRules_AllScopes(t *testing.T) {
	svc := newTestService(t)

	saveRuleScoped(t, svc, "Project block rule", model.SeverityBlock, model.ScopeProject)
	saveRuleScoped(t, svc, "Project warn rule", model.SeverityWarn, model.ScopeProject)
	saveRuleScoped(t, svc, "Global info rule", model.SeverityInfo, model.ScopeGlobal)

	rules, err := svc.ListRules(context.Background(), service.ListRulesOptions{})
	if err != nil {
		t.Fatalf("ListRules: %v", err)
	}
	if len(rules) != 3 {
		t.Errorf("got %d rules, want 3", len(rules))
	}
}

func TestListRules_ProjectOnly(t *testing.T) {
	svc := newTestService(t)

	saveRuleScoped(t, svc, "Project rule", model.SeverityWarn, model.ScopeProject)
	saveRuleScoped(t, svc, "Global rule", model.SeverityInfo, model.ScopeGlobal)

	rules, err := svc.ListRules(context.Background(), service.ListRulesOptions{Scope: "project"})
	if err != nil {
		t.Fatalf("ListRules: %v", err)
	}
	if len(rules) != 1 {
		t.Errorf("got %d rules, want 1", len(rules))
	}
	if rules[0].Title != "Project rule" {
		t.Errorf("got title %q, want %q", rules[0].Title, "Project rule")
	}
}

func TestListRules_GlobalOnly(t *testing.T) {
	svc := newTestService(t)

	saveRuleScoped(t, svc, "Project rule", model.SeverityWarn, model.ScopeProject)
	saveRuleScoped(t, svc, "Global rule", model.SeverityInfo, model.ScopeGlobal)

	rules, err := svc.ListRules(context.Background(), service.ListRulesOptions{Scope: "global"})
	if err != nil {
		t.Fatalf("ListRules: %v", err)
	}
	if len(rules) != 1 {
		t.Errorf("got %d rules, want 1", len(rules))
	}
	if rules[0].Title != "Global rule" {
		t.Errorf("got title %q, want %q", rules[0].Title, "Global rule")
	}
}

func TestListRules_FilterBySeverity(t *testing.T) {
	svc := newTestService(t)

	saveRuleScoped(t, svc, "Block rule", model.SeverityBlock, model.ScopeProject)
	saveRuleScoped(t, svc, "Warn rule", model.SeverityWarn, model.ScopeProject)
	saveRuleScoped(t, svc, "Info rule", model.SeverityInfo, model.ScopeProject)

	rules, err := svc.ListRules(context.Background(), service.ListRulesOptions{Severity: model.SeverityBlock})
	if err != nil {
		t.Fatalf("ListRules: %v", err)
	}
	if len(rules) != 1 {
		t.Errorf("got %d rules, want 1", len(rules))
	}
	if rules[0].Severity != model.SeverityBlock {
		t.Errorf("severity = %q, want block", rules[0].Severity)
	}
}

func TestListRules_SortOrder(t *testing.T) {
	svc := newTestService(t)

	// Insert in reverse order of expected output.
	saveRuleScoped(t, svc, "Info rule", model.SeverityInfo, model.ScopeProject)
	saveRuleScoped(t, svc, "Block rule", model.SeverityBlock, model.ScopeProject)
	saveRuleScoped(t, svc, "Warn rule", model.SeverityWarn, model.ScopeProject)

	rules, err := svc.ListRules(context.Background(), service.ListRulesOptions{})
	if err != nil {
		t.Fatalf("ListRules: %v", err)
	}
	if len(rules) != 3 {
		t.Fatalf("got %d rules, want 3", len(rules))
	}
	if rules[0].Severity != model.SeverityBlock {
		t.Errorf("[0] severity = %q, want block", rules[0].Severity)
	}
	if rules[1].Severity != model.SeverityWarn {
		t.Errorf("[1] severity = %q, want warn", rules[1].Severity)
	}
	if rules[2].Severity != model.SeverityInfo {
		t.Errorf("[2] severity = %q, want info", rules[2].Severity)
	}
}

func TestListRules_EmptyDB(t *testing.T) {
	svc := newTestService(t)

	rules, err := svc.ListRules(context.Background(), service.ListRulesOptions{})
	if err != nil {
		t.Fatalf("ListRules: unexpected error: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("got %d rules, want 0", len(rules))
	}
}

func TestListRules_Limit(t *testing.T) {
	svc := newTestService(t)

	for i := 0; i < 5; i++ {
		saveRuleScoped(t, svc, "Rule", model.SeverityWarn, model.ScopeProject)
	}

	rules, err := svc.ListRules(context.Background(), service.ListRulesOptions{Limit: 3})
	if err != nil {
		t.Fatalf("ListRules: %v", err)
	}
	if len(rules) != 3 {
		t.Errorf("got %d rules, want 3", len(rules))
	}
}
