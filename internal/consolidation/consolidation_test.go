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

// ─── Edge decay tests ─────────────────────────────────────────────────────────

// newPipelineWithDecay returns a Pipeline with edge decay enabled at the given
// rate and grace period (in days). The project budget is set high enough that
// budget enforcement does not interfere with these tests.
func newPipelineWithDecay(s *store.MemoryStore, rate float64, graceDays int) *consolidation.Pipeline {
	cfg := config.Default()
	cfg.Storage.ProjectBudget = 100000
	cfg.Storage.GlobalBudget = 100000
	cfg.Graph.EdgeDecayRate = rate
	cfg.Graph.EdgeDecayAfterDays = graceDays
	return consolidation.NewPipeline(s, cfg, discardLogger()).WithProject("proj")
}

// createEntities is a helper that creates two named entities and returns their IDs.
func createEntities(t *testing.T, s *store.MemoryStore, nameA, nameB string) (string, string) {
	t.Helper()
	ctx := context.Background()
	a, err := s.CreateEntity(ctx, &model.Entity{Name: nameA, Kind: model.KindModule, Project: "proj"})
	if err != nil {
		t.Fatalf("CreateEntity %q: %v", nameA, err)
	}
	b, err := s.CreateEntity(ctx, &model.Entity{Name: nameB, Kind: model.KindModule, Project: "proj"})
	if err != nil {
		t.Fatalf("CreateEntity %q: %v", nameB, err)
	}
	return a.ID, b.ID
}

// TestPipeline_EdgeDecay_Applied verifies that an old relation (60 days since
// last traversal) is decayed when EdgeDecayRate > 0.
func TestPipeline_EdgeDecay_Applied(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	aID, bID := createEntities(t, s, "svc-A", "svc-B")

	old := time.Now().UTC().AddDate(0, 0, -60)
	rel, err := s.CreateRelation(ctx, &model.Relation{
		SourceID:        aID,
		TargetID:        bID,
		Type:            model.RelRelatedTo,
		Weight:          0.5,
		LastTraversedAt: old,
	})
	if err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}

	p := newPipelineWithDecay(s, 0.02, 30)
	result, err := p.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.EdgeDecayed != 1 {
		t.Errorf("EdgeDecayed = %d, want 1", result.EdgeDecayed)
	}

	// Verify weight decreased.
	got, err := s.FindRelation(ctx, aID, bID, model.RelRelatedTo)
	if err != nil {
		t.Fatalf("FindRelation: %v", err)
	}
	if got == nil {
		t.Fatal("relation disappeared after decay")
	}
	if got.ID != rel.ID {
		t.Error("different relation returned after decay")
	}
	if got.Weight >= 0.5 {
		t.Errorf("weight = %f, want < 0.5 (decayed)", got.Weight)
	}
}

// TestPipeline_EdgeDecay_GracePeriodRespected verifies that a recently-traversed
// relation is not decayed when within the grace period.
func TestPipeline_EdgeDecay_GracePeriodRespected(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	aID, bID := createEntities(t, s, "svc-C", "svc-D")

	recent := time.Now().UTC().AddDate(0, 0, -10) // within 30-day grace
	rel, err := s.CreateRelation(ctx, &model.Relation{
		SourceID:        aID,
		TargetID:        bID,
		Type:            model.RelRelatedTo,
		Weight:          0.5,
		LastTraversedAt: recent,
	})
	if err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}

	p := newPipelineWithDecay(s, 0.02, 30)
	result, err := p.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.EdgeDecayed != 0 {
		t.Errorf("EdgeDecayed = %d, want 0 (within grace period)", result.EdgeDecayed)
	}

	got, err := s.FindRelation(ctx, aID, bID, model.RelRelatedTo)
	if err != nil {
		t.Fatalf("FindRelation: %v", err)
	}
	if got == nil || got.ID != rel.ID {
		t.Fatal("relation missing or changed after no-op decay")
	}
	if got.Weight != 0.5 {
		t.Errorf("weight = %f, want 0.5 (unchanged)", got.Weight)
	}
}

// TestPipeline_EdgeDecay_NullExcluded verifies that explicit relations
// (last_traversed_at IS NULL) are not decayed.
func TestPipeline_EdgeDecay_NullExcluded(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	aID, bID := createEntities(t, s, "svc-E", "svc-F")

	rel, err := s.CreateRelation(ctx, &model.Relation{
		SourceID: aID,
		TargetID: bID,
		Type:     model.RelDependsOn,
		Weight:   0.9,
		// LastTraversedAt is zero — stored as NULL.
	})
	if err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}

	p := newPipelineWithDecay(s, 0.02, 0) // no grace period, any traversed relation would decay
	result, err := p.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.EdgeDecayed != 0 {
		t.Errorf("EdgeDecayed = %d, want 0 (NULL excluded)", result.EdgeDecayed)
	}

	got, err := s.FindRelation(ctx, aID, bID, model.RelDependsOn)
	if err != nil {
		t.Fatalf("FindRelation: %v", err)
	}
	if got == nil || got.ID != rel.ID {
		t.Fatal("relation missing after no-op decay")
	}
	if got.Weight != 0.9 {
		t.Errorf("weight = %f, want 0.9 (unchanged)", got.Weight)
	}
}

// TestPipeline_EdgeDecay_RateZeroDisabled verifies that EdgeDecayRate=0
// disables edge decay entirely.
func TestPipeline_EdgeDecay_RateZeroDisabled(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	aID, bID := createEntities(t, s, "svc-G", "svc-H")
	old := time.Now().UTC().AddDate(0, 0, -60)
	_, err := s.CreateRelation(ctx, &model.Relation{
		SourceID:        aID,
		TargetID:        bID,
		Type:            model.RelRelatedTo,
		Weight:          0.5,
		LastTraversedAt: old,
	})
	if err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}

	p := newPipelineWithDecay(s, 0, 30) // rate=0 disables decay
	result, err := p.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.EdgeDecayed != 0 {
		t.Errorf("EdgeDecayed = %d, want 0 (rate=0 disables decay)", result.EdgeDecayed)
	}
}

// TestPipeline_Run_IncludesEdgeDecayField verifies that the EdgeDecayed field
// is present and zero-initialized in results (not a negative sentinel) even
// when no relations are eligible for decay.
func TestPipeline_Run_IncludesEdgeDecayField(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	p := newPipelineWithDecay(s, 0.02, 30)
	result, err := p.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.EdgeDecayed < 0 {
		t.Errorf("EdgeDecayed = %d, want >= 0", result.EdgeDecayed)
	}
}

// ─── Community detection step ─────────────────────────────────────────────────

// TestPipeline_Run_NilDetector verifies that when no community detector is
// wired the pipeline still completes successfully and community fields are 0.
func TestPipeline_Run_NilDetector(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Pipeline created without WithCommunityDetector (detector is nil).
	p := newPipeline(s)
	result, err := p.Run(ctx)
	if err != nil {
		t.Fatalf("Run with nil detector: %v", err)
	}
	if result.CommunitiesDetected != 0 {
		t.Errorf("CommunitiesDetected = %d, want 0 (no detector)", result.CommunitiesDetected)
	}
	if result.CommunitiesNew != 0 {
		t.Errorf("CommunitiesNew = %d, want 0 (no detector)", result.CommunitiesNew)
	}
	if result.CommunitiesDeleted != 0 {
		t.Errorf("CommunitiesDeleted = %d, want 0 (no detector)", result.CommunitiesDeleted)
	}
}

// TestPipeline_Run_IncludesCommunityDetection verifies that when a community
// detector is wired its result is merged into ConsolidationResult.
func TestPipeline_Run_IncludesCommunityDetection(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Fake detector that returns a predictable DetectionResult.
	fakeDetector := consolidation.CommunityDetector(func(_ context.Context) (*model.DetectionResult, error) {
		return &model.DetectionResult{
			NewCommunities:     3,
			UpdatedCommunities: 1,
			DeletedCommunities: 2,
			TotalCommunities:   4, // new + updated
		}, nil
	})

	p := newPipeline(s).WithCommunityDetector(fakeDetector)
	result, err := p.Run(ctx)
	if err != nil {
		t.Fatalf("Run with detector: %v", err)
	}
	if result.CommunitiesDetected != 4 {
		t.Errorf("CommunitiesDetected = %d, want 4", result.CommunitiesDetected)
	}
	if result.CommunitiesNew != 3 {
		t.Errorf("CommunitiesNew = %d, want 3", result.CommunitiesNew)
	}
	if result.CommunitiesDeleted != 2 {
		t.Errorf("CommunitiesDeleted = %d, want 2", result.CommunitiesDeleted)
	}
}

// TestPipeline_Run_CommunityDetectionError verifies that an error from the
// detector is surfaced in the Run return value and the partial result is still
// populated with the steps that completed before the failure.
func TestPipeline_Run_CommunityDetectionError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	errDetector := consolidation.CommunityDetector(func(_ context.Context) (*model.DetectionResult, error) {
		return nil, fmt.Errorf("detector: simulated failure")
	})

	p := newPipeline(s).WithCommunityDetector(errDetector)
	result, err := p.Run(ctx)
	if err == nil {
		t.Fatal("expected error from failing detector, got nil")
	}
	// The steps that ran before detectCommunities (sweep, edgeDecay) should
	// have contributed non-negative counts to the partial result.
	if result == nil {
		t.Fatal("expected non-nil partial result on error")
	}
}
