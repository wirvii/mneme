package quality

import (
	"reflect"
	"testing"
)

var scopeRepoFiles = []string{
	"internal/quality/mutscope.go",
	"internal/quality/measure.go",
	"internal/foo/dup.go",
	"internal/bar/dup.go",
}

// TestScopeMutants_InDiffVsOutside covers AC9's two required mitades
// (G8a/G8b): a mutant on a changed line is in-diff; the SAME mutant one
// line below, outside the changed range, is not.
func TestScopeMutants_InDiffVsOutside(t *testing.T) {
	changed := map[string][]int{"internal/quality/mutscope.go": {10, 11, 12}}
	report := &MutantReport{Mutants: []Mutant{
		{File: "internal/quality/mutscope.go", Line: 10, Column: 1, Mutator: "x", Status: MutantKilled},
		{File: "internal/quality/mutscope.go", Line: 99, Column: 1, Mutator: "x", Status: MutantKilled},
	}}

	inDiff, outside, unresolved := ScopeMutants(report, changed, scopeRepoFiles)
	if len(inDiff) != 1 || inDiff[0].Line != 10 {
		t.Fatalf("inDiff = %+v, want exactly the line-10 mutant", inDiff)
	}
	if outside != 1 {
		t.Fatalf("outside = %d, want 1", outside)
	}
	if unresolved != 0 {
		t.Fatalf("unresolved = %d, want 0", unresolved)
	}
}

// TestScopeMutants_NormalizesModulePrefixedPath covers AC9's third row:
// a mutant whose tool wrote its path with a module-path prefix still
// resolves and matches, via NormalizeSourcePath (reused verbatim).
func TestScopeMutants_NormalizesModulePrefixedPath(t *testing.T) {
	changed := map[string][]int{"internal/quality/measure.go": {5}}
	report := &MutantReport{Mutants: []Mutant{
		{File: "github.com/wirvii/mneme/internal/quality/measure.go", Line: 5, Column: 1, Mutator: "x", Status: MutantKilled},
	}}

	inDiff, outside, unresolved := ScopeMutants(report, changed, scopeRepoFiles)
	if len(inDiff) != 1 {
		t.Fatalf("inDiff = %+v, want the normalized-path mutant to match", inDiff)
	}
	if outside != 0 || unresolved != 0 {
		t.Fatalf("outside=%d unresolved=%d, want 0/0", outside, unresolved)
	}
}

// TestScopeMutants_AmbiguousSuffixNeverMatches covers AC9's fourth row and
// G9: a mutant whose suffix matches TWO repo files never counts as in-diff
// — it is unresolved, never silently attributed to the first candidate.
func TestScopeMutants_AmbiguousSuffixNeverMatches(t *testing.T) {
	changed := map[string][]int{"internal/foo/dup.go": {1}, "internal/bar/dup.go": {1}}
	report := &MutantReport{Mutants: []Mutant{
		{File: "dup.go", Line: 1, Column: 1, Mutator: "x", Status: MutantKilled},
	}}

	inDiff, outside, unresolved := ScopeMutants(report, changed, scopeRepoFiles)
	if len(inDiff) != 0 {
		t.Fatalf("inDiff = %+v, want empty — an ambiguous suffix must never match", inDiff)
	}
	if unresolved != 1 {
		t.Fatalf("unresolved = %d, want 1", unresolved)
	}
	if outside != 0 {
		t.Fatalf("outside = %d, want 0", outside)
	}
}

// TestScopeMutants_NilReport covers the degenerate nil-report input
// (Verify never constructs one before a successful parse, but ScopeMutants
// itself must not panic on it).
func TestScopeMutants_NilReport(t *testing.T) {
	inDiff, outside, unresolved := ScopeMutants(nil, nil, nil)
	if inDiff != nil || outside != 0 || unresolved != 0 {
		t.Fatalf("ScopeMutants(nil) = (%v, %d, %d), want (nil, 0, 0)", inDiff, outside, unresolved)
	}
}

// TestTally_NotViableNeverDeathNorSurvivor is G10a/AC10: not_viable is
// counted (ByStatus), but contributes to neither Survivors nor any
// "killed" interpretation — proven by comparing two tallies that differ
// ONLY in one mutant's status.
func TestTally_NotViableNeverDeathNorSurvivor(t *testing.T) {
	base := []Mutant{
		{File: "a.go", Line: 1, Mutator: "x", Status: MutantKilled},
		{File: "a.go", Line: 2, Mutator: "x", Status: MutantKilled},
		{File: "a.go", Line: 3, Mutator: "x", Status: MutantNotViable},
	}
	tally := Tally(base)
	if tally.Total != 3 {
		t.Fatalf("Total = %d, want 3", tally.Total)
	}
	if tally.ByStatus[MutantNotViable] != 1 {
		t.Fatalf("ByStatus[not_viable] = %d, want 1 (counted, not dropped)", tally.ByStatus[MutantNotViable])
	}
	if len(tally.Survivors) != 0 {
		t.Fatalf("Survivors = %+v, want empty — not_viable is not a survivor", tally.Survivors)
	}

	// Hermana: the SAME informe with the third mutant now `lived` produces
	// findings := VerdictFindings (AC10's own contrast row);
	// TestTally_NotViableNeverDeathNorSurvivor itself only asserts the
	// tally shape, the fila-2 certificate-level contrast lives in P8.
	lived := []Mutant{base[0], base[1], {File: "a.go", Line: 3, Mutator: "x", Status: MutantLived}}
	tally2 := Tally(lived)
	if len(tally2.Survivors) != 1 {
		t.Fatalf("Survivors = %+v, want exactly 1 once the third mutant is lived", tally2.Survivors)
	}
}

// TestTally_AllSixStatusesCounted covers the full vocabulary being counted
// (D1 pata c's table) — every one of the six statuses lands in ByStatus.
func TestTally_AllSixStatusesCounted(t *testing.T) {
	mutants := []Mutant{
		{File: "a.go", Line: 1, Mutator: "x", Status: MutantKilled},
		{File: "a.go", Line: 2, Mutator: "x", Status: MutantLived},
		{File: "a.go", Line: 3, Mutator: "x", Status: MutantNotViable},
		{File: "a.go", Line: 4, Mutator: "x", Status: MutantNotCovered},
		{File: "a.go", Line: 5, Mutator: "x", Status: MutantTimedOut},
		{File: "a.go", Line: 6, Mutator: "x", Status: MutantSkipped},
	}
	tally := Tally(mutants)
	for _, status := range []MutantStatus{MutantKilled, MutantLived, MutantNotViable, MutantNotCovered, MutantTimedOut, MutantSkipped} {
		if tally.ByStatus[status] != 1 {
			t.Errorf("ByStatus[%s] = %d, want 1", status, tally.ByStatus[status])
		}
	}
}

// TestTally_SurvivorOrder_AscendingDeterministic is G17/AC17's third row:
// two runs over the SAME informe produce the SAME order, verified against
// a LITERAL expected ascending order — not just "stable across two
// calls", which an inverted-but-consistent comparator would also satisfy
// (the plan's own refinement of AC17).
func TestTally_SurvivorOrder_AscendingDeterministic(t *testing.T) {
	mutants := []Mutant{
		{File: "b.go", Line: 5, Column: 1, Mutator: "z", Status: MutantLived},
		{File: "a.go", Line: 20, Column: 1, Mutator: "x", Status: MutantLived},
		{File: "a.go", Line: 10, Column: 2, Mutator: "y", Status: MutantLived},
		{File: "a.go", Line: 10, Column: 1, Mutator: "y", Status: MutantLived},
	}
	want := []string{
		"a.go:10:1:y",
		"a.go:10:2:y",
		"a.go:20:1:x",
		"b.go:5:1:z",
	}

	for i := 0; i < 2; i++ {
		tally := Tally(mutants)
		got := make([]string, len(tally.Survivors))
		for j, m := range tally.Survivors {
			got[j] = m.ID()
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d: Survivors order = %v, want %v", i, got, want)
		}
	}
}

// TestEvaluateMutation_ViabilityQuota_StrictlyGreater is G10c/AC11's
// hermana pair: exactly at the quota passes; one mutant over breaches —
// pinning the boundary, not just its direction.
func TestEvaluateMutation_ViabilityQuota_StrictlyGreater(t *testing.T) {
	// 1 of 4 not_viable = 25%. cfg.MaxNotViablePct = 25.0: AT the quota,
	// must NOT breach.
	atQuota := Tally([]Mutant{
		{File: "a.go", Line: 1, Mutator: "x", Status: MutantNotViable},
		{File: "a.go", Line: 2, Mutator: "x", Status: MutantKilled},
		{File: "a.go", Line: 3, Mutator: "x", Status: MutantKilled},
		{File: "a.go", Line: 4, Mutator: "x", Status: MutantKilled},
	})
	outcome := EvaluateMutation(atQuota, MutationThresholds{MaxNotViablePct: 25.0})
	if outcome.ViabilityBreached {
		t.Fatalf("ViabilityBreached = true at exactly the quota (25%%), want false")
	}

	// 2 of 4 not_viable = 50% > 25%: must breach.
	overQuota := Tally([]Mutant{
		{File: "a.go", Line: 1, Mutator: "x", Status: MutantNotViable},
		{File: "a.go", Line: 2, Mutator: "x", Status: MutantNotViable},
		{File: "a.go", Line: 3, Mutator: "x", Status: MutantKilled},
		{File: "a.go", Line: 4, Mutator: "x", Status: MutantKilled},
	})
	outcome2 := EvaluateMutation(overQuota, MutationThresholds{MaxNotViablePct: 25.0})
	if !outcome2.ViabilityBreached {
		t.Fatalf("ViabilityBreached = false at 50%% against a 25%% quota, want true")
	}
}

// TestEvaluateMutation_AllNotViable_IsTheCatastrophicGreen is D1 pata d's
// own scenario, at the arithmetic layer (AC11's certificate-level
// contrast lives in P8): a tally where EVERY in-diff mutant is not_viable
// has ZERO survivors yet still breaches viability — the case that must
// never read as an unqualified pass.
func TestEvaluateMutation_AllNotViable_IsTheCatastrophicGreen(t *testing.T) {
	allNotViable := Tally([]Mutant{
		{File: "a.go", Line: 1, Mutator: "x", Status: MutantNotViable},
		{File: "a.go", Line: 2, Mutator: "x", Status: MutantNotViable},
		{File: "a.go", Line: 3, Mutator: "x", Status: MutantNotViable},
	})
	if len(allNotViable.Survivors) != 0 {
		t.Fatalf("Survivors = %+v, want empty (this is exactly the trap)", allNotViable.Survivors)
	}
	outcome := EvaluateMutation(allNotViable, MutationThresholds{MaxNotViablePct: 25.0})
	if !outcome.ViabilityBreached {
		t.Fatal("ViabilityBreached = false for an all-not_viable tally — this is the catastrophic green D1 pata d exists to close")
	}
	if outcome.ViabilityPct != 100 {
		t.Fatalf("ViabilityPct = %v, want 100", outcome.ViabilityPct)
	}
}

// TestEvaluateMutation_ZeroTotal_NeverDividesByZero covers the
// zero-mutants-in-diff case at the arithmetic layer: ViabilityPct is 0,
// never NaN or a panic — mutation/scope (P8) is what turns this into a
// finding, never this function.
func TestEvaluateMutation_ZeroTotal_NeverDividesByZero(t *testing.T) {
	outcome := EvaluateMutation(MutantTally{ByStatus: map[MutantStatus]int{}}, MutationThresholds{MaxNotViablePct: 25.0})
	if outcome.ViabilityPct != 0 {
		t.Fatalf("ViabilityPct = %v, want 0 for a zero-total tally", outcome.ViabilityPct)
	}
	if outcome.ViabilityBreached {
		t.Fatal("ViabilityBreached = true for a zero-total tally, want false")
	}
}

// TestEvaluateMutation_HasTimeoutsAndNotCovered covers the two remaining
// pure fields EvaluateMutation reports.
func TestEvaluateMutation_HasTimeoutsAndNotCovered(t *testing.T) {
	tally := Tally([]Mutant{
		{File: "a.go", Line: 1, Mutator: "x", Status: MutantTimedOut},
		{File: "a.go", Line: 2, Mutator: "x", Status: MutantNotCovered},
		{File: "a.go", Line: 3, Mutator: "x", Status: MutantNotCovered},
	})
	outcome := EvaluateMutation(tally, MutationThresholds{})
	if !outcome.HasTimeouts {
		t.Error("HasTimeouts = false, want true")
	}
	if outcome.NotCoveredCount != 2 {
		t.Errorf("NotCoveredCount = %d, want 2", outcome.NotCoveredCount)
	}
}

// TestEvaluateMutation_SurvivorsTruncated covers AC18's pure half: the cap
// is read from the exported MaxSurvivorRows constant, never a literal.
func TestEvaluateMutation_SurvivorsTruncated(t *testing.T) {
	makeTally := func(survivors int) MutantTally {
		mutants := make([]Mutant, survivors)
		for i := range mutants {
			mutants[i] = Mutant{File: "a.go", Line: i + 1, Mutator: "x", Status: MutantLived}
		}
		return Tally(mutants)
	}

	atCap := EvaluateMutation(makeTally(MaxSurvivorRows), MutationThresholds{})
	if atCap.SurvivorsTruncated {
		t.Fatal("SurvivorsTruncated = true at exactly MaxSurvivorRows, want false")
	}

	overCap := EvaluateMutation(makeTally(MaxSurvivorRows+1), MutationThresholds{})
	if !overCap.SurvivorsTruncated {
		t.Fatal("SurvivorsTruncated = false at MaxSurvivorRows+1, want true")
	}
}

// TestEvaluateMutation_CarriesMaxEquivalentVerbatim covers the plumbing
// mutation/score's detail (P8) and Sign's cupo-of-record (D9/P9) both
// depend on: MaxEquivalent flows through EvaluateMutation unchanged.
func TestEvaluateMutation_CarriesMaxEquivalentVerbatim(t *testing.T) {
	outcome := EvaluateMutation(MutantTally{ByStatus: map[MutantStatus]int{}}, MutationThresholds{MaxEquivalent: 2})
	if outcome.MaxEquivalent != 2 {
		t.Fatalf("MaxEquivalent = %d, want 2", outcome.MaxEquivalent)
	}
}
