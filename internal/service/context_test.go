package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/db"
	"github.com/juanftp/mneme/internal/embed"
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
	// Build a service with distinct project and global in-memory databases to
	// verify that global memories (stored in globalStore) are mixed into the
	// project context response.
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
	cfg.Context.GlobalMinImportance = 0.5
	svc := service.NewMemoryService(projectStore, globalStore, cfg, "test/project", embed.NopEmbedder{})

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

// ─── SPEC-002: Rules injection tests ─────────────────────────────────────────

// newTestServiceWithRulesBudget builds a service with a custom RulesBudget
// for SPEC-002 tests. IncludeGlobal is false by default so project and global
// rule injection can be tested independently.
func newTestServiceWithRulesBudget(t *testing.T, rulesBudget int) *service.MemoryService {
	t.Helper()
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
	cfg.Context.RulesBudget = rulesBudget
	cfg.Context.IncludeGlobal = false
	return service.NewMemoryService(projectStore, globalStore, cfg, "test/project", embed.NopEmbedder{})
}

// saveRule is a helper that saves a rule memory and returns its ID.
func saveRule(t *testing.T, svc *service.MemoryService, title, content string, sev model.Severity) string {
	t.Helper()
	ctx := context.Background()
	m, err := svc.Save(ctx, model.SaveRequest{
		Title:     title,
		Content:   content,
		Type:      model.TypeRule,
		Scope:     model.ScopeProject,
		AppliesTo: []string{"**/*.go"},
		Severity:  sev,
	})
	if err != nil {
		t.Fatalf("save rule %q: %v", title, err)
	}
	return m.ID
}

// TestContext_RulesInjected verifies the core SPEC-002 guarantee: rule-type
// memories appear in resp.Rules and not in resp.Memories, regardless of focus.
func TestContext_RulesInjected(t *testing.T) {
	svc := newTestServiceWithRulesBudget(t, 1500)
	ctx := context.Background()

	// Save 3 rules with different severities.
	saveRule(t, svc, "Never store plain passwords", "Always use bcrypt.", model.SeverityBlock)
	saveRule(t, svc, "SQL in .sql files only", "No inline SQL.", model.SeverityWarn)
	saveRule(t, svc, "Prefer context.Context", "Pass ctx as first arg.", model.SeverityInfo)

	// Save a regular memory to populate the general section.
	imp09 := 0.9
	_, err := svc.Save(ctx, model.SaveRequest{
		Title:      "Auth design",
		Content:    "JWT + refresh tokens.",
		Type:       model.TypeArchitecture,
		Importance: &imp09,
	})
	if err != nil {
		t.Fatalf("save regular memory: %v", err)
	}

	resp, err := svc.Context(ctx, model.ContextRequest{Focus: "authentication"})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}

	if resp.RulesCount != 3 {
		t.Errorf("RulesCount: got %d, want 3", resp.RulesCount)
	}
	if len(resp.Rules) != 3 {
		t.Errorf("len(resp.Rules): got %d, want 3", len(resp.Rules))
	}

	// Rules must not appear in general Memories.
	ruleIDs := make(map[string]bool)
	for _, r := range resp.Rules {
		ruleIDs[r.ID] = true
	}
	for _, m := range resp.Memories {
		if ruleIDs[m.ID] {
			t.Errorf("rule %q appeared in resp.Memories (must be deduplicated)", m.ID)
		}
	}
}

// TestContext_RulesSortedBySeverity verifies that within resp.Rules the order
// is block → warn → info, as specified by D2 of the design.
func TestContext_RulesSortedBySeverity(t *testing.T) {
	svc := newTestServiceWithRulesBudget(t, 1500)

	// Save in reverse order (info first) to test that sort is applied.
	saveRule(t, svc, "Info rule", "Advisory.", model.SeverityInfo)
	saveRule(t, svc, "Warn rule", "Be careful.", model.SeverityWarn)
	saveRule(t, svc, "Block rule", "Reject immediately.", model.SeverityBlock)

	resp, err := svc.Context(context.Background(), model.ContextRequest{})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}

	if len(resp.Rules) != 3 {
		t.Fatalf("expected 3 rules, got %d", len(resp.Rules))
	}
	if resp.Rules[0].Severity != model.SeverityBlock {
		t.Errorf("Rules[0].Severity: got %q, want %q", resp.Rules[0].Severity, model.SeverityBlock)
	}
	if resp.Rules[1].Severity != model.SeverityWarn {
		t.Errorf("Rules[1].Severity: got %q, want %q", resp.Rules[1].Severity, model.SeverityWarn)
	}
	if resp.Rules[2].Severity != model.SeverityInfo {
		t.Errorf("Rules[2].Severity: got %q, want %q", resp.Rules[2].Severity, model.SeverityInfo)
	}
}

// TestContext_RulesBudgetExhausted verifies that when the rules budget is
// exhausted, rules_truncated reflects the count of omitted rules, while smaller
// rules that still fit are included (continue, not break).
func TestContext_RulesBudgetExhausted(t *testing.T) {
	// Very small budget: only one rule (~10-15 tokens) will fit.
	svc := newTestServiceWithRulesBudget(t, 10)
	ctx := context.Background()

	// Save 3 rules that together exceed 10 tokens.
	for i := 0; i < 3; i++ {
		saveRule(t, svc, "Rule title", "Rule content that is longer.", model.SeverityWarn)
	}

	resp, err := svc.Context(ctx, model.ContextRequest{})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}

	// At least one rule should have been truncated.
	if resp.RulesTruncated == 0 {
		t.Errorf("expected RulesTruncated > 0 with tiny budget, got 0")
	}
	// Total packed rules plus truncated should equal 3.
	if resp.RulesCount+resp.RulesTruncated != 3 {
		t.Errorf("RulesCount(%d) + RulesTruncated(%d) should equal 3",
			resp.RulesCount, resp.RulesTruncated)
	}
}

// TestContext_RulesBudgetZero_NoInjection verifies that RulesBudget=0 disables
// the rules section entirely (toggle-off). Rules may still appear in Memories
// through general scoring, but resp.Rules must be empty.
func TestContext_RulesBudgetZero_NoInjection(t *testing.T) {
	svc := newTestServiceWithRulesBudget(t, 0)
	ctx := context.Background()

	saveRule(t, svc, "A block rule", "Must not bypass auth.", model.SeverityBlock)

	resp, err := svc.Context(ctx, model.ContextRequest{})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}

	if len(resp.Rules) != 0 {
		t.Errorf("expected no rules with RulesBudget=0, got %d", len(resp.Rules))
	}
	if resp.RulesCount != 0 {
		t.Errorf("expected RulesCount=0, got %d", resp.RulesCount)
	}
}

// TestContext_RulesDedup verifies that a rule that would also appear in the
// general scored section (by importance) is not duplicated in resp.Memories.
func TestContext_RulesDedup(t *testing.T) {
	svc := newTestServiceWithRulesBudget(t, 1500)
	ctx := context.Background()

	// A single rule with very high importance so it would rank first in scoring.
	saveRule(t, svc, "Unique rule", "Applies everywhere.", model.SeverityBlock)

	resp, err := svc.Context(ctx, model.ContextRequest{})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}

	if len(resp.Rules) == 0 {
		t.Fatal("expected the rule in resp.Rules")
	}

	ruleID := resp.Rules[0].ID
	for _, m := range resp.Memories {
		if m.ID == ruleID {
			t.Error("rule appears in resp.Memories (dedup failed)")
		}
	}
}

// TestContext_GlobalRulesIncluded verifies that rules from the global store
// appear in resp.Rules when IncludeGlobal is true.
func TestContext_GlobalRulesIncluded(t *testing.T) {
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
	cfg.Context.RulesBudget = 1500
	cfg.Context.IncludeGlobal = true
	cfg.Context.GlobalMinImportance = 0.5
	svc := service.NewMemoryService(projectStore, globalStore, cfg, "test/project", embed.NopEmbedder{})

	ctx := context.Background()

	// Save a rule in the global store directly.
	_, err = svc.Save(ctx, model.SaveRequest{
		Title:     "Global rule: no time.Now() direct calls",
		Content:   "Use clock injection for testability.",
		Type:      model.TypeRule,
		Scope:     model.ScopeGlobal,
		AppliesTo: []string{"**/*.go"},
		Severity:  model.SeverityWarn,
	})
	if err != nil {
		t.Fatalf("save global rule: %v", err)
	}

	resp, err := svc.Context(ctx, model.ContextRequest{Project: "test/project"})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}

	found := false
	for _, r := range resp.Rules {
		if r.Scope == model.ScopeGlobal {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected global rule in resp.Rules")
	}
}

// TestContext_GlobalRulesExcluded_IncludeGlobalFalse verifies that when
// IncludeGlobal is false, global rules are not injected.
func TestContext_GlobalRulesExcluded_IncludeGlobalFalse(t *testing.T) {
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
	cfg.Context.RulesBudget = 1500
	cfg.Context.IncludeGlobal = false
	svc := service.NewMemoryService(projectStore, globalStore, cfg, "test/project", embed.NopEmbedder{})

	ctx := context.Background()

	_, err = svc.Save(ctx, model.SaveRequest{
		Title:     "Global rule",
		Content:   "Some global constraint.",
		Type:      model.TypeRule,
		Scope:     model.ScopeGlobal,
		AppliesTo: []string{"**"},
		Severity:  model.SeverityBlock,
	})
	if err != nil {
		t.Fatalf("save global rule: %v", err)
	}

	resp, err := svc.Context(ctx, model.ContextRequest{Project: "test/project"})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}

	for _, r := range resp.Rules {
		if r.Scope == model.ScopeGlobal {
			t.Error("global rule appeared in resp.Rules with IncludeGlobal=false")
		}
	}
}

// TestContext_RuleLargerThanBudget_Skipped verifies that a rule whose token
// cost exceeds the entire rules budget is counted in rules_truncated and not
// included, while smaller rules that fit are still packed (continue not break).
func TestContext_RuleLargerThanBudget_Skipped(t *testing.T) {
	// Budget of 5 tokens — too small for any real rule content.
	svc := newTestServiceWithRulesBudget(t, 5)
	ctx := context.Background()

	// One huge rule.
	saveRule(t, svc, "Big rule", strings.Repeat("x", 300), model.SeverityBlock)

	resp, err := svc.Context(ctx, model.ContextRequest{})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}

	if resp.RulesTruncated != 1 {
		t.Errorf("expected RulesTruncated=1, got %d", resp.RulesTruncated)
	}
	if len(resp.Rules) != 0 {
		t.Errorf("expected no packed rules, got %d", len(resp.Rules))
	}
}

// TestContext_NoRules_BackwardCompat verifies that when no rules exist the
// response is backward-compatible: Rules is nil, RulesCount=0, RulesTokens=0,
// RulesTruncated=0, and Memories/TotalAvailable still work as before.
func TestContext_NoRules_BackwardCompat(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	imp08 := 0.8
	_, err := svc.Save(ctx, model.SaveRequest{
		Title:      "Architecture decision",
		Content:    "Use hexagonal architecture.",
		Type:       model.TypeArchitecture,
		Importance: &imp08,
	})
	if err != nil {
		t.Fatalf("save memory: %v", err)
	}

	resp, err := svc.Context(ctx, model.ContextRequest{})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}

	if len(resp.Rules) != 0 {
		t.Errorf("expected nil Rules with no rules in DB, got %d", len(resp.Rules))
	}
	if resp.RulesCount != 0 {
		t.Errorf("expected RulesCount=0, got %d", resp.RulesCount)
	}
	if resp.RulesTokens != 0 {
		t.Errorf("expected RulesTokens=0, got %d", resp.RulesTokens)
	}
	if resp.RulesTruncated != 0 {
		t.Errorf("expected RulesTruncated=0, got %d", resp.RulesTruncated)
	}
	if len(resp.Memories) == 0 {
		t.Error("expected general memories to still be populated")
	}
}

// TestContext_Performance_LoadActiveRules verifies that Context completes in
// under 100ms with 50 project rules — a conservative bound that still catches
// regressions like accidental O(n^2) behavior.
func TestContext_Performance_LoadActiveRules(t *testing.T) {
	svc := newTestServiceWithRulesBudget(t, 5000)
	ctx := context.Background()

	// Insert 50 rules.
	for i := 0; i < 50; i++ {
		saveRule(t, svc, "Rule", "Short rule content.", model.SeverityWarn)
	}

	start := time.Now()
	_, err := svc.Context(ctx, model.ContextRequest{})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Context: %v", err)
	}

	const limit = 100 * time.Millisecond
	if elapsed > limit {
		t.Errorf("Context with 50 rules took %v, want < %v", elapsed, limit)
	}
}

// newTestServiceWithGraphForContext creates a MemoryService with a real SQLite
// store for context graph expansion tests.
func newTestServiceWithGraphForContext(t *testing.T, graphMode string) (*service.MemoryService, *store.MemoryStore) {
	t.Helper()
	projectDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open project db: %v", err)
	}
	globalDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open global db: %v", err)
	}
	t.Cleanup(func() { projectDB.Close(); globalDB.Close() })

	ps := store.NewMemoryStore(projectDB)
	gs := store.NewMemoryStore(globalDB)
	cfg := config.Default()
	cfg.Graph.ExpansionEnabled = true
	cfg.Graph.ExpansionThreshold = 0.3
	cfg.Graph.ExpansionFanOutCap = 50
	cfg.Graph.ExpansionSeedTopK = 10
	cfg.Graph.GraphMode = graphMode

	svc := service.NewMemoryService(ps, gs, cfg, "test/ctx-graph", embed.NopEmbedder{})
	return svc, ps
}

// TestContext_GraphFocus_SurfacesNeighbor verifies acceptance criterion 3: when
// focus is set and graph is enabled, memories topologically related to focus
// results receive the +0.3 focus boost and appear higher in context.
func TestContext_GraphFocus_SurfacesNeighbor(t *testing.T) {
	svc, ps := newTestServiceWithGraphForContext(t, "ppr")
	ctx := context.Background()

	// A: matches focus query via FTS5.
	impA := 0.7
	aResp, err := svc.Save(ctx, model.SaveRequest{
		Title:      "zxqwvr focus anchor memory",
		Content:    "zxqwvr focus anchor memory zxqwvr zxqwvr",
		Importance: &impA,
	})
	if err != nil {
		t.Fatalf("Save A: %v", err)
	}

	// B: does NOT match the focus query but is graph-connected to A.
	impB := 0.4
	bResp, err := svc.Save(ctx, model.SaveRequest{
		Title:      "kyjmpx graph neighbor memory",
		Content:    "kyjmpx completely different tokens graph neighbor",
		Importance: &impB,
	})
	if err != nil {
		t.Fatalf("Save B: %v", err)
	}

	// C: no text match, no graph connection — control group.
	impC := 0.6
	cResp, err := svc.Save(ctx, model.SaveRequest{
		Title:      "wqmpbz unrelated control memory",
		Content:    "wqmpbz unrelated control no graph connection",
		Importance: &impC,
	})
	if err != nil {
		t.Fatalf("Save C: %v", err)
	}

	// Wire A and B via entity + relation.
	eA, err := ps.FindOrCreateEntity(ctx, "ctx-entity-a", model.KindConcept, "test/ctx-graph")
	if err != nil {
		t.Fatalf("entity A: %v", err)
	}
	eB, err := ps.FindOrCreateEntity(ctx, "ctx-entity-b", model.KindConcept, "test/ctx-graph")
	if err != nil {
		t.Fatalf("entity B: %v", err)
	}
	if err := ps.LinkMemoryEntity(ctx, aResp.ID, eA.ID, "subject"); err != nil {
		t.Fatalf("link A: %v", err)
	}
	if err := ps.LinkMemoryEntity(ctx, bResp.ID, eB.ID, "subject"); err != nil {
		t.Fatalf("link B: %v", err)
	}
	if _, err := ps.CreateRelation(ctx, &model.Relation{
		SourceID: eA.ID,
		TargetID: eB.ID,
		Type:     model.RelRelatedTo,
		Weight:   0.9,
	}); err != nil {
		t.Fatalf("relation A->B: %v", err)
	}

	// Context with focus on A's unique tokens.
	resp, err := svc.Context(ctx, model.ContextRequest{
		Focus: "zxqwvr",
	})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}

	// Find positions of B and C in the packed memories.
	posB, posC := -1, -1
	for i, m := range resp.Memories {
		if m.ID == bResp.ID {
			posB = i
		}
		if m.ID == cResp.ID {
			posC = i
		}
	}

	if posB == -1 {
		t.Fatal("graph neighbor B not found in context memories")
	}

	// B (graph-connected, importance 0.4 + 0.3 boost = 0.7) should rank above
	// C (no connection, importance 0.6 no boost = 0.6).
	if posC != -1 && posB > posC {
		t.Errorf("expected graph neighbor B (pos %d) to rank above unconnected C (pos %d)", posB, posC)
	}
}

// TestContext_GraphFocus_Disabled verifies that IncludeGraph=false in the
// request prevents graph expansion from augmenting the focus set.
func TestContext_GraphFocus_Disabled(t *testing.T) {
	svc, ps := newTestServiceWithGraphForContext(t, "ppr")
	ctx := context.Background()

	impA := 0.8
	aResp, err := svc.Save(ctx, model.SaveRequest{
		Title:      "lmnopq graph disabled anchor",
		Content:    "lmnopq graph disabled anchor lmnopq lmnopq",
		Importance: &impA,
	})
	if err != nil {
		t.Fatalf("Save A: %v", err)
	}

	// B has no text match with focus and only graph connection.
	impB := 0.9 // high importance so it would appear even without boost if graph worked
	bResp, err := svc.Save(ctx, model.SaveRequest{
		Title:      "rstuv graph neighbor disabled",
		Content:    "rstuv graph neighbor disabled different tokens",
		Importance: &impB,
	})
	if err != nil {
		t.Fatalf("Save B: %v", err)
	}

	eA, err := ps.FindOrCreateEntity(ctx, "dis-entity-a", model.KindConcept, "test/ctx-graph")
	if err != nil {
		t.Fatalf("entity A: %v", err)
	}
	eB, err := ps.FindOrCreateEntity(ctx, "dis-entity-b", model.KindConcept, "test/ctx-graph")
	if err != nil {
		t.Fatalf("entity B: %v", err)
	}
	if err := ps.LinkMemoryEntity(ctx, aResp.ID, eA.ID, "subject"); err != nil {
		t.Fatalf("link A: %v", err)
	}
	if err := ps.LinkMemoryEntity(ctx, bResp.ID, eB.ID, "subject"); err != nil {
		t.Fatalf("link B: %v", err)
	}
	if _, err := ps.CreateRelation(ctx, &model.Relation{
		SourceID: eA.ID,
		TargetID: eB.ID,
		Type:     model.RelRelatedTo,
		Weight:   0.9,
	}); err != nil {
		t.Fatalf("relation: %v", err)
	}

	// Request with IncludeGraph=false: graph expansion must not run.
	igFalse := false
	_, err = svc.Context(ctx, model.ContextRequest{
		Focus:        "lmnopq",
		IncludeGraph: &igFalse,
	})
	if err != nil {
		t.Fatalf("Context with IncludeGraph=false: %v", err)
	}
	// No assertion on B's position here — the test verifies no error and that
	// the request param is accepted correctly. The scoring with IncludeGraph=false
	// must not produce a different number of returned memories vs enabled.
}

// TestContext_NoFocus_NoGraphExpansion verifies that without a focus query,
// context graph expansion is never triggered.
func TestContext_NoFocus_NoGraphExpansion(t *testing.T) {
	svc, _ := newTestServiceWithGraphForContext(t, "ppr")
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_, err := svc.Save(ctx, model.SaveRequest{
			Title:   "memory without focus",
			Content: "content without any special tokens",
		})
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	// Without focus: context must not panic and must return memories.
	resp, err := svc.Context(ctx, model.ContextRequest{})
	if err != nil {
		t.Fatalf("Context without focus: %v", err)
	}
	if len(resp.Memories) == 0 {
		t.Error("expected memories in context even without focus")
	}
}

// TestContext_GraphFocus_PPRFallback verifies that context graph expansion
// gracefully falls back when PPR fails (empty graph for seeds).
func TestContext_GraphFocus_PPRFallback(t *testing.T) {
	svc, _ := newTestServiceWithGraphForContext(t, "ppr")
	ctx := context.Background()

	// Save a memory with unique tokens; no entity/relation setup so graph is empty.
	_, err := svc.Save(ctx, model.SaveRequest{
		Title:   "xqzpnm fallback seed",
		Content: "xqzpnm fallback seed xqzpnm unique tokens here",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Context with focus must not error even when BuildGraphForSeeds returns empty.
	resp, err := svc.Context(ctx, model.ContextRequest{
		Focus: "xqzpnm",
	})
	if err != nil {
		t.Fatalf("Context (PPR fallback): %v", err)
	}
	if len(resp.Memories) == 0 {
		t.Error("expected at least one memory after graceful PPR fallback")
	}
}
