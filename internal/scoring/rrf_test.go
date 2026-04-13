package scoring_test

import (
	"testing"

	"github.com/juanftp/mneme/internal/scoring"
)

func TestRRFScore(t *testing.T) {
	t.Parallel()

	const epsilon = 1e-9
	const k = scoring.DefaultRRFK

	tests := []struct {
		name  string
		ranks []scoring.RankedResult
		k     float64
		want  []scoring.FusedResult
	}{
		{
			name:  "empty input returns empty slice",
			ranks: []scoring.RankedResult{},
			k:     k,
			want:  []scoring.FusedResult{},
		},
		{
			name: "single ranked list — score = weight / (k + rank)",
			ranks: []scoring.RankedResult{
				{ID: "a", Rank: 1, Weight: 1.0},
				{ID: "b", Rank: 2, Weight: 1.0},
				{ID: "c", Rank: 3, Weight: 1.0},
			},
			k: k,
			want: []scoring.FusedResult{
				{ID: "a", Score: 1.0 / (k + 1)},
				{ID: "b", Score: 1.0 / (k + 2)},
				{ID: "c", Score: 1.0 / (k + 3)},
			},
		},
		{
			name: "two lists with overlap — scores accumulate",
			ranks: []scoring.RankedResult{
				// List 1 (weight 1.0): a=1, b=2
				{ID: "a", Rank: 1, Weight: 1.0},
				{ID: "b", Rank: 2, Weight: 1.0},
				// List 2 (weight 1.0): b=1, c=2
				{ID: "b", Rank: 1, Weight: 1.0},
				{ID: "c", Rank: 2, Weight: 1.0},
			},
			k: k,
			want: []scoring.FusedResult{
				// b appears in both lists
				{ID: "b", Score: 1.0/(k+2) + 1.0/(k+1)},
				{ID: "a", Score: 1.0 / (k + 1)},
				{ID: "c", Score: 1.0 / (k + 2)},
			},
		},
		{
			name: "weighted lists scale contributions",
			ranks: []scoring.RankedResult{
				{ID: "x", Rank: 1, Weight: 2.0},
				{ID: "y", Rank: 1, Weight: 1.0},
			},
			k: k,
			want: []scoring.FusedResult{
				{ID: "x", Score: 2.0 / (k + 1)},
				{ID: "y", Score: 1.0 / (k + 1)},
			},
		},
		{
			name: "results are sorted by score descending",
			ranks: []scoring.RankedResult{
				{ID: "low", Rank: 10, Weight: 1.0},
				{ID: "high", Rank: 1, Weight: 1.0},
				{ID: "mid", Rank: 5, Weight: 1.0},
			},
			k: k,
			want: []scoring.FusedResult{
				{ID: "high", Score: 1.0 / (k + 1)},
				{ID: "mid", Score: 1.0 / (k + 5)},
				{ID: "low", Score: 1.0 / (k + 10)},
			},
		},
		{
			name: "custom k value changes scores",
			ranks: []scoring.RankedResult{
				{ID: "a", Rank: 1, Weight: 1.0},
			},
			k: 10.0,
			want: []scoring.FusedResult{
				{ID: "a", Score: 1.0 / (10.0 + 1)},
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := scoring.RRFScore(tc.ranks, tc.k)

			if len(got) != len(tc.want) {
				t.Fatalf("RRFScore() len = %d, want %d", len(got), len(tc.want))
			}

			for i, w := range tc.want {
				if got[i].ID != w.ID {
					t.Errorf("result[%d].ID = %q, want %q", i, got[i].ID, w.ID)
				}
				if !approxEqual(got[i].Score, w.Score, epsilon) {
					t.Errorf("result[%d].Score = %v, want %v", i, got[i].Score, w.Score)
				}
			}
		})
	}
}
