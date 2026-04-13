// Package scoring provides algorithms for calculating memory importance,
// time-based decay, and search result relevance. These scores determine which
// memories surface in search results and context assembly, and which memories
// are candidates for consolidation and eviction.
package scoring

import "github.com/juanftp/mneme/internal/model"

// InitialImportance returns the starting importance score for a memory of the
// given type. When override is non-nil its value is used (clamped to [0.0,
// 1.0]), allowing callers to express explicit importance without bypassing the
// clamping contract. When override is nil the type-specific default from
// model.DefaultImportance is used. Unknown types fall back to 0.5, a neutral
// midpoint that neither prioritises nor suppresses the memory.
func InitialImportance(memType model.MemoryType, override *float64) float64 {
	if override != nil {
		return clamp(*override, 0.0, 1.0)
	}
	if v, ok := model.DefaultImportance[memType]; ok {
		return v
	}
	return 0.5
}

// DecayRateForType returns the per-day exponential decay multiplier for the
// given memory type. A lower rate means the memory retains its effective
// importance longer between accesses. Unknown types receive 0.01, a
// conservative default that preserves moderate-lived memories.
func DecayRateForType(memType model.MemoryType) float64 {
	if v, ok := model.DefaultDecayRate[memType]; ok {
		return v
	}
	return 0.01
}

// clamp returns v if it lies within [min, max], min if v < min, or max if v > max.
// It is used internally to enforce the [0.0, 1.0] contract on all score outputs
// without callers needing to guard against out-of-range values.
func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
