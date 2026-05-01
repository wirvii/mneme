package model

import (
	"encoding/json"
	"testing"
)

// TestTopicKeyMatch_JSONRoundTrip verifies that the new omitempty fields
// serialize correctly and absent zero-value fields do not appear in JSON.
func TestTopicKeyMatch_JSONRoundTrip(t *testing.T) {
	t.Run("all fields present", func(t *testing.T) {
		m := TopicKeyMatch{
			TopicKey:     "auth/jwt-setup",
			Title:        "auth/jwt-setup",
			Score:        0.72,
			FromGap:      true,
			PendingCount: 8,
			Reason:       "unresolved gap, 8 pending mentions",
		}

		data, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}

		var got TopicKeyMatch
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		if got.TopicKey != m.TopicKey {
			t.Errorf("TopicKey: got %q, want %q", got.TopicKey, m.TopicKey)
		}
		if got.Score != m.Score {
			t.Errorf("Score: got %f, want %f", got.Score, m.Score)
		}
		if !got.FromGap {
			t.Error("FromGap: expected true")
		}
		if got.PendingCount != 8 {
			t.Errorf("PendingCount: got %d, want 8", got.PendingCount)
		}
		if got.Reason != m.Reason {
			t.Errorf("Reason: got %q, want %q", got.Reason, m.Reason)
		}
		// ID should be absent in JSON (empty string + omitempty).
		if got.ID != "" {
			t.Errorf("ID should be empty for gap match, got %q", got.ID)
		}
	})

	t.Run("id field omitted when empty", func(t *testing.T) {
		m := TopicKeyMatch{TopicKey: "auth/jwt-setup", Title: "auth/jwt-setup"}
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}

		// "id" must not appear in the JSON output.
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("unmarshal raw: %v", err)
		}
		if _, ok := raw["id"]; ok {
			t.Error("expected 'id' to be absent in JSON when empty")
		}
	})

	t.Run("score omitted when zero", func(t *testing.T) {
		m := TopicKeyMatch{TopicKey: "some/key", Title: "Title", ID: "abc"}
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}

		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("unmarshal raw: %v", err)
		}
		if _, ok := raw["score"]; ok {
			t.Error("expected 'score' to be absent in JSON when zero")
		}
	})
}

// TestTopicKeySuggestion_BackwardCompat verifies that the old shape
// (no GapMatches, no Score) serialises identically to pre-SPEC-014 output.
// Agents that ignore unknown fields receive the same essential data.
func TestTopicKeySuggestion_BackwardCompat(t *testing.T) {
	s := TopicKeySuggestion{
		Suggestion: "discovery/authentication-model",
		ExistingMatches: []TopicKeyMatch{
			{
				TopicKey: "architecture/auth-model",
				Title:    "Authentication model",
				ID:       "019de100-abcd-7fff-8000-000000000001",
			},
		},
		IsNewTopic: false,
	}

	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}

	// gap_matches must not appear when nil.
	if _, ok := raw["gap_matches"]; ok {
		t.Error("expected 'gap_matches' to be absent when nil")
	}

	// Existing fields must be present.
	if raw["suggestion"] != "discovery/authentication-model" {
		t.Errorf("suggestion: got %v", raw["suggestion"])
	}
	if raw["is_new_topic"] != false {
		t.Errorf("is_new_topic: got %v", raw["is_new_topic"])
	}
	existingMatches, ok := raw["existing_matches"].([]any)
	if !ok || len(existingMatches) != 1 {
		t.Fatalf("existing_matches: got %v", raw["existing_matches"])
	}
	match, ok := existingMatches[0].(map[string]any)
	if !ok {
		t.Fatalf("existing_matches[0]: not a map")
	}
	if match["topic_key"] != "architecture/auth-model" {
		t.Errorf("topic_key: got %v", match["topic_key"])
	}
	// score must not appear for zero-value entry.
	if _, ok := match["score"]; ok {
		t.Error("expected 'score' to be absent in existing match when zero")
	}
}
