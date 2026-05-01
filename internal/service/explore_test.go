package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/store"
)

// depthPtr converts an int to *int for use in ExploreRequest.Depth.
func depthPtr(n int) *int { return &n }

// TestExplore_Basic_Depth1 verifies that a seed with direct neighbors returns
// the correct nodes with distance=1 and positive accumulated weights.
func TestExplore_Basic_Depth1(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	seedID, neighborIDs := buildStarGraphSvc(t, ctx, svc, ps, "basic-depth1", 3, 0.8)
	_ = neighborIDs

	resp, err := svc.Explore(ctx, model.ExploreRequest{
		Seed:  seedID,
		Depth: depthPtr(1),
	})
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}
	if resp.SeedID != seedID {
		t.Errorf("SeedID: got %q, want %q", resp.SeedID, seedID)
	}
	if resp.TotalNodes != 3 {
		t.Errorf("TotalNodes: got %d, want 3 (got nodes: %+v)", resp.TotalNodes, resp.Nodes)
	}
	for _, n := range resp.Nodes {
		if n.Distance != 1 {
			t.Errorf("node %q: Distance=%d, want 1", n.MemoryID, n.Distance)
		}
		if n.AccumulatedWeight <= 0 || n.AccumulatedWeight > 1.0 {
			t.Errorf("node %q: AccumulatedWeight=%f out of range (0,1]", n.MemoryID, n.AccumulatedWeight)
		}
	}
}

// TestExplore_Depth2_Transitive verifies that a depth-2 exploration returns a
// transitive node (leaf) with accumulated_weight = w1 * w2.
func TestExplore_Depth2_Transitive(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	const w1, w2 = 0.9, 0.7
	seedID, middleID, leafID := buildChainGraphSvc(t, ctx, svc, ps, "chain", w1, w2)

	resp, err := svc.Explore(ctx, model.ExploreRequest{
		Seed:  seedID,
		Depth: depthPtr(2),
	})
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}

	var foundLeaf *model.ExploreNode
	for i := range resp.Nodes {
		if resp.Nodes[i].MemoryID == leafID {
			foundLeaf = &resp.Nodes[i]
		}
	}
	if foundLeaf == nil {
		t.Fatalf("leaf node %q not found in response (all nodes: %+v)", leafID, resp.Nodes)
	}
	if foundLeaf.Distance != 2 {
		t.Errorf("leaf Distance: got %d, want 2", foundLeaf.Distance)
	}
	wantWeight := w1 * w2
	if diff := foundLeaf.AccumulatedWeight - wantWeight; diff > 0.001 || diff < -0.001 {
		t.Errorf("leaf AccumulatedWeight: got %f, want ~%f", foundLeaf.AccumulatedWeight, wantWeight)
	}
	_ = middleID
}

// TestExplore_CycleDetection verifies that a graph cycle does not cause an
// infinite loop and each memory appears at most once in the result.
func TestExplore_CycleDetection(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	seedID, aID, bID, cID := buildCycleGraphSvc(t, ctx, svc, ps)

	resp, err := svc.Explore(ctx, model.ExploreRequest{
		Seed:  seedID,
		Depth: depthPtr(5),
	})
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}

	seen := make(map[string]int)
	for _, n := range resp.Nodes {
		seen[n.MemoryID]++
	}
	for id, count := range seen {
		if count > 1 {
			t.Errorf("memory %q appeared %d times, want at most 1", id, count)
		}
	}
	for _, n := range resp.Nodes {
		if n.MemoryID == seedID {
			t.Error("seed itself should not appear in result nodes")
		}
	}
	_ = aID
	_ = bID
	_ = cID
}

// TestExplore_BudgetLimit verifies that nodes are skipped when the token
// budget is exhausted.
func TestExplore_BudgetLimit(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	seedID, _ := buildStarGraphSvc(t, ctx, svc, ps, "budget", 5, 0.8)

	resp, err := svc.Explore(ctx, model.ExploreRequest{
		Seed:   seedID,
		Depth:  depthPtr(1),
		Budget: 5, // very tight — no neighbor should fit
	})
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}
	if len(resp.Nodes) > 0 {
		t.Errorf("expected 0 nodes with tiny budget=5, got %d", len(resp.Nodes))
	}
}

// TestExplore_NodeCap verifies that at most ExploreMaxNodes (200) nodes are
// returned even when more are reachable.
func TestExplore_NodeCap(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	seedID, _ := buildStarGraphSvc(t, ctx, svc, ps, "cap", 250, 0.5)

	resp, err := svc.Explore(ctx, model.ExploreRequest{
		Seed:   seedID,
		Depth:  depthPtr(1),
		Budget: 10_000_000,
	})
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}
	if resp.TotalNodes > 200 {
		t.Errorf("TotalNodes=%d exceeds default cap of 200", resp.TotalNodes)
	}
}

// TestExplore_DepthZero verifies that depth=0 produces an empty Nodes slice.
func TestExplore_DepthZero(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	seedID, _ := buildStarGraphSvc(t, ctx, svc, ps, "depth-zero", 3, 0.8)

	resp, err := svc.Explore(ctx, model.ExploreRequest{
		Seed:  seedID,
		Depth: depthPtr(0),
	})
	if err != nil {
		t.Fatalf("Explore depth=0: %v", err)
	}
	if len(resp.Nodes) != 0 {
		t.Errorf("expected 0 nodes for depth=0, got %d", len(resp.Nodes))
	}
	if resp.SeedID != seedID {
		t.Errorf("SeedID: got %q, want %q", resp.SeedID, seedID)
	}
}

// TestExplore_SeedNoEntities verifies that a seed with no entity links returns
// an empty result without an error.
func TestExplore_SeedNoEntities(t *testing.T) {
	svc, _ := newTestServiceWithGraph(t)
	ctx := context.Background()

	saved, err := svc.Save(ctx, model.SaveRequest{
		Title:   "lonely memory",
		Content: "no graph links",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	resp, err := svc.Explore(ctx, model.ExploreRequest{
		Seed:  saved.ID,
		Depth: depthPtr(2),
	})
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}
	if resp.TotalNodes != 0 {
		t.Errorf("expected 0 nodes for isolated memory, got %d", resp.TotalNodes)
	}
}

// TestExplore_SeedNotFound verifies that ErrNotFound is returned when the seed
// UUID does not match any memory.
func TestExplore_SeedNotFound(t *testing.T) {
	svc, _ := newTestServiceWithGraph(t)
	ctx := context.Background()

	_, err := svc.Explore(ctx, model.ExploreRequest{
		Seed:  "00000000-0000-7000-8000-000000000000",
		Depth: depthPtr(2),
	})
	if err == nil {
		t.Fatal("expected error for nonexistent seed, got nil")
	}
	if !errors.Is(err, model.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

// TestExplore_SeedByTopicKey verifies that the seed can be resolved via its
// topic_key string.
func TestExplore_SeedByTopicKey(t *testing.T) {
	svc, _ := newTestServiceWithGraph(t)
	ctx := context.Background()

	saved, err := svc.Save(ctx, model.SaveRequest{
		Title:    "seed by topic key",
		Content:  "content",
		TopicKey: "test/seed-by-topic-key",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	resp, err := svc.Explore(ctx, model.ExploreRequest{
		Seed:  "test/seed-by-topic-key",
		Depth: depthPtr(1),
	})
	if err != nil {
		t.Fatalf("Explore by topic_key: %v", err)
	}
	if resp.SeedID != saved.ID {
		t.Errorf("SeedID: got %q, want %q", resp.SeedID, saved.ID)
	}
}

// TestExplore_SeedByShortPrefix verifies that the seed can be resolved via an
// 8-character hex prefix of the UUID.
func TestExplore_SeedByShortPrefix(t *testing.T) {
	svc, _ := newTestServiceWithGraph(t)
	ctx := context.Background()

	saved, err := svc.Save(ctx, model.SaveRequest{
		Title:   "seed by short prefix",
		Content: "content",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Strip hyphens and take the first 8 hex chars as prefix.
	rawID := strings.ReplaceAll(saved.ID, "-", "")
	prefix := rawID[:8]

	resp, err := svc.Explore(ctx, model.ExploreRequest{
		Seed:  prefix,
		Depth: depthPtr(1),
	})
	if err != nil {
		t.Fatalf("Explore by short prefix %q: %v", prefix, err)
	}
	if resp.SeedID != saved.ID {
		t.Errorf("SeedID: got %q, want %q", resp.SeedID, saved.ID)
	}
}

// TestExplore_ThresholdFilter verifies that relations below the threshold are
// not followed during traversal.
func TestExplore_ThresholdFilter(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	seedResp, _ := svc.Save(ctx, model.SaveRequest{Title: "threshold seed", Content: "seed"})
	neighborResp, _ := svc.Save(ctx, model.SaveRequest{Title: "weak neighbor", Content: "neighbor"})
	seedID, neighborID := seedResp.ID, neighborResp.ID

	seedEnt := findOrCreate(t, ctx, ps, "thresh-seed-ent", "test/graph")
	neighborEnt := findOrCreate(t, ctx, ps, "thresh-nbr-ent", "test/graph")
	_ = ps.LinkMemoryEntity(ctx, seedID, seedEnt.ID, "mention")
	_ = ps.LinkMemoryEntity(ctx, neighborID, neighborEnt.ID, "mention")
	_, _ = ps.CreateRelation(ctx, &model.Relation{
		SourceID: seedEnt.ID, TargetID: neighborEnt.ID,
		Type: model.RelRelatedTo, Weight: 0.1, // weak
	})

	resp, err := svc.Explore(ctx, model.ExploreRequest{
		Seed:      seedID,
		Depth:     depthPtr(1),
		Threshold: 0.5, // filter out 0.1
	})
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}
	for _, n := range resp.Nodes {
		if n.MemoryID == neighborID {
			t.Error("weak neighbor (w=0.1) should not appear when threshold=0.5")
		}
	}
}

// TestExplore_ParentMemoryID verifies that each node's ParentMemoryID correctly
// tracks which node was its parent in the BFS traversal.
func TestExplore_ParentMemoryID(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	seedID, middleID, leafID := buildChainGraphSvc(t, ctx, svc, ps, "parent-track", 0.9, 0.7)

	resp, err := svc.Explore(ctx, model.ExploreRequest{
		Seed:  seedID,
		Depth: depthPtr(2),
	})
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}

	byID := make(map[string]model.ExploreNode)
	for _, n := range resp.Nodes {
		byID[n.MemoryID] = n
	}

	middle, ok := byID[middleID]
	if !ok {
		t.Fatalf("middle node %q not in result", middleID)
	}
	if middle.ParentMemoryID != seedID {
		t.Errorf("middle.ParentMemoryID: got %q, want seed %q", middle.ParentMemoryID, seedID)
	}

	leaf, ok := byID[leafID]
	if !ok {
		t.Fatalf("leaf node %q not in result", leafID)
	}
	if leaf.ParentMemoryID != middleID {
		t.Errorf("leaf.ParentMemoryID: got %q, want middle %q", leaf.ParentMemoryID, middleID)
	}
}

// TestExplore_OrderByDistanceThenWeight verifies that nodes are sorted
// distance ASC, accumulated_weight DESC.
func TestExplore_OrderByDistanceThenWeight(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	seedID, _ := buildStarGraphSvc(t, ctx, svc, ps, "sort", 3, 0.5)

	resp, err := svc.Explore(ctx, model.ExploreRequest{
		Seed:   seedID,
		Depth:  depthPtr(1),
		Budget: 1_000_000,
	})
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}

	for i := 1; i < len(resp.Nodes); i++ {
		prev, curr := resp.Nodes[i-1], resp.Nodes[i]
		if prev.Distance > curr.Distance {
			t.Errorf("not sorted by distance ASC: nodes[%d].Distance=%d > nodes[%d].Distance=%d",
				i-1, prev.Distance, i, curr.Distance)
		}
		if prev.Distance == curr.Distance && prev.AccumulatedWeight < curr.AccumulatedWeight {
			t.Errorf("same distance, not sorted by AccumulatedWeight DESC at positions %d,%d", i-1, i)
		}
	}
}

// TestExplore_SeedPrefixAmbiguous verifies that ErrAmbiguousSeed is returned
// when a short prefix matches more than one memory in the project store.
// We force a collision by updating the two IDs via direct SQL after creation so
// they share a common hex prefix (white-box store test technique).
func TestExplore_SeedPrefixAmbiguous(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	m1, err := svc.Save(ctx, model.SaveRequest{Title: "ambiguous 1", Content: "content"})
	if err != nil {
		t.Fatalf("Save m1: %v", err)
	}
	m2, err := svc.Save(ctx, model.SaveRequest{Title: "ambiguous 2", Content: "content"})
	if err != nil {
		t.Fatalf("Save m2: %v", err)
	}

	// Force the two IDs to share the first 8 hex chars by updating them via
	// direct SQL (same technique used in TestStore_GetByIDPrefix_MultipleMatches).
	commonHex := "aabbccdd"
	_, err = ps.DB().ExecContext(ctx,
		"UPDATE memories SET id = ? WHERE id = ?",
		commonHex+"0000-0000-0000-000000000001",
		m1.ID,
	)
	if err != nil {
		t.Fatalf("direct ID update m1: %v", err)
	}
	_, err = ps.DB().ExecContext(ctx,
		"UPDATE memories SET id = ? WHERE id = ?",
		commonHex+"0000-0000-0000-000000000002",
		m2.ID,
	)
	if err != nil {
		t.Fatalf("direct ID update m2: %v", err)
	}

	_, exploreErr := svc.Explore(ctx, model.ExploreRequest{
		Seed:  commonHex,
		Depth: depthPtr(1),
	})
	if exploreErr == nil {
		t.Fatal("expected ErrAmbiguousSeed, got nil error")
	}
	if !errors.Is(exploreErr, model.ErrAmbiguousSeed) {
		t.Errorf("expected ErrAmbiguousSeed, got: %v", exploreErr)
	}
}

// TestExplore_TouchRelations verifies that Explore calls BatchTouchRelations for
// the relations it traverses, updating last_traversed_at to a non-zero value.
func TestExplore_TouchRelations(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	seedResp, _ := svc.Save(ctx, model.SaveRequest{Title: "touch-seed", Content: "seed"})
	neighborResp, _ := svc.Save(ctx, model.SaveRequest{Title: "touch-neighbor", Content: "neighbor"})
	seedID, neighborID := seedResp.ID, neighborResp.ID

	seedEnt := findOrCreate(t, ctx, ps, "touch-seed-ent", "test/graph")
	neighborEnt := findOrCreate(t, ctx, ps, "touch-nbr-ent", "test/graph")
	if err := ps.LinkMemoryEntity(ctx, seedID, seedEnt.ID, "mention"); err != nil {
		t.Fatalf("LinkMemoryEntity seed: %v", err)
	}
	if err := ps.LinkMemoryEntity(ctx, neighborID, neighborEnt.ID, "mention"); err != nil {
		t.Fatalf("LinkMemoryEntity neighbor: %v", err)
	}
	rel, err := ps.CreateRelation(ctx, &model.Relation{
		SourceID: seedEnt.ID, TargetID: neighborEnt.ID,
		Type: model.RelRelatedTo, Weight: 0.8,
	})
	if err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}

	// Confirm last_traversed_at is zero before exploration.
	before, err := ps.FindRelation(ctx, seedEnt.ID, neighborEnt.ID, model.RelRelatedTo)
	if err != nil {
		t.Fatalf("FindRelation before: %v", err)
	}
	if before == nil {
		t.Fatal("expected relation, got nil")
	}
	if !before.LastTraversedAt.IsZero() {
		t.Error("expected LastTraversedAt to be zero before explore")
	}

	_, err = svc.Explore(ctx, model.ExploreRequest{
		Seed:  seedID,
		Depth: depthPtr(1),
	})
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}

	// BatchTouchRelations is fire-and-forget (goroutine). Give it a moment.
	// Use a short sleep only — the goroutine typically completes in <1ms.
	// The test uses a synchronous store, so the update is immediate on the same
	// SQLite connection pool.
	// We retry a few times to avoid flakiness on slow CI.
	var after *model.Relation
	for i := 0; i < 50; i++ {
		after, err = ps.FindRelation(ctx, seedEnt.ID, neighborEnt.ID, model.RelRelatedTo)
		if err != nil {
			t.Fatalf("FindRelation after: %v", err)
		}
		if after != nil && !after.LastTraversedAt.IsZero() {
			break
		}
	}
	_ = rel
	if after == nil || after.LastTraversedAt.IsZero() {
		t.Error("expected LastTraversedAt to be non-zero after Explore (BatchTouchRelations not called)")
	}
}

// ─── graph builder helpers ────────────────────────────────────────────────────

// buildStarGraphSvc creates a seed memory linked to n neighbors via distinct
// entity pairs. Returns (seedID, neighborIDs).
func buildStarGraphSvc(
	t *testing.T, ctx context.Context,
	svc interface{ Save(context.Context, model.SaveRequest) (*model.SaveResponse, error) },
	ps *store.MemoryStore,
	prefix string, n int, weight float64,
) (seedID string, neighborIDs []string) {
	t.Helper()
	seedResp, err := svc.Save(ctx, model.SaveRequest{
		Title:   prefix + " seed",
		Content: prefix + " seed content for graph test",
	})
	if err != nil {
		t.Fatalf("buildStarGraphSvc Save seed: %v", err)
	}
	seedID = seedResp.ID
	seedEnt := findOrCreate(t, ctx, ps, prefix+"-seed-ent", "test/graph")
	if err := ps.LinkMemoryEntity(ctx, seedID, seedEnt.ID, "mention"); err != nil {
		t.Fatalf("buildStarGraphSvc LinkMemoryEntity seed: %v", err)
	}

	for i := 0; i < n; i++ {
		nResp, err := svc.Save(ctx, model.SaveRequest{
			Title:   prefix + " neighbor",
			Content: prefix + " neighbor content for traversal",
		})
		if err != nil {
			t.Fatalf("buildStarGraphSvc Save neighbor %d: %v", i, err)
		}
		neighborIDs = append(neighborIDs, nResp.ID)

		nEnt := findOrCreate(t, ctx, ps, prefix+"-nbr-ent-"+nResp.ID[:8], "test/graph")
		if err := ps.LinkMemoryEntity(ctx, nResp.ID, nEnt.ID, "mention"); err != nil {
			t.Fatalf("buildStarGraphSvc LinkMemoryEntity neighbor %d: %v", i, err)
		}
		_, err = ps.CreateRelation(ctx, &model.Relation{
			SourceID: seedEnt.ID,
			TargetID: nEnt.ID,
			Type:     model.RelRelatedTo,
			Weight:   weight,
		})
		if err != nil {
			t.Fatalf("buildStarGraphSvc CreateRelation %d: %v", i, err)
		}
	}
	return
}

// buildChainGraphSvc creates seed→middle→leaf with controlled edge weights.
// Returns (seedID, middleID, leafID).
func buildChainGraphSvc(
	t *testing.T, ctx context.Context,
	svc interface{ Save(context.Context, model.SaveRequest) (*model.SaveResponse, error) },
	ps *store.MemoryStore,
	prefix string, w1, w2 float64,
) (seedID, middleID, leafID string) {
	t.Helper()
	sResp, _ := svc.Save(ctx, model.SaveRequest{Title: prefix + " chain-seed", Content: "seed"})
	mResp, _ := svc.Save(ctx, model.SaveRequest{Title: prefix + " chain-middle", Content: "middle"})
	lResp, _ := svc.Save(ctx, model.SaveRequest{Title: prefix + " chain-leaf", Content: "leaf"})
	seedID, middleID, leafID = sResp.ID, mResp.ID, lResp.ID

	sEnt := findOrCreate(t, ctx, ps, prefix+"-chain-s", "test/graph")
	mEnt := findOrCreate(t, ctx, ps, prefix+"-chain-m", "test/graph")
	lEnt := findOrCreate(t, ctx, ps, prefix+"-chain-l", "test/graph")

	_ = ps.LinkMemoryEntity(ctx, seedID, sEnt.ID, "mention")
	_ = ps.LinkMemoryEntity(ctx, middleID, mEnt.ID, "mention")
	_ = ps.LinkMemoryEntity(ctx, leafID, lEnt.ID, "mention")

	_, _ = ps.CreateRelation(ctx, &model.Relation{SourceID: sEnt.ID, TargetID: mEnt.ID, Type: model.RelRelatedTo, Weight: w1})
	_, _ = ps.CreateRelation(ctx, &model.Relation{SourceID: mEnt.ID, TargetID: lEnt.ID, Type: model.RelRelatedTo, Weight: w2})
	return
}

// buildCycleGraphSvc creates seed→A→B→C→A cycle. Returns (seedID, aID, bID, cID).
func buildCycleGraphSvc(
	t *testing.T, ctx context.Context,
	svc interface{ Save(context.Context, model.SaveRequest) (*model.SaveResponse, error) },
	ps *store.MemoryStore,
) (seedID, aID, bID, cID string) {
	t.Helper()
	sResp, _ := svc.Save(ctx, model.SaveRequest{Title: "cycle-seed", Content: "seed"})
	aResp, _ := svc.Save(ctx, model.SaveRequest{Title: "cycle-A", Content: "A"})
	bResp, _ := svc.Save(ctx, model.SaveRequest{Title: "cycle-B", Content: "B"})
	cResp, _ := svc.Save(ctx, model.SaveRequest{Title: "cycle-C", Content: "C"})
	seedID, aID, bID, cID = sResp.ID, aResp.ID, bResp.ID, cResp.ID

	sEnt := findOrCreate(t, ctx, ps, "cyc-s", "test/graph")
	aEnt := findOrCreate(t, ctx, ps, "cyc-a", "test/graph")
	bEnt := findOrCreate(t, ctx, ps, "cyc-b", "test/graph")
	cEnt := findOrCreate(t, ctx, ps, "cyc-c", "test/graph")

	_ = ps.LinkMemoryEntity(ctx, seedID, sEnt.ID, "mention")
	_ = ps.LinkMemoryEntity(ctx, aID, aEnt.ID, "mention")
	_ = ps.LinkMemoryEntity(ctx, bID, bEnt.ID, "mention")
	_ = ps.LinkMemoryEntity(ctx, cID, cEnt.ID, "mention")

	_, _ = ps.CreateRelation(ctx, &model.Relation{SourceID: sEnt.ID, TargetID: aEnt.ID, Type: model.RelRelatedTo, Weight: 0.9})
	_, _ = ps.CreateRelation(ctx, &model.Relation{SourceID: aEnt.ID, TargetID: bEnt.ID, Type: model.RelRelatedTo, Weight: 0.8})
	_, _ = ps.CreateRelation(ctx, &model.Relation{SourceID: bEnt.ID, TargetID: cEnt.ID, Type: model.RelRelatedTo, Weight: 0.7})
	_, _ = ps.CreateRelation(ctx, &model.Relation{SourceID: cEnt.ID, TargetID: aEnt.ID, Type: model.RelRelatedTo, Weight: 0.6}) // cycle
	return
}

// findOrCreate finds or creates an entity with the given name for the given
// project. Fails the test if the operation returns an error.
func findOrCreate(t *testing.T, ctx context.Context, ps *store.MemoryStore, name, project string) *model.Entity {
	t.Helper()
	e, err := ps.FindOrCreateEntity(ctx, name, model.KindConcept, project)
	if err != nil {
		t.Fatalf("FindOrCreateEntity %q: %v", name, err)
	}
	return e
}
