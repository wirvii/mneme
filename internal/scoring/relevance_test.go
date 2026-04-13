package scoring_test

import (
	"math"
	"testing"
	"time"

	"github.com/juanftp/mneme/internal/scoring"
)

func TestFinalScoreAt(t *testing.T) {
	t.Parallel()

	const epsilon = 1e-6
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name         string
		bm25         float64
		importance   float64
		lastAccessed time.Time
		now          time.Time
		decayRate    float64
		want         float64
	}{
		{
			name:         "high importance, zero decay, zero days",
			bm25:         1.0,
			importance:   1.0,
			lastAccessed: base,
			now:          base,
			decayRate:    0.0,
			// importanceWeight = 0.5 + 1.0*0.5 = 1.0; recency = e^0 = 1.0
			want: 1.0 * 1.0 * 1.0,
		},
		{
			name:         "zero importance, zero decay, zero days",
			bm25:         1.0,
			importance:   0.0,
			lastAccessed: base,
			now:          base,
			decayRate:    0.0,
			// importanceWeight = 0.5 + 0.0*0.5 = 0.5; recency = 1.0
			want: 1.0 * 0.5 * 1.0,
		},
		{
			name:         "mid importance, 30 days, decay 0.02",
			bm25:         2.0,
			importance:   0.8,
			lastAccessed: base,
			now:          base.AddDate(0, 0, 30),
			decayRate:    0.02,
			// importanceWeight = 0.5 + 0.8*0.5 = 0.9; recency = e^(-0.02*30)
			want: 2.0 * 0.9 * math.Exp(-0.02*30),
		},
		{
			name:         "low bm25 boosts nothing",
			bm25:         0.1,
			importance:   1.0,
			lastAccessed: base,
			now:          base,
			decayRate:    0.0,
			want:         0.1 * 1.0 * 1.0,
		},
		{
			name:         "future lastAccessed — days = 0",
			bm25:         1.0,
			importance:   0.6,
			lastAccessed: base.AddDate(0, 0, 5),
			now:          base,
			decayRate:    0.1,
			// days clamped to 0 → recency = 1.0
			want: 1.0 * (0.5 + 0.6*0.5) * 1.0,
		},
		{
			name:         "high decay over 100 days",
			bm25:         3.0,
			importance:   0.5,
			lastAccessed: base,
			now:          base.AddDate(0, 0, 100),
			decayRate:    0.05,
			want:         3.0 * (0.5 + 0.5*0.5) * math.Exp(-0.05*100),
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := scoring.FinalScoreAt(tc.bm25, tc.importance, tc.lastAccessed, tc.now, tc.decayRate)
			if !approxEqual(got, tc.want, epsilon) {
				t.Errorf("FinalScoreAt() = %v, want %v", got, tc.want)
			}
		})
	}
}
