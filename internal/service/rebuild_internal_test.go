package service

import (
	"testing"

	"github.com/juanftp/mneme/internal/store"
)

// TestFindCandidatePairsInMemory_Basic verifies that two memories sharing
// 2 entities produce a single pair with SharedCount=2 when minShared=2.
func TestFindCandidatePairsInMemory_Basic(t *testing.T) {
	// m1 and m2 share entity-A and entity-B.
	pending := []pendingLink{
		{memoryID: "m1", entity: extractedEntity{Name: "entity-A"}},
		{memoryID: "m1", entity: extractedEntity{Name: "entity-B"}},
		{memoryID: "m2", entity: extractedEntity{Name: "entity-A"}},
		{memoryID: "m2", entity: extractedEntity{Name: "entity-B"}},
		// m3 only shares entity-A with the others.
		{memoryID: "m3", entity: extractedEntity{Name: "entity-A"}},
	}

	pairs := findCandidatePairsInMemory(pending, 2)

	// Only m1<->m2 satisfies minShared=2; m1<->m3 and m2<->m3 have SharedCount=1.
	if len(pairs) != 1 {
		t.Fatalf("expected 1 pair, got %d: %v", len(pairs), pairs)
	}
	p := pairs[0]
	if p.MemoryID1 != "m1" || p.MemoryID2 != "m2" {
		t.Errorf("wrong pair IDs: got (%s, %s), want (m1, m2)", p.MemoryID1, p.MemoryID2)
	}
	if p.SharedCount != 2 {
		t.Errorf("SharedCount = %d, want 2", p.SharedCount)
	}
	// Invariant: MemoryID1 < MemoryID2 lexicographically.
	if p.MemoryID1 >= p.MemoryID2 {
		t.Errorf("pair order violated: MemoryID1=%s >= MemoryID2=%s", p.MemoryID1, p.MemoryID2)
	}
}

// TestFindCandidatePairsInMemory_BelowThreshold verifies that a pair with
// SharedCount < minShared is excluded from the result.
func TestFindCandidatePairsInMemory_BelowThreshold(t *testing.T) {
	// m1 and m2 share only entity-A (SharedCount=1). With minShared=2: 0 pairs.
	pending := []pendingLink{
		{memoryID: "m1", entity: extractedEntity{Name: "entity-A"}},
		{memoryID: "m2", entity: extractedEntity{Name: "entity-A"}},
	}

	pairs := findCandidatePairsInMemory(pending, 2)

	if len(pairs) != 0 {
		t.Errorf("expected 0 pairs below threshold, got %d: %v", len(pairs), pairs)
	}
}

// TestFindCandidatePairsInMemory_DedupSameMemory verifies that the same
// (memoryID, entityName) appearing multiple times in the pending list is
// counted only once, preventing inflated pair counts.
func TestFindCandidatePairsInMemory_DedupSameMemory(t *testing.T) {
	// entity-A appears 3 times for m1 (duplicate) — should still count as 1.
	// m1 and m2 share entity-A and entity-B, so SharedCount should be 2.
	pending := []pendingLink{
		{memoryID: "m1", entity: extractedEntity{Name: "entity-A"}},
		{memoryID: "m1", entity: extractedEntity{Name: "entity-A"}}, // duplicate
		{memoryID: "m1", entity: extractedEntity{Name: "entity-A"}}, // duplicate
		{memoryID: "m1", entity: extractedEntity{Name: "entity-B"}},
		{memoryID: "m2", entity: extractedEntity{Name: "entity-A"}},
		{memoryID: "m2", entity: extractedEntity{Name: "entity-B"}},
	}

	pairs := findCandidatePairsInMemory(pending, 2)

	if len(pairs) != 1 {
		t.Fatalf("expected 1 pair after dedup, got %d: %v", len(pairs), pairs)
	}
	if pairs[0].SharedCount != 2 {
		t.Errorf("SharedCount = %d after dedup, want 2 (not inflated by duplicates)", pairs[0].SharedCount)
	}
}

// TestFindCandidatePairsInMemory_Deterministic verifies that running the
// function 10 times on the same input always returns pairs in identical order.
func TestFindCandidatePairsInMemory_Deterministic(t *testing.T) {
	pending := []pendingLink{
		{memoryID: "m1", entity: extractedEntity{Name: "entity-A"}},
		{memoryID: "m1", entity: extractedEntity{Name: "entity-B"}},
		{memoryID: "m1", entity: extractedEntity{Name: "entity-C"}},
		{memoryID: "m2", entity: extractedEntity{Name: "entity-A"}},
		{memoryID: "m2", entity: extractedEntity{Name: "entity-B"}},
		{memoryID: "m3", entity: extractedEntity{Name: "entity-A"}},
		{memoryID: "m3", entity: extractedEntity{Name: "entity-B"}},
		{memoryID: "m3", entity: extractedEntity{Name: "entity-C"}},
	}

	first := findCandidatePairsInMemory(pending, 2)
	if len(first) == 0 {
		t.Fatal("expected at least one pair for determinism test")
	}

	for run := 0; run < 9; run++ {
		got := findCandidatePairsInMemory(pending, 2)
		if len(got) != len(first) {
			t.Fatalf("run %d: len = %d, want %d", run+2, len(got), len(first))
		}
		for i := range first {
			if got[i] != first[i] {
				t.Errorf("run %d index %d: got %+v, want %+v", run+2, i, got[i], first[i])
			}
		}
	}
}

// TestFindCandidatePairsInMemory_Empty verifies that an empty pending list
// produces an empty result without panicking.
func TestFindCandidatePairsInMemory_Empty(t *testing.T) {
	pairs := findCandidatePairsInMemory(nil, 2)
	if pairs == nil {
		// A nil slice is acceptable — callers check len == 0.
		pairs = []store.CandidatePair{}
	}
	if len(pairs) != 0 {
		t.Errorf("expected 0 pairs for empty input, got %d", len(pairs))
	}
}
