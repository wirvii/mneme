package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/wirvii/mneme/internal/model"
)

func newSessionID(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("generate session id: %v", err)
	}
	return id.String()
}

// TestCreateSession verifies that a session is persisted with all fields.
func TestCreateSession(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	sess := &model.Session{
		ID:        newSessionID(t),
		Project:   "myproject",
		Agent:     "claude-code",
		StartedAt: time.Now().UTC().Truncate(time.Second),
	}

	got, err := s.CreateSession(ctx, sess)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if got.ID != sess.ID {
		t.Errorf("ID mismatch: got %s, want %s", got.ID, sess.ID)
	}
	if got.Project != sess.Project {
		t.Errorf("Project mismatch: got %q, want %q", got.Project, sess.Project)
	}
	if got.Agent != sess.Agent {
		t.Errorf("Agent mismatch: got %q, want %q", got.Agent, sess.Agent)
	}
}

// TestEndSession verifies that ended_at and summary_id are set correctly.
func TestEndSession(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	sess := &model.Session{
		ID:        newSessionID(t),
		Project:   "myproject",
		Agent:     "claude-code",
		StartedAt: time.Now().UTC(),
	}
	if _, err := s.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// summary_id is a FK to memories(id), so we need a real memory.
	summary, err := s.Create(ctx, &model.Memory{
		Type:      model.TypeSessionSummary,
		Scope:     model.ScopeProject,
		Title:     "Session summary",
		Content:   "What happened in this session.",
		Project:   "myproject",
		DecayRate: 0.05,
	})
	if err != nil {
		t.Fatalf("Create summary memory: %v", err)
	}

	if err := s.EndSession(ctx, sess.ID, summary.ID); err != nil {
		t.Fatalf("EndSession: %v", err)
	}

	got, err := s.GetLastSession(ctx, sess.Project)
	if err != nil {
		t.Fatalf("GetLastSession: %v", err)
	}
	if got == nil {
		t.Fatal("expected session, got nil")
	}
	if got.EndedAt == nil {
		t.Error("expected EndedAt to be set after EndSession")
	}
	if got.SummaryID != summary.ID {
		t.Errorf("SummaryID mismatch: got %q, want %q", got.SummaryID, summary.ID)
	}
}

// TestGetLastSession verifies that the most recently started session is returned.
func TestGetLastSession(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	base := time.Now().UTC().Add(-time.Hour)

	sessions := []*model.Session{
		{ID: newSessionID(t), Project: "myproject", Agent: "a", StartedAt: base},
		{ID: newSessionID(t), Project: "myproject", Agent: "b", StartedAt: base.Add(10 * time.Minute)},
		{ID: newSessionID(t), Project: "myproject", Agent: "c", StartedAt: base.Add(20 * time.Minute)},
	}

	for _, sess := range sessions {
		if _, err := s.CreateSession(ctx, sess); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
	}

	got, err := s.GetLastSession(ctx, "myproject")
	if err != nil {
		t.Fatalf("GetLastSession: %v", err)
	}
	if got == nil {
		t.Fatal("expected session, got nil")
	}
	if got.Agent != "c" {
		t.Errorf("expected agent=c (most recent), got %q", got.Agent)
	}
}

// TestListSessionsByProject verifies that all sessions for a given project are
// returned in ascending started_at order, and that sessions from other projects
// are not included.
func TestListSessionsByProject(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	base := time.Now().UTC().Add(-2 * time.Hour)

	// Three sessions for "proj-a", one for "proj-b".
	projA := []*model.Session{
		{ID: newSessionID(t), Project: "proj-a", Agent: "a", StartedAt: base},
		{ID: newSessionID(t), Project: "proj-a", Agent: "b", StartedAt: base.Add(time.Hour)},
		{ID: newSessionID(t), Project: "proj-a", Agent: "c", StartedAt: base.Add(2 * time.Hour)},
	}
	projB := &model.Session{ID: newSessionID(t), Project: "proj-b", Agent: "x", StartedAt: base}

	for _, sess := range projA {
		if _, err := s.CreateSession(ctx, sess); err != nil {
			t.Fatalf("CreateSession proj-a: %v", err)
		}
	}
	if _, err := s.CreateSession(ctx, projB); err != nil {
		t.Fatalf("CreateSession proj-b: %v", err)
	}

	got, err := s.ListSessionsByProject(ctx, "proj-a")
	if err != nil {
		t.Fatalf("ListSessionsByProject: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len: got %d, want 3", len(got))
	}
	// Results must be ascending by started_at.
	if got[0].Agent != "a" || got[1].Agent != "b" || got[2].Agent != "c" {
		t.Errorf("order wrong: agents = %q %q %q", got[0].Agent, got[1].Agent, got[2].Agent)
	}
	for _, sess := range got {
		if sess.Project != "proj-a" {
			t.Errorf("unexpected project %q in results", sess.Project)
		}
	}
}

// TestListSessionsByProject_Empty verifies that an empty slice (not nil error) is
// returned when no sessions exist for the requested project.
func TestListSessionsByProject_Empty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	got, err := s.ListSessionsByProject(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 sessions, got %d", len(got))
	}
}

// TestGetLastSession_None verifies that GetLastSession returns nil, nil when no sessions exist.
func TestGetLastSession_None(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	got, err := s.GetLastSession(ctx, "no-such-project")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil session, got %+v", got)
	}
}

// backdateMemory sets a memory's created_at directly via raw SQL, since no
// store write path preserves a caller-supplied created_at (Create/CreateWithID
// always stamp time.Now(), memory.go:74-80). Only store package tests can do
// this — s.db is unexported (SPEC-108 plan §0.2).
func backdateMemory(t *testing.T, s *MemoryStore, id string, ts time.Time) {
	t.Helper()
	_, err := s.db.ExecContext(context.Background(),
		"UPDATE memories SET created_at = ? WHERE id = ?",
		ts.UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		t.Fatalf("backdate memory %s: %v", id, err)
	}
}

// createSessionWork creates a memory of type discovery attributed to the
// given project/sessionID, backdated to ts, and returns its ID.
func createSessionWork(t *testing.T, s *MemoryStore, project, sessionID string, ts time.Time) string {
	t.Helper()
	ctx := context.Background()
	m, err := s.Create(ctx, &model.Memory{
		Type:      model.TypeDiscovery,
		Scope:     model.ScopeProject,
		Title:     "work",
		Content:   "work content",
		Project:   project,
		SessionID: sessionID,
		DecayRate: 0.05,
	})
	if err != nil {
		t.Fatalf("create session work memory: %v", err)
	}
	backdateMemory(t, s, m.ID, ts)
	return m.ID
}

// createSessionSummary creates the session_summary memory SessionEnd would
// have written for sessionID, with the real topic_key it uses to close a
// session.
func createSessionSummary(t *testing.T, s *MemoryStore, project, sessionID string) string {
	t.Helper()
	ctx := context.Background()
	m, err := s.Create(ctx, &model.Memory{
		Type:      model.TypeSessionSummary,
		Scope:     model.ScopeProject,
		Title:     "Session summary: " + sessionID,
		Content:   "summary content",
		TopicKey:  model.SessionSummaryTopicKey(sessionID),
		Project:   project,
		SessionID: sessionID,
		DecayRate: 0.05,
	})
	if err != nil {
		t.Fatalf("create session summary memory: %v", err)
	}
	return m.ID
}

// TestGetSessionActivity verifies the count and FirstAt/LastAt extremes for a
// single session, and that session_summary memories are excluded from the
// count (AC2).
func TestGetSessionActivity(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	sessionID := newSessionID(t)
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)

	createSessionWork(t, s, "myproject", sessionID, base)
	createSessionWork(t, s, "myproject", sessionID, base.Add(10*time.Minute))
	createSessionWork(t, s, "myproject", sessionID, base.Add(20*time.Minute))
	// The summary itself must not count as work.
	createSessionSummary(t, s, "myproject", sessionID)

	got, err := s.GetSessionActivity(ctx, "myproject", sessionID)
	if err != nil {
		t.Fatalf("GetSessionActivity: %v", err)
	}
	if got.MemoryCount != 3 {
		t.Errorf("MemoryCount: got %d, want 3", got.MemoryCount)
	}
	if !got.FirstAt.Equal(base) {
		t.Errorf("FirstAt: got %v, want %v", got.FirstAt, base)
	}
	if !got.LastAt.Equal(base.Add(20 * time.Minute)) {
		t.Errorf("LastAt: got %v, want %v", got.LastAt, base.Add(20*time.Minute))
	}
}

// TestGetSessionActivity_NoRows verifies MemoryCount 0 with zero times and no
// error when the session left no work.
func TestGetSessionActivity_NoRows(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	got, err := s.GetSessionActivity(ctx, "myproject", "no-such-session")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if got.MemoryCount != 0 {
		t.Errorf("MemoryCount: got %d, want 0", got.MemoryCount)
	}
	if !got.FirstAt.IsZero() || !got.LastAt.IsZero() {
		t.Errorf("expected zero FirstAt/LastAt, got %v / %v", got.FirstAt, got.LastAt)
	}
}

// TestListSessionsWithoutSummary verifies one row per orphaned session,
// ordered by last activity DESC (AC3).
func TestListSessionsWithoutSummary(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	base := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)

	older := newSessionID(t)
	newer := newSessionID(t)
	createSessionWork(t, s, "myproject", older, base)
	createSessionWork(t, s, "myproject", newer, base.Add(time.Hour))

	got, err := s.ListSessionsWithoutSummary(ctx, "myproject")
	if err != nil {
		t.Fatalf("ListSessionsWithoutSummary: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len: got %d, want 2", len(got))
	}
	if got[0].SessionID != newer {
		t.Errorf("expected most recent session first, got %q", got[0].SessionID)
	}
	if got[1].SessionID != older {
		t.Errorf("expected older session second, got %q", got[1].SessionID)
	}
}

// TestSessionWork_ExcludesSoftDeleted verifies that soft-deleted work memories
// are excluded from both the list and the count (AC4).
func TestSessionWork_ExcludesSoftDeleted(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	sessionID := newSessionID(t)
	id := createSessionWork(t, s, "myproject", sessionID, time.Now().UTC())
	if err := s.SoftDelete(ctx, id); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	activity, err := s.GetSessionActivity(ctx, "myproject", sessionID)
	if err != nil {
		t.Fatalf("GetSessionActivity: %v", err)
	}
	if activity.MemoryCount != 0 {
		t.Errorf("MemoryCount: got %d, want 0 after soft delete", activity.MemoryCount)
	}

	list, err := s.ListSessionsWithoutSummary(ctx, "myproject")
	if err != nil {
		t.Fatalf("ListSessionsWithoutSummary: %v", err)
	}
	for _, a := range list {
		if a.SessionID == sessionID {
			t.Errorf("expected session %q to be absent after soft delete, found it", sessionID)
		}
	}
}

// TestSessionWork_ExcludesSuperseded verifies that a session whose only work
// was fully superseded converges to silence rather than nagging forever
// (AC5).
func TestSessionWork_ExcludesSuperseded(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	sessionID := newSessionID(t)
	id := createSessionWork(t, s, "myproject", sessionID, time.Now().UTC())
	winner, err := s.Create(ctx, &model.Memory{
		Type:      model.TypeDiscovery,
		Scope:     model.ScopeProject,
		Title:     "winner",
		Content:   "newer version",
		Project:   "myproject",
		DecayRate: 0.05,
	})
	if err != nil {
		t.Fatalf("create winner memory: %v", err)
	}
	if err := s.SetSupersededBy(ctx, id, winner.ID); err != nil {
		t.Fatalf("SetSupersededBy: %v", err)
	}

	activity, err := s.GetSessionActivity(ctx, "myproject", sessionID)
	if err != nil {
		t.Fatalf("GetSessionActivity: %v", err)
	}
	if activity.MemoryCount != 0 {
		t.Errorf("MemoryCount: got %d, want 0 after supersession", activity.MemoryCount)
	}

	list, err := s.ListSessionsWithoutSummary(ctx, "myproject")
	if err != nil {
		t.Fatalf("ListSessionsWithoutSummary: %v", err)
	}
	for _, a := range list {
		if a.SessionID == sessionID {
			t.Errorf("expected session %q to be absent after full supersession, found it", sessionID)
		}
	}
}

// TestSummaryExistence_SupersededSummaryStillCloses verifies the deliberate
// asymmetry of D7/D8: the WORK count filters BOTH deleted_at and
// superseded_by, but the SUMMARY-existence check filters ONLY deleted_at. A
// superseded summary still proves the session was closed — if this NOT
// EXISTS filtered superseded_by, a summary displaced by a synthesis would
// leave the notice nagging forever — SPEC-105 en miniatura (AC6).
func TestSummaryExistence_SupersededSummaryStillCloses(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	sessionID := newSessionID(t)
	createSessionWork(t, s, "myproject", sessionID, time.Now().UTC())
	summaryID := createSessionSummary(t, s, "myproject", sessionID)

	winner, err := s.Create(ctx, &model.Memory{
		Type:      model.TypeSynthesis,
		Scope:     model.ScopeProject,
		Title:     "synthesis",
		Content:   "displaces the summary",
		Project:   "myproject",
		DecayRate: 0.05,
	})
	if err != nil {
		t.Fatalf("create synthesis memory: %v", err)
	}
	if err := s.SetSupersededBy(ctx, summaryID, winner.ID); err != nil {
		t.Fatalf("SetSupersededBy: %v", err)
	}

	list, err := s.ListSessionsWithoutSummary(ctx, "myproject")
	if err != nil {
		t.Fatalf("ListSessionsWithoutSummary: %v", err)
	}
	for _, a := range list {
		if a.SessionID == sessionID {
			t.Fatalf("expected session %q to still be closed (superseded summary), but it was listed as orphaned", sessionID)
		}
	}
}

// TestSummaryExistence_SoftDeletedSummaryReopens verifies the other half of
// the asymmetry: a soft-deleted summary is NOT a proof of closure, so the
// session reopens (AC7).
func TestSummaryExistence_SoftDeletedSummaryReopens(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	sessionID := newSessionID(t)
	createSessionWork(t, s, "myproject", sessionID, time.Now().UTC())
	summaryID := createSessionSummary(t, s, "myproject", sessionID)

	if err := s.SoftDelete(ctx, summaryID); err != nil {
		t.Fatalf("SoftDelete summary: %v", err)
	}

	list, err := s.ListSessionsWithoutSummary(ctx, "myproject")
	if err != nil {
		t.Fatalf("ListSessionsWithoutSummary: %v", err)
	}
	found := false
	for _, a := range list {
		if a.SessionID == sessionID {
			found = true
		}
	}
	if !found {
		t.Errorf("expected session %q to reopen after its summary was soft-deleted", sessionID)
	}
}

// TestSessionQueries_ShareSameWherePredicate is the anti-divergence guard
// (AC8): both listSessionsWithoutSummaryQuery and getSessionActivityQuery
// MUST embed the exact same sessionWorkWhere fragment, or the asymmetry that
// left a divergent guard forever in SPEC-105 reappears here.
func TestSessionQueries_ShareSameWherePredicate(t *testing.T) {
	if !strings.Contains(listSessionsWithoutSummaryQuery, sessionWorkWhere) {
		t.Error("listSessionsWithoutSummaryQuery does not embed sessionWorkWhere")
	}
	if !strings.Contains(getSessionActivityQuery, sessionWorkWhere) {
		t.Error("getSessionActivityQuery does not embed sessionWorkWhere")
	}
}

// TestSessionQueries_NoInlineTopicKeyLiteral verifies that neither query
// contains the inline literal "session/" — the topic_key prefix must always
// travel as a bound SQL parameter (AC9, SPEC-108 D9).
func TestSessionQueries_NoInlineTopicKeyLiteral(t *testing.T) {
	if strings.Contains(listSessionsWithoutSummaryQuery, "session/") {
		t.Error("listSessionsWithoutSummaryQuery contains an inline \"session/\" literal")
	}
	if strings.Contains(getSessionActivityQuery, "session/") {
		t.Error("getSessionActivityQuery contains an inline \"session/\" literal")
	}
}

// TestSessionWork_ProjectScoped verifies that memories from another project
// in the same DB never contaminate a session's activity or orphan listing
// (AC10).
func TestSessionWork_ProjectScoped(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	sessionID := newSessionID(t)
	createSessionWork(t, s, "other-project", sessionID, time.Now().UTC())

	activity, err := s.GetSessionActivity(ctx, "myproject", sessionID)
	if err != nil {
		t.Fatalf("GetSessionActivity: %v", err)
	}
	if activity.MemoryCount != 0 {
		t.Errorf("MemoryCount: got %d, want 0 (work belongs to another project)", activity.MemoryCount)
	}

	list, err := s.ListSessionsWithoutSummary(ctx, "myproject")
	if err != nil {
		t.Fatalf("ListSessionsWithoutSummary: %v", err)
	}
	for _, a := range list {
		if a.SessionID == sessionID {
			t.Errorf("expected session %q from another project to be absent", sessionID)
		}
	}
}
