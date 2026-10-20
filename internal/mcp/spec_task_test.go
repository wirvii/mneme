package mcp

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// TestToolSchema_SpecDocWriteTask covers criterion "schema": kind gains
// task and a section property carries the five slugs.
func TestToolSchema_SpecDocWriteTask(t *testing.T) {
	tool := findTool(allTools(), "spec_doc_write")
	if tool == nil {
		t.Fatal("spec_doc_write not found")
	}
	props := tool.InputSchema.(map[string]any)["properties"].(map[string]any)

	kinds := props["kind"].(map[string]any)["enum"].([]string)
	found := false
	for _, k := range kinds {
		found = found || k == "task"
	}
	if !found {
		t.Errorf("kind enum %v lacks task", kinds)
	}
	section, ok := props["section"].(map[string]any)
	if !ok {
		t.Fatal("section property missing")
	}
	want := []string{"que", "diseno", "plan", "entrega", "revision"}
	if got := section["enum"].([]string); !reflect.DeepEqual(got, want) {
		t.Errorf("section enum = %v, want %v", got, want)
	}
}

// TestHandleSpecDocWrite_TaskErrors covers the MCP half of criterion
// "errors": the four sentinels surface as CodeInvalidParams.
func TestHandleSpecDocWrite_TaskErrors(t *testing.T) {
	srv, _ := newTestServerWithSDDAndWorkflowDir(t)
	newResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "spec_new",
		Arguments: mustMarshal(t, map[string]any{"title": "task errors", "lane": "standard"}),
	})
	if newResp.Error != nil {
		t.Fatalf("spec_new: %v", newResp.Error.Message)
	}
	var spec struct {
		ID string `json:"id"`
	}
	unmarshalToolText(t, newResp, &spec)

	tests := []struct {
		name string
		args map[string]any
	}{
		{"section required", map[string]any{"kind": "task", "content": "x"}},
		{"unknown section", map[string]any{"kind": "task", "section": "bogus", "content": "x"}},
		{"section not applicable", map[string]any{"kind": "spec", "section": "plan", "content": "x"}},
	}
	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.args["id"] = spec.ID
			resp := process(t, srv, "tools/call", 10+i, ToolCallParams{Name: "spec_doc_write", Arguments: mustMarshal(t, tc.args)})
			if resp.Error == nil || resp.Error.Code != CodeInvalidParams {
				t.Errorf("error = %+v, want CodeInvalidParams", resp.Error)
			}
		})
	}

	t.Run("malformed document", func(t *testing.T) {
		ok := process(t, srv, "tools/call", 20, ToolCallParams{
			Name:      "spec_doc_write",
			Arguments: mustMarshal(t, map[string]any{"id": spec.ID, "kind": "task", "section": "plan", "content": "x"}),
		})
		if ok.Error != nil {
			t.Fatalf("valid task write: %v", ok.Error.Message)
		}
		var res struct {
			Path    string `json:"path"`
			Section string `json:"section"`
		}
		unmarshalToolText(t, ok, &res)
		if res.Section != "plan" {
			t.Errorf("section = %q, want plan", res.Section)
		}
		if err := os.WriteFile(res.Path, []byte("hand edited, no markers"), 0o644); err != nil {
			t.Fatal(err)
		}
		resp := process(t, srv, "tools/call", 21, ToolCallParams{
			Name:      "spec_doc_write",
			Arguments: mustMarshal(t, map[string]any{"id": spec.ID, "kind": "task", "section": "plan", "content": "y"}),
		})
		if resp.Error == nil || resp.Error.Code != CodeInvalidParams {
			t.Errorf("error = %+v, want CodeInvalidParams", resp.Error)
		}
	})
}

// TestCallerPolicy_TaskSection covers criterion "codex-parity".
func TestCallerPolicy_TaskSection(t *testing.T) {
	call := func(section string) json.RawMessage {
		return mustMarshal(t, map[string]any{"kind": "task", "section": section})
	}
	tests := []struct {
		archetype string
		section   string
		denied    bool
	}{
		{"backend", "diseno", true},
		{"backend", "entrega", false},
		{"backend", "revision", true},
		{"qa-tester", "revision", false},
		{"qa-tester", "entrega", true},
		{"architect", "que", false},
		{"architect", "entrega", true},
		{"backend", "", false},
		{"backend", "bogus", false},
	}
	for _, tc := range tests {
		t.Run(tc.archetype+"/"+tc.section, func(t *testing.T) {
			p := CallerPolicy{Role: tc.archetype, Archetype: tc.archetype}
			err := p.authorizeCall(ToolCallParams{Name: "spec_doc_write", Arguments: call(tc.section)})
			if (err != nil) != tc.denied {
				t.Errorf("denied = %v, want %v (%v)", err != nil, tc.denied, err)
			}
		})
	}
}
