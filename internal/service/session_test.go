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
