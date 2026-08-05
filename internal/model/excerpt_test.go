package model

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestExcerpt covers the byte-vs-rune boundary that makes Excerpt necessary
// in the first place: this repo's backlog ledgers carry accented characters
// and em-dashes (—, U+2014, 3 bytes in UTF-8), so a naive byte-slice cut
// would produce invalid UTF-8 at the exact points these cases exercise.
func TestExcerpt(t *testing.T) {
	tests := []struct {
		name          string
		s             string
		maxRunes      int
		wantExcerpt   string
		wantTruncated bool
	}{
		{
			name:          "empty string",
			s:             "",
			maxRunes:      200,
			wantExcerpt:   "",
			wantTruncated: false,
		},
		{
			name:          "shorter than maxRunes returns input unchanged",
			s:             "short description",
			maxRunes:      200,
			wantExcerpt:   "short description",
			wantTruncated: false,
		},
		{
			name:          "exactly maxRunes is not truncated",
			s:             strings.Repeat("a", 200),
			maxRunes:      200,
			wantExcerpt:   strings.Repeat("a", 200),
			wantTruncated: false,
		},
		{
			name:          "maxRunes zero with non-empty input trims everything",
			s:             "non-empty",
			maxRunes:      0,
			wantExcerpt:   "",
			wantTruncated: true,
		},
		{
			name:          "maxRunes negative with non-empty input trims everything",
			s:             "non-empty",
			maxRunes:      -5,
			wantExcerpt:   "",
			wantTruncated: true,
		},
		{
			name:          "maxRunes zero with empty input is not truncated",
			s:             "",
			maxRunes:      0,
			wantExcerpt:   "",
			wantTruncated: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotExcerpt, gotTruncated := Excerpt(tt.s, tt.maxRunes)
			if gotExcerpt != tt.wantExcerpt {
				t.Errorf("Excerpt(%q, %d) excerpt = %q, want %q", tt.s, tt.maxRunes, gotExcerpt, tt.wantExcerpt)
			}
			if gotTruncated != tt.wantTruncated {
				t.Errorf("Excerpt(%q, %d) truncated = %v, want %v", tt.s, tt.maxRunes, gotTruncated, tt.wantTruncated)
			}
			if !utf8.ValidString(gotExcerpt) {
				t.Errorf("Excerpt(%q, %d) produced invalid UTF-8: %q", tt.s, tt.maxRunes, gotExcerpt)
			}
		})
	}
}

// TestExcerpt_MultibyteBoundary is AC1: the 200th (last included) rune is an
// em-dash (U+2014, 3 bytes) and the input runs one rune past the cut point,
// so the excerpt must be produced by counting RUNES, not bytes. If Excerpt
// instead sliced by bytes (s[:200]), the multibyte character would be split
// mid-sequence and the result would fail utf8.ValidString. Asserting the
// excerpt has MORE than 200 bytes despite holding exactly 200 runes is the
// proof the cut was rune-aware: a byte-count cut could never exceed 200
// bytes for a 200-rune result.
func TestExcerpt_MultibyteBoundary(t *testing.T) {
	runes := make([]rune, 0, 201)
	for i := 0; i < 199; i++ {
		runes = append(runes, 'a')
	}
	runes = append(runes, '—') // em-dash: rune #200, 3 bytes in UTF-8.
	runes = append(runes, 'z') // rune #201: past the cut, must be dropped.
	input := string(runes)

	excerpt, truncated := Excerpt(input, 200)

	if !truncated {
		t.Fatal("expected truncated=true for a 201-rune input with maxRunes=200")
	}
	if !utf8.ValidString(excerpt) {
		t.Fatalf("excerpt is not valid UTF-8: %q", excerpt)
	}
	if got := len([]rune(excerpt)); got != 200 {
		t.Fatalf("excerpt has %d runes, want 200", got)
	}
	if len(excerpt) <= 200 {
		t.Fatalf("excerpt has %d bytes, want > 200 (proves the cut was by runes, not bytes)", len(excerpt))
	}
	if strings.ContainsRune(excerpt, 'z') {
		t.Fatal("excerpt must not contain rune #201 ('z')")
	}
}

// TestExcerpt_AccentedBoundary exercises precomposed accented characters
// (á, é — 2 bytes each in UTF-8) straddling the cut point, the other
// multibyte case this repo's ledgers actually contain (besides em-dash).
func TestExcerpt_AccentedBoundary(t *testing.T) {
	runes := make([]rune, 0, 205)
	for i := 0; i < 195; i++ {
		runes = append(runes, 'x')
	}
	runes = append(runes, 'á', 'é', 'í', 'ó', 'ú', 'ñ', 'ü', 'ç', 'à', 'è')
	input := string(runes)

	excerpt, truncated := Excerpt(input, 200)

	if !truncated {
		t.Fatal("expected truncated=true")
	}
	if !utf8.ValidString(excerpt) {
		t.Fatalf("excerpt is not valid UTF-8: %q", excerpt)
	}
	if got := len([]rune(excerpt)); got != 200 {
		t.Fatalf("excerpt has %d runes, want 200", got)
	}
}
