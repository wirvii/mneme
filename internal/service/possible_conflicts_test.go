package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/wirvii/mneme/internal/model"
)

// saveRetryMemory saves a memory about retry backoff policy; every one of them
// shares the same salient terms so FTS5 proposes them to each other.
func saveRetryMemory(t *testing.T, svc interface {
	Save(context.Context, model.SaveRequest) (*model.SaveResponse, error)
}, n int, typ model.MemoryType) *model.SaveResponse {
	t.Helper()
	resp, err := svc.Save(context.Background(), model.SaveRequest{
		Title:   fmt.Sprintf("Exponential backoff retry policy number %d", n),
		Content: "The gateway client retries failed requests using exponential backoff with jitter and a retry budget.",
		Type:    typ,
	})
	if err != nil {
		t.Fatalf("Save #%d: %v", n, err)
	}
	return resp
}

// TestSave_PossibleConflicts is SPEC-166 AC1.
func TestSave_PossibleConflicts(t *testing.T) {
	t.Run("capped at three and never self", func(t *testing.T) {
		svc := newTestService(t)
		var ids []string
		for i := 1; i <= 4; i++ {
			ids = append(ids, saveRetryMemory(t, svc, i, model.TypeDecision).ID)
		}
		resp := saveRetryMemory(t, svc, 5, model.TypeDecision)
		if len(resp.PossibleConflicts) != 3 {
			t.Fatalf("PossibleConflicts = %d, want 3", len(resp.PossibleConflicts))
		}
		known := map[string]bool{}
		for _, id := range ids {
			known[id] = true
		}
		for _, pc := range resp.PossibleConflicts {
			if pc.ID == resp.ID {
				t.Error("must not include the memory itself")
			}
			if !known[pc.ID] || pc.Title == "" || pc.Type != model.TypeDecision || pc.UpdatedAt.IsZero() {
				t.Errorf("unexpected candidate %+v", pc)
			}
		}
	})

	t.Run("excludes superseded and deleted", func(t *testing.T) {
		svc := newTestService(t)
		ctx := context.Background()
		old := saveRetryMemory(t, svc, 1, model.TypeDecision)
		gone := saveRetryMemory(t, svc, 2, model.TypeDecision)
		keep := saveRetryMemory(t, svc, 3, model.TypeDecision)
		winner := saveRetryMemory(t, svc, 4, model.TypeDecision)
		if err := svc.ConflictLink(ctx, winner.ID, old.ID, "supersedes", ""); err != nil {
			t.Fatalf("ConflictLink: %v", err)
		}
		if err := svc.Forget(ctx, gone.ID, ""); err != nil {
			t.Fatalf("Forget: %v", err)
		}
		resp := saveRetryMemory(t, svc, 5, model.TypeDecision)
		for _, pc := range resp.PossibleConflicts {
			if pc.ID == old.ID {
				t.Error("superseded memory proposed")
			}
		}
		found := false
		for _, pc := range resp.PossibleConflicts {
			if pc.ID == keep.ID || pc.ID == winner.ID {
				found = true
			}
		}
		if !found {
			t.Errorf("expected live candidates, got %+v", resp.PossibleConflicts)
		}
	})

	t.Run("ephemeral types neither get nor are candidates", func(t *testing.T) {
		svc := newTestService(t)
		saveRetryMemory(t, svc, 1, model.TypeSessionSummary)
		resp := saveRetryMemory(t, svc, 2, model.TypeDecision)
		if len(resp.PossibleConflicts) != 0 {
			t.Errorf("session_summary proposed as candidate: %+v", resp.PossibleConflicts)
		}
		saveRetryMemory(t, svc, 3, model.TypeDecision)
		summary := saveRetryMemory(t, svc, 4, model.TypeSessionSummary)
		if len(summary.PossibleConflicts) != 0 {
			t.Errorf("session_summary must get no candidates: %+v", summary.PossibleConflicts)
		}
	})
}

// TestSave_PossibleConflicts_DeterministicAndNegativeCache is SPEC-166 AC2.
func TestSave_PossibleConflicts_DeterministicAndNegativeCache(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		saveRetryMemory(t, svc, i, model.TypeDecision)
	}

	// Same topic_key makes the second save an upsert of the same row, so the two
	// calls see an identical base.
	req := model.SaveRequest{
		Title:    "Exponential backoff retry policy final",
		Content:  "The gateway client retries failed requests using exponential backoff with jitter and a retry budget.",
		TopicKey: "net/retry",
	}
	first, err := svc.Save(ctx, req)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	second, err := svc.Save(ctx, req)
	if err != nil {
		t.Fatalf("Save again: %v", err)
	}
	if len(first.PossibleConflicts) == 0 {
		t.Fatal("expected candidates")
	}
	if len(first.PossibleConflicts) != len(second.PossibleConflicts) {
		t.Fatalf("not deterministic: %v vs %v", first.PossibleConflicts, second.PossibleConflicts)
	}
	for i := range first.PossibleConflicts {
		if first.PossibleConflicts[i].ID != second.PossibleConflicts[i].ID {
			t.Errorf("order differs at %d", i)
		}
	}

	// No shared terms -> nothing.
	none, err := svc.Save(ctx, model.SaveRequest{Title: "Zebra migration", Content: "Quagga herds cross savannah rivers."})
	if err != nil {
		t.Fatalf("Save unrelated: %v", err)
	}
	if len(none.PossibleConflicts) != 0 {
		t.Errorf("expected none, got %+v", none.PossibleConflicts)
	}

	// A pair already judged unrelated is no longer proposed.
	judged := second.PossibleConflicts[0].ID
	if err := svc.ConflictLink(ctx, first.ID, judged, "unrelated", ""); err != nil {
		t.Fatalf("ConflictLink: %v", err)
	}
	third, err := svc.Save(ctx, req)
	if err != nil {
		t.Fatalf("Save third: %v", err)
	}
	for _, pc := range third.PossibleConflicts {
		if pc.ID == judged {
			t.Error("judged-unrelated pair proposed again")
		}
	}
}

// TestUpdate_SupersedesIsReversible is SPEC-166 AC3.
func TestUpdate_SupersedesIsReversible(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	old := saveRetryMemory(t, svc, 1, model.TypeDecision)
	cur := saveRetryMemory(t, svc, 2, model.TypeDecision)

	resp, err := svc.Update(ctx, cur.ID, model.UpdateRequest{Supersedes: []string{old.ID}})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(resp.RelationsApplied) != 1 || resp.RelationsApplied[0] != (model.AppliedRelation{TargetID: old.ID, Relation: "supersedes"}) {
		t.Fatalf("RelationsApplied = %+v", resp.RelationsApplied)
	}

	contains := func(include bool) bool {
		sr, err := svc.Search(ctx, model.SearchRequest{Query: "exponential backoff retry", Limit: 20, IncludeSuperseded: include})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		for _, r := range sr.Results {
			if r.ID == old.ID {
				return true
			}
		}
		return false
	}
	if contains(false) {
		t.Error("superseded memory still in default search")
	}
	if !contains(true) {
		t.Error("superseded memory must still exist (include_superseded)")
	}

	if err := svc.ConflictUnlink(ctx, cur.ID, old.ID); err != nil {
		t.Fatalf("ConflictUnlink: %v", err)
	}
	if !contains(false) {
		t.Error("unlink must bring the memory back to search")
	}
}

// TestUpdate_RelationsJudgedByAgent is SPEC-166 AC4.
func TestUpdate_RelationsJudgedByAgent(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	cur := saveRetryMemory(t, svc, 1, model.TypeDecision)
	a := saveRetryMemory(t, svc, 2, model.TypeDecision)
	b := saveRetryMemory(t, svc, 3, model.TypeDecision)
	c := saveRetryMemory(t, svc, 4, model.TypeDecision)
	d := saveRetryMemory(t, svc, 5, model.TypeDecision)

	if _, err := svc.Update(ctx, cur.ID, model.UpdateRequest{ConflictsWith: []string{a.ID}, UnrelatedTo: []string{b.ID}}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := svc.ConflictLink(ctx, c.ID, d.ID, "conflicts_with", ""); err != nil {
		t.Fatalf("ConflictLink: %v", err)
	}

	list, err := svc.ConflictList(ctx, "", 50)
	if err != nil {
		t.Fatalf("ConflictList: %v", err)
	}
	judged := map[string]string{} // relation -> judged_by for the pairs involving cur
	manual := ""
	for _, r := range list.Relations {
		if r.FromID == cur.ID || r.ToID == cur.ID {
			judged[r.Relation] = r.JudgedBy
		} else {
			manual = r.JudgedBy
		}
	}
	if judged["conflicts_with"] != "agent" || judged["unrelated"] != "agent" {
		t.Errorf("judged_by for agent relations = %v, want agent", judged)
	}
	if manual != "manual" {
		t.Errorf("ConflictLink judged_by = %q, want manual", manual)
	}
}

// TestUpdate_RelationValidationWritesNothing is SPEC-166 AC5.
func TestUpdate_RelationValidationWritesNothing(t *testing.T) {
	tests := []struct {
		name    string
		build   func(cur, other, global string) model.UpdateRequest
		wantErr error
	}{
		{"self", func(cur, _, _ string) model.UpdateRequest { return model.UpdateRequest{Supersedes: []string{cur}} }, model.ErrInvalidRelation},
		{"empty id", func(_, _, _ string) model.UpdateRequest { return model.UpdateRequest{Supersedes: []string{""}} }, model.ErrInvalidRelation},
		{"two lists", func(_, other, _ string) model.UpdateRequest {
			return model.UpdateRequest{Supersedes: []string{other}, ConflictsWith: []string{other}}
		}, model.ErrInvalidRelation},
		{"missing", func(_, _, _ string) model.UpdateRequest {
			return model.UpdateRequest{ConflictsWith: []string{"does-not-exist"}}
		}, model.ErrNotFound},
		{"cross store", func(_, _, global string) model.UpdateRequest {
			return model.UpdateRequest{UnrelatedTo: []string{global}}
		}, model.ErrCrossStoreRelation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestService(t)
			ctx := context.Background()
			cur := saveRetryMemory(t, svc, 1, model.TypeDecision)
			other := saveRetryMemory(t, svc, 2, model.TypeDecision)
			glob, err := svc.Save(ctx, model.SaveRequest{
				Title: "Global retry guideline", Content: "Always use exponential backoff.", Scope: model.ScopeGlobal,
			})
			if err != nil {
				t.Fatalf("Save global: %v", err)
			}

			req := tt.build(cur.ID, other.ID, glob.ID)
			newTitle := "Changed title"
			req.Title = &newTitle
			_, err = svc.Update(ctx, cur.ID, req)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}

			after, err := svc.Get(ctx, cur.ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if after.Title == newTitle || after.RevisionCount != cur.RevisionCount {
				t.Errorf("fields changed despite the error: title=%q revision=%d", after.Title, after.RevisionCount)
			}
			list, err := svc.ConflictList(ctx, "", 50)
			if err != nil {
				t.Fatalf("ConflictList: %v", err)
			}
			if len(list.Relations) != 0 {
				t.Errorf("relations written: %+v", list.Relations)
			}
			o, err := svc.Get(ctx, other.ID)
			if err != nil {
				t.Fatalf("Get other: %v", err)
			}
			if o.SupersededBy != "" {
				t.Error("superseded_by written")
			}
		})
	}
}

// TestUpdate_RelationsOnlyIsNotARevision is SPEC-166 AC6.
func TestUpdate_RelationsOnlyIsNotARevision(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	cur := saveRetryMemory(t, svc, 1, model.TypeDecision)
	other := saveRetryMemory(t, svc, 2, model.TypeDecision)

	before, err := svc.Get(ctx, cur.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp, err := svc.Update(ctx, cur.ID, model.UpdateRequest{UnrelatedTo: []string{other.ID}})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	after, err := svc.Get(ctx, cur.ID)
	if err != nil {
		t.Fatalf("Get after: %v", err)
	}
	if resp.RevisionCount != before.RevisionCount || after.RevisionCount != before.RevisionCount {
		t.Errorf("revision_count changed: %d -> %d", before.RevisionCount, after.RevisionCount)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("updated_at changed: %v -> %v", before.UpdatedAt, after.UpdatedAt)
	}

	title := "Retitled and judged"
	resp, err = svc.Update(ctx, cur.ID, model.UpdateRequest{Title: &title, ConflictsWith: []string{other.ID}})
	if err != nil {
		t.Fatalf("combined Update: %v", err)
	}
	// The pair was already judged unrelated; the new judgement replaces it.
	if resp.Title != title || len(resp.RelationsApplied) != 1 || resp.RevisionCount <= before.RevisionCount {
		t.Errorf("combined update not applied: %+v", resp)
	}
}
