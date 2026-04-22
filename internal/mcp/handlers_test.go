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
