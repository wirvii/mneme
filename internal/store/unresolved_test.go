package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/juanftp/mneme/internal/model"
)

// makeUnresolved returns a test UnresolvedReference pointing from sourceID
// to the given target topic key within the given project.
func makeUnresolved(sourceID, targetTopicKey, project string) *model.UnresolvedReference {
	return &model.UnresolvedReference{
		SourceMemoryID: sourceID,
		TargetTopicKey: targetTopicKey,
		Project:        project,
	}
}

// createTestMemory is a helper that creates a memory and returns its ID.
func createTestMemory(t *testing.T, s *MemoryStore, title, project string) string {
	t.Helper()
	m := &model.Memory{
		Type:       model.TypeDecision,
		Scope:      model.ScopeProject,
		Title:      title,
		Content:    "Test content for " + title,
		Project:    project,
		Importance: 0.5,
		Confidence: 0.9,
		DecayRate:  0.01,
	}
	created, err := s.Create(context.Background(), m)
	if err != nil {
		t.Fatalf("createTestMemory %q: %v", title, err)
	}
	return created.ID
}

// TestRegisterUnresolved_Insert verifies that the first call creates a row
// with mention_count=1.
func TestRegisterUnresolved_Insert(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	srcID := createTestMemory(t, s, "Source", "proj")
	ref := makeUnresolved(srcID, "missing/topic", "proj")

	if err := s.RegisterUnresolved(ctx, ref); err != nil {
		t.Fatalf("RegisterUnresolved: %v", err)
	}

	refs, err := s.FindUnresolvedByTarget(ctx, "missing/topic", "proj")
	if err != nil {
		t.Fatalf("FindUnresolvedByTarget: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].MentionCount != 1 {
		t.Errorf("mention_count = %d, want 1", refs[0].MentionCount)
	}
	if refs[0].SourceMemoryID != srcID {
		t.Errorf("source_memory_id = %q, want %q", refs[0].SourceMemoryID, srcID)
	}
	if refs[0].TargetTopicKey != "missing/topic" {
		t.Errorf("target_topic_key = %q, want %q", refs[0].TargetTopicKey, "missing/topic")
	}
}

// TestRegisterUnresolved_Upsert verifies that a second call to RegisterUnresolved
// with the same (source, target) increments mention_count to 2 and does not
// create a second row.
func TestRegisterUnresolved_Upsert(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	srcID := createTestMemory(t, s, "Source", "proj")
	ref := makeUnresolved(srcID, "missing/topic", "proj")

	if err := s.RegisterUnresolved(ctx, ref); err != nil {
		t.Fatalf("first RegisterUnresolved: %v", err)
	}
	if err := s.RegisterUnresolved(ctx, ref); err != nil {
		t.Fatalf("second RegisterUnresolved: %v", err)
	}

	refs, err := s.FindUnresolvedByTarget(ctx, "missing/topic", "proj")
	if err != nil {
		t.Fatalf("FindUnresolvedByTarget: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 row (UPSERT), got %d", len(refs))
	}
	if refs[0].MentionCount != 2 {
		t.Errorf("mention_count after upsert = %d, want 2", refs[0].MentionCount)
	}
}

// TestFindUnresolvedByTarget_Found verifies that FindUnresolvedByTarget returns
// matching rows for the correct (topicKey, project) pair.
func TestFindUnresolvedByTarget_Found(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	src1 := createTestMemory(t, s, "Src1", "proj")
	src2 := createTestMemory(t, s, "Src2", "proj")

	if err := s.RegisterUnresolved(ctx, makeUnresolved(src1, "gap/topic", "proj")); err != nil {
		t.Fatalf("RegisterUnresolved src1: %v", err)
	}
	if err := s.RegisterUnresolved(ctx, makeUnresolved(src2, "gap/topic", "proj")); err != nil {
		t.Fatalf("RegisterUnresolved src2: %v", err)
	}
	// Different target — should not appear in results.
	if err := s.RegisterUnresolved(ctx, makeUnresolved(src1, "other/topic", "proj")); err != nil {
		t.Fatalf("RegisterUnresolved other: %v", err)
	}

	refs, err := s.FindUnresolvedByTarget(ctx, "gap/topic", "proj")
	if err != nil {
		t.Fatalf("FindUnresolvedByTarget: %v", err)
	}
	if len(refs) != 2 {
		t.Errorf("expected 2 refs for gap/topic, got %d", len(refs))
	}
}

// TestFindUnresolvedByTarget_Empty verifies that FindUnresolvedByTarget returns
// an empty (non-nil) slice when no rows match.
func TestFindUnresolvedByTarget_Empty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	refs, err := s.FindUnresolvedByTarget(ctx, "nonexistent/topic", "proj")
	if err != nil {
		t.Fatalf("FindUnresolvedByTarget: %v", err)
	}
	if refs == nil {
		t.Error("expected empty slice, got nil")
	}
	if len(refs) != 0 {
		t.Errorf("expected 0 refs, got %d", len(refs))
	}
}

// TestDeleteUnresolved_Exists verifies that DeleteUnresolved removes a row by ID.
func TestDeleteUnresolved_Exists(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	srcID := createTestMemory(t, s, "Source", "proj")
	if err := s.RegisterUnresolved(ctx, makeUnresolved(srcID, "del/topic", "proj")); err != nil {
		t.Fatalf("RegisterUnresolved: %v", err)
	}

	refs, err := s.FindUnresolvedByTarget(ctx, "del/topic", "proj")
	if err != nil {
		t.Fatalf("FindUnresolvedByTarget: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref before delete, got %d", len(refs))
	}

	if err := s.DeleteUnresolved(ctx, refs[0].ID); err != nil {
		t.Fatalf("DeleteUnresolved: %v", err)
	}

	afterRefs, err := s.FindUnresolvedByTarget(ctx, "del/topic", "proj")
	if err != nil {
		t.Fatalf("FindUnresolvedByTarget after delete: %v", err)
	}
	if len(afterRefs) != 0 {
		t.Errorf("expected 0 refs after delete, got %d", len(afterRefs))
	}
}

// TestDeleteUnresolved_NotExists verifies that DeleteUnresolved returns no
// error when the ID does not exist (no-op).
func TestDeleteUnresolved_NotExists(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.DeleteUnresolved(ctx, "00000000-0000-0000-0000-000000000000"); err != nil {
		t.Errorf("DeleteUnresolved non-existent ID returned error: %v", err)
	}
}

// TestDeleteUnresolvedBySourceAndTarget verifies deletion via the composite key.
func TestDeleteUnresolvedBySourceAndTarget(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	srcID := createTestMemory(t, s, "Source", "proj")
	if err := s.RegisterUnresolved(ctx, makeUnresolved(srcID, "composite/topic", "proj")); err != nil {
		t.Fatalf("RegisterUnresolved: %v", err)
	}

	if err := s.DeleteUnresolvedBySourceAndTarget(ctx, srcID, "composite/topic"); err != nil {
		t.Fatalf("DeleteUnresolvedBySourceAndTarget: %v", err)
	}

	refs, err := s.FindUnresolvedByTarget(ctx, "composite/topic", "proj")
	if err != nil {
		t.Fatalf("FindUnresolvedByTarget: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("expected 0 refs after composite delete, got %d", len(refs))
	}
}

// TestListUnresolved_OrderedByCount verifies that ListUnresolved returns rows
// ordered by mention_count descending.
func TestListUnresolved_OrderedByCount(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	src1 := createTestMemory(t, s, "Src1", "proj")
	src2 := createTestMemory(t, s, "Src2", "proj")
	src3 := createTestMemory(t, s, "Src3", "proj")

	// Register src1 pointing to "topic/a" twice → mention_count=2
	if err := s.RegisterUnresolved(ctx, makeUnresolved(src1, "topic/a", "proj")); err != nil {
		t.Fatalf("RegisterUnresolved: %v", err)
	}
	if err := s.RegisterUnresolved(ctx, makeUnresolved(src1, "topic/a", "proj")); err != nil {
		t.Fatalf("RegisterUnresolved upsert: %v", err)
	}

	// Register src2 → "topic/b" once → mention_count=1
	if err := s.RegisterUnresolved(ctx, makeUnresolved(src2, "topic/b", "proj")); err != nil {
		t.Fatalf("RegisterUnresolved src2: %v", err)
	}

	// Register src3 → "topic/c" three times → mention_count=3
	for i := 0; i < 3; i++ {
		if err := s.RegisterUnresolved(ctx, makeUnresolved(src3, "topic/c", "proj")); err != nil {
			t.Fatalf("RegisterUnresolved src3 %d: %v", i, err)
		}
	}

	refs, err := s.ListUnresolved(ctx, "proj", 10)
	if err != nil {
		t.Fatalf("ListUnresolved: %v", err)
	}
	if len(refs) != 3 {
		t.Fatalf("expected 3 refs, got %d", len(refs))
	}
	// Verify descending order: 3, 2, 1
	if refs[0].MentionCount != 3 {
		t.Errorf("refs[0].MentionCount = %d, want 3", refs[0].MentionCount)
	}
	if refs[1].MentionCount != 2 {
		t.Errorf("refs[1].MentionCount = %d, want 2", refs[1].MentionCount)
	}
	if refs[2].MentionCount != 1 {
		t.Errorf("refs[2].MentionCount = %d, want 1", refs[2].MentionCount)
	}
}

// TestCountUnresolved verifies that CountUnresolved returns the correct count
// scoped to the given project.
func TestCountUnresolved(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	src1 := createTestMemory(t, s, "Src1", "proj-a")
	src2 := createTestMemory(t, s, "Src2", "proj-b")

	if err := s.RegisterUnresolved(ctx, makeUnresolved(src1, "gap/one", "proj-a")); err != nil {
		t.Fatalf("RegisterUnresolved proj-a: %v", err)
	}
	if err := s.RegisterUnresolved(ctx, makeUnresolved(src1, "gap/two", "proj-a")); err != nil {
		t.Fatalf("RegisterUnresolved proj-a two: %v", err)
	}
	if err := s.RegisterUnresolved(ctx, makeUnresolved(src2, "gap/three", "proj-b")); err != nil {
		t.Fatalf("RegisterUnresolved proj-b: %v", err)
	}

	countA, err := s.CountUnresolved(ctx, "proj-a")
	if err != nil {
		t.Fatalf("CountUnresolved proj-a: %v", err)
	}
	if countA != 2 {
		t.Errorf("CountUnresolved(proj-a) = %d, want 2", countA)
	}

	countB, err := s.CountUnresolved(ctx, "proj-b")
	if err != nil {
		t.Fatalf("CountUnresolved proj-b: %v", err)
	}
	if countB != 1 {
		t.Errorf("CountUnresolved(proj-b) = %d, want 1", countB)
	}
}

// --------------------------------------------------------------------------
// ListGaps
// --------------------------------------------------------------------------

// TestListGaps_MultipleSorted verifies that ListGaps returns gaps sorted by
// total_mentions descending.
func TestListGaps_MultipleSorted(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	src1 := createTestMemory(t, s, "Src1", "proj")
	src2 := createTestMemory(t, s, "Src2", "proj")
	src3 := createTestMemory(t, s, "Src3", "proj")

	// gap/alpha: src1 mentions twice.
	for range 2 {
		if err := s.RegisterUnresolved(ctx, makeUnresolved(src1, "gap/alpha", "proj")); err != nil {
			t.Fatalf("RegisterUnresolved alpha: %v", err)
		}
	}
	// gap/beta: src2 mentions once.
	if err := s.RegisterUnresolved(ctx, makeUnresolved(src2, "gap/beta", "proj")); err != nil {
		t.Fatalf("RegisterUnresolved beta: %v", err)
	}
	// gap/gamma: src3 mentions three times.
	for range 3 {
		if err := s.RegisterUnresolved(ctx, makeUnresolved(src3, "gap/gamma", "proj")); err != nil {
			t.Fatalf("RegisterUnresolved gamma: %v", err)
		}
	}

	gaps, total, err := s.ListGaps(ctx, "proj", 10, 1)
	if err != nil {
		t.Fatalf("ListGaps: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(gaps) != 3 {
		t.Fatalf("len(gaps) = %d, want 3", len(gaps))
	}
	// Verify descending order by total_mentions.
	if gaps[0].TargetTopicKey != "gap/gamma" {
		t.Errorf("gaps[0] = %q, want gap/gamma", gaps[0].TargetTopicKey)
	}
	if gaps[0].TotalMentions != 3 {
		t.Errorf("gaps[0].TotalMentions = %d, want 3", gaps[0].TotalMentions)
	}
	if gaps[1].TargetTopicKey != "gap/alpha" {
		t.Errorf("gaps[1] = %q, want gap/alpha", gaps[1].TargetTopicKey)
	}
	if gaps[2].TargetTopicKey != "gap/beta" {
		t.Errorf("gaps[2] = %q, want gap/beta", gaps[2].TargetTopicKey)
	}
}

// TestListGaps_Limit verifies that the limit parameter is respected.
func TestListGaps_Limit(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for i := range 5 {
		src := createTestMemory(t, s, fmt.Sprintf("Src%d", i), "proj")
		if err := s.RegisterUnresolved(ctx, makeUnresolved(src, fmt.Sprintf("gap/%d", i), "proj")); err != nil {
			t.Fatalf("RegisterUnresolved %d: %v", i, err)
		}
	}

	gaps, total, err := s.ListGaps(ctx, "proj", 2, 1)
	if err != nil {
		t.Fatalf("ListGaps: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	if len(gaps) != 2 {
		t.Errorf("len(gaps) = %d, want 2", len(gaps))
	}
}

// TestListGaps_MinMentions verifies that gaps below the threshold are excluded.
func TestListGaps_MinMentions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	src1 := createTestMemory(t, s, "Src1", "proj")
	src2 := createTestMemory(t, s, "Src2", "proj")

	// gap/high: 5 mentions.
	for range 5 {
		if err := s.RegisterUnresolved(ctx, makeUnresolved(src1, "gap/high", "proj")); err != nil {
			t.Fatalf("RegisterUnresolved high: %v", err)
		}
	}
	// gap/low: 1 mention — should be filtered out.
	if err := s.RegisterUnresolved(ctx, makeUnresolved(src2, "gap/low", "proj")); err != nil {
		t.Fatalf("RegisterUnresolved low: %v", err)
	}

	gaps, total, err := s.ListGaps(ctx, "proj", 10, 3)
	if err != nil {
		t.Fatalf("ListGaps: %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}
	if len(gaps) != 1 {
		t.Fatalf("len(gaps) = %d, want 1", len(gaps))
	}
	if gaps[0].TargetTopicKey != "gap/high" {
		t.Errorf("gaps[0] = %q, want gap/high", gaps[0].TargetTopicKey)
	}
}

// TestListGaps_EmptyDB verifies that ListGaps returns an empty (non-nil) slice
// and total=0 when there are no unresolved references.
func TestListGaps_EmptyDB(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	gaps, total, err := s.ListGaps(ctx, "proj", 10, 1)
	if err != nil {
		t.Fatalf("ListGaps: %v", err)
	}
	if total != 0 {
		t.Errorf("total = %d, want 0", total)
	}
	if gaps == nil {
		t.Error("expected empty slice, got nil")
	}
	if len(gaps) != 0 {
		t.Errorf("len(gaps) = %d, want 0", len(gaps))
	}
}

// TestListGaps_ProjectFilter verifies that gaps from different projects are
// isolated when a project is specified.
func TestListGaps_ProjectFilter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	srcA := createTestMemory(t, s, "SrcA", "proj-a")
	srcB := createTestMemory(t, s, "SrcB", "proj-b")

	if err := s.RegisterUnresolved(ctx, makeUnresolved(srcA, "gap/shared", "proj-a")); err != nil {
		t.Fatalf("RegisterUnresolved a: %v", err)
	}
	if err := s.RegisterUnresolved(ctx, makeUnresolved(srcB, "gap/shared", "proj-b")); err != nil {
		t.Fatalf("RegisterUnresolved b: %v", err)
	}

	gapsA, _, err := s.ListGaps(ctx, "proj-a", 10, 1)
	if err != nil {
		t.Fatalf("ListGaps proj-a: %v", err)
	}
	if len(gapsA) != 1 {
		t.Errorf("proj-a: len(gaps) = %d, want 1", len(gapsA))
	}

	gapsB, _, err := s.ListGaps(ctx, "proj-b", 10, 1)
	if err != nil {
		t.Fatalf("ListGaps proj-b: %v", err)
	}
	if len(gapsB) != 1 {
		t.Errorf("proj-b: len(gaps) = %d, want 1", len(gapsB))
	}
}

// TestListGaps_SourceCount verifies that SourceCount is set correctly when
// multiple distinct sources reference the same gap.
func TestListGaps_SourceCount(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	src1 := createTestMemory(t, s, "Src1", "proj")
	src2 := createTestMemory(t, s, "Src2", "proj")
	src3 := createTestMemory(t, s, "Src3", "proj")

	for _, src := range []string{src1, src2, src3} {
		if err := s.RegisterUnresolved(ctx, makeUnresolved(src, "gap/multi", "proj")); err != nil {
			t.Fatalf("RegisterUnresolved: %v", err)
		}
	}

	gaps, _, err := s.ListGaps(ctx, "proj", 10, 1)
	if err != nil {
		t.Fatalf("ListGaps: %v", err)
	}
	if len(gaps) != 1 {
		t.Fatalf("len(gaps) = %d, want 1", len(gaps))
	}
	if gaps[0].SourceCount != 3 {
		t.Errorf("SourceCount = %d, want 3", gaps[0].SourceCount)
	}
	if gaps[0].TotalMentions != 3 {
		t.Errorf("TotalMentions = %d, want 3", gaps[0].TotalMentions)
	}
}

// --------------------------------------------------------------------------
// ListGapSamples
// --------------------------------------------------------------------------

// TestListGapSamples_Normal verifies that up to 3 samples are returned ordered
// by mention_count descending.
func TestListGapSamples_Normal(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	src1 := createTestMemory(t, s, "Src1", "proj")
	src2 := createTestMemory(t, s, "Src2", "proj")

	// src1 mentions 3 times, src2 once.
	for range 3 {
		if err := s.RegisterUnresolved(ctx, makeUnresolved(src1, "gap/topic", "proj")); err != nil {
			t.Fatalf("RegisterUnresolved src1: %v", err)
		}
	}
	if err := s.RegisterUnresolved(ctx, makeUnresolved(src2, "gap/topic", "proj")); err != nil {
		t.Fatalf("RegisterUnresolved src2: %v", err)
	}

	samples, err := s.ListGapSamples(ctx, "gap/topic", "proj", 3)
	if err != nil {
		t.Fatalf("ListGapSamples: %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("len(samples) = %d, want 2", len(samples))
	}
	// src1 should appear first (higher mention_count).
	if samples[0].MemoryID != src1 {
		t.Errorf("samples[0].MemoryID = %q, want %q", samples[0].MemoryID, src1)
	}
	if samples[0].Title != "Src1" {
		t.Errorf("samples[0].Title = %q, want Src1", samples[0].Title)
	}
}

// TestListGapSamples_SoftDeletedExcluded verifies that soft-deleted source
// memories are not included in samples.
func TestListGapSamples_SoftDeletedExcluded(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	live := createTestMemory(t, s, "Live", "proj")
	dead := createTestMemory(t, s, "Dead", "proj")

	if err := s.RegisterUnresolved(ctx, makeUnresolved(live, "gap/topic", "proj")); err != nil {
		t.Fatalf("RegisterUnresolved live: %v", err)
	}
	if err := s.RegisterUnresolved(ctx, makeUnresolved(dead, "gap/topic", "proj")); err != nil {
		t.Fatalf("RegisterUnresolved dead: %v", err)
	}

	// Soft-delete the "dead" memory.
	if err := s.SoftDelete(ctx, dead); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	samples, err := s.ListGapSamples(ctx, "gap/topic", "proj", 3)
	if err != nil {
		t.Fatalf("ListGapSamples: %v", err)
	}
	if len(samples) != 1 {
		t.Fatalf("len(samples) = %d, want 1 (deleted excluded)", len(samples))
	}
	if samples[0].MemoryID != live {
		t.Errorf("samples[0].MemoryID = %q, want %q (live)", samples[0].MemoryID, live)
	}
}

// TestListGapSamples_MaxCap verifies that the maxSamples cap is respected even
// when more sources exist.
func TestListGapSamples_MaxCap(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Create 5 sources, all pointing to the same gap.
	for i := range 5 {
		src := createTestMemory(t, s, fmt.Sprintf("Src%d", i), "proj")
		if err := s.RegisterUnresolved(ctx, makeUnresolved(src, "gap/topic", "proj")); err != nil {
			t.Fatalf("RegisterUnresolved %d: %v", i, err)
		}
	}

	samples, err := s.ListGapSamples(ctx, "gap/topic", "proj", 3)
	if err != nil {
		t.Fatalf("ListGapSamples: %v", err)
	}
	if len(samples) != 3 {
		t.Errorf("len(samples) = %d, want 3 (capped)", len(samples))
	}
}

// --------------------------------------------------------------------------
// CountDistinctGaps
// --------------------------------------------------------------------------

// TestCountDistinctGaps verifies the distinct count of target_topic_keys.
func TestCountDistinctGaps(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	src := createTestMemory(t, s, "Src", "proj")

	for _, key := range []string{"gap/a", "gap/b", "gap/c"} {
		if err := s.RegisterUnresolved(ctx, makeUnresolved(src, key, "proj")); err != nil {
			t.Fatalf("RegisterUnresolved %q: %v", key, err)
		}
	}

	n, err := s.CountDistinctGaps(ctx, "proj")
	if err != nil {
		t.Fatalf("CountDistinctGaps: %v", err)
	}
	if n != 3 {
		t.Errorf("CountDistinctGaps = %d, want 3", n)
	}
}

// TestCountDistinctGaps_Empty verifies that CountDistinctGaps returns 0 for an
// empty database.
func TestCountDistinctGaps_Empty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	n, err := s.CountDistinctGaps(ctx, "proj")
	if err != nil {
		t.Fatalf("CountDistinctGaps empty: %v", err)
	}
	if n != 0 {
		t.Errorf("CountDistinctGaps empty = %d, want 0", n)
	}
}

// TestCascadeDelete_HardDelete verifies that hard-deleting a memory cascades to
// its unresolved references via the ON DELETE CASCADE foreign key.
func TestCascadeDelete_HardDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	srcID := createTestMemory(t, s, "ToDelete", "proj")
	if err := s.RegisterUnresolved(ctx, makeUnresolved(srcID, "cascade/topic", "proj")); err != nil {
		t.Fatalf("RegisterUnresolved: %v", err)
	}

	// Verify the unresolved ref exists.
	refs, err := s.FindUnresolvedByTarget(ctx, "cascade/topic", "proj")
	if err != nil {
		t.Fatalf("FindUnresolvedByTarget before hard delete: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref before hard delete, got %d", len(refs))
	}

	// Soft-delete then hard-delete the source memory.
	if err := s.SoftDelete(ctx, srcID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	n, err := s.HardDelete(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("HardDelete: %v", err)
	}
	if n == 0 {
		t.Fatal("expected HardDelete to remove at least 1 memory")
	}

	// The unresolved ref should be gone via CASCADE.
	refsAfter, err := s.FindUnresolvedByTarget(ctx, "cascade/topic", "proj")
	if err != nil {
		t.Fatalf("FindUnresolvedByTarget after hard delete: %v", err)
	}
	if len(refsAfter) != 0 {
		t.Errorf("expected 0 refs after hard delete (CASCADE), got %d", len(refsAfter))
	}
}
