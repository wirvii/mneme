package mcp

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/juanftp/mneme/internal/codegraph"
	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/db"
	"github.com/juanftp/mneme/internal/embed"
	"github.com/juanftp/mneme/internal/service"
	"github.com/juanftp/mneme/internal/store"
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
	srv := NewServer(svc, nil, logger, "all", "test")
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
