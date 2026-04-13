package scoring

import "sort"

// DefaultRRFK is the smoothing constant recommended by the original RRF paper
// (Cormack et al., 2009). Values around 60 prevent any single top-ranked
// result from dominating the fusion and empirically outperform other choices
// across a wide range of retrieval tasks.
const DefaultRRFK = 60.0

// RankedResult represents a single document's rank in one retrieval system.
// Weight lets callers express that some retrieval backends (e.g. BM25) are
// more trustworthy than others (e.g. a rough heuristic) without discarding
// either signal entirely.
type RankedResult struct {
	// ID is the document identifier, matching Memory.ID in the domain model.
	ID string

	// Rank is the 1-based position of this document in its ranked list.
	// Rank 1 is the best match. Lower is better.
	Rank int

	// Weight scales the contribution of this ranked list to the fused score.
	// A weight of 1.0 is neutral; 2.0 doubles the list's influence.
	Weight float64
}

// FusedResult holds the aggregated RRF score for a single document after
// combining contributions from all ranked lists.
type FusedResult struct {
	// ID is the document identifier.
	ID string

	// Score is the sum of weight/(k+rank) over all lists containing this document.
	// Higher is better. Results are returned sorted by Score descending.
	Score float64
}

// RRFScore implements Reciprocal Rank Fusion over one or more ranked lists.
// For each document it computes:
//
//	score += weight / (k + rank)
//
// across all lists that contain the document, then returns results sorted by
// score descending. Passing k = DefaultRRFK (60) is recommended unless there
// is a specific reason to tune it.
//
// This function is prepared for Fase 2 hybrid retrieval (BM25 + vector + graph)
// but can be used with any combination of ranked lists today.
func RRFScore(ranks []RankedResult, k float64) []FusedResult {
	if len(ranks) == 0 {
		return []FusedResult{}
	}

	scores := make(map[string]float64, len(ranks))
	for _, r := range ranks {
		scores[r.ID] += r.Weight / (k + float64(r.Rank))
	}

	fused := make([]FusedResult, 0, len(scores))
	for id, score := range scores {
		fused = append(fused, FusedResult{ID: id, Score: score})
	}

	sort.Slice(fused, func(i, j int) bool {
		if fused[i].Score != fused[j].Score {
			return fused[i].Score > fused[j].Score
		}
		// Stable tie-break by ID ensures deterministic output.
		return fused[i].ID < fused[j].ID
	})

	return fused
}
