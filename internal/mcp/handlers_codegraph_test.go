package mcp

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/codegraph"
	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// TestMCP_AllCodegraphToolsRegistered verifies that all 10 codegraph tools are
// present in the allTools() slice.
func TestMCP_AllCodegraphToolsRegistered(t *testing.T) {
	tools := allTools()
	wantTools := []string{
		"codegraph_search", "codegraph_context", "codegraph_callers",
		"codegraph_callees", "codegraph_impact", "codegraph_node",
		"codegraph_explore", "codegraph_trace", "codegraph_status", "codegraph_files",
	}
	for _, want := range wantTools {
		found := false
		for _, tool := range tools {
			if tool.Name == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("tool %q not registered in allTools()", want)
		}
	}
}

// newTestServerWithCodeGraph creates a Server with a CodeGraphService backed by
// an in-memory DB. It indexes a temp dir with two Go source files so that
// codegraph queries have data to work with.
func newTestServerWithCodeGraph(t *testing.T) *Server {
	t.Helper()

	// Memory service (required for server construction).
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

	// CodeGraph service with in-memory DB and indexed files.
	cdb, err := codegraph.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("open codegraph db: %v", err)
	}
	t.Cleanup(func() { _ = cdb.Close() })

	cgSvc := service.NewCodeGraphServiceFromDB(cdb)

	// Index a temp dir with Go files.
	dir := t.TempDir()
	writeTestGoFiles(t, dir)
	if _, err := cgSvc.Index(codegraph.IndexOptions{RootDir: dir}); err != nil {
		t.Fatalf("codegraph index: %v", err)
	}

	logger := slog.Default()
	srv := NewServer(svc, nil, nil, nil, logger, "all", "test")
	// Inject the CodeGraphService directly into the handlers.
	srv.handlers.cgSvc = cgSvc
	return srv
}

// writeTestGoFiles writes two Go source files to dir for codegraph indexing.
func writeTestGoFiles(t *testing.T, dir string) {
	t.Helper()
	writeGoFile(t, dir, "main.go", `package main

import "fmt"

func main() {
	fmt.Println(Hello())
}

func Hello() string {
	return "hello"
}
`)
	writeGoFile(t, dir, "util.go", `package main

// Add adds two numbers.
func Add(a, b int) int {
	return a + b
}
`)
}

func writeGoFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// TestMCP_CodegraphStatus verifies that codegraph_status returns a valid text
// response when the codegraph DB exists (even if empty).
func TestMCP_CodegraphStatus(t *testing.T) {
	srv := newTestServerWithCodeGraph(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "codegraph_status",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if resp.Error != nil {
		t.Fatalf("codegraph_status: code=%d message=%s", resp.Error.Code, resp.Error.Message)
	}

	// Verify the response has a text content block.
	var result ToolCallResult
	unmarshalResult(t, resp, &result)
	if len(result.Content) == 0 {
		t.Fatal("expected at least one content block")
	}
	if result.Content[0].Type != "text" {
		t.Errorf("content type = %q, want %q", result.Content[0].Type, "text")
	}
	if result.Content[0].Text == "" {
		t.Error("expected non-empty text in status response")
	}
}

// TestMCP_CodegraphSearch_MissingQuery verifies that codegraph_search without
// a query parameter returns -32602 (CodeInvalidParams).
func TestMCP_CodegraphSearch_MissingQuery(t *testing.T) {
	srv := newTestServerWithCodeGraph(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "codegraph_search",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if resp.Error == nil {
		t.Fatal("expected error for missing query, got nil")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want %d (CodeInvalidParams)", resp.Error.Code, CodeInvalidParams)
	}
}

// TestMCP_CodegraphSearch_Success verifies that codegraph_search returns results
// when matching symbols exist in the indexed code.
func TestMCP_CodegraphSearch_Success(t *testing.T) {
	srv := newTestServerWithCodeGraph(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "codegraph_search",
		Arguments: mustMarshal(t, map[string]any{
			"query": "Hello",
		}),
	})
	if resp.Error != nil {
		t.Fatalf("codegraph_search: code=%d message=%s", resp.Error.Code, resp.Error.Message)
	}

	var result ToolCallResult
	unmarshalResult(t, resp, &result)
	if len(result.Content) == 0 {
		t.Fatal("expected content blocks")
	}
	if result.Content[0].Text == "" {
		t.Error("expected non-empty search results")
	}
}

// TestMCP_CodegraphCallers_MissingSymbol verifies that codegraph_callers without
// the required symbol parameter returns -32602.
func TestMCP_CodegraphCallers_MissingSymbol(t *testing.T) {
	srv := newTestServerWithCodeGraph(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "codegraph_callers",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if resp.Error == nil {
		t.Fatal("expected error for missing symbol, got nil")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want %d", resp.Error.Code, CodeInvalidParams)
	}
}

// TestMCP_CodegraphFiles verifies that codegraph_files returns a response listing
// the indexed files.
func TestMCP_CodegraphFiles(t *testing.T) {
	srv := newTestServerWithCodeGraph(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "codegraph_files",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if resp.Error != nil {
		t.Fatalf("codegraph_files: code=%d message=%s", resp.Error.Code, resp.Error.Message)
	}

	var result ToolCallResult
	unmarshalResult(t, resp, &result)
	if len(result.Content) == 0 {
		t.Fatal("expected content blocks")
	}
	if result.Content[0].Text == "" {
		t.Error("expected non-empty files list")
	}
}

// TestMCP_MemContext_IncludesCodeGraphHint_Indexed verifies that when a codegraph
// service has indexed data, the mem_context response includes a codegraph_hint
// field with symbol/file counts and tool usage instructions.
func TestMCP_MemContext_IncludesCodeGraphHint_Indexed(t *testing.T) {
	srv := newTestServerWithCodeGraph(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "mem_context",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if resp.Error != nil {
		t.Fatalf("mem_context: code=%d message=%s", resp.Error.Code, resp.Error.Message)
	}

	var ctxResp model.ContextResponse
	unmarshalToolText(t, resp, &ctxResp)

	if ctxResp.CodeGraphHint == "" {
		t.Fatal("expected codegraph_hint to be non-empty when cgSvc has indexed data")
	}
	if !strings.Contains(ctxResp.CodeGraphHint, "Code Graph (indexed)") {
		t.Errorf("codegraph_hint should indicate indexed state, got: %s", ctxResp.CodeGraphHint)
	}
	if !strings.Contains(ctxResp.CodeGraphHint, "codegraph_search") {
		t.Errorf("codegraph_hint should mention codegraph_search tool, got: %s", ctxResp.CodeGraphHint)
	}
	if strings.HasPrefix(ctxResp.CodeGraphHint, codegraph.NoticeToken) {
		t.Errorf("codegraph_hint carries the notice on a HEALTHY graph: %s", ctxResp.CodeGraphHint)
	}
}

// TestMCP_MemContext_IncludesCodeGraphHint_Degraded is SPEC-142 AC9 — the
// codegraph hint is the first thing an agent reads every session, and on a
// graph missing a whole language, "Code Graph (indexed): N symbols" without
// qualification is exactly the half-true claim this spec exists to stop.
func TestMCP_MemContext_IncludesCodeGraphHint_Degraded(t *testing.T) {
	srv := newTestServerWithMarkedCodeGraph(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "mem_context",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if resp.Error != nil {
		t.Fatalf("mem_context: code=%d message=%s", resp.Error.Code, resp.Error.Message)
	}

	var ctxResp model.ContextResponse
	unmarshalToolText(t, resp, &ctxResp)

	if !strings.HasPrefix(ctxResp.CodeGraphHint, codegraph.NoticeToken) {
		t.Errorf("codegraph_hint does not start with the notice on a marked graph: %s", ctxResp.CodeGraphHint)
	}
}

// TestMCP_MemContext_IncludesCodeGraphHint_NoCgSvc verifies that when no
// codegraph service is available (no project slug), the mem_context response
// includes the generic codegraph hint.
func TestMCP_MemContext_IncludesCodeGraphHint_NoCgSvc(t *testing.T) {
	// Use a server with no project slug to trigger the generic hint path.
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
	// Empty project slug means no codegraph DB can be found.
	svc := service.NewMemoryService(projectStore, globalStore, cfg, "", embed.NopEmbedder{})

	logger := slog.Default()
	srv := NewServer(svc, nil, nil, nil, logger, "all", "test")

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "mem_context",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if resp.Error != nil {
		t.Fatalf("mem_context: code=%d message=%s", resp.Error.Code, resp.Error.Message)
	}

	var ctxResp model.ContextResponse
	unmarshalToolText(t, resp, &ctxResp)

	if ctxResp.CodeGraphHint == "" {
		t.Fatal("expected codegraph_hint to be non-empty even without project")
	}
	if !strings.Contains(ctxResp.CodeGraphHint, "codegraph_search") {
		t.Errorf("generic hint should mention codegraph_search, got: %s", ctxResp.CodeGraphHint)
	}
}

// TestMCP_MemContext_IncludesCodeGraphHint_NotIndexed verifies that when cgSvc
// exists but has no data (empty DB), the hint indicates indexing is needed.
func TestMCP_MemContext_IncludesCodeGraphHint_NotIndexed(t *testing.T) {
	// Create a server with an empty codegraph DB (no indexed data).
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

	// Empty codegraph DB — no nodes.
	cdb, err := codegraph.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("open codegraph db: %v", err)
	}
	t.Cleanup(func() { _ = cdb.Close() })
	cgSvc := service.NewCodeGraphServiceFromDB(cdb)

	logger := slog.Default()
	srv := NewServer(svc, nil, nil, nil, logger, "all", "test")
	srv.handlers.cgSvc = cgSvc

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "mem_context",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if resp.Error != nil {
		t.Fatalf("mem_context: code=%d message=%s", resp.Error.Code, resp.Error.Message)
	}

	var ctxResp model.ContextResponse
	unmarshalToolText(t, resp, &ctxResp)

	if ctxResp.CodeGraphHint == "" {
		t.Fatal("expected codegraph_hint to be non-empty for empty codegraph DB")
	}
	if !strings.Contains(ctxResp.CodeGraphHint, "not yet indexed") {
		t.Errorf("hint should mention 'not yet indexed', got: %s", ctxResp.CodeGraphHint)
	}
}

// codegraphMinimalArgs declares, per codegraph_* tool, the minimal argument
// set that makes it succeed against the fixture indexed by
// newTestServerWithCodeGraph (SPEC-142 AC6). TestMCP_CodegraphToolsCarryGraphNotice
// checks set equality between this map's keys and allTools()'s own
// "codegraph_"-prefixed population, in BOTH directions — so neither an
// eleventh tool nor a stale entry here can go unnoticed (plan step 1, form 4:
// a criterion must never enumerate its own population by hand and leave it
// unverified against the real source).
var codegraphMinimalArgs = map[string]map[string]any{
	"codegraph_search":  {"query": "Hello"},
	"codegraph_context": {"symbol": "Hello"},
	"codegraph_callers": {"symbol": "Hello"},
	"codegraph_callees": {"symbol": "Hello"},
	"codegraph_impact":  {"symbol": "Hello"},
	"codegraph_node":    {"symbol": "Hello"},
	"codegraph_explore": {"symbols": []any{"Hello"}},
	"codegraph_trace":   {"from": "Hello", "to": "Hello"},
	"codegraph_status":  {},
	"codegraph_files":   {},
}

// codegraphToolNames returns the "codegraph_"-prefixed subset of allTools(),
// the population every set-equality check below is DERIVED from rather than
// hand-listed.
func codegraphToolNames(t *testing.T) []string {
	t.Helper()
	var names []string
	for _, tool := range allTools() {
		if strings.HasPrefix(tool.Name, "codegraph_") {
			names = append(names, tool.Name)
		}
	}
	return names
}

// newTestServerWithMarkedCodeGraph is newTestServerWithCodeGraph plus a
// typescript degraded-language mark written directly to the SAME underlying
// DB the server's CodeGraphService reads from — the shortest honest way to
// produce "a graph indexed and known incomplete" without depending on a real
// Node.js toolchain in this unit test.
func newTestServerWithMarkedCodeGraph(t *testing.T) *Server {
	t.Helper()

	cdb, err := codegraph.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("open codegraph db: %v", err)
	}
	t.Cleanup(func() { _ = cdb.Close() })

	cgSvc := service.NewCodeGraphServiceFromDB(cdb)
	dir := t.TempDir()
	writeTestGoFiles(t, dir)
	if _, err := cgSvc.Index(codegraph.IndexOptions{RootDir: dir}); err != nil {
		t.Fatalf("codegraph index: %v", err)
	}

	// The mark is written AFTER indexing: a plain-Go fixture's own full scan
	// would otherwise REPLACE it with an empty finding (SPEC-142 D6) the
	// instant Index runs, since a full scan asserts the complete truth about
	// what it just walked.
	st := codegraph.NewStore(cdb)
	if err := st.SetDegradedLanguages([]codegraph.DegradedLanguage{
		{Language: "typescript", Cause: codegraph.CauseToolchainIncompatible, Reason: "test fixture"},
	}); err != nil {
		t.Fatalf("SetDegradedLanguages: %v", err)
	}

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
	srv := NewServer(svc, nil, nil, nil, logger, "all", "test")
	srv.handlers.cgSvc = cgSvc
	return srv
}

// TestMCP_CodegraphToolsCarryGraphNotice is SPEC-142 AC6/AC7: every
// codegraph_* tool must declare the graph-incompleteness notice on a marked
// graph, and must NOT on a healthy one — measured through the real dispatch
// path (process -> handleMessage -> handleToolCall), never through
// codegraph.Notice called in isolation.
func TestMCP_CodegraphToolsCarryGraphNotice(t *testing.T) {
	names := codegraphToolNames(t)

	// Set-equality in BOTH directions against codegraphMinimalArgs (plan
	// step 1, form 4): neither an eleventh tool nor a stale map entry can go
	// unnoticed.
	declared := make(map[string]bool, len(codegraphMinimalArgs))
	for name := range codegraphMinimalArgs {
		declared[name] = true
	}
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		seen[name] = true
		if !declared[name] {
			t.Errorf("allTools() declares %q but codegraphMinimalArgs has no entry for it", name)
		}
	}
	for name := range declared {
		if !seen[name] {
			t.Errorf("codegraphMinimalArgs declares %q but it is not in allTools()'s codegraph_ population", name)
		}
	}

	for _, name := range names {
		name := name

		t.Run("marked_"+name, func(t *testing.T) {
			srv := newTestServerWithMarkedCodeGraph(t)
			resp := process(t, srv, "tools/call", 1, ToolCallParams{
				Name:      name,
				Arguments: mustMarshal(t, codegraphMinimalArgs[name]),
			})
			if resp.Error != nil {
				if !strings.HasPrefix(resp.Error.Message, codegraph.NoticeToken) {
					t.Errorf("%s: error message does not start with the notice on a marked graph: %q", name, resp.Error.Message)
				}
				return
			}
			var result ToolCallResult
			unmarshalResult(t, resp, &result)
			if len(result.Content) == 0 || !strings.HasPrefix(result.Content[0].Text, codegraph.NoticeToken) {
				t.Errorf("%s: result does not start with the notice on a marked graph: %+v", name, result.Content)
			}
		})

		t.Run("healthy_"+name, func(t *testing.T) {
			srv := newTestServerWithCodeGraph(t)
			resp := process(t, srv, "tools/call", 1, ToolCallParams{
				Name:      name,
				Arguments: mustMarshal(t, codegraphMinimalArgs[name]),
			})
			if resp.Error != nil {
				if strings.HasPrefix(resp.Error.Message, codegraph.NoticeToken) {
					t.Errorf("%s: error message carries the notice on a HEALTHY graph: %q", name, resp.Error.Message)
				}
				return
			}
			var result ToolCallResult
			unmarshalResult(t, resp, &result)
			if len(result.Content) > 0 && strings.HasPrefix(result.Content[0].Text, codegraph.NoticeToken) {
				t.Errorf("%s: result carries the notice on a HEALTHY graph: %q", name, result.Content[0].Text)
			}
		})
	}
}

// TestMCP_CodegraphSearch_EmptyResultCarriesNotice is SPEC-142 AC7 — the
// single scenario O1 exists for, given its own dedicated assertion rather
// than folding it into the loop above: a query that finds NOTHING on a
// marked graph must still declare the graph incomplete. Without this, a
// zero-result answer reads exactly like "this symbol does not exist",
// when it may simply belong to the language nobody could index.
func TestMCP_CodegraphSearch_EmptyResultCarriesNotice(t *testing.T) {
	srv := newTestServerWithMarkedCodeGraph(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "codegraph_search",
		Arguments: mustMarshal(t, map[string]any{"query": "ThisSymbolDoesNotExistAnywhere"}),
	})
	if resp.Error != nil {
		t.Fatalf("codegraph_search: code=%d message=%s", resp.Error.Code, resp.Error.Message)
	}

	var result ToolCallResult
	unmarshalResult(t, resp, &result)
	if len(result.Content) == 0 {
		t.Fatal("expected a content block")
	}
	text := result.Content[0].Text
	if !strings.HasPrefix(text, codegraph.NoticeToken) {
		t.Errorf("empty-result text does not start with the notice: %q", text)
	}
	if !strings.Contains(text, "No results found.") {
		t.Errorf("expected the underlying empty-result message to survive, got: %q", text)
	}
}

// TestMCP_CodegraphExplore_NoticeSurvivesBudgetTruncation is SPEC-142 AC8:
// the notice is prepended AFTER codegraph_explore's own budget cutoff, so it
// is never itself counted against that budget — the total response length
// may exceed budget by the notice's own length.
func TestMCP_CodegraphExplore_NoticeSurvivesBudgetTruncation(t *testing.T) {
	srv := newTestServerWithMarkedCodeGraph(t)

	const budget = 200
	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "codegraph_explore",
		Arguments: mustMarshal(t, map[string]any{
			"symbols": []any{"Hello"},
			"budget":  budget,
		}),
	})
	if resp.Error != nil {
		t.Fatalf("codegraph_explore: code=%d message=%s", resp.Error.Code, resp.Error.Message)
	}

	var result ToolCallResult
	unmarshalResult(t, resp, &result)
	if len(result.Content) == 0 {
		t.Fatal("expected a content block")
	}
	text := result.Content[0].Text
	if !strings.HasPrefix(text, codegraph.NoticeToken) {
		t.Errorf("codegraph_explore text does not start with the notice: %q", text)
	}
}

