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

// Search performs a full-text search and re-ranks results by a combined score
// that blends BM25 text relevance, memory importance, and time-decay signals.
//
// Validation rules:
//   - Query must not be empty (ErrQueryRequired)
//   - Limit defaults to config.Search.DefaultLimit when zero or negative
//   - Limit is capped at 50 to protect the context window
//   - Project defaults to the service's project when omitted
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

	var results []model.SearchResult

	if req.Scope != nil && (*req.Scope == model.ScopeGlobal || *req.Scope == model.ScopeOrg) {
		// Explicit global/org scope: query only the global store.
		r, err := svc.globalStore.FTS5Search(ctx, req.Query, opts)
		if err != nil {
			return nil, fmt.Errorf("service: search: global store: %w", err)
		}
		results = r
	} else if req.Scope == nil {
		// No scope filter: merge results from both stores so the agent can find
		// any memory regardless of where it lives.
		projectResults, err := svc.projectStore.FTS5Search(ctx, req.Query, opts)
		if err != nil {
			return nil, fmt.Errorf("service: search: project store: %w", err)
		}
		globalOpts := opts
		globalOpts.Project = "" // global store does not partition by project
		globalResults, err := svc.globalStore.FTS5Search(ctx, req.Query, globalOpts)
		if err != nil {
			return nil, fmt.Errorf("service: search: global store: %w", err)
		}
		results = append(projectResults, globalResults...)
	} else {
		// Explicit project scope: query only the project store.
		r, err := svc.projectStore.FTS5Search(ctx, req.Query, opts)
		if err != nil {
			return nil, fmt.Errorf("service: search: project store: %w", err)
		}
		results = r
	}

	now := time.Now()

	// Re-rank by combining BM25 with importance and recency via scoring.FinalScoreAt.
	// BM25 scores from SQLite are negative (closer to 0 = better match). We negate
	// them to produce a positive value for use as the bm25Score argument, so that
	// a strong BM25 match yields a higher final score.
	for i := range results {
		r := &results[i]
		lastAccessed := r.Memory.CreatedAt // fallback when never accessed
		if r.Memory.LastAccessed != nil {
			lastAccessed = *r.Memory.LastAccessed
		}
		// BM25Score is negative; negate it so higher = better match.
		positiveBM25 := -r.BM25Score
		r.RelevanceScore = scoring.FinalScoreAt(
			positiveBM25,
			r.Memory.Importance,
			lastAccessed,
			now,
			r.Memory.DecayRate,
		)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].RelevanceScore > results[j].RelevanceScore
	})

	return &model.SearchResponse{
		Results: results,
		Total:   len(results),
		Query:   req.Query,
	}, nil
}
