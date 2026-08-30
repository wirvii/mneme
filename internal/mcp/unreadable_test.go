// Package mcp — SPEC-133 step 4's own tests: backlog_list, spec_list, and
// lane_stats gain an additive `unreadable`/`unreadable_total` pair. Both
// halves are required (AC2): the AFFIRMATIVE half (present, correct, and
// truncated when a row is unreadable) and the ABSENT half (the key does not
// appear at all on a healthy project) — a field that is always present,
// even empty, would satisfy the affirmative half alone and prove nothing.
package mcp

import (
	"context"
	"log/slog"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// newTestServerWithSDDAndDB mirrors newTestServerWithSDD but also returns
// the underlying project *db.DB, so a test can insert a row directly via
// SQL — the only way to produce one collectBacklogItems/collectSpecs
// cannot fully read (no mneme write path ever produces one).
func newTestServerWithSDDAndDB(t *testing.T) (*Server, *db.DB) {
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
	return NewServer(svc, sddSvc, nil, nil, logger, "all", "test"), projectDB
}

// insertRawUnreadableBacklogItemMCP inserts a backlog_items row directly
// via SQL with an unparseable created_at.
func insertRawUnreadableBacklogItemMCP(t *testing.T, database *db.DB, id string) {
	t.Helper()
	if _, err := database.ExecContext(context.Background(),
		`INSERT INTO backlog_items (id, title, description, status, priority, project, position, lane, scope, uuid, previous_ids, created_at, updated_at)
		 VALUES (?, ?, '', ?, ?, ?, 0, ?, '', '', '', ?, ?)`,
		id, "pre-existing malformed row", string(model.BacklogStatusRaw), string(model.PriorityMedium),
		"test-project", string(model.LaneStandard), "not-a-timestamp", "not-a-timestamp",
	); err != nil {
		t.Fatalf("insert malformed fixture row %s: %v", id, err)
	}
}

// insertRawUnreadableSpecMCP inserts a specs row directly via SQL with an
// unparseable updated_at.
func insertRawUnreadableSpecMCP(t *testing.T, database *db.DB, id string) {
	t.Helper()
	if _, err := database.ExecContext(context.Background(),
		`INSERT INTO specs (id, title, status, project, backlog_id, assigned_agents, files_changed, created_at, updated_at, lane, scope, base_sha, uuid, previous_ids)
		 VALUES (?, ?, ?, ?, NULL, '[]', '[]', ?, ?, ?, '', '', '', '')`,
		id, "pre-existing malformed spec", string(model.SpecStatusDraft), "test-project",
		"2026-01-01T00:00:00Z", "not-a-timestamp", string(model.LaneStandard),
	); err != nil {
		t.Fatalf("insert malformed fixture spec %s: %v", id, err)
	}
}

// TestHandleBacklogList_UnreadableKeyAbsentOnHealthyProject is AC2's
// discriminating half for backlog_list: with nothing unreadable, the
// response carries no "unreadable" or "unreadable_total" key at all.
func TestHandleBacklogList_UnreadableKeyAbsentOnHealthyProject(t *testing.T) {
	srv, _ := newTestServerWithSDDAndDB(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "backlog_list",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if resp.Error != nil {
		t.Fatalf("backlog_list: %v", resp.Error.Message)
	}

	var out map[string]any
	unmarshalToolText(t, resp, &out)
	if _, present := out["unreadable"]; present {
		t.Errorf("backlog_list on a healthy project must not carry 'unreadable': %v", out)
	}
	if _, present := out["unreadable_total"]; present {
		t.Errorf("backlog_list on a healthy project must not carry 'unreadable_total': %v", out)
	}
}

// TestHandleBacklogList_UnreadableKeyNamesTheRow is AC2's affirmative half
// for backlog_list.
func TestHandleBacklogList_UnreadableKeyNamesTheRow(t *testing.T) {
	srv, database := newTestServerWithSDDAndDB(t)
	insertRawUnreadableBacklogItemMCP(t, database, "BL-901")

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "backlog_list",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if resp.Error != nil {
		t.Fatalf("backlog_list: %v", resp.Error.Message)
	}

	var out struct {
		Unreadable      []model.UnreadableRow `json:"unreadable"`
		UnreadableTotal int                   `json:"unreadable_total"`
	}
	unmarshalToolText(t, resp, &out)
	if out.UnreadableTotal != 1 {
		t.Errorf("UnreadableTotal = %d, want 1", out.UnreadableTotal)
	}
	if len(out.Unreadable) != 1 || out.Unreadable[0].ID != "BL-901" {
		t.Errorf("Unreadable = %+v, want exactly one row naming BL-901", out.Unreadable)
	}
}

// TestHandleSpecList_UnreadableKeyAbsentOnHealthyProject is AC2's
// discriminating half for spec_list.
func TestHandleSpecList_UnreadableKeyAbsentOnHealthyProject(t *testing.T) {
	srv, _ := newTestServerWithSDDAndDB(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "spec_list",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if resp.Error != nil {
		t.Fatalf("spec_list: %v", resp.Error.Message)
	}

	var out map[string]any
	unmarshalToolText(t, resp, &out)
	if _, present := out["unreadable"]; present {
		t.Errorf("spec_list on a healthy project must not carry 'unreadable': %v", out)
	}
	if _, present := out["unreadable_total"]; present {
		t.Errorf("spec_list on a healthy project must not carry 'unreadable_total': %v", out)
	}
}

// TestHandleSpecList_UnreadableKeyNamesTheRow is AC2's affirmative half for
// spec_list.
func TestHandleSpecList_UnreadableKeyNamesTheRow(t *testing.T) {
	srv, database := newTestServerWithSDDAndDB(t)
	insertRawUnreadableSpecMCP(t, database, "SPEC-901")

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "spec_list",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if resp.Error != nil {
		t.Fatalf("spec_list: %v", resp.Error.Message)
	}

	var out struct {
		Unreadable      []model.UnreadableRow `json:"unreadable"`
		UnreadableTotal int                   `json:"unreadable_total"`
	}
	unmarshalToolText(t, resp, &out)
	if out.UnreadableTotal != 1 {
		t.Errorf("UnreadableTotal = %d, want 1", out.UnreadableTotal)
	}
	if len(out.Unreadable) != 1 || out.Unreadable[0].ID != "SPEC-901" {
		t.Errorf("Unreadable = %+v, want exactly one row naming SPEC-901", out.Unreadable)
	}
}

// TestHandleLaneStats_UnreadableKeyAbsentOnHealthyProject is AC2's
// discriminating half for lane_stats.
func TestHandleLaneStats_UnreadableKeyAbsentOnHealthyProject(t *testing.T) {
	srv, _ := newTestServerWithSDDAndDB(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "lane_stats",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if resp.Error != nil {
		t.Fatalf("lane_stats: %v", resp.Error.Message)
	}

	var out map[string]any
	unmarshalToolText(t, resp, &out)
	if _, present := out["unreadable"]; present {
		t.Errorf("lane_stats on a healthy project must not carry 'unreadable': %v", out)
	}
	if _, present := out["unreadable_total"]; present {
		t.Errorf("lane_stats on a healthy project must not carry 'unreadable_total': %v", out)
	}
}

// TestHandleLaneStats_UnreadableKeyNamesTheRow is AC2's affirmative half
// for lane_stats: a malformed TRIVIAL spec (lane_stats only aggregates
// trivial-lane specs) is named.
func TestHandleLaneStats_UnreadableKeyNamesTheRow(t *testing.T) {
	srv, database := newTestServerWithSDDAndDB(t)
	if _, err := database.ExecContext(context.Background(),
		`INSERT INTO specs (id, title, status, project, backlog_id, assigned_agents, files_changed, created_at, updated_at, lane, scope, base_sha, uuid, previous_ids)
		 VALUES (?, ?, ?, ?, NULL, '[]', '[]', ?, ?, ?, '', '', '', '')`,
		"SPEC-902", "pre-existing malformed trivial spec", string(model.SpecStatusDraft), "test-project",
		"2026-01-01T00:00:00Z", "not-a-timestamp", string(model.LaneTrivial),
	); err != nil {
		t.Fatalf("insert malformed trivial spec: %v", err)
	}

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "lane_stats",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if resp.Error != nil {
		t.Fatalf("lane_stats: %v", resp.Error.Message)
	}

	var out struct {
		Unreadable      []model.UnreadableRow `json:"unreadable"`
		UnreadableTotal int                   `json:"unreadable_total"`
	}
	unmarshalToolText(t, resp, &out)
	if out.UnreadableTotal != 1 {
		t.Errorf("UnreadableTotal = %d, want 1", out.UnreadableTotal)
	}
	if len(out.Unreadable) != 1 || out.Unreadable[0].ID != "SPEC-902" {
		t.Errorf("Unreadable = %+v, want exactly one row naming SPEC-902", out.Unreadable)
	}
}

// TestTruncateUnreadable_CapsAtMaxAndReportsRealTotal is SPEC-133 D14's own
// unit-level pin: the MCP frontend truncates to model.MaxUnreadableListed
// (20) and reports the real count separately, never in the store.
func TestTruncateUnreadable_CapsAtMaxAndReportsRealTotal(t *testing.T) {
	rows := make([]model.UnreadableRow, 25)
	for i := range rows {
		rows[i] = model.UnreadableRow{Kind: model.UnreadableKindBacklog, ID: "BL-x", Column: "created_at", Reason: "x"}
	}

	limited, total := truncateUnreadable(rows)
	if total != 25 {
		t.Errorf("total = %d, want 25 (the real count, uncapped)", total)
	}
	if len(limited) != model.MaxUnreadableListed {
		t.Errorf("len(limited) = %d, want %d (capped at MaxUnreadableListed)", len(limited), model.MaxUnreadableListed)
	}

	empty, emptyTotal := truncateUnreadable(nil)
	if empty != nil || emptyTotal != 0 {
		t.Errorf("truncateUnreadable(nil) = (%v, %d), want (nil, 0)", empty, emptyTotal)
	}
}
