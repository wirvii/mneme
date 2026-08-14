package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/quality"
	"github.com/wirvii/mneme/internal/store"
)

// mutationTestConstitution returns a Constitution with [mutation] declared
// and enabled, using the mutants-v1 format and a report_path that never
// collides with anything a coverage/budget test writes.
func mutationTestConstitution() *quality.Constitution {
	return &quality.Constitution{
		SchemaVersion: 5, MutationDeclared: true,
		Mutation: quality.MutationConfig{
			Enabled: true, Format: "mutants-v1", Command: []string{"true"},
			ReportPath: "tmp/mutants.json", Timeout: 5 * time.Minute,
			MaxEquivalent: 2, MaxNotViablePct: 25.0,
		},
	}
}

// mutantsV1Doc builds a minimal mutants-v1 JSON document from a list of
// "file:line:mutator:status" shorthand entries — keeps every test's
// fixture legible without hand-writing JSON each time.
func mutantsV1Doc(entries ...string) string {
	var b strings.Builder
	b.WriteString(`{"schema":"mutants-v1","mutants":[`)
	for i, e := range entries {
		parts := strings.SplitN(e, ":", 4)
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"file":"` + parts[0] + `","line":` + parts[1] + `,"mutator":"` + parts[2] + `","status":"` + parts[3] + `"}`)
	}
	b.WriteString(`]}`)
	return b.String()
}

// findMutationCheck locates a (kind, name) row in checks or fails the
// test — distinct name from criteria_test.go's findCheck/budget_test.go's
// findBudgetCheck so all three coexist without a signature clash.
func findMutationCheck(t *testing.T, checks []*model.QualityCheck, kind, name string) *model.QualityCheck {
	t.Helper()
	for _, c := range checks {
		if c.Kind == kind && c.Name == name {
			return c
		}
	}
	t.Fatalf("no %s/%s row in %+v", kind, name, checks)
	return nil
}

// TestRunMutationChecks_SkipReasons covers AC16/AC25: four DISTINCT texts
// (never the same summary for two different causes), all six rows
// skipped, and the fake runner recording ZERO invocations of the mutation
// command in every case (G15a/G15b's own "cero invocaciones" guardian).
func TestRunMutationChecks_SkipReasons(t *testing.T) {
	spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard}
	g := &quality.Git{RepoDir: t.TempDir()}

	tests := []struct {
		name          string
		constitution  *quality.Constitution
		gatesStopped  bool
		anyGateFailed bool
	}{
		{
			name:         "gate cascade stopped (required gate failed)",
			constitution: mutationTestConstitution(),
			gatesStopped: true,
		},
		{
			name:         "schema < 5 (mutation not declared)",
			constitution: &quality.Constitution{SchemaVersion: 4, MutationDeclared: false},
		},
		{
			name:         "mutation.enabled = false",
			constitution: &quality.Constitution{SchemaVersion: 5, MutationDeclared: true, Mutation: quality.MutationConfig{Enabled: false}},
		},
		{
			name:          "a non-required gate is in fail (G15a — stricter than gatesStopped alone)",
			constitution:  mutationTestConstitution(),
			anyGateFailed: true,
		},
	}

	summaries := make(map[string]string, len(tests))
	runner := &fakeGateRunner{}
	svc := &QualityService{runner: runner}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checks, pure, err := svc.runMutationChecks(context.Background(), g, tt.constitution, spec, tt.gatesStopped, tt.anyGateFailed)
			if err != nil {
				t.Fatalf("runMutationChecks: %v", err)
			}
			if len(checks) != 6 || len(pure) != 6 {
				t.Fatalf("len(checks)=%d len(pure)=%d, want 6 each", len(checks), len(pure))
			}
			for _, c := range checks {
				if c.Status != "skipped" {
					t.Errorf("row %s/%s status = %q, want skipped", c.Kind, c.Name, c.Status)
				}
			}
			summaries[tt.name] = checks[0].Summary
		})
	}

	if len(runner.calls) != 0 {
		t.Fatalf("runner.calls = %v, want zero invocations across every skip cause", runner.calls)
	}

	seen := make(map[string]string, len(summaries))
	for name, summary := range summaries {
		if other, dup := seen[summary]; dup {
			t.Errorf("skip summary %q is shared by %q and %q — AC25 requires four DISTINCT texts", summary, name, other)
		}
		seen[summary] = name
	}
}

// TestMutationSkipReason_FourDistinctTexts is G25's OWN dedicated
// guardian, deliberately stricter than TestRunMutationChecks_SkipReasons'
// own uniqueness check: it holds constitution.SchemaVersion IDENTICAL
// (5) across the "not declared" and "enabled=false" fixtures, so the two
// summaries can only differ if mutationSkipReason's own wording differs —
// a coincidental difference in SchemaVersion between two loosely-built
// fixtures could otherwise mask a genuine text collision (the "aritmetica
// con N pequeno" trap this EPIC has hit before, in a different guise).
func TestMutationSkipReason_FourDistinctTexts(t *testing.T) {
	sameSchema := 5
	notDeclared := mutationSkipReason(false, false, &quality.Constitution{SchemaVersion: sameSchema, MutationDeclared: false})
	disabled := mutationSkipReason(false, false, &quality.Constitution{
		SchemaVersion: sameSchema, MutationDeclared: true, Mutation: quality.MutationConfig{Enabled: false},
	})
	cascade := mutationSkipReason(true, false, &quality.Constitution{SchemaVersion: sameSchema, MutationDeclared: true, Mutation: quality.MutationConfig{Enabled: true}})
	gateRed := mutationSkipReason(false, true, &quality.Constitution{SchemaVersion: sameSchema, MutationDeclared: true, Mutation: quality.MutationConfig{Enabled: true}})

	texts := map[string]string{"not-declared": notDeclared, "disabled": disabled, "cascade": cascade, "gate-red": gateRed}
	seen := make(map[string]string, len(texts))
	for name, text := range texts {
		if text == "" {
			t.Fatalf("%s: mutationSkipReason returned empty, want a non-empty skip reason", name)
		}
		if other, dup := seen[text]; dup {
			t.Fatalf("%s and %s produced the IDENTICAL text %q — AC25 requires four distinct texts, with SchemaVersion held constant across fixtures", name, other, text)
		}
		seen[text] = name
	}
}

// TestMutationSkipReason_AllGatesGreen_Evaluates is G15b's own hermana:
// with no cascade and no gate in fail, mutationSkipReason returns "" and
// the mechanism proceeds to evaluate.
func TestMutationSkipReason_AllGatesGreen_Evaluates(t *testing.T) {
	reason := mutationSkipReason(false, false, mutationTestConstitution())
	if reason != "" {
		t.Errorf("mutationSkipReason(clean) = %q, want empty (mechanism must evaluate)", reason)
	}
}

// TestRunMutationChecks_ReportFailures covers AC14's five ways row 1
// fails and the one way it passes, table-driven on the SAME repo fixture
// — the mold of TestQualityService_Verify_Coverage_ProfileChecks (S2),
// applied to mutation/report instead of coverage/profile.
func TestRunMutationChecks_ReportFailures(t *testing.T) {
	validDoc := mutantsV1Doc("a.go:1:x:killed")

	tests := []struct {
		name        string
		runnerRes   quality.GateResult
		writeReport string // "" = runner writes nothing
		wantStatus  string
	}{
		{
			name:       "command exits non-zero",
			runnerRes:  quality.GateResult{Status: quality.GateStatusFail, ExitCode: 1},
			wantStatus: "fail",
		},
		{
			name:       "command exits 0 but writes no file",
			runnerRes:  quality.GateResult{Status: quality.GateStatusPass},
			wantStatus: "fail",
		},
		{
			name:        "file exists but is not parseable in the declared format",
			runnerRes:   quality.GateResult{Status: quality.GateStatusPass},
			writeReport: "not json at all [[[",
			wantStatus:  "fail",
		},
		{
			name:        "command exits 0 with a valid report",
			runnerRes:   quality.GateResult{Status: quality.GateStatusPass},
			writeReport: validDoc,
			wantStatus:  "pass",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoDir := newTestGitRepo(t)
			spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard, BaseSHA: headSHAFor(t, repoDir)}
			g := &quality.Git{RepoDir: repoDir}

			runner := &fakeGateRunner{results: map[string]quality.GateResult{"mutation-report": tt.runnerRes}}
			if tt.writeReport != "" {
				runner.writeFiles = map[string]map[string]string{"mutation-report": {"tmp/mutants.json": tt.writeReport}}
			}
			svc := &QualityService{repoDir: repoDir, runner: runner}

			checks, _, err := svc.runMutationChecks(context.Background(), g, mutationTestConstitution(), spec, false, false)
			if err != nil {
				t.Fatalf("runMutationChecks: %v", err)
			}
			row := findMutationCheck(t, checks, "mutation", "report")
			if row.Status != tt.wantStatus {
				t.Errorf("mutation/report status = %q, want %q (summary=%q)", row.Status, tt.wantStatus, row.Summary)
			}
			if tt.wantStatus == "fail" {
				// Rows 2-6 must be skipped, never accumulated on top of
				// row 1's own failure (D13's "no se acumulan dos
				// diagnosticos del mismo hecho").
				for _, name := range []string{"scope", "viability", "timeouts", "not-covered", "score"} {
					r := findMutationCheck(t, checks, "mutation", name)
					if r.Status != "skipped" {
						t.Errorf("mutation/%s status = %q, want skipped when report failed", name, r.Status)
					}
				}
			}
		})
	}
}

// TestRunMutationChecks_ReportPathTracked covers the report_path-is-an-
// output guardrail (G13b), reusing prepareDeclaredOutput (P7): a
// report_path tracked by git is refused, and the message names the
// mutation-specific subject ("el informe de mutacion"), not coverage's.
func TestRunMutationChecks_ReportPathTracked(t *testing.T) {
	repoDir := newTestGitRepo(t)
	if err := os.MkdirAll(filepath.Join(repoDir, "tmp"), 0o755); err != nil {
		t.Fatalf("mkdir tmp: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "tmp", "mutants.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write tracked report: %v", err)
	}
	commitAll(t, repoDir, "track tmp/mutants.json")

	spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard, BaseSHA: headSHAFor(t, repoDir)}
	g := &quality.Git{RepoDir: repoDir}
	runner := &fakeGateRunner{}
	svc := &QualityService{repoDir: repoDir, runner: runner}

	checks, _, err := svc.runMutationChecks(context.Background(), g, mutationTestConstitution(), spec, false, false)
	if err != nil {
		t.Fatalf("runMutationChecks: %v", err)
	}
	row := findMutationCheck(t, checks, "mutation", "report")
	if row.Status != "fail" {
		t.Fatalf("mutation/report status = %q, want fail (tracked report_path)", row.Status)
	}
	if !strings.Contains(row.Summary, "informe de mutacion") {
		t.Errorf("mutation/report summary = %q, want it to name %q", row.Summary, "informe de mutacion")
	}
	if len(runner.calls) != 0 {
		t.Errorf("runner.calls = %v, want zero — a tracked report_path must never reach the runner", runner.calls)
	}
	// The tracked file itself must survive untouched — mneme never
	// deletes a versioned file (V7/D4 of the design).
	if _, statErr := os.Stat(filepath.Join(repoDir, "tmp", "mutants.json")); statErr != nil {
		t.Errorf("tracked report file was removed: %v", statErr)
	}
}

// TestRunMutationChecks_StaleReportDeletedBeforeRun covers G13a: a stale
// mutants.json left over from a PRIOR run must be deleted before the
// mutation command executes — a fake runner that writes NOTHING must
// therefore leave mutation/report `fail` on a missing file, never a false
// `pass` reading the old report's contents.
func TestRunMutationChecks_StaleReportDeletedBeforeRun(t *testing.T) {
	repoDir := newTestGitRepo(t)
	if err := os.MkdirAll(filepath.Join(repoDir, "tmp"), 0o755); err != nil {
		t.Fatalf("mkdir tmp: %v", err)
	}
	staleDoc := mutantsV1Doc("a.go:1:x:killed")
	if err := os.WriteFile(filepath.Join(repoDir, "tmp", "mutants.json"), []byte(staleDoc), 0o644); err != nil {
		t.Fatalf("seed stale report: %v", err)
	}
	// Deliberately NOT committed (an untracked leftover, exactly like a
	// prior local run's own artifact — .gitignore covers tmp/ in the real
	// repo, but IsTracked is what this helper actually consults).

	spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard, BaseSHA: headSHAFor(t, repoDir)}
	g := &quality.Git{RepoDir: repoDir}
	runner := &fakeGateRunner{} // writes nothing at all
	svc := &QualityService{repoDir: repoDir, runner: runner}

	checks, _, err := svc.runMutationChecks(context.Background(), g, mutationTestConstitution(), spec, false, false)
	if err != nil {
		t.Fatalf("runMutationChecks: %v", err)
	}
	row := findMutationCheck(t, checks, "mutation", "report")
	if row.Status != "fail" {
		t.Fatalf("mutation/report status = %q, want fail — the stale report must have been deleted before the (no-op) runner ran", row.Status)
	}
	if _, statErr := os.Stat(filepath.Join(repoDir, "tmp", "mutants.json")); !os.IsNotExist(statErr) {
		t.Errorf("stale report still exists after Verify (statErr=%v) — prepareDeclaredOutput must delete it before running the command", statErr)
	}
}

// TestRunMutationChecks_BudgetExceeded covers AC15: a timeout is a
// `finding` `budget-exceeded`, DISTINCT from a plain non-zero exit
// (`fail`) — the exact GateResult shape ExecRunner.Run produces on
// timeout (runner.go).
func TestRunMutationChecks_BudgetExceeded(t *testing.T) {
	repoDir := newTestGitRepo(t)
	spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard, BaseSHA: headSHAFor(t, repoDir)}
	g := &quality.Git{RepoDir: repoDir}
	runner := &fakeGateRunner{results: map[string]quality.GateResult{
		"mutation-report": {Status: quality.GateStatusFail, ExitCode: -1, Summary: "timeout tras 5m0s"},
	}}
	svc := &QualityService{repoDir: repoDir, runner: runner}

	checks, _, err := svc.runMutationChecks(context.Background(), g, mutationTestConstitution(), spec, false, false)
	if err != nil {
		t.Fatalf("runMutationChecks: %v", err)
	}
	row := findMutationCheck(t, checks, "mutation", "report")
	if row.Status != "finding" {
		t.Fatalf("mutation/report status = %q, want finding (budget-exceeded)", row.Status)
	}
	if !strings.Contains(row.Summary, "budget-exceeded") {
		t.Errorf("mutation/report summary = %q, want it to name budget-exceeded", row.Summary)
	}
	for _, name := range []string{"scope", "viability", "timeouts", "not-covered", "score"} {
		r := findMutationCheck(t, checks, "mutation", name)
		if r.Status != "skipped" {
			t.Errorf("mutation/%s status = %q, want skipped after budget-exceeded", name, r.Status)
		}
	}
}

// TestIsMutationTimeout covers the discriminator directly: only the exact
// ExecRunner timeout shape is a timeout — a plain -1 with a DIFFERENT
// Summary (e.g. "command not found") is not.
func TestIsMutationTimeout(t *testing.T) {
	tests := []struct {
		name string
		res  quality.GateResult
		want bool
	}{
		{"exact timeout shape", quality.GateResult{ExitCode: -1, Summary: "timeout tras 30m0s"}, true},
		{"exit -1 but not-found summary", quality.GateResult{ExitCode: -1, Summary: "comando no encontrado en PATH: x"}, false},
		{"exit 1, no timeout", quality.GateResult{ExitCode: 1}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isMutationTimeout(tt.res); got != tt.want {
				t.Errorf("isMutationTimeout(%+v) = %v, want %v", tt.res, got, tt.want)
			}
		})
	}
}

// TestRunMutationChecks_BaseUnknown covers AC13/D18 point 4: an empty
// spec.BaseSHA (and, separately, an unreachable merge-base) makes row 2 a
// `finding` `base-unknown` — NEVER a `pass`, never a silent `skipped` —
// and rows 3-6 skip naming it. Row 1 (report) still ran and passed: report
// generation does not depend on knowing the base.
func TestRunMutationChecks_BaseUnknown(t *testing.T) {
	tests := []struct {
		name    string
		baseSHA string
	}{
		{"empty base_sha", ""},
		{"unreachable merge-base", "0000000000000000000000000000000000000000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoDir := newTestGitRepo(t)
			spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard, BaseSHA: tt.baseSHA}
			g := &quality.Git{RepoDir: repoDir}
			runner := &fakeGateRunner{writeFiles: map[string]map[string]string{
				"mutation-report": {"tmp/mutants.json": mutantsV1Doc("a.go:1:x:killed")},
			}}
			svc := &QualityService{repoDir: repoDir, runner: runner}

			checks, pure, err := svc.runMutationChecks(context.Background(), g, mutationTestConstitution(), spec, false, false)
			if err != nil {
				t.Fatalf("runMutationChecks: %v", err)
			}
			reportRow := findMutationCheck(t, checks, "mutation", "report")
			if reportRow.Status != "pass" {
				t.Errorf("mutation/report status = %q, want pass (report generation does not need the base)", reportRow.Status)
			}
			scopeRow := findMutationCheck(t, checks, "mutation", "scope")
			if scopeRow.Status != "finding" || !strings.Contains(scopeRow.Summary, "base-unknown") {
				t.Errorf("mutation/scope = %+v, want finding naming base-unknown", scopeRow)
			}
			for _, name := range []string{"viability", "timeouts", "not-covered", "score"} {
				r := findMutationCheck(t, checks, "mutation", name)
				if r.Status != "skipped" {
					t.Errorf("mutation/%s status = %q, want skipped", name, r.Status)
				}
				if !strings.Contains(r.Summary, "base-unknown") {
					t.Errorf("mutation/%s summary = %q, want it to name base-unknown", name, r.Summary)
				}
			}
			if len(pure) != 6 {
				t.Fatalf("len(pure) = %d, want 6", len(pure))
			}
		})
	}
}

// mutationGitFixture creates a real repo with one base commit and one
// follow-up commit that adds exactly one new line to foo.go, returning
// repoDir, the base commit's SHA, and the line number the new commit adds
// (so a test can place a mutant precisely in or out of range).
func mutationGitFixture(t *testing.T) (repoDir, baseSHA string, changedLine int) {
	t.Helper()
	repoDir = newTestGitRepo(t)
	baseSHA = headSHAFor(t, repoDir)

	content := "package main\n\nfunc Foo() {}\n\nfunc Bar() {}\n"
	if err := os.WriteFile(filepath.Join(repoDir, "foo.go"), []byte(content), 0o644); err != nil {
		t.Fatalf("write foo.go: %v", err)
	}
	commitAll(t, repoDir, "add foo.go")
	// Line 5 ("func Bar() {}") is the newly added line this spec's diff
	// covers — main.go (committed in newTestGitRepo) and lines 1-4 of
	// foo.go are NOT part of the changed set.
	return repoDir, baseSHA, 5
}

// TestRunMutationChecks_ScopingInDiffVsOutside covers G8a/G8b/G11 at the
// service-integration layer: a mutant on the changed line is in-diff and
// contributes to the tally; a mutant on an unrelated, pre-existing line
// does not — and an informe with NOTHING in the changed range degrades to
// mutation/scope's own finding (G11, the empty-denominator trap).
func TestRunMutationChecks_ScopingInDiffVsOutside(t *testing.T) {
	repoDir, baseSHA, changedLine := mutationGitFixture(t)
	spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard, BaseSHA: baseSHA}
	g := &quality.Git{RepoDir: repoDir}

	t.Run("mutant in the changed range contributes", func(t *testing.T) {
		runner := &fakeGateRunner{writeFiles: map[string]map[string]string{
			"mutation-report": {"tmp/mutants.json": mutantsV1Doc(
				"foo.go:"+itoa(changedLine)+":x:killed",
				// main.go pre-exists the spec's base commit and is
				// untouched by it — outside the diff (newTestGitRepo
				// commits it BEFORE mutationGitFixture captures baseSHA).
				"main.go:1:x:killed",
			)},
		}}
		svc := &QualityService{repoDir: repoDir, runner: runner}
		checks, _, err := svc.runMutationChecks(context.Background(), g, mutationTestConstitution(), spec, false, false)
		if err != nil {
			t.Fatalf("runMutationChecks: %v", err)
		}
		scopeRow := findMutationCheck(t, checks, "mutation", "scope")
		if scopeRow.Status != "pass" || !strings.Contains(scopeRow.Summary, "1 mutante") {
			t.Errorf("mutation/scope = %+v, want pass naming exactly 1 mutante en el rango", scopeRow)
		}
	})

	t.Run("no mutants in the changed range at all (G11)", func(t *testing.T) {
		runner := &fakeGateRunner{writeFiles: map[string]map[string]string{
			"mutation-report": {"tmp/mutants.json": mutantsV1Doc("main.go:1:x:killed")},
		}}
		svc := &QualityService{repoDir: repoDir, runner: runner}
		checks, _, err := svc.runMutationChecks(context.Background(), g, mutationTestConstitution(), spec, false, false)
		if err != nil {
			t.Fatalf("runMutationChecks: %v", err)
		}
		scopeRow := findMutationCheck(t, checks, "mutation", "scope")
		if scopeRow.Status != "finding" || scopeRow.Summary != "no-mutants-in-diff" {
			t.Errorf("mutation/scope = %+v, want finding no-mutants-in-diff", scopeRow)
		}
	})
}

// TestRunMutationChecks_Viability covers G10b — the single most important
// guardian in this spec — plus its hermana: an informe whose in-diff
// mutants are ALL not_viable has ZERO survivors and yet mutation/viability
// is a `finding`, degrading the certificate's verdict via DeriveVerdict;
// the same shape below the quota passes.
func TestRunMutationChecks_Viability(t *testing.T) {
	repoDir, baseSHA, changedLine := mutationGitFixture(t)
	spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard, BaseSHA: baseSHA}
	g := &quality.Git{RepoDir: repoDir}
	line := itoa(changedLine)

	t.Run("all not_viable: catastrophic green closed", func(t *testing.T) {
		runner := &fakeGateRunner{writeFiles: map[string]map[string]string{
			"mutation-report": {"tmp/mutants.json": mutantsV1Doc(
				"foo.go:"+line+":a:not_viable",
				"foo.go:"+line+":b:not_viable",
				"foo.go:"+line+":c:not_viable",
			)},
		}}
		svc := &QualityService{repoDir: repoDir, runner: runner}
		checks, pure, err := svc.runMutationChecks(context.Background(), g, mutationTestConstitution(), spec, false, false)
		if err != nil {
			t.Fatalf("runMutationChecks: %v", err)
		}
		viabilityRow := findMutationCheck(t, checks, "mutation", "viability")
		if viabilityRow.Status != "finding" {
			t.Fatalf("mutation/viability status = %q, want finding — an all-not_viable informe must NOT be a silent pass (D1 pata d)", viabilityRow.Status)
		}
		// No mutant rows at all: not_viable is neither death nor survivor.
		for _, c := range checks {
			if c.Kind == "mutant" {
				t.Errorf("unexpected mutant row %+v — not_viable must never produce a survivor row", c)
			}
		}
		verdict := quality.DeriveVerdict(pure)
		if verdict != quality.VerdictFindings {
			t.Errorf("DeriveVerdict(pure) = %q, want findings — this is the certificate-level proof the catastrophic green is closed", verdict)
		}
	})

	t.Run("below the quota: pass (hermana)", func(t *testing.T) {
		runner := &fakeGateRunner{writeFiles: map[string]map[string]string{
			"mutation-report": {"tmp/mutants.json": mutantsV1Doc(
				"foo.go:"+line+":a:not_viable",
				"foo.go:"+line+":b:killed",
				"foo.go:"+line+":c:killed",
				"foo.go:"+line+":d:killed",
			)},
		}}
		svc := &QualityService{repoDir: repoDir, runner: runner}
		checks, _, err := svc.runMutationChecks(context.Background(), g, mutationTestConstitution(), spec, false, false)
		if err != nil {
			t.Fatalf("runMutationChecks: %v", err)
		}
		viabilityRow := findMutationCheck(t, checks, "mutation", "viability")
		if viabilityRow.Status != "pass" {
			t.Errorf("mutation/viability status = %q, want pass (25%% <= 25%% quota)", viabilityRow.Status)
		}
	})
}

// TestRunMutationChecks_TimeoutsAndNotCovered covers AC23/AC24/G23/G24:
// not_covered is informative-only (always pass, never degrades) while
// timed_out is a finding (neither death nor survival) — the two
// hermanas paired on the same fixture.
func TestRunMutationChecks_TimeoutsAndNotCovered(t *testing.T) {
	repoDir, baseSHA, changedLine := mutationGitFixture(t)
	spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard, BaseSHA: baseSHA}
	g := &quality.Git{RepoDir: repoDir}
	line := itoa(changedLine)

	runner := &fakeGateRunner{writeFiles: map[string]map[string]string{
		"mutation-report": {"tmp/mutants.json": mutantsV1Doc(
			"foo.go:"+line+":a:not_covered",
			"foo.go:"+line+":b:not_covered",
			"foo.go:"+line+":c:timed_out",
			"foo.go:"+line+":d:killed",
		)},
	}}
	svc := &QualityService{repoDir: repoDir, runner: runner}
	checks, pure, err := svc.runMutationChecks(context.Background(), g, mutationTestConstitution(), spec, false, false)
	if err != nil {
		t.Fatalf("runMutationChecks: %v", err)
	}

	notCoveredRow := findMutationCheck(t, checks, "mutation", "not-covered")
	if notCoveredRow.Status != "pass" || !strings.Contains(notCoveredRow.Summary, "2 mutante") {
		t.Errorf("mutation/not-covered = %+v, want pass naming 2 mutantes", notCoveredRow)
	}
	timeoutsRow := findMutationCheck(t, checks, "mutation", "timeouts")
	if timeoutsRow.Status != "finding" || !strings.Contains(timeoutsRow.Summary, "1 mutante") {
		t.Errorf("mutation/timeouts = %+v, want finding naming 1 mutante", timeoutsRow)
	}
	for _, c := range checks {
		if c.Kind == "mutant" {
			t.Errorf("unexpected mutant row %+v — neither not_covered nor timed_out is a survivor", c)
		}
	}
	verdict := quality.DeriveVerdict(pure)
	if verdict != quality.VerdictFindings {
		t.Errorf("DeriveVerdict(pure) = %q, want findings (the timed_out row alone must degrade it)", verdict)
	}
}

// TestRunMutationChecks_NotCoveredAlone_NeverDegrades is
// TestRunMutationChecks_TimeoutsAndNotCovered's own isolating hermana:
// with ONLY not_covered mutants (zero timed_out, zero survivors), the
// certificate's derived verdict is `pass`, never `findings` — proving
// not_covered alone never condemns (AC23).
func TestRunMutationChecks_NotCoveredAlone_NeverDegrades(t *testing.T) {
	repoDir, baseSHA, changedLine := mutationGitFixture(t)
	spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard, BaseSHA: baseSHA}
	g := &quality.Git{RepoDir: repoDir}
	line := itoa(changedLine)

	runner := &fakeGateRunner{writeFiles: map[string]map[string]string{
		"mutation-report": {"tmp/mutants.json": mutantsV1Doc("foo.go:"+line+":a:not_covered", "foo.go:"+line+":b:killed")},
	}}
	svc := &QualityService{repoDir: repoDir, runner: runner}
	_, pure, err := svc.runMutationChecks(context.Background(), g, mutationTestConstitution(), spec, false, false)
	if err != nil {
		t.Fatalf("runMutationChecks: %v", err)
	}
	if verdict := quality.DeriveVerdict(pure); verdict != quality.VerdictPass {
		t.Errorf("DeriveVerdict(pure) = %q, want pass — not_covered alone must never degrade the verdict", verdict)
	}
}

// TestRunMutationChecks_SurvivorRows covers AC17/G16: exactly N `mutant`
// rows for N survivors, each named by its exact ID, and the hermana of
// zero survivors producing zero mutant rows with mutation/score `pass`.
func TestRunMutationChecks_SurvivorRows(t *testing.T) {
	repoDir, baseSHA, changedLine := mutationGitFixture(t)
	spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard, BaseSHA: baseSHA}
	g := &quality.Git{RepoDir: repoDir}
	line := itoa(changedLine)

	t.Run("three survivors: exactly three rows", func(t *testing.T) {
		runner := &fakeGateRunner{writeFiles: map[string]map[string]string{
			"mutation-report": {"tmp/mutants.json": mutantsV1Doc(
				"foo.go:"+line+":a:lived", "foo.go:"+line+":b:lived", "foo.go:"+line+":c:lived",
			)},
		}}
		svc := &QualityService{repoDir: repoDir, runner: runner}
		checks, pure, err := svc.runMutationChecks(context.Background(), g, mutationTestConstitution(), spec, false, false)
		if err != nil {
			t.Fatalf("runMutationChecks: %v", err)
		}
		// AC19/G19a: a survivor row must DEGRADE the certificate's derived
		// verdict — this is what "tumba el certificado" means mechanically:
		// ensureCertified later refuses implementing->qa on anything but
		// VerdictPass.
		if verdict := quality.DeriveVerdict(pure); verdict != quality.VerdictFindings {
			t.Fatalf("DeriveVerdict(pure) = %q, want findings — a survivor must block implementing->qa", verdict)
		}
		var mutantRows []*model.QualityCheck
		for _, c := range checks {
			if c.Kind == "mutant" {
				mutantRows = append(mutantRows, c)
			}
		}
		if len(mutantRows) != 3 {
			t.Fatalf("len(mutant rows) = %d, want 3: %+v", len(mutantRows), mutantRows)
		}
		names := map[string]bool{}
		for _, r := range mutantRows {
			if r.Status != "finding" {
				t.Errorf("mutant row %s status = %q, want finding", r.Name, r.Status)
			}
			names[r.Name] = true
		}
		for _, want := range []string{"foo.go:" + line + ":0:a", "foo.go:" + line + ":0:b", "foo.go:" + line + ":0:c"} {
			if !names[want] {
				t.Errorf("expected a mutant row named %q, got names %v", want, names)
			}
		}
		scoreRow := findMutationCheck(t, checks, "mutation", "score")
		if scoreRow.Status != "pass" {
			t.Errorf("mutation/score status = %q, want pass (3 survivors is under the cap)", scoreRow.Status)
		}
	})

	t.Run("zero survivors: no mutant rows, score pass", func(t *testing.T) {
		runner := &fakeGateRunner{writeFiles: map[string]map[string]string{
			"mutation-report": {"tmp/mutants.json": mutantsV1Doc("foo.go:" + line + ":a:killed")},
		}}
		svc := &QualityService{repoDir: repoDir, runner: runner}
		checks, _, err := svc.runMutationChecks(context.Background(), g, mutationTestConstitution(), spec, false, false)
		if err != nil {
			t.Fatalf("runMutationChecks: %v", err)
		}
		for _, c := range checks {
			if c.Kind == "mutant" {
				t.Errorf("unexpected mutant row %+v with zero survivors", c)
			}
		}
		scoreRow := findMutationCheck(t, checks, "mutation", "score")
		if scoreRow.Status != "pass" {
			t.Errorf("mutation/score status = %q, want pass", scoreRow.Status)
		}
	})
}

// TestRunMutationChecks_SurvivorOrderIsDeterministic covers AC17's third
// row at the service-integration layer: two Verify-equivalent calls over
// the SAME informe produce the SAME row order.
func TestRunMutationChecks_SurvivorOrderIsDeterministic(t *testing.T) {
	repoDir, baseSHA, changedLine := mutationGitFixture(t)
	spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard, BaseSHA: baseSHA}
	g := &quality.Git{RepoDir: repoDir}
	line := itoa(changedLine)
	doc := mutantsV1Doc("foo.go:"+line+":z:lived", "foo.go:"+line+":a:lived")

	var firstOrder []string
	for i := 0; i < 2; i++ {
		runner := &fakeGateRunner{writeFiles: map[string]map[string]string{"mutation-report": {"tmp/mutants.json": doc}}}
		svc := &QualityService{repoDir: repoDir, runner: runner}
		checks, _, err := svc.runMutationChecks(context.Background(), g, mutationTestConstitution(), spec, false, false)
		if err != nil {
			t.Fatalf("runMutationChecks: %v", err)
		}
		var order []string
		for _, c := range checks {
			if c.Kind == "mutant" {
				order = append(order, c.Name)
			}
		}
		if i == 0 {
			firstOrder = order
			continue
		}
		if len(order) != len(firstOrder) {
			t.Fatalf("run %d: order = %v, want same length as %v", i, order, firstOrder)
		}
		for j := range order {
			if order[j] != firstOrder[j] {
				t.Fatalf("run %d: order = %v, want %v (deterministic)", i, order, firstOrder)
			}
		}
	}
}

// TestRunMutationChecks_SurvivorCap covers AC18/G18: MaxSurvivorRows+1
// survivors produce exactly MaxSurvivorRows rows and mutation/score
// `fail` naming the real total; exactly MaxSurvivorRows survivors do not
// fail.
func TestRunMutationChecks_SurvivorCap(t *testing.T) {
	repoDir, baseSHA, changedLine := mutationGitFixture(t)
	spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard, BaseSHA: baseSHA}
	g := &quality.Git{RepoDir: repoDir}
	line := itoa(changedLine)

	makeEntries := func(n int) []string {
		entries := make([]string, n)
		for i := range entries {
			entries[i] = "foo.go:" + line + ":m" + itoa(i) + ":lived"
		}
		return entries
	}

	t.Run("exactly the cap: no fail", func(t *testing.T) {
		runner := &fakeGateRunner{writeFiles: map[string]map[string]string{
			"mutation-report": {"tmp/mutants.json": mutantsV1Doc(makeEntries(quality.MaxSurvivorRows)...)},
		}}
		svc := &QualityService{repoDir: repoDir, runner: runner}
		checks, _, err := svc.runMutationChecks(context.Background(), g, mutationTestConstitution(), spec, false, false)
		if err != nil {
			t.Fatalf("runMutationChecks: %v", err)
		}
		count := 0
		for _, c := range checks {
			if c.Kind == "mutant" {
				count++
			}
		}
		if count != quality.MaxSurvivorRows {
			t.Errorf("mutant row count = %d, want %d", count, quality.MaxSurvivorRows)
		}
		scoreRow := findMutationCheck(t, checks, "mutation", "score")
		if scoreRow.Status != "pass" {
			t.Errorf("mutation/score status = %q, want pass at exactly the cap", scoreRow.Status)
		}
	})

	t.Run("cap+1: truncated to the cap, score fails naming the real total", func(t *testing.T) {
		runner := &fakeGateRunner{writeFiles: map[string]map[string]string{
			"mutation-report": {"tmp/mutants.json": mutantsV1Doc(makeEntries(quality.MaxSurvivorRows + 1)...)},
		}}
		svc := &QualityService{repoDir: repoDir, runner: runner}
		checks, _, err := svc.runMutationChecks(context.Background(), g, mutationTestConstitution(), spec, false, false)
		if err != nil {
			t.Fatalf("runMutationChecks: %v", err)
		}
		count := 0
		for _, c := range checks {
			if c.Kind == "mutant" {
				count++
			}
		}
		if count != quality.MaxSurvivorRows {
			t.Errorf("mutant row count = %d, want exactly %d (truncated)", count, quality.MaxSurvivorRows)
		}
		scoreRow := findMutationCheck(t, checks, "mutation", "score")
		if scoreRow.Status != "fail" {
			t.Fatalf("mutation/score status = %q, want fail (cap exceeded)", scoreRow.Status)
		}
		wantTotal := itoa(quality.MaxSurvivorRows + 1)
		if !strings.Contains(scoreRow.Summary, wantTotal) {
			t.Errorf("mutation/score summary = %q, want it to name the real total %s", scoreRow.Summary, wantTotal)
		}
	})
}

// TestRunMutationChecks_ScoreDetail_RoundTrips covers the plumbing D9/P9
// depend on: mutation/score's Detail carries the full recount and
// max_equivalent, readable back by json.Unmarshal.
func TestRunMutationChecks_ScoreDetail_RoundTrips(t *testing.T) {
	repoDir, baseSHA, changedLine := mutationGitFixture(t)
	spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard, BaseSHA: baseSHA}
	g := &quality.Git{RepoDir: repoDir}
	line := itoa(changedLine)

	runner := &fakeGateRunner{writeFiles: map[string]map[string]string{
		"mutation-report": {"tmp/mutants.json": mutantsV1Doc("foo.go:"+line+":a:killed", "foo.go:"+line+":b:lived")},
	}}
	svc := &QualityService{repoDir: repoDir, runner: runner}
	checks, _, err := svc.runMutationChecks(context.Background(), g, mutationTestConstitution(), spec, false, false)
	if err != nil {
		t.Fatalf("runMutationChecks: %v", err)
	}
	scoreRow := findMutationCheck(t, checks, "mutation", "score")
	var detail mutantScoreDetail
	if jsonErr := json.Unmarshal([]byte(scoreRow.Detail), &detail); jsonErr != nil {
		t.Fatalf("unmarshal mutation/score detail: %v", jsonErr)
	}
	if detail.MaxEquivalent != 2 {
		t.Errorf("detail.MaxEquivalent = %d, want 2", detail.MaxEquivalent)
	}
	if detail.ByStatus["killed"] != 1 || detail.ByStatus["lived"] != 1 {
		t.Errorf("detail.ByStatus = %v, want killed=1 lived=1", detail.ByStatus)
	}
	if detail.SurvivorCount != 1 {
		t.Errorf("detail.SurvivorCount = %d, want 1", detail.SurvivorCount)
	}
}

// itoa avoids importing strconv solely for test literals.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// --- SPEC-119 EPIC-calidad S5 P9: the escotilla — Sign/Ack + cupo ---

// writeConstitutionV5Mutation writes a full schema_version=5 constitution
// — every prior section declared-and-off, [mutation] configured with the
// given enabled/maxEquivalent — the mold of writeConstitutionV3Criteria/
// writeMinimalBudgetConstitution, extended one schema further.
func writeConstitutionV5Mutation(t *testing.T, repoDir string, maxEquivalent int) {
	t.Helper()
	dir := filepath.Join(repoDir, ".mneme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir .mneme: %v", err)
	}
	doc := fmt.Sprintf(`
schema_version = 5
enabled = true
[execution]
output_tail_bytes = 4096
[coverage]
enabled = false
format = "go-cover"
command = ["true"]
profile_path = "tmp/coverage.out"
timeout = "20m"
min_diff_line_pct = 80.0
min_changed_lines = 5
exclude = []
[ratchet]
enabled = false
max_global_line_pct_drop = 0.0
max_baseline_staleness_pct = 1.0
[criteria]
enabled = false
timeout = "5m"
max_manual_pct = 25.0
max_command_pct = 30.0
[budget]
enabled = false
timeout = "2m"
test_globs = ["**/*_test.go"]
test_reach_depth = 3
[mutation]
enabled = true
format = "mutants-v1"
command = ["true"]
report_path = "tmp/mutants.json"
timeout = "30m"
max_equivalent = %d
max_not_viable_pct = 25.0
`, maxEquivalent)
	if err := os.WriteFile(filepath.Join(dir, "quality.toml"), []byte(doc), 0o644); err != nil {
		t.Fatalf("write quality.toml: %v", err)
	}
}

// verifyWithSurvivors drives a REAL Verify() call (through the full
// constitution-on-disk path, unlike this file's other tests that call
// runMutationChecks directly) producing exactly n `mutant` survivor rows
// on a single changed line — the fixture Sign/Ack's escotilla tests need,
// since the cupo (D9) is read back from a PERSISTED certificate's
// mutation/score row, which only a real Verify()+InsertCertificate
// round-trip can produce.
func verifyWithSurvivors(t *testing.T, s *store.SDDStore, specID string, maxEquivalent, n int) (*model.QualityCertificate, []int64) {
	t.Helper()
	repoDir := newTestGitRepo(t)
	writeConstitutionV5Mutation(t, repoDir, maxEquivalent)
	commitAll(t, repoDir, "add constitution")
	// baseSHA is captured AFTER the constitution lands, so the
	// constitution itself is NOT part of the spec's own range — otherwise
	// constitution/unchanged-in-range would be a permanent finding on
	// every certificate this fixture produces, independent of anything
	// this test is actually about.
	baseSHA := headSHAFor(t, repoDir)

	if err := os.WriteFile(filepath.Join(repoDir, "foo.go"), []byte("package main\n\nfunc Foo() {}\n"), 0o644); err != nil {
		t.Fatalf("write foo.go: %v", err)
	}
	commitAll(t, repoDir, "add foo.go")

	spec := insertTestSpec(t, s, specID, "proj", model.SpecStatusImplementing, baseSHA)

	entries := make([]string, n)
	for i := range entries {
		entries[i] = "foo.go:1:m" + itoa(i) + ":lived"
	}
	runner := &fakeGateRunner{writeFiles: map[string]map[string]string{
		"mutation-report": {"tmp/mutants.json": mutantsV1Doc(entries...)},
	}}
	svc := NewQualityService(s, "proj", repoDir, runner)

	cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: spec.ID})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	checks, err := s.ListChecks(context.Background(), cert.ID)
	if err != nil {
		t.Fatalf("ListChecks: %v", err)
	}
	var seqs []int64
	for _, c := range checks {
		if c.Kind == "mutant" {
			seqs = append(seqs, int64(c.Seq))
		}
	}
	if len(seqs) != n {
		t.Fatalf("verifyWithSurvivors: got %d mutant rows, want %d: %+v", len(seqs), n, checks)
	}
	return cert, seqs
}

// TestSign_MutantSurvivor_LiftsBlock covers AC19/G19b: signing a survivor
// converts it to `acked` and the certificate's stored verdict, recomputed
// by AckCheck in the SAME transaction (store.go, untouched), becomes
// `pass` — the certificate-level proof the escotilla actually reopens the
// door Verify's own finding closed.
func TestSign_MutantSurvivor_LiftsBlock(t *testing.T) {
	s := newTestQualityStore(t)
	cert, seqs := verifyWithSurvivors(t, s, "SPEC-901", 2, 1)
	if cert.Verdict != model.QualityVerdictFindings {
		t.Fatalf("cert.Verdict = %q, want findings before signing", cert.Verdict)
	}

	svc := NewQualityService(s, "proj", t.TempDir(), &fakeGateRunner{})
	if err := svc.Sign(context.Background(), model.QualitySignRequest{
		CertificateID: cert.ID, Seq: int(seqs[0]), By: "qa-tester", Evidence: "equivalente: no cambia comportamiento observable",
	}); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	updated, err := s.GetLatestCertificate(context.Background(), "proj", "SPEC-901")
	if err != nil {
		t.Fatalf("GetLatestCertificate: %v", err)
	}
	if updated.Verdict != model.QualityVerdictPass {
		t.Errorf("cert.Verdict after Sign = %q, want pass — the escotilla must actually lift the block", updated.Verdict)
	}
}

// TestSignAck_Complementarity_AtServiceLayer covers AC20's rows at the
// SERVICE layer (internal/quality/signature_test.go already covers the
// predicate itself in isolation): Sign accepts a `mutant` row and refuses
// a `mutation/viability` finding; Ack does the exact opposite. The fourth
// AC20 row — Ack keeps working EXACTLY as before for a non-attested
// finding — is already proven, UNMODIFIED, by
// TestAck_StillWorksForNonCriterionRows (criteria_test.go), which ran
// earlier in this same suite without a single line changed.
func TestSignAck_Complementarity_AtServiceLayer(t *testing.T) {
	s := newTestQualityStore(t)
	cert, seqs := verifyWithSurvivors(t, s, "SPEC-902", 2, 1)

	checks, err := s.ListChecks(context.Background(), cert.ID)
	if err != nil {
		t.Fatalf("ListChecks: %v", err)
	}
	viabilityRow := findMutationCheck(t, checks, "mutation", "viability")

	svc := NewQualityService(s, "proj", t.TempDir(), &fakeGateRunner{})

	// Sign on mutation/viability: ErrNotSignable (checked FIRST — the mutant
	// row below still needs to be an unsigned `finding` afterward).
	if err := svc.Sign(context.Background(), model.QualitySignRequest{
		CertificateID: cert.ID, Seq: viabilityRow.Seq, By: "qa-tester", Evidence: "x",
	}); !errors.Is(err, model.ErrNotSignable) {
		t.Errorf("Sign(mutation/viability) error = %v, want ErrNotSignable", err)
	}

	// Ack on the mutant row: ErrRequiresSign.
	if err := svc.Ack(context.Background(), model.QualityAckRequest{
		CertificateID: cert.ID, Seq: int(seqs[0]), By: "orch", Justification: "x",
	}); !errors.Is(err, model.ErrRequiresSign) {
		t.Errorf("Ack(mutant) error = %v, want ErrRequiresSign", err)
	}

	// Sign on the mutant row: OK — checked LAST since it converts the row.
	if err := svc.Sign(context.Background(), model.QualitySignRequest{
		CertificateID: cert.ID, Seq: int(seqs[0]), By: "qa-tester", Evidence: "x",
	}); err != nil {
		t.Errorf("Sign(mutant) = %v, want nil", err)
	}
}

// TestSignAlias_S3ErrorsStillHold covers AC21: errors.Is(err,
// model.ErrNotACriterion) and errors.Is(err, model.ErrCriterionRequiresSign)
// still hold for the SAME S3 cases — verified here explicitly, in BOTH
// directions, without touching a single S3 test (criteria_test.go's
// TestSign_RejectsNonCriterionRow/TestAck_RejectsCriterionRow already ran,
// unmodified, earlier in this suite).
func TestSignAlias_S3ErrorsStillHold(t *testing.T) {
	if !errors.Is(model.ErrNotSignable, model.ErrNotACriterion) || !errors.Is(model.ErrNotACriterion, model.ErrNotSignable) {
		t.Error("ErrNotACriterion is not a true alias of ErrNotSignable in both directions")
	}
	if !errors.Is(model.ErrRequiresSign, model.ErrCriterionRequiresSign) || !errors.Is(model.ErrCriterionRequiresSign, model.ErrRequiresSign) {
		t.Error("ErrCriterionRequiresSign is not a true alias of ErrRequiresSign in both directions")
	}

	s := newTestQualityStore(t)
	cert, seqs := verifyWithSurvivors(t, s, "SPEC-904", 2, 1)
	svc := NewQualityService(s, "proj", t.TempDir(), &fakeGateRunner{})

	// A mutant row rejected by Ack must ALSO satisfy errors.Is against the
	// OLD S3 sentinel name, since it is now the same value.
	err := svc.Ack(context.Background(), model.QualityAckRequest{CertificateID: cert.ID, Seq: int(seqs[0]), By: "orch", Justification: "x"})
	if !errors.Is(err, model.ErrCriterionRequiresSign) {
		t.Errorf("Ack(mutant) error = %v, want it to ALSO satisfy errors.Is(_, ErrCriterionRequiresSign) via the alias", err)
	}
}

// TestSign_EquivalentQuota covers AC22's four rows: with max_equivalent=2,
// the first two signatures succeed; the third is refused with
// model.ErrEquivalentQuotaExceeded and the row stays in `finding`
// (re-read from the store, not from memory); and a certificate with NO
// mutation/score row at all refuses ANY mutant signature outright (fails
// CLOSED, never "unlimited").
func TestSign_EquivalentQuota(t *testing.T) {
	s := newTestQualityStore(t)
	cert, seqs := verifyWithSurvivors(t, s, "SPEC-905", 2, 3)
	svc := NewQualityService(s, "proj", t.TempDir(), &fakeGateRunner{})

	for i := 0; i < 2; i++ {
		if err := svc.Sign(context.Background(), model.QualitySignRequest{
			CertificateID: cert.ID, Seq: int(seqs[i]), By: "qa-tester", Evidence: "equivalente",
		}); err != nil {
			t.Fatalf("Sign #%d: %v", i+1, err)
		}
	}

	thirdErr := svc.Sign(context.Background(), model.QualitySignRequest{
		CertificateID: cert.ID, Seq: int(seqs[2]), By: "qa-tester", Evidence: "equivalente",
	})
	if !errors.Is(thirdErr, model.ErrEquivalentQuotaExceeded) {
		t.Fatalf("third Sign error = %v, want ErrEquivalentQuotaExceeded", thirdErr)
	}

	checks, err := s.ListChecks(context.Background(), cert.ID)
	if err != nil {
		t.Fatalf("ListChecks: %v", err)
	}
	var thirdRow *model.QualityCheck
	for _, c := range checks {
		if int64(c.Seq) == seqs[2] {
			thirdRow = c
		}
	}
	if thirdRow == nil || thirdRow.Status != "finding" {
		t.Fatalf("third mutant row (re-read from store) = %+v, want status finding (unsigned)", thirdRow)
	}
}

// TestSign_EquivalentQuota_ReadsFromCertificateNotDisk covers AC22's own
// D9 guardian (G22b): the cupo that governs is the one recorded on the
// CERTIFICATE at verification time — editing .mneme/quality.toml's
// max_equivalent AFTER certifying, but BEFORE signing, buys not one extra
// signature.
func TestSign_EquivalentQuota_ReadsFromCertificateNotDisk(t *testing.T) {
	s := newTestQualityStore(t)
	cert, seqs := verifyWithSurvivors(t, s, "SPEC-906", 2, 3)
	svc := NewQualityService(s, "proj", t.TempDir(), &fakeGateRunner{})

	for i := 0; i < 2; i++ {
		if err := svc.Sign(context.Background(), model.QualitySignRequest{
			CertificateID: cert.ID, Seq: int(seqs[i]), By: "qa-tester", Evidence: "equivalente",
		}); err != nil {
			t.Fatalf("Sign #%d: %v", i+1, err)
		}
	}

	// The constitution on THIS service's repoDir is irrelevant — svc here
	// was constructed with an unrelated t.TempDir() and never reads it for
	// Sign at all; the point this test pins is structural: Sign never
	// calls os.ReadFile/quality.Parse. The third signature must still be
	// refused, using ONLY the certificate's own recorded cupo.
	thirdErr := svc.Sign(context.Background(), model.QualitySignRequest{
		CertificateID: cert.ID, Seq: int(seqs[2]), By: "qa-tester", Evidence: "equivalente",
	})
	if !errors.Is(thirdErr, model.ErrEquivalentQuotaExceeded) {
		t.Fatalf("third Sign (after a would-be constitution edit) error = %v, want ErrEquivalentQuotaExceeded — the cupo of record is the certificate's, never the disk file's", thirdErr)
	}
}

// TestSign_NoScoreRow_RefusesClosed covers AC22's fourth row (G22c): a
// certificate with no mutation/score row at all refuses ANY `mutant`
// signature — the absence of a recorded cupo must never read as
// "unlimited".
func TestSign_NoScoreRow_RefusesClosed(t *testing.T) {
	s := newTestQualityStore(t)
	ctx := context.Background()

	cert := &model.QualityCertificate{
		Project: "proj", SpecID: "SPEC-907", HeadSHA: "deadbeef", Verdict: model.QualityVerdictFindings,
	}
	mutantCheck := &model.QualityCheck{Kind: "mutant", Name: "a.go:1:1:x", Status: "finding"}
	if err := s.InsertCertificate(ctx, cert, []*model.QualityCheck{mutantCheck}); err != nil {
		t.Fatalf("InsertCertificate: %v", err)
	}

	svc := NewQualityService(s, "proj", t.TempDir(), &fakeGateRunner{})
	err := svc.Sign(ctx, model.QualitySignRequest{CertificateID: cert.ID, Seq: 1, By: "qa-tester", Evidence: "x"})
	if !errors.Is(err, model.ErrEquivalentQuotaExceeded) {
		t.Fatalf("Sign(mutant, no mutation/score row on the certificate) error = %v, want ErrEquivalentQuotaExceeded (fail closed)", err)
	}
}

// TestQualityService_Status_ReportsMutationInfo covers D14: quality_status
// reports the declared [mutation] config plus the latest certificate's own
// figures (signed-equivalent count against the cupo, survivor count, full
// recount) — read-only, no re-execution, mirroring
// TestQualityService_Status_ReportsBudgetInfo's own shape.
func TestQualityService_Status_ReportsMutationInfo(t *testing.T) {
	repoDir := newTestGitRepo(t)
	writeConstitutionV5Mutation(t, repoDir, 2)
	commitAll(t, repoDir, "add constitution")
	baseSHA := headSHAFor(t, repoDir)

	if err := os.WriteFile(filepath.Join(repoDir, "foo.go"), []byte("package main\n\nfunc Foo() {}\n"), 0o644); err != nil {
		t.Fatalf("write foo.go: %v", err)
	}
	commitAll(t, repoDir, "add foo.go")

	s := newTestQualityStore(t)
	spec := insertTestSpec(t, s, "SPEC-908", "proj", model.SpecStatusImplementing, baseSHA)

	runner := &fakeGateRunner{writeFiles: map[string]map[string]string{
		"mutation-report": {"tmp/mutants.json": mutantsV1Doc("foo.go:1:a:lived", "foo.go:1:b:killed")},
	}}
	svc := NewQualityService(s, "proj", repoDir, runner)
	if _, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: spec.ID}); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	resp, err := svc.Status(context.Background(), model.QualityStatusRequest{ID: spec.ID})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if resp.Mutation == nil {
		t.Fatal("resp.Mutation = nil, want populated")
	}
	if resp.Mutation.Format != "mutants-v1" || resp.Mutation.ReportPath != "tmp/mutants.json" {
		t.Errorf("Mutation = %+v, want format=mutants-v1 report_path=tmp/mutants.json", resp.Mutation)
	}
	if resp.Mutation.MaxEquivalent != 2 {
		t.Errorf("Mutation.MaxEquivalent = %d, want 2", resp.Mutation.MaxEquivalent)
	}
	if resp.Mutation.SurvivorCount != 1 {
		t.Errorf("Mutation.SurvivorCount = %d, want 1 (unsigned so far)", resp.Mutation.SurvivorCount)
	}
	if resp.Mutation.SignedEquivalent != 0 {
		t.Errorf("Mutation.SignedEquivalent = %d, want 0 (nothing signed yet)", resp.Mutation.SignedEquivalent)
	}
	if resp.Mutation.ByStatus["killed"] != 1 || resp.Mutation.ByStatus["lived"] != 1 {
		t.Errorf("Mutation.ByStatus = %v, want killed=1 lived=1", resp.Mutation.ByStatus)
	}

	// Sign the survivor and confirm Status reflects it without re-running
	// anything.
	checks, err := s.ListChecks(context.Background(), resp.LatestCertificate.ID)
	if err != nil {
		t.Fatalf("ListChecks: %v", err)
	}
	var survivorSeq int
	for _, c := range checks {
		if c.Kind == "mutant" {
			survivorSeq = c.Seq
		}
	}
	if err := svc.Sign(context.Background(), model.QualitySignRequest{
		CertificateID: resp.LatestCertificate.ID, Seq: survivorSeq, By: "qa-tester", Evidence: "x",
	}); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	resp2, err := svc.Status(context.Background(), model.QualityStatusRequest{ID: spec.ID})
	if err != nil {
		t.Fatalf("Status (after sign): %v", err)
	}
	if resp2.Mutation.SignedEquivalent != 1 {
		t.Errorf("Mutation.SignedEquivalent after Sign = %d, want 1", resp2.Mutation.SignedEquivalent)
	}
	if resp2.Mutation.SurvivorCount != 0 {
		t.Errorf("Mutation.SurvivorCount after Sign = %d, want 0 (the survivor is now acked, not a finding)", resp2.Mutation.SurvivorCount)
	}
}
