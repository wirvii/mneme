package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/model"
)

var fixedPast = time.Date(2024, 3, 15, 10, 30, 0, 0, time.UTC)

func TestCreateBacklogItemFromRecord_PreservesGivenIdentityAndDates(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	item := &model.BacklogItem{
		ID:        "BL-050",
		UUID:      "0198f000-0000-7000-8000-00000000aaaa",
		Title:     "hand written",
		Status:    model.BacklogStatusRaw,
		Priority:  model.PriorityMedium,
		Project:   "wirvii-mneme",
		Lane:      model.LaneStandard,
		CreatedAt: fixedPast,
		UpdatedAt: fixedPast,
	}

	if err := s.CreateBacklogItemFromRecord(ctx, item); err != nil {
		t.Fatalf("CreateBacklogItemFromRecord: %v", err)
	}
	if item.UUID != "0198f000-0000-7000-8000-00000000aaaa" {
		t.Errorf("UUID was overwritten: got %s", item.UUID)
	}

	got, err := s.GetBacklogItem(ctx, "BL-050")
	if err != nil {
		t.Fatalf("GetBacklogItem: %v", err)
	}
	if got.UUID != item.UUID {
		t.Errorf("stored UUID = %s, want %s", got.UUID, item.UUID)
	}
	if !got.CreatedAt.Equal(fixedPast) || !got.UpdatedAt.Equal(fixedPast) {
		t.Errorf("dates not preserved verbatim: created=%s updated=%s", got.CreatedAt, got.UpdatedAt)
	}
}

func TestCreateBacklogItemFromRecord_MintsAnchorWhenEmpty(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	item := &model.BacklogItem{
		ID:       "BL-051",
		Title:    "no anchor yet",
		Status:   model.BacklogStatusRaw,
		Priority: model.PriorityMedium,
		Project:  "wirvii-mneme",
		Lane:     model.LaneStandard,
	}
	if err := s.CreateBacklogItemFromRecord(ctx, item); err != nil {
		t.Fatalf("CreateBacklogItemFromRecord: %v", err)
	}
	if item.UUID == "" {
		t.Fatal("CreateBacklogItemFromRecord must mint an anchor when item.UUID is empty")
	}
	if item.CreatedAt.IsZero() || item.UpdatedAt.IsZero() {
		t.Fatal("CreateBacklogItemFromRecord must stamp now() when dates are zero")
	}
}

func TestUpdateBacklogItemFromRecord_LocatesByAnchorAndPreservesDate(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	item := &model.BacklogItem{
		ID: "BL-052", Title: "orig", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: "wirvii-mneme", Lane: model.LaneStandard,
	}
	if err := s.CreateBacklogItem(ctx, item); err != nil {
		t.Fatalf("CreateBacklogItem: %v", err)
	}

	updated := &model.BacklogItem{
		ID: "BL-052", UUID: item.UUID, Title: "from repo", Status: model.BacklogStatusRefined,
		Priority: model.PriorityHigh, Project: "wirvii-mneme", Lane: model.LaneStandard,
		UpdatedAt: fixedPast,
	}
	if err := s.UpdateBacklogItemFromRecord(ctx, updated); err != nil {
		t.Fatalf("UpdateBacklogItemFromRecord: %v", err)
	}

	got, err := s.GetBacklogItem(ctx, "BL-052")
	if err != nil {
		t.Fatalf("GetBacklogItem: %v", err)
	}
	if got.Title != "from repo" || got.Status != model.BacklogStatusRefined {
		t.Errorf("update did not apply: %+v", got)
	}
	if !got.UpdatedAt.Equal(fixedPast) {
		t.Errorf("updated_at = %s, want verbatim %s (never stamped to now)", got.UpdatedAt, fixedPast)
	}
	if got.ID != "BL-052" {
		t.Errorf("id must never change: got %s", got.ID)
	}
}

func TestUpdateBacklogItemFromRecord_UnknownAnchorReturnsNotFound(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	item := &model.BacklogItem{
		ID: "BL-999", UUID: "0198f000-0000-7000-8000-00000000dead",
		Title: "ghost", Status: model.BacklogStatusRaw, Priority: model.PriorityMedium,
		Project: "wirvii-mneme", Lane: model.LaneStandard, UpdatedAt: fixedPast,
	}
	err := s.UpdateBacklogItemFromRecord(ctx, item)
	if !errors.Is(err, model.ErrBacklogNotFound) {
		t.Errorf("err = %v, want ErrBacklogNotFound", err)
	}
}

func TestMergeBacklogRefinements_InsertsAndUpdatesNeverDeletes(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	item := &model.BacklogItem{
		ID: "BL-060", Title: "x", Status: model.BacklogStatusRefined,
		Priority: model.PriorityMedium, Project: "wirvii-mneme", Lane: model.LaneStandard,
	}
	if err := s.CreateBacklogItem(ctx, item); err != nil {
		t.Fatalf("CreateBacklogItem: %v", err)
	}

	// Local base already has seq 1, 2, 3.
	for seq := 1; seq <= 3; seq++ {
		if _, err := s.AppendBacklogRefinement(ctx, "BL-060", model.BacklogStatusRefined, model.BacklogStatusRefined,
			"local body", "orchestrator"); err != nil {
			t.Fatalf("AppendBacklogRefinement seq %d: %v", seq, err)
		}
	}

	// The file only brings seq 1 and 2, with seq 2's body changed.
	incoming := []*model.BacklogRefinement{
		{ItemID: "BL-060", Seq: 1, Body: "local body", By: "orchestrator", At: fixedPast},
		{ItemID: "BL-060", Seq: 2, Body: "changed from repo", By: "architect", At: fixedPast},
	}
	if err := s.MergeBacklogRefinements(ctx, "BL-060", incoming); err != nil {
		t.Fatalf("MergeBacklogRefinements: %v", err)
	}

	refs, err := s.ListBacklogRefinements(ctx, "BL-060")
	if err != nil {
		t.Fatalf("ListBacklogRefinements: %v", err)
	}
	if len(refs) != 3 {
		t.Fatalf("len(refs) = %d, want 3 (seq 3 must survive — never deleted)", len(refs))
	}
	if refs[1].Body != "changed from repo" || refs[1].By != "architect" {
		t.Errorf("seq 2 not updated: %+v", refs[1])
	}
	if refs[2].Body != "local body" {
		t.Errorf("seq 3 must be untouched: %+v", refs[2])
	}
}

func TestMergeBacklogRefinements_RerunIsIdempotent(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	item := &model.BacklogItem{
		ID: "BL-061", Title: "x", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: "wirvii-mneme", Lane: model.LaneStandard,
	}
	if err := s.CreateBacklogItem(ctx, item); err != nil {
		t.Fatalf("CreateBacklogItem: %v", err)
	}

	incoming := []*model.BacklogRefinement{
		{ItemID: "BL-061", Seq: 1, Body: "b", By: "x", At: fixedPast},
	}
	if err := s.MergeBacklogRefinements(ctx, "BL-061", incoming); err != nil {
		t.Fatalf("MergeBacklogRefinements (1st): %v", err)
	}
	if err := s.MergeBacklogRefinements(ctx, "BL-061", incoming); err != nil {
		t.Fatalf("MergeBacklogRefinements (2nd): %v", err)
	}

	refs, err := s.ListBacklogRefinements(ctx, "BL-061")
	if err != nil {
		t.Fatalf("ListBacklogRefinements: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("len(refs) = %d, want 1 — rerunning must not duplicate", len(refs))
	}
}

func TestCreateSpecFromRecord_PreservesGivenIdentityAndDates(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	spec := &model.Spec{
		ID: "SPEC-050", UUID: "0198f000-0000-7000-8000-00000000bbbb",
		Title: "hand written", Status: model.SpecStatusDraft, Project: "wirvii-mneme",
		Lane: model.LaneStandard, CreatedAt: fixedPast, UpdatedAt: fixedPast,
	}
	if err := s.CreateSpecFromRecord(ctx, spec); err != nil {
		t.Fatalf("CreateSpecFromRecord: %v", err)
	}

	got, err := s.GetSpec(ctx, "SPEC-050")
	if err != nil {
		t.Fatalf("GetSpec: %v", err)
	}
	if got.UUID != spec.UUID {
		t.Errorf("stored UUID = %s, want %s", got.UUID, spec.UUID)
	}
	if !got.CreatedAt.Equal(fixedPast) || !got.UpdatedAt.Equal(fixedPast) {
		t.Errorf("dates not preserved verbatim: created=%s updated=%s", got.CreatedAt, got.UpdatedAt)
	}
}

func TestUpdateSpecFromRecord_NoOptimisticLockNoHistoryWrite(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	spec := &model.Spec{
		ID: "SPEC-051", Title: "orig", Status: model.SpecStatusDraft,
		Project: "wirvii-mneme", Lane: model.LaneStandard,
	}
	if err := s.CreateSpec(ctx, spec); err != nil {
		t.Fatalf("CreateSpec: %v", err)
	}

	// Jump directly from draft to implementing — a transition
	// UpdateSpecStatus's optimistic lock would never allow without the
	// intermediate states. UpdateSpecFromRecord must not care.
	updated := &model.Spec{
		ID: "SPEC-051", UUID: spec.UUID, Title: "from repo", Status: model.SpecStatusImplementing,
		Project: "wirvii-mneme", Lane: model.LaneStandard, UpdatedAt: fixedPast,
	}
	if err := s.UpdateSpecFromRecord(ctx, updated); err != nil {
		t.Fatalf("UpdateSpecFromRecord: %v", err)
	}

	got, err := s.GetSpec(ctx, "SPEC-051")
	if err != nil {
		t.Fatalf("GetSpec: %v", err)
	}
	if got.Status != model.SpecStatusImplementing {
		t.Errorf("status = %s, want implementing (no optimistic lock)", got.Status)
	}
	if !got.UpdatedAt.Equal(fixedPast) {
		t.Errorf("updated_at = %s, want verbatim %s", got.UpdatedAt, fixedPast)
	}

	history, err := s.GetSpecHistory(ctx, "SPEC-051")
	if err != nil {
		t.Fatalf("GetSpecHistory: %v", err)
	}
	if len(history) != 0 {
		t.Errorf("GetSpecHistory = %d rows, want 0 — UpdateSpecFromRecord must never synthesize history", len(history))
	}
}

func TestUpdateSpecFromRecord_UnknownAnchorReturnsNotFound(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	spec := &model.Spec{
		ID: "SPEC-999", UUID: "0198f000-0000-7000-8000-00000000dead",
		Title: "ghost", Status: model.SpecStatusDraft, Project: "wirvii-mneme",
		Lane: model.LaneStandard, UpdatedAt: fixedPast,
	}
	err := s.UpdateSpecFromRecord(ctx, spec)
	if !errors.Is(err, model.ErrSpecNotFound) {
		t.Errorf("err = %v, want ErrSpecNotFound", err)
	}
}

func TestMergeSpecHistory_InsertsMissingNeverUpdatesExisting(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	spec := &model.Spec{
		ID: "SPEC-060", Title: "x", Status: model.SpecStatusDraft,
		Project: "wirvii-mneme", Lane: model.LaneStandard,
	}
	if err := s.CreateSpec(ctx, spec); err != nil {
		t.Fatalf("CreateSpec: %v", err)
	}
	if err := s.UpdateSpecStatus(ctx, "SPEC-060", model.SpecStatusDraft, model.SpecStatusSpeccing, "arch", "start"); err != nil {
		t.Fatalf("UpdateSpecStatus: %v", err)
	}

	existing, err := s.GetSpecHistory(ctx, "SPEC-060")
	if err != nil {
		t.Fatalf("GetSpecHistory: %v", err)
	}
	if len(existing) != 1 {
		t.Fatalf("setup: expected 1 history row, got %d", len(existing))
	}
	originalID := existing[0].ID

	// The file brings the SAME row (mutated reason, must be ignored — history
	// is immutable) plus a genuinely new one.
	incoming := []*model.SpecHistory{
		{ID: originalID, SpecID: "SPEC-060", FromStatus: model.SpecStatusDraft, ToStatus: model.SpecStatusSpeccing,
			By: "someone-else", Reason: "tampered", At: fixedPast},
		{ID: "0198f000-0000-7000-8000-00000000cccc", SpecID: "SPEC-060",
			FromStatus: model.SpecStatusSpeccing, ToStatus: model.SpecStatusSpecced,
			By: "arch", Reason: "from repo", At: fixedPast},
	}
	if err := s.MergeSpecHistory(ctx, "SPEC-060", incoming); err != nil {
		t.Fatalf("MergeSpecHistory: %v", err)
	}

	got, err := s.GetSpecHistory(ctx, "SPEC-060")
	if err != nil {
		t.Fatalf("GetSpecHistory: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(history) = %d, want 2", len(got))
	}
	for _, h := range got {
		if h.ID == originalID && h.By != "arch" {
			t.Errorf("existing history row was mutated: By=%s, want unchanged 'arch'", h.By)
		}
	}
}

func TestMergeSpecHistory_RerunIsIdempotent(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	spec := &model.Spec{
		ID: "SPEC-061", Title: "x", Status: model.SpecStatusDraft,
		Project: "wirvii-mneme", Lane: model.LaneStandard,
	}
	if err := s.CreateSpec(ctx, spec); err != nil {
		t.Fatalf("CreateSpec: %v", err)
	}

	incoming := []*model.SpecHistory{
		{ID: "0198f000-0000-7000-8000-00000000eeee", SpecID: "SPEC-061",
			FromStatus: "", ToStatus: model.SpecStatusDraft, By: "system", Reason: "created", At: fixedPast},
	}
	if err := s.MergeSpecHistory(ctx, "SPEC-061", incoming); err != nil {
		t.Fatalf("MergeSpecHistory (1st): %v", err)
	}
	if err := s.MergeSpecHistory(ctx, "SPEC-061", incoming); err != nil {
		t.Fatalf("MergeSpecHistory (2nd): %v", err)
	}

	got, err := s.GetSpecHistory(ctx, "SPEC-061")
	if err != nil {
		t.Fatalf("GetSpecHistory: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(history) = %d, want 1 — rerunning must not duplicate", len(got))
	}
}

func TestMergeSpecPushbacks_InsertsAndResolvesExisting(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	spec := &model.Spec{
		ID: "SPEC-070", Title: "x", Status: model.SpecStatusSpeccing,
		Project: "wirvii-mneme", Lane: model.LaneStandard,
	}
	if err := s.CreateSpec(ctx, spec); err != nil {
		t.Fatalf("CreateSpec: %v", err)
	}
	pb := &model.SpecPushback{SpecID: "SPEC-070", FromAgent: "architect", Questions: []string{"q1"}}
	if err := s.CreatePushback(ctx, pb); err != nil {
		t.Fatalf("CreatePushback: %v", err)
	}

	resolvedAt := fixedPast
	incoming := []*model.SpecPushback{
		{ID: pb.ID, SpecID: "SPEC-070", FromAgent: "architect", Questions: []string{"q1"},
			Resolved: true, Resolution: "answered", CreatedAt: pb.CreatedAt, ResolvedAt: &resolvedAt},
		{ID: "0198f000-0000-7000-8000-00000000ffff", SpecID: "SPEC-070", FromAgent: "backend",
			Questions: []string{"q2"}, Resolved: false, CreatedAt: fixedPast},
	}
	if err := s.MergeSpecPushbacks(ctx, "SPEC-070", incoming); err != nil {
		t.Fatalf("MergeSpecPushbacks: %v", err)
	}

	got, err := s.GetAllPushbacks(ctx, "SPEC-070")
	if err != nil {
		t.Fatalf("GetAllPushbacks: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(pushbacks) = %d, want 2", len(got))
	}
	var resolved, unresolved *model.SpecPushback
	for _, p := range got {
		if p.ID == pb.ID {
			resolved = p
		} else {
			unresolved = p
		}
	}
	if resolved == nil || !resolved.Resolved || resolved.Resolution != "answered" {
		t.Errorf("existing pushback not resolved: %+v", resolved)
	}
	if unresolved == nil || unresolved.Resolved {
		t.Errorf("new pushback should stay unresolved: %+v", unresolved)
	}
}

func TestMergeSpecPushbacks_RerunIsIdempotent(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	spec := &model.Spec{
		ID: "SPEC-071", Title: "x", Status: model.SpecStatusSpeccing,
		Project: "wirvii-mneme", Lane: model.LaneStandard,
	}
	if err := s.CreateSpec(ctx, spec); err != nil {
		t.Fatalf("CreateSpec: %v", err)
	}

	incoming := []*model.SpecPushback{
		{ID: "0198f000-0000-7000-8000-000000001111", SpecID: "SPEC-071", FromAgent: "architect",
			Questions: []string{"q1"}, Resolved: false, CreatedAt: fixedPast},
	}
	if err := s.MergeSpecPushbacks(ctx, "SPEC-071", incoming); err != nil {
		t.Fatalf("MergeSpecPushbacks (1st): %v", err)
	}
	if err := s.MergeSpecPushbacks(ctx, "SPEC-071", incoming); err != nil {
		t.Fatalf("MergeSpecPushbacks (2nd): %v", err)
	}

	got, err := s.GetAllPushbacks(ctx, "SPEC-071")
	if err != nil {
		t.Fatalf("GetAllPushbacks: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(pushbacks) = %d, want 1 — rerunning must not duplicate", len(got))
	}
}

func TestMarshalPreviousIDs_RoundTripsThroughUnmarshal(t *testing.T) {
	ids := []model.PreviousID{
		{ID: "BL-050", Origin: "local", Reason: "enable-collision", At: fixedPast},
	}
	raw, err := marshalPreviousIDs(ids)
	if err != nil {
		t.Fatalf("marshalPreviousIDs: %v", err)
	}
	got := unmarshalPreviousIDs(raw)
	if len(got) != 1 || got[0].ID != "BL-050" || got[0].Origin != "local" {
		t.Errorf("round trip mismatch: %+v", got)
	}
}

func TestMarshalPreviousIDs_EmptyIsEmptyString(t *testing.T) {
	raw, err := marshalPreviousIDs(nil)
	if err != nil {
		t.Fatalf("marshalPreviousIDs: %v", err)
	}
	if raw != "" {
		t.Errorf("marshalPreviousIDs(nil) = %q, want \"\" (matches column default)", raw)
	}
}
