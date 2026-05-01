package scoring

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"testing"
)

// ─── NewSparseGraph ──────────────────────────────────────────────────────────

func TestNewSparseGraph_Empty(t *testing.T) {
	t.Parallel()
	g := NewSparseGraph(nil)
	if len(g.Nodes) != 0 {
		t.Fatalf("expected 0 nodes, got %d", len(g.Nodes))
	}
	if len(g.Adj) != 0 {
		t.Fatalf("expected empty Adj, got %d entries", len(g.Adj))
	}
}

func TestNewSparseGraph_Basic(t *testing.T) {
	t.Parallel()
	edges := []Edge{
		{From: "A", To: "B", Weight: 0.8},
		{From: "B", To: "C", Weight: 0.5},
		{From: "A", To: "C", Weight: 0.3},
	}
	g := NewSparseGraph(edges)

	// Three distinct nodes.
	if len(g.Nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(g.Nodes))
	}
	// Adjacency checks.
	if g.Adj["A"]["B"] != 0.8 {
		t.Errorf("Adj[A][B] = %v, want 0.8", g.Adj["A"]["B"])
	}
	if g.Adj["B"]["C"] != 0.5 {
		t.Errorf("Adj[B][C] = %v, want 0.5", g.Adj["B"]["C"])
	}
	// OutStrength checks.
	wantOutA := 0.8 + 0.3
	if math.Abs(g.OutStrength["A"]-wantOutA) > 1e-12 {
		t.Errorf("OutStrength[A] = %v, want %v", g.OutStrength["A"], wantOutA)
	}
	if g.OutStrength["B"] != 0.5 {
		t.Errorf("OutStrength[B] = %v, want 0.5", g.OutStrength["B"])
	}
}

func TestNewSparseGraph_FilterZeroWeight(t *testing.T) {
	t.Parallel()
	edges := []Edge{
		{From: "A", To: "B", Weight: 0.5},
		{From: "A", To: "C", Weight: 0.0},  // zero — filtered
		{From: "A", To: "D", Weight: -0.1}, // negative — filtered
	}
	g := NewSparseGraph(edges)

	// C and D should not appear because their only edge was filtered.
	for _, n := range g.Nodes {
		if n == "C" || n == "D" {
			t.Errorf("node %q should not be present (edge was filtered)", n)
		}
	}
	if len(g.Nodes) != 2 { // A and B
		t.Errorf("expected 2 nodes, got %d", len(g.Nodes))
	}
}

func TestNewSparseGraph_DanglingNode(t *testing.T) {
	t.Parallel()
	// B appears only as a target — no outgoing edges.
	edges := []Edge{
		{From: "A", To: "B", Weight: 1.0},
	}
	g := NewSparseGraph(edges)

	if g.OutStrength["B"] != 0 {
		t.Errorf("OutStrength[B] = %v, want 0 (dangling)", g.OutStrength["B"])
	}
	if len(g.Adj["B"]) != 0 {
		t.Errorf("Adj[B] should be empty for a dangling node")
	}
}

func TestNewSparseGraph_DuplicateEdge(t *testing.T) {
	t.Parallel()
	// When the same From/To pair appears twice, the last weight wins.
	edges := []Edge{
		{From: "A", To: "B", Weight: 0.3},
		{From: "A", To: "B", Weight: 0.9},
	}
	g := NewSparseGraph(edges)

	if g.Adj["A"]["B"] != 0.9 {
		t.Errorf("Adj[A][B] = %v, want 0.9 (last write wins)", g.Adj["A"]["B"])
	}
	// OutStrength should reflect only the surviving edge.
	if math.Abs(g.OutStrength["A"]-0.9) > 1e-12 {
		t.Errorf("OutStrength[A] = %v, want 0.9", g.OutStrength["A"])
	}
}

func TestNewSparseGraph_SortedNodes(t *testing.T) {
	t.Parallel()
	edges := []Edge{
		{From: "Z", To: "A", Weight: 0.5},
		{From: "M", To: "Z", Weight: 0.5},
		{From: "A", To: "M", Weight: 0.5},
	}
	g := NewSparseGraph(edges)

	if !sort.StringsAreSorted(g.Nodes) {
		t.Errorf("Nodes slice is not sorted: %v", g.Nodes)
	}
}

// ─── DefaultPPROptions ───────────────────────────────────────────────────────

func TestDefaultPPROptions(t *testing.T) {
	t.Parallel()
	opts := DefaultPPROptions()
	if opts.Alpha != 0.85 {
		t.Errorf("Alpha = %v, want 0.85", opts.Alpha)
	}
	if opts.MaxIter != 100 {
		t.Errorf("MaxIter = %d, want 100", opts.MaxIter)
	}
	if opts.Epsilon != 1e-6 {
		t.Errorf("Epsilon = %v, want 1e-6", opts.Epsilon)
	}
}

// ─── Validation errors ───────────────────────────────────────────────────────

func TestPPR_Error_NoSeeds(t *testing.T) {
	t.Parallel()
	g := NewSparseGraph([]Edge{{From: "A", To: "B", Weight: 1.0}})
	_, err := PPR(g, nil, DefaultPPROptions())
	if err == nil {
		t.Fatal("expected error for empty seeds, got nil")
	}
}

func TestPPR_Error_AlphaZero(t *testing.T) {
	t.Parallel()
	g := NewSparseGraph([]Edge{{From: "A", To: "B", Weight: 1.0}})
	opts := DefaultPPROptions()
	opts.Alpha = 0
	_, err := PPR(g, []NodeID{"A"}, opts)
	if err == nil {
		t.Fatal("expected error for alpha=0, got nil")
	}
}

func TestPPR_Error_AlphaOne(t *testing.T) {
	t.Parallel()
	g := NewSparseGraph([]Edge{{From: "A", To: "B", Weight: 1.0}})
	opts := DefaultPPROptions()
	opts.Alpha = 1.0
	_, err := PPR(g, []NodeID{"A"}, opts)
	if err == nil {
		t.Fatal("expected error for alpha=1, got nil")
	}
}

func TestPPR_Error_AlphaNegative(t *testing.T) {
	t.Parallel()
	g := NewSparseGraph([]Edge{{From: "A", To: "B", Weight: 1.0}})
	opts := DefaultPPROptions()
	opts.Alpha = -0.5
	_, err := PPR(g, []NodeID{"A"}, opts)
	if err == nil {
		t.Fatal("expected error for alpha=-0.5, got nil")
	}
}

func TestPPR_Error_MaxIterZero(t *testing.T) {
	t.Parallel()
	g := NewSparseGraph([]Edge{{From: "A", To: "B", Weight: 1.0}})
	opts := DefaultPPROptions()
	opts.MaxIter = 0
	_, err := PPR(g, []NodeID{"A"}, opts)
	if err == nil {
		t.Fatal("expected error for max_iter=0, got nil")
	}
}

func TestPPR_Error_EpsilonZero(t *testing.T) {
	t.Parallel()
	g := NewSparseGraph([]Edge{{From: "A", To: "B", Weight: 1.0}})
	opts := DefaultPPROptions()
	opts.Epsilon = 0
	_, err := PPR(g, []NodeID{"A"}, opts)
	if err == nil {
		t.Fatal("expected error for epsilon=0, got nil")
	}
}

func TestPPR_Error_NoSeedsInGraph(t *testing.T) {
	t.Parallel()
	g := NewSparseGraph([]Edge{{From: "A", To: "B", Weight: 1.0}})
	_, err := PPR(g, []NodeID{"X", "Y"}, DefaultPPROptions())
	if err == nil {
		t.Fatal("expected error when no seeds exist in graph, got nil")
	}
}

// ─── Core algorithm ──────────────────────────────────────────────────────────

// TestPPR_SingleSeed_Chain verifies ranking order on a linear graph A→B→C→D.
// With seed=[A] and alpha=0.85, mass decays along the chain so
// score(A) > score(B) > score(C) > score(D).
func TestPPR_SingleSeed_Chain(t *testing.T) {
	t.Parallel()
	edges := []Edge{
		{From: "A", To: "B", Weight: 1.0},
		{From: "B", To: "C", Weight: 1.0},
		{From: "C", To: "D", Weight: 1.0},
	}
	g := NewSparseGraph(edges)
	res, err := PPR(g, []NodeID{"A"}, DefaultPPROptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !(res.Scores["A"] > res.Scores["B"] &&
		res.Scores["B"] > res.Scores["C"] &&
		res.Scores["C"] > res.Scores["D"]) {
		t.Errorf("expected A>B>C>D, got A=%v B=%v C=%v D=%v",
			res.Scores["A"], res.Scores["B"], res.Scores["C"], res.Scores["D"])
	}
}

// TestPPR_SingleSeed_Star verifies symmetric distribution.
// A→B, A→C, A→D with equal weights: score(B) ≈ score(C) ≈ score(D).
func TestPPR_SingleSeed_Star(t *testing.T) {
	t.Parallel()
	edges := []Edge{
		{From: "A", To: "B", Weight: 1.0},
		{From: "A", To: "C", Weight: 1.0},
		{From: "A", To: "D", Weight: 1.0},
	}
	g := NewSparseGraph(edges)
	res, err := PPR(g, []NodeID{"A"}, DefaultPPROptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	const tol = 1e-9
	if math.Abs(res.Scores["B"]-res.Scores["C"]) > tol ||
		math.Abs(res.Scores["C"]-res.Scores["D"]) > tol {
		t.Errorf("star leaves should have equal scores: B=%v C=%v D=%v",
			res.Scores["B"], res.Scores["C"], res.Scores["D"])
	}
	if res.Scores["A"] <= res.Scores["B"] {
		t.Errorf("seed A should dominate leaves, got A=%v B=%v", res.Scores["A"], res.Scores["B"])
	}
}

// TestPPR_MultipleSeeds_Symmetric verifies that a symmetric graph with two
// seeds produces equal scores for both seeds.
func TestPPR_MultipleSeeds_Symmetric(t *testing.T) {
	t.Parallel()
	// A ⇄ B, each direction weight 1.0.
	edges := []Edge{
		{From: "A", To: "B", Weight: 1.0},
		{From: "B", To: "A", Weight: 1.0},
	}
	g := NewSparseGraph(edges)
	res, err := PPR(g, []NodeID{"A", "B"}, DefaultPPROptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	const tol = 1e-9
	if math.Abs(res.Scores["A"]-res.Scores["B"]) > tol {
		t.Errorf("symmetric seeds should have equal scores: A=%v B=%v", res.Scores["A"], res.Scores["B"])
	}
}

func TestPPR_Convergence(t *testing.T) {
	t.Parallel()
	g := NewSparseGraph(chainEdges("node", 10, 1.0))
	res, err := PPR(g, []NodeID{"node0"}, DefaultPPROptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Converged {
		t.Errorf("expected Converged=true, got false after %d iterations", res.Iterations)
	}
}

func TestPPR_MaxIterReached(t *testing.T) {
	t.Parallel()
	g := NewSparseGraph(chainEdges("node", 10, 1.0))
	opts := DefaultPPROptions()
	opts.MaxIter = 5
	opts.Epsilon = 1e-20 // effectively never converge within 5 iters
	res, err := PPR(g, []NodeID{"node0"}, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Converged {
		t.Error("expected Converged=false with epsilon=1e-20 and max_iter=5")
	}
	if res.Iterations != 5 {
		t.Errorf("Iterations = %d, want 5", res.Iterations)
	}
}

// TestPPR_MassConservation verifies |1.0 - Σ scores| < 1e-10 (invariant 1).
func TestPPR_MassConservation(t *testing.T) {
	t.Parallel()
	edges := randomEdges("n", 50, 150, 42)
	g := NewSparseGraph(edges)
	res, err := PPR(g, []NodeID{"n0", "n1", "n2"}, DefaultPPROptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sum := 0.0
	for _, s := range res.Scores {
		sum += s
	}
	if math.Abs(1.0-sum) > 1e-10 {
		t.Errorf("|1.0 - Σscores| = %e, want < 1e-10 (got sum=%v)", math.Abs(1.0-sum), sum)
	}
}

// TestPPR_DanglingNode verifies that dangling-node mass is redistributed to
// seeds and does not cause total mass to decay.
func TestPPR_DanglingNode(t *testing.T) {
	t.Parallel()
	// A → B (B is dangling), seed = A.
	edges := []Edge{{From: "A", To: "B", Weight: 1.0}}
	g := NewSparseGraph(edges)
	res, err := PPR(g, []NodeID{"A"}, DefaultPPROptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sum := 0.0
	for _, s := range res.Scores {
		sum += s
	}
	if math.Abs(1.0-sum) > 1e-10 {
		t.Errorf("mass not conserved with dangling node: sum=%v", sum)
	}
	// Seed A must still receive mass (via teleport from dangling B).
	if res.Scores["A"] <= 0 {
		t.Errorf("seed A should have positive score, got %v", res.Scores["A"])
	}
}

// TestPPR_Deterministic verifies that two independent runs produce bit-exact results.
func TestPPR_Deterministic(t *testing.T) {
	t.Parallel()
	edges := randomEdges("n", 30, 90, 7)
	g := NewSparseGraph(edges)
	seeds := []NodeID{"n0", "n5", "n10"}

	res1, err := PPR(g, seeds, DefaultPPROptions())
	if err != nil {
		t.Fatalf("run 1 error: %v", err)
	}
	res2, err := PPR(g, seeds, DefaultPPROptions())
	if err != nil {
		t.Fatalf("run 2 error: %v", err)
	}

	for node, s1 := range res1.Scores {
		s2 := res2.Scores[node]
		if s1 != s2 {
			t.Errorf("non-deterministic result for node %q: %v vs %v", node, s1, s2)
		}
	}
}

// ─── Edge cases ──────────────────────────────────────────────────────────────

// TestPPR_DisconnectedGraph verifies that nodes disconnected from seeds receive
// score 0.
func TestPPR_DisconnectedGraph(t *testing.T) {
	t.Parallel()
	edges := []Edge{
		{From: "A", To: "B", Weight: 1.0},
		// Isolated component — no path from A.
		{From: "X", To: "Y", Weight: 1.0},
	}
	g := NewSparseGraph(edges)
	res, err := PPR(g, []NodeID{"A"}, DefaultPPROptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Scores["X"] != 0 || res.Scores["Y"] != 0 {
		t.Errorf("disconnected nodes should score 0, got X=%v Y=%v", res.Scores["X"], res.Scores["Y"])
	}
}

// TestPPR_SingleNodeGraph verifies that a lone seed with no edges scores 1.0.
func TestPPR_SingleNodeGraph(t *testing.T) {
	t.Parallel()
	// A seed that appears only as a source but points to nothing that is in
	// the graph node list will be a dangling node (OutStrength=0).
	// Build a graph with just one self-referencing dangling node by adding
	// it as an isolated seed via a zero-edge graph trick:
	// we need at least one edge for the node to appear; use A→A (self-loop).
	edges := []Edge{{From: "solo", To: "solo", Weight: 1.0}}
	g := NewSparseGraph(edges)
	res, err := PPR(g, []NodeID{"solo"}, DefaultPPROptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(res.Scores["solo"]-1.0) > 1e-9 {
		t.Errorf("single node should score 1.0, got %v", res.Scores["solo"])
	}
}

// TestPPR_LargeFanOut verifies convergence with a hub that has 200 neighbors.
func TestPPR_LargeFanOut(t *testing.T) {
	t.Parallel()
	edges := make([]Edge, 0, 200)
	for i := 0; i < 200; i++ {
		edges = append(edges, Edge{
			From:   "hub",
			To:     fmt.Sprintf("leaf%d", i),
			Weight: 1.0,
		})
	}
	g := NewSparseGraph(edges)
	opts := DefaultPPROptions()
	opts.MaxIter = 50
	res, err := PPR(g, []NodeID{"hub"}, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Converged {
		t.Errorf("expected convergence within 50 iterations for large fan-out, got %d iters", res.Iterations)
	}
}

// TestPPR_DuplicateSeeds verifies that duplicate seeds are deduped and the
// teleport vector sums to 1.
func TestPPR_DuplicateSeeds(t *testing.T) {
	t.Parallel()
	edges := []Edge{
		{From: "A", To: "C", Weight: 1.0},
		{From: "B", To: "C", Weight: 1.0},
	}
	g := NewSparseGraph(edges)
	// A appears twice; effective seed set is {A, B} → teleport = {A:0.5, B:0.5}.
	resDedup, err := PPR(g, []NodeID{"A", "A", "B"}, DefaultPPROptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resNormal, err := PPR(g, []NodeID{"A", "B"}, DefaultPPROptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, node := range g.Nodes {
		if resDedup.Scores[node] != resNormal.Scores[node] {
			t.Errorf("duplicate seeds produce different score for %q: %v vs %v",
				node, resDedup.Scores[node], resNormal.Scores[node])
		}
	}
}

// TestPPR_SelfLoop verifies that a self-loop does not cause division-by-zero
// or incorrect scores.
func TestPPR_SelfLoop(t *testing.T) {
	t.Parallel()
	// A→A only. A is both a source and a dangling-like node for PPR purposes
	// (it has OutStrength > 0 so it is not dangling, but it loops).
	edges := []Edge{{From: "A", To: "A", Weight: 1.0}}
	g := NewSparseGraph(edges)
	res, err := PPR(g, []NodeID{"A"}, DefaultPPROptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(res.Scores["A"]-1.0) > 1e-9 {
		t.Errorf("self-loop single-seed should score 1.0, got %v", res.Scores["A"])
	}
}

// TestPPR_WeightedPreference verifies that heavier edges attract more mass.
func TestPPR_WeightedPreference(t *testing.T) {
	t.Parallel()
	edges := []Edge{
		{From: "A", To: "B", Weight: 0.9},
		{From: "A", To: "C", Weight: 0.1},
	}
	g := NewSparseGraph(edges)
	res, err := PPR(g, []NodeID{"A"}, DefaultPPROptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Scores["B"] <= res.Scores["C"] {
		t.Errorf("heavier edge to B should produce score(B)>score(C), got B=%v C=%v",
			res.Scores["B"], res.Scores["C"])
	}
}

// TestPPR_PartialSeedsInGraph verifies that seeds missing from the graph are
// silently skipped when at least one seed is present.
func TestPPR_PartialSeedsInGraph(t *testing.T) {
	t.Parallel()
	edges := []Edge{{From: "A", To: "B", Weight: 1.0}}
	g := NewSparseGraph(edges)
	// "A" is present; "MISSING" is not — should not error.
	_, err := PPR(g, []NodeID{"A", "MISSING"}, DefaultPPROptions())
	if err != nil {
		t.Errorf("partial seeds should succeed: %v", err)
	}
}

// ─── Integration tests ───────────────────────────────────────────────────────

// TestPPR_RealisticGraph_100Nodes verifies convergence and that top-ranked
// nodes are topologically close to seeds.
func TestPPR_RealisticGraph_100Nodes(t *testing.T) {
	t.Parallel()
	edges := randomEdges("m", 100, 300, 99)
	g := NewSparseGraph(edges)
	seeds := []NodeID{"m0", "m1", "m2"}
	res, err := PPR(g, seeds, DefaultPPROptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Converged {
		t.Errorf("expected convergence on 100-node graph, got %d iterations", res.Iterations)
	}
	// Mass conservation.
	sum := sumScores(res.Scores)
	if math.Abs(1.0-sum) > 1e-10 {
		t.Errorf("|1 - Σscores| = %e (100-node graph)", math.Abs(1.0-sum))
	}
	// Seeds must appear in the top of the ranking (they receive guaranteed
	// teleport mass each iteration).
	type kv struct {
		id    string
		score float64
	}
	ranked := make([]kv, 0, len(res.Scores))
	for id, s := range res.Scores {
		ranked = append(ranked, kv{id, s})
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	top10 := make(map[string]bool, 10)
	for i := 0; i < 10 && i < len(ranked); i++ {
		top10[ranked[i].id] = true
	}
	for _, s := range seeds {
		if !top10[s] {
			t.Errorf("seed %q not in top-10 (it must receive teleport mass)", s)
		}
	}
}

// TestPPR_RealisticGraph_1000Nodes verifies convergence within 40 iterations
// and mass conservation on a larger graph.
func TestPPR_RealisticGraph_1000Nodes(t *testing.T) {
	t.Parallel()
	edges := randomEdges("p", 1000, 3000, 13)
	g := NewSparseGraph(edges)
	seeds := []NodeID{"p0", "p1", "p2", "p3", "p4"}
	res, err := PPR(g, seeds, DefaultPPROptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Iterations > 40 {
		t.Errorf("expected convergence in ≤40 iterations on 1K-node graph, got %d", res.Iterations)
	}
	sum := sumScores(res.Scores)
	if math.Abs(1.0-sum) > 1e-10 {
		t.Errorf("|1 - Σscores| = %e (1K-node graph)", math.Abs(1.0-sum))
	}
}

// ─── Benchmarks ─────────────────────────────────────────────────────────────

func BenchmarkNewSparseGraph_10K_Edges(b *testing.B) {
	edges := randomEdges("e", 5000, 10000, 1)
	b.ResetTimer()
	for range b.N {
		_ = NewSparseGraph(edges)
	}
}

func BenchmarkPPR_1K_Nodes(b *testing.B) {
	g := NewSparseGraph(randomEdges("q", 1000, 3000, 2))
	seeds := []NodeID{"q0", "q1", "q2"}
	opts := DefaultPPROptions()
	b.ResetTimer()
	for range b.N {
		_, _ = PPR(g, seeds, opts)
	}
}

func BenchmarkPPR_5K_50K(b *testing.B) {
	g := NewSparseGraph(randomEdges("r", 5000, 50000, 3))
	seeds := []NodeID{"r0", "r1", "r2", "r3", "r4"}
	opts := DefaultPPROptions()
	b.ResetTimer()
	for range b.N {
		_, _ = PPR(g, seeds, opts)
	}
}

func BenchmarkPPR_10K_Nodes(b *testing.B) {
	g := NewSparseGraph(randomEdges("s", 10000, 30000, 4))
	seeds := []NodeID{"s0", "s1", "s2", "s3", "s4", "s5", "s6", "s7", "s8", "s9"}
	opts := DefaultPPROptions()
	b.ResetTimer()
	for range b.N {
		_, _ = PPR(g, seeds, opts)
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// chainEdges builds a linear graph prefix0 → prefix1 → … → prefixN-1.
func chainEdges(prefix string, n int, weight float64) []Edge {
	edges := make([]Edge, 0, n-1)
	for i := 0; i < n-1; i++ {
		edges = append(edges, Edge{
			From:   fmt.Sprintf("%s%d", prefix, i),
			To:     fmt.Sprintf("%s%d", prefix, i+1),
			Weight: weight,
		})
	}
	return edges
}

// randomEdges generates count directed edges among nodes named prefix0..prefixN-1
// using the given random seed for reproducibility.
func randomEdges(prefix string, nodes, count int, seed int64) []Edge {
	rng := rand.New(rand.NewSource(seed))
	edges := make([]Edge, 0, count)
	for range count {
		from := fmt.Sprintf("%s%d", prefix, rng.Intn(nodes))
		to := fmt.Sprintf("%s%d", prefix, rng.Intn(nodes))
		weight := 0.1 + rng.Float64()*0.9 // (0.1, 1.0]
		edges = append(edges, Edge{From: from, To: to, Weight: weight})
	}
	return edges
}

// sumScores sums all values in a score map.
func sumScores(scores map[NodeID]float64) float64 {
	sum := 0.0
	for _, s := range scores {
		sum += s
	}
	return sum
}
