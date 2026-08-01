package mcp

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/wirvii/mneme/internal/codegraph"
	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/querylog"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// newQuerylogTestHandlers builds a handlers whose service uses dataDir and the
// given slug, with querylog telemetry toggled by enabled. The codegraph handler
// itself may error (no indexed DB) — logCodegraphUse runs before dispatch, so
// the "use" event is recorded regardless.
func newQuerylogTestHandlers(t *testing.T, dataDir, slug string, enabled bool) *handlers {
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

	cfg := config.Default()
	cfg.Storage.DataDir = dataDir
	cfg.Codegraph.QuerylogEnabled = enabled

	svc := service.NewMemoryService(
		store.NewMemoryStore(projectDB),
		store.NewMemoryStore(globalDB),
		cfg, slug, embed.NopEmbedder{},
	)
	return newHandlers(svc, nil, nil, nil, slog.Default())
}

// TestLogCodegraphUse_AppendsUseEvent verifies that a codegraph_* MCP call
// appends exactly one "use" event when querylog is enabled (AC2).
func TestLogCodegraphUse_AppendsUseEvent(t *testing.T) {
	dataDir := t.TempDir()
	slug := "wirvii/mneme"
	h := newQuerylogTestHandlers(t, dataDir, slug, true)

	_, _ = h.handleToolCall(context.Background(), ToolCallParams{
		Name:      "codegraph_search",
		Arguments: json.RawMessage(`{"query":"anything"}`),
	})

	path := codegraph.QuerylogPath(filepath.Join(dataDir, "projects"), slug)
	events, err := querylog.Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 use event, got %d", len(events))
	}
	ev := events[0]
	if ev.Kind != querylog.KindUse || ev.Tool != "codegraph_search" || ev.Source != "mcp" {
		t.Errorf("unexpected event: %+v", ev)
	}
	if ev.Project != slug {
		t.Errorf("event project = %q, want %q", ev.Project, slug)
	}
	if ev.Session != "" {
		t.Errorf("MCP use event must carry no session id, got %q", ev.Session)
	}
}

// TestLogCodegraphUse_DisabledWritesNothing verifies the off-switch (AC2).
func TestLogCodegraphUse_DisabledWritesNothing(t *testing.T) {
	dataDir := t.TempDir()
	slug := "wirvii/mneme"
	h := newQuerylogTestHandlers(t, dataDir, slug, false)

	_, _ = h.handleToolCall(context.Background(), ToolCallParams{
		Name:      "codegraph_search",
		Arguments: json.RawMessage(`{"query":"anything"}`),
	})

	path := codegraph.QuerylogPath(filepath.Join(dataDir, "projects"), slug)
	events, err := querylog.Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no events when querylog disabled, got %d", len(events))
	}
}

// TestLogCodegraphUse_NonCodegraphToolIgnored verifies non-codegraph tools do
// not produce use events.
func TestLogCodegraphUse_NonCodegraphToolIgnored(t *testing.T) {
	dataDir := t.TempDir()
	slug := "wirvii/mneme"
	h := newQuerylogTestHandlers(t, dataDir, slug, true)

	_, _ = h.handleToolCall(context.Background(), ToolCallParams{
		Name:      "mem_stats",
		Arguments: json.RawMessage(`{}`),
	})

	path := codegraph.QuerylogPath(filepath.Join(dataDir, "projects"), slug)
	events, err := querylog.Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("mem_stats must not log a use event, got %d", len(events))
	}
}
