package service

import (
	"context"
	"fmt"
	"time"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/store"
)

// SessionProbe answers one read-only question for the Stop hook of SPEC-183:
// how much work has this session done since it last closed, and when did
// that work start. It is a separate, lightweight service rather than a
// MemoryService method because building a MemoryService starts the Hebbian
// pool and needs the global store and the embedder: weight with no use in a
// hook that must answer in well under a second over a read-only connection.
type SessionProbe struct {
	store *store.MemoryStore
}

// NewSessionProbe returns a probe over the project store st.
func NewSessionProbe(st *store.MemoryStore) *SessionProbe {
	return &SessionProbe{store: st}
}

// Window returns the session's current work window. The latest close is the
// newest updated_at between the session summary (session/<id>, superseded or
// not) and the project's checkpoint/latest, the same literal key
// MemoryService.Checkpoint upserts (a checkpoint counts as a close because
// it is exactly what an orchestrator that keeps working should do); the window is the work created
// after it, with the shared work predicate of the store. The probe never
// looks at the clock: whether the window is old enough to remind is the
// caller's decision.
func (p *SessionProbe) Window(ctx context.Context, project, sessionID string) (*model.SessionWindow, error) {
	var lastClose time.Time
	for _, key := range []string{model.SessionSummaryTopicKey(sessionID), "checkpoint/latest"} {
		m, err := p.store.GetByTopicKey(ctx, key, project)
		if err != nil {
			return nil, fmt.Errorf("service: session probe: read close %s: %w", key, err)
		}
		if m != nil && m.UpdatedAt.After(lastClose) {
			lastClose = m.UpdatedAt
		}
	}

	activity, err := p.store.GetSessionActivitySince(ctx, project, sessionID, lastClose)
	if err != nil {
		return nil, fmt.Errorf("service: session probe: window: %w", err)
	}
	return &model.SessionWindow{
		SessionID: sessionID,
		LastClose: lastClose,
		Count:     activity.MemoryCount,
		Anchor:    activity.FirstAt,
		LastAt:    activity.LastAt,
	}, nil
}
