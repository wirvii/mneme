package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/db"
	"github.com/juanftp/mneme/internal/embed"
	"github.com/juanftp/mneme/internal/service"
	"github.com/juanftp/mneme/internal/store"
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
	return NewServer(svc, logger, "all", "test")
}

// sendMessage writes a single JSON-RPC request as a line to buf and returns the
// raw bytes written (useful for debugging).
func sendMessage(t *testing.T, buf *bytes.Buffer, method string, id int, params any) {
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
	buf.Write(b)
	buf.WriteByte('\n')
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

// processNotification sends a notification (no response expected).
func processNotification(t *testing.T, srv *Server, method string, params any) {
	t.Helper()
	var in bytes.Buffer
	// Notifications have no id field in the JSON.
	req := struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	b, _ := json.Marshal(req)
	in.Write(b)

	_, hasResp := srv.handleMessage(in.Bytes())
	if hasResp {
		t.Fatalf("processNotification: expected no response for notification %s", method)
	}
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
		"mem_save", "mem_search", "mem_get", "mem_context",
		"mem_update", "mem_session_end", "mem_suggest_topic_key",
		"mem_relate", "mem_timeline", "mem_stats", "mem_checkpoint", "mem_forget",
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
	scanner := bufio.NewScanner(strings.NewReader(out.String()))
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
