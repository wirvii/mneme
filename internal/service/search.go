package service

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/scoring"
	"github.com/juanftp/mneme/internal/store"
)

// weightFTS5 is the RRF contribution weight for the FTS5 BM25 ranked list.
// It is slightly higher than vector weight because BM25 is exact-match and
// tends to be very precise when it fires.
const weightFTS5 = 1.0

// weightVector is the RRF contribution weight for the vector similarity list.
// Slightly lower than FTS5 because TF-IDF embeddings are approximate signals.
const weightVector = 0.8

// weightGraph is the RRF contribution weight for the graph expansion
// channel. Lower than both FTS5 and vector because graph proximity is an
// indirect signal (topology, not content). High enough to surface strongly
// connected memories that both text channels miss.
const weightGraph = 0.6

// graphTopN is the maximum number of results returned by graphChannelPPR
// before feeding into RRF. Caps the PPR channel at 50 results (same order
// of magnitude as FTS5 ~50 and vector ~100) so graph noise doesn't dominate
// the fusion. Not configurable: it is an output-cap, not an input-scale
// parameter (which is ExpansionSeedTopK).
const graphTopN = 50

// graphResult holds a memory discovered via 1-hop graph expansion.
// Used internally in the service layer; not exposed to any frontend.
type graphResult struct {
	// MemoryID is the UUIDv7 of the discovered memory.
	MemoryID string

	// GraphScore is max(rel_weight × 1/seed_rank) over all paths that led
	// to this memory. Used for building RankedResult entries.
	GraphScore float64
}

// Search performs a hybrid retrieval combining FTS5 BM25 and vector similarity
// when an embedder is active. Results are fused with Reciprocal Rank Fusion
// (RRF) and then re-ranked by the combined score that blends BM25 text
// relevance, memory importance, and time-decay signals.
//
// Validation rules:
//   - Query must not be empty (ErrQueryRequired)
//   - Limit defaults to config.Search.DefaultLimit when zero or negative
//   - Limit is capped at 50 to protect the context window
//   - Project defaults to the service's project when omitted
//
// When the embedder is NopEmbedder (Model() == "none") the method degrades
// gracefully to FTS5-only retrieval, identical to the pre-P002 behaviour.
func (svc *MemoryService) Search(ctx context.Context, req model.SearchRequest) (*model.SearchResponse, error) {
	if req.Query == "" {
		return nil, fmt.Errorf("service: search: %w", model.ErrQueryRequired)
	}

	limit := req.Limit
	if limit <= 0 {
		limit = svc.config.Search.DefaultLimit
	}
	if limit > 50 {
		limit = 50
	}

	if req.Project == "" {
		req.Project = svc.project
	}

	opts := store.SearchOptions{
		Project:           req.Project,
		Limit:             limit,
		IncludeSuperseded: req.IncludeSuperseded,
	}
	if req.Scope != nil {
		opts.Scope = *req.Scope
	}
	if req.Type != nil {
		opts.Type = *req.Type
	}

	// === Signal 1: FTS5 BM25 (always active) ===
	ftsResults, err := svc.fts5SearchAll(ctx, req, opts)
	if err != nil {
		return nil, err
	}

	// === Signal 2: Vector similarity (active only when embedder is configured) ===
	var vectorResults []store.VectorResult
	if svc.embedder.Model() != "none" {
		queryVec := svc.embedder.Embed(req.Query)
		if len(queryVec) > 0 {
			vectorResults = svc.vectorSearchAll(ctx, queryVec, req, opts)
		}
	}

	// Resolve effectiveMode: priority chain is IncludeGraph (request) >
	// ExpansionEnabled (config kill switch) > GraphMode (algorithm selector).
	effectiveMode := svc.config.Graph.GraphMode
	if effectiveMode == "" {
		effectiveMode = "ppr" // empty treated as default
	}
	if !svc.config.Graph.ExpansionEnabled {
		effectiveMode = "off"
	}
	if req.IncludeGraph != nil && !*req.IncludeGraph {
		effectiveMode = "off"
	}

	// === Fusion ===
	// Always use fuseAndRank when graph expansion is enabled, or when vector
	// results are available, so that graph expansion can augment even FTS5-only
	// queries. Fall back to the simpler reRankFTS5 path only when both vector
	// search and graph expansion are inactive.
	var results []model.SearchResult
	if len(vectorResults) > 0 || effectiveMode != "off" {
		results = svc.fuseAndRank(ctx, ftsResults, vectorResults, limit, effectiveMode)
	} else {
		// Fallback: FTS5-only with the existing re-ranking logic.
		results = svc.reRankFTS5(ftsResults)
		if len(results) > limit {
			results = results[:limit]
		}
	}

	// Hebbian tracking: record the top-3 results so co-access pairs can
	// strengthen relations between frequently co-retrieved entities (Q2).
	// Only the top-3 are tracked to limit noise from long result lists.
	const hebbianTopN = 3
	topN := len(results)
	if topN > hebbianTopN {
		topN = hebbianTopN
	}
	for _, sr := range results[:topN] {
		if sr.Memory == nil {
			continue
		}
		svc.recordHebbianAccess(ctx, svc.storeFor(sr.Scope), sr.Memory)
	}

	return &model.SearchResponse{
		Results: results,
		Total:   len(results),
		Query:   req.Query,
	}, nil
}

// ─── internal helpers ─────────────────────────────────────────────────────────

// fts5SearchAll queries the appropriate store(s) based on req.Scope and returns
// all FTS5 results merged into a single slice.
func (svc *MemoryService) fts5SearchAll(ctx context.Context, req model.SearchRequest, opts store.SearchOptions) ([]model.SearchResult, error) {
	if req.Scope != nil && (*req.Scope == model.ScopeGlobal || *req.Scope == model.ScopeOrg) {
		r, err := svc.globalStore.FTS5Search(ctx, req.Query, opts)
		if err != nil {
			return nil, fmt.Errorf("service: search: global store fts5: %w", err)
		}
		return r, nil
	}
	if req.Scope == nil {
		projectResults, err := svc.projectStore.FTS5Search(ctx, req.Query, opts)
		if err != nil {
			return nil, fmt.Errorf("service: search: project store fts5: %w", err)
		}
		globalOpts := opts
		globalOpts.Project = ""
		globalResults, err := svc.globalStore.FTS5Search(ctx, req.Query, globalOpts)
		if err != nil {
			return nil, fmt.Errorf("service: search: global store fts5: %w", err)
		}
		return append(projectResults, globalResults...), nil
	}
	// Explicit project scope.
	r, err := svc.projectStore.FTS5Search(ctx, req.Query, opts)
	if err != nil {
		return nil, fmt.Errorf("service: search: project store fts5: %w", err)
	}
	return r, nil
}

// vectorSearchAll runs a vector similarity search across the appropriate
// store(s) and returns the combined results. Failures are suppressed — the
// vector signal is always best-effort.
func (svc *MemoryService) vectorSearchAll(ctx context.Context, queryVec []float32, req model.SearchRequest, opts store.SearchOptions) []store.VectorResult {
	vOpts := store.VectorSearchOptions{
		Project: req.Project,
		Limit:   opts.Limit * 2, // over-fetch so RRF has more candidates
	}
	if req.Scope != nil {
		vOpts.Scope = *req.Scope
	}

	var results []store.VectorResult

	if req.Scope != nil && (*req.Scope == model.ScopeGlobal || *req.Scope == model.ScopeOrg) {
		globalOpts := vOpts
		globalOpts.Project = ""
		r, err := svc.globalStore.VectorSearch(ctx, queryVec, globalOpts)
		if err != nil {
			return nil
		}
		return r
	}

	if req.Scope == nil {
		projectResults, err := svc.projectStore.VectorSearch(ctx, queryVec, vOpts)
		if err == nil {
			results = append(results, projectResults...)
		}
		globalOpts := vOpts
		globalOpts.Project = ""
		globalResults, err := svc.globalStore.VectorSearch(ctx, queryVec, globalOpts)
		if err == nil {
			results = append(results, globalResults...)
		}
		return results
	}

	// Explicit project scope.
	r, err := svc.projectStore.VectorSearch(ctx, queryVec, vOpts)
	if err != nil {
		return nil
	}
	return r
}

// fuseAndRank merges FTS5, vector, and (optionally) graph expansion results
// using Reciprocal Rank Fusion (RRF), then assembles SearchResult values with
// VectorScore populated for transparency.
//
// effectiveMode controls graph expansion:
//   - "ppr"  — runs graphChannelPPR (Personalized PageRank, multi-hop). Default.
//   - "1hop" — runs graphExpand (1-hop bidirectional, SPEC-007 behaviour).
//   - "off"  — no graph channel; 2-channel RRF (FTS5 + vector) only.
//
// Graph expansion runs even when the embedder is NopEmbedder (FTS5-only path)
// — vector results are simply an empty slice in that case.
//
// When a memory appears only in vector results (no FTS5 match), it is loaded
// from the store so that semantic-only hits are not silently dropped. This is
// the core guarantee of hybrid retrieval: a query like "authentication flow"
// must surface memories about JWT auth even when FTS5 finds no token overlap.
func (svc *MemoryService) fuseAndRank(ctx context.Context, ftsResults []model.SearchResult, vectorResults []store.VectorResult, limit int, effectiveMode string) []model.SearchResult {
	// Convert FTS5 results into RankedResults (1-based rank).
	ftsRanks := make([]scoring.RankedResult, len(ftsResults))
	for i, r := range ftsResults {
		ftsRanks[i] = scoring.RankedResult{
			ID:     r.ID,
			Rank:   i + 1,
			Weight: weightFTS5,
		}
	}

	// Convert vector results into RankedResults.
	vecRanks := make([]scoring.RankedResult, len(vectorResults))
	for i, vr := range vectorResults {
		vecRanks[i] = scoring.RankedResult{
			ID:     vr.MemoryID,
			Rank:   i + 1,
			Weight: weightVector,
		}
	}

	// === Signal 3: Graph expansion (ppr/1hop/off) ===
	var graphRanks []scoring.RankedResult
	if effectiveMode != "off" {
		// Preliminary 2-channel fusion to identify seeds.
		preliminary := scoring.RRFScore(append(ftsRanks, vecRanks...), scoring.DefaultRRFK)
		topK := svc.config.Graph.ExpansionSeedTopK
		if topK > len(preliminary) {
			topK = len(preliminary)
		}
		if topK > 0 {
			seedIDs := make([]string, topK)
			for i := 0; i < topK; i++ {
				seedIDs[i] = preliminary[i].ID
			}

			var graphResults []graphResult
			var touchIDs []string
			switch effectiveMode {
			case "ppr":
				graphResults, touchIDs = svc.graphChannelPPR(ctx, seedIDs)
			default: // "1hop"
				graphResults, touchIDs = svc.graphExpand(ctx, seedIDs)
			}

			graphRanks = make([]scoring.RankedResult, len(graphResults))
			for i, gr := range graphResults {
				graphRanks[i] = scoring.RankedResult{
					ID:     gr.MemoryID,
					Rank:   i + 1,
					Weight: weightGraph,
				}
			}

			// Touch traversed relations asynchronously (best-effort timestamp update).
			svc.batchTouchRelations(ctx, touchIDs)
		}
	}

	all := append(append(ftsRanks, vecRanks...), graphRanks...)
	fused := scoring.RRFScore(all, scoring.DefaultRRFK)

	// Build a lookup map for fast access to FTS5 results by ID.
	ftsMap := make(map[string]*model.SearchResult, len(ftsResults))
	for i := range ftsResults {
		ftsMap[ftsResults[i].ID] = &ftsResults[i]
	}

	// Build a lookup map for vector scores.
	vecScoreMap := make(map[string]float64, len(vectorResults))
	for _, vr := range vectorResults {
		vecScoreMap[vr.MemoryID] = vr.Similarity
	}

	// Assemble the final result list in RRF-fused order.
	// When a memory appears only in vector results (not in FTS5), load it from
	// the store so that semantic-only matches are included in the final output.
	results := make([]model.SearchResult, 0, len(fused))
	seen := make(map[string]bool, len(fused))

	now := time.Now()

	for _, fr := range fused {
		if seen[fr.ID] {
			continue
		}
		seen[fr.ID] = true

		sr, ok := ftsMap[fr.ID]
		if !ok {
			// Memory found only by vector search — load it from the store to
			// build a complete SearchResult with a preview snippet.
			mem, _, loadErr := svc.getFromEitherStore(ctx, fr.ID)
			if loadErr != nil || mem == nil {
				// Non-fatal: best-effort, skip if unavailable.
				continue
			}
			lastAccessed := mem.CreatedAt
			if mem.LastAccessed != nil {
				lastAccessed = *mem.LastAccessed
			}
			finalScore := scoring.FinalScoreAt(0, mem.Importance, lastAccessed, now, mem.DecayRate)
			results = append(results, model.SearchResult{
				Memory:         mem,
				Preview:        makeTimelinePreview(mem.Content),
				BM25Score:      0,
				VectorScore:    vecScoreMap[fr.ID],
				RelevanceScore: finalScore + fr.Score,
			})
			continue
		}

		// Update RelevanceScore with the RRF-fused score plus time-decay.
		lastAccessed := sr.CreatedAt
		if sr.LastAccessed != nil {
			lastAccessed = *sr.LastAccessed
		}
		positiveBM25 := -sr.BM25Score
		finalScore := scoring.FinalScoreAt(positiveBM25, sr.Importance, lastAccessed, now, sr.DecayRate)

		result := model.SearchResult{
			Memory:         sr.Memory,
			Preview:        sr.Preview,
			BM25Score:      sr.BM25Score,
			VectorScore:    vecScoreMap[fr.ID],
			RelevanceScore: finalScore + fr.Score, // blend decay-adjusted score with RRF
		}
		results = append(results, result)
	}

	// Include any FTS5-only results not already in the fused list.
	for i := range ftsResults {
		id := ftsResults[i].ID
		if seen[id] {
			continue
		}
		seen[id] = true

		sr := &ftsResults[i]
		lastAccessed := sr.CreatedAt
		if sr.LastAccessed != nil {
			lastAccessed = *sr.LastAccessed
		}
		sr.RelevanceScore = scoring.FinalScoreAt(-sr.BM25Score, sr.Importance, lastAccessed, now, sr.DecayRate)
		results = append(results, *sr)
	}

	// Final sort by RelevanceScore descending.
	sort.Slice(results, func(i, j int) bool {
		return results[i].RelevanceScore > results[j].RelevanceScore
	})

	if len(results) > limit {
		results = results[:limit]
	}

	return results
}

// graphChannelPPR builds a PPR-ranked graph channel for RRF fusion. It
// constructs a subgraph from seed memories via BuildGraphForSeeds, runs
// Personalized PageRank, maps entity scores back to memory IDs, and returns
// the top graphTopN memory results ranked by PPR score.
//
// On any PPR failure (empty graph, no seed entities, PPR error, or panic) the
// function falls back to graphExpand (1-hop). Search never fails from graph
// expansion.
//
// Returns (graphResults, touchIDs). touchIDs are the relation IDs traversed
// during BFS so the caller can update last_traversed_at via batchTouchRelations.
func (svc *MemoryService) graphChannelPPR(ctx context.Context, seedIDs []string) (results []graphResult, touchIDs []string) {
	// Defensive panic recovery: PPR is well-tested but any unexpected panic
	// must never break search.
	defer func() {
		if r := recover(); r != nil {
			slog.WarnContext(ctx, "ppr graph channel panic, falling back to 1-hop",
				"event", "graph_ppr_panic",
				"recover", r,
			)
			results, touchIDs = svc.graphExpand(ctx, seedIDs)
		}
	}()

	// 1. Build the BFS subgraph around the seeds.
	opts := DefaultGraphBuildOptions()
	graph, bfsTouchIDs := svc.BuildGraphForSeeds(ctx, seedIDs, opts)

	if len(graph.Nodes) == 0 {
		slog.DebugContext(ctx, "ppr graph empty, falling back to 1-hop",
			"event", "graph_ppr_fallback",
			"reason", "empty_graph",
		)
		return svc.graphExpand(ctx, seedIDs)
	}

	// 2. Map seed memory IDs to entity IDs for PPR teleport targets.
	pprSeeds := svc.resolveSeedEntities(ctx, seedIDs)
	if len(pprSeeds) == 0 {
		slog.DebugContext(ctx, "ppr no seed entities, falling back to 1-hop",
			"event", "graph_ppr_fallback",
			"reason", "no_seed_entities",
		)
		return svc.graphExpand(ctx, seedIDs)
	}

	// 3. Run PPR power iteration.
	pprResult, err := scoring.PPR(*graph, pprSeeds, scoring.DefaultPPROptions())
	if err != nil {
		slog.WarnContext(ctx, "ppr failed, falling back to 1-hop",
			"event", "graph_ppr_fallback",
			"reason", "ppr_error",
			"error", err,
		)
		return svc.graphExpand(ctx, seedIDs)
	}

	// 4. Map entity PPR scores back to memory IDs.
	// Memory score = max(PPR score of linked entities).
	accumulated := make(map[string]float64)
	for entityID, score := range pprResult.Scores {
		if score <= 0 {
			continue
		}
		memIDs, memErr := svc.projectStore.GetEntityMemoryIDs(ctx, entityID)
		if memErr != nil {
			continue
		}
		for _, memID := range memIDs {
			if isSeed(memID, seedIDs) {
				continue // exclude seeds from graph results
			}
			if score > accumulated[memID] {
				accumulated[memID] = score
			}
		}
	}

	// 5. Sort by PPR score descending, apply top-N cap.
	results = make([]graphResult, 0, len(accumulated))
	for memID, score := range accumulated {
		results = append(results, graphResult{MemoryID: memID, GraphScore: score})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].GraphScore != results[j].GraphScore {
			return results[i].GraphScore > results[j].GraphScore
		}
		return results[i].MemoryID < results[j].MemoryID
	})
	if len(results) > graphTopN {
		results = results[:graphTopN]
	}

	slog.DebugContext(ctx, "ppr graph channel done",
		"event", "graph_ppr_done",
		"nodes", len(graph.Nodes),
		"ppr_iterations", pprResult.Iterations,
		"ppr_converged", pprResult.Converged,
		"graph_results", len(results),
	)

	return results, bfsTouchIDs
}

// resolveSeedEntities maps seed memory IDs to the entity IDs that PPR will use
// as teleport targets. Returns a deduplicated slice of entity IDs.
// Errors are swallowed (best-effort, same as graphExpand pattern).
func (svc *MemoryService) resolveSeedEntities(ctx context.Context, seedIDs []string) []scoring.NodeID {
	seen := make(map[scoring.NodeID]struct{}, len(seedIDs)*3)
	result := make([]scoring.NodeID, 0, len(seedIDs)*3)

	for _, seedID := range seedIDs {
		entities, err := svc.projectStore.GetMemoryEntities(ctx, seedID)
		if err != nil || len(entities) == 0 {
			continue
		}
		for _, e := range entities {
			if _, ok := seen[e.ID]; !ok {
				seen[e.ID] = struct{}{}
				result = append(result, e.ID)
			}
		}
	}
	return result
}

// isSeed reports whether memID is contained in the seedIDs slice.
// Linear scan is acceptable: seedIDs is at most ExpansionSeedTopK long (default 10).
func isSeed(memID string, seedIDs []string) bool {
	for _, id := range seedIDs {
		if id == memID {
			return true
		}
	}
	return false
}

// graphExpand performs a 1-hop graph expansion from a set of seed memory IDs.
// For each seed (ordered by its rank in seedIDs), it:
//  1. Loads the entities linked to the seed memory.
//  2. Queries strong relations (weight > ExpansionThreshold) for each entity.
//  3. Maps neighbor entities back to their memory IDs.
//  4. Scores each discovered memory as max(rel_weight × 1/seed_rank).
//
// Returns the discovered memories sorted by GraphScore descending and a slice
// of relation IDs that should be touched (last_traversed_at updated).
//
// Operates exclusively on projectStore — cross-scope expansion is not supported
// (same constraint as SPEC-006 D1: relations live within a single DB).
func (svc *MemoryService) graphExpand(ctx context.Context, seedIDs []string) ([]graphResult, []string) {
	cfg := svc.config.Graph
	accumulated := make(map[string]float64, len(seedIDs)*5) // memoryID → max graph score
	var touchIDs []string

	slog.DebugContext(ctx, "graph expansion start",
		"event", "graph_expansion",
		"seeds_count", len(seedIDs),
	)

	for rank, seedID := range seedIDs {
		seedWeight := 1.0 / float64(rank+1) // inverse rank, 1-based

		// Step 1: Get entities linked to this seed memory.
		entities, err := svc.projectStore.GetMemoryEntities(ctx, seedID)
		if err != nil || len(entities) == 0 {
			continue
		}

		// Step 2: For each entity, query strong relations.
		// neighborEntityID → slice of relations connecting to it from this seed's entities.
		neighborRelations := make(map[string][]*model.Relation)

		for _, entity := range entities {
			rels, err := svc.projectStore.GetStrongRelations(ctx, entity.ID, cfg.ExpansionThreshold, cfg.ExpansionFanOutCap)
			if err != nil {
				continue
			}
			for _, rel := range rels {
				// Determine the "other end" of the relation from this entity's perspective.
				neighborEntityID := rel.TargetID
				if rel.TargetID == entity.ID {
					neighborEntityID = rel.SourceID
				}
				neighborRelations[neighborEntityID] = append(neighborRelations[neighborEntityID], rel)
				touchIDs = append(touchIDs, rel.ID)
			}
		}

		// Step 3: For each neighbor entity, find memories and score them.
		for neighborEntityID, rels := range neighborRelations {
			// Maximum relation weight to this neighbor.
			maxWeight := 0.0
			for _, rel := range rels {
				if rel.Weight > maxWeight {
					maxWeight = rel.Weight
				}
			}

			memIDs, err := svc.projectStore.GetEntityMemoryIDs(ctx, neighborEntityID)
			if err != nil {
				continue
			}
			for _, memID := range memIDs {
				if memID == seedID {
					continue // don't re-score the seed itself
				}
				score := maxWeight * seedWeight
				// Use max (not sum) to avoid inflating hub nodes.
				if score > accumulated[memID] {
					accumulated[memID] = score
				}
			}
		}
	}

	if len(accumulated) == 0 {
		slog.InfoContext(ctx, "graph expansion done",
			"event", "graph_expansion",
			"neighbors_found", 0,
			"relations_touched", len(touchIDs),
		)
		return nil, touchIDs
	}

	// Build sorted results slice.
	results := make([]graphResult, 0, len(accumulated))
	for memID, score := range accumulated {
		results = append(results, graphResult{MemoryID: memID, GraphScore: score})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].GraphScore != results[j].GraphScore {
			return results[i].GraphScore > results[j].GraphScore
		}
		return results[i].MemoryID < results[j].MemoryID
	})

	slog.DebugContext(ctx, "graph expansion done",
		"event", "graph_expansion",
		"neighbors_found", len(results),
		"relations_touched", len(touchIDs),
	)

	return results, touchIDs
}

// batchTouchRelations updates last_traversed_at for a deduplicated set of
// relation IDs. Best-effort: failures are logged but never propagated to the
// caller since search results are not affected by a failed timestamp update.
func (svc *MemoryService) batchTouchRelations(ctx context.Context, relationIDs []string) {
	if len(relationIDs) == 0 {
		return
	}

	// Dedup: a relation can appear multiple times if it connects entities
	// that are both linked to different seeds.
	seen := make(map[string]bool, len(relationIDs))
	unique := make([]string, 0, len(relationIDs))
	for _, id := range relationIDs {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}

	if err := svc.projectStore.BatchTouchRelations(ctx, unique, time.Now().UTC()); err != nil {
		slog.WarnContext(ctx, "graph expansion: batch touch failed",
			"event", "graph_touch_error",
			"count", len(unique),
			"error", err,
		)
	}
}

// reRankFTS5 applies the existing time-decay + importance re-ranking over a
// list of FTS5 results. Used when vector search is disabled or returns nothing.
func (svc *MemoryService) reRankFTS5(results []model.SearchResult) []model.SearchResult {
	now := time.Now()
	for i := range results {
		r := &results[i]
		lastAccessed := r.CreatedAt
		if r.LastAccessed != nil {
			lastAccessed = *r.LastAccessed
		}
		r.RelevanceScore = scoring.FinalScoreAt(
			-r.BM25Score,
			r.Importance,
			lastAccessed,
			now,
			r.DecayRate,
		)
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].RelevanceScore > results[j].RelevanceScore
	})
	return results
}
