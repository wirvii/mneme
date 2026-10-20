package mcp

import (
	"strings"
	"testing"
)

// TestAllTools_Count31 pins the integrated MCP surface. Deltas from the 87
// tools before v2.0, one line per spec:
//
//	SPEC-158: -5  (quality_*)                                    87 -> 82
//	SPEC-163: -23 (skills_*, model_*, conflicts_*, subagent_*, sdd_status, sdd_import)
//	SPEC-165: -6  (codegraph_callees, explore, files, node, status, trace)
//	SPEC-161: -2  (speech_*)
//	SPEC-162: -11 (profile_*, project_new, app_add, scaffold_capture)
//	SPEC-164: -9  (mem_promote, mem_forget, mem_gaps, mem_explore, mem_timeline, mem_stats,
//	              mem_checkpoint, mem_relate, mem_suggest_topic_key)
//	82 - 23 - 6 - 2 - 11 - 9 = 31. Later specs adjust the number and append their delta.
func TestAllTools_Count31(t *testing.T) {
	tools := allTools()
	if len(tools) != 31 {
		t.Errorf("allTools() returned %d tools, want 31", len(tools))
	}
}

// retiredAdminTools are the 23 tools SPEC-163 removed from the MCP server.
// Their capability lives on in the CLI (mneme skills|model|conflicts|sdd|
// subagents ...).
var retiredAdminTools = []string{
	"skills_list", "skills_install", "skills_pin", "skills_unpin",
	"skills_remove", "skills_lint", "skills_validate",
	"model_list", "model_set", "model_reset",
	"conflicts_candidates", "conflicts_scan", "conflicts_link", "conflicts_unlink", "conflicts_list",
	"subagent_fingerprint", "subagent_profile_get", "subagent_profile_save",
	"subagent_compose", "subagent_write", "subagent_manifest_list",
	"sdd_status", "sdd_import",
}

// TestAllTools_NoRetiredAdminTools is SPEC-163 C1: none of the 23 retired
// tools is declared any more, while init (the one administration tool that
// stays) still is — the positive anchor keeps this from passing vacuously.
func TestAllTools_NoRetiredAdminTools(t *testing.T) {
	tools := allTools()
	for _, name := range retiredAdminTools {
		if findTool(tools, name) != nil {
			t.Errorf("allTools() still declares retired tool %q", name)
		}
	}
	if findTool(tools, "init") == nil {
		t.Fatal("allTools() has no init tool — this test proves nothing without a positive anchor")
	}
}

// TestAllTools_NoProfileFamily covers SPEC-162 C1: neither the team-profile
// tools nor the project-template tools are declared any more. The positive
// anchor keeps the test from passing vacuously. It deliberately asserts only
// the absence of the 11 retired tools: the per-project subagent profile tools
// are a different concept and their presence is not this test's business.
func TestAllTools_NoProfileFamily(t *testing.T) {
	retired := map[string]bool{"project_new": true, "app_add": true, "scaffold_capture": true}
	seen := map[string]bool{}
	for _, tool := range allTools() {
		seen[tool.Name] = true
		if strings.HasPrefix(tool.Name, "profile_") || retired[tool.Name] {
			t.Errorf("allTools() still declares %q, want it retired", tool.Name)
		}
	}
	if !seen["spec_advance"] {
		t.Fatal("allTools() has no spec_advance — this test proves nothing without a positive anchor")
	}
}

// TestAllTools_NoSpeechFamily covers SPEC-161 C1: no tool name starts with
// speech_ any more. The positive anchor (spec_advance) keeps the test from
// passing vacuously against an empty tool list.
func TestAllTools_NoSpeechFamily(t *testing.T) {
	sawSpecAdvance := false
	for _, tool := range allTools() {
		if tool.Name == "spec_advance" {
			sawSpecAdvance = true
		}
		if strings.HasPrefix(tool.Name, "speech_") {
			t.Errorf("allTools() still declares %q, want the speech_ family entirely retired", tool.Name)
		}
	}
	if !sawSpecAdvance {
		t.Fatal("allTools() has no spec_advance — this test proves nothing without a positive anchor")
	}
}

// TestToolsList_MemSurfaceIsSix is SPEC-164 AC1: the MCP server exposes
// exactly six mem_* tools; the other nine moved to the CLI or were absorbed.
func TestToolsList_MemSurfaceIsSix(t *testing.T) {
	want := map[string]bool{
		"mem_save": true, "mem_search": true, "mem_get": true,
		"mem_context": true, "mem_update": true, "mem_session_end": true,
	}
	got := map[string]bool{}
	for _, tool := range allTools() {
		if strings.HasPrefix(tool.Name, "mem_") {
			got[tool.Name] = true
		}
	}
	if len(got) != len(want) {
		t.Fatalf("mem_* tools = %v, want exactly %v", got, want)
	}
	for name := range want {
		if !got[name] {
			t.Errorf("missing %s", name)
		}
	}
}

// TestAllTools_NoQualityFamily covers SPEC-158 AC10: no tool name starts
// with quality_ any more — asked of allTools() directly, never of prose.
// Positive anchor: spec_advance must still be present, so this test cannot
// pass vacuously against an empty tool list.
func TestAllTools_NoQualityFamily(t *testing.T) {
	tools := allTools()
	sawSpecAdvance := false
	for _, tool := range tools {
		if tool.Name == "spec_advance" {
			sawSpecAdvance = true
		}
		if strings.HasPrefix(tool.Name, "quality_") {
			t.Errorf("allTools() still declares %q, want the quality_ family entirely retired", tool.Name)
		}
	}
	if !sawSpecAdvance {
		t.Fatal("allTools() has no spec_advance — this test proves nothing without a positive anchor")
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

// TestListToolSchemas_DeclareLimit is AC21: backlog_list and spec_list
// each declare a limit property with type integer, minimum 1,
// maximum 50, and mention the default 20 in their description.
func TestListToolSchemas_DeclareLimit(t *testing.T) {
	tools := allTools()
	for _, name := range []string{"backlog_list", "spec_list"} {
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

// TestBacklogArchiveSchema_RequiresIDAndReason is SPEC-125 AC36:
// backlog_archive declares both "id" and "reason" as required properties.
func TestBacklogArchiveSchema_RequiresIDAndReason(t *testing.T) {
	tools := allTools()
	tool := findTool(tools, "backlog_archive")
	if tool == nil {
		t.Fatal("backlog_archive tool not found in allTools()")
	}
	schema, ok := tool.InputSchema.(map[string]any)
	if !ok {
		t.Fatal("backlog_archive InputSchema is not map[string]any")
	}

	required, ok := schema["required"].([]string)
	if !ok || len(required) != 2 || required[0] != "id" || required[1] != "reason" {
		t.Errorf(`backlog_archive required = %v, want ["id", "reason"]`, schema["required"])
	}

	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("backlog_archive properties is not map[string]any")
	}
	for _, name := range []string{"id", "reason"} {
		prop, ok := properties[name].(map[string]any)
		if !ok {
			t.Fatalf("backlog_archive properties missing %q", name)
		}
		if prop["type"] != "string" {
			t.Errorf("backlog_archive properties[%q].type = %v, want \"string\"", name, prop["type"])
		}
		if desc, _ := prop["description"].(string); desc == "" {
			t.Errorf("backlog_archive properties[%q] has no description", name)
		}
	}
}
