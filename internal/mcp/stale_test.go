// Package mcp — SPEC-186's spec_list tests: every listed spec carries
// `stale` and `days_since_last_move`, and the response carries
// `stale_count` counted over ALL matches, never only the returned page.
package mcp

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/db"
)

// ageSpecMCP makes spec id look days whole days old by writing that instant
// into specs.updated_at AND every spec_history.at row of the spec (the age
// is the later of the two). A spec created by the tool has no history
// today, so the second write touches no row; it is applied anyway so the
// test stays correct the day spec_new starts recording history.
func ageSpecMCP(t *testing.T, database *db.DB, id string, days int) {
	t.Helper()
	ctx := context.Background()
	at := time.Now().Add(-time.Duration(days) * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
	res, err := database.ExecContext(ctx, `UPDATE specs SET updated_at = ? WHERE id = ?`, at, id)
	if err != nil {
		t.Fatalf("age spec %s: %v", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		t.Fatalf("age spec %s: no specs row matched", id)
	}
	if _, err := database.ExecContext(ctx, `UPDATE spec_history SET at = ? WHERE spec_id = ?`, at, id); err != nil {
		t.Fatalf("age history of %s: %v", id, err)
	}
}

// newSpecsViaTool creates n standard specs through the spec_new tool and
// returns their IDs in creation order.
func newSpecsViaTool(t *testing.T, srv *Server, n int) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		resp := process(t, srv, "tools/call", i, ToolCallParams{
			Name:      "spec_new",
			Arguments: mustMarshal(t, map[string]any{"title": fmt.Sprintf("stale %d", i), "lane": "standard"}),
		})
		if resp.Error != nil {
			t.Fatalf("spec_new %d: %v", i, resp.Error.Message)
		}
		var spec map[string]any
		unmarshalToolText(t, resp, &spec)
		id, _ := spec["id"].(string)
		if id == "" {
			t.Fatalf("spec_new %d returned no id: %v", i, spec)
		}
		ids = append(ids, id)
	}
	return ids
}

// callSpecList runs spec_list with args and decodes the raw response.
func callSpecList(t *testing.T, srv *Server, args map[string]any) map[string]any {
	t.Helper()
	resp := process(t, srv, "tools/call", 999, ToolCallParams{
		Name:      "spec_list",
		Arguments: mustMarshal(t, args),
	})
	if resp.Error != nil {
		t.Fatalf("spec_list: %v", resp.Error.Message)
	}
	var out map[string]any
	unmarshalToolText(t, resp, &out)
	return out
}

// TestHandleSpecList_Stale pins SPEC-186 C4.
func TestHandleSpecList_Stale(t *testing.T) {
	t.Run("StaleCountOverAllMatches", func(t *testing.T) {
		srv, database := newTestServerWithSDDAndDB(t)
		ids := newSpecsViaTool(t, srv, 22)
		for _, id := range ids[len(ids)-3:] {
			ageSpecMCP(t, database, id, 20)
		}

		out := callSpecList(t, srv, map[string]any{"limit": 5})
		specs, ok := out["specs"].([]any)
		if !ok || len(specs) != 5 {
			t.Fatalf("specs = %v, want 5 objects", out["specs"])
		}
		for i, raw := range specs {
			obj, ok := raw.(map[string]any)
			if !ok {
				t.Fatalf("specs[%d] is %T, want an object", i, raw)
			}
			for _, key := range []string{"id", "status", "stale", "days_since_last_move"} {
				if _, present := obj[key]; !present {
					t.Errorf("specs[%d] lacks %q: %v", i, key, obj)
				}
			}
		}
		if got, _ := out["stale_count"].(float64); got != 3 {
			t.Errorf("stale_count = %v, want 3 (counted over all 22 matches, not the page of 5)", out["stale_count"])
		}
	})

	t.Run("StaleCountPresentAtZero", func(t *testing.T) {
		srv, _ := newTestServerWithSDDAndDB(t)
		newSpecsViaTool(t, srv, 2)

		out := callSpecList(t, srv, map[string]any{})
		value, present := out["stale_count"]
		if !present {
			t.Fatalf("stale_count absent with nothing stale; want it present with 0: %v", out)
		}
		if value != float64(0) {
			t.Errorf("stale_count = %v, want 0", value)
		}
	})
}
