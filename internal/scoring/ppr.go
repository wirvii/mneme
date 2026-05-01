// Package scoring provides pure scoring and ranking functions for mneme's
// retrieval pipeline. All functions in this package are stateless: they accept
// inputs, compute, and return results without side effects on any database or
// global state.
package scoring

import (
	"errors"
	"math"
	"sort"
)

// NodeID is the identifier for a node in the sparse graph. In mneme this is the
// entity UUID from the entities table, but the type alias keeps scoring/ free of
// any dependency on the store or model layers.
type NodeID = string

// Edge represents a single weighted directed edge in a sparse graph.
// Edges with non-positive Weight are ignored by NewSparseGraph.
type Edge struct {
	// From is the source node.
	From NodeID
	// To is the destination node.
	To NodeID
	// Weight is the edge strength in (0, +∞). Typical values in mneme are in
	// [0.1, 1.0] as produced by Hebbian strengthening (SPEC-006).
	Weight float64
}

// SparseGraph represents a weighted directed graph using adjacency lists.
// The graph is immutable after construction via NewSparseGraph. Callers must
// not modify Adj or OutStrength after passing the value to PPR.
//
// Design: map-based adjacency (not CSR) is used because mneme graphs contain
// fewer than 50K nodes per project. At that scale the simplicity of map
// iteration outweighs the cache-locality advantage of CSR (D1).
type SparseGraph struct {
	// Adj holds the adjacency list. Adj[u][v] is the weight of the directed
	// edge u → v. If Adj[u] is empty, u is a dangling node.
	Adj map[NodeID]map[NodeID]float64

	// OutStrength[u] = Σ Adj[u][v] for all v. Precomputed once at construction
	// time so the inner iteration loop does not recompute it on every step.
	// OutStrength[u] == 0 means u is a dangling node (no outgoing edges).
	OutStrength map[NodeID]float64

	// Nodes is the sorted slice of all node IDs present in the graph.
	// Precomputed once and sorted lexicographically so that every iteration
	// of power-iteration visits nodes in the same order, guaranteeing
	// bit-exact determinism across runs (D7).
	Nodes []NodeID
}

// NewSparseGraph constructs a SparseGraph from a list of directed weighted edges.
//
// Behaviour:
//   - Edges with Weight ≤ 0 are silently skipped.
//   - Nodes that appear only as edge targets (no outgoing edges) are included
//     with OutStrength 0, marking them as dangling nodes.
//   - The Nodes slice is sorted lexicographically for deterministic iteration (D7).
//   - If two edges share the same From/To pair, the last weight wins (map overwrite).
//
// This is a pure function: it never touches the store or any global state.
func NewSparseGraph(edges []Edge) SparseGraph {
	adj := make(map[NodeID]map[NodeID]float64)
	outStrength := make(map[NodeID]float64)
	nodeSet := make(map[NodeID]struct{})

	for _, e := range edges {
		if e.Weight <= 0 {
			continue
		}
		if adj[e.From] == nil {
			adj[e.From] = make(map[NodeID]float64)
		}
		// If the same edge appears twice the later weight overwrites the earlier
		// one. Callers should deduplicate before calling NewSparseGraph when the
		// last-write-wins semantic is undesirable.
		prev := adj[e.From][e.To]
		adj[e.From][e.To] = e.Weight
		outStrength[e.From] += e.Weight - prev // adjust for overwrite
		nodeSet[e.From] = struct{}{}
		nodeSet[e.To] = struct{}{}
	}

	// Guarantee every node has entries in both maps so callers never get zero
	// values from missing keys.
	for node := range nodeSet {
		if adj[node] == nil {
			adj[node] = make(map[NodeID]float64)
		}
		// outStrength for target-only nodes is already the zero value.
	}

	nodes := make([]NodeID, 0, len(nodeSet))
	for node := range nodeSet {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)

	return SparseGraph{
		Adj:         adj,
		OutStrength: outStrength,
		Nodes:       nodes,
	}
}

// PPROptions configures a Personalized PageRank computation.
//
// Use DefaultPPROptions() to obtain a pre-filled struct with the recommended
// values; override individual fields as needed.
type PPROptions struct {
	// Alpha is the teleport (restart) probability. At each step the random
	// surfer teleports back to a seed node with probability Alpha, or follows
	// an outgoing edge with probability 1-Alpha.
	//
	// Must be strictly inside (0.0, 1.0). The standard PageRank value is 0.85,
	// which empirically balances topological exploration with seed affinity
	// (Brin & Page 1998). Higher values (0.90–0.95) strengthen seed bias.
	Alpha float64

	// MaxIter is the maximum number of power-iteration steps. The algorithm
	// stops when either MaxIter is reached or L1 convergence (see Epsilon).
	// Must be > 0. Default: 100.
	MaxIter int

	// Epsilon is the L1 convergence threshold. When Σ|p(t+1)[i]-p(t)[i]| < Epsilon
	// the distribution is considered stationary and iteration stops early.
	// Must be > 0. Default: 1e-6.
	Epsilon float64
}

// DefaultPPROptions returns the recommended configuration for PPR over mneme's
// memory graphs. Alpha=0.85 is the canonical value from Brin & Page (1998).
// MaxIter=100 and Epsilon=1e-6 ensure convergence on graphs up to 50K nodes.
func DefaultPPROptions() PPROptions {
	return PPROptions{
		Alpha:   0.85,
		MaxIter: 100,
		Epsilon: 1e-6,
	}
}

// PPRResult holds the output of a Personalized PageRank run.
type PPRResult struct {
	// Scores maps each node to its PPR score. Higher means closer (in the
	// random-walk sense) to the seed set. The sum of all scores is
	// approximately 1.0 (conservation of probability mass).
	Scores map[NodeID]float64

	// Iterations is the number of power-iteration steps actually performed.
	// Less than PPROptions.MaxIter when convergence was reached early.
	Iterations int

	// Converged is true when the L1 delta dropped below PPROptions.Epsilon
	// before MaxIter was exhausted.
	Converged bool
}

// PPR computes Personalized PageRank over a sparse weighted directed graph.
//
// The algorithm is power iteration (Brin & Page 1998) with topic-sensitive
// teleportation (Haveliwala 2002). In each step the random surfer either:
//   - teleports to a uniformly-chosen seed node with probability opts.Alpha, or
//   - follows a weighted outgoing edge with probability 1-opts.Alpha.
//
// The iterative update rule is:
//
//	p(t+1) = α·s + (1−α)·Mᵀ·p(t) + (1−α)·danglingMass·s
//
// where s is the teleport vector (uniform over seeds), M is the column-
// stochastic transition matrix derived from the weighted adjacency, and
// danglingMass is the total probability mass held by dangling nodes
// (those with no outgoing edges). Redistributing dangling mass to s prevents
// probability leakage (D3).
//
// Convergence is measured by the L1 norm of the update delta (D5). Iteration
// order is deterministic via pre-sorted node keys (D7). The algorithm is
// serial (no goroutines) because for graphs ≤50K nodes it completes in
// under 2ms, making parallelism overhead counter-productive (D8).
//
// Error contract (D11):
//   - len(seeds) == 0              → "ppr: at least one seed required"
//   - alpha ≤ 0 or alpha ≥ 1      → "ppr: alpha must be in (0.0, 1.0)"
//   - max_iter ≤ 0                 → "ppr: max_iter must be > 0"
//   - epsilon ≤ 0                  → "ppr: epsilon must be > 0"
//   - all seeds absent from graph  → "ppr: no seeds found in graph"
//   - seeds present but missing from graph → silently skipped (partial seeds OK)
func PPR(graph SparseGraph, seeds []NodeID, opts PPROptions) (*PPRResult, error) {
	// --- Input validation (D11) ---
	if len(seeds) == 0 {
		return nil, errors.New("ppr: at least one seed required")
	}
	if opts.Alpha <= 0 || opts.Alpha >= 1 {
		return nil, errors.New("ppr: alpha must be in (0.0, 1.0)")
	}
	if opts.MaxIter <= 0 {
		return nil, errors.New("ppr: max_iter must be > 0")
	}
	if opts.Epsilon <= 0 {
		return nil, errors.New("ppr: epsilon must be > 0")
	}

	// --- Build teleport vector s (uniform over seeds present in graph) ---
	//
	// Seeds not present in graph.Nodes are silently skipped. If none of the
	// provided seeds exist in the graph, return an error rather than running
	// a degenerate computation.
	nodeIndex := make(map[NodeID]struct{}, len(graph.Nodes))
	for _, n := range graph.Nodes {
		nodeIndex[n] = struct{}{}
	}

	seedSet := make(map[NodeID]struct{})
	for _, s := range seeds {
		if _, ok := nodeIndex[s]; ok {
			seedSet[s] = struct{}{}
		}
	}
	if len(seedSet) == 0 {
		return nil, errors.New("ppr: no seeds found in graph")
	}

	teleportWeight := 1.0 / float64(len(seedSet))
	teleport := make(map[NodeID]float64, len(seedSet))
	for s := range seedSet {
		teleport[s] = teleportWeight
	}

	// --- Identify dangling nodes (out-strength == 0) ---
	//
	// A dangling node absorbs probability mass but has nowhere to send it via
	// transition. Standard practice (Brin & Page 1998) redirects that mass to
	// the teleport vector so the stochastic matrix remains column-stochastic.
	isDangling := make([]bool, len(graph.Nodes))
	for i, node := range graph.Nodes {
		if graph.OutStrength[node] == 0 {
			isDangling[i] = true
		}
	}

	// --- Initialise p to the teleport vector ---
	//
	// Starting from s is the standard initialisation; it places all mass on the
	// seeds and lets the iteration spread it outward.
	p := make(map[NodeID]float64, len(graph.Nodes))
	for s, w := range teleport {
		p[s] = w
	}

	alpha := opts.Alpha
	oneMinusAlpha := 1.0 - alpha

	// --- Power iteration (D2) ---
	var iterations int
	var converged bool

	for iter := 0; iter < opts.MaxIter; iter++ {
		iterations = iter + 1

		// Compute dangling mass: sum of p[d] for all dangling nodes (D3).
		danglingMass := 0.0
		for i, node := range graph.Nodes {
			if isDangling[i] {
				danglingMass += p[node]
			}
		}

		// Accumulate transition mass into pNext.
		// For each non-dangling source node u with p[u] > 0, distribute its
		// probability mass to neighbours proportional to edge weight.
		//
		// pNext[v] += (1-α) * p[u] * w(u,v) / outStrength[u]
		//
		// Nodes are visited in sorted order (D7) so floating-point operations
		// happen in a deterministic sequence.
		pNext := make(map[NodeID]float64, len(graph.Nodes))

		for _, u := range graph.Nodes {
			pu := p[u]
			if pu == 0 || graph.OutStrength[u] == 0 {
				// Zero-probability or dangling nodes contribute nothing here;
				// their mass (if any) is redistributed via danglingMass below.
				continue
			}
			factor := oneMinusAlpha * pu / graph.OutStrength[u]
			for v, w := range graph.Adj[u] {
				pNext[v] += factor * w
			}
		}

		// Teleport + dangling redistribution (D3).
		//
		// Each seed node s receives:
		//   α * teleport[s]                    ← direct restart
		//   (1-α) * danglingMass * teleport[s] ← dangling nodes' orphaned mass
		//
		// Combined: (α + (1-α)*danglingMass) * teleport[s]
		teleportFactor := alpha + oneMinusAlpha*danglingMass
		for s, tw := range teleport {
			pNext[s] += teleportFactor * tw
		}

		// L1 convergence check (D5).
		// Compute Σ|pNext[i] - p[i]| over all nodes.
		l1 := 0.0
		for _, node := range graph.Nodes {
			l1 += math.Abs(pNext[node] - p[node])
		}

		p = pNext

		if l1 < opts.Epsilon {
			converged = true
			break
		}
	}

	return &PPRResult{
		Scores:     p,
		Iterations: iterations,
		Converged:  converged,
	}, nil
}
