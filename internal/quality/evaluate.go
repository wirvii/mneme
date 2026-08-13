// Package quality — this file implements the PURE evaluator for a criteria
// document's assertions (SPEC-117 EPIC-calidad S3 D3/D5): given a ref's
// already-collected tree facts, decide whether an assertion holds, whether
// a criterion passed/failed/turned out vacuous, and whether a manual or
// command quota is breached. Nothing in this file touches disk, a
// subprocess, or git — every fact it needs (a file listing, a resolved
// path->line-count map) is collected once by the caller (internal/service)
// and handed in, which is what lets D5's vacuity rule be proven with
// tables instead of a git fixture per row.
package quality

import (
	"fmt"

	"github.com/bmatcuk/doublestar/v4"
)

// Outcome is a criterion's classification after evaluating its full
// conjunction of assertions against HEAD and, when knowable, the spec's
// base commit (D5).
type Outcome string

const (
	// OutcomePass means the criterion did not hold at base but does at
	// HEAD — it distinguishes the before from the after.
	OutcomePass Outcome = "pass"

	// OutcomeFail means the criterion does not hold at HEAD. Base is never
	// even consulted (D5: "no -> fail. Fin.").
	OutcomeFail Outcome = "fail"

	// OutcomeVacuous means the criterion held ALREADY at base — it proves
	// nothing about the work done, and costs a signature (D5).
	OutcomeVacuous Outcome = "vacuous"

	// OutcomeAnchorNotNew means an assertion declared new=true but its
	// anchor (Path, or a file matched by In/DefinedIn) already existed at
	// base — the author's declaration and the repository disagree (D7).
	OutcomeAnchorNotNew Outcome = "anchor-not-new"

	// OutcomeBaseUnknown means the criterion holds at HEAD but the base
	// commit could not be determined (no BaseSHA, or an unreachable
	// merge-base) — NEVER reported as pass: "could not check" and "checked
	// and it's fine" must never share a status (D5).
	OutcomeBaseUnknown Outcome = "base-unknown"
)

// TreeFacts is everything one ref's evaluation needs, collected ONCE by
// the caller via ListFilesAtRef/GrepLinesAtRef and handed in — this file
// never re-derives it and never calls git itself.
type TreeFacts struct {
	// Files is the ref's complete file listing (ListFilesAtRef output).
	Files []string

	// Matches maps MatchKey(needle, word) to GrepLinesAtRef's own
	// path->line-count result for that EXACT (needle, word) pair on this
	// ref — memoized by the caller so two assertions searching the same
	// needle never pay for two git invocations (P7's own dependency note).
	Matches map[string]map[string]int
}

// MatchKey builds TreeFacts.Matches' lookup key for a given needle/word
// pair. Exported so the code collecting TreeFacts (internal/service) and
// the code reading it (this file) can never invent two different key
// formats that quietly stop matching each other.
func MatchKey(needle string, word bool) string {
	if word {
		return "w:" + needle
	}
	return "s:" + needle
}

// MatchGlobs reports whether path matches at least one of globs — the
// ONLY place in this mechanism a doublestar glob is evaluated against a
// path (D4): git itself is NEVER handed a pathspec; its raw tree/grep
// output is filtered here, in Go, so the same filter runs identically for
// HEAD and for base.
func MatchGlobs(path string, globs []string) bool {
	for _, g := range globs {
		if ok, _ := doublestar.Match(g, path); ok {
			return true
		}
	}
	return false
}

// EvaluateAssertion evaluates a SINGLE assertion against one ref's facts:
// files is that ref's complete file listing; matches is the
// ALREADY-RESOLVED path->line-count map for THIS assertion's own exact
// (needle, word) pair on that SAME ref (nil/empty when nothing matched at
// all — a valid, common state, never an error). Pure: no I/O, so every row
// of AC11-AC15 is a table entry, never a git fixture.
func EvaluateAssertion(a Assertion, files []string, matches map[string]int) (bool, string) {
	switch a.Verb {
	case VerbFileExists:
		for _, f := range files {
			if f == a.Path {
				return true, fmt.Sprintf("%s existe", a.Path)
			}
		}
		return false, fmt.Sprintf("%s no existe", a.Path)

	case VerbPatternCount:
		count := sumMatchesInGlobs(a.In, matches)
		ok := compareCount(a.Comparator, count, a.Count)
		return ok, fmt.Sprintf("%d lineas contienen %q en %v (quiere %s %d)", count, a.Contains, a.In, a.Comparator, a.Count)

	case VerbSymbolDefined:
		for f, n := range matches {
			if n > 0 && MatchGlobs(f, a.In) {
				return true, fmt.Sprintf("%s definido en %s", a.Symbol, f)
			}
		}
		return false, fmt.Sprintf("%s no aparece como palabra completa en ningun fichero que case %v", a.Symbol, a.In)

	case VerbSymbolReferenced:
		for f, n := range matches {
			if n == 0 || MatchGlobs(f, a.DefinedIn) || MatchGlobs(f, a.Ignore) {
				continue
			}
			return true, fmt.Sprintf("%s referenciado en %s (fuera de defined_in/ignore)", a.Symbol, f)
		}
		return false, fmt.Sprintf("%s solo aparece en ficheros de defined_in/ignore, o en ninguno", a.Symbol)

	default:
		return false, fmt.Sprintf("verbo desconocido %q", a.Verb)
	}
}

// sumMatchesInGlobs sums matches' line counts over only the files that
// match at least one of globs — pattern_count's own "count across the
// files In selects" semantics (D3), the filter always applied in Go, never
// passed to git as a pathspec (D4).
func sumMatchesInGlobs(globs []string, matches map[string]int) int {
	total := 0
	for f, n := range matches {
		if MatchGlobs(f, globs) {
			total += n
		}
	}
	return total
}

// compareCount applies Comparator to (got, want) — the three-value closed
// set ParseCriteria already validated at parse time.
func compareCount(cmp Comparator, got, want int) bool {
	switch cmp {
	case ComparatorGTE:
		return got >= want
	case ComparatorLTE:
		return got <= want
	case ComparatorEQ:
		return got == want
	default:
		return false
	}
}

// anchorPreexistedAtBase reports whether a's ANCHOR — file_exists's Path,
// or a file matching pattern_count/symbol_defined's In or
// symbol_referenced's DefinedIn — already existed in baseFiles (D7 point
// 2). This is a question about the anchor's LOCATION only, never about
// whether the searched content matched there — a new=true promise is about
// the anchor coming into existence, not about the assertion's overall
// truth at base (which evaluateAllAssertions checks separately, and which
// is how OutcomeVacuous gets classified when no promise was broken).
func anchorPreexistedAtBase(a Assertion, baseFiles []string) bool {
	switch a.Verb {
	case VerbFileExists:
		for _, f := range baseFiles {
			if f == a.Path {
				return true
			}
		}
		return false
	case VerbPatternCount, VerbSymbolDefined:
		for _, f := range baseFiles {
			if MatchGlobs(f, a.In) {
				return true
			}
		}
		return false
	case VerbSymbolReferenced:
		for _, f := range baseFiles {
			if MatchGlobs(f, a.DefinedIn) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// matchesFor extracts the specific matches submap assertion a needs out of
// facts.Matches, using the SAME MatchKey the caller collecting facts must
// have used. symbol_defined/symbol_referenced always search as a whole
// word (there is no `word` key for those verbs — Word is pattern_count's
// own declared field, D2's prohibited-keys table).
func matchesFor(facts TreeFacts, a Assertion) map[string]int {
	switch a.Verb {
	case VerbPatternCount:
		return facts.Matches[MatchKey(a.Contains, a.Word)]
	case VerbSymbolDefined, VerbSymbolReferenced:
		return facts.Matches[MatchKey(a.Symbol, true)]
	default:
		return nil
	}
}

// evaluateAllAssertions evaluates every assertion in asserts against facts
// and ANDs the results — a criterion's assertions are a conjunction (D2):
// the first assertion that does not hold names itself in the returned
// reason.
func evaluateAllAssertions(asserts []Assertion, facts TreeFacts) (bool, string) {
	for i, a := range asserts {
		ok, why := EvaluateAssertion(a, facts.Files, matchesFor(facts, a))
		if !ok {
			return false, fmt.Sprintf("assert[%d]: %s", i, why)
		}
	}
	return true, "todas las aserciones se cumplen"
}

// EvaluateCriterion classifies a MODE-ASSERT criterion's outcome (D5),
// evaluating its full conjunction of assertions against head, and — only
// when head already holds — against base, applying D7's anchor-not-new
// check FIRST so a broken "new" promise is reported as that specific,
// more actionable finding rather than as a generic vacuous (a criterion
// whose new=true anchor turns out to have preexisted at base is ALSO,
// trivially, satisfied at base — anchor-not-new is the more precise
// diagnosis of why). baseKnown false means no BaseSHA, or an unreachable
// merge-base — reported as OutcomeBaseUnknown, never OutcomePass.
func EvaluateCriterion(c Criterion, head, base TreeFacts, baseKnown bool) (Outcome, string) {
	headOK, headWhy := evaluateAllAssertions(c.Assert, head)
	if !headOK {
		return OutcomeFail, headWhy
	}

	if !baseKnown {
		return OutcomeBaseUnknown, "el commit base es desconocido, o su merge-base es inalcanzable"
	}

	for i, a := range c.Assert {
		if !a.New {
			continue
		}
		if anchorPreexistedAtBase(a, base.Files) {
			return OutcomeAnchorNotNew, fmt.Sprintf("assert[%d]: new=true pero el anclaje ya existia en el commit base", i)
		}
	}

	baseOK, _ := evaluateAllAssertions(c.Assert, base)
	if baseOK {
		return OutcomeVacuous, "el criterio ya se cumplia en el commit base — no prueba nada"
	}
	return OutcomePass, headWhy
}

// CheckQuota reports the percentage n represents of total, and whether it
// STRICTLY exceeds maxPct (D10) — strict, not >=, so max_manual_pct=25.0
// with 4 total criteria admits EXACTLY one manual (25.0 is not > 25.0). A
// total of 0 never breaches (there is nothing to have a proportion of);
// ParseCriteria already rejects a zero-criteria document before this is
// ever called in practice.
func CheckQuota(n, total int, maxPct float64) (pct float64, breached bool) {
	if total == 0 {
		return 0, false
	}
	pct = 100 * float64(n) / float64(total)
	return pct, pct > maxPct
}
