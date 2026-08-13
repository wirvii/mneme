package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
	"github.com/wirvii/mneme/internal/subagents"
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
	return NewServer(svc, sddSvc, nil, nil, logger, "all", "test")
}

// newTestServerWithSDDAndMemSvc mirrors newTestServerWithSDD but additionally
// returns the underlying *service.MemoryService so tests can seed a subagent
// manifest directly via service.NewSubagentService (SPEC-068 executor-envelope
// coverage) without needing a dedicated subagent_manifest_save MCP tool — none
// exists; manifests are normally only written by subagent_write.
func newTestServerWithSDDAndMemSvc(t *testing.T) (*Server, *service.MemoryService) {
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
	return NewServer(svc, sddSvc, nil, nil, logger, "all", "test"), svc
}

// specAdvanceTestEnvelope mirrors the {spec, executor} shape handleSpecAdvance
// returns (SPEC-068 D5), scoped to the fields these tests assert on.
type specAdvanceTestEnvelope struct {
	Spec struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"spec"`
	Executor struct {
		Stage           string `json:"stage"`
		ResponsibleRole string `json:"responsible_role"`
		Executor        string `json:"executor"`
		Delegate        bool   `json:"delegate"`
		Subagents       []struct {
			Role string `json:"role"`
			Path string `json:"path"`
		} `json:"subagents"`
		Degraded bool   `json:"degraded"`
		Hint     string `json:"hint"`
	} `json:"executor"`
}

// advanceSpecToPlanned creates a fresh standard-lane spec and advances it
// from draft to planned (4 transitions: speccing, specced, planning,
// planned), returning its ID. The caller performs the final planned ->
// implementing transition itself so it can inspect that specific executor
// envelope.
func advanceSpecToPlanned(t *testing.T, srv *Server, title string) string {
	t.Helper()

	newResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "spec_new",
		Arguments: mustMarshal(t, map[string]any{
			"title": title,
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

	for i, by := range []string{"orchestrator", "architect", "architect", "architect"} {
		advResp := process(t, srv, "tools/call", i+2, ToolCallParams{
			Name: "spec_advance",
			Arguments: mustMarshal(t, map[string]any{
				"id": spec.ID,
				"by": by,
			}),
		})
		if advResp.Error != nil {
			t.Fatalf("spec_advance %d (%s): %v", i, by, advResp.Error.Message)
		}
	}
	return spec.ID
}

// TestHandleSpecAdvance_ExecutorDelegatesToBackend covers AC1: with a backend
// entry in the manifest, advancing to implementing returns an executor
// envelope recommending delegation to that subagent.
func TestHandleSpecAdvance_ExecutorDelegatesToBackend(t *testing.T) {
	srv, memSvc := newTestServerWithSDDAndMemSvc(t)
	subagentSvc := service.NewSubagentService(memSvc)
	if _, err := subagentSvc.SaveManifest(context.Background(), "test-project", []service.ManifestEntry{
		{Role: subagents.RoleBackend, Path: "/repo/.claude/agents/backend.md"},
	}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}

	specID := advanceSpecToPlanned(t, srv, "AC1 executor delegate")

	advResp := process(t, srv, "tools/call", 10, ToolCallParams{
		Name: "spec_advance",
		Arguments: mustMarshal(t, map[string]any{
			"id": specID,
			"by": "architect",
		}),
	})
	if advResp.Error != nil {
		t.Fatalf("spec_advance (planned->implementing): %v", advResp.Error.Message)
	}

	var envelope specAdvanceTestEnvelope
	unmarshalToolText(t, advResp, &envelope)

	if envelope.Spec.Status != "implementing" {
		t.Fatalf("spec.status = %q, want implementing", envelope.Spec.Status)
	}
	if envelope.Executor.Executor != "subagent" {
		t.Errorf("executor.executor = %q, want subagent", envelope.Executor.Executor)
	}
	if !envelope.Executor.Delegate {
		t.Error("executor.delegate = false, want true")
	}
	if envelope.Executor.Degraded {
		t.Error("executor.degraded = true, want false")
	}
	if len(envelope.Executor.Subagents) != 1 || envelope.Executor.Subagents[0].Role != "backend" {
		t.Errorf("executor.subagents = %+v, want single backend entry", envelope.Executor.Subagents)
	}
}

// TestHandleSpecAdvance_ExecutorDegradesWithoutImplementer covers AC2: a
// manifest without backend/frontend degrades implementing to the
// orchestrator, with a hint mentioning the degraded mode and materializing a
// subagent.
func TestHandleSpecAdvance_ExecutorDegradesWithoutImplementer(t *testing.T) {
	srv, memSvc := newTestServerWithSDDAndMemSvc(t)
	subagentSvc := service.NewSubagentService(memSvc)
	if _, err := subagentSvc.SaveManifest(context.Background(), "test-project", []service.ManifestEntry{
		{Role: subagents.RoleQATester, Path: "/repo/.claude/agents/qa-tester.md"},
	}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}

	specID := advanceSpecToPlanned(t, srv, "AC2 executor degraded")

	advResp := process(t, srv, "tools/call", 10, ToolCallParams{
		Name: "spec_advance",
		Arguments: mustMarshal(t, map[string]any{
			"id": specID,
			"by": "architect",
		}),
	})
	if advResp.Error != nil {
		t.Fatalf("spec_advance (planned->implementing): %v", advResp.Error.Message)
	}

	var envelope specAdvanceTestEnvelope
	unmarshalToolText(t, advResp, &envelope)

	if envelope.Executor.Executor != "orchestrator" {
		t.Errorf("executor.executor = %q, want orchestrator", envelope.Executor.Executor)
	}
	if envelope.Executor.Delegate {
		t.Error("executor.delegate = true, want false")
	}
	if !envelope.Executor.Degraded {
		t.Error("executor.degraded = false, want true")
	}
	if len(envelope.Executor.Subagents) != 0 {
		t.Errorf("executor.subagents = %+v, want empty", envelope.Executor.Subagents)
	}
	lowerHint := strings.ToLower(envelope.Executor.Hint)
	if !strings.Contains(lowerHint, "degradad") || !strings.Contains(lowerHint, "materializ") {
		t.Errorf("executor.hint = %q, want it to mention degraded mode and materializing a subagent", envelope.Executor.Hint)
	}
}

// TestHandleSpecAdvance_NoManifestDoesNotFailAdvance covers AC5: a project
// that never ran the grill (no manifest memory at all — ReadManifest returns
// nil, nil) must not fail spec_advance. The spec subfield remains correct and
// the executor envelope reports an empty subagent list with Degraded=true for
// a delegable stage.
func TestHandleSpecAdvance_NoManifestDoesNotFailAdvance(t *testing.T) {
	srv := newTestServerWithSDD(t) // no manifest ever seeded for "test-project"

	newResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "spec_new",
		Arguments: mustMarshal(t, map[string]any{
			"title": "AC5 no manifest",
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

	advResp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name: "spec_advance",
		Arguments: mustMarshal(t, map[string]any{
			"id": spec.ID,
			"by": "orchestrator",
		}),
	})
	if advResp.Error != nil {
		t.Fatalf("spec_advance (draft->speccing) must not fail without a manifest: %v", advResp.Error.Message)
	}

	var envelope specAdvanceTestEnvelope
	unmarshalToolText(t, advResp, &envelope)

	if envelope.Spec.ID != spec.ID || envelope.Spec.Status != "speccing" {
		t.Errorf("spec subfield = %+v, want id=%s status=speccing", envelope.Spec, spec.ID)
	}
	if len(envelope.Executor.Subagents) != 0 {
		t.Errorf("executor.subagents = %+v, want empty", envelope.Executor.Subagents)
	}
	if !envelope.Executor.Degraded {
		t.Error("executor.degraded = false, want true for speccing without an architect manifest entry")
	}
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
		Item struct {
			ID string `json:"id"`
		} `json:"item"`
	}
	unmarshalToolText(t, addResp, &addResult)

	// Refine the item (required before promote).
	refineResp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name: "backlog_refine",
		Arguments: mustMarshal(t, map[string]any{
			"id":         addResult.Item.ID,
			"refinement": "Refined description with enough detail.",
		}),
	})
	if refineResp.Error != nil {
		t.Fatalf("backlog_refine: %v", refineResp.Error.Message)
	}

	// First promote — should succeed.
	promoteResp := process(t, srv, "tools/call", 3, ToolCallParams{
		Name:      "backlog_promote",
		Arguments: mustMarshal(t, map[string]any{"id": addResult.Item.ID}),
	})
	if promoteResp.Error != nil {
		t.Fatalf("first backlog_promote: %v", promoteResp.Error.Message)
	}

	// Second promote of the same item — the backlog item is already promoted
	// (status is no longer "refined"), so the service should return an error.
	promoteResp2 := process(t, srv, "tools/call", 4, ToolCallParams{
		Name:      "backlog_promote",
		Arguments: mustMarshal(t, map[string]any{"id": addResult.Item.ID}),
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

// TestBacklogAdd_AdvisoryEnvelope verifies the {item, advisory} envelope
// (SPEC-103 AC3/AC6): a standard-lane item carries a non-empty advisory
// mentioning grill-me, while a trivial-lane item carries no advisory field at
// all (thanks to omitempty).
func TestBacklogAdd_AdvisoryEnvelope(t *testing.T) {
	srv := newTestServerWithSDD(t)

	standardResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "backlog_add",
		Arguments: mustMarshal(t, map[string]any{
			"title": "Standard-lane item",
			"lane":  "standard",
		}),
	})
	if standardResp.Error != nil {
		t.Fatalf("backlog_add (standard): %v", standardResp.Error.Message)
	}
	var standardResult struct {
		Item struct {
			ID string `json:"id"`
		} `json:"item"`
		Advisory string `json:"advisory"`
	}
	unmarshalToolText(t, standardResp, &standardResult)
	if standardResult.Item.ID == "" {
		t.Error("standard-lane item.id is empty")
	}
	if !strings.Contains(standardResult.Advisory, "grill-me") {
		t.Errorf("standard-lane advisory = %q, want it to mention grill-me", standardResult.Advisory)
	}

	trivialResp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name: "backlog_add",
		Arguments: mustMarshal(t, map[string]any{
			"title": "Trivial-lane item",
			"lane":  "trivial",
			"scope": "internal/model/*.go",
		}),
	})
	if trivialResp.Error != nil {
		t.Fatalf("backlog_add (trivial): %v", trivialResp.Error.Message)
	}
	var trivialEnvelope map[string]json.RawMessage
	unmarshalToolText(t, trivialResp, &trivialEnvelope)
	if _, present := trivialEnvelope["advisory"]; present {
		t.Errorf("trivial-lane envelope has an \"advisory\" field, want it omitted: %v", trivialEnvelope)
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
	return NewServer(svc, nil, nil, nil, logger, "all", "test"), ps
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
	var saved1 struct {
		ID string `json:"id"`
	}
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
	var saved2 struct {
		ID string `json:"id"`
	}
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
	return NewServer(svc, sddSvc, nil, nil, logger, "all", "test")
}

// initTrivialBreachRepo creates a throwaway git repository whose current branch
// (feature) diverges from `main` by more than three files, so that a trivial-lane
// audit run with base_ref=main is guaranteed to breach (file count 4 > limit 3).
// It returns the repository root directory. The files are plain .txt so the
// public-symbol check (Go/TS only) is skipped and the breach comes purely from
// the file-count rule, keeping the scenario deterministic and self-contained.
func initTrivialBreachRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	// Initialise with `main` as the default branch (portable across git versions
	// that still default to `master`).
	run("init", "-q")
	run("checkout", "-q", "-B", "main")
	run("config", "user.email", "ci@example.com")
	run("config", "user.name", "CI Test")

	// Base commit on main.
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base file: %v", err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "base commit on main")

	// Feature branch adds four files (> trivial limit of 3) → guaranteed breach.
	run("checkout", "-q", "-b", "feature")
	for i := 1; i <= 4; i++ {
		name := filepath.Join(dir, fmt.Sprintf("changed_%d.txt", i))
		if err := os.WriteFile(name, []byte("change\n"), 0o644); err != nil {
			t.Fatalf("write changed file %d: %v", i, err)
		}
	}
	run("add", "-A")
	run("commit", "-q", "-m", "feature: add four files")

	return dir
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
	// Build a dedicated temporary git repository whose feature branch diverges
	// from `main` by more than 3 files. This makes the audit breach deterministic
	// in every environment (clean CI checkout on main, or a local feature branch),
	// instead of assuming the mneme working tree itself has >3 changed files vs main.
	repoDir := initTrivialBreachRepo(t)

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
	var auditResult model.LaneAuditResult
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

// TestLaneAuditResult_SerializesWithLiteralFieldNames covers AC28's own
// promise precisely: `mneme lane audit`'s / `lane_audit`'s JSON shape is
// compared against a LITERAL string fixed in this test, never eyeballed or
// roundtripped through the same struct (a roundtrip can't detect a field
// RENAME — it would decode right back into whatever name the struct now
// has). This exercises the exact json.Marshal call handleLaneAudit uses
// (SPEC-118 P11, internal/mcp/handlers.go) directly against
// model.LaneAuditResult — neither type ever declared a `json:"..."` tag
// (SPEC-118 P7), so Go's default field-name-verbatim encoding is what this
// literal pins.
func TestLaneAuditResult_SerializesWithLiteralFieldNames(t *testing.T) {
	result := model.LaneAuditResult{
		FileCount:           2,
		LinesChanged:        14,
		OutOfScopeFiles:     []string{"docs/readme.md"},
		ForbiddenPaths:      []string{"internal/db/migrations/001.sql"},
		PublicSymbolChanges: []string{"NewExportedFunc"},
		Breaches:            []string{"too many files"},
		Passed:              false,
	}
	b, err := json.Marshal(&result)
	if err != nil {
		t.Fatalf("json.Marshal(LaneAuditResult): %v", err)
	}
	const want = `{"FileCount":2,"LinesChanged":14,"OutOfScopeFiles":["docs/readme.md"],"ForbiddenPaths":["internal/db/migrations/001.sql"],"PublicSymbolChanges":["NewExportedFunc"],"Breaches":["too many files"],"Passed":false}`
	if string(b) != want {
		t.Errorf("json.Marshal(LaneAuditResult) = %s, want literal:\n%s", b, want)
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

	// Advance to qa (6 advances for standard lane). spec_advance now returns
	// the {spec, executor} envelope (SPEC-068 D5) rather than a bare Spec.
	var envelope struct {
		Spec struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"spec"`
	}
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
		unmarshalToolText(t, advResp, &envelope)
		spec.ID = envelope.Spec.ID
		spec.Status = envelope.Spec.Status
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

// --- SPEC DOC WRITE TESTS (SPEC-087 D3) ---

// newTestServerWithSDDAndWorkflowDir mirrors newTestServerWithSDD but points
// Workflow.Dir at a fresh t.TempDir() so spec_doc_write tests never touch the
// real ~/.mneme/workflows directory.
func newTestServerWithSDDAndWorkflowDir(t *testing.T) (*Server, string) {
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
	workflowDir := t.TempDir()
	cfg.Workflow.Dir = workflowDir
	svc := service.NewMemoryService(projectStore, globalStore, cfg, "test-project", embed.NopEmbedder{})

	sddStore := store.NewSDDStore(projectDB)
	sddSvc := service.NewSDDService(sddStore, cfg, "test-project", svc)

	logger := slog.Default()
	return NewServer(svc, sddSvc, nil, nil, logger, "all", "test"), workflowDir
}

// TestHandleSpecDocWrite_HappyPath verifies spec_doc_write writes content to
// the expected path derived from the persisted spec record.
func TestHandleSpecDocWrite_HappyPath(t *testing.T) {
	srv, workflowDir := newTestServerWithSDDAndWorkflowDir(t)

	newResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "spec_new",
		Arguments: mustMarshal(t, map[string]any{
			"title": "spec_doc_write happy path",
			"lane":  "standard",
		}),
	})
	if newResp.Error != nil {
		t.Fatalf("spec_new: %v", newResp.Error.Message)
	}
	var spec struct {
		ID      string `json:"id"`
		Project string `json:"project"`
	}
	unmarshalToolText(t, newResp, &spec)

	writeResp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name: "spec_doc_write",
		Arguments: mustMarshal(t, map[string]any{
			"id":      spec.ID,
			"kind":    "qa-report",
			"content": "# QA Report\n\nAPROBADO\n",
		}),
	})
	if writeResp.Error != nil {
		t.Fatalf("spec_doc_write: %v", writeResp.Error.Message)
	}

	var result struct {
		Path    string `json:"path"`
		Bytes   int    `json:"bytes"`
		Created bool   `json:"created"`
	}
	unmarshalToolText(t, writeResp, &result)

	wantPath := filepath.Join(workflowDir, "test-project", "specs", spec.ID, "qa-report.md")
	if result.Path != wantPath {
		t.Errorf("Path = %q, want %q", result.Path, wantPath)
	}
	if !result.Created {
		t.Error("Created = false, want true")
	}

	data, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(data) != "# QA Report\n\nAPROBADO\n" {
		t.Errorf("file content = %q", string(data))
	}
}

// TestHandleSpecDocWrite_UnknownKind verifies an unrecognised kind returns
// CodeInvalidParams instead of writing anything.
func TestHandleSpecDocWrite_UnknownKind(t *testing.T) {
	srv, _ := newTestServerWithSDDAndWorkflowDir(t)

	newResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "spec_new",
		Arguments: mustMarshal(t, map[string]any{
			"title": "spec_doc_write unknown kind",
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

	writeResp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name: "spec_doc_write",
		Arguments: mustMarshal(t, map[string]any{
			"id":      spec.ID,
			"kind":    "bogus",
			"content": "x",
		}),
	})
	if writeResp.Error == nil {
		t.Fatal("expected error for unknown kind")
	}
	if writeResp.Error.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want %d", writeResp.Error.Code, CodeInvalidParams)
	}
}

// TestHandleSpecDocWrite_UnknownSpec verifies a nonexistent spec ID returns
// an error rather than writing anywhere.
func TestHandleSpecDocWrite_UnknownSpec(t *testing.T) {
	srv, _ := newTestServerWithSDDAndWorkflowDir(t)

	writeResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "spec_doc_write",
		Arguments: mustMarshal(t, map[string]any{
			"id":      "SPEC-999",
			"kind":    "spec",
			"content": "x",
		}),
	})
	if writeResp.Error == nil {
		t.Fatal("expected error for unknown spec id")
	}
}

// --- MODEL TOOL TESTS (SPEC-038) ---

// newTestServerWithModels builds a test server with a real ModelsService
// targeting a fresh temp config file.
func newTestServerWithModels(t *testing.T) *Server {
	t.Helper()
	srv := newTestServerWithSDD(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	modelsSvc := service.NewModelsService(cfgPath)
	srv.modelsSvc = modelsSvc
	srv.handlers.modelsSvc = modelsSvc
	return srv
}

func TestHandleModelList_ReturnsAllAgents(t *testing.T) {
	srv := newTestServerWithModels(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "model_list",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if resp.Error != nil {
		t.Fatalf("model_list error: %v", resp.Error.Message)
	}

	var result struct {
		Agents []struct {
			Agent  string `json:"agent"`
			Model  string `json:"model"`
			Origin string `json:"origin"`
		} `json:"agents"`
	}
	unmarshalToolText(t, resp, &result)

	if len(result.Agents) == 0 {
		t.Fatal("model_list returned no agents")
	}
	for _, a := range result.Agents {
		if a.Model == "" {
			t.Errorf("agent %s has empty model", a.Agent)
		}
	}
}

func TestHandleModelSet_KnownAgent(t *testing.T) {
	srv := newTestServerWithModels(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "model_set",
		Arguments: mustMarshal(t, map[string]any{
			"agent": "bug-hunter",
			"model": "opus",
		}),
	})
	if resp.Error != nil {
		t.Fatalf("model_set error: %v", resp.Error.Message)
	}
}

func TestHandleModelSet_UnknownAgent_InvalidParams(t *testing.T) {
	srv := newTestServerWithModels(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "model_set",
		Arguments: mustMarshal(t, map[string]any{
			"agent": "nosuchagent",
			"model": "opus",
		}),
	})
	if resp.Error == nil {
		t.Fatal("expected error for unknown agent")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("error code: got %d, want %d (invalid params)", resp.Error.Code, CodeInvalidParams)
	}
}

func TestHandleModelSet_EmptyModel_InvalidParams(t *testing.T) {
	srv := newTestServerWithModels(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "model_set",
		Arguments: mustMarshal(t, map[string]any{
			"agent": "backend",
			"model": "",
		}),
	})
	if resp.Error == nil {
		t.Fatal("expected error for empty model")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("error code: got %d, want %d (invalid params)", resp.Error.Code, CodeInvalidParams)
	}
}

func TestHandleModelReset_ReturnsReset(t *testing.T) {
	srv := newTestServerWithModels(t)

	// Set first.
	_ = process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "model_set",
		Arguments: mustMarshal(t, map[string]any{"agent": "backend", "model": "haiku"}),
	})

	// Then reset.
	resp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name:      "model_reset",
		Arguments: mustMarshal(t, map[string]any{"agent": "backend"}),
	})
	if resp.Error != nil {
		t.Fatalf("model_reset error: %v", resp.Error.Message)
	}

	var result struct {
		Reset []string `json:"reset"`
	}
	unmarshalToolText(t, resp, &result)
	if len(result.Reset) != 1 || result.Reset[0] != "backend" {
		t.Errorf("model_reset returned %v, want [backend]", result.Reset)
	}
}

// seedBacklogItems creates n backlog items via the backlog_add tool, all
// standard lane, and returns nothing — the count is what these tests need.
func seedBacklogItems(t *testing.T, srv *Server, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		resp := process(t, srv, "tools/call", 100+i, ToolCallParams{
			Name: "backlog_add",
			Arguments: mustMarshal(t, map[string]any{
				"title": fmt.Sprintf("item %d", i),
				"lane":  "standard",
			}),
		})
		if resp.Error != nil {
			t.Fatalf("backlog_add %d: %v", i, resp.Error.Message)
		}
	}
}

// TestHandleBacklogList_NoLimitDefaultsTo20 is AC13: omitting limit returns
// at most model.ListDefaultLimit (20) items even when more exist.
func TestHandleBacklogList_NoLimitDefaultsTo20(t *testing.T) {
	srv := newTestServerWithSDD(t)
	seedBacklogItems(t, srv, 25)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "backlog_list",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if resp.Error != nil {
		t.Fatalf("backlog_list: %v", resp.Error.Message)
	}

	var view backlogListView
	unmarshalToolText(t, resp, &view)
	if len(view.Items) != model.ListDefaultLimit {
		t.Errorf("got %d items, want %d (the default limit)", len(view.Items), model.ListDefaultLimit)
	}
	if view.Total != 25 {
		t.Errorf("Total = %d, want 25 (the real match count)", view.Total)
	}
}

// TestHandleBacklogList_NegativeLimitDefaultsTo20 is AC14: limit=-1 (a
// minimum:1 schema violation) must not fall through to the unwindowed CLI
// path — the handler never forwards limit<=0 to the service.
func TestHandleBacklogList_NegativeLimitDefaultsTo20(t *testing.T) {
	srv := newTestServerWithSDD(t)
	seedBacklogItems(t, srv, 25)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "backlog_list",
		Arguments: mustMarshal(t, map[string]any{"limit": -1}),
	})
	if resp.Error != nil {
		t.Fatalf("backlog_list: %v", resp.Error.Message)
	}

	var view backlogListView
	unmarshalToolText(t, resp, &view)
	if len(view.Items) != model.ListDefaultLimit {
		t.Errorf("got %d items, want %d (limit=-1 must default, not return everything)", len(view.Items), model.ListDefaultLimit)
	}
}

// TestHandleBacklogGet_FullDescription is AC18: backlog_get returns the
// complete description with no excerpt/truncated fields at all.
func TestHandleBacklogGet_FullDescription(t *testing.T) {
	srv := newTestServerWithSDD(t)

	longDesc := strings.Repeat("grill ledger content ", 500)
	addResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "backlog_add",
		Arguments: mustMarshal(t, map[string]any{
			"title":       "Ledger item",
			"lane":        "standard",
			"description": longDesc,
		}),
	})
	if addResp.Error != nil {
		t.Fatalf("backlog_add: %v", addResp.Error.Message)
	}
	var added struct {
		Item struct {
			ID string `json:"id"`
		} `json:"item"`
	}
	unmarshalToolText(t, addResp, &added)

	getResp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name:      "backlog_get",
		Arguments: mustMarshal(t, map[string]any{"id": added.Item.ID}),
	})
	if getResp.Error != nil {
		t.Fatalf("backlog_get: %v", getResp.Error.Message)
	}

	// SPEC-110 D7: backlog_get now returns the {item, refinements} envelope
	// instead of the raw item at the top level.
	var raw map[string]any
	unmarshalToolText(t, getResp, &raw)
	item, ok := raw["item"].(map[string]any)
	if !ok {
		t.Fatalf("expected top-level 'item' object in the envelope, got %#v", raw)
	}
	if _, hasExcerpt := item["excerpt"]; hasExcerpt {
		t.Error("backlog_get must not emit 'excerpt' — it returns the full item")
	}
	if _, hasTruncated := item["truncated"]; hasTruncated {
		t.Error("backlog_get must not emit 'truncated' — it returns the full item")
	}
	gotDesc, _ := item["description"].(string)
	if gotDesc != longDesc {
		t.Errorf("description = %d runes, want %d (full description)", len([]rune(gotDesc)), len([]rune(longDesc)))
	}
}

// TestHandleBacklogList_EmitsRefinementCount is AC21: backlog_list emits
// `refinements` (present even when 0, no omitempty) alongside excerpt/
// truncated/total, and never emits `description`.
func TestHandleBacklogList_EmitsRefinementCount(t *testing.T) {
	srv := newTestServerWithSDD(t)

	addResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "backlog_add",
		Arguments: mustMarshal(t, map[string]any{
			"title": "Ledger item", "lane": "standard", "description": "d",
		}),
	})
	if addResp.Error != nil {
		t.Fatalf("backlog_add: %v", addResp.Error.Message)
	}
	var added struct {
		Item struct {
			ID string `json:"id"`
		} `json:"item"`
	}
	unmarshalToolText(t, addResp, &added)

	if resp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name: "backlog_refine",
		Arguments: mustMarshal(t, map[string]any{
			"id": added.Item.ID, "refinement": "r1",
		}),
	}); resp.Error != nil {
		t.Fatalf("backlog_refine: %v", resp.Error.Message)
	}

	listResp := process(t, srv, "tools/call", 3, ToolCallParams{
		Name:      "backlog_list",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if listResp.Error != nil {
		t.Fatalf("backlog_list: %v", listResp.Error.Message)
	}

	var raw map[string]any
	unmarshalToolText(t, listResp, &raw)
	items, ok := raw["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("expected exactly 1 item, got %#v", raw["items"])
	}
	itemMap, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("item is not a map: %#v", items[0])
	}
	refinements, ok := itemMap["refinements"]
	if !ok {
		t.Fatal("backlog_list item must emit 'refinements'")
	}
	if refinements != float64(1) {
		t.Errorf("refinements = %v, want 1", refinements)
	}
	if _, hasDescription := itemMap["description"]; hasDescription {
		t.Error("backlog_list must never emit 'description'")
	}
}

// TestHandleBacklogGet_ReturnsEnvelopeWithAllRefinements is AC22:
// backlog_get returns {item, refinements} with the full refinement bodies,
// no excerpt/truncated/total anywhere in the envelope.
func TestHandleBacklogGet_ReturnsEnvelopeWithAllRefinements(t *testing.T) {
	srv := newTestServerWithSDD(t)

	addResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "backlog_add",
		Arguments: mustMarshal(t, map[string]any{"title": "X", "lane": "standard"}),
	})
	if addResp.Error != nil {
		t.Fatalf("backlog_add: %v", addResp.Error.Message)
	}
	var added struct {
		Item struct {
			ID string `json:"id"`
		} `json:"item"`
	}
	unmarshalToolText(t, addResp, &added)

	for _, body := range []string{"r1", "r2", "r3"} {
		if resp := process(t, srv, "tools/call", 2, ToolCallParams{
			Name:      "backlog_refine",
			Arguments: mustMarshal(t, map[string]any{"id": added.Item.ID, "refinement": body}),
		}); resp.Error != nil {
			t.Fatalf("backlog_refine(%s): %v", body, resp.Error.Message)
		}
	}

	getResp := process(t, srv, "tools/call", 3, ToolCallParams{
		Name:      "backlog_get",
		Arguments: mustMarshal(t, map[string]any{"id": added.Item.ID}),
	})
	if getResp.Error != nil {
		t.Fatalf("backlog_get: %v", getResp.Error.Message)
	}

	var raw map[string]any
	unmarshalToolText(t, getResp, &raw)
	if _, hasTotal := raw["total"]; hasTotal {
		t.Error("backlog_get envelope must not emit 'total' (D7)")
	}
	refinements, ok := raw["refinements"].([]any)
	if !ok || len(refinements) != 3 {
		t.Fatalf("expected 3 refinements, got %#v", raw["refinements"])
	}
	for i, want := range []string{"r1", "r2", "r3"} {
		r, ok := refinements[i].(map[string]any)
		if !ok {
			t.Fatalf("refinement %d is not a map: %#v", i, refinements[i])
		}
		if r["body"] != want {
			t.Errorf("refinement[%d].body = %v, want %q", i, r["body"], want)
		}
		if _, hasExcerpt := r["excerpt"]; hasExcerpt {
			t.Errorf("refinement[%d] must not emit 'excerpt'", i)
		}
	}
}

// TestHandleBacklogRefine_PromotedReturnsInvalidParams is AC23: refining a
// promoted item returns CodeInvalidParams (-32602), not the opaque internal
// error code (-32603) — model.ErrBacklogNotRefinable must be registered in
// mapServiceError.
func TestHandleBacklogRefine_PromotedReturnsInvalidParams(t *testing.T) {
	srv := newTestServerWithSDD(t)

	addResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "backlog_add",
		Arguments: mustMarshal(t, map[string]any{"title": "X", "lane": "standard"}),
	})
	if addResp.Error != nil {
		t.Fatalf("backlog_add: %v", addResp.Error.Message)
	}
	var added struct {
		Item struct {
			ID string `json:"id"`
		} `json:"item"`
	}
	unmarshalToolText(t, addResp, &added)

	if resp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name:      "backlog_refine",
		Arguments: mustMarshal(t, map[string]any{"id": added.Item.ID, "refinement": "r1"}),
	}); resp.Error != nil {
		t.Fatalf("backlog_refine: %v", resp.Error.Message)
	}
	if resp := process(t, srv, "tools/call", 3, ToolCallParams{
		Name:      "backlog_promote",
		Arguments: mustMarshal(t, map[string]any{"id": added.Item.ID}),
	}); resp.Error != nil {
		t.Fatalf("backlog_promote: %v", resp.Error.Message)
	}

	resp := process(t, srv, "tools/call", 4, ToolCallParams{
		Name:      "backlog_refine",
		Arguments: mustMarshal(t, map[string]any{"id": added.Item.ID, "refinement": "should fail"}),
	})
	if resp.Error == nil {
		t.Fatal("expected backlog_refine on a promoted item to fail")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want %d (CodeInvalidParams)", resp.Error.Code, CodeInvalidParams)
	}
}

// TestHandleBacklogGet_NotFound is AC19: an unknown ID surfaces
// CodeMemoryNotFound (-32000) end-to-end, via the existing
// model.ErrBacklogNotFound -> mapServiceError mapping — no new sentinel.
func TestHandleBacklogGet_NotFound(t *testing.T) {
	srv := newTestServerWithSDD(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "backlog_get",
		Arguments: mustMarshal(t, map[string]any{"id": "BL-999"}),
	})
	if resp.Error == nil {
		t.Fatal("expected an error for a non-existent backlog item")
	}
	if resp.Error.Code != CodeMemoryNotFound {
		t.Errorf("error code = %d, want %d (CodeMemoryNotFound)", resp.Error.Code, CodeMemoryNotFound)
	}
}
