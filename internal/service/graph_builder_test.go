package service_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/db"
	"github.com/juanftp/mneme/internal/embed"
	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/scoring"
	"github.com/juanftp/mneme/internal/service"
	"github.com/juanftp/mneme/internal/store"
)

// TestDefaultGraphBuildOptions verifies that the factory function returns the
// values agreed in SPEC-016 D8.
func TestDefaultGraphBuildOptions(t *testing.T) {
	opts := service.DefaultGraphBuildOptions()

	if opts.MaxDepth != 3 {
		t.Errorf("MaxDepth: got %d, want 3", opts.MaxDepth)
	}
	if opts.MaxNodes != 5000 {
		t.Errorf("MaxNodes: got %d, want 5000", opts.MaxNodes)
	}
	if opts.WeightThreshold != 0.3 {
		t.Errorf("WeightThreshold: got %f, want 0.3", opts.WeightThreshold)
	}
	if opts.FanOutCap != 50 {
		t.Errorf("FanOutCap: got %d, want 50", opts.FanOutCap)
	}
}

// ─── graph builder helpers ────────────────────────────────────────────────────

// buildChainEntities creates entities A→B→C with configurable weights and
// seeds memory seedID with entity A. Returns entity IDs (a, b, c).
func buildChainEntities(
	t *testing.T, ctx context.Context,
	ps *store.MemoryStore,
	prefix, seedMemID string,
	weightAB, weightBC float64,
) (aID, bID, cID string) {
	t.Helper()

	a := findOrCreate(t, ctx, ps, prefix+"-A", "test/graph")
	b := findOrCreate(t, ctx, ps, prefix+"-B", "test/graph")
	c := findOrCreate(t, ctx, ps, prefix+"-C", "test/graph")
	aID, bID, cID = a.ID, b.ID, c.ID

	if err := ps.LinkMemoryEntity(ctx, seedMemID, a.ID, "mention"); err != nil {
		t.Fatalf("LinkMemoryEntity seed→A: %v", err)
	}

	mustCreateRelation(t, ctx, ps, a.ID, b.ID, weightAB)
	mustCreateRelation(t, ctx, ps, b.ID, c.ID, weightBC)
	return
}

// buildStarEntities creates entity hub with n leaf neighbors, linking seedMemID
// to hub. Returns hub entity ID and leaf entity IDs.
func buildStarEntities(
	t *testing.T, ctx context.Context,
	ps *store.MemoryStore,
	prefix, seedMemID string,
	n int, weight float64,
) (hubID string, leafIDs []string) {
	t.Helper()

	hub := findOrCreate(t, ctx, ps, prefix+"-hub", "test/graph")
	hubID = hub.ID
	if err := ps.LinkMemoryEntity(ctx, seedMemID, hub.ID, "mention"); err != nil {
		t.Fatalf("LinkMemoryEntity seed→hub: %v", err)
	}
	for i := 0; i < n; i++ {
		leaf := findOrCreate(t, ctx, ps, fmt.Sprintf("%s-leaf%d", prefix, i), "test/graph")
		leafIDs = append(leafIDs, leaf.ID)
		mustCreateRelation(t, ctx, ps, hub.ID, leaf.ID, weight)
	}
	return
}

// mustCreateRelation creates a weighted related_to relation. Fails the test on
// error.
func mustCreateRelation(t *testing.T, ctx context.Context, ps *store.MemoryStore, srcID, tgtID string, weight float64) {
	t.Helper()
	_, err := ps.CreateRelation(ctx, &model.Relation{
		SourceID: srcID,
		TargetID: tgtID,
		Type:     model.RelRelatedTo,
		Weight:   weight,
	})
	if err != nil {
		t.Fatalf("CreateRelation %s→%s: %v", srcID, tgtID, err)
	}
}

// saveMem saves a memory with the given title through svc and returns its ID.
func saveMem(t *testing.T, ctx context.Context, svc *service.MemoryService, title string) string {
	t.Helper()
	resp, err := svc.Save(ctx, model.SaveRequest{
		Title:   title,
		Content: title + " content",
	})
	if err != nil {
		t.Fatalf("Save %q: %v", title, err)
	}
	return resp.ID
}

// nodeSet returns the set of NodeIDs in graph for easy membership testing.
func nodeSet(g *scoring.SparseGraph) map[string]struct{} {
	s := make(map[string]struct{}, len(g.Nodes))
	for _, n := range g.Nodes {
		s[n] = struct{}{}
	}
	return s
}

// ─── Core BFS tests ───────────────────────────────────────────────────────────

// TestBuildGraphForSeeds_SingleSeed_Chain verifies that a chain A→B→C with
// MaxDepth=3 produces a graph containing all three entity nodes with
// bidirectional edges.
func TestBuildGraphForSeeds_SingleSeed_Chain(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	seedID := saveMem(t, ctx, svc, "chain seed")
	aID, bID, cID := buildChainEntities(t, ctx, ps, "chain1", seedID, 0.8, 0.7)

	opts := service.DefaultGraphBuildOptions()
	graph, touchIDs := svc.BuildGraphForSeeds(ctx, []string{seedID}, opts)

	if graph == nil {
		t.Fatal("BuildGraphForSeeds returned nil graph")
	}
	if len(graph.Nodes) != 3 {
		t.Errorf("Nodes: got %d, want 3 (got: %v)", len(graph.Nodes), graph.Nodes)
	}

	ns := nodeSet(graph)
	for _, id := range []string{aID, bID, cID} {
		if _, ok := ns[id]; !ok {
			t.Errorf("node %q missing from graph", id)
		}
	}

	// Bidirectional edges: A↔B and B↔C
	if _, ok := graph.Adj[aID][bID]; !ok {
		t.Errorf("edge A→B missing")
	}
	if _, ok := graph.Adj[bID][aID]; !ok {
		t.Errorf("edge B→A missing")
	}
	if _, ok := graph.Adj[bID][cID]; !ok {
		t.Errorf("edge B→C missing")
	}
	if _, ok := graph.Adj[cID][bID]; !ok {
		t.Errorf("edge C→B missing")
	}

	if len(touchIDs) == 0 {
		t.Error("touchIDs should be non-empty for a traversed chain")
	}
}

// TestBuildGraphForSeeds_SingleSeed_Star verifies that a hub with 5 neighbors
// produces a graph with 6 nodes and 5 bidirectional edge pairs.
func TestBuildGraphForSeeds_SingleSeed_Star(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	const n = 5
	seedID := saveMem(t, ctx, svc, "star seed")
	hubID, leafIDs := buildStarEntities(t, ctx, ps, "star1", seedID, n, 0.6)

	opts := service.DefaultGraphBuildOptions()
	graph, _ := svc.BuildGraphForSeeds(ctx, []string{seedID}, opts)

	if graph == nil {
		t.Fatal("nil graph")
	}
	if len(graph.Nodes) != n+1 {
		t.Errorf("Nodes: got %d, want %d", len(graph.Nodes), n+1)
	}
	// Each leaf must be reachable from hub and hub from leaf.
	for _, leafID := range leafIDs {
		if _, ok := graph.Adj[hubID][leafID]; !ok {
			t.Errorf("edge hub→leaf %q missing", leafID)
		}
		if _, ok := graph.Adj[leafID][hubID]; !ok {
			t.Errorf("edge leaf→hub %q missing", leafID)
		}
	}
}

// TestBuildGraphForSeeds_MultipleSeeds verifies that two seeds each linked to
// their own entity (connected via a shared neighbor) both contribute nodes to
// the final graph.
func TestBuildGraphForSeeds_MultipleSeeds(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	seedA := saveMem(t, ctx, svc, "multi-seed-A")
	seedB := saveMem(t, ctx, svc, "multi-seed-B")

	aEnt := findOrCreate(t, ctx, ps, "multi-ent-A", "test/graph")
	bEnt := findOrCreate(t, ctx, ps, "multi-ent-B", "test/graph")
	shared := findOrCreate(t, ctx, ps, "multi-ent-shared", "test/graph")

	if err := ps.LinkMemoryEntity(ctx, seedA, aEnt.ID, "mention"); err != nil {
		t.Fatalf("link seedA: %v", err)
	}
	if err := ps.LinkMemoryEntity(ctx, seedB, bEnt.ID, "mention"); err != nil {
		t.Fatalf("link seedB: %v", err)
	}

	// Connect each seed entity to a shared neighbor — gives BFS something to
	// collect so the nodes appear in the graph.
	mustCreateRelation(t, ctx, ps, aEnt.ID, shared.ID, 0.8)
	mustCreateRelation(t, ctx, ps, bEnt.ID, shared.ID, 0.7)

	opts := service.DefaultGraphBuildOptions()
	graph, _ := svc.BuildGraphForSeeds(ctx, []string{seedA, seedB}, opts)

	ns := nodeSet(graph)
	if _, ok := ns[aEnt.ID]; !ok {
		t.Errorf("entity from seedA missing")
	}
	if _, ok := ns[bEnt.ID]; !ok {
		t.Errorf("entity from seedB missing")
	}
	if _, ok := ns[shared.ID]; !ok {
		t.Errorf("shared neighbor entity missing")
	}
}

// TestBuildGraphForSeeds_DepthLimited verifies that a chain A→B→C→D with
// MaxDepth=2 includes A, B, C but excludes D.
func TestBuildGraphForSeeds_DepthLimited(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	seedID := saveMem(t, ctx, svc, "depth-seed")

	a := findOrCreate(t, ctx, ps, "depth-A", "test/graph")
	b := findOrCreate(t, ctx, ps, "depth-B", "test/graph")
	c := findOrCreate(t, ctx, ps, "depth-C", "test/graph")
	d := findOrCreate(t, ctx, ps, "depth-D", "test/graph")

	if err := ps.LinkMemoryEntity(ctx, seedID, a.ID, "mention"); err != nil {
		t.Fatalf("link: %v", err)
	}
	mustCreateRelation(t, ctx, ps, a.ID, b.ID, 0.8)
	mustCreateRelation(t, ctx, ps, b.ID, c.ID, 0.8)
	mustCreateRelation(t, ctx, ps, c.ID, d.ID, 0.8)

	opts := service.DefaultGraphBuildOptions()
	opts.MaxDepth = 2

	graph, _ := svc.BuildGraphForSeeds(ctx, []string{seedID}, opts)
	ns := nodeSet(graph)

	for _, id := range []string{a.ID, b.ID, c.ID} {
		if _, ok := ns[id]; !ok {
			t.Errorf("entity %q should be in depth-2 graph", id)
		}
	}
	if _, ok := ns[d.ID]; ok {
		t.Errorf("entity D at depth-3 should be excluded with MaxDepth=2")
	}
}

// TestBuildGraphForSeeds_TouchIDs verifies that every traversed relation ID
// appears in the returned touchIDs slice.
func TestBuildGraphForSeeds_TouchIDs(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	seedID := saveMem(t, ctx, svc, "touch-seed")
	a := findOrCreate(t, ctx, ps, "touch-A", "test/graph")
	b := findOrCreate(t, ctx, ps, "touch-B", "test/graph")
	c := findOrCreate(t, ctx, ps, "touch-C", "test/graph")

	if err := ps.LinkMemoryEntity(ctx, seedID, a.ID, "mention"); err != nil {
		t.Fatalf("link: %v", err)
	}
	relAB, _ := ps.CreateRelation(ctx, &model.Relation{SourceID: a.ID, TargetID: b.ID, Type: model.RelRelatedTo, Weight: 0.7})
	relBC, _ := ps.CreateRelation(ctx, &model.Relation{SourceID: b.ID, TargetID: c.ID, Type: model.RelRelatedTo, Weight: 0.7})

	opts := service.DefaultGraphBuildOptions()
	_, touchIDs := svc.BuildGraphForSeeds(ctx, []string{seedID}, opts)

	got := make(map[string]bool)
	for _, id := range touchIDs {
		got[id] = true
	}
	for _, wantID := range []string{relAB.ID, relBC.ID} {
		if !got[wantID] {
			t.Errorf("relation %q not in touchIDs", wantID)
		}
	}
}

// TestBuildGraphForSeeds_BidirectionalEdges confirms that a single A→B relation
// produces both Adj[A][B] and Adj[B][A] with the same weight.
func TestBuildGraphForSeeds_BidirectionalEdges(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	seedID := saveMem(t, ctx, svc, "bidir-seed")
	a := findOrCreate(t, ctx, ps, "bidir-A", "test/graph")
	b := findOrCreate(t, ctx, ps, "bidir-B", "test/graph")

	if err := ps.LinkMemoryEntity(ctx, seedID, a.ID, "mention"); err != nil {
		t.Fatalf("link: %v", err)
	}
	const wantWeight = 0.75
	mustCreateRelation(t, ctx, ps, a.ID, b.ID, wantWeight)

	opts := service.DefaultGraphBuildOptions()
	graph, _ := svc.BuildGraphForSeeds(ctx, []string{seedID}, opts)

	wAB, okAB := graph.Adj[a.ID][b.ID]
	wBA, okBA := graph.Adj[b.ID][a.ID]
	if !okAB {
		t.Error("Adj[A][B] missing")
	}
	if !okBA {
		t.Error("Adj[B][A] missing")
	}
	if okAB && wAB != wantWeight {
		t.Errorf("Adj[A][B]: got %f, want %f", wAB, wantWeight)
	}
	if okBA && wBA != wantWeight {
		t.Errorf("Adj[B][A]: got %f, want %f", wBA, wantWeight)
	}
}

// ─── Edge case tests ──────────────────────────────────────────────────────────

// TestBuildGraphForSeeds_EmptySeeds verifies that 0 seeds returns an empty
// graph and nil touchIDs without panicking.
func TestBuildGraphForSeeds_EmptySeeds(t *testing.T) {
	svc, _ := newTestServiceWithGraph(t)
	ctx := context.Background()

	opts := service.DefaultGraphBuildOptions()
	graph, touchIDs := svc.BuildGraphForSeeds(ctx, nil, opts)

	if graph == nil {
		t.Fatal("got nil graph, want empty non-nil graph")
	}
	if len(graph.Nodes) != 0 {
		t.Errorf("Nodes: got %d, want 0", len(graph.Nodes))
	}
	if len(touchIDs) != 0 {
		t.Errorf("touchIDs: got %d, want 0", len(touchIDs))
	}
}

// TestBuildGraphForSeeds_NonexistentSeed verifies that a seed ID not present
// in the DB is silently skipped and an empty graph is returned.
func TestBuildGraphForSeeds_NonexistentSeed(t *testing.T) {
	svc, _ := newTestServiceWithGraph(t)
	ctx := context.Background()

	opts := service.DefaultGraphBuildOptions()
	graph, touchIDs := svc.BuildGraphForSeeds(ctx, []string{"00000000-0000-0000-0000-000000000000"}, opts)

	if len(graph.Nodes) != 0 {
		t.Errorf("Nodes: got %d, want 0", len(graph.Nodes))
	}
	if len(touchIDs) != 0 {
		t.Errorf("touchIDs: got %d, want 0", len(touchIDs))
	}
}

// TestBuildGraphForSeeds_SeedWithoutEntities verifies that a seed memory that
// exists but has no entries in memory_entities is silently skipped.
func TestBuildGraphForSeeds_SeedWithoutEntities(t *testing.T) {
	svc, _ := newTestServiceWithGraph(t)
	ctx := context.Background()

	seedID := saveMem(t, ctx, svc, "no-entity seed")
	// Intentionally do NOT link any entity to this memory.

	opts := service.DefaultGraphBuildOptions()
	graph, _ := svc.BuildGraphForSeeds(ctx, []string{seedID}, opts)

	if len(graph.Nodes) != 0 {
		t.Errorf("Nodes: got %d, want 0 (no entities linked)", len(graph.Nodes))
	}
}

// TestBuildGraphForSeeds_MaxNodesReached verifies that the BFS stops when the
// visited set reaches MaxNodes and returns a partial (but valid) graph.
func TestBuildGraphForSeeds_MaxNodesReached(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	const cap = 10

	// Create a hub with 30 leaf entities — well above the cap.
	seedID := saveMem(t, ctx, svc, "cap-seed")
	hubID, _ := buildStarEntities(t, ctx, ps, "cap", seedID, 30, 0.8)
	_ = hubID

	opts := service.DefaultGraphBuildOptions()
	opts.MaxNodes = cap

	graph, _ := svc.BuildGraphForSeeds(ctx, []string{seedID}, opts)

	// The graph must not exceed MaxNodes. Due to initial frontier setup, the
	// hub entity itself is added before the cap check on neighbours, so we
	// allow up to cap+1.
	if len(graph.Nodes) > cap+1 {
		t.Errorf("Nodes: got %d, want <= %d (MaxNodes=%d)", len(graph.Nodes), cap+1, cap)
	}
}

// TestBuildGraphForSeeds_ThresholdFiltersAll verifies that when all relations
// have weight below WeightThreshold, an empty graph (only seed entities) is
// returned with no edges.
func TestBuildGraphForSeeds_ThresholdFiltersAll(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	seedID := saveMem(t, ctx, svc, "threshold-seed")
	a := findOrCreate(t, ctx, ps, "thr-A", "test/graph")
	b := findOrCreate(t, ctx, ps, "thr-B", "test/graph")
	if err := ps.LinkMemoryEntity(ctx, seedID, a.ID, "mention"); err != nil {
		t.Fatalf("link: %v", err)
	}
	// weight 0.1 is below threshold 0.3
	mustCreateRelation(t, ctx, ps, a.ID, b.ID, 0.1)

	opts := service.DefaultGraphBuildOptions()
	opts.WeightThreshold = 0.3

	graph, _ := svc.BuildGraphForSeeds(ctx, []string{seedID}, opts)

	// Only the seed entity A is in the graph; B was not reachable above threshold.
	if _, ok := graph.Adj[a.ID][b.ID]; ok {
		t.Errorf("edge A→B should not exist below threshold 0.3 (weight=0.1)")
	}
}

// TestBuildGraphForSeeds_DuplicateRelations verifies that discovering the same
// relation from both endpoints does not duplicate nodes.
func TestBuildGraphForSeeds_DuplicateRelations(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	// Two seed memories each linked to one of the entities in A→B.
	seedA := saveMem(t, ctx, svc, "dup-seed-A")
	seedB := saveMem(t, ctx, svc, "dup-seed-B")

	a := findOrCreate(t, ctx, ps, "dup-A", "test/graph")
	b := findOrCreate(t, ctx, ps, "dup-B", "test/graph")

	if err := ps.LinkMemoryEntity(ctx, seedA, a.ID, "mention"); err != nil {
		t.Fatalf("link seedA: %v", err)
	}
	if err := ps.LinkMemoryEntity(ctx, seedB, b.ID, "mention"); err != nil {
		t.Fatalf("link seedB: %v", err)
	}
	mustCreateRelation(t, ctx, ps, a.ID, b.ID, 0.8)

	opts := service.DefaultGraphBuildOptions()
	graph, _ := svc.BuildGraphForSeeds(ctx, []string{seedA, seedB}, opts)

	// Both entities must be present exactly once.
	ns := nodeSet(graph)
	if _, ok := ns[a.ID]; !ok {
		t.Errorf("entity A missing")
	}
	if _, ok := ns[b.ID]; !ok {
		t.Errorf("entity B missing")
	}
	if len(graph.Nodes) != 2 {
		t.Errorf("Nodes: got %d, want 2 (no duplicates)", len(graph.Nodes))
	}
}

// ─── Integration with PPR ─────────────────────────────────────────────────────

// TestBuildGraphForSeeds_FeedsPPR verifies that the graph produced by
// BuildGraphForSeeds is directly consumable by scoring.PPR: it converges,
// scores sum approximately 1.0, and the seed entity has a non-zero score.
func TestBuildGraphForSeeds_FeedsPPR(t *testing.T) {
	svc, ps := newTestServiceWithGraph(t)
	ctx := context.Background()

	// Build a small star: seed→hub, hub→5 leaves.
	seedID := saveMem(t, ctx, svc, "ppr-seed")
	hub := findOrCreate(t, ctx, ps, "ppr-hub", "test/graph")
	if err := ps.LinkMemoryEntity(ctx, seedID, hub.ID, "mention"); err != nil {
		t.Fatalf("link: %v", err)
	}
	for i := 0; i < 5; i++ {
		leaf := findOrCreate(t, ctx, ps, fmt.Sprintf("ppr-leaf%d", i), "test/graph")
		mustCreateRelation(t, ctx, ps, hub.ID, leaf.ID, 0.6)
	}

	opts := service.DefaultGraphBuildOptions()
	graph, _ := svc.BuildGraphForSeeds(ctx, []string{seedID}, opts)

	if len(graph.Nodes) == 0 {
		t.Fatal("empty graph cannot feed PPR")
	}

	// hub is the seed entity; pass it as the PPR seed.
	pprResult, err := scoring.PPR(*graph, []scoring.NodeID{hub.ID}, scoring.DefaultPPROptions())
	if err != nil {
		t.Fatalf("PPR: %v", err)
	}
	if !pprResult.Converged {
		t.Errorf("PPR did not converge in %d iterations", pprResult.Iterations)
	}

	// Scores should sum approximately 1.0.
	total := 0.0
	for _, s := range pprResult.Scores {
		total += s
	}
	if total < 0.99 || total > 1.01 {
		t.Errorf("PPR score sum: got %f, want ~1.0", total)
	}

	// Seed entity should have a higher score than random leaves.
	hubScore := pprResult.Scores[hub.ID]
	if hubScore <= 0 {
		t.Errorf("hub (seed) should have non-zero PPR score, got %f", hubScore)
	}
}

// ─── Benchmarks ───────────────────────────────────────────────────────────────

// BenchmarkBuildGraphForSeeds_500Entities measures BuildGraphForSeeds against a
// corpus of 500 entities and 2000 relations with 5 seeds. Target: <30ms.
//
// Run with: CGO_ENABLED=1 go test -tags fts5 -bench=BenchmarkBuildGraphForSeeds_500Entities -benchtime=5s ./internal/service/
func BenchmarkBuildGraphForSeeds_500Entities(b *testing.B) {
	benchBuildGraph(b, 500, 2000, 5)
}

// BenchmarkBuildGraphForSeeds_5KEntities measures BuildGraphForSeeds against a
// corpus of 5000 entities and 20000 relations with 10 seeds. MaxNodes=5000 cap
// applies. Target: <50ms (SPEC-016 acceptance criterion 5).
//
// Run with: CGO_ENABLED=1 go test -tags fts5 -bench=BenchmarkBuildGraphForSeeds_5KEntities -benchtime=5s ./internal/service/
func BenchmarkBuildGraphForSeeds_5KEntities(b *testing.B) {
	benchBuildGraph(b, 5000, 20000, 10)
}

// benchBuildGraph is the shared benchmark body for different corpus sizes.
func benchBuildGraph(b *testing.B, numEntities, numRelations, numSeeds int) {
	b.Helper()

	projectDB, err := db.OpenMemory()
	if err != nil {
		b.Fatalf("open project db: %v", err)
	}
	globalDB, err := db.OpenMemory()
	if err != nil {
		b.Fatalf("open global db: %v", err)
	}
	b.Cleanup(func() { projectDB.Close(); globalDB.Close() })

	ps := store.NewMemoryStore(projectDB)
	gs := store.NewMemoryStore(globalDB)
	cfg := config.Default()
	cfg.Graph.ExpansionEnabled = true
	cfg.Graph.ExpansionThreshold = 0.3
	cfg.Graph.ExpansionFanOutCap = 50

	svc := service.NewMemoryService(ps, gs, cfg, "test/graph", embed.NopEmbedder{})
	ctx := context.Background()

	// Create entities.
	entities := make([]string, numEntities)
	for i := 0; i < numEntities; i++ {
		e, err := ps.CreateEntity(ctx, &model.Entity{
			Name:    fmt.Sprintf("bench-ent-%d", i),
			Kind:    model.KindModule,
			Project: "test/graph",
		})
		if err != nil {
			b.Fatalf("CreateEntity %d: %v", i, err)
		}
		entities[i] = e.ID
	}

	// Create relations in a ring + some cross-links to simulate a real graph.
	relsCreated := 0
	for i := 0; i < numEntities && relsCreated < numRelations; i++ {
		next := (i + 1) % numEntities
		_, err := ps.CreateRelation(ctx, &model.Relation{
			SourceID: entities[i],
			TargetID: entities[next],
			Type:     model.RelRelatedTo,
			Weight:   0.5,
		})
		if err != nil {
			b.Fatalf("CreateRelation: %v", err)
		}
		relsCreated++
	}
	// Add cross-links until we reach numRelations.
	step := numEntities / 10
	if step < 2 {
		step = 2
	}
	for i := 0; i < numEntities && relsCreated < numRelations; i += step {
		j := (i + step/2) % numEntities
		_, err := ps.CreateRelation(ctx, &model.Relation{
			SourceID: entities[i],
			TargetID: entities[j],
			Type:     model.RelRelatedTo,
			Weight:   0.6,
		})
		if err != nil {
			b.Fatalf("CreateRelation cross: %v", err)
		}
		relsCreated++
	}

	// Create seed memories and link them to the first numSeeds entities.
	seedIDs := make([]string, numSeeds)
	for i := 0; i < numSeeds; i++ {
		resp, err := svc.Save(ctx, model.SaveRequest{
			Title:   fmt.Sprintf("bench-seed-%d", i),
			Content: fmt.Sprintf("benchmark seed memory %d", i),
		})
		if err != nil {
			b.Fatalf("Save seed %d: %v", i, err)
		}
		seedIDs[i] = resp.ID
		if err := ps.LinkMemoryEntity(ctx, resp.ID, entities[i], "mention"); err != nil {
			b.Fatalf("LinkMemoryEntity seed %d: %v", i, err)
		}
	}

	opts := service.DefaultGraphBuildOptions()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		graph, _ := svc.BuildGraphForSeeds(ctx, seedIDs, opts)
		_ = graph
	}
}

