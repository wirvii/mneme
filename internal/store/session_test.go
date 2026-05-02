package store

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/juanftp/mneme/internal/model"
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
