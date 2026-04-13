package scoring_test

import (
	"math"
	"testing"
	"time"

	"github.com/juanftp/mneme/internal/scoring"
)

// approxEqual reports whether a and b are within epsilon of each other.
// It is used throughout the scoring tests to avoid brittle float comparisons
// against constants that are rounded transcendental values.
func approxEqual(a, b, epsilon float64) bool {
	return math.Abs(a-b) <= epsilon
}

func TestEffectiveImportanceAt(t *testing.T) {
	t.Parallel()

	const epsilon = 1e-4
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name         string
		importance   float64
		decayRate    float64
		lastAccessed time.Time
		now          time.Time
		// wantFactor is the expected multiplier applied to importance.
		// want = importance * wantFactor
		wantFactor float64
	}{
		{
			name:         "recently accessed (0 days) — no decay",
			importance:   0.8,
			decayRate:    0.02,
			lastAccessed: base,
			now:          base,
			wantFactor:   1.0, // e^0 = 1
		},
		{
			name:         "30 days with decay 0.02",
			importance:   0.8,
			decayRate:    0.02,
			lastAccessed: base,
			now:          base.AddDate(0, 0, 30),
			wantFactor:   math.Exp(-0.02 * 30), // ≈ 0.5488
		},
		{
			name:         "100 days with decay 0.005",
			importance:   1.0,
			decayRate:    0.005,
			lastAccessed: base,
			now:          base.AddDate(0, 0, 100),
			wantFactor:   math.Exp(-0.005 * 100), // ≈ 0.6065
		},
		{
			name:         "1 day with decay 0.05",
			importance:   1.0,
			decayRate:    0.05,
			lastAccessed: base,
			now:          base.AddDate(0, 0, 1),
			wantFactor:   math.Exp(-0.05 * 1), // ≈ 0.9512
		},
		{
			name:         "lastAccessed in the future — days clamped to 0",
			importance:   0.7,
			decayRate:    0.1,
			lastAccessed: base.AddDate(0, 0, 10), // 10 days ahead
			now:          base,
			wantFactor:   1.0, // negative days → 0 → e^0 = 1
		},
		{
			name:         "result clamped to 1.0 when importance > 1",
			importance:   2.0, // invalid but clamp must hold
			decayRate:    0.0,
			lastAccessed: base,
			now:          base,
			wantFactor:   1.0, // 2.0 * e^0 = 2.0, clamped to 1.0
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			want := clampF(tc.importance*tc.wantFactor, 0.0, 1.0)
			got := scoring.EffectiveImportanceAt(tc.importance, tc.decayRate, tc.lastAccessed, tc.now)

			if !approxEqual(got, want, epsilon) {
				t.Errorf("EffectiveImportanceAt() = %v, want %v (epsilon %v)", got, want, epsilon)
			}
		})
	}
}

// clampF is a local helper for expected-value calculations in table rows that
// would otherwise overflow [0, 1]. It mirrors the internal clamp logic.
func clampF(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
