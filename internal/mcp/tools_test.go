package mcp

import (
	"strings"
	"testing"
)

// TestAllTools_Count76 verifies that describing session_id's role in
// mem_save/mem_session_end (SPEC-108 D14/step 8) changed no tool's shape —
// only two Description strings. 76 is the count established by SPEC-105 §8
// DD21 (profile_deactivate, 75 -> 76); this spec adds zero tools.
func TestAllTools_Count76(t *testing.T) {
	tools := allTools()
	if len(tools) != 76 {
		t.Errorf("allTools() returned %d tools, want 76", len(tools))
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
