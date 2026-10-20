package store

import (
	"context"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/model"
)

// rawReviewed reads memories.reviewed_at and updated_at/revision_count as
// stored, bypassing every store method under test.
func rawReviewed(t *testing.T, s *MemoryStore, id string) (reviewed *string, updated string, revision int) {
	t.Helper()
	err := s.db.QueryRow(`SELECT reviewed_at, updated_at, revision_count FROM memories WHERE id = ?`, id).
		Scan(&reviewed, &updated, &revision)
	if err != nil {
		t.Fatalf("raw select: %v", err)
	}
	return reviewed, updated, revision
}

// TestMemoryStore_ReviewedAtLifecycle covers SPEC-167 AC3 / D3.
func TestMemoryStore_ReviewedAtLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m, err := s.Create(ctx, &model.Memory{
		Type: model.TypeDiscovery, Scope: model.ScopeProject, Title: "T", Content: "C", Project: "p",
		TopicKey: "k/one", Importance: 0.5, Confidence: 0.8, DecayRate: 0.01,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	reviewed, _, _ := rawReviewed(t, s, m.ID)
	if reviewed == nil || *reviewed != m.CreatedAt.Format(time.RFC3339Nano) {
		t.Fatalf("Create: reviewed_at = %v, want created_at %s", reviewed, m.CreatedAt.Format(time.RFC3339Nano))
	}

	// Backdate so every later "moved to now" assertion is unambiguous.
	old := time.Now().UTC().Add(-90 * 24 * time.Hour)
	oldStr := old.Format(time.RFC3339Nano)
	if err := s.MarkReviewed(ctx, m.ID, old); err != nil {
		t.Fatalf("MarkReviewed: %v", err)
	}

	// Importance-only update must not move it.
	imp := 0.9
	if err := s.Update(ctx, m.ID, &model.UpdateRequest{Importance: &imp}); err != nil {
		t.Fatalf("Update importance: %v", err)
	}
	if got, _, _ := rawReviewed(t, s, m.ID); got == nil || *got != oldStr {
		t.Errorf("importance-only update moved reviewed_at: %v", got)
	}

	// Content update moves it to now.
	content := "new content"
	if err := s.Update(ctx, m.ID, &model.UpdateRequest{Content: &content}); err != nil {
		t.Fatalf("Update content: %v", err)
	}
	got, _, _ := rawReviewed(t, s, m.ID)
	if got == nil || !parsedAfter(t, *got, old) {
		t.Errorf("content update did not move reviewed_at: %v", got)
	}

	// MarkReviewed sets it without touching updated_at or revision_count.
	_, updatedBefore, revBefore := rawReviewed(t, s, m.ID)
	if err := s.MarkReviewed(ctx, m.ID, old); err != nil {
		t.Fatalf("MarkReviewed: %v", err)
	}
	got, updatedAfter, revAfter := rawReviewed(t, s, m.ID)
	if got == nil || *got != oldStr || updatedAfter != updatedBefore || revAfter != revBefore {
		t.Errorf("MarkReviewed side effects: reviewed=%v updated %s->%s revision %d->%d", got, updatedBefore, updatedAfter, revBefore, revAfter)
	}
	if err := s.MarkReviewed(ctx, "missing", old); err == nil {
		t.Error("MarkReviewed on a missing id must fail")
	}

	// Upsert by topic_key moves it to now.
	if _, created, err := s.Upsert(ctx, &model.Memory{
		Type: model.TypeDiscovery, Scope: model.ScopeProject, Title: "T2", Content: "C2", Project: "p",
		TopicKey: "k/one", Importance: 0.5, Confidence: 0.8, DecayRate: 0.01,
	}); err != nil || created {
		t.Fatalf("Upsert: created=%v err=%v", created, err)
	}
	if got, _, _ := rawReviewed(t, s, m.ID); got == nil || !parsedAfter(t, *got, old) {
		t.Errorf("upsert did not move reviewed_at: %v", got)
	}

	// ReviewDates falls back to updated_at when reviewed_at is NULL.
	if _, err := s.db.Exec(`UPDATE memories SET reviewed_at = NULL WHERE id = ?`, m.ID); err != nil {
		t.Fatalf("null reviewed_at: %v", err)
	}
	_, updatedRaw, _ := rawReviewed(t, s, m.ID)
	dates, err := s.ReviewDates(ctx, []string{m.ID, "missing"})
	if err != nil {
		t.Fatalf("ReviewDates: %v", err)
	}
	want, err := parseTime(updatedRaw)
	if err != nil {
		t.Fatalf("parse updated_at: %v", err)
	}
	if !dates[m.ID].Equal(want) {
		t.Errorf("ReviewDates = %v, want updated_at %v", dates[m.ID], want)
	}
	if _, ok := dates["missing"]; ok {
		t.Error("missing id must be absent from the map")
	}
}

func parsedAfter(t *testing.T, raw string, than time.Time) bool {
	t.Helper()
	got, err := parseTime(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return got.After(than)
}

// TestMemoryStore_ReviewDatesChunks makes sure more ids than one IN clause
// holds are all returned.
func TestMemoryStore_ReviewDatesChunks(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	var ids []string
	for i := 0; i < reviewDatesChunk+5; i++ {
		m, err := s.Create(ctx, &model.Memory{
			Type: model.TypeDiscovery, Scope: model.ScopeProject, Title: "T", Content: "C", Project: "p",
			Importance: 0.5, Confidence: 0.8, DecayRate: 0.01,
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		ids = append(ids, m.ID)
	}
	dates, err := s.ReviewDates(ctx, ids)
	if err != nil {
		t.Fatalf("ReviewDates: %v", err)
	}
	if len(dates) != len(ids) {
		t.Errorf("got %d dates, want %d", len(dates), len(ids))
	}
}
