package store

import (
	"context"
	"testing"

	"github.com/wirvii/mneme/internal/model"
)

// TestListBacklogItemsAndSpecs_UnreadableRowsNamePreciselyAcrossBothTables is
// SPEC-133 AC3: three rows corrupted in three different ways, across BOTH
// tables (a backlog_items row bad in created_at, a specs row bad in
// updated_at, and a specs row bad in assigned_agents), must each be named
// with an EXACT Kind/ID/Column and a non-empty Reason. Comparing by equality
// on all three fields — not merely "the row got reported somehow" — is what
// catches an implementation that fills Column with a fixed placeholder or
// Kind with the wrong table's constant; SPEC-133's own paso 0 found that
// confusing the two tables is a mistake real prose actually made (spec.md
// §2.1), so this is exactly the boundary worth pinning.
func TestListBacklogItemsAndSpecs_UnreadableRowsNamePreciselyAcrossBothTables(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	project := "ac3-project"

	insertRawUnreadableBacklogItem(t, s, "BL-901", project, string(model.BacklogStatusRaw), "created_at", "not-a-timestamp")
	insertRawUnreadableSpec(t, s, "SPEC-901", project, string(model.SpecStatusDraft), "updated_at", "not-a-timestamp")
	insertRawUnreadableSpec(t, s, "SPEC-902", project, string(model.SpecStatusDraft), "assigned_agents", "{")

	_, _, backlogUnreadable, err := s.ListBacklogItems(ctx, project, "", 0)
	if err != nil {
		t.Fatalf("ListBacklogItems: %v", err)
	}
	_, _, specUnreadable, err := s.ListSpecs(ctx, project, "", 0)
	if err != nil {
		t.Fatalf("ListSpecs: %v", err)
	}

	if len(backlogUnreadable) != 1 {
		t.Fatalf("backlog unreadable = %+v, want exactly 1 row", backlogUnreadable)
	}
	gotBL := backlogUnreadable[0]
	if gotBL.Kind != model.UnreadableKindBacklog || gotBL.ID != "BL-901" || gotBL.Column != "created_at" {
		t.Errorf("BL-901 = %+v, want Kind=%s ID=BL-901 Column=created_at", gotBL, model.UnreadableKindBacklog)
	}
	if gotBL.Reason == "" {
		t.Error("BL-901's Reason must not be empty")
	}

	if len(specUnreadable) != 2 {
		t.Fatalf("spec unreadable = %+v, want exactly 2 rows", specUnreadable)
	}
	byID := make(map[string]model.UnreadableRow, len(specUnreadable))
	for _, row := range specUnreadable {
		byID[row.ID] = row
	}

	spec901, ok := byID["SPEC-901"]
	if !ok {
		t.Fatalf("SPEC-901 missing from spec unreadable: %+v", specUnreadable)
	}
	if spec901.Kind != model.UnreadableKindSpec || spec901.Column != "updated_at" {
		t.Errorf("SPEC-901 = %+v, want Kind=%s Column=updated_at", spec901, model.UnreadableKindSpec)
	}
	if spec901.Reason == "" {
		t.Error("SPEC-901's Reason must not be empty")
	}

	spec902, ok := byID["SPEC-902"]
	if !ok {
		t.Fatalf("SPEC-902 missing from spec unreadable: %+v", specUnreadable)
	}
	if spec902.Kind != model.UnreadableKindSpec || spec902.Column != "assigned_agents" {
		t.Errorf("SPEC-902 = %+v, want Kind=%s Column=assigned_agents", spec902, model.UnreadableKindSpec)
	}
	if spec902.Reason == "" {
		t.Error("SPEC-902's Reason must not be empty")
	}
}

// TestCollectBacklogItems_ScanFailureStaysHardError is SPEC-133 AC4/D3: a
// projection with FEWER columns than collectBacklogItems' Scan call expects
// is a programming error — the scanner/query mismatch would affect EVERY
// row, not one corrupt datum — and must stay a hard error with an empty
// unreadable relation, never silently tolerated. This is a real rows.Scan
// failure, not a text-matching guard: an implementation that tolerated
// everything indiscriminately would return a nil error here.
func TestCollectBacklogItems_ScanFailureStaysHardError(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	if err := s.CreateBacklogItem(ctx, &model.BacklogItem{
		ID: "BL-001", Title: "healthy", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: "ac4-project", Lane: model.LaneStandard,
	}); err != nil {
		t.Fatalf("CreateBacklogItem: %v", err)
	}

	rows, err := s.db.QueryContext(ctx, `SELECT id FROM backlog_items`)
	if err != nil {
		t.Fatalf("query short projection: %v", err)
	}
	defer rows.Close()

	items, unreadable, err := collectBacklogItems(rows)
	if err == nil {
		t.Fatal("collectBacklogItems must return an error when the projection has fewer columns than the scanner expects")
	}
	if items != nil {
		t.Errorf("items = %v, want nil on a hard error", items)
	}
	if unreadable != nil {
		t.Errorf("unreadable = %v, want nil on a hard error — a Scan mismatch is not attributable to one row", unreadable)
	}
}

// TestCollectSpecs_ScanFailureStaysHardError mirrors the guard above for
// collectSpecs (AC4/D3).
func TestCollectSpecs_ScanFailureStaysHardError(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	if err := s.CreateSpec(ctx, &model.Spec{
		ID: "SPEC-001", Title: "healthy", Status: model.SpecStatusDraft,
		Project: "ac4-project", Lane: model.LaneStandard,
	}); err != nil {
		t.Fatalf("CreateSpec: %v", err)
	}

	rows, err := s.db.QueryContext(ctx, `SELECT id FROM specs`)
	if err != nil {
		t.Fatalf("query short projection: %v", err)
	}
	defer rows.Close()

	specs, unreadable, err := collectSpecs(rows)
	if err == nil {
		t.Fatal("collectSpecs must return an error when the projection has fewer columns than the scanner expects")
	}
	if specs != nil {
		t.Errorf("specs = %v, want nil on a hard error", specs)
	}
	if unreadable != nil {
		t.Errorf("unreadable = %v, want nil on a hard error — a Scan mismatch is not attributable to one row", unreadable)
	}
}

// TestGetBacklogItemAndGetSpec_StillFailOnACorruptedRow is SPEC-133 AC5/D5:
// a reader that asked for ONE specific row keeps failing noisily on a
// corrupted one — GetBacklogItem/GetSpec are NOT widened by this spec's
// tolerance for the plural listings, because the importer's own "roto"
// detection depends on them failing with something other than "not found"
// (TestSDDImport_MalformedExistingRowIsSkippedAsRoto and its spec sibling,
// verified elsewhere to remain green and unmodified).
func TestGetBacklogItemAndGetSpec_StillFailOnACorruptedRow(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	project := "ac5-project"

	insertRawUnreadableBacklogItem(t, s, "BL-901", project, string(model.BacklogStatusRaw), "created_at", "not-a-timestamp")
	insertRawUnreadableSpec(t, s, "SPEC-901", project, string(model.SpecStatusDraft), "updated_at", "not-a-timestamp")

	if _, err := s.GetBacklogItem(ctx, "BL-901"); err == nil {
		t.Error("GetBacklogItem on a corrupted row must return a non-nil error (D5)")
	}
	if _, err := s.GetSpec(ctx, "SPEC-901"); err == nil {
		t.Error("GetSpec on a corrupted row must return a non-nil error (D5)")
	}
}
