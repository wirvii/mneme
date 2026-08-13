package quality

import "sort"

// MaxSurvivorRows bounds how many `mutant/<id>` rows a single certificate
// may emit (D6). Past this many survivors the remedy is not a signature —
// it is rewriting tests — so mneme stops emitting rows and fails
// mutation/score outright, naming the real total in its summary. A
// storage cap of the REGISTRY, not a quality threshold a project tunes:
// exactly the same boundary S4's BudgetedKinds constant draws between
// "how much of this row-shape a certificate holds" and "what a project's
// constitution decides".
const MaxSurvivorRows = 50

// ScopeMutants reconciles report against changed — the spec's own
// merge-base-anchored changed lines (D3) — reusing NormalizeSourcePath
// (measure.go, S2, untouched) verbatim for the path reconciliation: the
// "a third party wrote the path its own way" problem a mutation report
// poses is identical to the one a coverage profile already posed.
//
// A mutant is IN-DIFF iff its path normalizes against repoFiles (rule 5:
// an AMBIGUOUS suffix match — one that resolves to more than one repo
// file — never counts as a match, exactly as it never does for coverage)
// AND its Line is present in changed[normalizedPath]. unresolved counts
// mutants whose path could not be normalized at all (repoFiles omits
// it, or the match was ambiguous); outside counts mutants whose path DID
// normalize but whose line was not in the changed set. Both are reported
// separately from inDiff — neither counts toward the mutation verdict,
// which judges only what THIS spec changed (D3, mirroring S2's own diff
// coverage scoping).
func ScopeMutants(report *MutantReport, changed map[string][]int, repoFiles []string) (inDiff []Mutant, outside int, unresolved int) {
	if report == nil {
		return nil, 0, 0
	}

	for _, m := range report.Mutants {
		rel, ok := NormalizeSourcePath(m.File, repoFiles)
		if !ok {
			unresolved++
			continue
		}
		if lineInChangedSet(changed[rel], m.Line) {
			inDiff = append(inDiff, m)
			continue
		}
		outside++
	}

	return inDiff, outside, unresolved
}

// lineInChangedSet reports whether line appears in lines — a small linear
// scan is deliberate: changed-line lists are the size of a single spec's
// diff (tens to low hundreds of entries), never worth a set allocation per
// file.
func lineInChangedSet(lines []int, line int) bool {
	for _, l := range lines {
		if l == line {
			return true
		}
	}
	return false
}

// MutantTally is the aritmetica's own accounting of one in-diff mutant
// set, keyed by MutantStatus (D1 pata c): Total is len(inDiff), ByStatus
// counts every status VERBATIM (including MutantNotViable and
// MutantTimedOut — counted, never silently dropped), and Survivors lists
// only the MutantLived entries, in the deterministic order D6/AC17 require
// (ascending by File, then Line, then Column, then Mutator) — the order a
// caller relies on for BOTH repeatability across two runs of the SAME
// report AND for MaxSurvivorRows' truncation to be deterministic rather
// than "whichever 50 happened to sort first this time".
type MutantTally struct {
	Total     int
	ByStatus  map[MutantStatus]int
	Survivors []Mutant
}

// Tally computes MutantTally from an already-scoped (ScopeMutants) mutant
// set. Never receives the WHOLE report — only inDiff — so a caller cannot
// accidentally judge the repository's pre-existing debt (D3/D6 of the
// grill): this function has no way to see a mutant ScopeMutants excluded.
func Tally(inDiff []Mutant) MutantTally {
	t := MutantTally{Total: len(inDiff), ByStatus: make(map[MutantStatus]int, 6)}

	for _, m := range inDiff {
		t.ByStatus[m.Status]++
		if m.Status == MutantLived {
			t.Survivors = append(t.Survivors, m)
		}
	}

	sort.Slice(t.Survivors, func(i, j int) bool {
		a, b := t.Survivors[i], t.Survivors[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Column != b.Column {
			return a.Column < b.Column
		}
		return a.Mutator < b.Mutator
	})

	return t
}

// MutationThresholds is the pure evaluation config EvaluateMutation reads
// — deliberately NARROWER than the full `[mutation]` constitution table
// (Command/ReportPath/Timeout/Format/Enabled live in the constitution's
// own MutationConfig, parsed in constitution.go, P5): the two fields here
// are the only ones a PURE function of already-tallied facts ever needs,
// and keeping them separate is what lets this file stay a pure function of
// tallied facts with no forward reference to the constitution parser that
// lands two steps later in the plan. The service layer (P8) is the one
// place that ever translates constitution.MutationConfig's two matching
// fields into this type.
type MutationThresholds struct {
	// MaxEquivalent is the absolute (never percentage, D9) cap on mutants a
	// qa-tester may sign as equivalent for ONE certificate — read here only
	// to be carried verbatim into MutationOutcome for the service layer to
	// persist in mutation/score's detail; EvaluateMutation itself never
	// enforces this cap (Sign does, at signing time, D9).
	MaxEquivalent int

	// MaxNotViablePct is the quota (D1 pata d): the proportion of in-diff
	// mutants that are MutantNotViable above which the informe is judged
	// to be talking about the mutator, not about the tests.
	MaxNotViablePct float64
}

// MutationOutcome is EvaluateMutation's pure verdict over one MutantTally
// — everything the service layer's row-assembly (P8) needs to decide
// mutation/viability, mutation/timeouts, mutation/not-covered, and
// mutation/score's own fail-vs-pass, without re-deriving any arithmetic
// itself.
type MutationOutcome struct {
	// ViabilityPct is the percentage of Total that is MutantNotViable — 0
	// when Total is 0 (the caller, never this function, is responsible for
	// treating a zero-mutant tally as mutation/scope's own finding, D1 pata
	// b's own "the empty denominator is somebody ELSE's row").
	ViabilityPct float64

	// ViabilityBreached is true iff ViabilityPct is STRICTLY GREATER than
	// cfg.MaxNotViablePct (G10c) — at exactly the quota, the check passes:
	// the guardian's hermana fixes the boundary, not just the direction.
	ViabilityBreached bool

	// HasTimeouts is true iff ByStatus[MutantTimedOut] > 0.
	HasTimeouts bool

	// NotCoveredCount is ByStatus[MutantNotCovered], carried through
	// verbatim for mutation/not-covered's informational row (D10) — never
	// itself a verdict input.
	NotCoveredCount int

	// SurvivorsTruncated is true iff len(Survivors) exceeds MaxSurvivorRows
	// — the service layer emits only the first MaxSurvivorRows (Survivors
	// is already sorted deterministically by Tally) and fails
	// mutation/score naming the REAL total (D6).
	SurvivorsTruncated bool

	// MaxEquivalent is cfg.MaxEquivalent, carried through verbatim so the
	// service layer can persist it into mutation/score's detail without
	// reading the constitution a second time (D9: `Sign` later reads the
	// cupo from THIS certificate's row, never from the file on disk).
	MaxEquivalent int
}

// EvaluateMutation is the pure arithmetic D1's four legs and D6's cap rest
// on: never counts MutantNotViable as a death (already true simply by
// Tally never adding it to Survivors — G10a is enforced by never reading
// ByStatus[MutantNotViable] as a death anywhere in this package), applies
// the viability quota with a STRICT `>` comparison (G10c), and reports
// whether the survivor list needs truncating.
func EvaluateMutation(t MutantTally, cfg MutationThresholds) MutationOutcome {
	viabilityPct := 0.0
	if t.Total > 0 {
		viabilityPct = float64(t.ByStatus[MutantNotViable]) / float64(t.Total) * 100
	}

	return MutationOutcome{
		ViabilityPct:       viabilityPct,
		ViabilityBreached:  viabilityPct > cfg.MaxNotViablePct,
		HasTimeouts:        t.ByStatus[MutantTimedOut] > 0,
		NotCoveredCount:    t.ByStatus[MutantNotCovered],
		SurvivorsTruncated: len(t.Survivors) > MaxSurvivorRows,
		MaxEquivalent:      cfg.MaxEquivalent,
	}
}
