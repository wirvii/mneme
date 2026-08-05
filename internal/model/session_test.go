package model

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSessionSummaryTopicKey verifies the deterministic topic_key derivation
// used by SessionEnd's upsert and by the store's NOT EXISTS lookup (SPEC-108 D9).
func TestSessionSummaryTopicKey(t *testing.T) {
	got := SessionSummaryTopicKey("abc")
	want := "session/abc"
	if got != want {
		t.Errorf("SessionSummaryTopicKey(%q) = %q, want %q", "abc", got, want)
	}
	if !strings.HasPrefix(got, SessionSummaryTopicKeyPrefix) {
		t.Errorf("expected %q to start with prefix %q", got, SessionSummaryTopicKeyPrefix)
	}
}

// TestSessionEndResponse_OptionalFieldsOmitted verifies that MemoriesCreated
// (a *int) and SessionDuration (a string) are absent from the marshaled JSON
// when unset — the honest "nothing to report" state (SPEC-108 D13), replacing
// the old hardcoded 0/"0s" literals.
func TestSessionEndResponse_OptionalFieldsOmitted(t *testing.T) {
	resp := SessionEndResponse{
		SessionID:       "sess-1",
		SummaryMemoryID: "mem-1",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal into map: %v", err)
	}

	if _, ok := raw["memories_created"]; ok {
		t.Errorf("expected memories_created to be absent, got %s", data)
	}
	if _, ok := raw["session_duration"]; ok {
		t.Errorf("expected session_duration to be absent, got %s", data)
	}
}

// TestSessionEndResponse_ZeroCountIsPresent verifies that a *int pointing at
// 0 (a real, meaningful count) is a present JSON key with value 0 — omitempty
// on a pointer only omits nil, never the pointee's zero value.
func TestSessionEndResponse_ZeroCountIsPresent(t *testing.T) {
	zero := 0
	resp := SessionEndResponse{
		SessionID:       "sess-1",
		SummaryMemoryID: "mem-1",
		MemoriesCreated: &zero,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal into map: %v", err)
	}

	got, ok := raw["memories_created"]
	if !ok {
		t.Fatalf("expected memories_created to be present, got %s", data)
	}
	if string(got) != "0" {
		t.Errorf("expected memories_created=0, got %s", got)
	}
}
