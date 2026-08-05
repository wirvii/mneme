package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/scoring"
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
	topicKey := model.SessionSummaryTopicKey(sessionID)

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

	savedMem, _, err := svc.projectStore.Upsert(ctx, summaryMemory)
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

	// If CreateSession fails (e.g. duplicate key), fall through and update via EndSession.
	_, createErr := svc.projectStore.CreateSession(ctx, sess)

	if err := svc.projectStore.EndSession(ctx, sessionID, savedMem.ID); err != nil {
		// If the session wasn't found above (createErr != nil and EndSession
		// returned ErrNotFound), it means the session was just created and
		// ended in the same call — this is the common case. Surface EndSession
		// errors only when CreateSession also succeeded (i.e. this is a real
		// failure to end an existing session).
		if createErr == nil {
			return nil, fmt.Errorf("service: session end: end session: %w", err)
		}
	}

	resp := &model.SessionEndResponse{
		SessionID:       sessionID,
		SummaryMemoryID: savedMem.ID,
	}

	// When the caller omitted session_id, mneme generated a fresh UUID above:
	// there is no prior work to attribute to a UUID that was just invented,
	// so neither field is even queried — both stay absent (SPEC-108 D13).
	if req.SessionID == "" {
		return resp, nil
	}

	// A failure here must not fail the close: the summary is already saved,
	// and losing the metric is preferable to losing the close itself.
	activity, actErr := svc.projectStore.GetSessionActivity(ctx, req.Project, sessionID)
	if actErr != nil {
		slog.WarnContext(ctx, "session end: get session activity failed",
			"session_id", sessionID, "error", actErr)
		return resp, nil
	}

	count := activity.MemoryCount
	resp.MemoriesCreated = &count
	if count > 0 {
		resp.SessionDuration = formatSessionDuration(activity.FirstAt, time.Now().UTC())
	}

	return resp, nil
}

// formatSessionDuration es puro para que la corrección numérica se pueda
// probar con tiempos fijos: en un test de integración toda memoria nace
// "ahora" y la duración redondearía a "0s", justo el literal que esta spec
// elimina (SPEC-108 §nota 3 del plan).
func formatSessionDuration(firstAt, now time.Time) string {
	return now.Sub(firstAt).Round(time.Second).String()
}

// PendingSessionSummaries devuelve la sesión huérfana MÁS RECIENTE (la que
// dejó trabajo y no tiene resumen), excluyendo CurrentSessionID, más cuántas
// otras más antiguas están igual. Solo la más reciente se reporta y el aviso
// repite en cada arranque hasta que exista el resumen: es convergente y se
// autolimita porque cada sesión desplaza a la anterior (SPEC-108 D4).
// Pending nil + OlderCount 0 cuando no hay ninguna (no es error).
func (svc *MemoryService) PendingSessionSummaries(ctx context.Context, req model.PendingSessionsRequest) (*model.PendingSessionsResponse, error) {
	if req.Project == "" {
		req.Project = svc.project
	}

	orphaned, err := svc.projectStore.ListSessionsWithoutSummary(ctx, req.Project)
	if err != nil {
		return nil, fmt.Errorf("service: pending session summaries: %w", err)
	}

	filtered := make([]model.SessionActivity, 0, len(orphaned))
	for _, a := range orphaned {
		if a.SessionID == req.CurrentSessionID {
			continue
		}
		filtered = append(filtered, a)
	}

	if len(filtered) == 0 {
		return &model.PendingSessionsResponse{}, nil
	}

	pending := filtered[0]

	return &model.PendingSessionsResponse{
		Pending:    &pending,
		OlderCount: len(filtered) - 1,
	}, nil
}

// Checkpoint saves a snapshot of the agent's current work state as a
// session_summary memory with topic_key "checkpoint/latest". Because of the
// upsert-by-topic-key semantics, only one checkpoint is ever retained per
// project — each call overwrites the previous one.
//
// This provides compaction insurance: if Claude Code compacts context mid-task,
// the agent can call mem_context after recovery and find the checkpoint in the
// top-importance memories.
//
// Validation rules:
//   - Summary must not be empty (ErrSummaryRequired)
//   - Project defaults to the service's project when omitted
func (svc *MemoryService) Checkpoint(ctx context.Context, req model.CheckpointRequest) (*model.CheckpointResponse, error) {
	if req.Summary == "" {
		return nil, fmt.Errorf("service: checkpoint: %w", model.ErrSummaryRequired)
	}

	if req.Project == "" {
		req.Project = svc.project
	}

	content := "## Current State\n" + req.Summary
	if req.Decisions != "" {
		content += "\n\n## Decisions\n" + req.Decisions
	}
	if req.NextSteps != "" {
		content += "\n\n## Next Steps\n" + req.NextSteps
	}

	m := &model.Memory{
		Type:       model.TypeSessionSummary,
		Scope:      model.ScopeProject,
		Title:      "Work checkpoint",
		Content:    content,
		TopicKey:   "checkpoint/latest",
		Project:    req.Project,
		Importance: scoring.InitialImportance(model.TypeSessionSummary, nil),
		Confidence: model.DefaultConfidence,
		DecayRate:  scoring.DecayRateForType(model.TypeSessionSummary),
	}

	savedMem, created, err := svc.projectStore.Upsert(ctx, m)
	if err != nil {
		return nil, fmt.Errorf("service: checkpoint: upsert memory: %w", err)
	}

	action := "updated"
	if created {
		action = "created"
	}

	return &model.CheckpointResponse{
		ID:     savedMem.ID,
		Action: action,
	}, nil
}
