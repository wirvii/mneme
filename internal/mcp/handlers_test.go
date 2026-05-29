package mcp

import (
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/db"
	"github.com/juanftp/mneme/internal/embed"
	"github.com/juanftp/mneme/internal/lane"
	"github.com/juanftp/mneme/internal/model"
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
	return NewServer(svc, sddSvc, nil, logger, "all", "test")
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
			"lane":        "standard",
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

// newTestServerWithStore creates a Server like newTestServer but also returns the
// underlying project MemoryStore so tests can do direct SQL manipulation (e.g.
// to manufacture UUID prefix collisions that cannot be produced via the API).
func newTestServerWithStore(t *testing.T) (*Server, *store.MemoryStore) {
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

	ps := store.NewMemoryStore(projectDB)
	gs := store.NewMemoryStore(globalDB)
	cfg := config.Default()
	svc := service.NewMemoryService(ps, gs, cfg, "test-project", embed.NopEmbedder{})

	logger := slog.Default()
	return NewServer(svc, nil, nil, logger, "all", "test"), ps
}

// TestMCP_MemExplore_DepthExceeded verifies that supplying a depth value above
// the allowed maximum (5) returns an error response. The handler delegates to
// the service which validates depth ∈ [0, 5].
func TestMCP_MemExplore_DepthExceeded(t *testing.T) {
	srv := newTestServer(t)

	// Save a seed memory to prevent a "not found" error masking the depth error.
	saveResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "mem_save",
		Arguments: mustMarshal(t, map[string]any{
			"title":   "depth-exceeded seed",
			"content": "seed for depth exceeded test",
		}),
	})
	if saveResp.Error != nil {
		t.Fatalf("mem_save: %v", saveResp.Error.Message)
	}
	var saved struct {
		ID string `json:"id"`
	}
	unmarshalToolText(t, saveResp, &saved)

	resp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name: "mem_explore",
		Arguments: mustMarshal(t, map[string]any{
			"seed":  saved.ID,
			"depth": 6, // above the maximum of 5
		}),
	})
	if resp.Error == nil {
		t.Fatal("expected error for depth=6, got nil")
	}
	// depth out of range is a non-sentinel fmt.Errorf from the service, so it
	// maps to CodeInternalError (-32603).
	if resp.Error.Code != CodeInternalError {
		t.Errorf("expected -32603 (CodeInternalError) for depth exceeded, got %d: %s",
			resp.Error.Code, resp.Error.Message)
	}
}

// TestMCP_MemExplore_SeedAmbiguous verifies that when a short UUID prefix
// matches more than one memory, the handler returns -32602 (CodeInvalidParams).
func TestMCP_MemExplore_SeedAmbiguous(t *testing.T) {
	srv, ps := newTestServerWithStore(t)
	ctx := t.Context()

	// Save two memories.
	save1 := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "mem_save",
		Arguments: mustMarshal(t, map[string]any{
			"title":   "ambiguous mcp seed 1",
			"content": "content 1",
		}),
	})
	if save1.Error != nil {
		t.Fatalf("mem_save 1: %v", save1.Error.Message)
	}
	var saved1 struct{ ID string `json:"id"` }
	unmarshalToolText(t, save1, &saved1)

	save2 := process(t, srv, "tools/call", 2, ToolCallParams{
		Name: "mem_save",
		Arguments: mustMarshal(t, map[string]any{
			"title":   "ambiguous mcp seed 2",
			"content": "content 2",
		}),
	})
	if save2.Error != nil {
		t.Fatalf("mem_save 2: %v", save2.Error.Message)
	}
	var saved2 struct{ ID string `json:"id"` }
	unmarshalToolText(t, save2, &saved2)

	// Force both IDs to share the same 8-char hex prefix via direct SQL.
	commonHex := "eeff0011"
	_, err := ps.DB().ExecContext(ctx,
		"UPDATE memories SET id = ? WHERE id = ?",
		commonHex+"0000-0000-0000-000000000001",
		saved1.ID,
	)
	if err != nil {
		t.Fatalf("direct ID update 1: %v", err)
	}
	_, err = ps.DB().ExecContext(ctx,
		"UPDATE memories SET id = ? WHERE id = ?",
		commonHex+"0000-0000-0000-000000000002",
		saved2.ID,
	)
	if err != nil {
		t.Fatalf("direct ID update 2: %v", err)
	}

	// Call mem_explore with the common prefix — should trigger ErrAmbiguousSeed.
	resp := process(t, srv, "tools/call", 3, ToolCallParams{
		Name: "mem_explore",
		Arguments: mustMarshal(t, map[string]any{
			"seed":  commonHex,
			"depth": 1,
		}),
	})
	if resp.Error == nil {
		t.Fatal("expected ErrAmbiguousSeed error, got nil")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("expected -32602 (CodeInvalidParams) for ambiguous seed, got %d: %s",
			resp.Error.Code, resp.Error.Message)
	}
}

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

// TestMemGaps_Empty verifies that mem_gaps returns a valid GapsResponse with an
// empty gaps array and total=0 when there are no unresolved references.
func TestMemGaps_Empty(t *testing.T) {
	srv := newTestServer(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "mem_gaps",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if resp.Error != nil {
		t.Fatalf("mem_gaps error: %s", resp.Error.Message)
	}

	var result struct {
		Gaps  []any `json:"gaps"`
		Total int   `json:"total"`
	}
	unmarshalToolText(t, resp, &result)

	if result.Gaps == nil {
		t.Error("expected non-nil gaps array")
	}
	if len(result.Gaps) != 0 {
		t.Errorf("expected 0 gaps, got %d", len(result.Gaps))
	}
	if result.Total != 0 {
		t.Errorf("expected total=0, got %d", result.Total)
	}
}

// TestMemGaps_Success verifies that mem_gaps returns a valid GapsResponse when
// unresolved references exist. It saves a memory with a [[wikilink]] to a
// non-existent topic_key, then calls mem_gaps and asserts the gap appears.
func TestMemGaps_Success(t *testing.T) {
	srv := newTestServer(t)

	// Save a memory with a wikilink to a non-existent topic_key.
	saveResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "mem_save",
		Arguments: mustMarshal(t, map[string]any{
			"title":     "Source memory",
			"content":   "See [[missing/gap-topic]] for details.",
			"topic_key": "source/mem",
			"type":      "decision",
		}),
	})
	if saveResp.Error != nil {
		t.Fatalf("mem_save: %s", saveResp.Error.Message)
	}

	// Call mem_gaps — the wikilink should have registered an unresolved ref.
	resp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name: "mem_gaps",
		Arguments: mustMarshal(t, map[string]any{
			"scope": "project",
		}),
	})
	if resp.Error != nil {
		t.Fatalf("mem_gaps: %s", resp.Error.Message)
	}

	var result struct {
		Gaps []struct {
			TargetTopicKey string `json:"target_topic_key"`
			TotalMentions  int    `json:"total_mentions"`
			SourceCount    int    `json:"source_count"`
		} `json:"gaps"`
		Total int `json:"total"`
	}
	unmarshalToolText(t, resp, &result)

	if result.Total == 0 {
		t.Fatal("expected total > 0 after saving a memory with an unresolved wikilink")
	}
	if len(result.Gaps) == 0 {
		t.Fatal("expected at least one gap")
	}
	// Find the specific gap we created.
	found := false
	for _, g := range result.Gaps {
		if g.TargetTopicKey == "missing/gap-topic" {
			found = true
			if g.TotalMentions < 1 {
				t.Errorf("TotalMentions = %d, want >= 1", g.TotalMentions)
			}
			if g.SourceCount < 1 {
				t.Errorf("SourceCount = %d, want >= 1", g.SourceCount)
			}
		}
	}
	if !found {
		t.Errorf("gap 'missing/gap-topic' not found in response; got: %+v", result.Gaps)
	}
}

// TestMemSuggestTopicKey_WithGaps verifies that mem_suggest_topic_key returns
// gap_matches in the response when a wikilink creates an unresolved reference
// whose topic_key shares tokens with the queried title.
func TestMemSuggestTopicKey_WithGaps(t *testing.T) {
	srv := newTestServer(t)

	// Save a memory with a wikilink to a non-existent topic_key.
	saveResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "mem_save",
		Arguments: mustMarshal(t, map[string]any{
			"title":   "Source memory about JWT tokens",
			"content": "See [[auth/jwt-setup]] for the token configuration.",
			"type":    "decision",
		}),
	})
	if saveResp.Error != nil {
		t.Fatalf("mem_save: %s", saveResp.Error.Message)
	}

	// Suggest a topic key with a title that shares tokens with the gap.
	resp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name: "mem_suggest_topic_key",
		Arguments: mustMarshal(t, map[string]any{
			"title": "JWT authentication setup",
		}),
	})
	if resp.Error != nil {
		t.Fatalf("mem_suggest_topic_key: %s", resp.Error.Message)
	}

	var result struct {
		Suggestion      string `json:"suggestion"`
		IsNewTopic      bool   `json:"is_new_topic"`
		ExistingMatches []struct {
			TopicKey string  `json:"topic_key"`
			Score    float64 `json:"score"`
		} `json:"existing_matches"`
		GapMatches []struct {
			TopicKey     string  `json:"topic_key"`
			Score        float64 `json:"score"`
			FromGap      bool    `json:"from_gap"`
			PendingCount int     `json:"pending_count"`
			Reason       string  `json:"reason"`
		} `json:"gap_matches"`
	}
	unmarshalToolText(t, resp, &result)

	if result.Suggestion == "" {
		t.Error("expected non-empty suggestion")
	}
	if len(result.GapMatches) == 0 {
		t.Fatal("expected gap_matches to be non-empty")
	}

	found := false
	for _, gm := range result.GapMatches {
		if gm.TopicKey == "auth/jwt-setup" {
			found = true
			if !gm.FromGap {
				t.Error("expected from_gap=true")
			}
			if gm.Score <= 0 {
				t.Errorf("expected positive score, got %f", gm.Score)
			}
			if gm.PendingCount < 1 {
				t.Errorf("expected pending_count >= 1, got %d", gm.PendingCount)
			}
			if gm.Reason == "" {
				t.Error("expected non-empty reason")
			}
			break
		}
	}
	if !found {
		t.Errorf("expected gap_matches to contain auth/jwt-setup, got %+v", result.GapMatches)
	}
}

// TestMemSuggestTopicKey_BackwardCompat verifies that mem_suggest_topic_key
// returns the expected fields for the pre-SPEC-014 case: an existing memory is
// found, suggestion is non-empty, and gap_matches is absent (omitted in JSON).
func TestMemSuggestTopicKey_BackwardCompat(t *testing.T) {
	srv := newTestServer(t)

	// Save a memory with an explicit topic key.
	saveResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "mem_save",
		Arguments: mustMarshal(t, map[string]any{
			"title":     "Authentication model overview",
			"content":   "Describes the authentication architecture.",
			"topic_key": "architecture/auth-model",
			"type":      "architecture",
		}),
	})
	if saveResp.Error != nil {
		t.Fatalf("mem_save: %s", saveResp.Error.Message)
	}

	resp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name: "mem_suggest_topic_key",
		Arguments: mustMarshal(t, map[string]any{
			"title": "Authentication model overview",
		}),
	})
	if resp.Error != nil {
		t.Fatalf("mem_suggest_topic_key: %s", resp.Error.Message)
	}

	var result struct {
		Suggestion      string `json:"suggestion"`
		IsNewTopic      bool   `json:"is_new_topic"`
		ExistingMatches []any  `json:"existing_matches"`
		GapMatches      []any  `json:"gap_matches"`
	}
	unmarshalToolText(t, resp, &result)

	if result.Suggestion == "" {
		t.Error("expected non-empty suggestion")
	}
	if result.IsNewTopic {
		t.Error("expected is_new_topic=false when existing match found")
	}
	if len(result.ExistingMatches) == 0 {
		t.Error("expected at least one existing_match")
	}
	// No gaps registered in this test, so gap_matches must be absent/empty.
	if len(result.GapMatches) != 0 {
		t.Errorf("expected no gap_matches, got %d", len(result.GapMatches))
	}
}

// TestMemGaps_InvalidParams verifies that a non-object arguments value (a JSON
// string instead of an object) causes the handler to return CodeInvalidParams.
// This exercises the json.Unmarshal failure path in handleMemGaps.
func TestMemGaps_InvalidParams(t *testing.T) {
	srv := newTestServer(t)

	// "arguments" is a JSON string — valid JSON at the transport level but
	// fails when the handler tries json.Unmarshal into model.GapsRequest (struct).
	raw := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"mem_gaps","arguments":"not-an-object"}}`)
	resp, hasResp := srv.handleMessage(raw)
	if !hasResp {
		t.Fatal("expected a response")
	}
	if resp.Error == nil {
		t.Fatal("expected error for non-object arguments, got nil")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("expected code %d (CodeInvalidParams), got %d: %s", CodeInvalidParams, resp.Error.Code, resp.Error.Message)
	}
}

// TestMemContext_IncludeGraph_Default verifies that omitting include_graph in a
// mem_context call is accepted and returns memories (graph active by default).
func TestMemContext_IncludeGraph_Default(t *testing.T) {
	srv := newTestServerWithSDD(t)

	saveResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "mem_save",
		Arguments: mustMarshal(t, map[string]any{
			"title":   "context graph default test",
			"content": "Testing mem_context with default include_graph behaviour.",
			"type":    "discovery",
		}),
	})
	if saveResp.Error != nil {
		t.Fatalf("mem_save: %v", saveResp.Error.Message)
	}

	resp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name: "mem_context",
		Arguments: mustMarshal(t, map[string]any{
			"focus": "context graph default",
			// include_graph intentionally omitted — defaults to config value (true)
		}),
	})
	if resp.Error != nil {
		t.Fatalf("mem_context (default include_graph): %v", resp.Error.Message)
	}

	var result struct {
		TotalAvailable int `json:"total_available"`
	}
	unmarshalToolText(t, resp, &result)
	if result.TotalAvailable == 0 {
		t.Error("expected at least one available memory")
	}
}

// TestMemContext_IncludeGraph_False verifies that passing include_graph=false to
// mem_context is accepted without error and graph expansion is disabled.
func TestMemContext_IncludeGraph_False(t *testing.T) {
	srv := newTestServerWithSDD(t)

	saveResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "mem_save",
		Arguments: mustMarshal(t, map[string]any{
			"title":   "context graph disabled test",
			"content": "Testing mem_context with include_graph explicitly false.",
			"type":    "discovery",
		}),
	})
	if saveResp.Error != nil {
		t.Fatalf("mem_save: %v", saveResp.Error.Message)
	}

	resp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name: "mem_context",
		Arguments: mustMarshal(t, map[string]any{
			"focus":         "context graph disabled",
			"include_graph": false,
		}),
	})
	if resp.Error != nil {
		t.Fatalf("mem_context (include_graph=false): %v", resp.Error.Message)
	}

	var result struct {
		TotalAvailable int `json:"total_available"`
	}
	unmarshalToolText(t, resp, &result)
	if result.TotalAvailable == 0 {
		t.Error("expected at least one available memory even with include_graph=false")
	}
}

// TestMCP_ToolSchema_MemContextIncludesIncludeGraph verifies the mem_context
// schema includes the include_graph property after SPEC-017.
func TestMCP_ToolSchema_MemContextIncludesIncludeGraph(t *testing.T) {
	tools := allTools()

	var memCtx *ToolDefinition
	for i := range tools {
		if tools[i].Name == "mem_context" {
			memCtx = &tools[i]
			break
		}
	}
	if memCtx == nil {
		t.Fatal("mem_context tool not found")
	}

	schema, ok := memCtx.InputSchema.(map[string]any)
	if !ok {
		t.Fatal("mem_context InputSchema is not map[string]any")
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("mem_context InputSchema.properties is not map[string]any")
	}

	if _, ok := props["include_graph"]; !ok {
		t.Error("mem_context schema missing include_graph property")
	}
}

// newTestServerWithRepoDir creates an SDD-enabled Server where the SDDService's
// lane auditor uses dir as the git repository root. This lets tests trigger real
// audit runs without depending on the working directory.
func newTestServerWithRepoDir(t *testing.T, dir string) *Server {
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
	sddSvc.WithRepoDir(dir)

	logger := slog.Default()
	return NewServer(svc, sddSvc, nil, logger, "all", "test")
}

// unmarshalToolResult extracts the ToolCallResult from a JSON-RPC response
// without failing when resp.Error is set. It returns the tool result and
// whether the JSON-RPC layer itself returned an error.
func unmarshalToolResult(t *testing.T, resp JSONRPCResponse) (ToolCallResult, bool) {
	t.Helper()
	if resp.Error != nil {
		return ToolCallResult{}, true
	}
	b, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("re-marshal result: %v", err)
	}
	var tr ToolCallResult
	if err := json.Unmarshal(b, &tr); err != nil {
		t.Fatalf("unmarshal ToolCallResult: %v", err)
	}
	return tr, false
}

// TestLaneAudit_FailedAuditReturnsBreaches verifies that when a trivial-lane spec
// fails the deterministic audit (too many changed files), the MCP handler returns
// a ToolCallResult with IsError=true and the AuditResult payload — not an empty
// JSON-RPC error. This is the regression test for the SPEC-035 QA critical bug.
func TestLaneAudit_FailedAuditReturnsBreaches(t *testing.T) {
	// Use the mneme repo itself as the audit target. The current feature branch
	// has far more than 3 changed files relative to main, guaranteeing a breach.
	repoDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// Walk up until we find the git root.
	for {
		if _, statErr := os.Stat(repoDir + "/.git"); statErr == nil {
			break
		}
		parent := repoDir[:strings.LastIndex(repoDir, string(os.PathSeparator))]
		if parent == repoDir {
			t.Skip("no git repo found; skipping lane audit integration test")
		}
		repoDir = parent
	}

	srv := newTestServerWithRepoDir(t, repoDir)

	// 1. Create a trivial-lane spec.
	newResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "spec_new",
		Arguments: mustMarshal(t, map[string]any{
			"title": "MCP lane audit regression test",
			"lane":  string(model.LaneTrivial),
			"scope": "internal/mcp/*.go",
		}),
	})
	if newResp.Error != nil {
		t.Fatalf("spec_new: %v", newResp.Error.Message)
	}
	var spec struct {
		ID string `json:"id"`
	}
	unmarshalToolText(t, newResp, &spec)

	// 2. Advance to implementing via spec_quick.
	quickResp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name: "spec_quick",
		Arguments: mustMarshal(t, map[string]any{
			"id":        spec.ID,
			"rationale": "Tiny fix — just one handler tweak.",
			"by":        "orchestrator",
		}),
	})
	if quickResp.Error != nil {
		t.Fatalf("spec_quick: %v", quickResp.Error.Message)
	}

	// 3. Advance implementing → audit.
	auditMoveResp := process(t, srv, "tools/call", 3, ToolCallParams{
		Name: "spec_advance",
		Arguments: mustMarshal(t, map[string]any{
			"id": spec.ID,
			"by": "backend",
		}),
	})
	if auditMoveResp.Error != nil {
		t.Fatalf("spec_advance (implementing->audit): %v", auditMoveResp.Error.Message)
	}

	// 4. Run lane_audit with an explicit base_ref pointing to main so the diff
	// spans the whole feature branch. Without an explicit base_ref, SPEC-036 would
	// use spec.base_sha (HEAD at implementing time), yielding an empty diff.
	// We override with "main" to guarantee >3 changed files and a breach.
	auditResp := process(t, srv, "tools/call", 4, ToolCallParams{
		Name: "lane_audit",
		Arguments: mustMarshal(t, map[string]any{
			"id":       spec.ID,
			"base_ref": "main",
		}),
	})

	// The JSON-RPC envelope must succeed (no protocol-level error).
	if auditResp.Error != nil {
		t.Fatalf("lane_audit returned a JSON-RPC error (breach info was discarded): code=%d msg=%s",
			auditResp.Error.Code, auditResp.Error.Message)
	}

	// The tool result must carry IsError=true (audit failed).
	toolResult, hasRPCErr := unmarshalToolResult(t, auditResp)
	if hasRPCErr {
		t.Fatal("lane_audit returned a JSON-RPC error instead of a tool-level error")
	}
	if !toolResult.IsError {
		t.Error("expected ToolCallResult.IsError=true for a failed audit, got false")
	}

	// The content must be a valid AuditResult with non-empty breaches.
	if len(toolResult.Content) == 0 {
		t.Fatal("ToolCallResult has no content blocks")
	}
	var auditResult lane.AuditResult
	if err := json.Unmarshal([]byte(toolResult.Content[0].Text), &auditResult); err != nil {
		t.Fatalf("unmarshal AuditResult: %v (text: %s)", err, toolResult.Content[0].Text)
	}
	if auditResult.Passed {
		t.Error("expected AuditResult.Passed=false")
	}
	if len(auditResult.Breaches) == 0 {
		t.Error("expected non-empty breaches in AuditResult")
	}
}

// TestHandleSpecReject_HappyPath verifies that spec_reject transitions a spec
// from qa (standard lane) to implementing and returns the updated spec.
func TestHandleSpecReject_HappyPath(t *testing.T) {
	srv := newTestServerWithSDD(t)

	// Create a standard-lane spec.
	newResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "spec_new",
		Arguments: mustMarshal(t, map[string]any{
			"title": "Reject happy path test",
			"lane":  "standard",
		}),
	})
	if newResp.Error != nil {
		t.Fatalf("spec_new: %v", newResp.Error.Message)
	}
	var spec struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	unmarshalToolText(t, newResp, &spec)

	// Advance to qa (6 advances for standard lane).
	for i, by := range []string{"orch", "arch", "arch", "arch", "backend", "backend"} {
		advResp := process(t, srv, "tools/call", i+2, ToolCallParams{
			Name: "spec_advance",
			Arguments: mustMarshal(t, map[string]any{
				"id": spec.ID,
				"by": by,
			}),
		})
		if advResp.Error != nil {
			t.Fatalf("spec_advance %d: %v", i, advResp.Error.Message)
		}
		unmarshalToolText(t, advResp, &spec)
	}
	if spec.Status != "qa" {
		t.Fatalf("expected qa status before reject, got %s", spec.Status)
	}

	// Reject back to implementing.
	rejectResp := process(t, srv, "tools/call", 10, ToolCallParams{
		Name: "spec_reject",
		Arguments: mustMarshal(t, map[string]any{
			"id":     spec.ID,
			"reason": "edge case test fails",
			"by":     "qa-agent",
		}),
	})
	if rejectResp.Error != nil {
		t.Fatalf("spec_reject: %v", rejectResp.Error.Message)
	}
	unmarshalToolText(t, rejectResp, &spec)
	if spec.Status != "implementing" {
		t.Errorf("expected implementing after reject, got %s", spec.Status)
	}
}

// TestHandleSpecReject_InvalidStatus verifies that spec_reject returns an
// error when the spec is in a status that does not allow backward transition.
func TestHandleSpecReject_InvalidStatus(t *testing.T) {
	srv := newTestServerWithSDD(t)

	// Create a standard-lane spec in draft (not qa — cannot reject from draft).
	newResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "spec_new",
		Arguments: mustMarshal(t, map[string]any{
			"title": "Reject invalid status",
			"lane":  "standard",
		}),
	})
	if newResp.Error != nil {
		t.Fatalf("spec_new: %v", newResp.Error.Message)
	}
	var spec struct {
		ID string `json:"id"`
	}
	unmarshalToolText(t, newResp, &spec)

	rejectResp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name: "spec_reject",
		Arguments: mustMarshal(t, map[string]any{
			"id":     spec.ID,
			"reason": "bad status test",
			"by":     "orchestrator",
		}),
	})
	// Expect a JSON-RPC error (invalid params).
	if rejectResp.Error == nil {
		t.Fatal("expected JSON-RPC error for invalid status reject, got nil")
	}
	if rejectResp.Error.Code != CodeInvalidParams {
		t.Errorf("error code: got %d, want %d (invalid params)", rejectResp.Error.Code, CodeInvalidParams)
	}
}

// TestHandleLaneStats_ReturnsResponse verifies that lane_stats returns a
// LaneStatsResponse with JSON fields.
func TestHandleLaneStats_ReturnsResponse(t *testing.T) {
	srv := newTestServerWithSDD(t)

	statsResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "lane_stats",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if statsResp.Error != nil {
		t.Fatalf("lane_stats: %v", statsResp.Error.Message)
	}

	var stats struct {
		TrivialCount   int     `json:"trivial_count"`
		AuditFailCount int     `json:"audit_fail_count"`
		AuditFailRate  float64 `json:"audit_fail_rate"`
	}
	unmarshalToolText(t, statsResp, &stats)
	// For an empty project all counts are 0; the response must be well-formed.
	if stats.TrivialCount < 0 {
		t.Errorf("TrivialCount must be ≥ 0, got %d", stats.TrivialCount)
	}
}
