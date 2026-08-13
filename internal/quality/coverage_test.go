package quality

import (
	"reflect"
	"testing"
)

// repoFiles is the shared fixture for NormalizeSourcePath's tests: a
// plausible small repo file list — AC12's "dos ficheros" ambiguity row
// needs two files sharing a suffix.
var repoFiles = []string{
	"internal/quality/git.go",
	"internal/x/y.go",
	"a/x/y.go",
	"b/x/y.go",
}

// TestNormalizeSourcePath covers AC12: one row per dialect, plus the
// ambiguity row.
func TestNormalizeSourcePath(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantRel string
		wantOK  bool
	}{
		{
			name:    "absolute path hanging off the repo root",
			raw:     "/home/dev/repo/internal/quality/git.go",
			wantRel: "internal/quality/git.go",
			wantOK:  true,
		},
		{
			name:    "relative as-is",
			raw:     "internal/quality/git.go",
			wantRel: "internal/quality/git.go",
			wantOK:  true,
		},
		{
			name:    "./-prefixed",
			raw:     "./internal/quality/git.go",
			wantRel: "internal/quality/git.go",
			wantOK:  true,
		},
		{
			name:    "module-prefixed (go-cover's native form)",
			raw:     "github.com/wirvii/mneme/internal/quality/git.go",
			wantRel: "internal/quality/git.go",
			wantOK:  true,
		},
		{
			name:    "backslash separators",
			raw:     `internal\quality\git.go`,
			wantRel: "internal/quality/git.go",
			wantOK:  true,
		},
		{
			name:   "absolute path foreign to the repo does not match",
			raw:    "/etc/passwd",
			wantOK: false,
		},
		{
			name:   "suffix matching two repo files does not match",
			raw:    "x/y.go",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rel, ok := NormalizeSourcePath(tt.raw, repoFiles)
			if ok != tt.wantOK {
				t.Fatalf("NormalizeSourcePath(%q) ok = %v, want %v (rel=%q)", tt.raw, ok, tt.wantOK, rel)
			}
			if ok && rel != tt.wantRel {
				t.Errorf("NormalizeSourcePath(%q) = %q, want %q", tt.raw, rel, tt.wantRel)
			}
		})
	}
}

// TestNormalizeSourcePath_MutationFirstInsteadOfUnique is the G17 guardian
// (plan P3): choosing "the first match" instead of requiring EXACTLY one
// must turn the ambiguity row above red — verified manually per the plan
// by temporarily changing NormalizeSourcePath's `case 1: return
// matched[0], true` / `default: return "", false` branches to always
// `return matched[0], true` regardless of len(matched), re-running
// TestNormalizeSourcePath (fails: the ambiguous "x/y.go" row now returns
// "a/x/y.go" instead of ok=false), and reverting byte-for-byte.
func TestNormalizeSourcePath_MutationFirstInsteadOfUnique(t *testing.T) {
	// This test intentionally duplicates the ambiguity assertion as a
	// single, minimal, always-green anchor pointing at the mutation
	// instructions above — see this test's godoc.
	_, ok := NormalizeSourcePath("x/y.go", repoFiles)
	if ok {
		t.Fatal("NormalizeSourcePath(ambiguous suffix) ok = true, want false")
	}
}

// TestComputeDiffCoverage_ExclusionsArePaired covers AC13: the SAME fixture
// (a generated file with zero coverage) passes with the exclusion in place
// and fails without it.
func TestComputeDiffCoverage_ExclusionsArePaired(t *testing.T) {
	changed := map[string][]int{
		"internal/x/y.go":     {1, 2},
		"internal/x/y_gen.go": {1, 2},
	}
	profile := &Profile{Files: map[string]FileCoverage{
		"internal/x/y.go":     {Lines: map[int]int{1: 1, 2: 1}},
		"internal/x/y_gen.go": {Lines: map[int]int{1: 0, 2: 0}},
	}}

	withExclusion := ComputeDiffCoverage(changed, profile, []string{"**/*_gen.go"})
	if withExclusion.Pct != 100 {
		t.Errorf("with exclusion: Pct = %v, want 100 (generated file must not drag it down)", withExclusion.Pct)
	}

	withoutExclusion := ComputeDiffCoverage(changed, profile, nil)
	if withoutExclusion.Pct == 100 {
		t.Errorf("without exclusion: Pct = %v, want < 100 (generated file's zero coverage must count)", withoutExclusion.Pct)
	}
}

// TestComputeGlobalStats_ExclusionsApplyToAggregateToo covers AC13's third
// row: exclusions apply to the AGGREGATE too, not only the delta.
func TestComputeGlobalStats_ExclusionsApplyToAggregateToo(t *testing.T) {
	profile := &Profile{Files: map[string]FileCoverage{
		"internal/x/y.go":     {Lines: map[int]int{1: 1, 2: 1}},
		"internal/x/y_gen.go": {Lines: map[int]int{1: 0, 2: 0}},
	}}

	totalWith, coveredWith, pctWith := ComputeGlobalStats(profile, []string{"**/*_gen.go"})
	if totalWith != 2 || coveredWith != 2 || pctWith != 100 {
		t.Errorf("with exclusion: total=%d covered=%d pct=%v, want 2/2/100", totalWith, coveredWith, pctWith)
	}

	totalWithout, coveredWithout, pctWithout := ComputeGlobalStats(profile, nil)
	if totalWithout != 4 || coveredWithout != 2 || pctWithout == 100 {
		t.Errorf("without exclusion: total=%d covered=%d pct=%v, want 4/2/50", totalWithout, coveredWithout, pctWithout)
	}
}

// TestComputeDiffCoverage_MissingLines pins the "which lines" contract
// AC24 needs: an eligible-but-uncovered line is listed, an ineligible
// (uninstrumented) one is neither counted nor listed.
func TestComputeDiffCoverage_MissingLines(t *testing.T) {
	changed := map[string][]int{"f.go": {1, 2, 3}}
	profile := &Profile{Files: map[string]FileCoverage{
		"f.go": {Lines: map[int]int{1: 1, 2: 0}}, // line 3 not instrumented at all
	}}

	stats := ComputeDiffCoverage(changed, profile, nil)
	if stats.LinesEligible != 2 {
		t.Fatalf("LinesEligible = %d, want 2 (line 3 is uninstrumented, neither counted nor penalized)", stats.LinesEligible)
	}
	if stats.LinesCovered != 1 {
		t.Fatalf("LinesCovered = %d, want 1", stats.LinesCovered)
	}
	if !reflect.DeepEqual(stats.Missing, map[string][]int{"f.go": {2}}) {
		t.Fatalf("Missing = %v, want {f.go: [2]}", stats.Missing)
	}
}

// TestScopeHash_OrderAndDuplicatesDoNotMatter pins ScopeHash's contract for
// AC21: the SAME set of excludes, given in a different order or with
// duplicates, produces the SAME hash — but a genuinely different set, or a
// different format, produces a DIFFERENT one.
func TestScopeHash_OrderAndDuplicatesDoNotMatter(t *testing.T) {
	a := ScopeHash("go-cover", []string{"**/*_gen.go", "**/mocks/**"})
	b := ScopeHash("go-cover", []string{"**/mocks/**", "**/*_gen.go", "**/*_gen.go"})
	if a != b {
		t.Errorf("ScopeHash differs for reordered/duplicated excludes: %s vs %s", a, b)
	}

	c := ScopeHash("go-cover", []string{"**/*_gen.go"})
	if a == c {
		t.Error("ScopeHash did not change when the exclude set genuinely changed")
	}

	d := ScopeHash("lcov", []string{"**/*_gen.go", "**/mocks/**"})
	if a == d {
		t.Error("ScopeHash did not change when the format changed")
	}
}
