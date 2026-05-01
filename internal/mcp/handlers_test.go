package mcp

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/db"
	"github.com/juanftp/mneme/internal/embed"
	"github.com/juanftp/mneme/internal/service"
	"github.com/juanftp/mneme/internal/store"
)

// newTestServerWithSDD creates a Server backed by in-memory SQLite databases
// with the full SDD service initialised. Suitable for tests that exercise SDD
// tool handlers.
func newTestServerWithSDD(t *testing.T) *Server {
	t.Helper()

	projectDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open project db: %v", err)
	}
	projectDB.SetMaxOpenConns(1)
	globalDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open global db: %v", err)
	}
	globalDB.SetMaxOpenConns(1)
	t.Cleanup(func() { projectDB.Close(); globalDB.Close() })

	projectStore := store.NewMemoryStore(projectDB)
	globalStore := store.NewMemoryStore(globalDB)
	cfg := config.Default()
	svc := service.NewMemoryService(projectStore, globalStore, cfg, "test-project", embed.NopEmbedder{})

	sddStore := store.NewSDDStore(projectDB)
	sddSvc := service.NewSDDService(sddStore, cfg, "test-project", svc)

	logger := slog.Default()
	return NewServer(svc, sddSvc, logger, "all", "test")
}

// TestMapServiceError_InternalErrorIncludesMessage is a regression test for the
// mapServiceError fix. When a store-layer error (not a sentinel) reaches a
// handler, the JSON-RPC error message must contain the actual error text rather
// than the opaque "internal error" string.
//
// We trigger a real constraint violation by promoting the same backlog item
// twice via backlog_promote after manipulating the spec table to force a
// duplicate-ID condition that the service layer will surface as a store error.
func TestMapServiceError_InternalErrorIncludesMessage(t *testing.T) {
	srv := newTestServerWithSDD(t)

	// Add a backlog item and refine it so it can be promoted.
	addResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "backlog_add",
		Arguments: mustMarshal(t, map[string]any{
			"title":       "Test item for error propagation",
			"description": "Checking that store errors reach the caller.",
		}),
	})
	if addResp.Error != nil {
		t.Fatalf("backlog_add: %v", addResp.Error.Message)
	}

	var addResult struct {
		ID string `json:"id"`
	}
	unmarshalToolText(t, addResp, &addResult)

	// Refine the item (required before promote).
	refineResp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name: "backlog_refine",
		Arguments: mustMarshal(t, map[string]any{
			"id":          addResult.ID,
			"description": "Refined description with enough detail.",
		}),
	})
	if refineResp.Error != nil {
		t.Fatalf("backlog_refine: %v", refineResp.Error.Message)
	}

	// First promote — should succeed.
	promoteResp := process(t, srv, "tools/call", 3, ToolCallParams{
		Name:      "backlog_promote",
		Arguments: mustMarshal(t, map[string]any{"id": addResult.ID}),
	})
	if promoteResp.Error != nil {
		t.Fatalf("first backlog_promote: %v", promoteResp.Error.Message)
	}

	// Second promote of the same item — the backlog item is already promoted
	// (status is no longer "refined"), so the service should return an error.
	promoteResp2 := process(t, srv, "tools/call", 4, ToolCallParams{
		Name:      "backlog_promote",
		Arguments: mustMarshal(t, map[string]any{"id": addResult.ID}),
	})
	if promoteResp2.Error == nil {
		t.Fatal("expected second backlog_promote to fail, got nil error")
	}

	// The error message must not be the old opaque string.
	if promoteResp2.Error.Message == "internal error" {
		t.Error("error message is still the opaque 'internal error'; expected real error detail")
	}
	// The message must be prefixed with the tool name.
	if !strings.HasPrefix(promoteResp2.Error.Message, "mcp: handle backlog_promote:") {
		t.Errorf("error message %q does not start with expected prefix", promoteResp2.Error.Message)
	}
}

// TestMapServiceError_SentinelCodesUnchanged verifies that sentinel errors
// (validation, not-found) still map to their specific JSON-RPC codes and are
// not accidentally lumped into CodeInternalError by the new fallback path.
func TestMapServiceError_SentinelCodesUnchanged(t *testing.T) {
	srv := newTestServerWithSDD(t)

	// mem_get with a non-existent ID must return CodeMemoryNotFound (-32001 or similar).
	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "mem_get",
		Arguments: mustMarshal(t, map[string]any{"id": "00000000-0000-0000-0000-000000000000"}),
	})
	if resp.Error == nil {
		t.Fatal("expected error for non-existent memory, got nil")
	}
	if resp.Error.Code == CodeInternalError {
		t.Errorf("expected a sentinel code (not-found), got CodeInternalError; message: %s", resp.Error.Message)
	}

	// mem_save with missing required fields must return CodeInvalidParams.
	saveResp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name:      "mem_save",
		Arguments: mustMarshal(t, map[string]any{}), // missing title + content
	})
	if saveResp.Error == nil {
		t.Fatal("expected error for empty mem_save, got nil")
	}
	if saveResp.Error.Code == CodeInternalError {
		t.Errorf("expected CodeInvalidParams, got CodeInternalError; message: %s", saveResp.Error.Message)
	}
}

// TestMCP_MemSave_RuleCreated verifies that a valid rule JSON-RPC request
// creates a memory and returns an "action":"created" response.
func TestMCP_MemSave_RuleCreated(t *testing.T) {
	srv := newTestServerWithSDD(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "mem_save",
		Arguments: mustMarshal(t, map[string]any{
			"title":      "Never use time.Now() directly",
			"content":    "Use the injected clock from the service constructor.",
			"type":       "rule",
			"scope":      "project",
			"topic_key":  "rule/no-time-now",
			"applies_to": []string{"internal/**/*.go", "!internal/**/*_test.go"},
			"severity":   "warn",
		}),
	})
	if resp.Error != nil {
		t.Fatalf("mem_save rule: unexpected error code=%d message=%s", resp.Error.Code, resp.Error.Message)
	}

	var result struct {
		Action   string `json:"action"`
		TopicKey string `json:"topic_key"`
	}
	unmarshalToolText(t, resp, &result)

	if result.Action != "created" {
		t.Errorf("action = %q, want %q", result.Action, "created")
	}
	if result.TopicKey != "rule/no-time-now" {
		t.Errorf("topic_key = %q, want %q", result.TopicKey, "rule/no-time-now")
	}
}

// TestMCP_MemSave_RuleMissingAppliesTo verifies that a rule without applies_to
// returns JSON-RPC error code -32602 (CodeInvalidParams).
func TestMCP_MemSave_RuleMissingAppliesTo(t *testing.T) {
	srv := newTestServerWithSDD(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "mem_save",
		Arguments: mustMarshal(t, map[string]any{
			"title":    "Rule without applies_to",
			"content":  "Should fail.",
			"type":     "rule",
			"severity": "warn",
			// applies_to intentionally omitted
		}),
	})
	if resp.Error == nil {
		t.Fatal("expected error, got nil")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want %d (CodeInvalidParams)", resp.Error.Code, CodeInvalidParams)
	}
}

// TestMCP_MemSave_NonRuleWithAppliesTo verifies that a non-rule memory with
// applies_to returns JSON-RPC error code -32602 (CodeInvalidParams).
func TestMCP_MemSave_NonRuleWithAppliesTo(t *testing.T) {
	srv := newTestServerWithSDD(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "mem_save",
		Arguments: mustMarshal(t, map[string]any{
			"title":      "Architecture with applies_to",
			"content":    "Should fail.",
			"type":       "architecture",
			"applies_to": []string{"internal/**"},
		}),
	})
	if resp.Error == nil {
		t.Fatal("expected error, got nil")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want %d (CodeInvalidParams)", resp.Error.Code, CodeInvalidParams)
	}
}

// TestMCP_MemRelate_WithWeight verifies that mem_relate accepts an explicit weight
// and returns it in the response.
func TestMCP_MemRelate_WithWeight(t *testing.T) {
	srv := newTestServerWithSDD(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "mem_relate",
		Arguments: mustMarshal(t, map[string]any{
			"source":      "auth-service",
			"target":      "jwt-library",
			"relation":    "depends_on",
			"source_kind": "service",
			"target_kind": "library",
			"weight":      0.95,
		}),
	})
	if resp.Error != nil {
		t.Fatalf("mem_relate with weight: %v", resp.Error.Message)
	}

	var result struct {
		Weight  float64 `json:"weight"`
		Created bool    `json:"created"`
	}
	unmarshalToolText(t, resp, &result)

	if result.Weight != 0.95 {
		t.Errorf("weight = %v, want 0.95", result.Weight)
	}
	if !result.Created {
		t.Error("expected created=true")
	}
}

// TestMCP_MemRelate_InvalidWeight verifies that an out-of-range weight is rejected
// with code CodeInvalidParams.
func TestMCP_MemRelate_InvalidWeight(t *testing.T) {
	srv := newTestServerWithSDD(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "mem_relate",
		Arguments: mustMarshal(t, map[string]any{
			"source":   "a",
			"target":   "b",
			"relation": "uses",
			"weight":   1.5,
		}),
	})
	if resp.Error == nil {
		t.Fatal("expected error for weight=1.5, got nil")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want %d (CodeInvalidParams)", resp.Error.Code, CodeInvalidParams)
	}
}

// TestMCP_MemRelate_ReferencesType verifies that relation type "references" is
// accepted and receives the default weight 0.4.
func TestMCP_MemRelate_ReferencesType(t *testing.T) {
	srv := newTestServerWithSDD(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "mem_relate",
		Arguments: mustMarshal(t, map[string]any{
			"source":   "note-a",
			"target":   "note-b",
			"relation": "references",
		}),
	})
	if resp.Error != nil {
		t.Fatalf("mem_relate with references type: %v", resp.Error.Message)
	}

	var result struct {
		Weight float64 `json:"weight"`
	}
	unmarshalToolText(t, resp, &result)

	const wantWeight = 0.4 // DefaultRelationWeights[RelReferences]
	if result.Weight != wantWeight {
		t.Errorf("weight = %v, want %v", result.Weight, wantWeight)
	}
}

// TestMCP_ToolSchema_IncludesRuleFields verifies that the mem_save tool schema
// in allTools() includes the applies_to and severity fields and that the type
// enum contains "rule".
func TestMCP_ToolSchema_IncludesRuleFields(t *testing.T) {
	tools := allTools()

	var memSave *ToolDefinition
	for i := range tools {
		if tools[i].Name == "mem_save" {
			memSave = &tools[i]
			break
		}
	}
	if memSave == nil {
		t.Fatal("mem_save tool not found in allTools()")
	}

	schema, ok := memSave.InputSchema.(map[string]any)
	if !ok {
		t.Fatal("mem_save.InputSchema is not map[string]any")
	}

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("mem_save.InputSchema.properties is not map[string]any")
	}

	if _, ok := props["applies_to"]; !ok {
		t.Error("mem_save schema is missing applies_to property")
	}
	if _, ok := props["severity"]; !ok {
		t.Error("mem_save schema is missing severity property")
	}

	typeProp, ok := props["type"].(map[string]any)
	if !ok {
		t.Fatal("mem_save schema 'type' property is not map[string]any")
	}
	enumVal, ok := typeProp["enum"].([]string)
	if !ok {
		t.Fatal("mem_save schema 'type' enum is not []string")
	}
	hasRule := false
	for _, v := range enumVal {
		if v == "rule" {
			hasRule = true
			break
		}
	}
	if !hasRule {
		t.Errorf("mem_save type enum does not include 'rule': %v", enumVal)
	}
}

// TestMemSearch_IncludeGraph_Default verifies that calling mem_search without
// the include_graph parameter succeeds and returns a result (graph is on by
// default through the config, but the param is optional).
func TestMemSearch_IncludeGraph_Default(t *testing.T) {
	srv := newTestServerWithSDD(t)

	// Save a memory so there is something to search for.
	saveResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "mem_save",
		Arguments: mustMarshal(t, map[string]any{
			"title":   "graph expansion default test",
			"content": "Testing that mem_search works with default include_graph behaviour.",
			"type":    "discovery",
		}),
	})
	if saveResp.Error != nil {
		t.Fatalf("mem_save: %v", saveResp.Error.Message)
	}

	// Search without specifying include_graph — handler must accept omitted param.
	resp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name: "mem_search",
		Arguments: mustMarshal(t, map[string]any{
			"query": "graph expansion default",
			// include_graph intentionally omitted — should default to config value (true)
		}),
	})
	if resp.Error != nil {
		t.Fatalf("mem_search (default include_graph): %v", resp.Error.Message)
	}

	var result struct {
		Total int `json:"total"`
	}
	unmarshalToolText(t, resp, &result)
	if result.Total == 0 {
		t.Error("expected at least one search result")
	}
}

// TestMemSearch_IncludeGraph_False verifies that passing include_graph=false
// to mem_search is accepted without error. The SearchRequest.IncludeGraph field
// must be set to false so the service skips the graph expansion path.
func TestMemSearch_IncludeGraph_False(t *testing.T) {
	srv := newTestServerWithSDD(t)

	// Save a memory so search returns results.
	saveResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "mem_save",
		Arguments: mustMarshal(t, map[string]any{
			"title":   "graph expansion disabled test",
			"content": "Testing that mem_search works when include_graph is explicitly false.",
			"type":    "discovery",
		}),
	})
	if saveResp.Error != nil {
		t.Fatalf("mem_save: %v", saveResp.Error.Message)
	}

	// Search with include_graph=false — must succeed and return results.
	resp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name: "mem_search",
		Arguments: mustMarshal(t, map[string]any{
			"query":         "graph expansion disabled",
			"include_graph": false,
		}),
	})
	if resp.Error != nil {
		t.Fatalf("mem_search (include_graph=false): %v", resp.Error.Message)
	}

	var result struct {
		Total int `json:"total"`
	}
	unmarshalToolText(t, resp, &result)
	if result.Total == 0 {
		t.Error("expected at least one search result even with include_graph=false")
	}
}

// TestMCP_ToolSchema_MemRelateIncludesWeight verifies that the mem_relate tool
// schema includes the weight property and the references enum value.
func TestMCP_ToolSchema_MemRelateIncludesWeight(t *testing.T) {
	tools := allTools()

	var memRelate *ToolDefinition
	for i := range tools {
		if tools[i].Name == "mem_relate" {
			memRelate = &tools[i]
			break
		}
	}
	if memRelate == nil {
		t.Fatal("mem_relate tool not found in allTools()")
	}

	schema, ok := memRelate.InputSchema.(map[string]any)
	if !ok {
		t.Fatal("mem_relate.InputSchema is not map[string]any")
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("mem_relate.InputSchema.properties is not map[string]any")
	}

	// Verify weight property is present.
	if _, ok := props["weight"]; !ok {
		t.Error("mem_relate schema is missing weight property")
	}

	// Verify references is in the relation enum.
	relProp, ok := props["relation"].(map[string]any)
	if !ok {
		t.Fatal("mem_relate schema 'relation' property is not map[string]any")
	}
	enumVal, ok := relProp["enum"].([]string)
	if !ok {
		t.Fatal("mem_relate schema 'relation' enum is not []string")
	}
	hasReferences := false
	for _, v := range enumVal {
		if v == "references" {
			hasReferences = true
			break
		}
	}
	if !hasReferences {
		t.Errorf("mem_relate relation enum does not include 'references': %v", enumVal)
	}
}

// ─── mem_explore tests ────────────────────────────────────────────────────────

// TestMCP_MemExplore_Schema verifies that mem_explore is present in allTools()
// with the required "seed" property in the input schema.
func TestMCP_MemExplore_Schema(t *testing.T) {
	tools := allTools()
	var exploreTool *ToolDefinition
	for i := range tools {
		if tools[i].Name == "mem_explore" {
			exploreTool = &tools[i]
			break
		}
	}
	if exploreTool == nil {
		t.Fatal("mem_explore not found in allTools()")
	}
	schema, ok := exploreTool.InputSchema.(map[string]any)
	if !ok {
		t.Fatal("InputSchema is not map[string]any")
	}
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatal("required is not []string")
	}
	hasSeeded := false
	for _, r := range required {
		if r == "seed" {
			hasSeeded = true
		}
	}
	if !hasSeeded {
		t.Errorf("mem_explore schema required does not include 'seed': %v", required)
	}
}

// TestMCP_MemExplore_SeedRequired verifies that omitting seed returns -32602.
func TestMCP_MemExplore_SeedRequired(t *testing.T) {
	srv := newTestServer(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "mem_explore",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if resp.Error == nil {
		t.Fatal("expected error for missing seed, got nil")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("expected -32602, got %d: %s", resp.Error.Code, resp.Error.Message)
	}
}

// TestMCP_MemExplore_SeedNotFound verifies that a nonexistent UUID seed
// returns -32000 (CodeMemoryNotFound).
func TestMCP_MemExplore_SeedNotFound(t *testing.T) {
	srv := newTestServer(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "mem_explore",
		Arguments: mustMarshal(t, map[string]any{
			"seed":  "00000000-0000-7000-8000-000000000000",
			"depth": 1,
		}),
	})
	if resp.Error == nil {
		t.Fatal("expected error for nonexistent seed, got nil")
	}
	if resp.Error.Code != CodeMemoryNotFound {
		t.Errorf("expected -32000, got %d: %s", resp.Error.Code, resp.Error.Message)
	}
}

// TestMCP_MemExplore_Basic verifies a successful end-to-end roundtrip: save a
// memory, call mem_explore, receive a valid ExploreResponse JSON.
func TestMCP_MemExplore_Basic(t *testing.T) {
	srv := newTestServer(t)

	// Save a memory to use as seed.
	saveResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "mem_save",
		Arguments: mustMarshal(t, map[string]any{
			"title":   "MCP explore seed",
			"content": "seed memory for MCP explore test",
		}),
	})
	if saveResp.Error != nil {
		t.Fatalf("mem_save: %v", saveResp.Error.Message)
	}
	var saved struct {
		ID string `json:"id"`
	}
	unmarshalToolText(t, saveResp, &saved)
	if saved.ID == "" {
		t.Fatal("expected non-empty ID from mem_save")
	}

	// Explore from the saved memory.
	exploreResp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name: "mem_explore",
		Arguments: mustMarshal(t, map[string]any{
			"seed":  saved.ID,
			"depth": 1,
		}),
	})
	if exploreResp.Error != nil {
		t.Fatalf("mem_explore: %v", exploreResp.Error.Message)
	}

	var result struct {
		SeedID     string `json:"seed_id"`
		TotalNodes int    `json:"total_nodes"`
	}
	unmarshalToolText(t, exploreResp, &result)
	if result.SeedID != saved.ID {
		t.Errorf("seed_id: got %q, want %q", result.SeedID, saved.ID)
	}
}
