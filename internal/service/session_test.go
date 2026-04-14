package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/juanftp/mneme/internal/model"
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
