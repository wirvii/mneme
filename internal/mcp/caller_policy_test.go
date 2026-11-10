package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// docWriteParams builds the ToolCallParams of a spec_doc_write call.
func docWriteParams(t *testing.T, kind string) ToolCallParams {
	t.Helper()
	args, err := json.Marshal(map[string]string{"id": "SPEC-001", "kind": kind, "content": "x"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return ToolCallParams{Name: "spec_doc_write", Arguments: args}
}

// TestCallerPolicy_DocKinds covers SPEC-181 C11's Codex half: the by-kind
// restriction is resolved by archetype through model.SpecDocKindAllows, the
// same function the Claude Code hook uses.
func TestCallerPolicy_DocKinds(t *testing.T) {
	restricted := []string{"criteria", "review-spec", "review-plan"}

	architect := CallerPolicy{Role: "architect", Archetype: "architect"}
	for _, kind := range restricted {
		if err := architect.authorizeCall(docWriteParams(t, kind)); err != nil {
			t.Errorf("architect denied kind %s: %v", kind, err)
		}
	}

	for _, archetype := range []string{"backend", "frontend", "bug-hunter", "qa-tester", "diagnostician", "spec-reviewer"} {
		p := CallerPolicy{Role: archetype, Archetype: archetype}
		for _, kind := range restricted {
			err := p.authorizeCall(docWriteParams(t, kind))
			if err == nil {
				t.Errorf("%s allowed kind %s", archetype, kind)
				continue
			}
			if err.Code != CodeInvalidParams || !strings.Contains(err.Message, kind) || !strings.Contains(err.Message, "architect") {
				t.Errorf("%s kind %s: unexpected error %+v", archetype, kind, err)
			}
		}
	}

	if err := (CallerPolicy{Role: "qa-tester", Archetype: "qa-tester"}).authorizeCall(docWriteParams(t, "qa-report")); err != nil {
		t.Errorf("qa-tester denied qa-report: %v", err)
	}
	if err := (CallerPolicy{Role: "backend", Archetype: "backend"}).authorizeCall(docWriteParams(t, "changes")); err != nil {
		t.Errorf("backend denied changes: %v", err)
	}
}

// TestCallerPolicy_AllowedTools covers SPEC-181 C12: with the spec-reviewer
// allowlist only the four codegraph tools are authorized; with a nil
// AllowedTools the behaviour is today's (only the lifecycle tools denied).
func TestCallerPolicy_AllowedTools(t *testing.T) {
	reviewer := CallerPolicy{
		Role: "spec-reviewer", Archetype: "spec-reviewer",
		AllowedTools: []string{"codegraph_search", "codegraph_context", "codegraph_callers", "codegraph_impact"},
	}
	for _, name := range reviewer.AllowedTools {
		if !reviewer.allowsTool(name) {
			t.Errorf("reviewer denied %s", name)
		}
	}
	for _, name := range []string{"mem_save", "mem_search", "spec_doc_write", "spec_pushback", "backlog_add", "spec_status", "spec_advance"} {
		if reviewer.allowsTool(name) {
			t.Errorf("reviewer allowed %s", name)
		}
		if err := reviewer.authorizeCall(ToolCallParams{Name: name, Arguments: json.RawMessage(`{}`)}); err == nil || err.Code != CodeInvalidParams {
			t.Errorf("authorizeCall(%s) = %+v, want denied", name, err)
		}
	}

	var denied []string
	for _, tool := range allTools() {
		if reviewer.allowsTool(tool.Name) {
			denied = append(denied, tool.Name)
		}
	}
	if len(denied) != 4 {
		t.Errorf("tools allowed to the reviewer = %v, want exactly the four codegraph tools", denied)
	}

	unrestricted := CallerPolicy{Role: "backend", Archetype: "backend"}
	for _, name := range []string{"mem_save", "spec_doc_write", "spec_status", "codegraph_search"} {
		if !unrestricted.allowsTool(name) {
			t.Errorf("nil AllowedTools denied %s", name)
		}
	}
	for _, name := range []string{"spec_advance", "spec_quick", "backlog_archive", "lane_override"} {
		if unrestricted.allowsTool(name) {
			t.Errorf("nil AllowedTools allowed lifecycle tool %s", name)
		}
	}

	empty := CallerPolicy{Role: "x", Archetype: "x", AllowedTools: []string{}}
	if empty.allowsTool("spec_status") {
		t.Error("an empty (non-nil) AllowedTools must deny every tool")
	}
}
