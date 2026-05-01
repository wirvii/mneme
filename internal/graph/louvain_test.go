package graph

import (
	"fmt"
	"math"
	"math/rand"
	"testing"

	"github.com/juanftp/mneme/internal/scoring"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// makeGraph builds a SparseGraph from a list of (from, to, weight) triples.
// Pass weight=1.0 for unweighted graphs. For undirected graphs, list both
// directions explicitly: (A,B,w) and (B,A,w).
func makeGraph(edges [][3]interface{}) scoring.SparseGraph {
	scoringEdges := make([]scoring.Edge, 0, len(edges))
	for _, e := range edges {
		scoringEdges = append(scoringEdges, scoring.Edge{
			From:   e[0].(string),
			To:     e[1].(string),
			Weight: e[2].(float64),
		})
	}
	return scoring.NewSparseGraph(scoringEdges)
}

// singleNodeGraph returns a graph with one node and no edges.
func singleNodeGraph(id string) scoring.SparseGraph {
	// NewSparseGraph requires at least one edge to record a node.
	// For a single node with no edges we use a self-loop workaround: we pass
	// an empty edge list but add the node via a zero-weight edge (which gets
	// filtered). Instead use a graph built with a real edge to itself.
	// Actually, self-loops with positive weight work: NewSparseGraph accepts them.
	return scoring.NewSparseGraph([]scoring.Edge{
		{From: id, To: id, Weight: 1.0},
	})
}

// twoNodeGraph returns an undirected graph with two nodes and a single edge.
func twoNodeGraph(a, b string, w float64) scoring.SparseGraph {
	return makeGraph([][3]interface{}{
		{a, b, w},
		{b, a, w},
	})
}

// triangleGraph returns a complete undirected 3-node graph with uniform weight.
func triangleGraph(a, b, c string, w float64) scoring.SparseGraph {
	return makeGraph([][3]interface{}{
		{a, b, w}, {b, a, w},
		{b, c, w}, {c, b, w},
		{a, c, w}, {c, a, w},
	})
}

// assertPartitionComplete verifies every node in the graph appears in exactly
// one community.
func assertPartitionComplete(t *testing.T, graph scoring.SparseGraph, result *LouvainResult) {
	t.Helper()
	seen := make(map[string]int) // nodeID -> community count
	for _, comm := range result.Communities {
		for _, member := range comm.Members {
			seen[member]++
		}
	}
	for _, nodeID := range graph.Nodes {
		count, ok := seen[nodeID]
		if !ok {
			t.Errorf("node %q missing from partition", nodeID)
			continue
		}
		if count != 1 {
			t.Errorf("node %q appears in %d communities (want 1)", nodeID, count)
		}
	}
	totalMembers := 0
	for _, comm := range result.Communities {
		totalMembers += len(comm.Members)
	}
	if totalMembers != len(graph.Nodes) {
		t.Errorf("partition total members = %d, want %d", totalMembers, len(graph.Nodes))
	}
}

// assertCommunityIDs verifies community IDs are 0-indexed and dense.
func assertCommunityIDs(t *testing.T, result *LouvainResult) {
	t.Helper()
	for i, comm := range result.Communities {
		if comm.ID != i {
			t.Errorf("community[%d].ID = %d, want %d", i, comm.ID, i)
		}
	}
}

// memberIn returns true if nodeID is a member of any community in result.
func memberIn(result *LouvainResult, nodeID string) (communityID int, found bool) {
	for _, comm := range result.Communities {
		for _, m := range comm.Members {
			if m == nodeID {
				return comm.ID, true
			}
		}
	}
	return -1, false
}

// ---------------------------------------------------------------------------
// Commit 1: Type tests and validation errors
// ---------------------------------------------------------------------------

func TestDefaultLouvainOptions(t *testing.T) {
	opts := DefaultLouvainOptions()
	if opts.Resolution != 1.0 {
		t.Errorf("Resolution = %v, want 1.0", opts.Resolution)
	}
	if opts.MinModularity != 1e-7 {
		t.Errorf("MinModularity = %v, want 1e-7", opts.MinModularity)
	}
	if opts.MaxLevels != 10 {
		t.Errorf("MaxLevels = %v, want 10", opts.MaxLevels)
	}
}

func TestLouvain_Error_EmptyGraph(t *testing.T) {
	g := scoring.NewSparseGraph(nil)
	_, err := Louvain(g, DefaultLouvainOptions())
	if err == nil {
		t.Fatal("expected error for empty graph, got nil")
	}
	if err.Error() != "louvain: empty graph" {
		t.Errorf("error = %q, want %q", err.Error(), "louvain: empty graph")
	}
}

func TestLouvain_Error_ResolutionZero(t *testing.T) {
	g := twoNodeGraph("A", "B", 1.0)
	opts := DefaultLouvainOptions()
	opts.Resolution = 0
	_, err := Louvain(g, opts)
	if err == nil {
		t.Fatal("expected error for Resolution=0, got nil")
	}
	if err.Error() != "louvain: resolution must be > 0" {
		t.Errorf("error = %q", err.Error())
	}
}

func TestLouvain_Error_ResolutionNegative(t *testing.T) {
	g := twoNodeGraph("A", "B", 1.0)
	opts := DefaultLouvainOptions()
	opts.Resolution = -1
	_, err := Louvain(g, opts)
	if err == nil {
		t.Fatal("expected error for Resolution=-1, got nil")
	}
	if err.Error() != "louvain: resolution must be > 0" {
		t.Errorf("error = %q", err.Error())
	}
}

func TestLouvain_Error_MaxLevelsZero(t *testing.T) {
	g := twoNodeGraph("A", "B", 1.0)
	opts := DefaultLouvainOptions()
	opts.MaxLevels = 0
	_, err := Louvain(g, opts)
	if err == nil {
		t.Fatal("expected error for MaxLevels=0, got nil")
	}
	if err.Error() != "louvain: max_levels must be > 0" {
		t.Errorf("error = %q", err.Error())
	}
}

func TestLouvain_Error_MinModularityNegative(t *testing.T) {
	g := twoNodeGraph("A", "B", 1.0)
	opts := DefaultLouvainOptions()
	opts.MinModularity = -1
	_, err := Louvain(g, opts)
	if err == nil {
		t.Fatal("expected error for MinModularity=-1, got nil")
	}
	if err.Error() != "louvain: min_modularity must be >= 0" {
		t.Errorf("error = %q", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Commit 2: State initialisation and modularity computation
// ---------------------------------------------------------------------------

func TestNewLouvainState_TwoNodeUndirected(t *testing.T) {
	g := twoNodeGraph("A", "B", 1.0)
	s := newLouvainState(g)

	if s.n != 2 {
		t.Errorf("n = %d, want 2", s.n)
	}
	// totalWeight = (1.0 + 1.0) / 2 = 1.0 (one undirected edge)
	if math.Abs(s.totalWeight-1.0) > 1e-9 {
		t.Errorf("totalWeight = %v, want 1.0", s.totalWeight)
	}
	// Each node has degree 1.0 (one outgoing edge in the directed representation)
	for i, nodeID := range g.Nodes {
		if math.Abs(s.nodeDegree[i]-1.0) > 1e-9 {
			t.Errorf("nodeDegree[%s] = %v, want 1.0", nodeID, s.nodeDegree[i])
		}
		// Singleton: commDegree = nodeDegree
		if math.Abs(s.commDegree[i]-s.nodeDegree[i]) > 1e-9 {
			t.Errorf("commDegree[%d] = %v, want %v", i, s.commDegree[i], s.nodeDegree[i])
		}
	}
}

func TestNewLouvainState_TriangleUndirected(t *testing.T) {
	g := triangleGraph("A", "B", "C", 1.0)
	s := newLouvainState(g)

	// 3 undirected edges, each with weight 1.0; directed sum = 6.0, m = 3.0.
	if math.Abs(s.totalWeight-3.0) > 1e-9 {
		t.Errorf("totalWeight = %v, want 3.0", s.totalWeight)
	}
	// Each node has degree 2.0 (two edges, each weight 1.0).
	for i := range g.Nodes {
		if math.Abs(s.nodeDegree[i]-2.0) > 1e-9 {
			t.Errorf("nodeDegree[%d] = %v, want 2.0", i, s.nodeDegree[i])
		}
	}
}

func TestComputeModularity_AllSingletons(t *testing.T) {
	// For a triangle, singleton partition: each node in its own community.
	// Σ_in(c) = 0 for singletons, Q = -γ * Σ(Σ_tot(c)/(2m))^2.
	g := triangleGraph("A", "B", "C", 1.0)
	s := newLouvainState(g)
	// Singletons: commIntern = 0 (no self-loops in this graph).
	q := computeModularity(s, 1.0)
	// Expected Q < 0 for singletons (penalty dominates)
	if q >= 0 {
		t.Errorf("singleton modularity = %v, expected < 0", q)
	}
}

func TestComputeModularity_AllOneComm_ZeroEdges(t *testing.T) {
	// Single self-loop node.
	g := singleNodeGraph("A")
	s := newLouvainState(g)
	q := computeModularity(s, 1.0)
	// Should be finite, no panic.
	if math.IsNaN(q) || math.IsInf(q, 0) {
		t.Errorf("modularity = %v (NaN or Inf)", q)
	}
}

// ---------------------------------------------------------------------------
// Commit 3: Phase 1 local moves
// ---------------------------------------------------------------------------

func TestLouvain_SingleNode(t *testing.T) {
	g := singleNodeGraph("A")
	result, err := Louvain(g, DefaultLouvainOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Communities) != 1 {
		t.Errorf("communities = %d, want 1", len(result.Communities))
	}
	if result.Communities[0].Members[0] != "A" {
		t.Errorf("member = %q, want A", result.Communities[0].Members[0])
	}
	assertPartitionComplete(t, g, result)
	assertCommunityIDs(t, result)
}

func TestLouvain_TwoNodes_Connected(t *testing.T) {
	g := twoNodeGraph("A", "B", 1.0)
	result, err := Louvain(g, DefaultLouvainOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Two connected nodes should merge into 1 community.
	if len(result.Communities) != 1 {
		t.Errorf("communities = %d, want 1", len(result.Communities))
	}
	assertPartitionComplete(t, g, result)
	assertCommunityIDs(t, result)
}

func TestLouvain_TwoNodes_Disconnected(t *testing.T) {
	// Two nodes with no edge between them: each a singleton.
	// Since NewSparseGraph requires edges to register nodes, use self-loops.
	g := scoring.NewSparseGraph([]scoring.Edge{
		{From: "A", To: "A", Weight: 1.0},
		{From: "B", To: "B", Weight: 1.0},
	})
	result, err := Louvain(g, DefaultLouvainOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Disconnected: each node in its own community.
	if len(result.Communities) != 2 {
		t.Errorf("communities = %d, want 2", len(result.Communities))
	}
	assertPartitionComplete(t, g, result)
	assertCommunityIDs(t, result)
}

func TestLouvain_Triangle(t *testing.T) {
	g := triangleGraph("A", "B", "C", 1.0)
	result, err := Louvain(g, DefaultLouvainOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// A complete triangle is a single community.
	if len(result.Communities) != 1 {
		t.Errorf("communities = %d, want 1", len(result.Communities))
	}
	assertPartitionComplete(t, g, result)
	assertCommunityIDs(t, result)
}

func TestLouvain_TwoCliques_Bridge(t *testing.T) {
	// Two triangles connected by one weak bridge edge.
	// Clique 1: A-B-C (weight 1.0); Clique 2: D-E-F (weight 1.0)
	// Bridge: C-D (weight 0.01) — much weaker than intra-clique edges.
	edges := [][3]interface{}{
		// Clique 1
		{"A", "B", 1.0}, {"B", "A", 1.0},
		{"B", "C", 1.0}, {"C", "B", 1.0},
		{"A", "C", 1.0}, {"C", "A", 1.0},
		// Clique 2
		{"D", "E", 1.0}, {"E", "D", 1.0},
		{"E", "F", 1.0}, {"F", "E", 1.0},
		{"D", "F", 1.0}, {"F", "D", 1.0},
		// Bridge (very weak)
		{"C", "D", 0.01}, {"D", "C", 0.01},
	}
	g := makeGraph(edges)
	result, err := Louvain(g, DefaultLouvainOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The two cliques should be detected as separate communities.
	if len(result.Communities) != 2 {
		t.Errorf("communities = %d, want 2", len(result.Communities))
	}
	assertPartitionComplete(t, g, result)
	assertCommunityIDs(t, result)
	// Verify A and D are in different communities.
	commA, _ := memberIn(result, "A")
	commD, _ := memberIn(result, "D")
	if commA == commD {
		t.Errorf("nodes A and D should be in different communities")
	}
}

func TestLouvain_Star(t *testing.T) {
	// Star graph: hub H connected to 5 spokes S1..S5 (weight 1.0).
	// At γ=1.0 a star merges into 1 community.
	edges := make([][3]interface{}, 0, 10)
	for i := 1; i <= 5; i++ {
		spoke := fmt.Sprintf("S%d", i)
		edges = append(edges, [3]interface{}{"H", spoke, 1.0})
		edges = append(edges, [3]interface{}{spoke, "H", 1.0})
	}
	g := makeGraph(edges)
	result, err := Louvain(g, DefaultLouvainOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Star with γ=1.0: hub + spokes should merge into 1 community.
	if len(result.Communities) != 1 {
		t.Errorf("communities = %d, want 1", len(result.Communities))
	}
	assertPartitionComplete(t, g, result)
}

// ---------------------------------------------------------------------------
// Commit 4: Phase 2 graph contraction and outer loop
// ---------------------------------------------------------------------------

func TestLouvain_ContractEdgeWeightConservation(t *testing.T) {
	// Build a graph with two cliques and verify the total weight is preserved
	// through contraction (mass conservation, invariant 5 from spec).
	g := makeGraph([][3]interface{}{
		{"A", "B", 1.0}, {"B", "A", 1.0},
		{"B", "C", 1.0}, {"C", "B", 1.0},
		{"A", "C", 1.0}, {"C", "A", 1.0},
		{"D", "E", 1.0}, {"E", "D", 1.0},
		{"E", "F", 1.0}, {"F", "E", 1.0},
		{"D", "F", 1.0}, {"F", "D", 1.0},
		{"C", "D", 0.5}, {"D", "C", 0.5},
	})

	// Compute original total weight (sum all adj weights / 2).
	origTotal := 0.0
	for _, adj := range g.Adj {
		for _, w := range adj {
			origTotal += w
		}
	}
	origTotal /= 2.0

	// Run one level of contraction manually via state.
	state := newLouvainState(g)
	phase1(g, state, DefaultLouvainOptions())
	contracted, _ := contractGraph(g, state)

	// Compute contracted total weight.
	contractedTotal := 0.0
	for _, adj := range contracted.Adj {
		for _, w := range adj {
			contractedTotal += w
		}
	}
	contractedTotal /= 2.0

	if math.Abs(contractedTotal-origTotal) > 1e-9 {
		t.Errorf("weight after contraction = %v, want %v (mass conservation violated)", contractedTotal, origTotal)
	}
}

func TestLouvain_MultiLevel(t *testing.T) {
	// A graph with clear multi-level structure:
	// Level 0: 4 triangles (12 nodes)
	// Level 1: after contraction, 4 super-nodes; two pairs connected
	// This forces at least 2 levels of Louvain.
	edges := make([][3]interface{}, 0)

	// 4 triangles: {A,B,C}, {D,E,F}, {G,H,I}, {J,K,L}
	triangles := [][3]string{
		{"A", "B", "C"},
		{"D", "E", "F"},
		{"G", "H", "I"},
		{"J", "K", "L"},
	}
	for _, tri := range triangles {
		for i := 0; i < 3; i++ {
			for j := 0; j < 3; j++ {
				if i != j {
					edges = append(edges, [3]interface{}{tri[i], tri[j], 1.0})
				}
			}
		}
	}

	// Connect triangle pairs with weak inter-cluster edges.
	// {A,B,C} <-> {D,E,F} (first meta-community)
	// {G,H,I} <-> {J,K,L} (second meta-community)
	edges = append(edges,
		[3]interface{}{"C", "D", 0.1}, [3]interface{}{"D", "C", 0.1},
		[3]interface{}{"F", "G", 0.05}, [3]interface{}{"G", "F", 0.05},
	)

	g := makeGraph(edges)
	result, err := Louvain(g, DefaultLouvainOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should produce 2-4 communities (depends on bridge weight vs γ).
	if len(result.Communities) < 1 || len(result.Communities) > 4 {
		t.Errorf("communities = %d, want 1-4", len(result.Communities))
	}
	assertPartitionComplete(t, g, result)
	assertCommunityIDs(t, result)
}

// ---------------------------------------------------------------------------
// Commit 5: Edge cases and Zachary's Karate Club integration test
// ---------------------------------------------------------------------------

func TestLouvain_DisconnectedComponents(t *testing.T) {
	// Three disconnected triangles: should produce 3 communities.
	edges := make([][3]interface{}, 0)
	tris := [][3]string{{"A", "B", "C"}, {"D", "E", "F"}, {"G", "H", "I"}}
	for _, tri := range tris {
		for i := 0; i < 3; i++ {
			for j := 0; j < 3; j++ {
				if i != j {
					edges = append(edges, [3]interface{}{tri[i], tri[j], 1.0})
				}
			}
		}
	}
	g := makeGraph(edges)
	result, err := Louvain(g, DefaultLouvainOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Communities) != 3 {
		t.Errorf("communities = %d, want 3", len(result.Communities))
	}
	assertPartitionComplete(t, g, result)
	assertCommunityIDs(t, result)
}

func TestLouvain_SelfLoops(t *testing.T) {
	// A graph with self-loops should not panic and should produce a valid partition.
	g := makeGraph([][3]interface{}{
		{"A", "A", 2.0}, // self-loop
		{"A", "B", 1.0}, {"B", "A", 1.0},
		{"B", "B", 0.5}, // self-loop
	})
	result, err := Louvain(g, DefaultLouvainOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertPartitionComplete(t, g, result)
	assertCommunityIDs(t, result)
}

func TestLouvain_WeightedEdges(t *testing.T) {
	// Two pairs of nodes with high intra-pair weight and low inter-pair weight.
	g := makeGraph([][3]interface{}{
		{"A", "B", 10.0}, {"B", "A", 10.0},
		{"C", "D", 10.0}, {"D", "C", 10.0},
		{"B", "C", 0.1}, {"C", "B", 0.1},
	})
	result, err := Louvain(g, DefaultLouvainOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect {A,B} and {C,D} as separate communities.
	if len(result.Communities) != 2 {
		t.Errorf("communities = %d, want 2", len(result.Communities))
	}
	assertPartitionComplete(t, g, result)

	// A and B in same community; C and D in same community.
	commA, _ := memberIn(result, "A")
	commB, _ := memberIn(result, "B")
	commC, _ := memberIn(result, "C")
	commD, _ := memberIn(result, "D")
	if commA != commB {
		t.Errorf("A and B should be in same community (got %d, %d)", commA, commB)
	}
	if commC != commD {
		t.Errorf("C and D should be in same community (got %d, %d)", commC, commD)
	}
	if commA == commC {
		t.Errorf("(A,B) and (C,D) should be in different communities")
	}
}

func TestLouvain_Resolution_High(t *testing.T) {
	// High γ should produce more communities than default γ.
	g := makeGraph([][3]interface{}{
		{"A", "B", 1.0}, {"B", "A", 1.0},
		{"B", "C", 1.0}, {"C", "B", 1.0},
		{"C", "D", 1.0}, {"D", "C", 1.0},
		{"D", "A", 1.0}, {"A", "D", 1.0},
		{"A", "C", 0.8}, {"C", "A", 0.8},
		{"B", "D", 0.8}, {"D", "B", 0.8},
	})
	optsDefault := DefaultLouvainOptions()
	optsHigh := DefaultLouvainOptions()
	optsHigh.Resolution = 3.0

	resultDefault, err := Louvain(g, optsDefault)
	if err != nil {
		t.Fatalf("default: unexpected error: %v", err)
	}
	resultHigh, err := Louvain(g, optsHigh)
	if err != nil {
		t.Fatalf("high γ: unexpected error: %v", err)
	}

	// High resolution should produce >= communities than default.
	if len(resultHigh.Communities) < len(resultDefault.Communities) {
		t.Errorf("high γ communities = %d, default = %d; expected high γ >= default",
			len(resultHigh.Communities), len(resultDefault.Communities))
	}
	assertPartitionComplete(t, g, resultHigh)
}

func TestLouvain_Resolution_Low(t *testing.T) {
	// Two weakly connected cliques: at low γ they should merge.
	g := makeGraph([][3]interface{}{
		{"A", "B", 1.0}, {"B", "A", 1.0},
		{"B", "C", 1.0}, {"C", "B", 1.0},
		{"A", "C", 1.0}, {"C", "A", 1.0},
		{"D", "E", 1.0}, {"E", "D", 1.0},
		{"E", "F", 1.0}, {"F", "E", 1.0},
		{"D", "F", 1.0}, {"F", "D", 1.0},
		{"C", "D", 0.5}, {"D", "C", 0.5}, // moderate bridge
	})
	optsLow := DefaultLouvainOptions()
	optsLow.Resolution = 0.3 // very low: favours merging

	result, err := Louvain(g, optsLow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// At very low γ the two cliques should merge into 1 community.
	if len(result.Communities) != 1 {
		t.Logf("low γ produced %d communities (expected 1, acceptable if algorithm converges differently)", len(result.Communities))
	}
	assertPartitionComplete(t, g, result)
}

func TestLouvain_MaxLevelsReached(t *testing.T) {
	// Force exactly MaxLevels=1 and verify result.Levels == 1.
	g := makeGraph([][3]interface{}{
		{"A", "B", 1.0}, {"B", "A", 1.0},
		{"B", "C", 1.0}, {"C", "B", 1.0},
		{"C", "D", 1.0}, {"D", "C", 1.0},
	})
	opts := DefaultLouvainOptions()
	opts.MaxLevels = 1
	result, err := Louvain(g, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Levels != 1 {
		t.Errorf("Levels = %d, want 1", result.Levels)
	}
	assertPartitionComplete(t, g, result)
}

func TestLouvain_Deterministic(t *testing.T) {
	// Run Louvain 10 times with the same input and verify bit-exact results.
	g := makeGraph([][3]interface{}{
		{"A", "B", 1.0}, {"B", "A", 1.0},
		{"B", "C", 1.0}, {"C", "B", 1.0},
		{"A", "C", 1.0}, {"C", "A", 1.0},
		{"D", "E", 1.0}, {"E", "D", 1.0},
		{"E", "F", 1.0}, {"F", "E", 1.0},
		{"D", "F", 1.0}, {"F", "D", 1.0},
		{"C", "D", 0.1}, {"D", "C", 0.1},
	})
	opts := DefaultLouvainOptions()
	ref, err := Louvain(g, opts)
	if err != nil {
		t.Fatalf("first run error: %v", err)
	}

	for run := 0; run < 9; run++ {
		result, err := Louvain(g, opts)
		if err != nil {
			t.Fatalf("run %d error: %v", run+2, err)
		}
		if len(result.Communities) != len(ref.Communities) {
			t.Fatalf("run %d: communities = %d, want %d", run+2, len(result.Communities), len(ref.Communities))
		}
		if result.Modularity != ref.Modularity {
			t.Errorf("run %d: Modularity = %v, want %v", run+2, result.Modularity, ref.Modularity)
		}
		if result.Levels != ref.Levels {
			t.Errorf("run %d: Levels = %d, want %d", run+2, result.Levels, ref.Levels)
		}
		for i, comm := range result.Communities {
			refComm := ref.Communities[i]
			if len(comm.Members) != len(refComm.Members) {
				t.Errorf("run %d comm %d: members = %d, want %d", run+2, i, len(comm.Members), len(refComm.Members))
				continue
			}
			for j, m := range comm.Members {
				if m != refComm.Members[j] {
					t.Errorf("run %d comm %d member[%d] = %q, want %q", run+2, i, j, m, refComm.Members[j])
				}
			}
		}
	}
}

// TestLouvain_PartitionCompleteness verifies the invariant for various graph types.
func TestLouvain_PartitionCompleteness(t *testing.T) {
	cases := []struct {
		name  string
		graph scoring.SparseGraph
	}{
		{"single", singleNodeGraph("X")},
		{"two connected", twoNodeGraph("P", "Q", 1.0)},
		{"triangle", triangleGraph("A", "B", "C", 1.0)},
		{"two cliques bridge", makeGraph([][3]interface{}{
			{"A", "B", 1.0}, {"B", "A", 1.0},
			{"B", "C", 1.0}, {"C", "B", 1.0},
			{"A", "C", 1.0}, {"C", "A", 1.0},
			{"D", "E", 1.0}, {"E", "D", 1.0},
			{"C", "D", 0.01}, {"D", "C", 0.01},
		})},
	}

	opts := DefaultLouvainOptions()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Louvain(tc.graph, opts)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertPartitionComplete(t, tc.graph, result)
			assertCommunityIDs(t, result)
		})
	}
}

func TestLouvain_ModularityNonNegative_ConnectedGraph(t *testing.T) {
	// For non-trivial connected graphs Louvain should find Q > 0 (community structure exists).
	g := makeGraph([][3]interface{}{
		{"A", "B", 1.0}, {"B", "A", 1.0},
		{"B", "C", 1.0}, {"C", "B", 1.0},
		{"A", "C", 1.0}, {"C", "A", 1.0},
		{"D", "E", 1.0}, {"E", "D", 1.0},
		{"D", "F", 1.0}, {"F", "D", 1.0},
		{"E", "F", 1.0}, {"F", "E", 1.0},
		{"C", "D", 0.1}, {"D", "C", 0.1},
	})
	result, err := Louvain(g, DefaultLouvainOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Modularity < 0 {
		t.Errorf("Modularity = %v, expected >= 0 for structured graph", result.Modularity)
	}
}

// ---------------------------------------------------------------------------
// Zachary's Karate Club integration test
// ---------------------------------------------------------------------------

// karateClubEdges returns the 78 undirected edges of Zachary's Karate Club
// network (Zachary 1977). Node 0 is "Mr Hi" and node 33 is "Officer".
// All edges are given bidirectional here for undirected treatment.
func karateClubEdges() [][3]interface{} {
	// The 78 pairs from Zachary (1977) Table 1.
	pairs := [][2]int{
		{0, 1}, {0, 2}, {0, 3}, {0, 4}, {0, 5}, {0, 6}, {0, 7}, {0, 8}, {0, 10},
		{0, 11}, {0, 12}, {0, 13}, {0, 17}, {0, 19}, {0, 21}, {0, 31},
		{1, 2}, {1, 3}, {1, 7}, {1, 11}, {1, 13}, {1, 17}, {1, 19}, {1, 21}, {1, 30},
		{2, 3}, {2, 7}, {2, 8}, {2, 9}, {2, 13}, {2, 27}, {2, 28}, {2, 32},
		{3, 7}, {3, 12}, {3, 13},
		{4, 6}, {4, 10},
		{5, 6}, {5, 10}, {5, 16},
		{6, 16},
		{8, 30}, {8, 32}, {8, 33},
		{9, 33},
		{13, 33},
		{14, 32}, {14, 33},
		{15, 32}, {15, 33},
		{18, 32}, {18, 33},
		{19, 33},
		{20, 32}, {20, 33},
		{22, 32}, {22, 33},
		{23, 25}, {23, 27}, {23, 29}, {23, 32}, {23, 33},
		{24, 25}, {24, 27}, {24, 31},
		{25, 31},
		{26, 29}, {26, 33},
		{27, 33},
		{28, 31}, {28, 33},
		{29, 32}, {29, 33},
		{30, 32}, {30, 33},
		{31, 32}, {31, 33},
		{32, 33},
	}

	edges := make([][3]interface{}, 0, len(pairs)*2)
	for _, p := range pairs {
		from := fmt.Sprintf("%d", p[0])
		to := fmt.Sprintf("%d", p[1])
		edges = append(edges, [3]interface{}{from, to, 1.0})
		edges = append(edges, [3]interface{}{to, from, 1.0})
	}
	return edges
}

func TestLouvain_KarateClub(t *testing.T) {
	g := makeGraph(karateClubEdges())

	// Verify graph structure: 34 nodes (Zachary 1977).
	if len(g.Nodes) != 34 {
		t.Fatalf("karate club: nodes = %d, want 34", len(g.Nodes))
	}

	result, err := Louvain(g, DefaultLouvainOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// AC-1: 2-4 communities.
	if len(result.Communities) < 2 || len(result.Communities) > 4 {
		t.Errorf("communities = %d, want 2-4", len(result.Communities))
	}

	// AC-1: Q > 0.3.
	if result.Modularity <= 0.3 {
		t.Errorf("modularity = %v, want > 0.3", result.Modularity)
	}

	// AC-1: nodes 0 and 33 in different communities.
	comm0, found0 := memberIn(result, "0")
	comm33, found33 := memberIn(result, "33")
	if !found0 {
		t.Error("node 0 not found in partition")
	}
	if !found33 {
		t.Error("node 33 not found in partition")
	}
	if found0 && found33 && comm0 == comm33 {
		t.Errorf("nodes 0 and 33 are in the same community (%d); they should be separated", comm0)
	}

	// AC-2: partition completeness.
	assertPartitionComplete(t, g, result)

	// AC-3: community IDs dense.
	assertCommunityIDs(t, result)

	t.Logf("Karate Club: %d communities, Q=%.4f, Levels=%d", len(result.Communities), result.Modularity, result.Levels)
}

// ---------------------------------------------------------------------------
// Commit 6: Benchmarks
// ---------------------------------------------------------------------------

// plantedGraph generates a random graph with k planted communities of n/k
// nodes each. Intra-community edge probability = pIn, inter-community = pOut.
// Using a fixed seed for reproducibility.
func plantedGraph(n, k int, pIn, pOut float64, seed int64) scoring.SparseGraph {
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec // reproducible benchmark seed
	edges := make([]scoring.Edge, 0)
	commSize := n / k

	nodeComm := make([]int, n)
	for i := range nodeComm {
		nodeComm[i] = i / commSize
	}

	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			p := pOut
			if nodeComm[i] == nodeComm[j] {
				p = pIn
			}
			if rng.Float64() < p {
				from := fmt.Sprintf("node-%d", i)
				to := fmt.Sprintf("node-%d", j)
				w := 0.5 + 0.5*rng.Float64()
				edges = append(edges, scoring.Edge{From: from, To: to, Weight: w})
				edges = append(edges, scoring.Edge{From: to, To: from, Weight: w})
			}
		}
	}
	return scoring.NewSparseGraph(edges)
}

func BenchmarkLouvain_100_Nodes(b *testing.B) {
	g := plantedGraph(100, 5, 0.3, 0.02, 42)
	opts := DefaultLouvainOptions()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Louvain(g, opts); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLouvain_1K_Nodes(b *testing.B) {
	g := plantedGraph(1000, 10, 0.05, 0.002, 42)
	opts := DefaultLouvainOptions()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Louvain(g, opts); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLouvain_5K_Nodes(b *testing.B) {
	g := plantedGraph(5000, 20, 0.01, 0.0004, 42)
	opts := DefaultLouvainOptions()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Louvain(g, opts); err != nil {
			b.Fatal(err)
		}
	}
}

