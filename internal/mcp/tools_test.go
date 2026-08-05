package mcp

import (
	"strings"
	"testing"
)

// TestAllTools_Count77 verifies the tool count after SPEC-109 adds
// backlog_get (D2/D12): 76 (established by SPEC-105 §8 DD21,
// profile_deactivate) -> 77.
func TestAllTools_Count77(t *testing.T) {
	tools := allTools()
	if len(tools) != 77 {
		t.Errorf("allTools() returned %d tools, want 77", len(tools))
	}
}

// TestAllTools_ExactlyOneBacklogGet is AC20's second half: exactly one tool
// named backlog_get exists.
func TestAllTools_ExactlyOneBacklogGet(t *testing.T) {
	tools := allTools()
	count := 0
	for _, tool := range tools {
		if tool.Name == "backlog_get" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 backlog_get tool, found %d", count)
	}
}

// TestListToolSchemas_DeclareLimit is AC21: backlog_list, spec_list, and
// conflicts_list each declare a limit property with type integer, minimum 1,
// maximum 50, and mention the default 20 in their description.
func TestListToolSchemas_DeclareLimit(t *testing.T) {
	tools := allTools()
	for _, name := range []string{"backlog_list", "spec_list", "conflicts_list"} {
		tool := findTool(tools, name)
		if tool == nil {
			t.Fatalf("%s tool not found in allTools()", name)
		}
		schema, ok := tool.InputSchema.(map[string]any)
		if !ok {
			t.Fatalf("%s InputSchema is not map[string]any", name)
		}
		props, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s InputSchema.properties is not map[string]any", name)
		}
		limit, ok := props["limit"].(map[string]any)
		if !ok {
			t.Fatalf("%s does not declare a 'limit' property", name)
		}
		if limit["type"] != "integer" {
			t.Errorf("%s limit.type = %v, want integer", name, limit["type"])
		}
		if limit["minimum"] != 1 {
			t.Errorf("%s limit.minimum = %v, want 1", name, limit["minimum"])
		}
		if limit["maximum"] != 50 {
			t.Errorf("%s limit.maximum = %v, want 50", name, limit["maximum"])
		}
		desc, _ := limit["description"].(string)
		if !strings.Contains(desc, "20") {
			t.Errorf("%s limit description does not mention the default 20: %q", name, desc)
		}
	}
}

// TestBacklogGetSchema_RequiresID verifies backlog_get's schema requires id.
func TestBacklogGetSchema_RequiresID(t *testing.T) {
	tools := allTools()
	tool := findTool(tools, "backlog_get")
	if tool == nil {
		t.Fatal("backlog_get tool not found in allTools()")
	}
	schema, ok := tool.InputSchema.(map[string]any)
	if !ok {
		t.Fatal("backlog_get InputSchema is not map[string]any")
	}
	required, ok := schema["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "id" {
		t.Errorf("backlog_get required = %v, want [\"id\"]", schema["required"])
	}
}

// TestBacklogRefineSchema_ByIsOptional is AC20: backlog_refine declares `by`
// in properties but does NOT require it (SPEC-110 D5/D19) — the field is
// backward compatible with every existing caller that omits it.
func TestBacklogRefineSchema_ByIsOptional(t *testing.T) {
	tools := allTools()
	tool := findTool(tools, "backlog_refine")
	if tool == nil {
		t.Fatal("backlog_refine tool not found in allTools()")
	}
	schema, ok := tool.InputSchema.(map[string]any)
	if !ok {
		t.Fatal("backlog_refine InputSchema is not map[string]any")
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("backlog_refine InputSchema.properties is not map[string]any")
	}
	if _, ok := props["by"]; !ok {
		t.Error("backlog_refine does not declare a 'by' property")
	}
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatal("backlog_refine InputSchema.required is not []string")
	}
	for _, r := range required {
		if r == "by" {
			t.Error("backlog_refine must NOT require 'by' — it is optional (D5)")
		}
	}
}

// findTool returns the ToolDefinition named name, or nil if absent.
func findTool(tools []ToolDefinition, name string) *ToolDefinition {
	for i := range tools {
		if tools[i].Name == name {
			return &tools[i]
		}
	}
	return nil
}

// propertyDescription extracts InputSchema.properties[prop].description from
// a tool's schema, or "" if any step of that path is missing/malformed.
func propertyDescription(t *testing.T, tool *ToolDefinition, prop string) string {
	t.Helper()
	schema, ok := tool.InputSchema.(map[string]any)
	if !ok {
		t.Fatalf("%s InputSchema is not map[string]any", tool.Name)
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("%s InputSchema.properties is not map[string]any", tool.Name)
	}
	field, ok := props[prop].(map[string]any)
	if !ok {
		t.Fatalf("%s property %q missing or malformed", tool.Name, prop)
	}
	desc, _ := field["description"].(string)
	return desc
}

// TestMemSaveSessionIDDescription_MentionsSessionStartBlock verifies the
// mem_save session_id property's description points the agent at the
// mneme:session block the SessionStart hook emits (SPEC-108 D14) — the
// propagation path that keeps the orphan detector from being inert.
func TestMemSaveSessionIDDescription_MentionsSessionStartBlock(t *testing.T) {
	tools := allTools()
	memSave := findTool(tools, "mem_save")
	if memSave == nil {
		t.Fatal("mem_save tool not found in allTools()")
	}

	desc := propertyDescription(t, memSave, "session_id")
	if !strings.Contains(desc, "SessionStart") {
		t.Errorf("mem_save session_id description does not mention the SessionStart block: %q", desc)
	}
}

// TestMemSessionEndDescription_MentionsOptionalMetrics verifies both the
// tool-level Description and the session_id property's description explain
// that memories_created/session_duration are absent without session_id
// (SPEC-108 D13/D14).
func TestMemSessionEndDescription_MentionsOptionalMetrics(t *testing.T) {
	tools := allTools()
	sessionEnd := findTool(tools, "mem_session_end")
	if sessionEnd == nil {
		t.Fatal("mem_session_end tool not found in allTools()")
	}

	if !strings.Contains(sessionEnd.Description, "memories_created") ||
		!strings.Contains(sessionEnd.Description, "session_duration") {
		t.Errorf("mem_session_end description does not explain the optional metrics: %q", sessionEnd.Description)
	}

	desc := propertyDescription(t, sessionEnd, "session_id")
	if !strings.Contains(desc, "SessionStart") {
		t.Errorf("mem_session_end session_id description does not mention the SessionStart block: %q", desc)
	}
}
