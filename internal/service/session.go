package service

import (
	"context"
	"fmt"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/scoring"
)

// SessionEnd closes the current agent session and persists a session_summary
// memory. It validates the request, creates a summary Memory via topic key upsert
// so that only one summary per session_id exists, then creates or updates the
// session record.
//
// Validation rules:
//   - Summary must not be empty (ErrSummaryRequired)
//   - Project defaults to the service's project when omitted
//   - SessionID is generated (UUIDv7) when omitted
func (svc *MemoryService) SessionEnd(ctx context.Context, req model.SessionEndRequest) (*model.SessionEndResponse, error) {
	if req.Summary == "" {
		return nil, fmt.Errorf("service: session end: %w", model.ErrSummaryRequired)
	}

	if req.Project == "" {
		req.Project = svc.project
	}

	sessionID := req.SessionID
	if sessionID == "" {
		id, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("service: session end: generate session id: %w", err)
		}
		sessionID = id.String()
	}

	// Create or update a session_summary memory keyed by the session ID so that
	// calling SessionEnd twice for the same session is idempotent.
	topicKey := fmt.Sprintf("session/%s", sessionID)

	summaryMemory := &model.Memory{
		Type:       model.TypeSessionSummary,
		Scope:      model.ScopeProject,
		Title:      fmt.Sprintf("Session summary: %s", sessionID),
		Content:    req.Summary,
		TopicKey:   topicKey,
		Project:    req.Project,
		SessionID:  sessionID,
		Importance: scoring.InitialImportance(model.TypeSessionSummary, nil),
		Confidence: model.DefaultConfidence,
		DecayRate:  scoring.DecayRateForType(model.TypeSessionSummary),
	}

	savedMem, _, err := svc.store.Upsert(ctx, summaryMemory)
	if err != nil {
		return nil, fmt.Errorf("service: session end: upsert summary memory: %w", err)
	}

	// Create the session record if it does not exist, then mark it as ended.
	now := time.Now().UTC()
	sess := &model.Session{
		ID:        sessionID,
		Project:   req.Project,
		StartedAt: now,
		SummaryID: savedMem.ID,
	}

	_, createErr := svc.store.CreateSession(ctx, sess)
	if createErr != nil {
		// If the session already exists (duplicate key), fall through and just
		// update the ended_at and summary_id via EndSession.
	}

	if err := svc.store.EndSession(ctx, sessionID, savedMem.ID); err != nil {
		// If the session wasn't found above (createErr != nil and EndSession
		// returned ErrNotFound), it means the session was just created and
		// ended in the same call — this is the common case. Surface EndSession
		// errors only when CreateSession also succeeded (i.e. this is a real
		// failure to end an existing session).
		if createErr == nil {
			return nil, fmt.Errorf("service: session end: end session: %w", err)
		}
	}

	return &model.SessionEndResponse{
		SessionID:       sessionID,
		SummaryMemoryID: savedMem.ID,
		MemoriesCreated: 0,    // Phase 2: count via session_id query
		SessionDuration: "0s", // Phase 2: compute from started_at
	}, nil
}
