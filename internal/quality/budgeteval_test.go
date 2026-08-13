package quality

import (
	"testing"
)

// symIn is a tiny constructor keeping the tables below readable: a created
// symbol in dir with a given key.
func symIn(dir, name string) Symbol {
	return Symbol{Key: SymbolKey(dir+"/x.go", name), QualifiedName: name, File: dir + "/x.go", Dir: dir}
}

// TestEvaluateBudget_MarginArithmetic covers AC11's three tramos plus the
// margin=0 fourth row: budget total 5 (a single quota dir), margin 2.
func TestEvaluateBudget_MarginArithmetic(t *testing.T) {
	budgetWithMargin := func(margin int) *Budget {
		return &Budget{Margin: margin, Radius: []string{"**"}, Quota: []Quota{{Dir: "internal/x", MaxNewSymbols: 5}}}
	}

	deltaOfSize := func(n int) SymbolDelta {
		var created []Symbol
		for i := 0; i < n; i++ {
			created = append(created, symIn("internal/x", string(rune('a'+i))))
		}
		return SymbolDelta{Created: created}
	}

	tests := []struct {
		name        string
		delivered   int
		margin      int
		wantPass    bool
		wantOverrun int
	}{
		{name: "delivered 5, margin 2 -> pass, overrun 0", delivered: 5, margin: 2, wantPass: true, wantOverrun: 0},
		{name: "delivered 7, margin 2 -> pass, overrun 2 (G12a)", delivered: 7, margin: 2, wantPass: true, wantOverrun: 2},
		{name: "delivered 8, margin 2 -> fail (G12b)", delivered: 8, margin: 2, wantPass: false, wantOverrun: 3},
		{name: "delivered 6, margin 0 -> fail", delivered: 6, margin: 0, wantPass: false, wantOverrun: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outcome := EvaluateBudget(deltaOfSize(tt.delivered), budgetWithMargin(tt.margin))
			if outcome.Pass != tt.wantPass {
				t.Errorf("Pass = %v, want %v (overrun=%d margin=%d)", outcome.Pass, tt.wantPass, outcome.Overrun, tt.margin)
			}
			if outcome.Overrun != tt.wantOverrun {
				t.Errorf("Overrun = %d, want %d", outcome.Overrun, tt.wantOverrun)
			}
		})
	}
}

// TestEvaluateBudget_RevisedIsNilWithoutRevision covers AC12's first row:
// the detail's three figures — Revised must be nil when Budget.Revision is
// nil.
func TestEvaluateBudget_RevisedIsNilWithoutRevision(t *testing.T) {
	b := &Budget{Margin: 1, Radius: []string{"**"}}
	outcome := EvaluateBudget(SymbolDelta{}, b)
	if outcome.Revised != nil {
		t.Errorf("Revised = %v, want nil", outcome.Revised)
	}
}

// TestEvaluateBudget_RevisionWidensTheEffectiveContract covers AC12's
// second row: a revision changes which margin/quotas are ACTUALLY
// evaluated, and Revised carries the widened total.
func TestEvaluateBudget_RevisionWidensTheEffectiveContract(t *testing.T) {
	b := &Budget{
		Margin: 0,
		Radius: []string{"**"},
		Quota:  []Quota{{Dir: "internal/x", MaxNewSymbols: 1}},
		Revision: &Revision{
			By: "architect", Rationale: "wiring exigio mas simbolos", Margin: 2,
			Quota: []Quota{{Dir: "internal/x", MaxNewSymbols: 3}},
		},
	}
	delta := SymbolDelta{Created: []Symbol{symIn("internal/x", "a"), symIn("internal/x", "b"), symIn("internal/x", "c")}}

	outcome := EvaluateBudget(delta, b)
	if outcome.Revised == nil || *outcome.Revised != 3 {
		t.Fatalf("Revised = %v, want pointer to 3", outcome.Revised)
	}
	if outcome.Budgeted != 1 {
		t.Errorf("Budgeted = %d, want 1 (the ORIGINAL quota, unaffected by the revision)", outcome.Budgeted)
	}
	if !outcome.Pass {
		t.Errorf("Pass = false, want true (3 created against the revised quota of 3)")
	}
}

// TestEvaluateBudget_CoverageByDirAndModify covers AC13's four rows against
// one shared fixture.
func TestEvaluateBudget_CoverageByDirAndModify(t *testing.T) {
	b := &Budget{
		Margin: 0,
		Radius: []string{"**"},
		Quota:  []Quota{{Dir: "internal/x", MaxNewSymbols: 1}},
		Modify: []ModifyEntry{{File: "internal/y/z.go", Symbol: "Declared"}},
	}
	delta := SymbolDelta{
		Created: []Symbol{
			symIn("internal/x", "CoveredByQuota"),   // dir has quota, capacity 1 -> covered
			symIn("internal/nope", "NoQuotaForDir"), // dir has NO [[quota]] -> uncovered
		},
		Modified: []Symbol{
			{Key: SymbolKey("internal/y/z.go", "Declared"), QualifiedName: "Declared", File: "internal/y/z.go", Dir: "internal/y"},
			{Key: SymbolKey("internal/y/z.go", "Undeclared"), QualifiedName: "Undeclared", File: "internal/y/z.go", Dir: "internal/y"},
		},
	}

	outcome := EvaluateBudget(delta, b)

	uncoveredNames := make(map[string]bool, len(outcome.Uncovered))
	for _, s := range outcome.Uncovered {
		uncoveredNames[s.QualifiedName] = true
	}

	if uncoveredNames["CoveredByQuota"] {
		t.Error("CoveredByQuota should be covered (its dir has capacity)")
	}
	if !uncoveredNames["NoQuotaForDir"] {
		t.Error("NoQuotaForDir should be uncovered (its dir has no [[quota]])")
	}
	if uncoveredNames["Declared"] {
		t.Error("Declared should be covered (it is in [[modify]])")
	}
	if !uncoveredNames["Undeclared"] {
		t.Error("Undeclared should be uncovered (it is NOT in [[modify]])")
	}
}

// TestEvaluateRadius_DoesNotConsumeMargin covers AC14: EvaluateRadius is a
// SEPARATE evaluator from EvaluateBudget — the two are never combined into
// one pool (G14 is caught at the CALLER that would wrongly merge them; this
// test pins that EvaluateRadius itself carries no margin concept at all).
func TestEvaluateRadius_DoesNotConsumeMargin(t *testing.T) {
	tests := []struct {
		name         string
		changedFiles []string
		globs        []string
		wantOutside  []string
	}{
		{
			name:         "file outside radius is reported",
			changedFiles: []string{"internal/quality/x.go", "docs/readme.md"},
			globs:        []string{"internal/quality/**"},
			wantOutside:  []string{"docs/readme.md"},
		},
		{
			name:         "all files inside radius -> nothing outside (positive)",
			changedFiles: []string{"internal/quality/x.go", "internal/quality/y.go"},
			globs:        []string{"internal/quality/**"},
			wantOutside:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateRadius(tt.changedFiles, tt.globs)
			if len(got) != len(tt.wantOutside) {
				t.Fatalf("EvaluateRadius() = %v, want %v", got, tt.wantOutside)
			}
			for i := range got {
				if got[i] != tt.wantOutside[i] {
					t.Errorf("EvaluateRadius()[%d] = %q, want %q", i, got[i], tt.wantOutside[i])
				}
			}
		})
	}
}

// TestEvaluateTrivialBudget covers AC26: the four breach kinds plus the
// obligatory clean hermana, all against DefaultTrivialBudget so the test
// never repeats the literal 3/20.
func TestEvaluateTrivialBudget(t *testing.T) {
	tests := []struct {
		name      string
		files     []FileStat
		delta     SymbolDelta
		scope     string
		wantCount int
		wantText  string
	}{
		{
			name:      "too many files",
			files:     []FileStat{{Path: "a.go"}, {Path: "b.go"}, {Path: "c.go"}, {Path: "d.go"}},
			wantCount: 1,
			wantText:  "file count 4 exceeds trivial limit of 3",
		},
		{
			name:      "too many lines",
			files:     []FileStat{{Path: "a.go", Added: 21}},
			wantCount: 1,
			wantText:  "line count 21 exceeds trivial limit of 20",
		},
		{
			name:      "out of scope",
			files:     []FileStat{{Path: "internal/other/x.go"}},
			scope:     "internal/store/*.go",
			wantCount: 1,
			wantText:  "out of scope: internal/other/x.go",
		},
		{
			name:      "forbidden path",
			files:     []FileStat{{Path: "internal/db/migrations/001.sql"}},
			wantCount: 1,
			wantText:  "forbidden path modified: internal/db/migrations/001.sql",
		},
		{
			name:  "exported symbol created",
			files: []FileStat{{Path: "internal/store/x.go"}},
			delta: SymbolDelta{Created: []Symbol{
				{QualifiedName: "NewPublicFunc", File: "internal/store/x.go", Exported: true},
			}},
			scope:     "internal/store/*.go",
			wantCount: 1,
			wantText:  "public symbol changed: NewPublicFunc in internal/store/x.go",
		},
		{
			name:      "clean spec, zero breaches (positive)",
			files:     []FileStat{{Path: "internal/store/x.go", Added: 10}},
			scope:     "internal/store/*.go",
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			breaches := EvaluateTrivialBudget(tt.files, tt.delta, tt.scope, DefaultTrivialBudget)
			if len(breaches) != tt.wantCount {
				t.Fatalf("EvaluateTrivialBudget() = %v, want %d breach(es)", breaches, tt.wantCount)
			}
			if tt.wantText != "" && string(breaches[0]) != tt.wantText {
				t.Errorf("breach = %q, want %q", breaches[0], tt.wantText)
			}
		})
	}
}
