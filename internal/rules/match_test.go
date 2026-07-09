package rules

import (
	"testing"

	"github.com/wirvii/mneme/internal/model"
)

// cwd is the synthetic working directory used throughout the test suite.
const cwd = "/home/user/project"

// makeRule is a helper that builds a minimal model.Memory of TypeRule.
func makeRule(severity model.Severity, appliesTo ...string) model.Memory {
	return model.Memory{
		ID:        "test-rule",
		Type:      model.TypeRule,
		Severity:  severity,
		AppliesTo: appliesTo,
	}
}

// ---- entryMatch unit tests --------------------------------------------------

func TestEntryMatch_Wildcard(t *testing.T) {
	if !entryMatch("**", "Edit", "internal/store/memory.go", false, CallerOrchestrator) {
		t.Error("** should match any tool and path")
	}
}

func TestEntryMatch_WildcardEmptyPath(t *testing.T) {
	if !entryMatch("**", "Edit", "", false, CallerOrchestrator) {
		t.Error("** should match even when pathRel is empty")
	}
}

func TestEntryMatch_ToolSelectorMatch(t *testing.T) {
	if !entryMatch("tool:Edit", "Edit", "any/path.go", false, CallerOrchestrator) {
		t.Error("tool:Edit should match when toolName is Edit")
	}
}

func TestEntryMatch_ToolSelectorMismatch(t *testing.T) {
	if entryMatch("tool:Write", "Edit", "any/path.go", false, CallerOrchestrator) {
		t.Error("tool:Write should not match when toolName is Edit")
	}
}

func TestEntryMatch_ToolSelectorCaseSensitive(t *testing.T) {
	if entryMatch("tool:edit", "Edit", "any/path.go", false, CallerOrchestrator) {
		t.Error("tool selector matching must be case-sensitive")
	}
}

func TestEntryMatch_PathGlobMatch(t *testing.T) {
	if !entryMatch("internal/**/*.go", "Edit", "internal/store/memory.go", false, CallerOrchestrator) {
		t.Error("glob should match file inside internal/")
	}
}

func TestEntryMatch_PathGlobMismatch(t *testing.T) {
	if entryMatch("internal/**/*.go", "Edit", "docs/README.md", false, CallerOrchestrator) {
		t.Error("glob internal/**/*.go should not match docs/README.md")
	}
}

func TestEntryMatch_NestedDoublestar(t *testing.T) {
	if !entryMatch("**/test/**", "Edit", "a/b/test/c/d.go", false, CallerOrchestrator) {
		t.Error("**/test/** should match nested test directory")
	}
}

func TestEntryMatch_CombinedToolAndPath_Match(t *testing.T) {
	if !entryMatch("tool:Edit+internal/**", "Edit", "internal/store/foo.go", false, CallerOrchestrator) {
		t.Error("combined tool:Edit+internal/** should match")
	}
}

func TestEntryMatch_CombinedToolAndPath_ToolMismatch(t *testing.T) {
	if entryMatch("tool:Edit+internal/**", "Write", "internal/store/foo.go", false, CallerOrchestrator) {
		t.Error("combined tool:Edit+internal/** should not match when tool is Write")
	}
}

func TestEntryMatch_CombinedToolAndPath_PathMismatch(t *testing.T) {
	if entryMatch("tool:Edit+internal/**", "Edit", "docs/x.md", false, CallerOrchestrator) {
		t.Error("combined tool:Edit+internal/** should not match docs/x.md")
	}
}

func TestEntryMatch_TwoToolSelectors_NeverMatches(t *testing.T) {
	// tool:Foo+tool:Bar — both tool selectors can never be simultaneously true.
	if entryMatch("tool:Foo+tool:Bar", "Foo", "any/path.go", false, CallerOrchestrator) {
		t.Error("tool:Foo+tool:Bar should never match (a tool cannot be two things)")
	}
}

func TestEntryMatch_OutOfTree_ToolSelectorMatches(t *testing.T) {
	if !entryMatch("tool:Edit", "Edit", "", true, CallerOrchestrator) {
		t.Error("tool selector should match even for out-of-tree paths")
	}
}

func TestEntryMatch_OutOfTree_PathGlobDoesNotMatch(t *testing.T) {
	if entryMatch("internal/**", "Edit", "", true, CallerOrchestrator) {
		t.Error("path glob should not match when path is out-of-tree")
	}
}

func TestEntryMatch_OutOfTree_WildcardMatches(t *testing.T) {
	if !entryMatch("**", "Edit", "", true, CallerOrchestrator) {
		t.Error("** should match even for out-of-tree paths")
	}
}

func TestEntryMatch_EmptyPath_ToolSelectorMatches(t *testing.T) {
	if !entryMatch("tool:Edit", "Edit", "", false, CallerOrchestrator) {
		t.Error("tool:Edit should match when pathRel is empty")
	}
}

func TestEntryMatch_EmptyPath_PathGlobDoesNotMatch(t *testing.T) {
	if entryMatch("internal/**", "Edit", "", false, CallerOrchestrator) {
		t.Error("path glob should not match when pathRel is empty")
	}
}

func TestEntryMatch_InvalidPlusEntry(t *testing.T) {
	// "+" with empty parts — entryMatch("", ...) returns false for any non-** entry.
	if entryMatch("+", "Edit", "any/path.go", false, CallerOrchestrator) {
		t.Error("bare + should not match")
	}
}

// ---- agent: selector unit tests (D2 table) ----------------------------------

func TestEntryMatch_AgentOrchestrator_MatchesOrchestrator(t *testing.T) {
	if !entryMatch("agent:orchestrator", "Edit", "any/path.go", false, CallerOrchestrator) {
		t.Error("agent:orchestrator should match when caller is orchestrator")
	}
}

func TestEntryMatch_AgentOrchestrator_NoMatchSubagent(t *testing.T) {
	if entryMatch("agent:orchestrator", "Edit", "any/path.go", false, CallerSubagent) {
		t.Error("agent:orchestrator should not match when caller is subagent")
	}
}

func TestEntryMatch_AgentSubagent_MatchesSubagent(t *testing.T) {
	if !entryMatch("agent:subagent", "Edit", "any/path.go", false, CallerSubagent) {
		t.Error("agent:subagent should match when caller is subagent")
	}
}

func TestEntryMatch_AgentSubagent_NoMatchOrchestrator(t *testing.T) {
	if entryMatch("agent:subagent", "Edit", "any/path.go", false, CallerOrchestrator) {
		t.Error("agent:subagent should not match when caller is orchestrator")
	}
}

func TestEntryMatch_AgentWildcard_MatchesOrchestrator(t *testing.T) {
	if !entryMatch("agent:*", "Edit", "any/path.go", false, CallerOrchestrator) {
		t.Error("agent:* should match orchestrator")
	}
}

func TestEntryMatch_AgentWildcard_MatchesSubagent(t *testing.T) {
	if !entryMatch("agent:*", "Edit", "any/path.go", false, CallerSubagent) {
		t.Error("agent:* should match subagent")
	}
}

func TestEntryMatch_AgentUnknownType_NoMatch(t *testing.T) {
	// agent:backend / agent:frontend — deferred; never matches either caller.
	if entryMatch("agent:backend", "Edit", "any/path.go", false, CallerOrchestrator) {
		t.Error("agent:backend should not match orchestrator (deferred)")
	}
	if entryMatch("agent:backend", "Edit", "any/path.go", false, CallerSubagent) {
		t.Error("agent:backend should not match subagent (deferred)")
	}
}

func TestEntryMatch_CombinedAgentToolPath_ThreeParts(t *testing.T) {
	// "agent:orchestrator+tool:Edit+internal/**" — three-part AND.
	entry := "agent:orchestrator+tool:Edit+internal/**"
	if !entryMatch(entry, "Edit", "internal/store/foo.go", false, CallerOrchestrator) {
		t.Error("three-part combined entry should match when all parts match")
	}
	// Fails when caller is subagent.
	if entryMatch(entry, "Edit", "internal/store/foo.go", false, CallerSubagent) {
		t.Error("three-part entry should not match when agent: part does not match")
	}
	// Fails when tool is wrong.
	if entryMatch(entry, "Write", "internal/store/foo.go", false, CallerOrchestrator) {
		t.Error("three-part entry should not match when tool: part does not match")
	}
}

func TestEntryMatch_NegationAgentSubagent(t *testing.T) {
	// "!agent:subagent" is a negation; matchRule strips "!" before calling entryMatch.
	// Verify entryMatch("agent:subagent", ..., CallerSubagent) returns true
	// so the negation correctly vetos the rule for subagents.
	if !entryMatch("agent:subagent", "Edit", "foo.go", false, CallerSubagent) {
		t.Error("agent:subagent should match subagent (so negation vetoes correctly)")
	}
	if entryMatch("agent:subagent", "Edit", "foo.go", false, CallerOrchestrator) {
		t.Error("agent:subagent should not match orchestrator")
	}
}

// ---- matchRule unit tests ---------------------------------------------------

func TestMatchRule_NegationExcludes(t *testing.T) {
	appliesTo := []string{"**", "!docs/**"}
	ok, _ := matchRule(appliesTo, "Edit", "docs/README.md", false, CallerOrchestrator)
	if ok {
		t.Error("rule should not fire when negative entry vetoes the path")
	}
}

func TestMatchRule_NegationAllowsOther(t *testing.T) {
	appliesTo := []string{"**", "!docs/**"}
	ok, _ := matchRule(appliesTo, "Edit", "internal/store/foo.go", false, CallerOrchestrator)
	if !ok {
		t.Error("rule should fire for a path not excluded by the negation")
	}
}

func TestMatchRule_NegationOnlyNeverMatches(t *testing.T) {
	// ["!docs/**"] has no positive entries, so it should never match.
	appliesTo := []string{"!docs/**"}
	ok, _ := matchRule(appliesTo, "Edit", "internal/foo.go", false, CallerOrchestrator)
	if ok {
		t.Error("negation-only applies_to should never match (no positives)")
	}
}

func TestMatchRule_MultiplePosAnyMatches(t *testing.T) {
	appliesTo := []string{"internal/**", "cmd/**"}
	ok, _ := matchRule(appliesTo, "Edit", "cmd/main.go", false, CallerOrchestrator)
	if !ok {
		t.Error("rule should fire when any positive entry matches")
	}
}

func TestMatchRule_CombinedWithNegation_InsidePlus(t *testing.T) {
	// "tool:Edit+!internal/**" — the second part starts with "!". entryMatch
	// receives "!internal/**" as a positive entry (it is the second token after
	// splitting on "+"). A path starting with "!" never matches any real glob,
	// so the combined entry never fires.
	appliesTo := []string{"tool:Edit+!internal/**"}
	ok, _ := matchRule(appliesTo, "Edit", "internal/foo.go", false, CallerOrchestrator)
	if ok {
		t.Error("tool:Edit+!path should not match because ! inside + is not special")
	}
}

func TestMatchRule_NegationAgentSubagent_VetoesForSubagent(t *testing.T) {
	// ["**", "!agent:subagent"] should fire for orchestrator but not for subagent.
	appliesTo := []string{"**", "!agent:subagent"}
	okOrch, _ := matchRule(appliesTo, "Edit", "foo.go", false, CallerOrchestrator)
	if !okOrch {
		t.Error("rule should fire for orchestrator when only subagent is negated")
	}
	okSub, _ := matchRule(appliesTo, "Edit", "foo.go", false, CallerSubagent)
	if okSub {
		t.Error("rule should not fire for subagent when !agent:subagent is present")
	}
}

// ---- normalisePath unit tests -----------------------------------------------

func TestNormalisePath_RelativeResult(t *testing.T) {
	rel, outOfTree := normalisePath("/home/user/project/internal/store/memory.go", cwd)
	if outOfTree {
		t.Error("path inside project should not be out-of-tree")
	}
	if rel != "internal/store/memory.go" {
		t.Errorf("rel = %q, want %q", rel, "internal/store/memory.go")
	}
}

func TestNormalisePath_OutOfTree(t *testing.T) {
	_, outOfTree := normalisePath("/etc/hosts", cwd)
	if !outOfTree {
		t.Error("/etc/hosts should be out-of-tree relative to /home/user/project")
	}
}

func TestNormalisePath_DotDotEscape(t *testing.T) {
	_, outOfTree := normalisePath("/home/user/other-project/secret.go", cwd)
	if !outOfTree {
		t.Error("sibling directory should be out-of-tree")
	}
}

func TestNormalisePath_Empty(t *testing.T) {
	rel, outOfTree := normalisePath("", cwd)
	if outOfTree {
		t.Error("empty path should not set outOfTree")
	}
	if rel != "" {
		t.Errorf("empty path should produce empty rel, got %q", rel)
	}
}

func TestNormalisePath_RelativeInput(t *testing.T) {
	rel, outOfTree := normalisePath("internal/foo.go", cwd)
	if outOfTree {
		t.Error("relative path inside project should not be out-of-tree")
	}
	if rel != "internal/foo.go" {
		t.Errorf("rel = %q, want %q", rel, "internal/foo.go")
	}
}

// ---- Match integration tests ------------------------------------------------

func TestMatch_WildcardMatchesAll(t *testing.T) {
	rule := makeRule(model.SeverityInfo, "**")
	result := Match([]model.Memory{rule}, Invocation{Tool: "Edit", FilePath: "/home/user/project/cmd/main.go", CWD: cwd, Caller: CallerOrchestrator})
	if len(result.Matched) != 1 {
		t.Errorf("expected 1 matched rule, got %d", len(result.Matched))
	}
}

func TestMatch_ToolSelectorOnly(t *testing.T) {
	rule := makeRule(model.SeverityWarn, "tool:Edit")
	result := Match([]model.Memory{rule}, Invocation{Tool: "Edit", FilePath: "/home/user/project/any.go", CWD: cwd, Caller: CallerOrchestrator})
	if len(result.Matched) != 1 {
		t.Errorf("expected 1 matched rule, got %d", len(result.Matched))
	}
}

func TestMatch_ToolSelectorMismatch(t *testing.T) {
	rule := makeRule(model.SeverityWarn, "tool:Write")
	result := Match([]model.Memory{rule}, Invocation{Tool: "Edit", FilePath: "/home/user/project/any.go", CWD: cwd, Caller: CallerOrchestrator})
	if len(result.Matched) != 0 {
		t.Errorf("expected 0 matched rules, got %d", len(result.Matched))
	}
}

func TestMatch_PathGlobMatch(t *testing.T) {
	rule := makeRule(model.SeverityInfo, "internal/**/*.go")
	result := Match([]model.Memory{rule}, Invocation{Tool: "Edit", FilePath: "/home/user/project/internal/store/foo.go", CWD: cwd, Caller: CallerOrchestrator})
	if len(result.Matched) != 1 {
		t.Errorf("expected 1 matched rule, got %d", len(result.Matched))
	}
}

func TestMatch_PathGlobMismatch(t *testing.T) {
	rule := makeRule(model.SeverityInfo, "internal/**/*.go")
	result := Match([]model.Memory{rule}, Invocation{Tool: "Edit", FilePath: "/home/user/project/docs/README.md", CWD: cwd, Caller: CallerOrchestrator})
	if len(result.Matched) != 0 {
		t.Errorf("expected 0 matched rules, got %d", len(result.Matched))
	}
}

func TestMatch_CombinedToolAndPath(t *testing.T) {
	rule := makeRule(model.SeverityBlock, "tool:Edit+internal/**")
	result := Match([]model.Memory{rule}, Invocation{Tool: "Edit", FilePath: "/home/user/project/internal/x.go", CWD: cwd, Caller: CallerOrchestrator})
	if len(result.Matched) != 1 {
		t.Errorf("expected 1 matched rule, got %d", len(result.Matched))
	}
	if result.MaxSev != model.SeverityBlock {
		t.Errorf("MaxSev = %q, want block", result.MaxSev)
	}
}

func TestMatch_CombinedToolMismatch(t *testing.T) {
	rule := makeRule(model.SeverityBlock, "tool:Edit+internal/**")
	result := Match([]model.Memory{rule}, Invocation{Tool: "Write", FilePath: "/home/user/project/internal/x.go", CWD: cwd, Caller: CallerOrchestrator})
	if len(result.Matched) != 0 {
		t.Errorf("expected 0 matched rules, got %d", len(result.Matched))
	}
}

func TestMatch_CombinedPathMismatch(t *testing.T) {
	rule := makeRule(model.SeverityBlock, "tool:Edit+internal/**")
	result := Match([]model.Memory{rule}, Invocation{Tool: "Edit", FilePath: "/home/user/project/docs/x.md", CWD: cwd, Caller: CallerOrchestrator})
	if len(result.Matched) != 0 {
		t.Errorf("expected 0 matched rules, got %d", len(result.Matched))
	}
}

func TestMatch_NegationExcludes(t *testing.T) {
	rule := makeRule(model.SeverityInfo, "**", "!docs/**")
	result := Match([]model.Memory{rule}, Invocation{Tool: "Edit", FilePath: "/home/user/project/docs/README.md", CWD: cwd, Caller: CallerOrchestrator})
	if len(result.Matched) != 0 {
		t.Errorf("expected 0 matched rules (negation veto), got %d", len(result.Matched))
	}
}

func TestMatch_NegationAllowsOther(t *testing.T) {
	rule := makeRule(model.SeverityInfo, "**", "!docs/**")
	result := Match([]model.Memory{rule}, Invocation{Tool: "Edit", FilePath: "/home/user/project/internal/x.go", CWD: cwd, Caller: CallerOrchestrator})
	if len(result.Matched) != 1 {
		t.Errorf("expected 1 matched rule, got %d", len(result.Matched))
	}
}

func TestMatch_NegationOnlyNeverMatches(t *testing.T) {
	rule := makeRule(model.SeverityInfo, "!docs/**")
	result := Match([]model.Memory{rule}, Invocation{Tool: "Edit", FilePath: "/home/user/project/internal/x.go", CWD: cwd, Caller: CallerOrchestrator})
	if len(result.Matched) != 0 {
		t.Errorf("negation-only rule should never match, got %d", len(result.Matched))
	}
}

func TestMatch_OutOfTreeToolSelector(t *testing.T) {
	rule := makeRule(model.SeverityBlock, "tool:Edit")
	result := Match([]model.Memory{rule}, Invocation{Tool: "Edit", FilePath: "/etc/hosts", CWD: cwd, Caller: CallerOrchestrator})
	if len(result.Matched) != 1 {
		t.Errorf("tool selector should match even for out-of-tree paths, got %d matches", len(result.Matched))
	}
}

func TestMatch_OutOfTreePathGlob(t *testing.T) {
	rule := makeRule(model.SeverityBlock, "internal/**")
	result := Match([]model.Memory{rule}, Invocation{Tool: "Edit", FilePath: "/etc/hosts", CWD: cwd, Caller: CallerOrchestrator})
	if len(result.Matched) != 0 {
		t.Errorf("path glob should not match out-of-tree path, got %d matches", len(result.Matched))
	}
}

func TestMatch_OutOfTreeWildcard(t *testing.T) {
	rule := makeRule(model.SeverityWarn, "**")
	result := Match([]model.Memory{rule}, Invocation{Tool: "Edit", FilePath: "/etc/hosts", CWD: cwd, Caller: CallerOrchestrator})
	if len(result.Matched) != 1 {
		t.Errorf("** should match even for out-of-tree paths, got %d matches", len(result.Matched))
	}
}

func TestMatch_MaxSeverityBlock(t *testing.T) {
	infoRule := makeRule(model.SeverityInfo, "**")
	blockRule := model.Memory{
		ID: "block-rule", Type: model.TypeRule,
		Severity: model.SeverityBlock, AppliesTo: []string{"internal/**"},
	}
	result := Match([]model.Memory{infoRule, blockRule}, Invocation{Tool: "Edit", FilePath: "/home/user/project/internal/x.go", CWD: cwd, Caller: CallerOrchestrator})
	if result.MaxSev != model.SeverityBlock {
		t.Errorf("MaxSev = %q, want block", result.MaxSev)
	}
}

func TestMatch_MaxSeverityWarn(t *testing.T) {
	infoRule := makeRule(model.SeverityInfo, "**")
	warnRule := model.Memory{
		ID: "warn-rule", Type: model.TypeRule,
		Severity: model.SeverityWarn, AppliesTo: []string{"internal/**"},
	}
	result := Match([]model.Memory{infoRule, warnRule}, Invocation{Tool: "Edit", FilePath: "/home/user/project/internal/x.go", CWD: cwd, Caller: CallerOrchestrator})
	if result.MaxSev != model.SeverityWarn {
		t.Errorf("MaxSev = %q, want warn", result.MaxSev)
	}
}

func TestMatch_NoRules(t *testing.T) {
	result := Match(nil, Invocation{Tool: "Edit", FilePath: "/home/user/project/internal/x.go", CWD: cwd, Caller: CallerOrchestrator})
	if len(result.Matched) != 0 {
		t.Errorf("expected 0 matched rules for empty rule set, got %d", len(result.Matched))
	}
	if result.MaxSev != "" {
		t.Errorf("MaxSev should be empty when no rules matched, got %q", result.MaxSev)
	}
}

func TestMatch_SortedBySeverityDesc(t *testing.T) {
	infoRule := model.Memory{ID: "info", Type: model.TypeRule, Severity: model.SeverityInfo, AppliesTo: []string{"**"}}
	warnRule := model.Memory{ID: "warn", Type: model.TypeRule, Severity: model.SeverityWarn, AppliesTo: []string{"**"}}
	blockRule := model.Memory{ID: "block", Type: model.TypeRule, Severity: model.SeverityBlock, AppliesTo: []string{"**"}}

	// Pass in reverse severity order to verify sort.
	result := Match([]model.Memory{infoRule, warnRule, blockRule}, Invocation{Tool: "Edit", FilePath: "/home/user/project/x.go", CWD: cwd, Caller: CallerOrchestrator})
	if len(result.Matched) != 3 {
		t.Fatalf("expected 3 matched rules, got %d", len(result.Matched))
	}
	if result.Matched[0].Effective != model.SeverityBlock {
		t.Errorf("first matched rule effective severity = %q, want block", result.Matched[0].Effective)
	}
	if result.Matched[1].Effective != model.SeverityWarn {
		t.Errorf("second matched rule effective severity = %q, want warn", result.Matched[1].Effective)
	}
	if result.Matched[2].Effective != model.SeverityInfo {
		t.Errorf("third matched rule effective severity = %q, want info", result.Matched[2].Effective)
	}
}

func TestMatch_DotDotEscape(t *testing.T) {
	pathGlobRule := makeRule(model.SeverityBlock, "secret/**")
	result := Match([]model.Memory{pathGlobRule}, Invocation{Tool: "Edit", FilePath: "/home/user/other-project/secret/file.go", CWD: cwd, Caller: CallerOrchestrator})
	if len(result.Matched) != 0 {
		t.Errorf("path glob should not match escaped path, got %d matches", len(result.Matched))
	}
}

func TestMatch_CaseSensitiveTool(t *testing.T) {
	rule := makeRule(model.SeverityInfo, "tool:edit") // lowercase
	result := Match([]model.Memory{rule}, Invocation{Tool: "Edit", FilePath: "/home/user/project/x.go", CWD: cwd, Caller: CallerOrchestrator})
	if len(result.Matched) != 0 {
		t.Errorf("tool selector should be case-sensitive, got %d matches", len(result.Matched))
	}
}

func TestMatch_NestedDoublestar(t *testing.T) {
	rule := makeRule(model.SeverityInfo, "**/test/**")
	result := Match([]model.Memory{rule}, Invocation{Tool: "Edit", FilePath: "/home/user/project/a/b/test/c/d.go", CWD: cwd, Caller: CallerOrchestrator})
	if len(result.Matched) != 1 {
		t.Errorf("expected 1 matched rule for nested doublestar, got %d", len(result.Matched))
	}
}

func TestMatch_TwoToolSelectors_NeverMatches(t *testing.T) {
	rule := makeRule(model.SeverityBlock, "tool:Foo+tool:Bar")
	result := Match([]model.Memory{rule}, Invocation{Tool: "Foo", FilePath: "/home/user/project/x.go", CWD: cwd, Caller: CallerOrchestrator})
	if len(result.Matched) != 0 {
		t.Errorf("tool:Foo+tool:Bar should never match, got %d matches", len(result.Matched))
	}
}

func TestMatch_EmptyPathToolOnly(t *testing.T) {
	rule := makeRule(model.SeverityInfo, "tool:Edit")
	result := Match([]model.Memory{rule}, Invocation{Tool: "Edit", FilePath: "", CWD: cwd, Caller: CallerOrchestrator})
	if len(result.Matched) != 1 {
		t.Errorf("tool:Edit should match even with empty filePath, got %d matches", len(result.Matched))
	}
}

func TestMatch_EmptyPathPathGlob(t *testing.T) {
	rule := makeRule(model.SeverityInfo, "internal/**")
	result := Match([]model.Memory{rule}, Invocation{Tool: "Edit", FilePath: "", CWD: cwd, Caller: CallerOrchestrator})
	if len(result.Matched) != 0 {
		t.Errorf("path glob should not match with empty filePath, got %d matches", len(result.Matched))
	}
}

// ---- TestMatch_Effective: the 7-row matrix from spec D5 ---------------------

// TestMatch_Effective verifies the effective severity and Degraded flag for all
// seven scenarios defined in spec D5. This is the canonical correctness gate for
// the role-aware enforcement logic.
func TestMatch_Effective(t *testing.T) {
	tests := []struct {
		name        string
		appliesTo   []string
		severity    model.Severity
		caller      Caller
		wantInMatch bool
		wantEff     model.Severity
		wantDeg     bool
	}{
		{
			name:        "no selector block orchestrator",
			appliesTo:   []string{"tool:Edit+internal/**"},
			severity:    model.SeverityBlock,
			caller:      CallerOrchestrator,
			wantInMatch: true,
			wantEff:     model.SeverityBlock,
			wantDeg:     false,
		},
		{
			name:        "no selector block subagent — DEGRADES",
			appliesTo:   []string{"tool:Edit+internal/**"},
			severity:    model.SeverityBlock,
			caller:      CallerSubagent,
			wantInMatch: true,
			wantEff:     model.SeverityWarn,
			wantDeg:     true,
		},
		{
			name:        "no selector warn subagent — no degradation",
			appliesTo:   []string{"**/*.go"},
			severity:    model.SeverityWarn,
			caller:      CallerSubagent,
			wantInMatch: true,
			wantEff:     model.SeverityWarn,
			wantDeg:     false,
		},
		{
			name:        "agent:orchestrator block orchestrator — applies as-is",
			appliesTo:   []string{"agent:orchestrator+internal/**"},
			severity:    model.SeverityBlock,
			caller:      CallerOrchestrator,
			wantInMatch: true,
			wantEff:     model.SeverityBlock,
			wantDeg:     false,
		},
		{
			name:        "agent:orchestrator block subagent — NO match",
			appliesTo:   []string{"agent:orchestrator+internal/**"},
			severity:    model.SeverityBlock,
			caller:      CallerSubagent,
			wantInMatch: false,
		},
		{
			name:        "agent:* block subagent — applies as-is (no degradation)",
			appliesTo:   []string{"agent:*+internal/**"},
			severity:    model.SeverityBlock,
			caller:      CallerSubagent,
			wantInMatch: true,
			wantEff:     model.SeverityBlock,
			wantDeg:     false,
		},
		{
			name:        "agent:subagent block orchestrator — NO match",
			appliesTo:   []string{"agent:subagent+internal/**"},
			severity:    model.SeverityBlock,
			caller:      CallerOrchestrator,
			wantInMatch: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rule := makeRule(tc.severity, tc.appliesTo...)
			rule.ID = tc.name

			result := Match([]model.Memory{rule}, Invocation{
				Tool:     "Edit",
				FilePath: "/home/user/project/internal/x.go",
				CWD:      cwd,
				Caller:   tc.caller,
			})

			if !tc.wantInMatch {
				if len(result.Matched) != 0 {
					t.Errorf("expected rule NOT in Matched, got %d matches", len(result.Matched))
				}
				return
			}

			if len(result.Matched) != 1 {
				t.Fatalf("expected 1 match, got %d", len(result.Matched))
			}
			mr := result.Matched[0]
			if mr.Effective != tc.wantEff {
				t.Errorf("Effective = %q, want %q", mr.Effective, tc.wantEff)
			}
			if mr.Degraded != tc.wantDeg {
				t.Errorf("Degraded = %v, want %v", mr.Degraded, tc.wantDeg)
			}
			if result.MaxSev != tc.wantEff {
				t.Errorf("MaxSev = %q, want %q", result.MaxSev, tc.wantEff)
			}
		})
	}
}

// TestSeverityOrder verifies the ranking used to pick MaxSev and sort results.
func TestSeverityOrder(t *testing.T) {
	if severityOrder(model.SeverityBlock) <= severityOrder(model.SeverityWarn) {
		t.Error("block must rank higher than warn")
	}
	if severityOrder(model.SeverityWarn) <= severityOrder(model.SeverityInfo) {
		t.Error("warn must rank higher than info")
	}
	if severityOrder(model.SeverityInfo) <= severityOrder("") {
		t.Error("info must rank higher than empty/unknown severity")
	}
}

// ---- ruleHasAgentSelector unit tests ----------------------------------------

func TestRuleHasAgentSelector_Positive(t *testing.T) {
	if !ruleHasAgentSelector([]string{"agent:orchestrator+internal/**"}) {
		t.Error("should detect agent: in combined entry")
	}
	if !ruleHasAgentSelector([]string{"agent:*"}) {
		t.Error("should detect standalone agent:*")
	}
	if !ruleHasAgentSelector([]string{"!agent:subagent"}) {
		t.Error("should detect agent: in negated entry")
	}
}

func TestRuleHasAgentSelector_Negative(t *testing.T) {
	if ruleHasAgentSelector([]string{"tool:Edit+internal/**", "**"}) {
		t.Error("should not detect agent: when absent")
	}
	if ruleHasAgentSelector(nil) {
		t.Error("nil slice should return false")
	}
}
