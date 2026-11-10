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
