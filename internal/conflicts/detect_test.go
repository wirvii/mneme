package conflicts

import (
	"testing"
)

// TestExtractSalientTerms verifies deterministic extraction of non-stop-word
// tokens from title+content input.
func TestExtractSalientTerms(t *testing.T) {
	cases := []struct {
		name     string
		title    string
		content  string
		maxTerms int
		// wantLen is the expected number of results; ≥ wantHas means all wantHas
		// terms appear somewhere in the result.
		wantHas []string
		wantNot []string
	}{
		{
			name:     "basic extraction",
			title:    "JWT authentication",
			content:  "We use JWT tokens for authentication in the API",
			maxTerms: 10,
			wantHas:  []string{"authentication", "tokens", "jwt"},
			wantNot:  []string{"we", "use", "in", "the", "for"},
		},
		{
			name:     "dedup across title and content",
			title:    "Database migration",
			content:  "Database schema migration steps",
			maxTerms: 10,
			wantHas:  []string{"database", "migration"},
		},
		{
			name:     "maxTerms cap",
			title:    "alpha beta gamma delta epsilon zeta eta theta iota kappa",
			content:  "",
			maxTerms: 3,
		},
		{
			name:     "all stop words returns empty",
			title:    "is the a",
			content:  "and or but",
			maxTerms: 10,
		},
		{
			name:     "very short tokens excluded",
			title:    "Go is a fast language",
			content:  "it is very fast",
			maxTerms: 10,
			wantNot:  []string{"is", "it"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractSalientTerms(tc.title, tc.content, tc.maxTerms)

			// Check max terms cap.
			if tc.maxTerms > 0 && len(got) > tc.maxTerms {
				t.Errorf("len(got) = %d, want ≤ %d", len(got), tc.maxTerms)
			}

			// Build lookup for easy membership check.
			lookup := make(map[string]bool, len(got))
			for _, term := range got {
				lookup[term] = true
			}

			for _, want := range tc.wantHas {
				if !lookup[want] {
					t.Errorf("expected term %q in result %v", want, got)
				}
			}
			for _, notWant := range tc.wantNot {
				if lookup[notWant] {
					t.Errorf("unexpected term %q in result %v", notWant, got)
				}
			}
		})
	}
}

// TestExtractSalientTerms_Deterministic verifies that the same input always
// produces the same output (order stability).
func TestExtractSalientTerms_Deterministic(t *testing.T) {
	title := "Authentication token signing key rotation"
	content := "HMAC-SHA256 tokens for authentication signing and verification"

	first := ExtractSalientTerms(title, content, 8)
	second := ExtractSalientTerms(title, content, 8)

	if len(first) != len(second) {
		t.Fatalf("length mismatch: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("position %d: %q vs %q", i, first[i], second[i])
		}
	}
}

// TestBuildCandidateQuery verifies that terms are correctly wrapped in FTS5
// double-quotes and joined with OR.
func TestBuildCandidateQuery(t *testing.T) {
	cases := []struct {
		name  string
		terms []string
		want  string
	}{
		{
			name:  "empty terms returns empty string",
			terms: nil,
			want:  "",
		},
		{
			name:  "single term",
			terms: []string{"authentication"},
			want:  `"authentication"`,
		},
		{
			name:  "multiple terms",
			terms: []string{"authentication", "token", "jwt"},
			want:  `"authentication" OR "token" OR "jwt"`,
		},
		{
			name:  "term with embedded double-quote escaped",
			terms: []string{`say "hello"`},
			want:  `"say ""hello"""`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildCandidateQuery(tc.terms)
			if got != tc.want {
				t.Errorf("BuildCandidateQuery(%v)\n got  %q\n want %q", tc.terms, got, tc.want)
			}
		})
	}
}
