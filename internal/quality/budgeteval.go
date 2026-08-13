// Package quality — this file implements the budget's arithmetic (SPEC-118
// EPIC-calidad S4 D4): a single pool of margin shared by the nominal
// (existing-symbol) and quota (new-symbol) halves of the contract, plus the
// radius/scope evaluator D7 declares is ONE evaluator with two callers
// (standard's `radius`, trivial's own `scope`), and the trivial lane's own
// four-check budget (D12/D13) — the same machine as standard, with a
// different source of limits. Everything here is pure: no I/O, no git, no
// graph database.
package quality

import (
	"fmt"
	"sort"
)

// DirCount is one directory's quota bookkeeping in a BudgetOutcome: how many
// new symbols it was budgeted, and how many were actually delivered
// (regardless of whether the quota covered all of them).
type DirCount struct {
	Budgeted  int
	Delivered int
}

// BudgetOutcome is EvaluateBudget's result — the three figures D3 of the
// grill requires ALWAYS travel together (Budgeted/Revised/Delivered), plus
// the single margin/overrun pair D4 of this spec collapses both halves of
// the contract into.
type BudgetOutcome struct {
	// Budgeted is the ORIGINAL quota total (sum of Budget.Quota's
	// MaxNewSymbols) — never the revised one, so a revision's widening is
	// always visible as a diff against this number.
	Budgeted int

	// Revised is nil when Budget.Revision is nil (the common, unrevised
	// case) — never a synonym for Budgeted.
	Revised *int

	// Delivered is the total number of created-or-modified symbols this
	// spec presents — informational; the pass/fail decision is Overrun vs
	// Margin, never a direct function of Delivered-Budgeted (a per-directory
	// quota can be exceeded in one place and slack in another).
	Delivered int

	// Margin is the EFFECTIVE margin: Budget.Revision.Margin when a
	// revision is present, Budget.Margin otherwise.
	Margin int

	// Overrun is len(Uncovered) — the single pool D4 charges both halves
	// of the contract against.
	Overrun int

	// ByDir is keyed by quota directory (the EFFECTIVE quota table: revised
	// when present, original otherwise).
	ByDir map[string]DirCount

	// Uncovered lists every created symbol whose directory's quota was
	// already exhausted, plus every modified symbol whose (file, name) pair
	// is not declared in [[modify]] — the exact set the excess is made of.
	Uncovered []Symbol

	// Pass is true iff Overrun <= Margin (G12a/G12b) — the comparison is
	// on the MARGIN side, never `Delivered > Budgeted` (which would ignore
	// margin entirely) and never a `>=`/`<` inversion (which would admit
	// one excess symbol too many).
	Pass bool
}

// sumQuota totals MaxNewSymbols across quotas.
func sumQuota(quotas []Quota) int {
	total := 0
	for _, q := range quotas {
		total += q.MaxNewSymbols
	}
	return total
}

// EvaluateBudget is the pure heart of D4: a single bag of margin, imputed
// deterministically (created symbols sorted by Key, so the same delta always
// produces the same Uncovered set regardless of map iteration order), shared
// by the quota half (new symbols) and the nominal half (existing symbols
// declared in [[modify]]). Moved and Deleted symbols never consume anything
// (D4).
func EvaluateBudget(delta SymbolDelta, b *Budget) BudgetOutcome {
	effectiveQuotas := b.Quota
	effectiveMargin := b.Margin
	var revised *int
	if b.Revision != nil {
		rt := sumQuota(b.Revision.Quota)
		revised = &rt
		effectiveQuotas = b.Revision.Quota
		effectiveMargin = b.Revision.Margin
	}

	remaining := make(map[string]int, len(effectiveQuotas))
	byDir := make(map[string]DirCount, len(effectiveQuotas))
	for _, q := range effectiveQuotas {
		remaining[q.Dir] = q.MaxNewSymbols
		byDir[q.Dir] = DirCount{Budgeted: q.MaxNewSymbols}
	}

	modifySet := make(map[string]bool, len(b.Modify))
	for _, m := range b.Modify {
		modifySet[m.File+":"+m.Symbol] = true
	}

	created := make([]Symbol, len(delta.Created))
	copy(created, delta.Created)
	sort.Slice(created, func(i, j int) bool { return created[i].Key < created[j].Key })

	var uncovered []Symbol
	for _, s := range created {
		dc := byDir[s.Dir]
		dc.Delivered++
		byDir[s.Dir] = dc

		if remaining[s.Dir] > 0 {
			remaining[s.Dir]--
			continue
		}
		uncovered = append(uncovered, s)
	}

	for _, s := range delta.Modified {
		if !modifySet[s.File+":"+s.QualifiedName] {
			uncovered = append(uncovered, s)
		}
	}

	return BudgetOutcome{
		Budgeted:  sumQuota(b.Quota),
		Revised:   revised,
		Delivered: len(delta.Created) + len(delta.Modified),
		Margin:    effectiveMargin,
		Overrun:   len(uncovered),
		ByDir:     byDir,
		Uncovered: uncovered,
		Pass:      len(uncovered) <= effectiveMargin,
	}
}

// EvaluateRadius is the evaluator SHARED by standard's `radius` and
// trivial's own `scope` (D7): both answer the same question — which
// changed paths fall outside the declared globs — so there is exactly one
// implementation, never a "provisional" second one for trivial. It NEVER
// participates in the margin pool (G14): a fichero fuera de radio is a
// design miss, not a quantity to forgive.
func EvaluateRadius(changedFiles, globs []string) []string {
	var outside []string
	for _, f := range changedFiles {
		if !MatchGlobs(f, globs) {
			outside = append(outside, f)
		}
	}
	return outside
}

// Breach is a single human-readable trivial-lane threshold violation — a
// named string type, not a bare string, so a caller cannot accidentally
// pass an unrelated string where a Breach is expected.
type Breach string

// TrivialBudget is the trivial lane's own budget: fixed limits on file
// count and total changed lines, plus the paths a trivial-lane spec must
// never touch (D12/D13).
type TrivialBudget struct {
	MaxFiles       int
	MaxLines       int
	ForbiddenGlobs []string
}

// DefaultTrivialBudget is the DEFINITION of the trivial lane (D13) — 3
// files, 20 lines, and the five forbidden-path globs migrated verbatim from
// lane.forbiddenGlobs. It lives in the BINARY, not the constitution: these
// numbers are mneme's own vocabulary for what "trivial" means in the SDD,
// documented in CLAUDE.md and docs/lanes.md — not a per-project threshold a
// team calibrates (that distinction is D13 of the grill's own boundary,
// and it is also what keeps `lane audit` working in a repository with no
// constitution at all).
var DefaultTrivialBudget = TrivialBudget{
	MaxFiles: 3,
	MaxLines: 20,
	ForbiddenGlobs: []string{
		"**/*.sql",
		"**/migrations/**",
		"**/schema.*",
		"cmd/**",
		"internal/install/assets/**",
	},
}

// EvaluateTrivialBudget is the trivial lane's own form of the budget
// evaluator (D12): the SAME four checks lane.Audit used to compute, now
// expressed over quality's own FileStat/SymbolDelta types, with the EXACT
// SAME breach texts (P4 point 4) — those strings are concatenated verbatim
// into lane_audits.Breaches and read back by LaneStatus, so changing them
// would break every historical audit's readability.
func EvaluateTrivialBudget(files []FileStat, symDelta SymbolDelta, scope string, b TrivialBudget) []Breach {
	var breaches []Breach

	if len(files) > b.MaxFiles {
		breaches = append(breaches, Breach(fmt.Sprintf("file count %d exceeds trivial limit of %d", len(files), b.MaxFiles)))
	}

	lines := 0
	for _, f := range files {
		lines += f.Added + f.Removed
	}
	if lines > b.MaxLines {
		breaches = append(breaches, Breach(fmt.Sprintf("line count %d exceeds trivial limit of %d", lines, b.MaxLines)))
	}

	for _, f := range files {
		if MatchGlobs(f.Path, b.ForbiddenGlobs) {
			breaches = append(breaches, Breach(fmt.Sprintf("forbidden path modified: %s", f.Path)))
		}
		if scope != "" && !MatchGlobs(f.Path, []string{scope}) {
			breaches = append(breaches, Breach(fmt.Sprintf("out of scope: %s", f.Path)))
		}
	}

	for _, s := range symDelta.Created {
		if s.Exported {
			breaches = append(breaches, Breach(fmt.Sprintf("public symbol changed: %s in %s", s.QualifiedName, s.File)))
		}
	}
	for _, s := range symDelta.Deleted {
		if s.Exported {
			breaches = append(breaches, Breach(fmt.Sprintf("public symbol changed: %s in %s", s.QualifiedName, s.File)))
		}
	}

	return breaches
}
