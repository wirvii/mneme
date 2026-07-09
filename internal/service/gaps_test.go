package service_test

import (
	"context"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// newGapsTestService returns a MemoryService with wikilinks enabled and direct
// access to both underlying stores for test setup.
func newGapsTestService(t *testing.T) (*service.MemoryService, *store.MemoryStore, *store.MemoryStore) {
	t.Helper()
	projectDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open project db: %v", err)
	}
	globalDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open global db: %v", err)
	}
	t.Cleanup(func() { projectDB.Close(); globalDB.Close() })

	ps := store.NewMemoryStore(projectDB)
	gs := store.NewMemoryStore(globalDB)
	cfg := config.Default()
	cfg.Graph.WikilinksEnabled = true
	cfg.Graph.WikilinkRelationWeight = 0.6

	svc := service.NewMemoryService(ps, gs, cfg, "test/project", embed.NopEmbedder{})
	return svc, ps, gs
}

// saveWithWikilink saves a memory containing a [[wikilink]] to an unresolved
// topic_key, which triggers unresolved_reference registration in the service.
func saveWithWikilink(t *testing.T, svc *service.MemoryService, title, topicKey, targetGap string) {
	t.Helper()
	_, err := svc.Save(context.Background(), model.SaveRequest{
		Title:    title,
		Content:  "References [[" + targetGap + "]] for more context.",
		TopicKey: topicKey,
		Type:     model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("saveWithWikilink %q -> %q: %v", topicKey, targetGap, err)
	}
}

// --------------------------------------------------------------------------
// TestGaps_ProjectScope
// --------------------------------------------------------------------------

// TestGaps_ProjectScope verifies that the default scope (project) returns gaps
// from the project store only.
func TestGaps_ProjectScope(t *testing.T) {
	svc, _, _ := newGapsTestService(t)
	ctx := context.Background()

	saveWithWikilink(t, svc, "Src1", "src/one", "missing/gap-one")
	saveWithWikilink(t, svc, "Src2", "src/two", "missing/gap-two")

	resp, err := svc.Gaps(ctx, model.GapsRequest{Scope: "project"})
	if err != nil {
		t.Fatalf("Gaps: %v", err)
	}
	if resp.Total < 2 {
		t.Errorf("Total = %d, want >= 2", resp.Total)
	}
	if len(resp.Gaps) < 2 {
		t.Errorf("len(Gaps) = %d, want >= 2", len(resp.Gaps))
	}
}

// --------------------------------------------------------------------------
// TestGaps_IncludeSamplesFalse
// --------------------------------------------------------------------------

// TestGaps_IncludeSamplesFalse verifies that when include_samples is false,
// the Samples slice is empty (not populated).
func TestGaps_IncludeSamplesFalse(t *testing.T) {
	svc, _, _ := newGapsTestService(t)
	ctx := context.Background()

	saveWithWikilink(t, svc, "Src", "src/a", "missing/gap")

	f := false
	resp, err := svc.Gaps(ctx, model.GapsRequest{
		Scope:          "project",
		IncludeSamples: &f,
	})
	if err != nil {
		t.Fatalf("Gaps(include_samples=false): %v", err)
	}
	if len(resp.Gaps) == 0 {
		t.Fatal("expected at least one gap")
	}
	if len(resp.Gaps[0].Samples) != 0 {
		t.Errorf("Samples should be empty when include_samples=false, got %d", len(resp.Gaps[0].Samples))
	}
}

// --------------------------------------------------------------------------
// TestGaps_LimitClamp
// --------------------------------------------------------------------------

// TestGaps_LimitClamp verifies that a limit above 100 is clamped to 100.
func TestGaps_LimitClamp(t *testing.T) {
	svc, _, _ := newGapsTestService(t)
	ctx := context.Background()

	// Create enough unique gaps to make clamping meaningful.
	for i := range 5 {
		key := "src/mem-" + string(rune('a'+i))
		saveWithWikilink(t, svc, "Src"+key, key, "missing/gap-"+string(rune('a'+i)))
	}

	// Requesting 999 should not panic and should return at most 100.
	resp, err := svc.Gaps(ctx, model.GapsRequest{Scope: "project", Limit: 999})
	if err != nil {
		t.Fatalf("Gaps(limit=999): %v", err)
	}
	// The actual gaps count is 5; the important thing is no error and count <= 100.
	if len(resp.Gaps) > 100 {
		t.Errorf("len(Gaps) = %d, should be <= 100 after clamp", len(resp.Gaps))
	}
}

// --------------------------------------------------------------------------
// TestGaps_EmptyProject
// --------------------------------------------------------------------------

// TestGaps_EmptyProject verifies that a project with no unresolved references
// returns an empty slice (not nil) and total=0.
func TestGaps_EmptyProject(t *testing.T) {
	svc, _, _ := newGapsTestService(t)
	ctx := context.Background()

	resp, err := svc.Gaps(ctx, model.GapsRequest{Scope: "project"})
	if err != nil {
		t.Fatalf("Gaps empty: %v", err)
	}
	if resp.Total != 0 {
		t.Errorf("Total = %d, want 0", resp.Total)
	}
	if resp.Gaps == nil {
		t.Error("Gaps should be empty slice, not nil")
	}
	if len(resp.Gaps) != 0 {
		t.Errorf("len(Gaps) = %d, want 0", len(resp.Gaps))
	}
}

// --------------------------------------------------------------------------
// TestGaps_WithSamples
// --------------------------------------------------------------------------

// TestGaps_WithSamples verifies that samples are populated when include_samples
// defaults to true.
func TestGaps_WithSamples(t *testing.T) {
	svc, _, _ := newGapsTestService(t)
	ctx := context.Background()

	saveWithWikilink(t, svc, "Src1", "src/alpha", "missing/target")
	saveWithWikilink(t, svc, "Src2", "src/beta", "missing/target")

	resp, err := svc.Gaps(ctx, model.GapsRequest{Scope: "project"})
	if err != nil {
		t.Fatalf("Gaps: %v", err)
	}
	if len(resp.Gaps) == 0 {
		t.Fatal("expected at least one gap")
	}
	if len(resp.Gaps[0].Samples) == 0 {
		t.Error("expected samples to be populated by default")
	}
}

// --------------------------------------------------------------------------
// TestStats_KnowledgeGaps
// --------------------------------------------------------------------------

// TestStats_KnowledgeGaps verifies that the Stats response includes a
// KnowledgeGaps field when unresolved references exist.
func TestStats_KnowledgeGaps(t *testing.T) {
	svc, _, _ := newGapsTestService(t)
	ctx := context.Background()

	saveWithWikilink(t, svc, "Source", "src/stats", "missing/stats-gap")

	stats, err := svc.Stats(ctx, "test/project")
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.KnowledgeGaps == nil {
		t.Fatal("KnowledgeGaps should not be nil when unresolved refs exist")
	}
	if stats.KnowledgeGaps.Total < 1 {
		t.Errorf("KnowledgeGaps.Total = %d, want >= 1", stats.KnowledgeGaps.Total)
	}
	if len(stats.KnowledgeGaps.Top) == 0 {
		t.Error("KnowledgeGaps.Top should not be empty")
	}
}

// --------------------------------------------------------------------------
// TestStats_NoGaps
// --------------------------------------------------------------------------

// TestStats_NoGaps verifies that KnowledgeGaps is nil in Stats when there are
// no unresolved references.
func TestStats_NoGaps(t *testing.T) {
	svc, _, _ := newGapsTestService(t)
	ctx := context.Background()

	// Save a memory without any wikilinks so no unresolved refs are created.
	_, err := svc.Save(ctx, model.SaveRequest{
		Title:   "Plain memory",
		Content: "No wikilinks here.",
		Type:    model.TypeDiscovery,
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	stats, err := svc.Stats(ctx, "test/project")
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.KnowledgeGaps != nil {
		t.Errorf("KnowledgeGaps should be nil when no unresolved refs exist, got %+v", stats.KnowledgeGaps)
	}
}
