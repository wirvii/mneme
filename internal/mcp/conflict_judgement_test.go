package mcp

import (
	"encoding/json"
	"testing"
)

// saveRetry saves one memory about retry backoff through mem_save and returns
// the decoded response.
func saveRetry(t *testing.T, srv *Server, n int, scope string) (id string, possible []map[string]any) {
	t.Helper()
	args := map[string]any{
		"title":   "Exponential backoff retry policy " + string(rune('a'+n)),
		"content": "The gateway client retries failed requests using exponential backoff with jitter and a retry budget.",
	}
	if scope != "" {
		args["scope"] = scope
	}
	resp := process(t, srv, "tools/call", n, ToolCallParams{Name: "mem_save", Arguments: mustMarshal(t, args)})
	var out struct {
		ID                string           `json:"id"`
		PossibleConflicts []map[string]any `json:"possible_conflicts"`
	}
	unmarshalToolText(t, resp, &out)
	return out.ID, out.PossibleConflicts
}

// TestMemSaveUpdate_ConflictJudgementRoundTrip is SPEC-166 AC7 (MCP half):
// mem_save proposes candidates, mem_update records the judgement.
func TestMemSaveUpdate_ConflictJudgementRoundTrip(t *testing.T) {
	srv := newTestServer(t)

	oldID, first := saveRetry(t, srv, 1, "")
	if len(first) != 0 {
		t.Fatalf("first save must have no candidates, got %v", first)
	}
	newID, possible := saveRetry(t, srv, 2, "")
	found := false
	for _, pc := range possible {
		if pc["id"] == oldID {
			found = true
		}
	}
	if !found {
		t.Fatalf("possible_conflicts = %v, want to contain %s", possible, oldID)
	}

	resp := process(t, srv, "tools/call", 10, ToolCallParams{
		Name:      "mem_update",
		Arguments: mustMarshal(t, map[string]any{"id": newID, "supersedes": []string{oldID}}),
	})
	var upd struct {
		RelationsApplied []struct {
			TargetID string `json:"target_id"`
			Relation string `json:"relation"`
		} `json:"relations_applied"`
	}
	unmarshalToolText(t, resp, &upd)
	if len(upd.RelationsApplied) != 1 || upd.RelationsApplied[0].TargetID != oldID || upd.RelationsApplied[0].Relation != "supersedes" {
		t.Fatalf("relations_applied = %+v", upd.RelationsApplied)
	}

	search := process(t, srv, "tools/call", 11, ToolCallParams{
		Name:      "mem_search",
		Arguments: mustMarshal(t, map[string]any{"query": "exponential backoff retry"}),
	})
	var sr struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	unmarshalToolText(t, search, &sr)
	for _, r := range sr.Results {
		if r.ID == oldID {
			t.Error("superseded memory still returned by mem_search")
		}
	}

	globalID, _ := saveRetry(t, srv, 3, "global")
	bad := process(t, srv, "tools/call", 12, ToolCallParams{
		Name:      "mem_update",
		Arguments: mustMarshal(t, map[string]any{"id": newID, "unrelated_to": []string{globalID}}),
	})
	if bad.Error == nil || bad.Error.Code != CodeInvalidParams {
		t.Errorf("cross-store target: error = %+v, want CodeInvalidParams", bad.Error)
	}
}

// TestMemUpdateSchema_DeclaresRelationLists pins the published contract for
// the three judgement fields (the SPEC-113 guardian checks presence; this
// checks the array-of-string shape).
func TestMemUpdateSchema_DeclaresRelationLists(t *testing.T) {
	tool := findTool(allTools(), "mem_update")
	if tool == nil {
		t.Fatal("mem_update not found")
	}
	raw, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var schema struct {
		Properties map[string]struct {
			Type  string `json:"type"`
			Items struct {
				Type string `json:"type"`
			} `json:"items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, name := range []string{"supersedes", "conflicts_with", "unrelated_to"} {
		p, ok := schema.Properties[name]
		if !ok || p.Type != "array" || p.Items.Type != "string" {
			t.Errorf("%s = %+v, want array of string", name, p)
		}
	}
}
