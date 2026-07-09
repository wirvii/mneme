// Package service graph_builder.go — BFS adapter that constructs a SparseGraph
// from the SQLite knowledge graph for use by the Personalized PageRank algorithm
// (SPEC-016). Kept in a separate file following the pattern of explore.go and
// rebuild.go, where each graph concern lives in its own file.
package service

import (
	"context"
	"log/slog"

	"github.com/wirvii/mneme/internal/scoring"
)

// GraphBuildOptions configures the BFS traversal used by BuildGraphForSeeds
// to convert the SQLite entity-relation graph into a scoring.SparseGraph.
//
// Use DefaultGraphBuildOptions to obtain a pre-filled struct with the recommended
// values calibrated to mneme's scale (SPEC-016 D8). Override individual fields
// only when profiling shows a specific need.
type GraphBuildOptions struct {
	// MaxDepth is the maximum number of BFS hops from the seed entities.
	// At hop depth d, PPR mass is (1-alpha)^d. For alpha=0.85, d=3 gives
	// ~0.34% of total mass — negligible. Deeper traversal adds latency with
	// diminishing returns. Default: 3.
	MaxDepth int

	// MaxNodes is the hard cap on the number of entity nodes collected during
	// BFS. When len(visited) reaches MaxNodes the traversal stops and the
	// partial subgraph is returned. This prevents dense graphs from exploding
	// query latency. Default: 5000.
	MaxNodes int

	// WeightThreshold is the minimum relation weight required for an edge to
	// be followed. Relations below this threshold are not added to the graph
	// and their neighbor entities are not enqueued. Consistent with
	// ExpansionThreshold from SPEC-007 config. Default: 0.3.
	WeightThreshold float64

	// FanOutCap is the maximum number of relations followed per entity per BFS
	// level. Passed as the limit argument to GetStrongRelations. Prevents hub
	// nodes from generating an unbounded number of edges. Default: 50.
	FanOutCap int
}

// DefaultGraphBuildOptions returns GraphBuildOptions with values calibrated to
// mneme's memory graph scale. All defaults are consistent with the parameters
// established in SPEC-007 (graph expansion) and SPEC-015 (PPR):
//
//   - MaxDepth=3:        PPR mass at d=3 is negligible; budget stays <50ms.
//   - MaxNodes=5000:     PPR benchmarks confirm <20ms at this node count.
//   - WeightThreshold=0.3: requires 4+ Hebbian co-accesses before a relation
//     is strong enough to follow (matches ExpansionThreshold).
//   - FanOutCap=50:      prevents hub-node explosion; matches ExpansionFanOutCap.
func DefaultGraphBuildOptions() GraphBuildOptions {
	return GraphBuildOptions{
		MaxDepth:        3,
		MaxNodes:        5000,
		WeightThreshold: 0.3,
		FanOutCap:       50,
	}
}

// BuildGraphForSeeds constructs a scoring.SparseGraph by performing a depth-
// limited BFS traversal of the entity-level knowledge graph starting from the
// entities linked to seedMemoryIDs.
//
// The returned graph can be passed directly to scoring.PPR without further
// transformation. The second return value is the slice of relation IDs that
// were traversed; the caller should forward them to BatchTouchRelations to
// update last_traversed_at and prevent premature edge decay (SPEC-008 D3).
//
// Error contract — lenient, no error return (D7):
//
//   - 0 seeds          → empty graph (0 nodes, 0 edges), nil touchIDs
//   - Nonexistent seed → seed is silently skipped; other seeds proceed
//   - Seed with no entities → treated the same as nonexistent
//   - SQL error on a single entity/relation query → logged + skipped
//   - MaxNodes reached mid-BFS → partial graph is returned (valid for PPR)
//   - Context cancelled → BFS stops at current position; partial graph returned
//
// Edges are bidirectional: a DB relation A→B produces scoring.Edge{A,B,w} and
// scoring.Edge{B,A,w} so that the PPR random surfer can traverse both
// directions (D4). Duplicate edges from symmetric traversal are handled by
// NewSparseGraph via last-write-wins semantics.
//
// Only svc.projectStore is consulted; cross-project relations do not exist by
// design (SPEC-006 D1, D9).
func (svc *MemoryService) BuildGraphForSeeds(
	ctx context.Context,
	seedMemoryIDs []string,
	opts GraphBuildOptions,
) (*scoring.SparseGraph, []string) {
	// visited tracks entity IDs already included in the BFS to prevent
	// re-processing and enforce the MaxNodes cap.
	visited := make(map[scoring.NodeID]struct{}, opts.MaxNodes)

	// frontier holds the entity IDs to expand in the current BFS level.
	frontier := make([]scoring.NodeID, 0, len(seedMemoryIDs)*3)

	var touchIDs []string
	var edges []scoring.Edge

	// --- Step 1: Resolve seed memory IDs → initial entity frontier ---
	//
	// For each seed memory, look up its linked entities. Non-existent memories
	// and memories with no entity links are silently skipped (D7).
	for _, seedID := range seedMemoryIDs {
		entities, err := svc.projectStore.GetMemoryEntities(ctx, seedID)
		if err != nil {
			slog.DebugContext(ctx, "graph builder: seed entity lookup failed",
				"event", "graph_build_seed_skip",
				"seed_id", seedID,
				"error", err,
			)
			continue
		}
		for _, e := range entities {
			if _, ok := visited[e.ID]; !ok {
				visited[e.ID] = struct{}{}
				frontier = append(frontier, e.ID)
			}
		}
	}

	// No seed entities resolved — return an empty (but valid) graph.
	if len(frontier) == 0 {
		empty := scoring.NewSparseGraph(nil)
		return &empty, nil
	}

	slog.DebugContext(ctx, "graph builder: BFS start",
		"event", "graph_build_start",
		"seed_entities", len(frontier),
		"max_depth", opts.MaxDepth,
		"max_nodes", opts.MaxNodes,
		"weight_threshold", opts.WeightThreshold,
	)

	// --- Step 2: BFS depth-limited entity traversal ---
	//
	// Each level expands the current frontier by querying GetStrongRelations for
	// every entity. Relations produce bidirectional edges. The traversal is
	// serial (D3): SQLite serialises all concurrent calls anyway, so parallelism
	// adds synchronisation overhead without throughput gain.
	for depth := 0; depth < opts.MaxDepth; depth++ {
		if len(frontier) == 0 || len(visited) >= opts.MaxNodes {
			break
		}

		nextFrontier := make([]scoring.NodeID, 0, len(frontier)*5)

		for _, entityID := range frontier {
			// Honour context cancellation between entities (not just between
			// levels) so callers with tight deadlines get a fast partial result.
			if ctx.Err() != nil {
				slog.DebugContext(ctx, "graph builder: context cancelled",
					"event", "graph_build_cancelled",
					"depth", depth,
					"nodes_so_far", len(visited),
				)
				break
			}
			if len(visited) >= opts.MaxNodes {
				break
			}

			rels, err := svc.projectStore.GetStrongRelations(
				ctx, entityID, opts.WeightThreshold, opts.FanOutCap,
			)
			if err != nil {
				slog.DebugContext(ctx, "graph builder: relation query failed",
					"event", "graph_build_rel_skip",
					"entity_id", entityID,
					"error", err,
				)
				continue
			}

			for _, rel := range rels {
				// Determine the neighbor entity on the other end of the relation.
				// GetStrongRelations is bidirectional (two-query merge in entity.go),
				// so entityID may appear as either SourceID or TargetID.
				neighborID := rel.TargetID
				if rel.TargetID == entityID {
					neighborID = rel.SourceID
				}

				// Bidirectional edges (D4): both directions with the same weight.
				edges = append(edges,
					scoring.Edge{From: entityID, To: neighborID, Weight: rel.Weight},
					scoring.Edge{From: neighborID, To: entityID, Weight: rel.Weight},
				)
				touchIDs = append(touchIDs, rel.ID)

				if _, ok := visited[neighborID]; !ok {
					if len(visited) >= opts.MaxNodes {
						// Node cap reached: still record the edge (the entity is
						// referenced by an edge we already added) but do not
						// expand it further. Break out of the relation loop so
						// the outer entity loop can also terminate.
						break
					}
					visited[neighborID] = struct{}{}
					nextFrontier = append(nextFrontier, neighborID)
				}
			}
		}

		frontier = nextFrontier
	}

	// --- Step 3: Construct the SparseGraph from the collected edges ---
	graph := scoring.NewSparseGraph(edges)

	slog.DebugContext(ctx, "graph builder: BFS done",
		"event", "graph_build_done",
		"nodes", len(graph.Nodes),
		"unique_relations", len(touchIDs),
		"raw_edges", len(edges),
	)

	return &graph, touchIDs
}
