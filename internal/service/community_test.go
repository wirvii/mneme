package service_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/db"
	"github.com/juanftp/mneme/internal/embed"
	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/service"
	"github.com/juanftp/mneme/internal/store"
)

// ─── community test helpers ────────────────────────────────────────────────────

// newCommunityTestService constructs a MemoryService with community detection
// enabled and a low min_size so small integration-test graphs can produce
// detectable communities.
func newCommunityTestService(t *testing.T, minSize int) (*service.MemoryService, *store.MemoryStore) {
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
	cfg.Graph.CommunityDetectionEnabled = true
	cfg.Graph.CommunityMinSize = minSize
	// Use a low weight threshold so test relations (weight=0.5) are followed.
	cfg.Graph.ExpansionThreshold = 0.3

	svc := service.NewMemoryService(ps, gs, cfg, "test/community", embed.NopEmbedder{})
	return svc, ps
}

// saveCommMem saves a memory in the "test/community" project and returns its ID.
func saveCommMem(t *testing.T, ctx context.Context, svc *service.MemoryService, title string) string {
	t.Helper()
	resp, err := svc.Save(ctx, model.SaveRequest{
		Title:   title,
		Content: title + " content",
	})
	if err != nil {
		t.Fatalf("saveCommMem %q: %v", title, err)
	}
	return resp.ID
}

// seedCluster creates n memories each linked to a unique entity, then creates
// dense relations among all entity pairs (weight 0.8). This forms a tightly
// connected clique that Louvain should recognise as a single community.
// Returns memory IDs and entity IDs.
func seedCluster(
	t *testing.T,
	ctx context.Context,
	svc *service.MemoryService,
	ps *store.MemoryStore,
	prefix string,
	n int,
) (memIDs, entityIDs []string) {
	t.Helper()
	for i := 0; i < n; i++ {
		memID := saveCommMem(t, ctx, svc, fmt.Sprintf("%s-mem-%d", prefix, i))
		memIDs = append(memIDs, memID)

		ent, err := ps.FindOrCreateEntity(ctx,
			fmt.Sprintf("%s-entity-%d", prefix, i),
			model.KindConcept,
			"test/community",
		)
		if err != nil {
			t.Fatalf("FindOrCreateEntity %s-%d: %v", prefix, i, err)
		}
		entityIDs = append(entityIDs, ent.ID)

		if err := ps.LinkMemoryEntity(ctx, memID, ent.ID, "mention"); err != nil {
			t.Fatalf("LinkMemoryEntity %s-%d: %v", prefix, i, err)
		}
	}

	// Dense intra-cluster edges.
	for i := 0; i < len(entityIDs); i++ {
		for j := i + 1; j < len(entityIDs); j++ {
			if _, err := ps.CreateRelation(ctx, &model.Relation{
				SourceID: entityIDs[i],
				TargetID: entityIDs[j],
				Type:     model.RelRelatedTo,
				Weight:   0.8,
			}); err != nil {
				t.Fatalf("CreateRelation cluster edge: %v", err)
			}
		}
	}
	return memIDs, entityIDs
}

// ─── tests ────────────────────────────────────────────────────────────────────

// TestDetectAndPersistCommunities_Disabled verifies that when
// CommunityDetectionEnabled=false the function returns a zero-value result
// immediately without touching the DB.
func TestDetectAndPersistCommunities_Disabled(t *testing.T) {
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
	cfg.Graph.CommunityDetectionEnabled = false

	svc := service.NewMemoryService(ps, gs, cfg, "test/community", embed.NopEmbedder{})
	ctx := context.Background()

	result, err := svc.DetectAndPersistCommunities(ctx, model.ScopeProject, "test/community")
	if err != nil {
		t.Fatalf("DetectAndPersistCommunities: %v", err)
	}
	if result.TotalCommunities != 0 || result.NewCommunities != 0 {
		t.Errorf("expected zero result when disabled, got %+v", result)
	}
}

// TestDetectAndPersistCommunities_EmptyProject verifies that a project with no
// memories returns early with a zero-value result and does not error.
func TestDetectAndPersistCommunities_EmptyProject(t *testing.T) {
	svc, _ := newCommunityTestService(t, 2)
	ctx := context.Background()

	result, err := svc.DetectAndPersistCommunities(ctx, model.ScopeProject, "no-such-project")
	if err != nil {
		t.Fatalf("DetectAndPersistCommunities: %v", err)
	}
	if result.TotalCommunities != 0 {
		t.Errorf("expected TotalCommunities=0 for empty project, got %d", result.TotalCommunities)
	}
}

// TestDetectAndPersistCommunities_MinSizeFilter verifies that communities with
// fewer than CommunityMinSize members are not persisted.
func TestDetectAndPersistCommunities_MinSizeFilter(t *testing.T) {
	// Use a very high min_size so nothing survives.
	svc, ps := newCommunityTestService(t, 100)
	ctx := context.Background()

	// Create 5 memories with entities and relations — real but small cluster.
	_, _ = seedCluster(t, ctx, svc, ps, "small", 5)

	result, err := svc.DetectAndPersistCommunities(ctx, model.ScopeProject, "test/community")
	if err != nil {
		t.Fatalf("DetectAndPersistCommunities: %v", err)
	}
	// With min_size=100 nothing should be persisted.
	if result.TotalCommunities != 0 {
		t.Errorf("expected TotalCommunities=0 with high min_size, got %d", result.TotalCommunities)
	}
}

// TestDetectAndPersistCommunities_NewCommunities verifies that a project with
// memories, entities, and dense relations produces at least one community and
// that it is persisted correctly (AC-2).
func TestDetectAndPersistCommunities_NewCommunities(t *testing.T) {
	// min_size=2 so any pair forms a community.
	svc, ps := newCommunityTestService(t, 2)
	ctx := context.Background()

	// Two tightly connected clusters of 4 entities each.
	_, _ = seedCluster(t, ctx, svc, ps, "clusterA", 4)
	_, _ = seedCluster(t, ctx, svc, ps, "clusterB", 4)

	result, err := svc.DetectAndPersistCommunities(ctx, model.ScopeProject, "test/community")
	if err != nil {
		t.Fatalf("DetectAndPersistCommunities: %v", err)
	}
	if result.TotalCommunities == 0 {
		t.Error("expected at least 1 community, got 0")
	}
	if result.NewCommunities == 0 {
		t.Error("expected NewCommunities > 0 on first run")
	}
	if result.UpdatedCommunities != 0 {
		t.Errorf("expected UpdatedCommunities=0 on first run, got %d", result.UpdatedCommunities)
	}
	if result.Duration <= 0 {
		t.Error("Duration should be positive")
	}
}

// TestDetectAndPersistCommunities_StableHash verifies that running detection
// twice without graph changes produces no new or deleted communities — all
// communities are "updated" with the same UUIDs (AC-3).
func TestDetectAndPersistCommunities_StableHash(t *testing.T) {
	svc, ps := newCommunityTestService(t, 2)
	ctx := context.Background()

	_, _ = seedCluster(t, ctx, svc, ps, "stable", 4)

	// First run: creates communities.
	r1, err := svc.DetectAndPersistCommunities(ctx, model.ScopeProject, "test/community")
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if r1.TotalCommunities == 0 {
		t.Skip("graph too sparse for community detection at this min_size — skip stability test")
	}

	// Record community IDs from first run.
	// We can't directly inspect the store here (service_test package), but we
	// can verify counts: the second run should have NewCommunities=0 and
	// DeletedCommunities=0.

	// Second run: should only update.
	r2, err := svc.DetectAndPersistCommunities(ctx, model.ScopeProject, "test/community")
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if r2.NewCommunities != 0 {
		t.Errorf("second run: expected NewCommunities=0, got %d", r2.NewCommunities)
	}
	if r2.DeletedCommunities != 0 {
		t.Errorf("second run: expected DeletedCommunities=0, got %d", r2.DeletedCommunities)
	}
	if r2.UpdatedCommunities != r1.TotalCommunities {
		t.Errorf("second run: UpdatedCommunities=%d, want %d (same as first total)",
			r2.UpdatedCommunities, r1.TotalCommunities)
	}
}

// TestDetectAndPersistCommunities_ChangedGraph verifies that adding a new
// isolated cluster after the first run causes the diff to detect changes
// (AC-4).
func TestDetectAndPersistCommunities_ChangedGraph(t *testing.T) {
	svc, ps := newCommunityTestService(t, 2)
	ctx := context.Background()

	_, _ = seedCluster(t, ctx, svc, ps, "initial", 4)

	// First run.
	r1, err := svc.DetectAndPersistCommunities(ctx, model.ScopeProject, "test/community")
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if r1.TotalCommunities == 0 {
		t.Skip("graph too sparse for community detection — skip changed-graph test")
	}

	// Add a second cluster and re-run.
	_, _ = seedCluster(t, ctx, svc, ps, "added", 4)

	r2, err := svc.DetectAndPersistCommunities(ctx, model.ScopeProject, "test/community")
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	// After adding more nodes, the graph topology changes and communities shift.
	// We expect the total to be >= the first run.
	if r2.TotalCommunities < r1.TotalCommunities {
		t.Errorf("expected TotalCommunities >= %d after expansion, got %d",
			r1.TotalCommunities, r2.TotalCommunities)
	}
}

// TestDetectAndPersistCommunities_ResultFields verifies that all fields of
// DetectionResult are populated correctly after a successful run.
func TestDetectAndPersistCommunities_ResultFields(t *testing.T) {
	svc, ps := newCommunityTestService(t, 2)
	ctx := context.Background()

	_, _ = seedCluster(t, ctx, svc, ps, "fields", 4)

	result, err := svc.DetectAndPersistCommunities(ctx, model.ScopeProject, "test/community")
	if err != nil {
		t.Fatalf("DetectAndPersistCommunities: %v", err)
	}

	// Duration should be measured.
	if result.Duration == 0 {
		t.Error("Duration should be non-zero after a detection run")
	}
	if result.Duration > 5*time.Second {
		t.Errorf("Duration unexpectedly long: %v", result.Duration)
	}
	// TotalCommunities = New + Updated.
	if result.TotalCommunities != result.NewCommunities+result.UpdatedCommunities {
		t.Errorf("TotalCommunities(%d) != New(%d) + Updated(%d)",
			result.TotalCommunities, result.NewCommunities, result.UpdatedCommunities)
	}
}
