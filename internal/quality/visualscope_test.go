package quality

import (
	"reflect"
	"testing"
)

// TestScopeTargets covers AC9: missing, extra, both, and neither — the four
// combinations D3's scope row rests on.
func TestScopeTargets(t *testing.T) {
	tests := []struct {
		name        string
		declared    []string
		reported    []string
		wantMissing []string
		wantExtra   []string
	}{
		{
			name:     "report is missing a declared target",
			declared: []string{"a", "b", "c"}, reported: []string{"a", "b"},
			wantMissing: []string{"c"}, wantExtra: nil,
		},
		{
			name:     "report has an extra, undeclared target",
			declared: []string{"a", "b", "c"}, reported: []string{"a", "b", "c", "d"},
			wantMissing: nil, wantExtra: []string{"d"},
		},
		{
			name:     "report matches exactly",
			declared: []string{"a", "b", "c"}, reported: []string{"a", "b", "c"},
			wantMissing: nil, wantExtra: nil,
		},
		{
			name:     "empty report is entirely missing",
			declared: []string{"a", "b", "c"}, reported: nil,
			wantMissing: []string{"a", "b", "c"}, wantExtra: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := &VisualReport{}
			for _, id := range tt.reported {
				rep.Targets = append(rep.Targets, VisualTarget{ID: id, Rendered: true})
			}
			missing, extra := ScopeTargets(tt.declared, rep)
			if !reflect.DeepEqual(missing, tt.wantMissing) {
				t.Errorf("missing = %v, want %v", missing, tt.wantMissing)
			}
			if !reflect.DeepEqual(extra, tt.wantExtra) {
				t.Errorf("extra = %v, want %v", extra, tt.wantExtra)
			}
		})
	}
}

// TestScopeTargets_NilReport covers the nil-report shape ScopeTargets treats
// as an empty report — every declared id is missing, nothing is extra.
func TestScopeTargets_NilReport(t *testing.T) {
	missing, extra := ScopeTargets([]string{"a", "b"}, nil)
	if !reflect.DeepEqual(missing, []string{"a", "b"}) {
		t.Errorf("missing = %v, want [a b]", missing)
	}
	if extra != nil {
		t.Errorf("extra = %v, want nil", extra)
	}
}

// TestFilterUnderDir covers AC17/G5b: the prefix is compared WITH a
// separator, so a sibling directory sharing the same string prefix never
// counts as being "under" the declared one.
func TestFilterUnderDir(t *testing.T) {
	paths := []string{
		".mneme/visual/reference/a.png",
		".mneme/visual/reference-old/a.png",
		"docs/reference/a.png",
		".mneme/visual/reference/b.png",
	}
	got := FilterUnderDir(paths, ".mneme/visual/reference")
	want := []string{".mneme/visual/reference/a.png", ".mneme/visual/reference/b.png"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FilterUnderDir() = %v, want %v", got, want)
	}
}

// TestEvaluateVisual_PageErrorsAlwaysFail covers G10a/G10b: an uncaught
// exception fails REGARDLESS of FailOnConsoleError; console.error only
// fails WHEN the project declared it should — the same fixture, two
// opposite verdicts.
func TestEvaluateVisual_PageErrorsAlwaysFail(t *testing.T) {
	rep := &VisualReport{Targets: []VisualTarget{
		{ID: "has-page-error", Rendered: true, PageErrors: []string{"TypeError: boom"}},
		{ID: "clean", Rendered: true},
	}}

	// FailOnConsoleError=false: the page error still fails (G10a).
	outcome := EvaluateVisual(rep, VisualThresholds{FailOnConsoleError: false})
	if !reflect.DeepEqual(outcome.PageErrorFailed, []string{"has-page-error"}) {
		t.Errorf("PageErrorFailed = %v, want [has-page-error] even with FailOnConsoleError=false", outcome.PageErrorFailed)
	}
}

// TestEvaluateVisual_ConsoleErrorConditional covers G10b: console.error
// only degrades the verdict when fail_on_console_error is declared true.
func TestEvaluateVisual_ConsoleErrorConditional(t *testing.T) {
	rep := &VisualReport{Targets: []VisualTarget{
		{ID: "noisy", Rendered: true, Console: ConsoleCounts{Error: 3}},
	}}

	off := EvaluateVisual(rep, VisualThresholds{FailOnConsoleError: false})
	if len(off.ConsoleErrorFailed) != 0 {
		t.Errorf("FailOnConsoleError=false: ConsoleErrorFailed = %v, want empty", off.ConsoleErrorFailed)
	}

	on := EvaluateVisual(rep, VisualThresholds{FailOnConsoleError: true})
	if !reflect.DeepEqual(on.ConsoleErrorFailed, []string{"noisy"}) {
		t.Errorf("FailOnConsoleError=true: ConsoleErrorFailed = %v, want [noisy]", on.ConsoleErrorFailed)
	}
}

// TestEvaluateVisual_A11y covers G11a/G11b/G11c: filtering by declared
// impact, failing on a declared impact, and "declared and not measured" is
// its own fail category.
func TestEvaluateVisual_A11y(t *testing.T) {
	rep := &VisualReport{Targets: []VisualTarget{
		{
			ID: "minor-only", Rendered: true,
			A11y: A11yResult{Reported: true, Violations: []A11yViolation{{Rule: "r1", Impact: A11yMinor}}},
		},
		{
			ID: "serious", Rendered: true,
			A11y: A11yResult{Reported: true, Violations: []A11yViolation{{Rule: "r2", Impact: A11ySerious}}},
		},
		{ID: "not-measured", Rendered: true},
	}}

	cfg := VisualThresholds{A11yFailImpacts: []A11yImpact{A11yCritical, A11ySerious}}
	outcome := EvaluateVisual(rep, cfg)

	// G11a: a violation OUTSIDE the declared set does not fail.
	for _, id := range outcome.A11yFailed {
		if id == "minor-only" {
			t.Errorf("A11yFailed contains %q, a minor-only violation must not fail against [critical serious]", id)
		}
	}
	// G11b: a violation INSIDE the declared set fails.
	if !reflect.DeepEqual(outcome.A11yFailed, []string{"serious"}) {
		t.Errorf("A11yFailed = %v, want [serious]", outcome.A11yFailed)
	}
	// G11c: declared-and-not-measured is its OWN fail category.
	if !reflect.DeepEqual(outcome.A11yNotReported, []string{"not-measured"}) {
		t.Errorf("A11yNotReported = %v, want [not-measured]", outcome.A11yNotReported)
	}

	// Hermana: with an EMPTY A11yFailImpacts, nothing fails at all — not
	// the serious violation, not the unmeasured target.
	empty := EvaluateVisual(rep, VisualThresholds{})
	if len(empty.A11yFailed) != 0 || len(empty.A11yNotReported) != 0 {
		t.Errorf("empty A11yFailImpacts: A11yFailed=%v A11yNotReported=%v, want both empty", empty.A11yFailed, empty.A11yNotReported)
	}
}

// TestEvaluateVisual_Breaches covers D10: a target failing for TWO reasons
// at once produces ONE entry in Breaches with BOTH reasons, never two
// separate entries or a half-truth.
func TestEvaluateVisual_Breaches(t *testing.T) {
	rep := &VisualReport{Targets: []VisualTarget{
		{ID: "double-trouble", Rendered: true, Console: ConsoleCounts{Error: 1}, PageErrors: []string{"boom"}},
	}}
	outcome := EvaluateVisual(rep, VisualThresholds{FailOnConsoleError: true})
	reasons, ok := outcome.Breaches["double-trouble"]
	if !ok {
		t.Fatalf("Breaches missing entry for double-trouble")
	}
	if len(reasons) != 2 {
		t.Fatalf("Breaches[double-trouble] = %v, want 2 reasons (page error + console error)", reasons)
	}
}

// TestBreachedTargetIDs_Ascending covers G19: the order is fixed ascending
// and checked against a LITERAL list — never merely "two runs agree with
// each other" (an inverted-but-repeatable comparator would pass that
// weaker check, the exact lesson S5 P3 already learned).
func TestBreachedTargetIDs_Ascending(t *testing.T) {
	outcome := VisualOutcome{Breaches: map[string][]string{
		"zebra": {"x"}, "alpha": {"x"}, "mike": {"x"},
	}}
	got := BreachedTargetIDs(outcome)
	want := []string{"alpha", "mike", "zebra"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BreachedTargetIDs() = %v, want %v", got, want)
	}
}
