// Package conflicts provides deterministic candidate detection and LLM-based
// judgment of memory conflict pairs.
//
// Design: detection (this file) is purely deterministic using FTS5 term
// extraction. Judgment (judge.go) is semantic and uses the local Claude CLI
// subprocess. The two responsibilities are intentionally kept separate to
// preserve the invariant that only the judgment step involves an LLM.
//
// This package is a leaf: it imports only stdlib and has no dependencies on
// internal/model or internal/store. The service layer owns the integration.
package conflicts

import (
	"sort"
	"strings"
)

// stopWords is the set of common English words excluded from candidate query
// construction. Matches the set used in internal/store/search.go so that FTS5
// term extraction is consistent across both search paths.
var stopWords = map[string]bool{
	"a": true, "an": true, "the": true, "is": true, "are": true, "was": true,
	"were": true, "be": true, "been": true, "being": true, "have": true,
	"has": true, "had": true, "do": true, "does": true, "did": true,
	"will": true, "would": true, "could": true, "should": true, "may": true,
	"might": true, "can": true, "shall": true, "to": true, "of": true,
	"in": true, "for": true, "on": true, "with": true, "at": true, "by": true,
	"from": true, "as": true, "into": true, "through": true, "during": true,
	"before": true, "after": true, "above": true, "below": true,
	"between": true, "out": true, "off": true, "over": true, "under": true,
	"again": true, "further": true, "then": true, "once": true, "here": true,
	"there": true, "when": true, "where": true, "why": true, "how": true,
	"all": true, "each": true, "every": true, "both": true, "few": true,
	"more": true, "most": true, "other": true, "some": true, "such": true,
	"no": true, "nor": true, "not": true, "only": true, "own": true,
	"same": true, "so": true, "than": true, "too": true, "very": true,
	"just": true, "because": true, "but": true, "and": true, "or": true,
	"if": true, "while": true, "about": true, "up": true, "it": true,
	"its": true, "this": true, "that": true, "these": true, "those": true,
	"i": true, "me": true, "my": true, "we": true, "our": true, "he": true,
	"she": true, "they": true, "them": true, "their": true, "what": true,
	"which": true, "who": true, "whom": true, "use": true, "used": true,
	"using": true, "also": true, "new": true, "make": true, "made": true,
}

// ExtractSalientTerms extracts up to maxTerms unique, non-stop-word tokens
// from the combined title and content. Tokens are normalised to lowercase,
// sorted by length descending (longer terms are more specific), and then
// deduplicated. The result is deterministic for the same input.
func ExtractSalientTerms(title, content string, maxTerms int) []string {
	combined := title + " " + content

	// Tokenise: split on whitespace and strip common punctuation.
	rawTokens := strings.Fields(combined)

	seen := make(map[string]bool, len(rawTokens))
	var kept []string

	for _, tok := range rawTokens {
		// Strip leading/trailing punctuation so "tokens," → "tokens".
		clean := strings.Trim(tok, `.,;:!?()[]{}'"` + "`")
		lower := strings.ToLower(clean)

		// Skip empty, stop words, very short tokens (≤2 chars), and duplicates.
		if lower == "" || len(lower) <= 2 || stopWords[lower] || seen[lower] {
			continue
		}
		seen[lower] = true
		kept = append(kept, lower)
	}

	// Sort by token length descending so more specific (longer) terms appear
	// first in the FTS query, improving candidate precision.
	sort.Slice(kept, func(i, j int) bool {
		if len(kept[i]) != len(kept[j]) {
			return len(kept[i]) > len(kept[j])
		}
		// Tie-break alphabetically for determinism.
		return kept[i] < kept[j]
	})

	if maxTerms > 0 && len(kept) > maxTerms {
		kept = kept[:maxTerms]
	}

	return kept
}

// BuildCandidateQuery converts a slice of salient terms into an FTS5 OR query
// where each term is wrapped in double-quotes to neutralise operator characters.
// An empty terms slice returns an empty string. The result is deterministic.
func BuildCandidateQuery(terms []string) string {
	if len(terms) == 0 {
		return ""
	}

	quoted := make([]string, len(terms))
	for i, term := range terms {
		// Escape embedded double-quotes by doubling them (FTS5 quoting rule).
		escaped := strings.ReplaceAll(term, `"`, `""`)
		quoted[i] = `"` + escaped + `"`
	}

	return strings.Join(quoted, " OR ")
}
