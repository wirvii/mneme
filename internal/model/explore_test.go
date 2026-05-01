package model

import (
	"encoding/json"
	"errors"
	"testing"
)

// TestExploreNode_JSONRoundtrip verifies that an ExploreNode can be marshalled
// to JSON and back with all fields preserved.
func TestExploreNode_JSONRoundtrip(t *testing.T) {
	t.Parallel()

	depth := 2
	original := ExploreNode{
		MemoryID:          "019de0f5-0a94-77b2-9bff-ab572de30067",
		ParentMemoryID:    "019de0f5-0a94-77b2-9bff-ab572de30068",
		Title:             "architecture decision about caching",
		TopicKey:          "arch/caching",
		Type:              TypeDecision,
		Distance:          depth,
		AccumulatedWeight: 0.72,
		RelationType:      RelRelatedTo,
		TokenEstimate:     42,
	}

	b, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got ExploreNode
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.MemoryID != original.MemoryID {
		t.Errorf("MemoryID: got %q, want %q", got.MemoryID, original.MemoryID)
	}
	if got.ParentMemoryID != original.ParentMemoryID {
		t.Errorf("ParentMemoryID: got %q, want %q", got.ParentMemoryID, original.ParentMemoryID)
	}
	if got.Title != original.Title {
		t.Errorf("Title: got %q, want %q", got.Title, original.Title)
	}
	if got.TopicKey != original.TopicKey {
		t.Errorf("TopicKey: got %q, want %q", got.TopicKey, original.TopicKey)
	}
	if got.Type != original.Type {
		t.Errorf("Type: got %q, want %q", got.Type, original.Type)
	}
	if got.Distance != original.Distance {
		t.Errorf("Distance: got %d, want %d", got.Distance, original.Distance)
	}
	if got.AccumulatedWeight != original.AccumulatedWeight {
		t.Errorf("AccumulatedWeight: got %f, want %f", got.AccumulatedWeight, original.AccumulatedWeight)
	}
	if got.RelationType != original.RelationType {
		t.Errorf("RelationType: got %q, want %q", got.RelationType, original.RelationType)
	}
	if got.TokenEstimate != original.TokenEstimate {
		t.Errorf("TokenEstimate: got %d, want %d", got.TokenEstimate, original.TokenEstimate)
	}
}

// TestExploreNode_JSONRoundtrip_OmitEmpty verifies that optional fields with
// zero values are omitted in the JSON output (omitempty semantics).
func TestExploreNode_JSONRoundtrip_OmitEmpty(t *testing.T) {
	t.Parallel()

	node := ExploreNode{
		MemoryID:          "019de0f5-0a94-77b2-9bff-000000000001",
		Title:             "no topic key, no parent",
		Type:              TypeDiscovery,
		Distance:          1,
		AccumulatedWeight: 0.9,
		RelationType:      RelRelatedTo,
		TokenEstimate:     10,
		// ParentMemoryID and TopicKey are intentionally zero.
	}

	b, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Neither "parent_memory_id" nor "topic_key" should appear in the JSON
	// because they are empty and tagged omitempty.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("Unmarshal into map: %v", err)
	}
	if _, ok := raw["parent_memory_id"]; ok {
		t.Error("expected parent_memory_id to be omitted when empty")
	}
	if _, ok := raw["topic_key"]; ok {
		t.Error("expected topic_key to be omitted when empty")
	}
}

// TestErrAmbiguousSeed_Sentinel verifies that ErrAmbiguousSeed is a valid
// sentinel error and that errors.Is correctly identifies it when wrapped.
func TestErrAmbiguousSeed_Sentinel(t *testing.T) {
	t.Parallel()

	if ErrAmbiguousSeed == nil {
		t.Fatal("ErrAmbiguousSeed must not be nil")
	}

	// Direct equality.
	if !errors.Is(ErrAmbiguousSeed, ErrAmbiguousSeed) {
		t.Error("errors.Is(ErrAmbiguousSeed, ErrAmbiguousSeed) should be true")
	}

	// Wrapped via fmt.Errorf — the most common way callers propagate it.
	wrapped := errors.Join(errors.New("outer context"), ErrAmbiguousSeed)
	if !errors.Is(wrapped, ErrAmbiguousSeed) {
		t.Error("errors.Is should unwrap through errors.Join")
	}

	// Distinct from other sentinels.
	if errors.Is(ErrAmbiguousSeed, ErrNotFound) {
		t.Error("ErrAmbiguousSeed must be distinct from ErrNotFound")
	}
}
