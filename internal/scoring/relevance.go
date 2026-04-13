package scoring

import (
	"math"
	"time"
)

// FinalScore combines a BM25 full-text search score with importance and recency
// signals to produce a single ranking score. Higher is better.
//
// The importance weight is mapped to [0.5, 1.0] so that even a zero-importance
// memory can still appear when the BM25 match is strong. The recency weight
// applies the same exponential decay used by EffectiveImportance, ensuring that
// stale memories are down-ranked relative to recently-accessed ones.
//
//	finalScore = bm25 × importanceWeight × recencyWeight
//	importanceWeight = 0.5 + (importance × 0.5)
//	recencyWeight    = e^(−decayRate × daysSinceLastAccess)
func FinalScore(bm25Score, importance float64, lastAccessed time.Time, decayRate float64) float64 {
	return FinalScoreAt(bm25Score, importance, lastAccessed, time.Now(), decayRate)
}

// FinalScoreAt is the deterministic counterpart of FinalScore. It accepts an
// explicit now parameter so the full ranking pipeline can be exercised in tests
// without depending on wall-clock time.
func FinalScoreAt(bm25Score, importance float64, lastAccessed, now time.Time, decayRate float64) float64 {
	importanceWeight := 0.5 + (importance * 0.5)

	days := now.Sub(lastAccessed).Hours() / 24.0
	if days < 0 {
		days = 0
	}
	recencyWeight := math.Exp(-decayRate * days)

	return bm25Score * importanceWeight * recencyWeight
}
