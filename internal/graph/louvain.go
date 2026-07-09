// Package graph implements graph-analysis algorithms for mneme's knowledge graph.
// This file implements the Louvain community detection algorithm
// (Blondel et al. 2008, "Fast unfolding of communities in large networks").
//
// Louvain is a greedy modularity optimisation algorithm that operates in two
// phases repeated iteratively:
//
//   - Phase 1 (local moves): for each node, evaluate moving it to each
//     neighbour's community and accept the move that maximises modularity
//     change ΔQ. Repeat until no move improves Q.
//   - Phase 2 (graph contraction): collapse each community into a single
//     super-node. Internal edges become self-loops; inter-community edges are
//     merged with summed weights. The contracted graph is fed back to Phase 1.
//
// The algorithm terminates when Phase 1 produces zero moves, the modularity
// gain drops below MinModularity, or MaxLevels is reached.
package graph

import (
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/wirvii/mneme/internal/scoring"
)

// LouvainOptions configures the Louvain community detection algorithm.
//
// Use DefaultLouvainOptions to obtain a pre-filled struct with the recommended
// values and override individual fields as needed.
type LouvainOptions struct {
	// Resolution is the γ parameter in the modularity formula. Higher values
	// produce more and smaller communities; lower values produce fewer and larger
	// ones. Must be > 0. Default: 1.0.
	//
	// Effect:
	//   γ = 1.0 → standard modularity (Blondel et al. 2008 default)
	//   γ > 1.0 → more communities, each smaller
	//   γ < 1.0 → fewer communities, each larger
	Resolution float64

	// MinModularity is the minimum modularity gain required between consecutive
	// Phase-1 passes for the outer loop to continue. When the accumulated ΔQ
	// across a full Phase-1 pass drops below this threshold the algorithm stops.
	// Must be >= 0. Default: 1e-7.
	MinModularity float64

	// MaxLevels is the maximum number of Phase-1 + Phase-2 iterations.
	// Must be > 0. Default: 10.
	MaxLevels int
}

// DefaultLouvainOptions returns the recommended configuration for Louvain over
// mneme's memory graphs. γ=1.0 is the standard modularity resolution
// (Blondel et al. 2008). MaxLevels=10 and MinModularity=1e-7 match reference
// implementations (NetworkX, igraph) and are well above float64 epsilon.
func DefaultLouvainOptions() LouvainOptions {
	return LouvainOptions{
		Resolution:    1.0,
		MinModularity: 1e-7,
		MaxLevels:     10,
	}
}

// Community represents a single detected community in the final partition.
// Community IDs are 0-indexed and dense (0, 1, …, k-1).
type Community struct {
	// ID is the community identifier, 0-indexed and dense.
	ID int
	// Members contains the original NodeIDs belonging to this community,
	// sorted lexicographically for deterministic output.
	Members []scoring.NodeID
}

// LouvainResult holds the output of a Louvain community detection run.
//
// Every node in the input graph appears in exactly one community
// (partition completeness invariant).
type LouvainResult struct {
	// Communities is the final partition, sorted by ID (0-indexed, dense).
	Communities []Community

	// Modularity is the final Q value of the partition. Higher is better;
	// values near 0 indicate weak community structure or a trivial partition.
	Modularity float64

	// Levels is the number of Phase-1 + Phase-2 iterations performed.
	Levels int
}

// louvainState holds the mutable bookkeeping for a single Phase-1 run on one
// graph. All slices are indexed by node position in graph.Nodes (D5).
type louvainState struct {
	n           int                   // number of nodes
	node2comm   []int                 // node2comm[i] = community ID of node i; -1 = sentinel (removed)
	commDegree  []float64             // commDegree[c] = Σ_tot(c): sum of weighted degrees of nodes in c
	commIntern  []float64             // commIntern[c] = Σ_in(c): sum of internal edge weights in c
	nodeDegree  []float64             // nodeDegree[i] = k_i: weighted degree of node i (precomputed)
	totalWeight float64               // m: sum(all edge weights in Adj)/2 (undirected treatment, D9)
	nodeIndex   map[scoring.NodeID]int // NodeID -> index in graph.Nodes
}

// newLouvainState initialises a louvainState for the given graph.
// Each node starts in its own singleton community (community ID == node index).
// nodeDegree is precomputed from the adjacency list. totalWeight = sum/2 (D9).
// Self-loops contribute to nodeDegree and commIntern but not to totalWeight
// inter-community edges (D10).
func newLouvainState(graph scoring.SparseGraph) *louvainState {
	n := len(graph.Nodes)
	s := &louvainState{
		n:          n,
		node2comm:  make([]int, n),
		commDegree: make([]float64, n),
		commIntern: make([]float64, n),
		nodeDegree: make([]float64, n),
		nodeIndex:  make(map[scoring.NodeID]int, n),
	}

	// Build nodeIndex first so we can compute degrees.
	for i, nodeID := range graph.Nodes {
		s.nodeIndex[nodeID] = i
	}

	// Precompute nodeDegree[i] = sum of all edge weights incident to node i.
	// For undirected treatment we sum over graph.Adj[nodeID] (which for mneme
	// graphs always has symmetric edges: BuildGraphForSeeds emits both A→B and
	// B→A with identical weight, D9).
	// Self-loops (nodeID == neighbor) are included in nodeDegree (D10).
	for i, nodeID := range graph.Nodes {
		for _, w := range graph.Adj[nodeID] {
			s.nodeDegree[i] += w
		}
		// totalWeight accumulates all directed edges; divides by 2 at end (D9).
		s.totalWeight += s.nodeDegree[i]
	}
	s.totalWeight /= 2.0

	// Initialise: each node i in its own community i (singleton partition).
	// commDegree[i] = nodeDegree[i]; commIntern[i] = weight of self-loop if any.
	for i, nodeID := range graph.Nodes {
		s.node2comm[i] = i
		s.commDegree[i] = s.nodeDegree[i]
		// Internal weight of a singleton = only self-loops, if present.
		if w, ok := graph.Adj[nodeID][nodeID]; ok {
			s.commIntern[i] = w
		}
	}

	return s
}

// computeModularity calculates the full modularity Q of the current partition.
//
// Uses the per-community formulation (Blondel et al. 2008, Eq. 1):
//
//	Q = Σ_c [ Σ_in(c)/(2m) − γ·(Σ_tot(c)/(2m))² ]
//
// This is equivalent to the standard pairwise formulation but O(communities)
// instead of O(nodes²). Used only for reporting — incremental ΔQ drives moves.
func computeModularity(state *louvainState, resolution float64) float64 {
	m := state.totalWeight
	if m == 0 {
		return 0
	}
	q := 0.0
	twoM := 2.0 * m
	for c := 0; c < state.n; c++ {
		sigmaIn := state.commIntern[c]
		sigmaTot := state.commDegree[c]
		if sigmaTot == 0 && sigmaIn == 0 {
			continue
		}
		q += sigmaIn/twoM - resolution*(sigmaTot/twoM)*(sigmaTot/twoM)
	}
	return q
}

// phase1 performs local node moves to maximise modularity (Phase 1 of Louvain).
//
// For each node i (in ascending index order, D4) it evaluates all neighbour
// communities and moves i to the community that maximises ΔQ (D2). Ties are
// broken by smallest community ID (D3). The inner loop repeats until a full
// pass over all nodes produces no moves.
//
// Returns the total ΔQ accumulated across all accepted moves and the number of
// moves made.
func phase1(graph scoring.SparseGraph, state *louvainState, opts LouvainOptions) (totalDeltaQ float64, moved int) {
	improved := true
	m := state.totalWeight
	if m == 0 {
		return 0, 0
	}
	twoM2 := 2.0 * m * m

	for improved {
		improved = false

		// D4: iterate nodes in ascending index order (graph.Nodes is pre-sorted).
		for i := 0; i < state.n; i++ {
			nodeID := graph.Nodes[i]
			currentComm := state.node2comm[i]
			ki := state.nodeDegree[i]

			// Compute k_i_in(currentComm): weight from i to its current community.
			kiInCurrent := 0.0
			for neighbor, w := range graph.Adj[nodeID] {
				j := state.nodeIndex[neighbor]
				if state.node2comm[j] == currentComm {
					kiInCurrent += w
				}
			}

			// Temporarily remove node i from its community (sentinel = -1).
			// Update commDegree and commIntern to reflect the removal.
			state.commDegree[currentComm] -= ki
			state.commIntern[currentComm] -= kiInCurrent
			state.node2comm[i] = -1

			// Collect unique neighbour communities and the weight from i into each.
			// map[communityID] -> k_i_in(community)
			neighborComms := make(map[int]float64)
			for neighbor, w := range graph.Adj[nodeID] {
				j := state.nodeIndex[neighbor]
				c := state.node2comm[j]
				if c == -1 {
					// Skip i itself (sentinel) — self-loop already in kiInCurrent.
					continue
				}
				neighborComms[c] += w
			}
			// Always evaluate staying in the original community (even if no
			// neighbours remain in it). This ensures the node rejoins if no
			// better option exists.
			if _, ok := neighborComms[currentComm]; !ok {
				neighborComms[currentComm] = 0
			}

			// Find the community c* that maximises ΔQ.
			// ΔQ(i → c) = k_i_in(c)/m − γ·Σ_tot(c)·k_i / (2m²)
			// (This is the gain term; the loss term of removing i from currentComm
			// is already baked in via commDegree/commIntern adjustment above.)
			bestComm := currentComm
			bestDeltaQ := -math.MaxFloat64

			for c, kiInC := range neighborComms {
				deltaQ := kiInC/m - opts.Resolution*(state.commDegree[c]*ki)/twoM2
				// D3: tie-break by smallest community ID.
				if deltaQ > bestDeltaQ || (deltaQ == bestDeltaQ && c < bestComm) {
					bestDeltaQ = deltaQ
					bestComm = c
				}
			}

			// Re-place node i into bestComm.
			state.node2comm[i] = bestComm

			// Recompute k_i_in(bestComm) for the new placement.
			kiInBest := 0.0
			for neighbor, w := range graph.Adj[nodeID] {
				j := state.nodeIndex[neighbor]
				if state.node2comm[j] == bestComm {
					kiInBest += w
				}
			}

			state.commDegree[bestComm] += ki
			state.commIntern[bestComm] += kiInBest

			if bestComm != currentComm {
				moved++
				totalDeltaQ += bestDeltaQ
				improved = true
			}
		}
	}

	return totalDeltaQ, moved
}

// contractGraph builds a super-graph where each community becomes a super-node
// (Phase 2 of Louvain).
//
// Internal community edges become self-loops on the super-node. Inter-community
// edges are merged with weights summed. The returned graph uses node IDs of the
// form "comm-<N>" (internal, never escape the algorithm, D6).
//
// The second return value maps each new dense community ID to the slice of
// original NodeIDs it contains at this level (before contraction). This allows
// the outer loop to propagate membership through multiple levels.
func contractGraph(
	graph scoring.SparseGraph,
	state *louvainState,
) (scoring.SparseGraph, map[int][]scoring.NodeID) {
	// Step 1: gather all active community IDs (non-empty communities).
	commMembersOld := make(map[int][]scoring.NodeID)
	for i, nodeID := range graph.Nodes {
		c := state.node2comm[i]
		commMembersOld[c] = append(commMembersOld[c], nodeID)
	}

	// Step 2: remap community IDs to dense 0..k-1 for determinism.
	oldIDs := make([]int, 0, len(commMembersOld))
	for id := range commMembersOld {
		oldIDs = append(oldIDs, id)
	}
	sort.Ints(oldIDs)

	remap := make(map[int]int, len(oldIDs))
	for newID, oldID := range oldIDs {
		remap[oldID] = newID
	}

	// Step 3: accumulate super-edges.
	// superEdges[[ci, cj]] = sum of weights of edges between community ci and cj.
	// When ci == cj the edge is a self-loop (internal community edge, D6).
	type edgeKey [2]int
	superEdges := make(map[edgeKey]float64)
	for i, nodeID := range graph.Nodes {
		ci := remap[state.node2comm[i]]
		for neighbor, w := range graph.Adj[nodeID] {
			j := state.nodeIndex[neighbor]
			cj := remap[state.node2comm[j]]
			superEdges[edgeKey{ci, cj}] += w
		}
	}

	// Step 4: build Edge slice for NewSparseGraph.
	edges := make([]scoring.Edge, 0, len(superEdges))
	for key, w := range superEdges {
		edges = append(edges, scoring.Edge{
			From:   fmt.Sprintf("comm-%d", key[0]),
			To:     fmt.Sprintf("comm-%d", key[1]),
			Weight: w,
		})
	}

	// Step 5: build remapped community membership with new dense IDs.
	commMembers := make(map[int][]scoring.NodeID, len(oldIDs))
	for oldID, members := range commMembersOld {
		commMembers[remap[oldID]] = members
	}

	return scoring.NewSparseGraph(edges), commMembers
}

// Louvain runs the Louvain community detection algorithm on the given graph.
//
// The algorithm is described in:
//   - Blondel, V.D., Guillaume, J.L., Lambiotte, R., & Lefebvre, E. (2008).
//     Fast unfolding of communities in large networks. Journal of Statistical
//     Mechanics: Theory and Experiment, P10008.
//
// The implementation is pure (no DB access, no global state) and deterministic
// for the same input (node visit order = ascending index, D4; tie-break =
// smallest community ID, D3).
//
// Graph treatment: SparseGraph is treated as undirected. totalWeight = sum(all
// Adj weights)/2 to avoid double-counting (D9). BuildGraphForSeeds already
// emits symmetric bidirectional edges, so no conversion is needed.
//
// Self-loops in the input graph are handled correctly: they contribute to
// nodeDegree and commIntern but not to inter-community ΔQ (D10).
//
// Error contract:
//   - empty graph (0 nodes)      → "louvain: empty graph"
//   - Resolution <= 0            → "louvain: resolution must be > 0"
//   - MaxLevels <= 0             → "louvain: max_levels must be > 0"
//   - MinModularity < 0          → "louvain: min_modularity must be >= 0"
//
// Special cases:
//   - 1 node, 0 edges            → 1 community, Q=0, Levels=1
//   - disconnected components    → each component forms 1+ communities independently
func Louvain(graph scoring.SparseGraph, opts LouvainOptions) (*LouvainResult, error) {
	if len(graph.Nodes) == 0 {
		return nil, errors.New("louvain: empty graph")
	}
	if opts.Resolution <= 0 {
		return nil, errors.New("louvain: resolution must be > 0")
	}
	if opts.MaxLevels <= 0 {
		return nil, errors.New("louvain: max_levels must be > 0")
	}
	if opts.MinModularity < 0 {
		return nil, errors.New("louvain: min_modularity must be >= 0")
	}

	// currentGraph is the graph being processed at the current level.
	// At level 0 it equals the input graph; at level k it is the contracted
	// graph from level k-1.
	currentGraph := graph

	// levelMemberships[commID] = slice of original NodeIDs in that community.
	// Initialised with singleton communities (one node per community).
	levelMemberships := make(map[int][]scoring.NodeID, len(graph.Nodes))
	for i, nodeID := range graph.Nodes {
		levelMemberships[i] = []scoring.NodeID{nodeID}
	}

	var levels int
	var finalModularity float64
	prevNodeCount := len(currentGraph.Nodes)

	for level := 0; level < opts.MaxLevels; level++ {
		levels = level + 1
		state := newLouvainState(currentGraph)

		totalDeltaQ, moved := phase1(currentGraph, state, opts)
		finalModularity = computeModularity(state, opts.Resolution)

		// D7 early stop condition 1: no moves → converged.
		if moved == 0 {
			break
		}
		// D7 early stop condition 2: total gain too small.
		if totalDeltaQ < opts.MinModularity {
			break
		}

		// Phase 2: contract the graph.
		contractedGraph, commMembers := contractGraph(currentGraph, state)

		// Propagate memberships: each contracted super-node's community maps to
		// the union of original NodeIDs from all sub-communities it absorbed.
		newMemberships := make(map[int][]scoring.NodeID, len(commMembers))
		for newCommID, oldNodeIDs := range commMembers {
			// oldNodeIDs are the NodeIDs from currentGraph that belong to newCommID.
			// For level 0, these are original NodeIDs; for level k they are
			// "comm-X" IDs from the previous contraction. We look up their
			// original memberships via levelMemberships.
			for _, oldID := range oldNodeIDs {
				// Find the index of oldID in currentGraph.Nodes to look up its
				// community assignment.
				oldIdx := state.nodeIndex[oldID]
				// The community at this level for oldID is state.node2comm[oldIdx].
				// But commMembers already maps new community IDs to the set of
				// currentGraph node IDs (which are keys into levelMemberships).
				// levelMemberships uses index-based keys at level 0, then
				// "comm-N" string conversions afterwards.
				// Simplest correct approach: use the current levelMemberships key
				// which is the old community index for level-0 nodes.
				_ = oldIdx
				// levelMemberships is keyed by the community index at the previous
				// level. At level 0 the keys are 0..n-1 (node indices). After
				// contraction the contracted graph nodes are "comm-0", "comm-1", ...
				// so we need a string→int mapping. Instead we track memberships by
				// storing the original NodeID slice directly.
				// The commMembers map already gives us what we need: each newCommID
				// maps to the NodeIDs of currentGraph that joined it. Those NodeIDs
				// are either original graph NodeIDs (level 0) or "comm-X" strings
				// from the previous contraction. In either case levelMemberships
				// was keyed by the integer community ID of the previous level.
				// We need a reverse lookup from contracted node name to prev community ID.
				// For level 0: NodeID == original node, levelMemberships key == nodeIndex.
				// For level k>0: NodeID == "comm-N" where N is the community ID from
				// the previous contraction (which equals the key in levelMemberships).
				// We handle both by trying integer parse on "comm-N" strings.
				_ = oldID
			}
			// Use the helper below.
			newMemberships[newCommID] = flattenMemberships(commMembers[newCommID], levelMemberships, state)
		}

		levelMemberships = newMemberships
		currentGraph = contractedGraph

		// D7 early stop condition 3 (implicit via MaxLevels).
		// Also stop if contraction produced no reduction.
		if len(currentGraph.Nodes) >= prevNodeCount {
			break
		}
		prevNodeCount = len(currentGraph.Nodes)
	}

	// Build sorted Community slice from final levelMemberships.
	commIDs := make([]int, 0, len(levelMemberships))
	for id := range levelMemberships {
		commIDs = append(commIDs, id)
	}
	sort.Ints(commIDs)

	communities := make([]Community, 0, len(commIDs))
	for newID, oldID := range commIDs {
		members := levelMemberships[oldID]
		sort.Strings(members)
		communities = append(communities, Community{
			ID:      newID,
			Members: members,
		})
	}

	return &LouvainResult{
		Communities: communities,
		Modularity:  finalModularity,
		Levels:      levels,
	}, nil
}

// flattenMemberships resolves the original NodeIDs for a set of contracted-graph
// node IDs. At level 0, memberIDs are original NodeIDs and levelMemberships keys
// are node indices. At level k>0, memberIDs are "comm-N" strings where N is the
// community ID from the previous level (and thus a key in levelMemberships).
func flattenMemberships(memberIDs []scoring.NodeID, levelMemberships map[int][]scoring.NodeID, state *louvainState) []scoring.NodeID {
	var result []scoring.NodeID
	for _, id := range memberIDs {
		// Try to interpret id as a "comm-N" contracted super-node.
		var commIdx int
		if n, err := fmt.Sscanf(id, "comm-%d", &commIdx); n == 1 && err == nil {
			// This is a contracted super-node; look up its original members.
			if members, ok := levelMemberships[commIdx]; ok {
				result = append(result, members...)
				continue
			}
		}
		// Fallback: id is an original NodeID. Look up by nodeIndex.
		if idx, ok := state.nodeIndex[id]; ok {
			if members, ok2 := levelMemberships[idx]; ok2 {
				result = append(result, members...)
				continue
			}
		}
		// Last resort: add as-is (should not happen for valid input).
		result = append(result, id)
	}
	return result
}
