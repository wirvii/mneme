package quality

import "testing"

// TestEvaluateAssertion_FileExists covers file_exists's two rows.
func TestEvaluateAssertion_FileExists(t *testing.T) {
	files := []string{"a.go", "b.go"}

	ok, _ := EvaluateAssertion(Assertion{Verb: VerbFileExists, Path: "a.go"}, files, nil)
	if !ok {
		t.Error("file_exists(a.go) = false, want true")
	}

	ok, _ = EvaluateAssertion(Assertion{Verb: VerbFileExists, Path: "missing.go"}, files, nil)
	if ok {
		t.Error("file_exists(missing.go) = true, want false")
	}
}

// TestEvaluateAssertion_PatternCount covers AC13: the three comparators,
// with count=0 included — the "no queda ninguna llamada a la API vieja"
// criterion's own shape.
func TestEvaluateAssertion_PatternCount(t *testing.T) {
	tests := []struct {
		name    string
		cmp     Comparator
		count   int
		matches map[string]int
		want    bool
	}{
		{"gte 1 with 3 lines", ComparatorGTE, 1, map[string]int{"a.go": 3}, true},
		{"gte 1 with 0 lines", ComparatorGTE, 1, map[string]int{}, false},
		{"lte 2 with 3 lines", ComparatorLTE, 2, map[string]int{"a.go": 3}, false},
		{"lte 2 with 2 lines", ComparatorLTE, 2, map[string]int{"a.go": 2}, true},
		{"eq 0 with 0 lines", ComparatorEQ, 0, map[string]int{}, true},
		{"eq 0 with 1 line", ComparatorEQ, 0, map[string]int{"a.go": 1}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := Assertion{Verb: VerbPatternCount, In: []string{"*.go"}, Comparator: tt.cmp, Count: tt.count}
			ok, _ := EvaluateAssertion(a, nil, tt.matches)
			if ok != tt.want {
				t.Errorf("EvaluateAssertion(%s) = %v, want %v", tt.name, ok, tt.want)
			}
		})
	}
}

// TestEvaluateAssertion_GlobFiltersOutput covers AC15: the same matches
// hold, but filtering by In changes the outcome depending on which glob is
// declared.
func TestEvaluateAssertion_GlobFiltersOutput(t *testing.T) {
	matches := map[string]int{"internal/a/x.go": 1}

	a := Assertion{Verb: VerbPatternCount, In: []string{"internal/a/**"}, Comparator: ComparatorGTE, Count: 1}
	ok, _ := EvaluateAssertion(a, nil, matches)
	if !ok {
		t.Error("EvaluateAssertion(in=internal/a/**) = false, want true (the match is inside internal/a)")
	}

	b := Assertion{Verb: VerbPatternCount, In: []string{"internal/b/**"}, Comparator: ComparatorGTE, Count: 1}
	ok, _ = EvaluateAssertion(b, nil, matches)
	if ok {
		t.Error("EvaluateAssertion(in=internal/b/**) = true, want false (the SAME match is outside internal/b)")
	}

	// The doubly-nested ** property path.Match lacks (D4's reason for
	// doublestar).
	nested := map[string]int{"internal/deep/nested/x.go": 1}
	c := Assertion{Verb: VerbPatternCount, In: []string{"internal/**/*.go"}, Comparator: ComparatorGTE, Count: 1}
	ok, _ = EvaluateAssertion(c, nil, nested)
	if !ok {
		t.Error("EvaluateAssertion(in=internal/**/*.go) = false, want true (doublestar matches a two-level-deep file)")
	}
}

// TestEvaluateAssertion_SymbolDefined_Word covers AC12: -w is load-bearing
// — Foo must not match inside FooBar.
func TestEvaluateAssertion_SymbolDefined_Word(t *testing.T) {
	onlyFooBar := map[string]int{"a.go": 1} // caller resolved via -w for "Foo"; FooBar alone never counts here
	// Simulate: a real GrepLinesAtRef(ref, "Foo", word=true) against a tree
	// containing only "FooBar" would return an EMPTY map (no whole-word
	// match) — the guardian this test protects is exactly that emptiness.
	empty := map[string]int{}

	a := Assertion{Verb: VerbSymbolDefined, Symbol: "Foo", In: []string{"*.go"}}
	ok, _ := EvaluateAssertion(a, nil, empty)
	if ok {
		t.Error("symbol_defined(Foo) against a tree with only FooBar (empty word-matches) = true, want false")
	}

	ok, _ = EvaluateAssertion(a, nil, onlyFooBar)
	if !ok {
		t.Error("symbol_defined(Foo) with a real whole-word match = false, want true")
	}
}

// TestEvaluateAssertion_SymbolReferenced covers AC14: the insignia
// detection — a symbol referenced ONLY by test files is dead code.
func TestEvaluateAssertion_SymbolReferenced(t *testing.T) {
	tests := []struct {
		name      string
		matches   map[string]int
		definedIn []string
		ignore    []string
		want      bool
	}{
		{
			name:      "only referenced from a test file, ignored -> not satisfied",
			matches:   map[string]int{"foo_test.go": 1},
			definedIn: []string{"foo.go"},
			ignore:    []string{"**/*_test.go"},
			want:      false,
		},
		{
			name:      "referenced from a test file, ignore empty -> satisfied (the test counts)",
			matches:   map[string]int{"foo_test.go": 1},
			definedIn: []string{"foo.go"},
			ignore:    []string{},
			want:      true,
		},
		{
			name:      "referenced from production code too -> satisfied",
			matches:   map[string]int{"foo_test.go": 1, "bar.go": 1},
			definedIn: []string{"foo.go"},
			ignore:    []string{"**/*_test.go"},
			want:      true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := Assertion{Verb: VerbSymbolReferenced, Symbol: "Foo", DefinedIn: tt.definedIn, Ignore: tt.ignore}
			ok, _ := EvaluateAssertion(a, nil, tt.matches)
			if ok != tt.want {
				t.Errorf("EvaluateAssertion(%s) = %v, want %v", tt.name, ok, tt.want)
			}
		})
	}
}

// TestEvaluateCriterion_Vacuity covers AC16: the three branches of D5's
// table, all mandatory — without the pass row a guardian marking
// everything vacuous would pass; without the vacuous row one marking
// nothing vacuous would pass.
func TestEvaluateCriterion_Vacuity(t *testing.T) {
	crit := Criterion{
		ID:   "AC1",
		Mode: ModeAssert,
		Assert: []Assertion{
			{Verb: VerbFileExists, Path: "new.go", New: true},
		},
	}

	// Row 1: holds at HEAD, not at base -> pass.
	head := TreeFacts{Files: []string{"new.go", "old.go"}}
	base := TreeFacts{Files: []string{"old.go"}}
	outcome, _ := EvaluateCriterion(crit, head, base, true)
	if outcome != OutcomePass {
		t.Errorf("outcome = %s, want pass", outcome)
	}

	// Row 2: does not hold at HEAD -> fail (base never consulted).
	headFail := TreeFacts{Files: []string{"old.go"}}
	outcome, _ = EvaluateCriterion(crit, headFail, base, true)
	if outcome != OutcomeFail {
		t.Errorf("outcome = %s, want fail", outcome)
	}

	// Row 3: holds at BOTH -> vacuous (declared new=false here, so
	// anchor-not-new does not preempt the classification).
	vacuousCrit := Criterion{
		ID:   "AC1",
		Mode: ModeAssert,
		Assert: []Assertion{
			{Verb: VerbFileExists, Path: "old.go", New: false},
		},
	}
	bothHave := TreeFacts{Files: []string{"old.go"}}
	outcome, _ = EvaluateCriterion(vacuousCrit, bothHave, bothHave, true)
	if outcome != OutcomeVacuous {
		t.Errorf("outcome = %s, want vacuous", outcome)
	}
}

// TestEvaluateCriterion_AnchorNotNew covers AC18: a new=true anchor that
// turns out to have preexisted at base is reported as the specific
// anchor-not-new finding, not a generic vacuous — and a genuinely new
// anchor is a clean pass.
func TestEvaluateCriterion_AnchorNotNew(t *testing.T) {
	crit := Criterion{
		ID:     "AC1",
		Mode:   ModeAssert,
		Assert: []Assertion{{Verb: VerbFileExists, Path: "lied.go", New: true}},
	}

	// The anchor ALREADY existed at base despite new=true.
	head := TreeFacts{Files: []string{"lied.go"}}
	baseHadIt := TreeFacts{Files: []string{"lied.go"}}
	outcome, _ := EvaluateCriterion(crit, head, baseHadIt, true)
	if outcome != OutcomeAnchorNotNew {
		t.Errorf("outcome = %s, want anchor-not-new", outcome)
	}

	// new=false on the SAME shape never triggers this finding.
	honestCrit := Criterion{
		ID:     "AC1",
		Mode:   ModeAssert,
		Assert: []Assertion{{Verb: VerbFileExists, Path: "lied.go", New: false}},
	}
	outcome, _ = EvaluateCriterion(honestCrit, head, baseHadIt, true)
	if outcome == OutcomeAnchorNotNew {
		t.Error("outcome = anchor-not-new with new=false, want anything else (that finding only applies to new=true)")
	}

	// A genuinely new anchor -> pass.
	baseDidNotHaveIt := TreeFacts{Files: []string{"unrelated.go"}}
	outcome, _ = EvaluateCriterion(crit, head, baseDidNotHaveIt, true)
	if outcome != OutcomePass {
		t.Errorf("outcome = %s, want pass (anchor genuinely did not exist at base)", outcome)
	}
}

// TestEvaluateCriterion_BaseUnknown covers AC19: base-unknown is reported
// even though HEAD holds — never pass, never skipped.
func TestEvaluateCriterion_BaseUnknown(t *testing.T) {
	crit := Criterion{
		ID:     "AC1",
		Mode:   ModeAssert,
		Assert: []Assertion{{Verb: VerbFileExists, Path: "a.go", New: false}},
	}
	head := TreeFacts{Files: []string{"a.go"}}
	outcome, _ := EvaluateCriterion(crit, head, TreeFacts{}, false)
	if outcome != OutcomeBaseUnknown {
		t.Errorf("outcome = %s, want base-unknown", outcome)
	}

	// HEAD failing still reports fail, never base-unknown — base status is
	// irrelevant once HEAD itself does not hold.
	headFail := TreeFacts{Files: []string{}}
	outcome, _ = EvaluateCriterion(crit, headFail, TreeFacts{}, false)
	if outcome != OutcomeFail {
		t.Errorf("outcome = %s, want fail even with an unknown base", outcome)
	}
}

// TestCheckQuota covers D10's arithmetic: strict comparison, and the
// small-N example the constitution's own template documents (25.0 with 4
// admits exactly one).
func TestCheckQuota(t *testing.T) {
	tests := []struct {
		name     string
		n, total int
		maxPct   float64
		want     bool
	}{
		{"1 of 4 at 25.0 pct cap: not breached (25.0 is not > 25.0)", 1, 4, 25.0, false},
		{"2 of 4 at 25.0 pct cap: breached", 2, 4, 25.0, true},
		{"0 of 4: never breached", 0, 4, 25.0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pct, breached := CheckQuota(tt.n, tt.total, tt.maxPct)
			if breached != tt.want {
				t.Errorf("CheckQuota(%d, %d, %v) breached = %v (pct=%v), want %v", tt.n, tt.total, tt.maxPct, breached, pct, tt.want)
			}
		})
	}
}

// TestMatchGlobs_DoublestarNesting confirms MatchGlobs uses doublestar
// (not path.Match), which is what lets `**` cross an arbitrary number of
// path segments (D4).
func TestMatchGlobs_DoublestarNesting(t *testing.T) {
	if !MatchGlobs("internal/deep/nested/x.go", []string{"internal/**/*.go"}) {
		t.Error("MatchGlobs(internal/**/*.go) did not match a two-level-deep file")
	}
	if MatchGlobs("other/x.go", []string{"internal/**/*.go"}) {
		t.Error("MatchGlobs(internal/**/*.go) matched a file outside internal/")
	}
}
