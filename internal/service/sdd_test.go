package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/store"
)

// newTestSDDService creates a real SDDService backed by an in-memory SQLite
// database with all migrations applied. No mocks — tests run against real SQL.
func newTestSDDService(t *testing.T, project string) *SDDService {
	t.Helper()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })

	sddStore := store.NewSDDStore(database)
	cfg := config.Default()
	// Pass nil memorySvc — completion memory saving is not exercised in unit tests.
	return NewSDDService(sddStore, cfg, project, nil)
}

// --- BACKLOG TESTS ---

func TestBacklogAdd_Success(t *testing.T) {
	svc := newTestSDDService(t, "testproject")
	ctx := context.Background()

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{
		Title:       "Add notifications",
		Description: "Push notification support",
		Priority:    model.PriorityHigh,
		Lane:        model.LaneStandard,
	})
	if err != nil {
		t.Fatalf("BacklogAdd: %v", err)
	}

	if item.ID != "BL-001" {
		t.Errorf("ID: got %q, want BL-001", item.ID)
	}
	if item.Status != model.BacklogStatusRaw {
		t.Errorf("Status: got %q, want raw", item.Status)
	}
	if item.Priority != model.PriorityHigh {
		t.Errorf("Priority: got %q, want high", item.Priority)
	}
	if item.Title != "Add notifications" {
		t.Errorf("Title: got %q", item.Title)
	}
	if item.CreatedAt.IsZero() {
		t.Error("CreatedAt must not be zero")
	}
}

func TestBacklogAdd_EmptyTitle(t *testing.T) {
	svc := newTestSDDService(t, "testproject")
	ctx := context.Background()

	_, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: ""})
	if !errors.Is(err, model.ErrTitleRequired) {
		t.Errorf("expected ErrTitleRequired, got %v", err)
	}
}

func TestBacklogAdd_InvalidPriority(t *testing.T) {
	svc := newTestSDDService(t, "testproject")
	ctx := context.Background()

	_, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{
		Title:    "Test",
		Priority: "urgent",
		Lane:     model.LaneStandard,
	})
	if !errors.Is(err, model.ErrInvalidPriority) {
		t.Errorf("expected ErrInvalidPriority, got %v", err)
	}
}

func TestBacklogAdd_DefaultProject(t *testing.T) {
	svc := newTestSDDService(t, "myproject")
	ctx := context.Background()

	// No Project in request — should use service project.
	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "Test", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("BacklogAdd: %v", err)
	}
	if item.Project != "myproject" {
		t.Errorf("Project: got %q, want myproject", item.Project)
	}
}

func TestBacklogAdd_DefaultPriority(t *testing.T) {
	svc := newTestSDDService(t, "p")
	ctx := context.Background()

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "Test", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("BacklogAdd: %v", err)
	}
	if item.Priority != model.PriorityMedium {
		t.Errorf("Priority: got %q, want medium", item.Priority)
	}
}

// TestBacklogGet_Success is AC14/AC18: BacklogGet returns the item plus ALL
// of its refinements, in full — no excerpt, no truncation, no limit (D6/D7).
// The refinement body lives in its own row (D2/D15), never in Description.
func TestBacklogGet_Success(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{
		Title: "Feature X", Lane: model.LaneStandard,
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	longRefinement := strings.Repeat("grill ledger content ", 500) // well over 200 runes
	if _, err := svc.BacklogRefine(ctx, model.BacklogRefineRequest{
		ID: item.ID, Refinement: longRefinement,
	}); err != nil {
		t.Fatalf("refine: %v", err)
	}

	got, err := svc.BacklogGet(ctx, item.ID)
	if err != nil {
		t.Fatalf("BacklogGet: %v", err)
	}
	if got.Item.Description != "" {
		t.Errorf("Description must stay write-once and untouched by refine, got %q", got.Item.Description)
	}
	if len(got.Refinements) != 1 {
		t.Fatalf("expected 1 refinement, got %d", len(got.Refinements))
	}
	if got.Refinements[0].Body != longRefinement {
		t.Errorf("refinement body was truncated or altered: got %d runes, want %d",
			len([]rune(got.Refinements[0].Body)), len([]rune(longRefinement)))
	}
	if len(got.Refinements) != got.Item.RefinementCount {
		t.Errorf("len(Refinements)=%d, Item.RefinementCount=%d — they must agree (D7)",
			len(got.Refinements), got.Item.RefinementCount)
	}
}

// TestBacklogGet_NotFound is AC19: an unknown ID surfaces
// model.ErrBacklogNotFound via errors.Is, with no new sentinel introduced.
func TestBacklogGet_NotFound(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	_, err := svc.BacklogGet(ctx, "BL-999")
	if !errors.Is(err, model.ErrBacklogNotFound) {
		t.Errorf("expected ErrBacklogNotFound, got %v", err)
	}
}

func TestBacklogList_FilterByStatus(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	// Create 3 items: 2 raw, 1 refined.
	for _, title := range []string{"A", "B"} {
		if _, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: title, Lane: model.LaneStandard}); err != nil {
			t.Fatalf("add %s: %v", title, err)
		}
	}
	itemC, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "C", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("add C: %v", err)
	}
	if _, err := svc.BacklogRefine(ctx, model.BacklogRefineRequest{ID: itemC.ID, Refinement: "details"}); err != nil {
		t.Fatalf("refine C: %v", err)
	}

	rawResp, err := svc.BacklogList(ctx, model.BacklogListRequest{Status: model.BacklogStatusRaw})
	if err != nil {
		t.Fatalf("BacklogList(raw): %v", err)
	}
	if len(rawResp.Items) != 2 {
		t.Errorf("expected 2 raw items, got %d", len(rawResp.Items))
	}
	if rawResp.Total != 2 {
		t.Errorf("expected Total=2, got %d", rawResp.Total)
	}

	refinedResp, err := svc.BacklogList(ctx, model.BacklogListRequest{Status: model.BacklogStatusRefined})
	if err != nil {
		t.Fatalf("BacklogList(refined): %v", err)
	}
	if len(refinedResp.Items) != 1 {
		t.Errorf("expected 1 refined item, got %d", len(refinedResp.Items))
	}
}

func TestBacklogRefine_Success(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "Feature X", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	refined, err := svc.BacklogRefine(ctx, model.BacklogRefineRequest{
		ID:         item.ID,
		Refinement: "This feature does Y and Z",
	})
	if err != nil {
		t.Fatalf("BacklogRefine: %v", err)
	}

	if refined.Status != model.BacklogStatusRefined {
		t.Errorf("Status: got %q, want refined", refined.Status)
	}
	// Description is write-once (D15): BacklogAdd created the item with no
	// description, and BacklogRefine must never touch it. The refinement
	// text lives in its own row instead.
	if refined.Description != "" {
		t.Errorf("Description: got %q, want unchanged empty string (D15)", refined.Description)
	}
	if refined.RefinementCount != 1 {
		t.Errorf("RefinementCount: got %d, want 1", refined.RefinementCount)
	}
}

// TestBacklogRefine_SecondRefinementAppendsAndStaysRefined is SPEC-110's R6
// replacement for the deleted TestBacklogRefine_NotRaw: refinement is
// ITERATIVE (D3). The second refinement must succeed, the item stays
// refined, RefinementCount reaches 2, and Description never changes.
func TestBacklogRefine_SecondRefinementAppendsAndStaysRefined(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "X", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	first, err := svc.BacklogRefine(ctx, model.BacklogRefineRequest{ID: item.ID, Refinement: "r1"})
	if err != nil {
		t.Fatalf("first refine: %v", err)
	}
	if first.Status != model.BacklogStatusRefined {
		t.Fatalf("after first refine: status = %q, want refined", first.Status)
	}
	if first.RefinementCount != 1 {
		t.Fatalf("after first refine: RefinementCount = %d, want 1", first.RefinementCount)
	}

	second, err := svc.BacklogRefine(ctx, model.BacklogRefineRequest{ID: item.ID, Refinement: "r2"})
	if err != nil {
		t.Fatalf("second refine: %v", err)
	}
	if second.Status != model.BacklogStatusRefined {
		t.Errorf("after second refine: status = %q, want refined (stays)", second.Status)
	}
	if second.RefinementCount != 2 {
		t.Errorf("after second refine: RefinementCount = %d, want 2", second.RefinementCount)
	}
	if second.Description != "" {
		t.Errorf("Description changed: got %q, want unchanged empty string (D15)", second.Description)
	}
}

// TestBacklogRefine_RejectsPromotedAndArchived is SPEC-110's R6 replacement
// for the deleted TestBacklogRefine_NotRaw: promoted and archived items
// cannot be refined (D3), with a typed error, zero rows inserted, and status
// and description left intact.
func TestBacklogRefine_RejectsPromotedAndArchived(t *testing.T) {
	tests := []struct {
		name   string
		status model.BacklogStatus
	}{
		{"promoted", model.BacklogStatusPromoted},
		{"archived", model.BacklogStatusArchived},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestSDDService(t, "project")
			ctx := context.Background()

			item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{
				Title: "X", Description: "original", Lane: model.LaneStandard,
			})
			if err != nil {
				t.Fatalf("add: %v", err)
			}
			if tt.status == model.BacklogStatusPromoted {
				if _, err := svc.BacklogRefine(ctx, model.BacklogRefineRequest{ID: item.ID, Refinement: "r"}); err != nil {
					t.Fatalf("refine before promote: %v", err)
				}
				if _, err := svc.BacklogPromote(ctx, item.ID); err != nil {
					t.Fatalf("promote: %v", err)
				}
			} else {
				if _, err := svc.BacklogArchive(ctx, model.BacklogArchiveRequest{
					ID: item.ID, Reason: "no longer needed",
				}); err != nil {
					t.Fatalf("archive: %v", err)
				}
			}

			before, err := svc.BacklogGet(ctx, item.ID)
			if err != nil {
				t.Fatalf("get before refine attempt: %v", err)
			}

			_, err = svc.BacklogRefine(ctx, model.BacklogRefineRequest{ID: item.ID, Refinement: "should not land"})
			if !errors.Is(err, model.ErrBacklogNotRefinable) {
				t.Fatalf("expected ErrBacklogNotRefinable, got %v", err)
			}

			after, err := svc.BacklogGet(ctx, item.ID)
			if err != nil {
				t.Fatalf("get after rejected refine: %v", err)
			}
			if after.Item.Status != before.Item.Status {
				t.Errorf("status changed: got %q, want unchanged %q", after.Item.Status, before.Item.Status)
			}
			if after.Item.Description != before.Item.Description {
				t.Errorf("description changed: got %q, want unchanged %q", after.Item.Description, before.Item.Description)
			}
			if len(after.Refinements) != len(before.Refinements) {
				t.Errorf("refinement count changed: got %d rows, want unchanged %d", len(after.Refinements), len(before.Refinements))
			}
		})
	}
}

// TestBacklogRefine_RejectsEmptyBody is AC16: an empty or whitespace-only
// refinement body is rejected with model.ErrContentRequired, no row inserted.
func TestBacklogRefine_RejectsEmptyBody(t *testing.T) {
	for _, body := range []string{"", "   \n  \t"} {
		body := body
		t.Run(fmt.Sprintf("body=%q", body), func(t *testing.T) {
			svc := newTestSDDService(t, "project")
			ctx := context.Background()

			item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "X", Lane: model.LaneStandard})
			if err != nil {
				t.Fatalf("add: %v", err)
			}

			_, err = svc.BacklogRefine(ctx, model.BacklogRefineRequest{ID: item.ID, Refinement: body})
			if !errors.Is(err, model.ErrContentRequired) {
				t.Fatalf("expected ErrContentRequired, got %v", err)
			}

			got, err := svc.BacklogGet(ctx, item.ID)
			if err != nil {
				t.Fatalf("BacklogGet: %v", err)
			}
			if len(got.Refinements) != 0 {
				t.Errorf("expected 0 refinements after rejected empty body, got %d", len(got.Refinements))
			}
		})
	}
}

// TestBacklogRefine_PersistsByAndAllowsEmpty is AC17: By is persisted
// verbatim when provided, and omitting it (empty string) is a valid,
// non-failing call — an honest "unattributed" (D5).
func TestBacklogRefine_PersistsByAndAllowsEmpty(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "X", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	if _, err := svc.BacklogRefine(ctx, model.BacklogRefineRequest{ID: item.ID, Refinement: "r1", By: "architect"}); err != nil {
		t.Fatalf("refine with by: %v", err)
	}
	if _, err := svc.BacklogRefine(ctx, model.BacklogRefineRequest{ID: item.ID, Refinement: "r2"}); err != nil {
		t.Fatalf("refine without by: %v", err)
	}

	got, err := svc.BacklogGet(ctx, item.ID)
	if err != nil {
		t.Fatalf("BacklogGet: %v", err)
	}
	if len(got.Refinements) != 2 {
		t.Fatalf("expected 2 refinements, got %d", len(got.Refinements))
	}
	if got.Refinements[0].By != "architect" {
		t.Errorf("refinement[0].By = %q, want architect", got.Refinements[0].By)
	}
	if got.Refinements[1].By != "" {
		t.Errorf("refinement[1].By = %q, want empty (unattributed)", got.Refinements[1].By)
	}
}

// TestBacklogGet_ReturnsRefinementsForPromotedItem is AC18: reading is always
// allowed, even for a promoted item — only writing (refining) is vedado (D3).
func TestBacklogGet_ReturnsRefinementsForPromotedItem(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "X", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := svc.BacklogRefine(ctx, model.BacklogRefineRequest{ID: item.ID, Refinement: "r1"}); err != nil {
		t.Fatalf("refine: %v", err)
	}
	if _, err := svc.BacklogPromote(ctx, item.ID); err != nil {
		t.Fatalf("promote: %v", err)
	}

	got, err := svc.BacklogGet(ctx, item.ID)
	if err != nil {
		t.Fatalf("BacklogGet on promoted item: %v", err)
	}
	if got.Item.Status != model.BacklogStatusPromoted {
		t.Errorf("status = %q, want promoted", got.Item.Status)
	}
	if len(got.Refinements) != 1 {
		t.Errorf("expected 1 refinement to remain readable, got %d", len(got.Refinements))
	}
}

// TestBacklogRefine_ReturnsFreshItem is AC19: BacklogRefine's returned item
// is re-read from the DB — its UpdatedAt is later than before the call, and
// its RefinementCount matches an immediately subsequent BacklogGet (D17).
func TestBacklogRefine_ReturnsFreshItem(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "X", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	beforeUpdatedAt := item.UpdatedAt

	refined, err := svc.BacklogRefine(ctx, model.BacklogRefineRequest{ID: item.ID, Refinement: "r1"})
	if err != nil {
		t.Fatalf("refine: %v", err)
	}
	if !refined.UpdatedAt.After(beforeUpdatedAt) {
		t.Errorf("UpdatedAt = %v, want after %v", refined.UpdatedAt, beforeUpdatedAt)
	}

	got, err := svc.BacklogGet(ctx, item.ID)
	if err != nil {
		t.Fatalf("BacklogGet: %v", err)
	}
	if refined.RefinementCount != got.Item.RefinementCount {
		t.Errorf("BacklogRefine RefinementCount=%d, immediately subsequent BacklogGet RefinementCount=%d — must match",
			refined.RefinementCount, got.Item.RefinementCount)
	}
}

func TestBacklogPromote_Success(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "Feature Y", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := svc.BacklogRefine(ctx, model.BacklogRefineRequest{ID: item.ID, Refinement: "details"}); err != nil {
		t.Fatalf("refine: %v", err)
	}

	spec, err := svc.BacklogPromote(ctx, item.ID)
	if err != nil {
		t.Fatalf("BacklogPromote: %v", err)
	}

	if spec.BacklogID != item.ID {
		t.Errorf("spec.BacklogID: got %q, want %q", spec.BacklogID, item.ID)
	}
	if spec.Status != model.SpecStatusDraft {
		t.Errorf("spec.Status: got %q, want draft", spec.Status)
	}
	if spec.Title != "Feature Y" {
		t.Errorf("spec.Title: got %q, want Feature Y", spec.Title)
	}

	// Verify backlog item is marked promoted.
	resp, err := svc.BacklogList(ctx, model.BacklogListRequest{Status: model.BacklogStatusPromoted})
	if err != nil {
		t.Fatalf("list promoted: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].SpecID != spec.ID {
		t.Errorf("expected 1 promoted item with spec_id=%q", spec.ID)
	}
}

func TestBacklogPromote_NotRefined(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "Raw item", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	_, err = svc.BacklogPromote(ctx, item.ID)
	if !errors.Is(err, model.ErrBacklogNotRefined) {
		t.Errorf("expected ErrBacklogNotRefined, got %v", err)
	}
}

// TestBacklogArchive_Success is SPEC-125 AC31: archiving an item with no
// linked spec returns FrozenSpec == nil and an Item reflecting the new
// status and reason, matching what a subsequent list would show.
func TestBacklogArchive_Success(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "Archive me", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	result, err := svc.BacklogArchive(ctx, model.BacklogArchiveRequest{ID: item.ID, Reason: "not needed anymore"})
	if err != nil {
		t.Fatalf("BacklogArchive: %v", err)
	}
	if result.FrozenSpec != nil {
		t.Errorf("expected FrozenSpec == nil for an item with no linked spec, got %+v", result.FrozenSpec)
	}
	if result.Item == nil || result.Item.Status != model.BacklogStatusArchived {
		t.Fatalf("expected Item with status archived, got %+v", result.Item)
	}
	if result.Item.ArchiveReason != "not needed anymore" {
		t.Errorf("ArchiveReason: got %q", result.Item.ArchiveReason)
	}

	resp, err := svc.BacklogList(ctx, model.BacklogListRequest{Status: model.BacklogStatusArchived})
	if err != nil {
		t.Fatalf("list archived: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 archived item, got %d", len(resp.Items))
	}
	if resp.Items[0].ArchiveReason != "not needed anymore" {
		t.Errorf("ArchiveReason: got %q", resp.Items[0].ArchiveReason)
	}
}

// TestBacklogArchive_ReasonRequired is SPEC-125 AC1/AC2: an empty or
// whitespace-only reason is rejected with ErrReasonRequired, and — because
// the ID used does not exist — the error proves no read ran: a store read
// would have surfaced ErrBacklogNotFound instead.
func TestBacklogArchive_ReasonRequired(t *testing.T) {
	tests := []struct {
		name   string
		reason string
	}{
		{"empty", ""},
		{"whitespace only", "  \t\n  "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestSDDService(t, "project")
			ctx := context.Background()

			_, err := svc.BacklogArchive(ctx, model.BacklogArchiveRequest{ID: "BL-does-not-exist", Reason: tt.reason})
			if !errors.Is(err, model.ErrReasonRequired) {
				t.Fatalf("expected ErrReasonRequired, got %v", err)
			}
			if errors.Is(err, model.ErrBacklogNotFound) {
				t.Fatalf("got ErrBacklogNotFound: the item lookup ran before the reason check")
			}
		})
	}
}

// promoteAndAdvance is a small test helper: it adds+refines+promotes a
// backlog item to a standard-lane spec, then advances the spec `steps` times
// (starting from draft), returning the item and the spec at its final state.
func promoteAndAdvance(t *testing.T, svc *SDDService, ctx context.Context, steps int) (*model.BacklogItem, *model.Spec) {
	t.Helper()

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "Linked to a spec", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := svc.BacklogRefine(ctx, model.BacklogRefineRequest{ID: item.ID, Refinement: "details"}); err != nil {
		t.Fatalf("refine: %v", err)
	}
	spec, err := svc.BacklogPromote(ctx, item.ID)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}

	bys := []string{"orch", "arch", "arch", "arch", "backend", "backend", "qa"}
	if steps > len(bys) {
		t.Fatalf("promoteAndAdvance: steps=%d exceeds the standard lane's %d transitions", steps, len(bys))
	}
	for i := range steps {
		spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: bys[i]})
		if err != nil {
			t.Fatalf("SpecAdvance step %d (status=%s): %v", i, spec.Status, err)
		}
	}

	item, err = svc.store.GetBacklogItem(ctx, item.ID)
	if err != nil {
		t.Fatalf("reload item: %v", err)
	}
	return item, spec
}

// newFrozenSpecFixture archives a fresh backlog item and creates a spec
// linked to it via BacklogID, seeded directly at the caller's chosen
// status/lane/scope/baseSHA (SPEC-125's freeze fixture). Seeding the spec
// directly — rather than driving it there through the full lifecycle —
// mirrors the established pattern in lane_audit_test.go
// (TestLaneAudit_NoBaseRefReturnsError): the spec's OWN status/lane
// preconditions are irrelevant here, since the freeze gate in
// loadMutableSpec fires before any of them are consulted (DD5).
func newFrozenSpecFixture(
	t *testing.T, svc *SDDService, ctx context.Context,
	specID string, status model.SpecStatus, lane model.Lane, scope, baseSHA string,
) (*model.BacklogItem, *model.Spec) {
	t.Helper()

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "Frozen fixture", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("add backlog item: %v", err)
	}
	if _, err := svc.BacklogArchive(ctx, model.BacklogArchiveRequest{ID: item.ID, Reason: "fixture archive"}); err != nil {
		t.Fatalf("archive backlog item: %v", err)
	}

	spec := &model.Spec{
		ID:        specID,
		Title:     "Frozen spec",
		Status:    status,
		Project:   svc.project,
		BacklogID: item.ID,
		Lane:      lane,
		Scope:     scope,
		BaseSHA:   baseSHA,
	}
	if err := svc.store.CreateSpec(ctx, spec); err != nil {
		t.Fatalf("create spec: %v", err)
	}
	return item, spec
}

// TestSpecVerbs_FrozenByArchivedBacklogItem is SPEC-125 AC11/AC12: every one
// of the eight spec-mutating verbs refuses with ErrSpecFrozen, naming the
// archived backlog item, when the spec's originating item is archived.
func TestSpecVerbs_FrozenByArchivedBacklogItem(t *testing.T) {
	tests := []struct {
		name string
		call func(svc *SDDService, ctx context.Context, id string) error
	}{
		{"SpecAdvance", func(svc *SDDService, ctx context.Context, id string) error {
			_, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: id, By: "orchestrator"})
			return err
		}},
		{"SpecPushback", func(svc *SDDService, ctx context.Context, id string) error {
			_, err := svc.SpecPushback(ctx, model.SpecPushbackRequest{ID: id, FromAgent: "qa", Questions: []string{"still unclear?"}})
			return err
		}},
		{"SpecReject", func(svc *SDDService, ctx context.Context, id string) error {
			_, err := svc.SpecReject(ctx, model.SpecRejectRequest{ID: id, Reason: "defect found", By: "qa-agent"})
			return err
		}},
		{"SpecResolve", func(svc *SDDService, ctx context.Context, id string) error {
			_, err := svc.SpecResolve(ctx, model.SpecResolveRequest{ID: id, Resolution: "resolved"})
			return err
		}},
		{"SpecQuick", func(svc *SDDService, ctx context.Context, id string) error {
			_, err := svc.SpecQuick(ctx, model.SpecQuickRequest{ID: id, Rationale: "one-liner", By: "orchestrator"})
			return err
		}},
		{"LaneAudit", func(svc *SDDService, ctx context.Context, id string) error {
			_, err := svc.LaneAudit(ctx, model.LaneAuditRequest{ID: id})
			return err
		}},
		{"LaneOverride", func(svc *SDDService, ctx context.Context, id string) error {
			_, err := svc.LaneOverride(ctx, model.LaneOverrideRequest{ID: id, Reason: "override anyway", By: "orchestrator"})
			return err
		}},
		{"LaneReclassify", func(svc *SDDService, ctx context.Context, id string) error {
			_, err := svc.LaneReclassify(ctx, model.LaneReclassifyRequest{ID: id, Lane: model.LaneStandard, By: "orchestrator"})
			return err
		}},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestSDDService(t, "project")
			ctx := context.Background()
			specID := fmt.Sprintf("SPEC-frozen-%02d", i+1)
			item, _ := newFrozenSpecFixture(t, svc, ctx, specID, model.SpecStatusDraft, model.LaneTrivial, "internal/model/*.go", "")

			err := tt.call(svc, ctx, specID)
			if !errors.Is(err, model.ErrSpecFrozen) {
				t.Fatalf("expected ErrSpecFrozen, got %v", err)
			}
			if !strings.Contains(err.Error(), item.ID) {
				t.Errorf("expected the error to name %s, got %q", item.ID, err.Error())
			}
		})
	}
}

// TestSpecPushback_Frozen_CreatesNoPushbackRow is SPEC-125 AC14: the gate
// sits in loadMutableSpec, entered BEFORE SpecPushback's own CreatePushback
// call — so a rejected pushback attempt against a frozen spec must leave
// zero unresolved pushback rows behind, not merely fail the transition.
func TestSpecPushback_Frozen_CreatesNoPushbackRow(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	_, spec := newFrozenSpecFixture(t, svc, ctx, "SPEC-frozen-pb", model.SpecStatusSpeccing, model.LaneStandard, "", "")

	before, err := svc.store.GetUnresolvedPushbacks(ctx, spec.ID)
	if err != nil {
		t.Fatalf("GetUnresolvedPushbacks (before): %v", err)
	}

	_, err = svc.SpecPushback(ctx, model.SpecPushbackRequest{ID: spec.ID, FromAgent: "qa", Questions: []string{"q"}})
	if !errors.Is(err, model.ErrSpecFrozen) {
		t.Fatalf("expected ErrSpecFrozen, got %v", err)
	}

	after, err := svc.store.GetUnresolvedPushbacks(ctx, spec.ID)
	if err != nil {
		t.Fatalf("GetUnresolvedPushbacks (after): %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("expected no new pushback row: before=%d after=%d", len(before), len(after))
	}
}

// TestSpecResolve_Frozen_ResolvesNoPushback is SPEC-125 AC15: an existing
// unresolved pushback must NOT be marked resolved when the spec has since
// been frozen — the gate sits before ResolvePushback is ever called.
func TestSpecResolve_Frozen_ResolvesNoPushback(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "Freeze after pushback", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := svc.BacklogRefine(ctx, model.BacklogRefineRequest{ID: item.ID, Refinement: "details"}); err != nil {
		t.Fatalf("refine: %v", err)
	}
	spec, err := svc.BacklogPromote(ctx, item.ID)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "orchestrator"}) // draft -> speccing
	if err != nil {
		t.Fatalf("advance to speccing: %v", err)
	}
	if _, err := svc.SpecPushback(ctx, model.SpecPushbackRequest{
		ID: spec.ID, FromAgent: "architect", Questions: []string{"what about X?"},
	}); err != nil {
		t.Fatalf("pushback: %v", err)
	}

	if _, err := svc.BacklogArchive(ctx, model.BacklogArchiveRequest{ID: item.ID, Reason: "abandoned while blocked"}); err != nil {
		t.Fatalf("archive: %v", err)
	}

	before, err := svc.store.GetUnresolvedPushbacks(ctx, spec.ID)
	if err != nil {
		t.Fatalf("GetUnresolvedPushbacks (before resolve attempt): %v", err)
	}
	if len(before) != 1 {
		t.Fatalf("expected 1 unresolved pushback before the attempt, got %d", len(before))
	}

	_, err = svc.SpecResolve(ctx, model.SpecResolveRequest{ID: spec.ID, Resolution: "no longer relevant"})
	if !errors.Is(err, model.ErrSpecFrozen) {
		t.Fatalf("expected ErrSpecFrozen, got %v", err)
	}

	after, err := svc.store.GetUnresolvedPushbacks(ctx, spec.ID)
	if err != nil {
		t.Fatalf("GetUnresolvedPushbacks (after resolve attempt): %v", err)
	}
	if len(after) != 1 {
		t.Errorf("expected the pushback to remain unresolved, got %d unresolved", len(after))
	}
}

// TestFrozenSpec_ReadOnlyPathsStillWork is SPEC-125 AC13: a frozen spec
// stays fully readable through every read-only path — none of them enter
// loadMutableSpec, so none of them are affected by the freeze.
func TestFrozenSpec_ReadOnlyPathsStillWork(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	// A real SpecNew-created spec (proper "SPEC-NNN" ID, an initial history
	// row) advanced once, then frozen by archiving its backlog item — the
	// realistic shape a frozen spec has, unlike newFrozenSpecFixture's
	// synthetic ID (which specDocPath's format guard would reject).
	item, spec := promoteAndAdvance(t, svc, ctx, 1) // draft -> speccing
	if _, err := svc.BacklogArchive(ctx, model.BacklogArchiveRequest{ID: item.ID, Reason: "freeze for read-only test"}); err != nil {
		t.Fatalf("archive: %v", err)
	}

	if status, err := svc.SpecStatus(ctx, spec.ID); err != nil || status.Spec.ID != spec.ID {
		t.Errorf("SpecStatus on a frozen spec: status=%+v err=%v", status, err)
	}
	if list, err := svc.SpecList(ctx, model.SpecListRequest{Project: "project"}); err != nil || len(list.Specs) != 1 {
		t.Errorf("SpecList on a frozen spec: list=%+v err=%v", list, err)
	}
	if hist, err := svc.SpecHistory(ctx, spec.ID); err != nil || len(hist) == 0 {
		t.Errorf("SpecHistory on a frozen spec: hist=%+v err=%v", hist, err)
	}
	if _, err := svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{ID: spec.ID, Kind: model.SpecDocKindChanges, Content: "notes"}); err != nil {
		t.Errorf("SpecDocWrite on a frozen spec: %v", err)
	}
	if laneStatus, err := svc.LaneStatus(ctx, spec.ID); err != nil || laneStatus.Spec.ID != spec.ID {
		t.Errorf("LaneStatus on a frozen spec: laneStatus=%+v err=%v", laneStatus, err)
	}
	if _, err := svc.LaneStats(ctx, "project"); err != nil {
		t.Errorf("LaneStats with a frozen spec present: %v", err)
	}
}

// TestSpecVerbs_FrozenPrecedence is SPEC-125 AC21/AC22: the three-step
// precedence DD5 fixes — argument validation, then the freeze, then a
// verb's own state preconditions.
func TestSpecVerbs_FrozenPrecedence(t *testing.T) {
	t.Run("SpecReject: empty reason wins over the freeze (AC21)", func(t *testing.T) {
		svc := newTestSDDService(t, "project")
		ctx := context.Background()
		_, spec := newFrozenSpecFixture(t, svc, ctx, "SPEC-frozen-rej1", model.SpecStatusQA, model.LaneStandard, "", "")

		_, err := svc.SpecReject(ctx, model.SpecRejectRequest{ID: spec.ID, By: "qa"})
		if !errors.Is(err, model.ErrReasonRequired) {
			t.Fatalf("expected ErrReasonRequired, got %v", err)
		}
	})

	t.Run("SpecReject: with reason, the freeze wins (AC21)", func(t *testing.T) {
		svc := newTestSDDService(t, "project")
		ctx := context.Background()
		_, spec := newFrozenSpecFixture(t, svc, ctx, "SPEC-frozen-rej2", model.SpecStatusQA, model.LaneStandard, "", "")

		_, err := svc.SpecReject(ctx, model.SpecRejectRequest{ID: spec.ID, Reason: "found a bug", By: "qa"})
		if !errors.Is(err, model.ErrSpecFrozen) {
			t.Fatalf("expected ErrSpecFrozen, got %v", err)
		}
	})

	t.Run("LaneOverride: empty reason wins over the freeze (AC21)", func(t *testing.T) {
		svc := newTestSDDService(t, "project")
		ctx := context.Background()
		_, spec := newFrozenSpecFixture(t, svc, ctx, "SPEC-frozen-lo1", model.SpecStatusAudit, model.LaneTrivial, "internal/model/*.go", "")

		_, err := svc.LaneOverride(ctx, model.LaneOverrideRequest{ID: spec.ID, By: "orchestrator"})
		if !errors.Is(err, model.ErrReasonRequired) {
			t.Fatalf("expected ErrReasonRequired, got %v", err)
		}
	})

	t.Run("LaneOverride: with reason, the freeze wins (AC21)", func(t *testing.T) {
		svc := newTestSDDService(t, "project")
		ctx := context.Background()
		_, spec := newFrozenSpecFixture(t, svc, ctx, "SPEC-frozen-lo2", model.SpecStatusAudit, model.LaneTrivial, "internal/model/*.go", "")

		_, err := svc.LaneOverride(ctx, model.LaneOverrideRequest{ID: spec.ID, Reason: "force it", By: "orchestrator"})
		if !errors.Is(err, model.ErrSpecFrozen) {
			t.Fatalf("expected ErrSpecFrozen, got %v", err)
		}
	})

	t.Run("LaneAudit: freeze wins over lane/status preconditions (AC22)", func(t *testing.T) {
		svc := newTestSDDService(t, "project")
		ctx := context.Background()
		// Standard lane AND not in audit status: either alone would already
		// fail with ErrLaneMismatch or ErrInvalidTransition if the freeze
		// gate did not run first.
		_, spec := newFrozenSpecFixture(t, svc, ctx, "SPEC-frozen-la", model.SpecStatusImplementing, model.LaneStandard, "", "")

		_, err := svc.LaneAudit(ctx, model.LaneAuditRequest{ID: spec.ID})
		if !errors.Is(err, model.ErrSpecFrozen) {
			t.Fatalf("expected ErrSpecFrozen, got %v", err)
		}
		if errors.Is(err, model.ErrLaneMismatch) || errors.Is(err, model.ErrInvalidTransition) {
			t.Errorf("expected the freeze to win, got a state-precondition error instead: %v", err)
		}
	})

	t.Run("LaneReclassify: freeze wins over already-standard precondition (AC22)", func(t *testing.T) {
		svc := newTestSDDService(t, "project")
		ctx := context.Background()
		_, spec := newFrozenSpecFixture(t, svc, ctx, "SPEC-frozen-lr", model.SpecStatusDraft, model.LaneStandard, "", "")

		_, err := svc.LaneReclassify(ctx, model.LaneReclassifyRequest{ID: spec.ID, Lane: model.LaneStandard, By: "orchestrator"})
		if !errors.Is(err, model.ErrSpecFrozen) {
			t.Fatalf("expected ErrSpecFrozen, got %v", err)
		}
		if errors.Is(err, model.ErrLaneMismatch) {
			t.Errorf("expected the freeze to win, got ErrLaneMismatch instead: %v", err)
		}
	})
}

// TestSpecVerbs_NotFrozenWhenBacklogItemIsLive is SPEC-125 AC23/AC24: a spec
// whose BacklogID is empty, or whose linked item is raw/refined/promoted
// (never archived), is never frozen — every verb behaves exactly as before
// this spec.
func TestSpecVerbs_NotFrozenWhenBacklogItemIsLive(t *testing.T) {
	t.Run("no BacklogID at all (AC23)", func(t *testing.T) {
		svc := newTestSDDService(t, "project")
		ctx := context.Background()

		spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Standalone spec", Lane: model.LaneStandard})
		if err != nil {
			t.Fatalf("SpecNew: %v", err)
		}
		if spec.BacklogID != "" {
			t.Fatalf("expected no BacklogID, got %q", spec.BacklogID)
		}

		if _, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "orchestrator"}); err != nil {
			t.Errorf("SpecAdvance on a spec with no BacklogID must succeed, got %v", err)
		}
	})

	for _, status := range []model.BacklogStatus{model.BacklogStatusRaw, model.BacklogStatusRefined, model.BacklogStatusPromoted} {
		t.Run(fmt.Sprintf("linked item is %s (AC24)", status), func(t *testing.T) {
			svc := newTestSDDService(t, "project")
			ctx := context.Background()

			item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "Live item", Lane: model.LaneStandard})
			if err != nil {
				t.Fatalf("add: %v", err)
			}
			if status == model.BacklogStatusRaw {
				// Already raw; nothing else to do.
			} else if _, err := svc.BacklogRefine(ctx, model.BacklogRefineRequest{ID: item.ID, Refinement: "details"}); err != nil {
				t.Fatalf("refine: %v", err)
			}

			var spec *model.Spec
			if status == model.BacklogStatusPromoted {
				spec, err = svc.BacklogPromote(ctx, item.ID)
				if err != nil {
					t.Fatalf("promote: %v", err)
				}
			} else {
				spec, err = svc.SpecNew(ctx, model.SpecNewRequest{Title: "Manually linked", Lane: model.LaneStandard, BacklogID: item.ID})
				if err != nil {
					t.Fatalf("SpecNew: %v", err)
				}
			}

			if _, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "orchestrator"}); err != nil {
				t.Errorf("SpecAdvance on a spec linked to a %s item must succeed, got %v", status, err)
			}
		})
	}
}

// TestSpecVerbs_DanglingBacklogItemFailsClosed is SPEC-125 AC25: a spec
// whose BacklogID names a backlog item that does not exist fails closed
// with ErrBacklogNotFound, naming the missing item — only a manual edit
// produces this state, and loadMutableSpec must not archive-by-omission.
func TestSpecVerbs_DanglingBacklogItemFailsClosed(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
		Title: "Dangling backlog link", Lane: model.LaneStandard, BacklogID: "BL-does-not-exist",
	})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	_, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "orchestrator"})
	if !errors.Is(err, model.ErrBacklogNotFound) {
		t.Fatalf("expected ErrBacklogNotFound (fail closed), got %v", err)
	}
	if !strings.Contains(err.Error(), "BL-does-not-exist") {
		t.Errorf("expected the error to name the missing item, got %q", err.Error())
	}
}

// TestBacklogArchive_SpecDoneVetoed is SPEC-125 AC4/AC5: archiving an item
// whose linked spec already reached done is refused, naming the spec, and
// leaves the item exactly as it was.
func TestBacklogArchive_SpecDoneVetoed(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	item, spec := promoteAndAdvance(t, svc, ctx, 7) // draft -> ... -> done
	if spec.Status != model.SpecStatusDone {
		t.Fatalf("expected spec done, got %s", spec.Status)
	}

	_, err := svc.BacklogArchive(ctx, model.BacklogArchiveRequest{ID: item.ID, Reason: "trying to discard delivered work"})
	if !errors.Is(err, model.ErrBacklogSpecCompleted) {
		t.Fatalf("expected ErrBacklogSpecCompleted, got %v", err)
	}
	if !strings.Contains(err.Error(), spec.ID) {
		t.Errorf("expected error to name %s, got %q", spec.ID, err.Error())
	}

	after, err := svc.store.GetBacklogItem(ctx, item.ID)
	if err != nil {
		t.Fatalf("reload item: %v", err)
	}
	if after.Status != model.BacklogStatusPromoted {
		t.Errorf("expected item to remain promoted, got %s", after.Status)
	}
	if after.ArchiveReason != "" {
		t.Errorf("expected ArchiveReason untouched (empty), got %q", after.ArchiveReason)
	}
}

// TestBacklogArchive_SpecAliveAllowed is SPEC-125 AC6/AC32: archiving an
// item whose linked spec is NOT in a final state is allowed and freezes the
// spec, reported back with the exact status it held at archive time.
func TestBacklogArchive_SpecAliveAllowed(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	item, spec := promoteAndAdvance(t, svc, ctx, 1) // draft -> speccing
	if spec.Status != model.SpecStatusSpeccing {
		t.Fatalf("expected spec speccing, got %s", spec.Status)
	}

	result, err := svc.BacklogArchive(ctx, model.BacklogArchiveRequest{ID: item.ID, Reason: "work abandoned mid-flight"})
	if err != nil {
		t.Fatalf("BacklogArchive: %v", err)
	}
	if result.FrozenSpec == nil {
		t.Fatalf("expected FrozenSpec != nil for a live spec")
	}
	if result.FrozenSpec.ID != spec.ID || result.FrozenSpec.Title != spec.Title || result.FrozenSpec.Status != model.SpecStatusSpeccing {
		t.Errorf("FrozenSpec = %+v, want ID=%s Title=%s Status=%s", result.FrozenSpec, spec.ID, spec.Title, model.SpecStatusSpeccing)
	}
}

// TestBacklogArchive_DanglingSpecFailsClosed is SPEC-125 AC7 / DD2's fail-
// closed edge: item.SpecID is non-empty but points at a spec that does not
// exist. Only a manual DB edit produces this; the operation must refuse
// rather than archive blindly.
func TestBacklogArchive_DanglingSpecFailsClosed(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "Dangling link", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	item.SpecID = "SPEC-does-not-exist"
	item.Status = model.BacklogStatusPromoted
	if err := svc.store.UpdateBacklogItem(ctx, item); err != nil {
		t.Fatalf("simulate dangling link: %v", err)
	}

	_, err = svc.BacklogArchive(ctx, model.BacklogArchiveRequest{ID: item.ID, Reason: "should fail closed"})
	if !errors.Is(err, model.ErrSpecNotFound) {
		t.Fatalf("expected ErrSpecNotFound (fail closed), got %v", err)
	}

	after, err := svc.store.GetBacklogItem(ctx, item.ID)
	if err != nil {
		t.Fatalf("reload item: %v", err)
	}
	if after.Status == model.BacklogStatusArchived {
		t.Errorf("item must not be archived when the dangling link fails closed")
	}
}

// TestBacklogArchive_AlreadyArchived is SPEC-125 AC8/AC9: archiving an
// already-archived item is refused, and the ORIGINAL archive_reason is left
// untouched — the defect this veto exists to fix is a silent overwrite.
func TestBacklogArchive_AlreadyArchived(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "Archive twice", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := svc.BacklogArchive(ctx, model.BacklogArchiveRequest{ID: item.ID, Reason: "original decision"}); err != nil {
		t.Fatalf("first archive: %v", err)
	}

	_, err = svc.BacklogArchive(ctx, model.BacklogArchiveRequest{ID: item.ID, Reason: "second, different reason"})
	if !errors.Is(err, model.ErrBacklogAlreadyArchived) {
		t.Fatalf("expected ErrBacklogAlreadyArchived, got %v", err)
	}

	after, err := svc.store.GetBacklogItem(ctx, item.ID)
	if err != nil {
		t.Fatalf("reload item: %v", err)
	}
	if after.ArchiveReason != "original decision" {
		t.Errorf("ArchiveReason must stay the original: got %q", after.ArchiveReason)
	}
}

// TestBacklogArchive_AlreadyArchivedResolvesWithoutReadingSpec is SPEC-125
// AC10: the already-archived veto must fire BEFORE the spec is ever read —
// proven with an archived item whose spec_id points nowhere. A read-the-
// spec-first implementation would surface ErrSpecNotFound instead.
func TestBacklogArchive_AlreadyArchivedResolvesWithoutReadingSpec(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "Archived with dangling link", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	item.Status = model.BacklogStatusArchived
	item.ArchiveReason = "already gone"
	item.SpecID = "SPEC-does-not-exist"
	if err := svc.store.UpdateBacklogItem(ctx, item); err != nil {
		t.Fatalf("simulate archived+dangling: %v", err)
	}

	_, err = svc.BacklogArchive(ctx, model.BacklogArchiveRequest{ID: item.ID, Reason: "trying again"})
	if !errors.Is(err, model.ErrBacklogAlreadyArchived) {
		t.Fatalf("expected ErrBacklogAlreadyArchived, got %v", err)
	}
	if errors.Is(err, model.ErrSpecNotFound) {
		t.Fatalf("got ErrSpecNotFound: the spec was read before the already-archived veto")
	}
}

// TestBacklogList_LimitCapsSilentlyButTotalStaysTrue is AC6 and AC11 at the
// service level: 25 items with req.Limit=10 returns exactly 10 items but
// Total is the real count of 25 (AC6 — impossible before SPEC-109, when
// Total could never exceed the limit); req.Limit=500 (above ListMaxLimit)
// is silently capped to 50 with no error (AC11).
func TestBacklogList_LimitCapsSilentlyButTotalStaysTrue(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	for i := 0; i < 25; i++ {
		if _, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{
			Title: fmt.Sprintf("item %d", i), Lane: model.LaneStandard,
		}); err != nil {
			t.Fatalf("add item %d: %v", i, err)
		}
	}

	resp, err := svc.BacklogList(ctx, model.BacklogListRequest{Limit: 10})
	if err != nil {
		t.Fatalf("BacklogList(limit=10): %v", err)
	}
	if len(resp.Items) != 10 {
		t.Errorf("expected 10 items, got %d", len(resp.Items))
	}
	if resp.Total != 25 {
		t.Errorf("Total=%d, want 25 (the real match count, not the limit)", resp.Total)
	}

	over, err := svc.BacklogList(ctx, model.BacklogListRequest{Limit: 500})
	if err != nil {
		t.Fatalf("BacklogList(limit=500): %v", err)
	}
	if len(over.Items) > model.ListMaxLimit {
		t.Errorf("expected at most %d items, got %d", model.ListMaxLimit, len(over.Items))
	}
	if over.Total != 25 {
		t.Errorf("Total=%d, want 25", over.Total)
	}
}

// TestBacklogList_LimitZeroReturnsEverythingWithTrueTotal is AC12: Limit<=0
// returns all matching items and Total equals the returned count — the CLI's
// full-fidelity path.
func TestBacklogList_LimitZeroReturnsEverythingWithTrueTotal(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if _, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{
			Title: fmt.Sprintf("item %d", i), Lane: model.LaneStandard,
		}); err != nil {
			t.Fatalf("add item %d: %v", i, err)
		}
	}

	resp, err := svc.BacklogList(ctx, model.BacklogListRequest{})
	if err != nil {
		t.Fatalf("BacklogList: %v", err)
	}
	if len(resp.Items) != 5 {
		t.Errorf("expected 5 items, got %d", len(resp.Items))
	}
	if resp.Total != len(resp.Items) {
		t.Errorf("Total=%d, want %d (== len(Items))", resp.Total, len(resp.Items))
	}
}

// TestBacklogList_TotalRespectsStatusFilter is AC7: Total reflects the
// number of matches WITHIN the status filter, not the whole project.
func TestBacklogList_TotalRespectsStatusFilter(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{
			Title: fmt.Sprintf("raw %d", i), Lane: model.LaneStandard,
		}); err != nil {
			t.Fatalf("add raw %d: %v", i, err)
		}
	}
	refinedItem, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "will refine", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("add refined item: %v", err)
	}
	if _, err := svc.BacklogRefine(ctx, model.BacklogRefineRequest{ID: refinedItem.ID, Refinement: "r"}); err != nil {
		t.Fatalf("refine: %v", err)
	}

	resp, err := svc.BacklogList(ctx, model.BacklogListRequest{Status: model.BacklogStatusRaw, Limit: 1})
	if err != nil {
		t.Fatalf("BacklogList(status=raw, limit=1): %v", err)
	}
	if resp.Total != 3 {
		t.Errorf("Total=%d, want 3 (raw items only, not the 4 total items)", resp.Total)
	}
	if len(resp.Items) != 1 {
		t.Errorf("expected 1 item (limit=1), got %d", len(resp.Items))
	}
}

// --- SPEC TESTS ---

func TestSpecNew(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "SDD Engine", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	if spec.ID != "SPEC-001" {
		t.Errorf("ID: got %q, want SPEC-001", spec.ID)
	}
	if spec.Status != model.SpecStatusDraft {
		t.Errorf("Status: got %q, want draft", spec.Status)
	}
	if spec.Project != "project" {
		t.Errorf("Project: got %q, want project", spec.Project)
	}
}

func TestSpecAdvance_ValidTransitions(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Full lifecycle", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	// Happy path: draft -> speccing -> specced -> planning -> planned -> implementing -> qa -> done
	path := []model.SpecStatus{
		model.SpecStatusSpeccing,
		model.SpecStatusSpecced,
		model.SpecStatusPlanning,
		model.SpecStatusPlanned,
		model.SpecStatusImplementing,
		model.SpecStatusQA,
		model.SpecStatusDone,
	}

	for _, expectedNext := range path {
		t.Run("advance to "+string(expectedNext), func(t *testing.T) {
			advanced, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{
				ID: spec.ID,
				By: "orchestrator",
			})
			if err != nil {
				t.Fatalf("SpecAdvance: %v", err)
			}
			if advanced.Status != expectedNext {
				t.Errorf("Status: got %q, want %q", advanced.Status, expectedNext)
			}
			// Use updated spec for next iteration.
			spec = advanced
		})
	}
}

func TestSpecAdvance_InvalidTransition(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Invalid advance", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	// Advance to speccing (valid).
	if _, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "test"}); err != nil {
		t.Fatalf("advance to speccing: %v", err)
	}

	// Advance to specced (valid).
	if _, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "test"}); err != nil {
		t.Fatalf("advance to specced: %v", err)
	}

	// Now spec is in specced. Advance forward succeeds (to planning).
	// But done from specced is invalid — SpecAdvance would try to go to planning,
	// which is correct. Instead test that from done nothing can advance.
	// Advance all the way to done first.
	remaining := []string{"planning", "planned", "implementing", "qa", "done"}
	for _, s := range remaining {
		if _, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "test"}); err != nil {
			t.Fatalf("advance to %s: %v", s, err)
		}
	}

	// Now at done — advancing should fail.
	_, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "test"})
	if !errors.Is(err, model.ErrInvalidTransition) {
		t.Errorf("expected ErrInvalidTransition from done state, got %v", err)
	}
}

func TestSpecPushback_FromSpeccing(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Pushback test", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}
	// Advance to speccing.
	if _, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "orch"}); err != nil {
		t.Fatalf("advance: %v", err)
	}

	pushed, err := svc.SpecPushback(ctx, model.SpecPushbackRequest{
		ID:        spec.ID,
		FromAgent: "architect",
		Questions: []string{"Hook in Go or shell?", "Auth model?"},
	})
	if err != nil {
		t.Fatalf("SpecPushback: %v", err)
	}

	if pushed.Status != model.SpecStatusNeedsGrill {
		t.Errorf("Status: got %q, want needs_grill", pushed.Status)
	}
}

func TestSpecPushback_FromImplementing(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Impl pushback", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}
	// Advance through: speccing, specced, planning, planned, implementing.
	for range []int{0, 1, 2, 3, 4} {
		if _, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "test"}); err != nil {
			t.Fatalf("advance: %v", err)
		}
	}

	pushed, err := svc.SpecPushback(ctx, model.SpecPushbackRequest{
		ID:        spec.ID,
		FromAgent: "backend",
		Questions: []string{"Missing auth contract"},
	})
	if err != nil {
		t.Fatalf("SpecPushback from implementing: %v", err)
	}
	if pushed.Status != model.SpecStatusNeedsGrill {
		t.Errorf("Status: got %q, want needs_grill", pushed.Status)
	}
}

func TestSpecPushback_InvalidState(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Draft pushback", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	// Draft cannot transition to needs_grill.
	_, err = svc.SpecPushback(ctx, model.SpecPushbackRequest{
		ID:        spec.ID,
		FromAgent: "architect",
		Questions: []string{"Q?"},
	})
	if !errors.Is(err, model.ErrInvalidTransition) {
		t.Errorf("expected ErrInvalidTransition from draft, got %v", err)
	}
}

func TestSpecResolve(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Resolve test", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}
	// Advance to speccing.
	if _, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "orch"}); err != nil {
		t.Fatalf("advance: %v", err)
	}
	// Push back.
	if _, err := svc.SpecPushback(ctx, model.SpecPushbackRequest{
		ID: spec.ID, FromAgent: "architect", Questions: []string{"Q1"},
	}); err != nil {
		t.Fatalf("pushback: %v", err)
	}

	resolved, err := svc.SpecResolve(ctx, model.SpecResolveRequest{
		ID:         spec.ID,
		Resolution: "Use Go hooks, not shell",
	})
	if err != nil {
		t.Fatalf("SpecResolve: %v", err)
	}
	if resolved.Status != model.SpecStatusSpeccing {
		t.Errorf("Status: got %q, want speccing", resolved.Status)
	}

	// Verify pushback is now resolved.
	sr, err := svc.SpecStatus(ctx, spec.ID)
	if err != nil {
		t.Fatalf("SpecStatus: %v", err)
	}
	if len(sr.Pushbacks) != 1 || !sr.Pushbacks[0].Resolved {
		t.Error("expected pushback to be marked resolved")
	}
}

func TestSpecResolve_NotNeedsGrill(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "No grill", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	_, err = svc.SpecResolve(ctx, model.SpecResolveRequest{ID: spec.ID, Resolution: "N/A"})
	if !errors.Is(err, model.ErrInvalidTransition) {
		t.Errorf("expected ErrInvalidTransition, got %v", err)
	}
}

func TestSpecStatus(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Status test", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}
	// Advance to speccing.
	if _, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "orch"}); err != nil {
		t.Fatalf("advance: %v", err)
	}
	// Push back.
	if _, err := svc.SpecPushback(ctx, model.SpecPushbackRequest{
		ID: spec.ID, FromAgent: "architect", Questions: []string{"Q1"},
	}); err != nil {
		t.Fatalf("pushback: %v", err)
	}

	sr, err := svc.SpecStatus(ctx, spec.ID)
	if err != nil {
		t.Fatalf("SpecStatus: %v", err)
	}

	if sr.Spec == nil {
		t.Fatal("Spec must not be nil")
	}
	if sr.Spec.Status != model.SpecStatusNeedsGrill {
		t.Errorf("Spec.Status: got %q, want needs_grill", sr.Spec.Status)
	}
	if len(sr.History) == 0 {
		t.Error("History must not be empty")
	}
	if len(sr.Pushbacks) != 1 {
		t.Errorf("expected 1 pushback, got %d", len(sr.Pushbacks))
	}
}

// TestSpecStatus_ReportsFreeze is SPEC-126 AC6-AC9: spec_status reports why
// a spec can no longer move, and — unlike loadMutableSpec — NEVER fails the
// read because of it: a frozen spec (or one whose link is dangling) stays
// fully readable (SPEC-125 AC13).
func TestSpecStatus_ReportsFreeze(t *testing.T) {
	t.Run("archived item (AC6)", func(t *testing.T) {
		svc := newTestSDDService(t, "project")
		ctx := context.Background()
		item, spec := newFrozenSpecFixture(t, svc, ctx, "SPEC-frozen-status", model.SpecStatusImplementing, model.LaneStandard, "", "")

		resp, err := svc.SpecStatus(ctx, spec.ID)
		if err != nil {
			t.Fatalf("SpecStatus: %v", err)
		}
		if resp.Frozen == nil {
			t.Fatal("expected Frozen != nil")
		}
		if resp.Frozen.State != model.SpecFreezeArchived {
			t.Errorf("State: got %q, want archived", resp.Frozen.State)
		}
		if resp.Frozen.BacklogID != item.ID {
			t.Errorf("BacklogID: got %q, want %q", resp.Frozen.BacklogID, item.ID)
		}
		// newFrozenSpecFixture archives with this literal reason (item, as
		// returned by BacklogAdd, predates the archive and still carries the
		// zero-value ArchiveReason).
		const wantReason = "fixture archive"
		if resp.Frozen.Reason != wantReason {
			t.Errorf("Reason: got %q, want %q", resp.Frozen.Reason, wantReason)
		}
	})

	for _, status := range []model.BacklogStatus{model.BacklogStatusRaw, model.BacklogStatusRefined, model.BacklogStatusPromoted} {
		t.Run(fmt.Sprintf("live item %s (AC7)", status), func(t *testing.T) {
			svc := newTestSDDService(t, "project")
			ctx := context.Background()

			item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "Live item", Lane: model.LaneStandard})
			if err != nil {
				t.Fatalf("add: %v", err)
			}
			if status != model.BacklogStatusRaw {
				if _, err := svc.BacklogRefine(ctx, model.BacklogRefineRequest{ID: item.ID, Refinement: "details"}); err != nil {
					t.Fatalf("refine: %v", err)
				}
			}

			var spec *model.Spec
			if status == model.BacklogStatusPromoted {
				spec, err = svc.BacklogPromote(ctx, item.ID)
				if err != nil {
					t.Fatalf("promote: %v", err)
				}
			} else {
				spec, err = svc.SpecNew(ctx, model.SpecNewRequest{Title: "Linked", Lane: model.LaneStandard, BacklogID: item.ID})
				if err != nil {
					t.Fatalf("SpecNew: %v", err)
				}
			}

			resp, err := svc.SpecStatus(ctx, spec.ID)
			if err != nil {
				t.Fatalf("SpecStatus: %v", err)
			}
			if resp.Frozen != nil {
				t.Errorf("expected Frozen == nil for a %s item, got %+v", status, resp.Frozen)
			}
		})
	}

	t.Run("no BacklogID, empty backlog table (AC8)", func(t *testing.T) {
		svc := newTestSDDService(t, "project")
		ctx := context.Background()

		spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Standalone", Lane: model.LaneStandard})
		if err != nil {
			t.Fatalf("SpecNew: %v", err)
		}

		resp, err := svc.SpecStatus(ctx, spec.ID)
		if err != nil {
			t.Fatalf("SpecStatus must succeed, got %v", err)
		}
		if resp.Frozen != nil {
			t.Errorf("expected Frozen == nil, got %+v", resp.Frozen)
		}
	})

	t.Run("BacklogID names a missing item (AC9)", func(t *testing.T) {
		svc := newTestSDDService(t, "project")
		ctx := context.Background()

		spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
			Title: "Dangling", Lane: model.LaneStandard, BacklogID: "BL-does-not-exist",
		})
		if err != nil {
			t.Fatalf("SpecNew: %v", err)
		}

		resp, err := svc.SpecStatus(ctx, spec.ID)
		if err != nil {
			t.Fatalf("SpecStatus must succeed even with a dangling BacklogID, got %v", err)
		}
		if resp.Frozen == nil {
			t.Fatal("expected Frozen != nil")
		}
		if resp.Frozen.State != model.SpecFreezeMissing {
			t.Errorf("State: got %q, want missing", resp.Frozen.State)
		}
		if resp.Frozen.BacklogID != "BL-does-not-exist" {
			t.Errorf("BacklogID: got %q, want BL-does-not-exist", resp.Frozen.BacklogID)
		}
	})
}

func TestSpecList_FilterByStatus(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	// Create 2 specs in draft, advance 1 to speccing.
	s1, _ := svc.SpecNew(ctx, model.SpecNewRequest{Title: "S1", Lane: model.LaneStandard})
	s2, _ := svc.SpecNew(ctx, model.SpecNewRequest{Title: "S2", Lane: model.LaneStandard})
	if _, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: s1.ID, By: "test"}); err != nil {
		t.Fatalf("advance s1: %v", err)
	}
	_ = s2

	draftsResp, err := svc.SpecList(ctx, model.SpecListRequest{Status: model.SpecStatusDraft})
	if err != nil {
		t.Fatalf("SpecList(draft): %v", err)
	}
	if len(draftsResp.Specs) != 1 {
		t.Errorf("expected 1 draft, got %d", len(draftsResp.Specs))
	}
	if draftsResp.Total != 1 {
		t.Errorf("expected Total=1, got %d", draftsResp.Total)
	}

	speccingResp, err := svc.SpecList(ctx, model.SpecListRequest{Status: model.SpecStatusSpeccing})
	if err != nil {
		t.Fatalf("SpecList(speccing): %v", err)
	}
	if len(speccingResp.Specs) != 1 {
		t.Errorf("expected 1 speccing, got %d", len(speccingResp.Specs))
	}
}

// --- LANE TESTS ---

// TestBacklogAdd_LaneRequired verifies that omitting lane returns ErrLaneRequired.
func TestBacklogAdd_LaneRequired(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	_, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "Test"})
	if !errors.Is(err, model.ErrLaneRequired) {
		t.Errorf("expected ErrLaneRequired, got %v", err)
	}
}

// TestBacklogAdd_ScopeRequired verifies that trivial lane without scope returns ErrScopeRequired.
func TestBacklogAdd_ScopeRequired(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	_, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "Test", Lane: model.LaneTrivial})
	if !errors.Is(err, model.ErrScopeRequired) {
		t.Errorf("expected ErrScopeRequired, got %v", err)
	}
}

// TestSpecNew_LaneRequired verifies that omitting lane returns ErrLaneRequired.
func TestSpecNew_LaneRequired(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	_, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Test"})
	if !errors.Is(err, model.ErrLaneRequired) {
		t.Errorf("expected ErrLaneRequired, got %v", err)
	}
}

// TestSpecNew_ScopeRequired verifies that trivial lane without scope returns ErrScopeRequired.
func TestSpecNew_ScopeRequired(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	_, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Test", Lane: model.LaneTrivial})
	if !errors.Is(err, model.ErrScopeRequired) {
		t.Errorf("expected ErrScopeRequired, got %v", err)
	}
}

// TestSpecQuick_RejectsStandard verifies that spec_quick returns ErrLaneMismatch
// when called on a standard-lane spec.
func TestSpecQuick_RejectsStandard(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Standard spec", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	_, err = svc.SpecQuick(ctx, model.SpecQuickRequest{
		ID:        spec.ID,
		Rationale: "This is quick",
		By:        "orchestrator",
	})
	if !errors.Is(err, model.ErrLaneMismatch) {
		t.Errorf("expected ErrLaneMismatch, got %v", err)
	}
}

// TestSpecQuick_TrivialFlow verifies the happy path: trivial spec goes from
// draft to implementing after spec_quick.
func TestSpecQuick_TrivialFlow(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
		Title: "Tiny fix",
		Lane:  model.LaneTrivial,
		Scope: "internal/store/*.go",
	})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}
	if spec.Lane != model.LaneTrivial {
		t.Errorf("Lane: got %q, want trivial", spec.Lane)
	}

	implementing, err := svc.SpecQuick(ctx, model.SpecQuickRequest{
		ID:        spec.ID,
		Rationale: "One-line fix to a comment typo",
		By:        "orchestrator",
	})
	if err != nil {
		t.Fatalf("SpecQuick: %v", err)
	}
	if implementing.Status != model.SpecStatusImplementing {
		t.Errorf("Status: got %q, want implementing", implementing.Status)
	}
}

// TestBacklogPromote_PropagatesLane verifies that BacklogPromote copies lane and
// scope from the backlog item to the newly created spec.
func TestBacklogPromote_PropagatesLane(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{
		Title: "Tiny thing",
		Lane:  model.LaneTrivial,
		Scope: "internal/model/*.go",
	})
	if err != nil {
		t.Fatalf("BacklogAdd: %v", err)
	}
	if _, err := svc.BacklogRefine(ctx, model.BacklogRefineRequest{
		ID:         item.ID,
		Refinement: "One-line refactoring",
	}); err != nil {
		t.Fatalf("BacklogRefine: %v", err)
	}

	spec, err := svc.BacklogPromote(ctx, item.ID)
	if err != nil {
		t.Fatalf("BacklogPromote: %v", err)
	}
	if spec.Lane != model.LaneTrivial {
		t.Errorf("spec.Lane: got %q, want trivial", spec.Lane)
	}
	if spec.Scope != "internal/model/*.go" {
		t.Errorf("spec.Scope: got %q, want internal/model/*.go", spec.Scope)
	}
}

// TestSpecAdvance_TrivialPath verifies the trivial lane forward path:
// draft → rationale → implementing → audit → done (using manual store transitions
// to avoid git dependency on the audit step).
func TestSpecAdvance_TrivialPath(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
		Title: "Trivial fix",
		Lane:  model.LaneTrivial,
		Scope: "internal/model/*.go",
	})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	// draft → rationale
	advanced, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "orch"})
	if err != nil {
		t.Fatalf("advance draft->rationale: %v", err)
	}
	if advanced.Status != model.SpecStatusRationale {
		t.Errorf("Status: got %q, want rationale", advanced.Status)
	}

	// rationale → implementing
	advanced, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "orch"})
	if err != nil {
		t.Fatalf("advance rationale->implementing: %v", err)
	}
	if advanced.Status != model.SpecStatusImplementing {
		t.Errorf("Status: got %q, want implementing", advanced.Status)
	}

	// implementing → audit
	advanced, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "orch"})
	if err != nil {
		t.Fatalf("advance implementing->audit: %v", err)
	}
	if advanced.Status != model.SpecStatusAudit {
		t.Errorf("Status: got %q, want audit", advanced.Status)
	}
}

// TestLaneReclassify_TrivialToStandard verifies that a trivial spec in draft
// can be reclassified to standard and moves to speccing.
func TestLaneReclassify_TrivialToStandard(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
		Title: "Will become standard",
		Lane:  model.LaneTrivial,
		Scope: "internal/model/*.go",
	})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	reclassified, err := svc.LaneReclassify(ctx, model.LaneReclassifyRequest{
		ID:   spec.ID,
		Lane: model.LaneStandard,
		By:   "orchestrator",
	})
	if err != nil {
		t.Fatalf("LaneReclassify: %v", err)
	}
	if reclassified.Lane != model.LaneStandard {
		t.Errorf("Lane: got %q, want standard", reclassified.Lane)
	}
	if reclassified.Status != model.SpecStatusSpeccing {
		t.Errorf("Status: got %q, want speccing", reclassified.Status)
	}
}

// TestLaneOverride_RequiresReason verifies that LaneOverride returns ErrReasonRequired
// when no reason is provided.
func TestLaneOverride_RequiresReason(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	_, err := svc.LaneOverride(ctx, model.LaneOverrideRequest{ID: "SPEC-001"})
	if !errors.Is(err, model.ErrReasonRequired) {
		t.Errorf("expected ErrReasonRequired, got %v", err)
	}
}

// TestLaneStatus_AfterFailedAudit verifies that LaneStatus returns the correct
// breaches from the most recent audit failure recorded in the lane_audits table.
// Updated for SPEC-036: uses InsertLaneAudit instead of the removed
// InsertSpecHistoryEntry hack; breaches are stored newline-joined.
func TestLaneStatus_AfterFailedAudit(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	// Create a trivial-lane spec.
	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
		Title: "Regression: LaneStatus after failed audit",
		Lane:  model.LaneTrivial,
		Scope: "internal/model/*.go",
	})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	// Advance: draft → rationale → implementing.
	spec, err = svc.SpecQuick(ctx, model.SpecQuickRequest{
		ID:        spec.ID,
		Rationale: "Tiny fix",
		By:        "orchestrator",
	})
	if err != nil {
		t.Fatalf("SpecQuick: %v", err)
	}

	// Advance: implementing → audit.
	spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{
		ID:     spec.ID,
		By:     "backend",
		Reason: "implementation done",
	})
	if err != nil {
		t.Fatalf("SpecAdvance (implementing→audit): %v", err)
	}
	if spec.Status != model.SpecStatusAudit {
		t.Fatalf("expected audit status, got %s", spec.Status)
	}

	// Insert a structured audit failure into lane_audits (SPEC-036 approach).
	// Breaches are stored newline-joined, not "; "-joined.
	breaches := []string{"file count 5 exceeds trivial limit of 3", "line count 42 exceeds trivial limit of 20"}
	auditRec := &model.LaneAuditRecord{
		SpecID:       spec.ID,
		Passed:       false,
		FileCount:    5,
		LinesChanged: 42,
		Breaches:     strings.Join(breaches, "\n"),
		BaseSHA:      "test-sha",
	}
	if err := svc.store.InsertLaneAudit(ctx, auditRec); err != nil {
		t.Fatalf("InsertLaneAudit: %v", err)
	}

	// LaneStatus must reflect the AUDIT FAILURE (not the implementing→audit reason).
	status, err := svc.LaneStatus(ctx, spec.ID)
	if err != nil {
		t.Fatalf("LaneStatus: %v", err)
	}

	if status.LatestAudit == nil {
		t.Fatal("expected LatestAudit to be non-nil after a recorded audit failure")
	}
	if status.LatestAudit.Passed {
		t.Error("expected LatestAudit.Passed=false")
	}
	if len(status.LatestAudit.Breaches) == 0 {
		t.Error("expected non-empty Breaches in LatestAudit")
	}
	// Verify the specific breach messages are preserved correctly.
	for _, want := range breaches {
		found := false
		for _, got := range status.LatestAudit.Breaches {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("breach %q not found in LatestAudit.Breaches %v", want, status.LatestAudit.Breaches)
		}
	}
}

// TestLaneStatus_NoAuditRun verifies that LaneStatus returns nil LatestAudit when
// the spec has only been transitioned to audit status but no audit has run.
// This ensures we don't accidentally report the implementing→audit reason as breaches.
func TestLaneStatus_NoAuditRun(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
		Title: "No audit run yet",
		Lane:  model.LaneTrivial,
		Scope: "internal/model/*.go",
	})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	spec, err = svc.SpecQuick(ctx, model.SpecQuickRequest{
		ID:        spec.ID,
		Rationale: "Small change",
		By:        "orchestrator",
	})
	if err != nil {
		t.Fatalf("SpecQuick: %v", err)
	}

	spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{
		ID:     spec.ID,
		By:     "backend",
		Reason: "done implementing",
	})
	if err != nil {
		t.Fatalf("SpecAdvance (implementing→audit): %v", err)
	}

	// No audit has been run yet. LaneStatus must NOT report the
	// implementing→audit transition as an audit result.
	status, err := svc.LaneStatus(ctx, spec.ID)
	if err != nil {
		t.Fatalf("LaneStatus: %v", err)
	}

	if status.LatestAudit != nil {
		t.Errorf("expected LatestAudit=nil before any audit has run, got %+v", status.LatestAudit)
	}
}

// newTestGitRepo initialises a minimal git repository in a temp dir and returns
// the directory path. It creates an initial commit so HEAD is valid.
func newTestGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}

	dir := t.TempDir()

	gitRun := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	gitRun("init", "-b", "main")
	gitRun("config", "user.email", "test@test.com")
	gitRun("config", "user.name", "Test")

	fPath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(fPath, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	gitRun("add", ".")
	gitRun("commit", "-m", "initial")

	return dir
}

// TestSpecReject_StandardQAToImplementing verifies that a standard-lane spec in
// qa status can be rejected back to implementing.
func TestSpecReject_StandardQAToImplementing(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
		Title: "Standard reject test",
		Lane:  model.LaneStandard,
	})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	// Advance to qa: draft→speccing→specced→planning→planned→implementing→qa (6 steps).
	for _, by := range []string{"orch", "arch", "arch", "arch", "backend", "backend"} {
		spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: by})
		if err != nil {
			t.Fatalf("SpecAdvance (to qa, status=%s): %v", spec.Status, err)
		}
	}
	if spec.Status != model.SpecStatusQA {
		t.Fatalf("expected qa status, got %s", spec.Status)
	}

	rejected, err := svc.SpecReject(ctx, model.SpecRejectRequest{
		ID:     spec.ID,
		Reason: "tests fail on edge case",
		By:     "qa-agent",
	})
	if err != nil {
		t.Fatalf("SpecReject: %v", err)
	}
	if rejected.Status != model.SpecStatusImplementing {
		t.Errorf("expected implementing, got %s", rejected.Status)
	}
}

// TestSpecReject_TrivialAuditToImplementing verifies that a trivial-lane spec
// in audit status can be rejected back to implementing.
func TestSpecReject_TrivialAuditToImplementing(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
		Title: "Trivial reject test",
		Lane:  model.LaneTrivial,
		Scope: "internal/model/*.go",
	})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	spec, err = svc.SpecQuick(ctx, model.SpecQuickRequest{
		ID:        spec.ID,
		Rationale: "One-line fix",
		By:        "orchestrator",
	})
	if err != nil {
		t.Fatalf("SpecQuick: %v", err)
	}

	// implementing → audit.
	spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "backend"})
	if err != nil {
		t.Fatalf("SpecAdvance to audit: %v", err)
	}
	if spec.Status != model.SpecStatusAudit {
		t.Fatalf("expected audit, got %s", spec.Status)
	}

	rejected, err := svc.SpecReject(ctx, model.SpecRejectRequest{
		ID:     spec.ID,
		Reason: "scope exceeded",
		By:     "orchestrator",
	})
	if err != nil {
		t.Fatalf("SpecReject (trivial audit): %v", err)
	}
	if rejected.Status != model.SpecStatusImplementing {
		t.Errorf("expected implementing, got %s", rejected.Status)
	}
}

// TestSpecReject_EmptyReasonReturnsError verifies that SpecReject returns
// ErrReasonRequired when Reason is empty.
func TestSpecReject_EmptyReasonReturnsError(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	_, err := svc.SpecReject(ctx, model.SpecRejectRequest{
		ID:     "SPEC-001",
		Reason: "",
		By:     "orchestrator",
	})
	if !errors.Is(err, model.ErrReasonRequired) {
		t.Errorf("expected ErrReasonRequired, got %v", err)
	}
}

// TestSpecReject_InvalidStatusReturnsError verifies that SpecReject returns
// ErrInvalidTransition when the spec is in a status that does not allow
// a backward transition to implementing (e.g. draft).
func TestSpecReject_InvalidStatusReturnsError(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
		Title: "Reject invalid",
		Lane:  model.LaneStandard,
	})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	// Spec is in draft — cannot reject from draft.
	_, err = svc.SpecReject(ctx, model.SpecRejectRequest{
		ID:     spec.ID,
		Reason: "bad status",
		By:     "orchestrator",
	})
	if !errors.Is(err, model.ErrInvalidTransition) {
		t.Errorf("expected ErrInvalidTransition, got %v", err)
	}
}

// --- SPEC-087 D6: spec_reject from done ---

// TestSpecReject_StandardDoneToImplementing verifies AC7: a standard-lane
// spec in done status can be rejected back to implementing, with the reason
// recorded in spec_history.
//
// Mutation guard (manually verified): removing the SpecStatusDone row from
// validTransitionsStandard turns this test red (ErrInvalidTransition).
func TestSpecReject_StandardDoneToImplementing(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Standard done reject", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}
	for _, by := range []string{"orch", "arch", "arch", "arch", "backend", "backend", "qa"} {
		spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: by})
		if err != nil {
			t.Fatalf("SpecAdvance (status=%s): %v", spec.Status, err)
		}
	}
	if spec.Status != model.SpecStatusDone {
		t.Fatalf("expected done status before reject, got %s", spec.Status)
	}

	rejected, err := svc.SpecReject(ctx, model.SpecRejectRequest{
		ID:     spec.ID,
		Reason: "post-hoc review found a regression",
		By:     "qa-agent",
	})
	if err != nil {
		t.Fatalf("SpecReject from done: %v", err)
	}
	if rejected.Status != model.SpecStatusImplementing {
		t.Errorf("expected implementing, got %s", rejected.Status)
	}

	history, err := svc.SpecHistory(ctx, spec.ID)
	if err != nil {
		t.Fatalf("SpecHistory: %v", err)
	}
	last := history[len(history)-1]
	if last.FromStatus != model.SpecStatusDone || last.ToStatus != model.SpecStatusImplementing {
		t.Errorf("last history entry = %s->%s, want done->implementing", last.FromStatus, last.ToStatus)
	}
	if !strings.Contains(last.Reason, "post-hoc review found a regression") {
		t.Errorf("history reason = %q, want it to contain the rejection reason", last.Reason)
	}
}

// TestSpecReject_TrivialDoneToImplementing mirrors the standard-lane test
// for the trivial lane (AC7).
//
// Mutation guard (manually verified): removing the SpecStatusDone row from
// validTransitionsTrivial turns this test red (ErrInvalidTransition).
func TestSpecReject_TrivialDoneToImplementing(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
		Title: "Trivial done reject", Lane: model.LaneTrivial, Scope: "internal/model/*.go",
	})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}
	spec, err = svc.SpecQuick(ctx, model.SpecQuickRequest{ID: spec.ID, Rationale: "one-liner", By: "orchestrator"})
	if err != nil {
		t.Fatalf("SpecQuick: %v", err)
	}
	for _, by := range []string{"backend", "backend"} {
		spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: by})
		if err != nil {
			t.Fatalf("SpecAdvance (status=%s): %v", spec.Status, err)
		}
	}
	if spec.Status != model.SpecStatusDone {
		t.Fatalf("expected done status before reject, got %s", spec.Status)
	}

	rejected, err := svc.SpecReject(ctx, model.SpecRejectRequest{
		ID:     spec.ID,
		Reason: "scope was actually exceeded",
		By:     "orchestrator",
	})
	if err != nil {
		t.Fatalf("SpecReject from done (trivial): %v", err)
	}
	if rejected.Status != model.SpecStatusImplementing {
		t.Errorf("expected implementing, got %s", rejected.Status)
	}
}

// TestSpecAdvance_FromDone_StillInvalid is AC8's non-regression guard: D6
// only opens spec_reject from done, never spec_advance.
// nextForwardStatusForLane has no SpecStatusDone key, so SpecAdvance keeps
// failing with ErrInvalidTransition before CanTransitionTo is even
// consulted.
//
// Mutation guard (manually verified): adding
// `model.SpecStatusDone: model.SpecStatusImplementing` to
// nextForwardStatusForLane's standardForward map turns this test red (the
// advance would succeed instead of failing).
func TestSpecAdvance_FromDone_StillInvalid(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Advance from done", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}
	for _, by := range []string{"orch", "arch", "arch", "arch", "backend", "backend", "qa"} {
		spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: by})
		if err != nil {
			t.Fatalf("SpecAdvance (status=%s): %v", spec.Status, err)
		}
	}
	if spec.Status != model.SpecStatusDone {
		t.Fatalf("expected done, got %s", spec.Status)
	}

	_, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "orchestrator"})
	if !errors.Is(err, model.ErrInvalidTransition) {
		t.Errorf("expected ErrInvalidTransition advancing from done, got %v", err)
	}
}

// TestSpecPushback_FromDone_StillInvalid is AC8's non-regression guard for
// SpecPushback: done has no needs_grill row in either transition table, so
// a pushback from done keeps failing.
func TestSpecPushback_FromDone_StillInvalid(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Pushback from done", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}
	for _, by := range []string{"orch", "arch", "arch", "arch", "backend", "backend", "qa"} {
		spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: by})
		if err != nil {
			t.Fatalf("SpecAdvance (status=%s): %v", spec.Status, err)
		}
	}
	if spec.Status != model.SpecStatusDone {
		t.Fatalf("expected done, got %s", spec.Status)
	}

	_, err = svc.SpecPushback(ctx, model.SpecPushbackRequest{ID: spec.ID, FromAgent: "qa", Questions: []string{"still ok?"}})
	if !errors.Is(err, model.ErrInvalidTransition) {
		t.Errorf("expected ErrInvalidTransition pushing back from done, got %v", err)
	}
}

// newTestSDDServiceWithMemory mirrors newTestSDDService but wires a real
// MemoryService over the SAME project database, so saveCompletionMemory
// (triggered when a spec re-enters done) actually persists — needed for
// TestSpecReject_DoneThenReAdvance_SingleCompletionMemory (AC9).
func newTestSDDServiceWithMemory(t *testing.T, project string) *SDDService {
	t.Helper()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })

	projectStore := store.NewMemoryStore(database)
	cfg := config.Default()
	memSvc := NewMemoryService(projectStore, projectStore, cfg, project, embed.NopEmbedder{})

	sddStore := store.NewSDDStore(database)
	return NewSDDService(sddStore, cfg, project, memSvc)
}

// TestSpecReject_DoneThenReAdvance_SingleCompletionMemory verifies AC9: a
// spec rejected from done and re-advanced back to done ends up with exactly
// ONE spec/<ID> completion memory (topic_key upsert, V4 in the design), with
// content reflecting the latest completion.
func TestSpecReject_DoneThenReAdvance_SingleCompletionMemory(t *testing.T) {
	svc := newTestSDDServiceWithMemory(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "AC9 spec", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}
	for _, by := range []string{"orch", "arch", "arch", "arch", "backend", "backend", "qa"} {
		spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: by})
		if err != nil {
			t.Fatalf("SpecAdvance (status=%s): %v", spec.Status, err)
		}
	}
	if spec.Status != model.SpecStatusDone {
		t.Fatalf("expected done, got %s", spec.Status)
	}

	if _, err := svc.SpecReject(ctx, model.SpecRejectRequest{
		ID: spec.ID, Reason: "found a regression", By: "qa-agent",
	}); err != nil {
		t.Fatalf("SpecReject: %v", err)
	}

	spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "backend"})
	if err != nil {
		t.Fatalf("re-advance to qa: %v", err)
	}
	if spec.Status != model.SpecStatusQA {
		t.Fatalf("expected qa, got %s", spec.Status)
	}
	spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "qa"})
	if err != nil {
		t.Fatalf("re-advance to done: %v", err)
	}
	if spec.Status != model.SpecStatusDone {
		t.Fatalf("expected done again, got %s", spec.Status)
	}

	// GetByTopicKey is itself the upsert-uniqueness guarantee (V4): the
	// unique index on (topic_key, project, scope) means a second
	// saveCompletionMemory call physically cannot create a second row for
	// the same topic_key — it can only update the existing one. Asserting
	// the single row's content reflects the SECOND completion (not stale
	// first-completion content) is therefore the meaningful AC9 check.
	mem, err := svc.memorySvc.projectStore.GetByTopicKey(ctx, "spec/"+spec.ID, "project")
	if err != nil {
		t.Fatalf("GetByTopicKey: %v", err)
	}
	if mem == nil {
		t.Fatal("expected a completion memory to exist after re-advancing to done")
	}
	if !strings.Contains(mem.Content, "Completed via spec "+spec.ID) {
		t.Errorf("completion memory content = %q, missing expected marker", mem.Content)
	}
}

// TestCaptureBaseSHA_ImplementingViaSpecAdvance verifies that when a standard
// spec enters implementing via SpecAdvance in a valid git repo, base_sha is set.
func TestCaptureBaseSHA_ImplementingViaSpecAdvance(t *testing.T) {
	repoDir := newTestGitRepo(t)

	svc := newTestSDDService(t, "project")
	svc.WithRepoDir(repoDir)
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
		Title: "SHA via advance",
		Lane:  model.LaneStandard,
	})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	// Advance to implementing: draft→speccing→specced→planning→planned→implementing (5 steps).
	for i := range 5 {
		spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "test"})
		if err != nil {
			t.Fatalf("SpecAdvance %d: %v", i, err)
		}
	}
	if spec.Status != model.SpecStatusImplementing {
		t.Fatalf("expected implementing, got %s", spec.Status)
	}

	// Reload to pick up the async captureBaseSHA write.
	got, err := svc.store.GetSpec(ctx, spec.ID)
	if err != nil {
		t.Fatalf("GetSpec: %v", err)
	}
	if got.BaseSHA == "" {
		t.Error("expected base_sha to be set after entering implementing, got empty string")
	}
	if len(got.BaseSHA) != 40 {
		t.Errorf("expected 40-char SHA, got %q", got.BaseSHA)
	}
}

// TestCaptureBaseSHA_ImplementingViaSpecQuick verifies that a trivial spec
// entering implementing via SpecQuick also captures base_sha.
func TestCaptureBaseSHA_ImplementingViaSpecQuick(t *testing.T) {
	repoDir := newTestGitRepo(t)

	svc := newTestSDDService(t, "project")
	svc.WithRepoDir(repoDir)
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
		Title: "SHA via quick",
		Lane:  model.LaneTrivial,
		Scope: "internal/model/*.go",
	})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	spec, err = svc.SpecQuick(ctx, model.SpecQuickRequest{
		ID:        spec.ID,
		Rationale: "Tiny fix",
		By:        "orchestrator",
	})
	if err != nil {
		t.Fatalf("SpecQuick: %v", err)
	}
	if spec.Status != model.SpecStatusImplementing {
		t.Fatalf("expected implementing, got %s", spec.Status)
	}

	got, err := svc.store.GetSpec(ctx, spec.ID)
	if err != nil {
		t.Fatalf("GetSpec: %v", err)
	}
	if got.BaseSHA == "" {
		t.Error("expected base_sha to be set after spec_quick, got empty string")
	}
}

// TestCaptureBaseSHA_NoGitNoBlock verifies that when repoDir is not a git
// repository, base_sha stays "" but the transition completes without error.
func TestCaptureBaseSHA_NoGitNoBlock(t *testing.T) {
	svc := newTestSDDService(t, "project")
	svc.WithRepoDir(t.TempDir()) // not a git repo
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
		Title: "No git, no block",
		Lane:  model.LaneTrivial,
		Scope: "internal/**",
	})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	spec, err = svc.SpecQuick(ctx, model.SpecQuickRequest{
		ID:        spec.ID,
		Rationale: "Should not fail",
		By:        "orchestrator",
	})
	if err != nil {
		t.Fatalf("SpecQuick must not fail when git is absent: %v", err)
	}
	if spec.Status != model.SpecStatusImplementing {
		t.Errorf("expected implementing, got %s", spec.Status)
	}

	got, err := svc.store.GetSpec(ctx, spec.ID)
	if err != nil {
		t.Fatalf("GetSpec: %v", err)
	}
	// base_sha should be empty — git failed silently.
	if got.BaseSHA != "" {
		t.Errorf("expected base_sha='', got %q", got.BaseSHA)
	}
}

// TestLaneStatus_RejectionCount verifies that RejectionCount reflects the
// number of qa/audit → implementing backward transitions in spec_history.
func TestLaneStatus_RejectionCount(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
		Title: "Rejection count test",
		Lane:  model.LaneTrivial,
		Scope: "internal/**",
	})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	spec, err = svc.SpecQuick(ctx, model.SpecQuickRequest{
		ID:        spec.ID,
		Rationale: "Small fix",
		By:        "orchestrator",
	})
	if err != nil {
		t.Fatalf("SpecQuick: %v", err)
	}

	// implementing → audit.
	spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "backend"})
	if err != nil {
		t.Fatalf("SpecAdvance to audit: %v", err)
	}

	// First rejection: audit → implementing.
	spec, err = svc.SpecReject(ctx, model.SpecRejectRequest{
		ID:     spec.ID,
		Reason: "first failure",
		By:     "orchestrator",
	})
	if err != nil {
		t.Fatalf("first SpecReject: %v", err)
	}

	// implementing → audit again.
	spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "backend"})
	if err != nil {
		t.Fatalf("SpecAdvance to audit (2nd): %v", err)
	}

	// Second rejection: audit → implementing.
	_, err = svc.SpecReject(ctx, model.SpecRejectRequest{
		ID:     spec.ID,
		Reason: "second failure",
		By:     "orchestrator",
	})
	if err != nil {
		t.Fatalf("second SpecReject: %v", err)
	}

	status, err := svc.LaneStatus(ctx, spec.ID)
	if err != nil {
		t.Fatalf("LaneStatus: %v", err)
	}
	if status.RejectionCount != 2 {
		t.Errorf("RejectionCount: got %d, want 2", status.RejectionCount)
	}
}

// TestLaneStats_Counts verifies that LaneStats returns correct counts for
// trivial specs, audit failures, overrides, and reclassifications.
func TestLaneStats_Counts(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	// Create 2 trivial specs.
	for i := range 2 {
		spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
			Title: "Trivial " + strings.Repeat("x", i),
			Lane:  model.LaneTrivial,
			Scope: "internal/**",
		})
		if err != nil {
			t.Fatalf("SpecNew trivial %d: %v", i, err)
		}

		spec, err = svc.SpecQuick(ctx, model.SpecQuickRequest{
			ID:        spec.ID,
			Rationale: "quick",
			By:        "orchestrator",
		})
		if err != nil {
			t.Fatalf("SpecQuick %d: %v", i, err)
		}

		// Advance to audit.
		spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "backend"})
		if err != nil {
			t.Fatalf("SpecAdvance to audit %d: %v", i, err)
		}

		// Insert a failing audit record for the first spec only.
		if i == 0 {
			if err := svc.store.InsertLaneAudit(ctx, &model.LaneAuditRecord{
				SpecID:       spec.ID,
				Passed:       false,
				FileCount:    6,
				LinesChanged: 50,
				Breaches:     "too many files",
				BaseSHA:      "sha0",
			}); err != nil {
				t.Fatalf("InsertLaneAudit: %v", err)
			}
		}
	}

	// Create 1 standard spec (should not count in trivial stats).
	_, err := svc.SpecNew(ctx, model.SpecNewRequest{
		Title: "Standard spec",
		Lane:  model.LaneStandard,
	})
	if err != nil {
		t.Fatalf("SpecNew standard: %v", err)
	}

	stats, err := svc.LaneStats(ctx, "project")
	if err != nil {
		t.Fatalf("LaneStats: %v", err)
	}

	if stats.TrivialCount != 2 {
		t.Errorf("TrivialCount: got %d, want 2", stats.TrivialCount)
	}
	if stats.AuditFailCount != 1 {
		t.Errorf("AuditFailCount: got %d, want 1", stats.AuditFailCount)
	}
	if stats.AuditFailRate == 0 {
		t.Error("AuditFailRate must be > 0")
	}
}

// --- SPEC DOC TESTS (SPEC-087 D3) ---

// TestSpecDocPath_ValidatesSpecID attacks specDocPath directly with hostile
// ids — NOT via SpecDocWrite/store.GetSpec (AC5). Going only through the
// service method would let store.GetSpec reject a malformed id as
// "not found" before specDocPath's own checks ever ran, so a test that
// deletes specIDPattern would still pass — a guard that cannot detect its
// own removal (memory testing/antipatron-guardian-que-no-detecta-su-eliminacion).
//
// Mutation guard (manually verified): commenting out the
// `if !specIDPattern.MatchString(id)` block in specDocPath turns every
// "invalid id" case below red (path traversal succeeds instead of erroring).
func TestSpecDocPath_ValidatesSpecID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		kind    model.SpecDocKind
		wantErr bool
	}{
		{"valid id", "SPEC-087", model.SpecDocKindSpec, false},
		{"path traversal via id", "../../../etc/passwd", model.SpecDocKindSpec, true},
		{"embedded traversal segments", "SPEC-001/../../..", model.SpecDocKindSpec, true},
		{"unknown kind", "SPEC-087", model.SpecDocKind("bogus"), true},
		{"empty id", "", model.SpecDocKindSpec, true},
		{"lowercase id rejected", "spec-087", model.SpecDocKindSpec, true},
		{"absolute path id", "/etc/passwd", model.SpecDocKindSpec, true},
		{"path traversal via id, kind budget (AC22)", "../../../etc", model.SpecDocKindBudget, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			path, err := specDocPath(root, "wirvii/mneme", tt.id, tt.kind)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("specDocPath(%q, %q) = %q, want error", tt.id, tt.kind, path)
				}
				return
			}
			if err != nil {
				t.Fatalf("specDocPath(%q, %q): unexpected error: %v", tt.id, tt.kind, err)
			}
			specDir := filepath.Join(root, "wirvii-mneme", "specs", tt.id)
			rel, relErr := filepath.Rel(specDir, path)
			if relErr != nil || strings.Contains(rel, "..") {
				t.Errorf("specDocPath resolved %q outside %q (rel=%q)", path, specDir, rel)
			}
		})
	}
}

// TestSpecDocPath_ProjectSlashSanitised verifies specDocPath sanitises a
// project slug containing "/" the same way config.ProjectWorkflowDir does,
// so the two never disagree on where a spec's workflow directory lives.
func TestSpecDocPath_ProjectSlashSanitised(t *testing.T) {
	root := t.TempDir()
	path, err := specDocPath(root, "wirvii/mneme", "SPEC-087", model.SpecDocKindPlan)
	if err != nil {
		t.Fatalf("specDocPath: %v", err)
	}
	want := filepath.Join(root, "wirvii-mneme", "specs", "SPEC-087", "plan.md")
	if path != want {
		t.Errorf("specDocPath = %q, want %q", path, want)
	}
}

// newTestSDDServiceWithWorkflowDir mirrors newTestSDDService but points
// Workflow.Dir at a fresh t.TempDir() so SpecDocWrite tests never touch the
// real ~/.mneme/workflows directory.
func newTestSDDServiceWithWorkflowDir(t *testing.T, project string) (*SDDService, string) {
	t.Helper()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })

	sddStore := store.NewSDDStore(database)
	cfg := config.Default()
	workflowDir := t.TempDir()
	cfg.Workflow.Dir = workflowDir
	return NewSDDService(sddStore, cfg, project, nil), workflowDir
}

// TestSpecDocWrite_Success writes a qa-report.md for a real spec and
// verifies its content, path, and Created flag.
func TestSpecDocWrite_Success(t *testing.T) {
	svc, workflowDir := newTestSDDServiceWithWorkflowDir(t, "wirvii/mneme")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{
		Title: "Test spec",
		Lane:  model.LaneStandard,
	})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	resp, err := svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{
		ID:      spec.ID,
		Kind:    model.SpecDocKindQAReport,
		Content: "# QA Report\n\nAPROBADO\n",
	})
	if err != nil {
		t.Fatalf("SpecDocWrite: %v", err)
	}

	wantPath := filepath.Join(workflowDir, "wirvii-mneme", "specs", spec.ID, "qa-report.md")
	if resp.Path != wantPath {
		t.Errorf("Path = %q, want %q", resp.Path, wantPath)
	}
	if !resp.Created {
		t.Error("Created = false, want true for a brand-new file")
	}
	if resp.Bytes != len("# QA Report\n\nAPROBADO\n") {
		t.Errorf("Bytes = %d, want %d", resp.Bytes, len("# QA Report\n\nAPROBADO\n"))
	}

	data, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(data) != "# QA Report\n\nAPROBADO\n" {
		t.Errorf("file content = %q, want %q", string(data), "# QA Report\n\nAPROBADO\n")
	}
}

// TestSpecDocWrite_OverwriteReportsNotCreated verifies a second write to the
// same kind reports Created=false and replaces the content.
func TestSpecDocWrite_OverwriteReportsNotCreated(t *testing.T) {
	svc, _ := newTestSDDServiceWithWorkflowDir(t, "wirvii/mneme")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Test spec", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	if _, err := svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{
		ID: spec.ID, Kind: model.SpecDocKindChanges, Content: "first",
	}); err != nil {
		t.Fatalf("first SpecDocWrite: %v", err)
	}

	resp, err := svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{
		ID: spec.ID, Kind: model.SpecDocKindChanges, Content: "second",
	})
	if err != nil {
		t.Fatalf("second SpecDocWrite: %v", err)
	}
	if resp.Created {
		t.Error("Created = true on overwrite, want false")
	}

	data, err := os.ReadFile(resp.Path)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(data) != "second" {
		t.Errorf("file content = %q, want %q (overwrite must replace, not append)", string(data), "second")
	}
}

// TestSpecDocWrite_UnknownSpec verifies a nonexistent spec ID errors out
// before anything is written.
func TestSpecDocWrite_UnknownSpec(t *testing.T) {
	svc, _ := newTestSDDServiceWithWorkflowDir(t, "wirvii/mneme")
	ctx := context.Background()

	_, err := svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{
		ID: "SPEC-999", Kind: model.SpecDocKindSpec, Content: "x",
	})
	if err == nil {
		t.Fatal("expected error for unknown spec ID")
	}
}

// TestSpecDocWrite_UnknownKind verifies an unrecognised kind errors before
// any file is written, for a spec that DOES exist.
func TestSpecDocWrite_UnknownKind(t *testing.T) {
	svc, _ := newTestSDDServiceWithWorkflowDir(t, "wirvii/mneme")
	ctx := context.Background()

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "Test spec", Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	_, err = svc.SpecDocWrite(ctx, model.SpecDocWriteRequest{
		ID: spec.ID, Kind: model.SpecDocKind("bogus"), Content: "x",
	})
	if !errors.Is(err, model.ErrUnknownSpecDocKind) {
		t.Errorf("expected ErrUnknownSpecDocKind, got %v", err)
	}
}
