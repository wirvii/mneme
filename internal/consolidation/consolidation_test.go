package consolidation_test

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/consolidation"
	"github.com/juanftp/mneme/internal/db"
	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/store"
)

// discardLogger returns a slog.Logger that throws away all output, keeping
// test output clean while still exercising the logging code paths.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(nopWriter{}, nil))
}

// nopWriter is an io.Writer that discards all bytes.
type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

// ─── test helpers ─────────────────────────────────────────────────────────────

// newTestStore opens a fresh in-memory SQLite database, runs all migrations,
// and returns a MemoryStore. The database is closed automatically when the
// test finishes.
func newTestStore(t *testing.T) *store.MemoryStore {
	t.Helper()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("consolidation test: open memory db: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })
	return store.NewMemoryStore(database)
}

// testConfig returns a Config with small budgets so tests can exercise budget
// enforcement without creating thousands of records.
func testConfig() *config.Config {
	cfg := config.Default()
	cfg.Storage.ProjectBudget = 5
	cfg.Storage.GlobalBudget = 5
	cfg.Consolidation.RetentionDays = 30
	return cfg
}

// newPipeline returns a Pipeline backed by s with a discard logger and
// project "proj" (matching the project slug used in all test memories).
func newPipeline(s *store.MemoryStore) *consolidation.Pipeline {
	return consolidation.NewPipeline(s, testConfig(), discardLogger()).WithProject("proj")
}

// saveMemory is a shortcut that creates a memory and fails the test on error.
func saveMemory(t *testing.T, s *store.MemoryStore, m *model.Memory) *model.Memory {
	t.Helper()
	created, err := s.Create(context.Background(), m)
	if err != nil {
		t.Fatalf("consolidation test: save memory: %v", err)
	}
	return created
}

// ─── TestSweep ────────────────────────────────────────────────────────────────

// TestSweep verifies that memories with effective importance below 0.05 are
// soft-deleted and that memories above the threshold are left untouched.
func TestSweep(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Memory with very high decay rate and last accessed 200 days ago.
	// effective = 0.4 * exp(-0.05 * 200) ≈ 0.4 * exp(-10) ≈ 0.4 * 4.5e-5 ≈ ~0.0
	// Should be swept.
	old := time.Now().UTC().Add(-200 * 24 * time.Hour)
	stale := saveMemory(t, s, &model.Memory{
		Type:         model.TypeSessionSummary,
		Scope:        model.ScopeProject,
		Title:        "Stale session",
		Content:      "Old content",
		Project:      "proj",
		Importance:   0.4,
		DecayRate:    0.05,
		LastAccessed: &old,
	})

	// Memory with low decay rate and accessed recently.
	// effective = 0.9 * exp(-0.005 * 1) ≈ 0.895 — should survive.
	recent := time.Now().UTC().Add(-1 * 24 * time.Hour)
	fresh := saveMemory(t, s, &model.Memory{
		Type:         model.TypeArchitecture,
		Scope:        model.ScopeProject,
		Title:        "Architecture decision",
		Content:      "Important decision",
		Project:      "proj",
		Importance:   0.9,
		DecayRate:    0.005,
		LastAccessed: &recent,
	})

	p := newPipeline(s)
	result, err := p.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.Swept != 1 {
		t.Errorf("swept: want 1, got %d", result.Swept)
	}

	// Stale memory should be soft-deleted (Get returns nil for deleted memories).
	got, err := s.Get(ctx, stale.ID)
	if err != nil {
		t.Fatalf("Get stale: %v", err)
	}
	if got != nil {
		t.Errorf("stale memory should be soft-deleted, but Get returned it")
	}

	// Fresh memory must still be alive.
	got, err = s.Get(ctx, fresh.ID)
	if err != nil {
		t.Fatalf("Get fresh: %v", err)
	}
	if got == nil {
		t.Errorf("fresh memory should still be active")
	}
}

// ─── TestHardDelete ───────────────────────────────────────────────────────────

// TestHardDelete verifies that memories soft-deleted longer than RetentionDays
// are permanently removed, while recently soft-deleted memories are kept.
func TestHardDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Create a memory with a high decay rate so sweep removes it immediately.
	// We also back-date its last_accessed so it lands below threshold.
	ancient := time.Now().UTC().Add(-400 * 24 * time.Hour)
	m := saveMemory(t, s, &model.Memory{
		Type:         model.TypeSessionSummary,
		Scope:        model.ScopeProject,
		Title:        "Ancient session",
		Content:      "Very old content",
		Project:      "proj",
		Importance:   0.4,
		DecayRate:    0.05,
		LastAccessed: &ancient,
	})

	// Soft-delete it manually so we control the deleted_at timestamp.
	if err := s.SoftDelete(ctx, m.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	// Run the pipeline with RetentionDays=-1 so the cutoff is tomorrow, making
	// every soft-deleted record (regardless of when it was deleted) eligible
	// for hard deletion. This simulates the retention window having elapsed.
	cfg := testConfig()
	cfg.Consolidation.RetentionDays = -1

	p := consolidation.NewPipeline(s, cfg, discardLogger()).WithProject("proj")
	result, err := p.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.HardDeleted < 1 {
		t.Errorf("hard_deleted: want >=1, got %d", result.HardDeleted)
	}

	// The memory should be completely gone — CountTotal should be 0.
	total, err := s.CountTotal(ctx, "proj")
	if err != nil {
		t.Fatalf("CountTotal: %v", err)
	}
	if total != 0 {
		t.Errorf("CountTotal: want 0 after hard delete, got %d", total)
	}
}

// ─── TestDedup ────────────────────────────────────────────────────────────────

// TestDedup verifies that two memories with the same title in the same project
// are merged: the one with lower importance is marked as superseded.
func TestDedup(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	high := saveMemory(t, s, &model.Memory{
		Type:       model.TypeDecision,
		Scope:      model.ScopeProject,
		Title:      "Same title",
		Content:    "Higher importance content",
		Project:    "proj",
		Importance: 0.85,
		DecayRate:  0.005,
	})

	low := saveMemory(t, s, &model.Memory{
		Type:       model.TypeDecision,
		Scope:      model.ScopeProject,
		Title:      "Same title",
		Content:    "Lower importance content with unique detail",
		Project:    "proj",
		Importance: 0.5,
		DecayRate:  0.005,
	})

	// Use a config with a large budget and no decay so only dedup runs.
	cfg := testConfig()
	cfg.Storage.ProjectBudget = 1000
	p := consolidation.NewPipeline(s, cfg, discardLogger()).WithProject("proj")
	result, err := p.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.Duplicates != 1 {
		t.Errorf("duplicates: want 1, got %d", result.Duplicates)
	}

	// The winner (high importance) must still be retrievable.
	winner, err := s.Get(ctx, high.ID)
	if err != nil {
		t.Fatalf("Get winner: %v", err)
	}
	if winner == nil {
		t.Fatalf("winner memory should still be active")
	}
	if winner.SupersededBy != "" {
		t.Errorf("winner should not be superseded, but SupersededBy=%q", winner.SupersededBy)
	}

	// The loser (low importance) must be superseded by the winner.
	// We need to read superseded memories — use List with IncludeSuperseded.
	all, err := s.List(ctx, store.ListOptions{
		Project:           "proj",
		IncludeSuperseded: true,
		Limit:             100,
	})
	if err != nil {
		t.Fatalf("List with superseded: %v", err)
	}

	var loserFound bool
	for _, m := range all {
		if m.ID == low.ID {
			loserFound = true
			if m.SupersededBy != high.ID {
				t.Errorf("loser.SupersededBy: want %q, got %q", high.ID, m.SupersededBy)
			}
		}
	}
	if !loserFound {
		t.Errorf("loser memory not found in list with IncludeSuperseded=true")
	}
}

// ─── TestBudgetEnforcement ────────────────────────────────────────────────────

// TestBudgetEnforcement verifies that when the store exceeds the configured
// budget, the pipeline evicts the lowest-scored memories until the count is
// back within budget.
func TestBudgetEnforcement(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	cfg := testConfig()
	cfg.Storage.ProjectBudget = 3
	// Disable sweep so memories are not removed by decay (all have high importance).
	// We achieve this by using high importance + very low decay rate.
	for i := 0; i < 6; i++ {
		importance := 0.5 + float64(i)*0.05 // 0.5, 0.55, 0.60, 0.65, 0.70, 0.75
		saveMemory(t, s, &model.Memory{
			Type:       model.TypePattern,
			Scope:      model.ScopeProject,
			Title:      fmt.Sprintf("Pattern %d", i),
			Content:    "Some content",
			Project:    "proj",
			Importance: importance,
			DecayRate:  0.0, // no decay so sweep doesn't interfere
		})
	}

	p := consolidation.NewPipeline(s, cfg, discardLogger()).WithProject("proj")
	result, err := p.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.Evicted != 3 {
		t.Errorf("evicted: want 3, got %d", result.Evicted)
	}

	active, err := s.CountActive(ctx, "proj")
	if err != nil {
		t.Fatalf("CountActive: %v", err)
	}
	if active != 3 {
		t.Errorf("active after enforcement: want 3, got %d", active)
	}
}

// ─── TestRun_FullCycle ────────────────────────────────────────────────────────

// TestRun_FullCycle exercises all pipeline stages together and validates that
// ConsolidationResult contains accurate per-stage counts.
func TestRun_FullCycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	cfg := testConfig()
	cfg.Storage.ProjectBudget = 4 // will force eviction after sweep
	cfg.Consolidation.RetentionDays = -1 // hard-delete anything soft-deleted (cutoff = tomorrow)

	// 1. Two memories that will be soft-deleted by sweep (very old + fast decay).
	ancient := time.Now().UTC().Add(-500 * 24 * time.Hour)
	for i := 0; i < 2; i++ {
		saveMemory(t, s, &model.Memory{
			Type:         model.TypeSessionSummary,
			Scope:        model.ScopeProject,
			Title:        fmt.Sprintf("Stale %d", i),
			Content:      "Old content",
			Project:      "proj",
			Importance:   0.4,
			DecayRate:    0.05,
			LastAccessed: &ancient,
		})
	}

	// 2. Two duplicate memories (same title). One will be superseded.
	saveMemory(t, s, &model.Memory{
		Type:       model.TypeDecision,
		Scope:      model.ScopeProject,
		Title:      "Dup decision",
		Content:    "Primary content",
		Project:    "proj",
		Importance: 0.85,
		DecayRate:  0.005,
	})
	saveMemory(t, s, &model.Memory{
		Type:       model.TypeDecision,
		Scope:      model.ScopeProject,
		Title:      "Dup decision",
		Content:    "Secondary content with unique detail",
		Project:    "proj",
		Importance: 0.6,
		DecayRate:  0.005,
	})

	// 3. Several healthy memories that stay alive.
	for i := 0; i < 5; i++ {
		saveMemory(t, s, &model.Memory{
			Type:       model.TypeArchitecture,
			Scope:      model.ScopeProject,
			Title:      fmt.Sprintf("Arch %d", i),
			Content:    "Architecture content",
			Project:    "proj",
			Importance: 0.9,
			DecayRate:  0.0,
		})
	}

	p := consolidation.NewPipeline(s, cfg, discardLogger()).WithProject("proj")
	result, err := p.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Two stale memories should have been swept.
	if result.Swept != 2 {
		t.Errorf("swept: want 2, got %d", result.Swept)
	}

	// The two swept memories should have been hard-deleted (retention=0).
	if result.HardDeleted != 2 {
		t.Errorf("hard_deleted: want 2, got %d", result.HardDeleted)
	}

	// One duplicate pair resolved.
	if result.Duplicates != 1 {
		t.Errorf("duplicates: want 1, got %d", result.Duplicates)
	}

	// Budget is 4. After sweep (2 removed) and dedup (1 superseded), the active
	// non-superseded count is: 5 healthy + 1 dedup-winner = 6. 6 - 4 = 2 evicted.
	if result.Evicted != 2 {
		t.Errorf("evicted: want 2, got %d", result.Evicted)
	}

	if result.Duration <= 0 {
		t.Errorf("duration: want >0, got %s", result.Duration)
	}
}
