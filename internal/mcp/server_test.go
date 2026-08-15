package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// newTestServer creates a Server backed by a fully migrated in-memory SQLite
// database. It also returns input and output bytes.Buffer so callers can feed
// messages and read responses without real stdio.
func newTestServer(t *testing.T) *Server {
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

	logger := slog.Default()
	return NewServer(svc, nil, nil, nil, logger, "all", "test")
}

// buildRequestLine marshals method/id/params into a single JSON-RPC request
// line with no trailing newline. Extracted from sendMessage so the large-
// message test table (TestRunLoop_LargeMessages) can control line endings and
// exact byte sizes itself instead of going through a buffer that always
// appends '\n'.
func buildRequestLine(t *testing.T, method string, id int, params any) []byte {
	t.Helper()

	var rawID json.RawMessage
	if id >= 0 {
		b, _ := json.Marshal(id)
		rawID = b
	}

	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("marshal params: %v", err)
		}
		rawParams = b
	}

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      rawID,
		Method:  method,
		Params:  rawParams,
	}

	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return b
}

// sendMessage writes a single JSON-RPC request as a line to buf and returns the
// raw bytes written (useful for debugging).
func sendMessage(t *testing.T, buf *bytes.Buffer, method string, id int, params any) {
	t.Helper()
	buf.Write(buildRequestLine(t, method, id, params))
	buf.WriteByte('\n')
}

// newResponseScanner builds a bufio.Scanner over r with a buffer large enough
// to read any response Run can legitimately produce (up to maxMessageBytes,
// D2). Without this, an assertion scanner reading a large-but-valid response
// (e.g. tools/list) would fail with "token too long" itself — the exact bug
// this spec fixes, but on the test's own read path instead of the server's
// (D5/AC16). The literal 10*1024*1024 duplicates maxMessageBytes on purpose:
// this test-only helper predates the constant in the P1→P2 TDD sequence and
// is not the production reader.
func newResponseScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)
	return scanner
}

// readResponse reads one line from scanner and deserializes it as a JSONRPCResponse.
func readResponse(t *testing.T, scanner *bufio.Scanner) JSONRPCResponse {
	t.Helper()
	if !scanner.Scan() {
		t.Fatalf("readResponse: no more lines (scanner error: %v)", scanner.Err())
	}
	var resp JSONRPCResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatalf("readResponse: unmarshal: %v (raw: %s)", err, scanner.Text())
	}
	return resp
}

// process sends a single message through the server and returns the response.
func process(t *testing.T, srv *Server, method string, id int, params any) JSONRPCResponse {
	t.Helper()
	var in bytes.Buffer
	sendMessage(t, &in, method, id, params)

	resp, hasResp := srv.handleMessage(in.Bytes()[:in.Len()-1]) // strip trailing newline
	if !hasResp {
		t.Fatalf("process: expected response for method %s but got notification handling", method)
	}
	return resp
}

// unmarshalResult unmarshals resp.Result into v. Fails the test if resp.Error is set.
func unmarshalResult(t *testing.T, resp JSONRPCResponse, v any) {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: code=%d message=%s", resp.Error.Code, resp.Error.Message)
	}
	b, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("re-marshal result: %v", err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
}

// unmarshalToolText unmarshals the first text content block of a ToolCallResult into v.
func unmarshalToolText(t *testing.T, resp JSONRPCResponse, v any) {
	t.Helper()
	var toolResult ToolCallResult
	unmarshalResult(t, resp, &toolResult)
	if len(toolResult.Content) == 0 {
		t.Fatal("tool result has no content blocks")
	}
	if err := json.Unmarshal([]byte(toolResult.Content[0].Text), v); err != nil {
		t.Fatalf("unmarshal tool text: %v (text: %s)", err, toolResult.Content[0].Text)
	}
}

// --- Tests ---

// TestNewServer_MaxMessageSeed is AC13: NewServer must seed maxMessage to the
// production constant maxMessageBytes. Other tests in this file lower
// srv.maxMessage after construction to exercise the oversized-message path
// cheaply (DD2) — this test guards the seed itself, so lowering it in one
// test can never mask a regression of what production actually gets.
func TestNewServer_MaxMessageSeed(t *testing.T) {
	srv := newTestServer(t)
	if srv.maxMessage != maxMessageBytes {
		t.Errorf("NewServer: maxMessage = %d, want %d (maxMessageBytes)", srv.maxMessage, maxMessageBytes)
	}
}

func TestInitialize(t *testing.T) {
	srv := newTestServer(t)
	resp := process(t, srv, "initialize", 1, map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test-client", "version": "0.1"},
	})

	var result InitializeResult
	unmarshalResult(t, resp, &result)

	if result.ProtocolVersion != "2024-11-05" {
		t.Errorf("protocolVersion = %q, want %q", result.ProtocolVersion, "2024-11-05")
	}
	if result.ServerInfo.Name != "mneme" {
		t.Errorf("serverInfo.name = %q, want %q", result.ServerInfo.Name, "mneme")
	}
	if result.Capabilities.Tools == nil {
		t.Error("capabilities.tools should not be nil")
	}
}

func TestToolsList(t *testing.T) {
	srv := newTestServer(t)

	// Initialize first.
	process(t, srv, "initialize", 1, map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test-client", "version": "0.1"},
	})

	resp := process(t, srv, "tools/list", 2, nil)

	var result ToolsListResult
	unmarshalResult(t, resp, &result)

	wantNames := []string{
		"speech_emit", "speech_control",
		"mem_save", "mem_search", "mem_get", "mem_context",
		"mem_update", "mem_session_end", "mem_suggest_topic_key",
		"mem_relate", "mem_timeline", "mem_stats", "mem_checkpoint", "mem_forget", "mem_promote",
		// SDD tools
		"backlog_add", "backlog_list", "backlog_get", "backlog_refine", "backlog_promote",
		"spec_new", "spec_status", "spec_advance", "spec_pushback", "spec_resolve",
		"spec_doc_write", "spec_list",
		// Lane tools (SPEC-035 + SPEC-036)
		"spec_quick", "lane_audit", "lane_reclassify", "lane_override", "lane_status",
		"spec_reject", "lane_stats",
		// Quality tools (SPEC-115 EPIC-calidad S1 + SPEC-117 S3)
		"quality_verify", "quality_status", "quality_ack", "quality_sign", "quality_report",
		// Graph tools
		"mem_gaps",
		"mem_explore",
		// Codegraph tools
		"codegraph_search", "codegraph_context", "codegraph_callers",
		"codegraph_callees", "codegraph_impact", "codegraph_node",
		"codegraph_explore", "codegraph_trace", "codegraph_status", "codegraph_files",
		// Skills tools (SPEC-037)
		"skills_list", "skills_install", "skills_pin", "skills_unpin",
		"skills_remove", "skills_lint", "skills_validate",
		// Model tools (SPEC-038)
		"model_list", "model_set", "model_reset",
		// Conflicts tools (SPEC-039)
		"conflicts_candidates", "conflicts_scan", "conflicts_link", "conflicts_unlink", "conflicts_list",
		// Profile tools (SPEC-091 §1 + SPEC-093 §3 + SPEC-095 §5 + SPEC-105 DD21)
		"profile_new", "profile_add", "profile_update", "profile_list", "profile_status",
		"profile_use", "profile_default", "profile_deactivate",
		// Project tools (SPEC-098 §7a + SPEC-099 §7b)
		"project_new", "app_add",
		// Scaffold tool (SPEC-100 §7c)
		"scaffold_capture",
		// Init tool (SPEC-041)
		"init",
		// Subagent tools (SPEC-057 / EPIC agnostic-agents SS-4)
		"subagent_fingerprint", "subagent_profile_get", "subagent_profile_save",
		"subagent_compose", "subagent_write", "subagent_manifest_list",
	}
	if len(result.Tools) != len(wantNames) {
		t.Fatalf("got %d tools, want %d", len(result.Tools), len(wantNames))
	}
	for i, want := range wantNames {
		if result.Tools[i].Name != want {
			t.Errorf("tools[%d].name = %q, want %q", i, result.Tools[i].Name, want)
		}
	}
}

func TestCallerPolicyFiltersAndAuthorizesSubagentTools(t *testing.T) {
	srv := newTestServer(t)
	srv.WithCallerPolicy(CallerPolicy{Role: "backend", Archetype: "backend"})

	for _, tool := range srv.tools {
		if tool.Name == "spec_advance" || tool.Name == "spec_quick" || tool.Name == "quality_ack" || tool.Name == "quality_sign" {
			t.Fatalf("restricted tool %q remained visible", tool.Name)
		}
	}

	denied := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"spec_advance","arguments":{}}}`)
	resp, ok := srv.handleMessage(denied)
	if !ok || resp.Error == nil || !strings.Contains(resp.Error.Message, "caller policy") {
		t.Fatalf("direct restricted call was not denied: %#v", resp)
	}

	docDenied := []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"spec_doc_write","arguments":{"kind":"budget"}}}`)
	resp, ok = srv.handleMessage(docDenied)
	if !ok || resp.Error == nil || !strings.Contains(resp.Error.Message, `kind "budget"`) {
		t.Fatalf("role-scoped document was not denied: %#v", resp)
	}
}

func TestCallerPolicyAllowsArchitectDocsAndQASign(t *testing.T) {
	architect := CallerPolicy{Role: "architect", Archetype: "architect"}
	if err := architect.authorizeCall(ToolCallParams{Name: "spec_doc_write", Arguments: json.RawMessage(`{"kind":"budget"}`)}); err != nil {
		t.Fatalf("architect budget denied: %v", err)
	}
	qa := CallerPolicy{Role: "qa-tester", Archetype: "qa-tester"}
	if !qa.allowsTool("quality_sign") {
		t.Fatal("qa-tester quality_sign denied")
	}
}

func TestMemSave(t *testing.T) {
	srv := newTestServer(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "mem_save",
		Arguments: mustMarshal(t, map[string]any{
			"title":   "Test memory",
			"content": "This is the content of the test memory.",
			"type":    "discovery",
		}),
	})

	var saveResp struct {
		ID     string `json:"id"`
		Action string `json:"action"`
	}
	unmarshalToolText(t, resp, &saveResp)

	if saveResp.ID == "" {
		t.Error("expected non-empty id in save response")
	}
	if saveResp.Action != "created" {
		t.Errorf("action = %q, want %q", saveResp.Action, "created")
	}
}

func TestMemSearch(t *testing.T) {
	srv := newTestServer(t)

	// Save a memory to search for.
	process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "mem_save",
		Arguments: mustMarshal(t, map[string]any{
			"title":   "SQLite FTS5 indexing",
			"content": "FTS5 provides fulltext search capabilities in SQLite.",
		}),
	})

	resp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name: "mem_search",
		Arguments: mustMarshal(t, map[string]any{
			"query": "SQLite FTS5",
		}),
	})

	var searchResp struct {
		Results []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"results"`
		Total int `json:"total"`
	}
	unmarshalToolText(t, resp, &searchResp)

	if searchResp.Total == 0 {
		t.Error("expected at least one search result")
	}
}

func TestMemGet(t *testing.T) {
	srv := newTestServer(t)

	// Save a memory to retrieve.
	saveResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "mem_save",
		Arguments: mustMarshal(t, map[string]any{
			"title":   "Architecture note",
			"content": "Hexagonal architecture keeps business logic independent of adapters.",
			"type":    "architecture",
		}),
	})

	var saved struct {
		ID string `json:"id"`
	}
	unmarshalToolText(t, saveResp, &saved)

	// Retrieve by ID.
	resp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name:      "mem_get",
		Arguments: mustMarshal(t, map[string]any{"id": saved.ID}),
	})

	var mem struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	unmarshalToolText(t, resp, &mem)

	if mem.ID != saved.ID {
		t.Errorf("id = %q, want %q", mem.ID, saved.ID)
	}
	if mem.Title != "Architecture note" {
		t.Errorf("title = %q, want %q", mem.Title, "Architecture note")
	}
}

func TestMemContext(t *testing.T) {
	srv := newTestServer(t)

	// Save a few memories so context has something to return.
	for i := 0; i < 3; i++ {
		process(t, srv, "tools/call", i+1, ToolCallParams{
			Name: "mem_save",
			Arguments: mustMarshal(t, map[string]any{
				"title":   "Context test memory",
				"content": "Memory body for context test.",
				"type":    "architecture",
			}),
		})
	}

	resp := process(t, srv, "tools/call", 10, ToolCallParams{
		Name:      "mem_context",
		Arguments: mustMarshal(t, map[string]any{}),
	})

	var ctxResp struct {
		Memories []any `json:"memories"`
		Included int   `json:"included"`
	}
	unmarshalToolText(t, resp, &ctxResp)

	// We should have at least some memories in the context.
	if ctxResp.Included == 0 {
		t.Error("expected at least one memory in context response")
	}
}

func TestMemUpdate(t *testing.T) {
	srv := newTestServer(t)

	// Save a memory to update.
	saveResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "mem_save",
		Arguments: mustMarshal(t, map[string]any{
			"title":   "Original title",
			"content": "Original content.",
		}),
	})
	var saved struct {
		ID string `json:"id"`
	}
	unmarshalToolText(t, saveResp, &saved)

	// Update the title.
	newTitle := "Updated title"
	resp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name: "mem_update",
		Arguments: mustMarshal(t, map[string]any{
			"id":    saved.ID,
			"title": newTitle,
		}),
	})

	var updateResp struct {
		ID     string `json:"id"`
		Action string `json:"action"`
		Title  string `json:"title"`
	}
	unmarshalToolText(t, resp, &updateResp)

	if updateResp.Action != "updated" {
		t.Errorf("action = %q, want %q", updateResp.Action, "updated")
	}
	if updateResp.Title != newTitle {
		t.Errorf("title = %q, want %q", updateResp.Title, newTitle)
	}
}

// TestMemPromote verifies the mem_promote tool marks a memory as
// team-curated (shared=2), that the change is durably persisted (verified by
// a fresh mem_get, not just the promote response), and that calling it twice
// is idempotent (SPEC-063 SS-C).
func TestMemPromote(t *testing.T) {
	srv := newTestServer(t)

	saveResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "mem_save",
		Arguments: mustMarshal(t, map[string]any{
			"title":   "A note to promote",
			"content": "This starts out local-only.",
			"type":    "discovery",
		}),
	})
	var saved struct {
		ID string `json:"id"`
	}
	unmarshalToolText(t, saveResp, &saved)

	resp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name:      "mem_promote",
		Arguments: mustMarshal(t, map[string]any{"id": saved.ID}),
	})

	var promoteResp struct {
		ID     string `json:"id"`
		Shared int    `json:"shared"`
		Status string `json:"status"`
	}
	unmarshalToolText(t, resp, &promoteResp)

	if promoteResp.Shared != 2 {
		t.Errorf("shared = %d, want 2", promoteResp.Shared)
	}
	if promoteResp.Status != "promoted" {
		t.Errorf("status = %q, want %q", promoteResp.Status, "promoted")
	}

	// Reload via mem_get to prove the change was persisted in the DB, not
	// just returned in-memory by mem_promote.
	getResp := process(t, srv, "tools/call", 3, ToolCallParams{
		Name:      "mem_get",
		Arguments: mustMarshal(t, map[string]any{"id": saved.ID}),
	})
	var got struct {
		Shared int `json:"shared"`
	}
	unmarshalToolText(t, getResp, &got)
	if got.Shared != 2 {
		t.Errorf("reloaded shared = %d, want 2 (persisted)", got.Shared)
	}

	// Second call must be idempotent — same shared level, no error.
	resp2 := process(t, srv, "tools/call", 4, ToolCallParams{
		Name:      "mem_promote",
		Arguments: mustMarshal(t, map[string]any{"id": saved.ID}),
	})
	var promoteResp2 struct {
		Shared int `json:"shared"`
	}
	unmarshalToolText(t, resp2, &promoteResp2)
	if promoteResp2.Shared != promoteResp.Shared {
		t.Errorf("mem_promote is not idempotent: first shared=%d, second shared=%d", promoteResp.Shared, promoteResp2.Shared)
	}
}

// TestMemPromote_NotFound verifies that mem_promote with an unknown id
// returns CodeInvalidParams (not CodeMemoryNotFound — SPEC-063 SS-C contract:
// the caller supplied a bad argument).
func TestMemPromote_NotFound(t *testing.T) {
	srv := newTestServer(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "mem_promote",
		Arguments: mustMarshal(t, map[string]any{"id": "01938f1b-0000-7000-8000-000000000000"}),
	})

	if resp.Error == nil {
		t.Fatal("expected an error response for a non-existent id")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want %d (CodeInvalidParams)", resp.Error.Code, CodeInvalidParams)
	}
}

// TestMemPromote_ProfileProvenance_RejectedAsInvalidParams is SPEC-094 §4
// AC4/AC9's MCP surface: promoting a memory with profile provenance must
// come back as CodeInvalidParams — an operator error, not an internal
// failure — carrying model.ErrProfileMemoryNotShareable's message. There is
// no public MCP tool that stamps profile provenance (mem_save's Source field
// is json:"-"), so this seeds the row directly via the underlying
// MemoryService.SaveProfileRule, exactly as a real profile activation would
// (SPEC-092 §2).
func TestMemPromote_ProfileProvenance_RejectedAsInvalidParams(t *testing.T) {
	projectDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open project db: %v", err)
	}
	t.Cleanup(func() { projectDB.Close() })
	globalDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open global db: %v", err)
	}
	t.Cleanup(func() { globalDB.Close() })

	svc := service.NewMemoryService(store.NewMemoryStore(projectDB), store.NewMemoryStore(globalDB), config.Default(), "test-project", embed.NopEmbedder{})
	srv := NewServer(svc, nil, nil, nil, slog.Default(), "all", "test")

	saveResp, err := svc.SaveProfileRule(t.Context(), model.SaveRequest{
		Title:     "A profile-injected rule",
		Content:   "Team standard rule.",
		AppliesTo: []string{"**"},
	}, "chatea-pro")
	if err != nil {
		t.Fatalf("SaveProfileRule: unexpected error: %v", err)
	}

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "mem_promote",
		Arguments: mustMarshal(t, map[string]any{"id": saveResp.ID}),
	})

	if resp.Error == nil {
		t.Fatal("expected an error response for a profile-provenance memory")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want %d (CodeInvalidParams)", resp.Error.Code, CodeInvalidParams)
	}
	if !strings.Contains(resp.Error.Message, "cannot be promoted") {
		t.Errorf("error message = %q, want it to mention ErrProfileMemoryNotShareable", resp.Error.Message)
	}

	reloaded, err := svc.Get(t.Context(), saveResp.ID)
	if err != nil {
		t.Fatalf("Get after rejected promote: unexpected error: %v", err)
	}
	if reloaded.Shared != 0 {
		t.Errorf("Shared must stay 0 after a rejected promote, got %d", reloaded.Shared)
	}
}

func TestMemSessionEnd(t *testing.T) {
	srv := newTestServer(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "mem_session_end",
		Arguments: mustMarshal(t, map[string]any{
			"summary": "Implemented the MCP server and wrote tests.",
		}),
	})

	var sessResp struct {
		SessionID       string `json:"session_id"`
		SummaryMemoryID string `json:"summary_memory_id"`
	}
	unmarshalToolText(t, resp, &sessResp)

	if sessResp.SessionID == "" {
		t.Error("expected non-empty session_id")
	}
	if sessResp.SummaryMemoryID == "" {
		t.Error("expected non-empty summary_memory_id")
	}
}

func TestMemCheckpoint(t *testing.T) {
	srv := newTestServer(t)

	// First call should create the checkpoint.
	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "mem_checkpoint",
		Arguments: mustMarshal(t, map[string]any{
			"summary":    "working on auth handler",
			"decisions":  "using JWT tokens",
			"next_steps": "write tests",
		}),
	})

	var checkResp struct {
		ID     string `json:"id"`
		Action string `json:"action"`
	}
	unmarshalToolText(t, resp, &checkResp)

	if checkResp.ID == "" {
		t.Error("expected non-empty id in checkpoint response")
	}
	if checkResp.Action != "created" {
		t.Errorf("action = %q, want %q", checkResp.Action, "created")
	}

	// Second call should update (upsert) the existing checkpoint.
	resp2 := process(t, srv, "tools/call", 2, ToolCallParams{
		Name: "mem_checkpoint",
		Arguments: mustMarshal(t, map[string]any{
			"summary": "auth handler complete, writing tests now",
		}),
	})

	var checkResp2 struct {
		ID     string `json:"id"`
		Action string `json:"action"`
	}
	unmarshalToolText(t, resp2, &checkResp2)

	if checkResp2.Action != "updated" {
		t.Errorf("second call action = %q, want %q", checkResp2.Action, "updated")
	}
	if checkResp.ID != checkResp2.ID {
		t.Errorf("id changed between checkpoints: %s → %s", checkResp.ID, checkResp2.ID)
	}
}

func TestMemCheckpoint_ValidationError(t *testing.T) {
	srv := newTestServer(t)

	// Call without required summary field → CodeInvalidParams.
	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "mem_checkpoint",
		Arguments: mustMarshal(t, map[string]any{}),
	})

	if resp.Error == nil {
		t.Fatal("expected JSON-RPC error for missing summary, got nil")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want %d (CodeInvalidParams)", resp.Error.Code, CodeInvalidParams)
	}
	if !strings.Contains(resp.Error.Message, "summary") {
		t.Errorf("error message %q should mention 'summary'", resp.Error.Message)
	}
}

func TestMemSuggestTopicKey(t *testing.T) {
	srv := newTestServer(t)

	// Save a memory with a topic key so it appears in suggestions.
	process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "mem_save",
		Arguments: mustMarshal(t, map[string]any{
			"title":     "Architecture decision for auth",
			"content":   "Use JWT tokens for stateless auth.",
			"type":      "decision",
			"topic_key": "decision/auth-model",
		}),
	})

	resp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name: "mem_suggest_topic_key",
		Arguments: mustMarshal(t, map[string]any{
			"title": "Architecture decision for auth",
		}),
	})

	var suggestion struct {
		Suggestion string `json:"suggestion"`
		IsNewTopic bool   `json:"is_new_topic"`
	}
	unmarshalToolText(t, resp, &suggestion)

	if suggestion.Suggestion == "" {
		t.Error("expected non-empty suggestion")
	}
}

func TestMemSave_Validation(t *testing.T) {
	srv := newTestServer(t)

	// Save without required title field → CodeInvalidParams.
	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "mem_save",
		Arguments: mustMarshal(t, map[string]any{
			"content": "Missing title field.",
		}),
	})

	if resp.Error == nil {
		t.Fatal("expected JSON-RPC error for missing title, got nil")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want %d (CodeInvalidParams)", resp.Error.Code, CodeInvalidParams)
	}
}

func TestMemGet_NotFound(t *testing.T) {
	srv := newTestServer(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "mem_get",
		Arguments: mustMarshal(t, map[string]any{"id": "01910000-0000-7000-8000-000000000000"}),
	})

	if resp.Error == nil {
		t.Fatal("expected JSON-RPC error for unknown id, got nil")
	}
	if resp.Error.Code != CodeMemoryNotFound {
		t.Errorf("error code = %d, want %d (CodeMemoryNotFound)", resp.Error.Code, CodeMemoryNotFound)
	}
}

func TestUnknownMethod(t *testing.T) {
	srv := newTestServer(t)

	resp := process(t, srv, "totally/unknown", 1, nil)

	if resp.Error == nil {
		t.Fatal("expected JSON-RPC error for unknown method, got nil")
	}
	if resp.Error.Code != CodeMethodNotFound {
		t.Errorf("error code = %d, want %d (CodeMethodNotFound)", resp.Error.Code, CodeMethodNotFound)
	}
}

func TestUnknownTool(t *testing.T) {
	srv := newTestServer(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "mem_does_not_exist",
		Arguments: mustMarshal(t, map[string]any{}),
	})

	if resp.Error == nil {
		t.Fatal("expected JSON-RPC error for unknown tool, got nil")
	}
	if resp.Error.Code != CodeMethodNotFound {
		t.Errorf("error code = %d, want %d (CodeMethodNotFound)", resp.Error.Code, CodeMethodNotFound)
	}
}

// TestRunLoop exercises Server.Run using real buffers to ensure the I/O loop
// wires up correctly around handleMessage.
func TestRunLoop(t *testing.T) {
	srv := newTestServer(t)

	var in bytes.Buffer

	// Write initialize + tools/list requests.
	sendMessage(t, &in, "initialize", 1, map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "t", "version": "1"},
	})
	sendMessage(t, &in, "tools/list", 2, nil)
	// Write a notification (no id) — should produce no response.
	notif := `{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"
	in.WriteString(notif)

	var out bytes.Buffer

	// Run with a context — we create a reader that returns EOF after the input
	// so Run terminates naturally.
	ctx := t.Context()
	if err := srv.Run(ctx, &in, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Expect exactly 2 responses (initialize + tools/list; notification has none).
	scanner := newResponseScanner(strings.NewReader(out.String()))
	for i := 1; i <= 2; i++ {
		if !scanner.Scan() {
			t.Fatalf("expected response %d, got EOF", i)
		}
		var resp JSONRPCResponse
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			t.Fatalf("response %d unmarshal: %v", i, err)
		}
		if resp.Error != nil {
			t.Errorf("response %d has error: %v", i, resp.Error)
		}
	}
	if scanner.Scan() {
		t.Errorf("unexpected extra response line: %s", scanner.Text())
	}
}

// initializeParams returns the standard params payload for an "initialize"
// request, shared by every TestRunLoop_LargeMessages case so the handshake
// itself is never the thing under test.
func initializeParams() map[string]any {
	return map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "t", "version": "1"},
	}
}

// paddedToolsListLine builds a "tools/list" request line of exactly target
// bytes. "tools/list" is used as the size knob because dispatchMethod ignores
// its Params entirely (server.go), so padding an otherwise-unused "pad" field
// grows the line to any target size without ever making the message invalid
// — the test controls size independently of any tool's argument schema.
func paddedToolsListLine(t *testing.T, id int, target int) []byte {
	t.Helper()

	base := buildRequestLine(t, "tools/list", id, map[string]any{"pad": ""})
	if target < len(base) {
		t.Fatalf("paddedToolsListLine: target %d is smaller than the unpadded base (%d bytes)", target, len(base))
	}

	line := buildRequestLine(t, "tools/list", id, map[string]any{"pad": strings.Repeat("a", target-len(base))})
	if len(line) != target {
		t.Fatalf("paddedToolsListLine: built %d bytes, want %d", len(line), target)
	}
	return line
}

// paddedLineParamsBeforeID builds a raw "tools/list" request line of exactly
// target bytes with "id" placed textually AFTER "params" — the opposite
// order buildRequestLine's fixed struct layout produces. Once padded well
// past idPrefixBytes, the id lands outside the retained prefix, exercising
// requestIDFromPrefix's documented fallback (DD6): a client that happens to
// emit params before id gets a `null`-correlated error, not a wrong guess.
func paddedLineParamsBeforeID(t *testing.T, id int, target int) []byte {
	t.Helper()

	build := func(pad string) string {
		return fmt.Sprintf(`{"jsonrpc":"2.0","method":"tools/list","params":{"pad":%q},"id":%d}`, pad, id)
	}

	base := build("")
	if target < len(base) {
		t.Fatalf("paddedLineParamsBeforeID: target %d is smaller than the unpadded base (%d bytes)", target, len(base))
	}

	line := build(strings.Repeat("a", target-len(base)))
	if len(line) != target {
		t.Fatalf("paddedLineParamsBeforeID: built %d bytes, want %d", len(line), target)
	}
	return []byte(line)
}

// TestRunLoop_LargeMessages is the regression test for the crash fixed by
// SPEC-104: a message near or over the historical 64 KiB bufio.Scanner token
// limit must never stop the server from answering the messages that follow
// it. Every case sends three messages — initialize (id 1), the message under
// test (id 2), and a final tools/list (id 3, the "survival canary") — and
// asserts: Run returns nil, exactly 3 responses arrive (no cascade, AC8), and
// response 3 (the canary) is error-free. The "limit+1" case additionally
// lowers srv.maxMessage (DD2) to exercise the real size-limit branch cheaply
// and asserts response 2 carries CodeMessageTooLarge; id recovery from the
// oversized message's prefix (DD6) lands in P4, so today it asserts "null".
func TestRunLoop_LargeMessages(t *testing.T) {
	const (
		size64KBMinus1 = 64*1024 - 1 // 65535 bytes: valid, must keep working (AC5).
		size64KBPlus1  = 64*1024 + 1 // 65537 bytes: today's exact crash trigger (AC4).
	)

	tests := []struct {
		name             string
		buildInput       func(t *testing.T, srv *Server) []byte
		serverMaxMessage int    // 0 = leave the server's default (maxMessageBytes)
		wantSecondID     string // expected literal json for responses[1].ID
		wantSecondError  bool
		wantSecondCode   int
	}{
		{
			name: "64KB-1",
			buildInput: func(t *testing.T, _ *Server) []byte {
				var buf bytes.Buffer
				buf.Write(buildRequestLine(t, "initialize", 1, initializeParams()))
				buf.WriteByte('\n')
				buf.Write(paddedToolsListLine(t, 2, size64KBMinus1))
				buf.WriteByte('\n')
				buf.Write(buildRequestLine(t, "tools/list", 3, nil))
				buf.WriteByte('\n')
				return buf.Bytes()
			},
			wantSecondID: "2",
		},
		{
			name: "64KB+1",
			buildInput: func(t *testing.T, _ *Server) []byte {
				var buf bytes.Buffer
				buf.Write(buildRequestLine(t, "initialize", 1, initializeParams()))
				buf.WriteByte('\n')
				buf.Write(paddedToolsListLine(t, 2, size64KBPlus1))
				buf.WriteByte('\n')
				buf.Write(buildRequestLine(t, "tools/list", 3, nil))
				buf.WriteByte('\n')
				return buf.Bytes()
			},
			wantSecondID: "2",
		},
		{
			name: "sin newline final",
			buildInput: func(t *testing.T, _ *Server) []byte {
				var buf bytes.Buffer
				buf.Write(buildRequestLine(t, "initialize", 1, initializeParams()))
				buf.WriteByte('\n')
				buf.Write(buildRequestLine(t, "tools/list", 2, nil))
				buf.WriteByte('\n')
				buf.Write(buildRequestLine(t, "tools/list", 3, nil)) // no trailing '\n'
				return buf.Bytes()
			},
			wantSecondID: "2",
		},
		{
			name: "CRLF",
			buildInput: func(t *testing.T, _ *Server) []byte {
				var buf bytes.Buffer
				buf.Write(buildRequestLine(t, "initialize", 1, initializeParams()))
				buf.WriteString("\r\n")
				buf.Write(buildRequestLine(t, "tools/list", 2, nil))
				buf.WriteString("\r\n")
				buf.Write(buildRequestLine(t, "tools/list", 3, nil))
				buf.WriteString("\r\n")
				return buf.Bytes()
			},
			wantSecondID: "2",
		},
		{
			// paddedToolsListLine (via buildRequestLine) places "id" right
			// after "jsonrpc" in the marshaled struct, well within
			// idPrefixBytes — so the id must be recovered, not null (AC7).
			name:             "limit+1",
			serverMaxMessage: 1024,
			buildInput: func(t *testing.T, srv *Server) []byte {
				var buf bytes.Buffer
				buf.Write(buildRequestLine(t, "initialize", 1, initializeParams()))
				buf.WriteByte('\n')
				buf.Write(paddedToolsListLine(t, 2, srv.maxMessage+1))
				buf.WriteByte('\n')
				buf.Write(buildRequestLine(t, "tools/list", 3, nil))
				buf.WriteByte('\n')
				return buf.Bytes()
			},
			wantSecondID:    "2",
			wantSecondError: true,
			wantSecondCode:  CodeMessageTooLarge,
		},
		{
			// "params" is placed BEFORE "id" in the raw JSON text (unlike
			// buildRequestLine's fixed struct field order), so once padded
			// past idPrefixBytes the id sits outside the retained prefix —
			// the documented fallback (DD6): null, not a wrong guess.
			name:             "limit+1 sin id recuperable",
			serverMaxMessage: 1024,
			buildInput: func(t *testing.T, srv *Server) []byte {
				var buf bytes.Buffer
				buf.Write(buildRequestLine(t, "initialize", 1, initializeParams()))
				buf.WriteByte('\n')
				buf.Write(paddedLineParamsBeforeID(t, 2, srv.maxMessage+1))
				buf.WriteByte('\n')
				buf.Write(buildRequestLine(t, "tools/list", 3, nil))
				buf.WriteByte('\n')
				return buf.Bytes()
			},
			wantSecondID:    "null",
			wantSecondError: true,
			wantSecondCode:  CodeMessageTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			if tt.serverMaxMessage > 0 {
				srv.maxMessage = tt.serverMaxMessage
			}
			in := bytes.NewReader(tt.buildInput(t, srv))
			var out bytes.Buffer

			if err := srv.Run(t.Context(), in, &out); err != nil {
				t.Fatalf("Run: %v", err)
			}

			scanner := newResponseScanner(bytes.NewReader(out.Bytes()))
			var responses []JSONRPCResponse
			for scanner.Scan() {
				var resp JSONRPCResponse
				if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
					t.Fatalf("unmarshal response: %v (raw: %s)", err, scanner.Text())
				}
				responses = append(responses, resp)
			}
			if err := scanner.Err(); err != nil {
				t.Fatalf("scan responses: %v", err)
			}

			// Exactly 3 responses: no cascade of extra error responses after
			// the oversized message is discarded (AC8).
			if len(responses) != 3 {
				t.Fatalf("got %d responses, want exactly 3", len(responses))
			}

			second := responses[1]
			if string(second.ID) != tt.wantSecondID {
				t.Errorf("responses[1].ID = %s, want %s", second.ID, tt.wantSecondID)
			}
			if tt.wantSecondError {
				if second.Error == nil {
					t.Fatal("responses[1].Error = nil, want an error")
				}
				if second.Error.Code != tt.wantSecondCode {
					t.Errorf("responses[1].Error.Code = %d, want %d", second.Error.Code, tt.wantSecondCode)
				}
				if !strings.HasPrefix(second.Error.Message, "mcp: message too large:") {
					t.Errorf("responses[1].Error.Message = %q, want prefix %q", second.Error.Message, "mcp: message too large:")
				}
			} else if second.Error != nil {
				t.Errorf("responses[1] has unexpected error: %v", second.Error)
			}

			// The property under test is survival: id 3 (the canary) must
			// have been answered correctly regardless of what happened to
			// message 2.
			third := responses[2]
			if string(third.ID) != "3" {
				t.Errorf("responses[2].ID = %s, want 3 (survival canary)", third.ID)
			}
			if third.Error != nil {
				t.Errorf("responses[2] (canary) has unexpected error: %v", third.Error)
			}
		})
	}
}

// mustMarshal marshals v or fails the test.
func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("mustMarshal: %v", err)
	}
	return b
}

// Ensure readResponse is referenced (used in TestRunLoop via scanner directly).
var _ = readResponse
