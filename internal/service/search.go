package service

import (
	"context"
	"fmt"
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

	// === Fusion ===
	var results []model.SearchResult
	if len(vectorResults) > 0 {
		results = svc.fuseAndRank(ctx, ftsResults, vectorResults, limit)
	} else {
		// Fallback: FTS5-only with the existing re-ranking logic.
		results = svc.reRankFTS5(ftsResults)
		if len(results) > limit {
			results = results[:limit]
		}
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

// fuseAndRank merges FTS5 and vector results using RRF, then assembles
// SearchResult values with VectorScore populated for transparency.
//
// When a memory appears only in vector results (no FTS5 match), it is loaded
// from the store so that semantic-only hits are not silently dropped. This is
// the core guarantee of hybrid retrieval: a query like "authentication flow"
// must surface memories about JWT auth even when FTS5 finds no token overlap.
func (svc *MemoryService) fuseAndRank(ctx context.Context, ftsResults []model.SearchResult, vectorResults []store.VectorResult, limit int) []model.SearchResult {
	// Convert FTS5 results into RankedResults (1-based rank).
	ftsRanks := make([]scoring.RankedResult, len(ftsResults))
	for i, r := range ftsResults {
		ftsRanks[i] = scoring.RankedResult{
			ID:     r.Memory.ID,
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

	all := append(ftsRanks, vecRanks...)
	fused := scoring.RRFScore(all, scoring.DefaultRRFK)

	// Build a lookup map for fast access to FTS5 results by ID.
	ftsMap := make(map[string]*model.SearchResult, len(ftsResults))
	for i := range ftsResults {
		ftsMap[ftsResults[i].Memory.ID] = &ftsResults[i]
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
		lastAccessed := sr.Memory.CreatedAt
		if sr.Memory.LastAccessed != nil {
			lastAccessed = *sr.Memory.LastAccessed
		}
		positiveBM25 := -sr.BM25Score
		finalScore := scoring.FinalScoreAt(positiveBM25, sr.Memory.Importance, lastAccessed, now, sr.Memory.DecayRate)

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
		id := ftsResults[i].Memory.ID
		if seen[id] {
			continue
		}
		seen[id] = true

		sr := &ftsResults[i]
		lastAccessed := sr.Memory.CreatedAt
		if sr.Memory.LastAccessed != nil {
			lastAccessed = *sr.Memory.LastAccessed
		}
		sr.RelevanceScore = scoring.FinalScoreAt(-sr.BM25Score, sr.Memory.Importance, lastAccessed, now, sr.Memory.DecayRate)
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

// reRankFTS5 applies the existing time-decay + importance re-ranking over a
// list of FTS5 results. Used when vector search is disabled or returns nothing.
func (svc *MemoryService) reRankFTS5(results []model.SearchResult) []model.SearchResult {
	now := time.Now()
	for i := range results {
		r := &results[i]
		lastAccessed := r.Memory.CreatedAt
		if r.Memory.LastAccessed != nil {
			lastAccessed = *r.Memory.LastAccessed
		}
		r.RelevanceScore = scoring.FinalScoreAt(
			-r.BM25Score,
			r.Memory.Importance,
			lastAccessed,
			now,
			r.Memory.DecayRate,
		)
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].RelevanceScore > results[j].RelevanceScore
	})
	return results
}
