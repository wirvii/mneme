package scoring

import "regexp"

// nonAlphanumRe splits text into tokens on any non-alphanumeric boundary.
// This handles topic-key separators (/, -) as well as whitespace.
var nonAlphanumRe = regexp.MustCompile(`[^a-z0-9]+`)

// jaccardStopWords is a conservative ES+EN list of function words that never
// carry meaning in a topic key. Kept separate from the FTS5 stop-word list in
// store/search.go because the two lists serve different purposes:
// FTS5 stopwords affect query parsing; Jaccard stopwords affect similarity scoring.
//
// The list is intentionally small (40 words): topic keys are short (2–5 tokens)
// and an aggressive list would eliminate too much signal.
var jaccardStopWords = map[string]bool{
	// English function words
	"a": true, "an": true, "the": true, "of": true, "to": true,
	"in": true, "on": true, "by": true, "for": true, "and": true,
	"or": true, "with": true, "is": true, "are": true, "was": true,
	"be": true, "not": true, "no": true, "it": true, "its": true,
	"as": true, "at": true, "from": true, "this": true, "that": true,
	// Spanish function words
	"el": true, "la": true, "los": true, "las": true, "de": true,
	"en": true, "y": true, "o": true, "un": true, "una": true,
	"del": true, "al": true, "con": true, "por": true, "para": true,
	"es": true, "son": true, "que": true, "se": true, "su": true,
}

// Tokenize splits text into a set of normalised tokens suitable for Jaccard
// similarity comparison. It lowercases, splits on non-alphanumeric boundaries,
// removes stopwords, and discards tokens shorter than 2 characters.
//
// The result is a set (map[string]bool) to eliminate duplicates, because
// Jaccard operates on sets, not multisets. A minimum length of 2 (not 3) is
// used to preserve short-but-meaningful tokens like "db", "ui", "go", "ci".
//
// Accented characters are stripped because topic keys in mneme are ASCII slugs.
// This means "resolución" and "resolucion" tokenise identically to "resolucin",
// which is acceptable for our use case.
func Tokenize(text string) map[string]bool {
	if text == "" {
		return map[string]bool{}
	}

	lower := make([]byte, len(text))
	for i := range len(text) {
		c := text[i]
		if c >= 'A' && c <= 'Z' {
			lower[i] = c + 32
		} else {
			lower[i] = c
		}
	}

	parts := nonAlphanumRe.Split(string(lower), -1)
	set := make(map[string]bool, len(parts))
	for _, tok := range parts {
		if len(tok) < 2 {
			continue
		}
		if jaccardStopWords[tok] {
			continue
		}
		set[tok] = true
	}
	return set
}

// JaccardSimilarity computes the Jaccard index between two token sets.
// Returns 0.0 when both sets are empty (no signal), and a value in [0.0, 1.0]
// otherwise. Higher values indicate stronger overlap.
//
//	J(A, B) = |A ∩ B| / |A ∪ B|
func JaccardSimilarity(a, b map[string]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0.0
	}

	// Iterate over the smaller set for efficiency.
	small, large := a, b
	if len(a) > len(b) {
		small, large = b, a
	}

	intersection := 0
	for tok := range small {
		if large[tok] {
			intersection++
		}
	}

	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0.0
	}
	return float64(intersection) / float64(union)
}
