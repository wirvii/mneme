package mcp

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// newAgingTestServer is newTestServer plus the project store, so a test can
// backdate a memory's review clock with MarkReviewed instead of sleeping.
func newAgingTestServer(t *testing.T) (*Server, *store.MemoryStore) {
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
	svc := service.NewMemoryService(ps, store.NewMemoryStore(globalDB), config.Default(), "test-project", embed.NopEmbedder{})
	return NewServer(svc, nil, nil, nil, slog.Default(), "all", "test"), ps
}

// saveStale saves a discovery through mem_save and backdates its review by 61
// days, returning its id.
func saveStale(t *testing.T, srv *Server, ps *store.MemoryStore) string {
	t.Helper()
	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "mem_save",
		Arguments: mustMarshal(t, map[string]any{
			"title":   "Quartz misfire policy",
			"content": "Quartz scheduler misfire handling notes.",
		}),
	})
	var saved struct {
		ID string `json:"id"`
	}
	unmarshalToolText(t, resp, &saved)
	if err := ps.MarkReviewed(context.Background(), saved.ID, time.Now().UTC().Add(-61*24*time.Hour)); err != nil {
		t.Fatalf("MarkReviewed: %v", err)
	}
	return saved.ID
}

func searchStale(t *testing.T, srv *Server) (due bool, note string) {
	t.Helper()
	resp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name:      "mem_search",
		Arguments: mustMarshal(t, map[string]any{"query": "quartz scheduler misfire"}),
	})
	var out struct {
		Results []struct {
			ReviewDue  bool   `json:"review_due"`
			ReviewNote string `json:"review_note"`
		} `json:"results"`
	}
	unmarshalToolText(t, resp, &out)
	if len(out.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(out.Results))
	}
	return out.Results[0].ReviewDue, out.Results[0].ReviewNote
}

// TestMemSearch_ReviewDue is SPEC-167 AC9 (MCP search half).
func TestMemSearch_ReviewDue(t *testing.T) {
	srv, ps := newAgingTestServer(t)
	saveStale(t, srv, ps)

	due, note := searchStale(t, srv)
	if !due || note == "" {
		t.Errorf("review_due = %v, note = %q; want flagged", due, note)
	}
}

// TestMemUpdate_Confirm is SPEC-167 AC9 (MCP confirm half).
func TestMemUpdate_Confirm(t *testing.T) {
	srv, ps := newAgingTestServer(t)
	id := saveStale(t, srv, ps)

	resp := process(t, srv, "tools/call", 3, ToolCallParams{
		Name:      "mem_update",
		Arguments: mustMarshal(t, map[string]any{"id": id, "confirm": true}),
	})
	var upd struct {
		ReviewedAt string `json:"reviewed_at"`
	}
	unmarshalToolText(t, resp, &upd)
	if upd.ReviewedAt == "" {
		t.Error("expected reviewed_at in the update response")
	}
	if due, _ := searchStale(t, srv); due {
		t.Error("confirmed memory must no longer be review_due")
	}
}
