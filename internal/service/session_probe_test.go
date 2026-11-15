package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

const probeProject = "probe-project"

// probeFixture is a real in-memory project database with the raw handle the
// tests need to backdate rows: no store write path keeps a caller-supplied
// created_at or updated_at.
type probeFixture struct {
	db    *db.DB
	store *store.MemoryStore
}

func newProbeFixture(t *testing.T) *probeFixture {
	t.Helper()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return &probeFixture{db: database, store: store.NewMemoryStore(database)}
}

// save stores a memory and stamps both created_at and updated_at with at.
func (f *probeFixture) save(t *testing.T, m *model.Memory, at time.Time) string {
	t.Helper()
	if m.Scope == "" {
		m.Scope = model.ScopeProject
	}
	if m.Content == "" {
		m.Content = "content"
	}
	if m.Title == "" {
		m.Title = "title"
	}
	m.DecayRate = 0.05
	saved, err := f.store.Create(context.Background(), m)
	if err != nil {
		t.Fatalf("create memory: %v", err)
	}
	stamp := at.UTC().Format(time.RFC3339Nano)
	if _, err := f.db.Exec("UPDATE memories SET created_at = ?, updated_at = ? WHERE id = ?", stamp, stamp, saved.ID); err != nil {
		t.Fatalf("backdate memory: %v", err)
	}
	return saved.ID
}

func (f *probeFixture) work(t *testing.T, project, sessionID string, at time.Time) string {
	t.Helper()
	return f.save(t, &model.Memory{Type: model.TypeDiscovery, Project: project, SessionID: sessionID}, at)
}

func (f *probeFixture) summary(t *testing.T, sessionID string, at time.Time) string {
	t.Helper()
	return f.save(t, &model.Memory{
		Type:      model.TypeSessionSummary,
		Project:   probeProject,
		SessionID: sessionID,
		TopicKey:  model.SessionSummaryTopicKey(sessionID),
	}, at)
}

func (f *probeFixture) checkpoint(t *testing.T, at time.Time) string {
	t.Helper()
	return f.save(t, &model.Memory{
		Type:     model.TypeSessionSummary,
		Project:  probeProject,
		TopicKey: "checkpoint/latest",
	}, at)
}

// TestSessionProbe_Window pins the work window of SPEC-183 §3.5 against real
// SQLite: the window starts at the first work memory after the latest close
// (session summary or project checkpoint), so neither an old checkpoint nor
// a close followed by a long pause ages it. Every instant sits in its own
// second (D20 caveat).
func TestSessionProbe_Window(t *testing.T) {
	const sid = "sess-probe"
	now := time.Now().UTC().Truncate(time.Second)
	at := func(minutesAgo int) time.Time { return now.Add(-time.Duration(minutesAgo) * time.Minute) }

	tests := []struct {
		name          string
		seed          func(t *testing.T, f *probeFixture)
		wantCount     int
		wantAnchor    time.Time
		wantLastAt    time.Time
		wantLastClose time.Time
	}{
		{
			name:      "no_memories",
			seed:      func(*testing.T, *probeFixture) {},
			wantCount: 0,
		},
		{
			name: "work_without_close",
			seed: func(t *testing.T, f *probeFixture) {
				f.work(t, probeProject, sid, at(45).Add(117554*time.Microsecond))
				f.work(t, probeProject, sid, at(30))
				f.work(t, probeProject, sid, at(10))
			},
			wantCount:  3,
			wantAnchor: at(45).Add(117554 * time.Microsecond),
			wantLastAt: at(10),
		},
		{
			name: "summary_after_work",
			seed: func(t *testing.T, f *probeFixture) {
				f.work(t, probeProject, sid, at(45))
				f.work(t, probeProject, sid, at(30))
				f.summary(t, sid, at(5))
			},
			wantCount:     0,
			wantLastClose: at(5),
		},
		{
			name: "summary_then_more_work",
			seed: func(t *testing.T, f *probeFixture) {
				f.work(t, probeProject, sid, at(60))
				f.summary(t, sid, at(40))
				f.work(t, probeProject, sid, at(35))
				f.work(t, probeProject, sid, at(20))
			},
			wantCount:     2,
			wantAnchor:    at(35),
			wantLastAt:    at(20),
			wantLastClose: at(40),
		},
		{
			name: "superseded_summary_still_closes",
			seed: func(t *testing.T, f *probeFixture) {
				f.work(t, probeProject, sid, at(45))
				sum := f.summary(t, sid, at(5))
				winner := f.work(t, probeProject, "", at(4))
				if _, err := f.db.Exec("UPDATE memories SET superseded_by = ? WHERE id = ?", winner, sum); err != nil {
					t.Fatalf("supersede summary: %v", err)
				}
			},
			wantCount:     0,
			wantLastClose: at(5),
		},
		{
			name: "checkpoint_counts_as_close",
			seed: func(t *testing.T, f *probeFixture) {
				f.work(t, probeProject, sid, at(45))
				f.checkpoint(t, at(5))
			},
			wantCount:     0,
			wantLastClose: at(5),
		},
		{
			name: "stale_checkpoint_and_recent_work",
			seed: func(t *testing.T, f *probeFixture) {
				f.checkpoint(t, now.Add(-72*time.Hour))
				f.work(t, probeProject, sid, at(10))
			},
			wantCount:     1,
			wantAnchor:    at(10),
			wantLastAt:    at(10),
			wantLastClose: now.Add(-72 * time.Hour),
		},
		{
			name: "last_close_is_max_of_both",
			seed: func(t *testing.T, f *probeFixture) {
				f.work(t, probeProject, sid, at(50))
				f.summary(t, sid, at(40))
				f.checkpoint(t, at(30))
				f.work(t, probeProject, sid, at(35))
				f.work(t, probeProject, sid, at(20))
			},
			wantCount:     1,
			wantAnchor:    at(20),
			wantLastAt:    at(20),
			wantLastClose: at(30),
		},
		{
			name: "other_project_not_seen",
			seed: func(t *testing.T, f *probeFixture) {
				f.work(t, "another-project", sid, at(45))
			},
			wantCount: 0,
		},
		{
			name: "memories_without_session_do_not_count",
			seed: func(t *testing.T, f *probeFixture) {
				f.work(t, probeProject, "", at(45))
			},
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newProbeFixture(t)
			tt.seed(t, f)

			win, err := service.NewSessionProbe(f.store).Window(context.Background(), probeProject, sid)
			if err != nil {
				t.Fatalf("Window: %v", err)
			}
			if win.SessionID != sid {
				t.Errorf("SessionID: got %q, want %q", win.SessionID, sid)
			}
			if win.Count != tt.wantCount {
				t.Errorf("Count: got %d, want %d", win.Count, tt.wantCount)
			}
			if !win.Anchor.Equal(tt.wantAnchor) {
				t.Errorf("Anchor: got %v, want %v", win.Anchor, tt.wantAnchor)
			}
			if !win.LastAt.Equal(tt.wantLastAt) {
				t.Errorf("LastAt: got %v, want %v", win.LastAt, tt.wantLastAt)
			}
			if !win.LastClose.Equal(tt.wantLastClose) {
				t.Errorf("LastClose: got %v, want %v", win.LastClose, tt.wantLastClose)
			}
		})
	}
}

// TestSessionProbe_Window_StoreError checks that a store failure is wrapped
// and returned, never reported as an empty window.
func TestSessionProbe_Window_StoreError(t *testing.T) {
	f := newProbeFixture(t)
	f.db.Close()
	if _, err := service.NewSessionProbe(f.store).Window(context.Background(), probeProject, "s"); err == nil {
		t.Fatal("Window on a closed database: want error, got nil")
	}
}
