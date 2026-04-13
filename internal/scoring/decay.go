package scoring

import (
	"math"
	"time"
)

// EffectiveImportance computes the current importance of a memory by applying
// exponential decay based on how long ago it was last accessed. Memories that
// have not been accessed recently are less likely to be relevant now, so their
// effective importance shrinks over time. The result is always in [0.0, 1.0].
//
// The formula is: importance × e^(−decayRate × daysSinceLastAccess)
//
// If lastAccessed is in the future (clock skew or test data), days defaults to
// zero so that importance is returned unchanged — a future timestamp cannot
// indicate staleness.
func EffectiveImportance(importance, decayRate float64, lastAccessed time.Time) float64 {
	return EffectiveImportanceAt(importance, decayRate, lastAccessed, time.Now())
}

// EffectiveImportanceAt is the deterministic counterpart of EffectiveImportance.
// It accepts an explicit now parameter so tests can exercise decay calculations
// without depending on wall-clock time. In production code prefer
// EffectiveImportance, which supplies now = time.Now() automatically.
func EffectiveImportanceAt(importance, decayRate float64, lastAccessed, now time.Time) float64 {
	days := now.Sub(lastAccessed).Hours() / 24.0
	if days < 0 {
		days = 0
	}
	result := importance * math.Exp(-decayRate*days)
	return clamp(result, 0.0, 1.0)
}
