package model

import (
	"encoding/json"
	"testing"
	"time"
)

// TestGap_JSONRoundtrip verifies that a Gap with all fields survives a
// marshal/unmarshal round-trip with values preserved.
func TestGap_JSONRoundtrip(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Second)
	original := Gap{
		TargetTopicKey: "architecture/auth-model",
		TotalMentions:  12,
		SourceCount:    5,
		FirstSeenAt:    now.Add(-24 * time.Hour),
		LastSeenAt:     now,
		Samples: []GapSample{
			{MemoryID: "019de100-0000-7fff-8000-000000000001", Title: "JWT refactor", TopicKey: "bugfix/jwt"},
			{MemoryID: "019de100-0000-7fff-8000-000000000002", Title: "Auth notes"},
		},
	}

	b, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got Gap
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.TargetTopicKey != original.TargetTopicKey {
		t.Errorf("TargetTopicKey: got %q, want %q", got.TargetTopicKey, original.TargetTopicKey)
	}
	if got.TotalMentions != original.TotalMentions {
		t.Errorf("TotalMentions: got %d, want %d", got.TotalMentions, original.TotalMentions)
	}
	if got.SourceCount != original.SourceCount {
		t.Errorf("SourceCount: got %d, want %d", got.SourceCount, original.SourceCount)
	}
	if len(got.Samples) != len(original.Samples) {
		t.Fatalf("Samples len: got %d, want %d", len(got.Samples), len(original.Samples))
	}
	if got.Samples[0].MemoryID != original.Samples[0].MemoryID {
		t.Errorf("Samples[0].MemoryID: got %q, want %q", got.Samples[0].MemoryID, original.Samples[0].MemoryID)
	}
	if got.Samples[1].TopicKey != original.Samples[1].TopicKey {
		t.Errorf("Samples[1].TopicKey: got %q, want %q", got.Samples[1].TopicKey, original.Samples[1].TopicKey)
	}
}

// TestGap_OmitEmpty verifies that samples and topic_key are omitted from JSON
// when they are zero/nil.
func TestGap_OmitEmpty(t *testing.T) {
	t.Parallel()

	g := Gap{
		TargetTopicKey: "config/missing",
		TotalMentions:  3,
		SourceCount:    1,
		// Samples intentionally empty.
	}

	b, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("Unmarshal into map: %v", err)
	}
	if _, ok := raw["samples"]; ok {
		t.Error("expected 'samples' to be omitted when empty")
	}
}

// TestGapSample_TopicKeyOmitted verifies that topic_key is omitted from JSON
// when it is empty.
func TestGapSample_TopicKeyOmitted(t *testing.T) {
	t.Parallel()

	s := GapSample{MemoryID: "abc", Title: "No topic key"}

	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("Unmarshal into map: %v", err)
	}
	if _, ok := raw["topic_key"]; ok {
		t.Error("expected 'topic_key' to be omitted when empty")
	}
}

// TestGapsRequest_IncludeSamples verifies that IncludeSamples round-trips
// correctly as a pointer, distinguishing nil from explicit false.
func TestGapsRequest_IncludeSamples(t *testing.T) {
	t.Parallel()

	f := false
	req := GapsRequest{
		Scope:          "project",
		Limit:          20,
		IncludeSamples: &f,
	}

	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got GapsRequest
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.IncludeSamples == nil {
		t.Fatal("expected IncludeSamples to be non-nil after round-trip")
	}
	if *got.IncludeSamples != false {
		t.Errorf("IncludeSamples: got %v, want false", *got.IncludeSamples)
	}
}

// TestGapsRequest_IncludeSamplesNilOmitted verifies that a nil IncludeSamples
// field is omitted from JSON output.
func TestGapsRequest_IncludeSamplesNilOmitted(t *testing.T) {
	t.Parallel()

	req := GapsRequest{Scope: "project"}

	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("Unmarshal into map: %v", err)
	}
	if _, ok := raw["include_samples"]; ok {
		t.Error("expected 'include_samples' to be omitted when nil")
	}
}

// TestKnowledgeGaps_JSONRoundtrip verifies that KnowledgeGaps serialises and
// deserialises correctly.
func TestKnowledgeGaps_JSONRoundtrip(t *testing.T) {
	t.Parallel()

	kg := KnowledgeGaps{
		Total: 8,
		Top: []Gap{
			{TargetTopicKey: "arch/auth", TotalMentions: 12, SourceCount: 5},
		},
	}

	b, err := json.Marshal(kg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got KnowledgeGaps
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Total != kg.Total {
		t.Errorf("Total: got %d, want %d", got.Total, kg.Total)
	}
	if len(got.Top) != len(kg.Top) {
		t.Fatalf("Top len: got %d, want %d", len(got.Top), len(kg.Top))
	}
	if got.Top[0].TargetTopicKey != kg.Top[0].TargetTopicKey {
		t.Errorf("Top[0].TargetTopicKey: got %q, want %q", got.Top[0].TargetTopicKey, kg.Top[0].TargetTopicKey)
	}
}
