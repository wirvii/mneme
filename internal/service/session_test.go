package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/wirvii/mneme/internal/model"
)

func TestSessionEnd_Success(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.SessionEnd(ctx, model.SessionEndRequest{
		Summary: "Completed OAuth2 integration with GitHub.",
	})
	if err != nil {
		t.Fatalf("SessionEnd: %v", err)
	}
	if resp.SessionID == "" {
		t.Error("expected non-empty SessionID")
	}
	if resp.SummaryMemoryID == "" {
		t.Error("expected non-empty SummaryMemoryID")
	}
}

func TestSessionEnd_Validation(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.SessionEnd(ctx, model.SessionEndRequest{Summary: ""})
	if !errors.Is(err, model.ErrSummaryRequired) {
		t.Errorf("expected ErrSummaryRequired, got %v", err)
	}
}

func TestSessionEnd_CreatesMemory(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.SessionEnd(ctx, model.SessionEndRequest{
		Summary:   "Refactored the scoring package.",
		SessionID: "test-session-001",
	})
	if err != nil {
		t.Fatalf("SessionEnd: %v", err)
	}

	// The created memory should be retrievable via Get.
	mem, err := svc.Get(ctx, resp.SummaryMemoryID)
	if err != nil {
		t.Fatalf("Get summary memory: %v", err)
	}
	if mem.Type != model.TypeSessionSummary {
		t.Errorf("expected type session_summary, got %q", mem.Type)
	}
	if mem.Content != "Refactored the scoring package." {
		t.Errorf("unexpected content: %q", mem.Content)
	}
	if mem.SessionID != "test-session-001" {
		t.Errorf("expected session_id=test-session-001, got %q", mem.SessionID)
	}
}

// TestSessionEnd_MetricsWithSessionID verifies that closing a session with a
// session_id that has attributable work reports the real count and a
// non-empty duration (AC11).
func TestSessionEnd_MetricsWithSessionID(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	sessionID := "sess-metrics-001"
	for i := 0; i < 3; i++ {
		if _, err := svc.Save(ctx, model.SaveRequest{
			Title:     "work item",
			Content:   "some discovery",
			SessionID: sessionID,
		}); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	resp, err := svc.SessionEnd(ctx, model.SessionEndRequest{
		Summary:   "closed with real work",
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("SessionEnd: %v", err)
	}

	if resp.MemoriesCreated == nil {
		t.Fatal("expected MemoriesCreated to be present")
	}
	if *resp.MemoriesCreated != 3 {
		t.Errorf("MemoriesCreated: got %d, want 3", *resp.MemoriesCreated)
	}
	if resp.SessionDuration == "" {
		t.Error("expected non-empty SessionDuration")
	}
}

// TestSessionEnd_OmitsMetricsWithoutSessionID verifies that the JSON response
// omits both memories_created and session_duration when the caller does not
// supply a session_id — mneme just generated one and cannot attribute prior
// work to it (AC12).
func TestSessionEnd_OmitsMetricsWithoutSessionID(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.SessionEnd(ctx, model.SessionEndRequest{
		Summary: "closed without session_id",
	})
	if err != nil {
		t.Fatalf("SessionEnd: %v", err)
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

// TestSessionEnd_ZeroWork verifies that a session_id with no attributable
// work reports memories_created=0 (present, a true value) while
// session_duration stays absent — there is nothing to measure a duration
// over (AC13).
func TestSessionEnd_ZeroWork(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.SessionEnd(ctx, model.SessionEndRequest{
		Summary:   "closed with no prior work",
		SessionID: "sess-zero-work",
	})
	if err != nil {
		t.Fatalf("SessionEnd: %v", err)
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
	if _, ok := raw["session_duration"]; ok {
		t.Errorf("expected session_duration to be absent, got %s", data)
	}
}

// TestSessionEnd_Idempotent verifies that closing the same session twice
// produces exactly one summary memory (upsert by topic_key), not a
// duplicate row (AC14).
func TestSessionEnd_Idempotent(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	sessionID := "sess-idempotent-001"

	first, err := svc.SessionEnd(ctx, model.SessionEndRequest{
		Summary:   "first close",
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("first SessionEnd: %v", err)
	}

	second, err := svc.SessionEnd(ctx, model.SessionEndRequest{
		Summary:   "second close, same session",
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("second SessionEnd: %v", err)
	}

	if first.SummaryMemoryID != second.SummaryMemoryID {
		t.Errorf("expected the same summary memory id across both calls: first=%s second=%s",
			first.SummaryMemoryID, second.SummaryMemoryID)
	}

	mem, err := svc.Get(ctx, second.SummaryMemoryID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if mem.Content != "second close, same session" {
		t.Errorf("expected the summary content to reflect the latest call, got %q", mem.Content)
	}
}

// TestPendingSessionSummaries_ExcludesCurrent verifies that among two
// orphaned sessions, the most recent one being the CURRENT session is
// excluded from the report, leaving the other one and OlderCount==0 (AC15).
func TestPendingSessionSummaries_ExcludesCurrent(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	older := "sess-older-001"
	current := "sess-current-001"

	if _, err := svc.Save(ctx, model.SaveRequest{
		Title: "older work", Content: "content", SessionID: older,
	}); err != nil {
		t.Fatalf("Save older: %v", err)
	}
	if _, err := svc.Save(ctx, model.SaveRequest{
		Title: "current work", Content: "content", SessionID: current,
	}); err != nil {
		t.Fatalf("Save current: %v", err)
	}

	resp, err := svc.PendingSessionSummaries(ctx, model.PendingSessionsRequest{
		CurrentSessionID: current,
	})
	if err != nil {
		t.Fatalf("PendingSessionSummaries: %v", err)
	}
	if resp.Pending == nil {
		t.Fatal("expected a pending session, got nil")
	}
	if resp.Pending.SessionID != older {
		t.Errorf("Pending.SessionID = %q, want %q", resp.Pending.SessionID, older)
	}
	if resp.OlderCount != 0 {
		t.Errorf("OlderCount = %d, want 0", resp.OlderCount)
	}
}

// TestPendingSessionSummaries_None verifies that Pending is nil (not an
// error) when there is no orphaned session (AC15).
func TestPendingSessionSummaries_None(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.PendingSessionSummaries(ctx, model.PendingSessionsRequest{})
	if err != nil {
		t.Fatalf("PendingSessionSummaries: %v", err)
	}
	if resp.Pending != nil {
		t.Errorf("expected nil Pending, got %+v", resp.Pending)
	}
	if resp.OlderCount != 0 {
		t.Errorf("OlderCount = %d, want 0", resp.OlderCount)
	}
}

// TestPendingCountMatchesSessionEndCount is the cross-check of D19: for the
// same session, the count PendingSessionSummaries reports must equal what
// SessionEnd's MemoriesCreated would report — both derive from the same
// predicate, so they can never diverge (AC16).
func TestPendingCountMatchesSessionEndCount(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	sessionID := "sess-cross-check-001"
	for i := 0; i < 4; i++ {
		if _, err := svc.Save(ctx, model.SaveRequest{
			Title: "work", Content: "content", SessionID: sessionID,
		}); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	pending, err := svc.PendingSessionSummaries(ctx, model.PendingSessionsRequest{
		CurrentSessionID: "some-other-current-session",
	})
	if err != nil {
		t.Fatalf("PendingSessionSummaries: %v", err)
	}
	if pending.Pending == nil {
		t.Fatal("expected a pending session")
	}

	endResp, err := svc.SessionEnd(ctx, model.SessionEndRequest{
		Summary:   "closing the same session",
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("SessionEnd: %v", err)
	}
	if endResp.MemoriesCreated == nil {
		t.Fatal("expected MemoriesCreated to be present")
	}

	if pending.Pending.MemoryCount != *endResp.MemoriesCreated {
		t.Errorf("PendingSessionSummaries count (%d) diverges from SessionEnd count (%d)",
			pending.Pending.MemoryCount, *endResp.MemoriesCreated)
	}
}

func TestCheckpoint_Success(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.Checkpoint(ctx, model.CheckpointRequest{
		Summary: "working on auth",
	})
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if resp.ID == "" {
		t.Error("expected non-empty ID")
	}
	if resp.Action != "created" {
		t.Errorf("action = %q, want %q", resp.Action, "created")
	}

	// The created memory should be retrievable and have topic_key "checkpoint/latest".
	mem, err := svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Get checkpoint memory: %v", err)
	}
	if mem.Type != model.TypeSessionSummary {
		t.Errorf("type = %q, want session_summary", mem.Type)
	}
	if mem.TopicKey != "checkpoint/latest" {
		t.Errorf("topic_key = %q, want checkpoint/latest", mem.TopicKey)
	}
	if mem.Title != "Work checkpoint" {
		t.Errorf("title = %q, want Work checkpoint", mem.Title)
	}
}

func TestCheckpoint_Validation(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.Checkpoint(ctx, model.CheckpointRequest{Summary: ""})
	if !errors.Is(err, model.ErrSummaryRequired) {
		t.Errorf("expected ErrSummaryRequired, got %v", err)
	}
}

func TestCheckpoint_Upsert(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// First call: creates the checkpoint.
	first, err := svc.Checkpoint(ctx, model.CheckpointRequest{
		Summary: "first checkpoint",
	})
	if err != nil {
		t.Fatalf("first Checkpoint: %v", err)
	}
	if first.Action != "created" {
		t.Errorf("first action = %q, want created", first.Action)
	}

	// Second call: overwrites the checkpoint — same topic_key.
	second, err := svc.Checkpoint(ctx, model.CheckpointRequest{
		Summary: "second checkpoint",
	})
	if err != nil {
		t.Fatalf("second Checkpoint: %v", err)
	}
	if second.Action != "updated" {
		t.Errorf("second action = %q, want updated", second.Action)
	}

	// Both calls return the same memory ID (upsert, not insert).
	if first.ID != second.ID {
		t.Errorf("id changed between checkpoints: first=%s second=%s", first.ID, second.ID)
	}
}

func TestCheckpoint_ContentStructure(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	tests := []struct {
		name        string
		req         model.CheckpointRequest
		wantContent string
	}{
		{
			name:        "summary only",
			req:         model.CheckpointRequest{Summary: "doing stuff"},
			wantContent: "## Current State\ndoing stuff",
		},
		{
			name: "summary with decisions and next_steps",
			req: model.CheckpointRequest{
				Summary:   "doing stuff",
				Decisions: "chose approach A",
				NextSteps: "run tests",
			},
			wantContent: "## Current State\ndoing stuff\n\n## Decisions\nchose approach A\n\n## Next Steps\nrun tests",
		},
		{
			name: "summary with decisions only",
			req: model.CheckpointRequest{
				Summary:   "doing stuff",
				Decisions: "chose approach A",
			},
			wantContent: "## Current State\ndoing stuff\n\n## Decisions\nchose approach A",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := svc.Checkpoint(ctx, tc.req)
			if err != nil {
				t.Fatalf("Checkpoint: %v", err)
			}
			mem, err := svc.Get(ctx, resp.ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if mem.Content != tc.wantContent {
				t.Errorf("content = %q, want %q", mem.Content, tc.wantContent)
			}
		})
	}
}
