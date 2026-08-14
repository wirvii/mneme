package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/quality"
)

// visualTestConstitution returns a Constitution with [visual] declared and
// enabled, two declared targets, nivel 2 OFF by default — the mold of
// mutationTestConstitution().
func visualTestConstitution(targets ...string) *quality.Constitution {
	if len(targets) == 0 {
		targets = []string{"home-light", "home-dark"}
	}
	return &quality.Constitution{
		SchemaVersion: 6, VisualDeclared: true,
		Visual: quality.VisualConfig{
			Enabled: true, Format: "visual-v1", Command: []string{"true"},
			ReportPath: "tmp/visual/report.json", Timeout: 5 * time.Minute,
			Targets: targets, FailOnConsoleError: false, A11yFailImpacts: nil,
			Compare: quality.VisualCompareConfig{Enabled: false},
		},
	}
}

// visualTargetJSON is the shorthand builder for one visual-v1 target
// document, keeping fixtures legible.
type visualTargetJSON struct {
	ID             string
	Rendered       bool
	Error          string
	PageErrors     []string
	ConsoleError   int
	A11yReported   bool
	A11yEngine     string
	A11yViolations []string // "rule:impact" shorthand
}

// visualV1Doc builds a minimal visual-v1 JSON document from a list of
// targets — keeps every test's fixture legible without hand-writing JSON.
func visualV1Doc(targets ...visualTargetJSON) string {
	var b strings.Builder
	b.WriteString(`{"schema":"visual-v1","harness":"toy","harness_version":"0.1","targets":[`)
	for i, t := range targets {
		if i > 0 {
			b.WriteString(",")
		}
		pageErrors, _ := json.Marshal(t.PageErrors)
		fmt.Fprintf(&b, `{"id":%q,"rendered":%v,"error":%q,"page_errors":%s,"console":{"error":%d,"warning":0,"info":0}`,
			t.ID, t.Rendered, t.Error, pageErrors, t.ConsoleError)
		if t.A11yReported {
			var violations strings.Builder
			for j, v := range t.A11yViolations {
				parts := strings.SplitN(v, ":", 2)
				if j > 0 {
					violations.WriteString(",")
				}
				fmt.Fprintf(&violations, `{"rule":%q,"impact":%q,"nodes":1}`, parts[0], parts[1])
			}
			fmt.Fprintf(&b, `,"a11y":{"engine":%q,"engine_version":"1.0","violations":[%s]}`, t.A11yEngine, violations.String())
		}
		b.WriteString("}")
	}
	b.WriteString("]}")
	return b.String()
}

// findVisualCheck locates a (kind, name) row in checks or fails the test —
// distinct name from findMutationCheck/findBudgetCheck so all coexist.
func findVisualCheck(t *testing.T, checks []*model.QualityCheck, kind, name string) *model.QualityCheck {
	t.Helper()
	for _, c := range checks {
		if c.Kind == kind && c.Name == name {
			return c
		}
	}
	t.Fatalf("no %s/%s row in %+v", kind, name, checks)
	return nil
}

// encodeVisualTestPNG is pixel_test.go's own encodeTestPNG, duplicated here
// (same package, different file — Go does not let one _test.go file "see"
// a helper unless it is in the same package, which service is NOT the same
// package as quality) with a fixed base fill and configurable diffCount.
func encodeVisualTestPNG(t *testing.T, w, h, diffCount int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	base := color.RGBA{R: 10, G: 20, B: 30, A: 255}
	diff := color.RGBA{R: 250, G: 5, B: 5, A: 255}
	set := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := base
			if set < diffCount {
				c = diff
				set++
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test PNG: %v", err)
	}
	return buf.Bytes()
}

// gitCmd runs an arbitrary git subcommand against repoDir — used by the
// merge-base fixture below, which needs `checkout -b`/`checkout` (commitAll
// only ever adds+commits).
func gitCmd(t *testing.T, repoDir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// writePNG writes raw PNG bytes at repoDir/relPath, creating parent dirs.
func writePNG(t *testing.T, repoDir, relPath string, data []byte) {
	t.Helper()
	full := filepath.Join(repoDir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
}

// TestRunVisualChecks_SkipReasons covers AC22: gate cascade, schema<6, and
// visual.enabled=false ALL skip every one of the seven rows, with THREE
// DISTINCT summaries, and the fake runner records ZERO invocations —
// proving the cascade UNLIKE mutation's is the STANDARD one (no
// anyGateFailed parameter at all, AC26).
func TestRunVisualChecks_SkipReasons(t *testing.T) {
	spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard}
	g := &quality.Git{RepoDir: t.TempDir()}

	tests := []struct {
		name         string
		constitution *quality.Constitution
		gatesStopped bool
	}{
		{name: "gate cascade stopped (required gate failed)", constitution: visualTestConstitution(), gatesStopped: true},
		{name: "schema < 6 (visual not declared)", constitution: &quality.Constitution{SchemaVersion: 5, VisualDeclared: false}},
		{name: "visual.enabled = false", constitution: &quality.Constitution{SchemaVersion: 6, VisualDeclared: true, Visual: quality.VisualConfig{Enabled: false}}},
	}

	summaries := make(map[string]string, len(tests))
	runner := &fakeGateRunner{}
	svc := &QualityService{runner: runner}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checks, pure, err := svc.runVisualChecks(context.Background(), g, tt.constitution, spec, tt.gatesStopped)
			if err != nil {
				t.Fatalf("runVisualChecks: %v", err)
			}
			if len(checks) != 7 || len(pure) != 7 {
				t.Fatalf("len(checks)=%d len(pure)=%d, want 7 each", len(checks), len(pure))
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
			t.Errorf("skip summary %q is shared by %q and %q — three DISTINCT texts required", summary, name, other)
		}
		seen[summary] = name
	}
}

// TestVisualSkipReason_FourDistinctTexts is G23's own guardian: the four
// texts AC22 requires — schema-anterior, enabled=false, gate-cascade, and
// compare-apagada (built directly, since it is not visualSkipReason's own
// text) — must ALL differ, with SchemaVersion held CONSTANT across the
// first three fixtures so a coincidental difference cannot mask a genuine
// collision (the same discipline TestMutationSkipReason_FourDistinctTexts
// already established).
func TestVisualSkipReason_FourDistinctTexts(t *testing.T) {
	const sameSchema = 6
	notDeclared := visualSkipReason(false, &quality.Constitution{SchemaVersion: sameSchema, VisualDeclared: false})
	disabled := visualSkipReason(false, &quality.Constitution{SchemaVersion: sameSchema, VisualDeclared: true, Visual: quality.VisualConfig{Enabled: false}})
	cascade := visualSkipReason(true, &quality.Constitution{SchemaVersion: sameSchema, VisualDeclared: true, Visual: quality.VisualConfig{Enabled: true}})
	const compareOff = "comparacion apagada (visual.compare.enabled = false)"

	texts := map[string]string{"not-declared": notDeclared, "disabled": disabled, "cascade": cascade, "compare-off": compareOff}
	seen := make(map[string]string, len(texts))
	for name, text := range texts {
		if text == "" {
			t.Fatalf("%s: empty text, want a non-empty skip reason", name)
		}
		if other, dup := seen[text]; dup {
			t.Fatalf("%s and %s produced the IDENTICAL text %q — AC22 requires four distinct texts", name, other, text)
		}
		seen[text] = name
	}
}

// TestVisualSkipReason_AllGatesGreen_Evaluates is the cascade's own
// hermana: with no cascade at all, visualSkipReason returns "" and the
// mechanism proceeds to evaluate — and, unlike mutation, a non-required
// gate sitting in `fail` does NOT stop it either (AC26): visualSkipReason
// takes no anyGateFailed parameter at all, so there is nothing to pass.
func TestVisualSkipReason_AllGatesGreen_Evaluates(t *testing.T) {
	reason := visualSkipReason(false, visualTestConstitution())
	if reason != "" {
		t.Errorf("visualSkipReason(clean) = %q, want empty", reason)
	}
}

// TestRunVisualChecks_ReportFailures covers AC19's ways row 1 fails and the
// one way it passes.
func TestRunVisualChecks_ReportFailures(t *testing.T) {
	validDoc := visualV1Doc(
		visualTargetJSON{ID: "home-light", Rendered: true},
		visualTargetJSON{ID: "home-dark", Rendered: true},
	)

	tests := []struct {
		name        string
		runnerRes   quality.GateResult
		writeReport string
		wantStatus  string
	}{
		{name: "command exits non-zero", runnerRes: quality.GateResult{Status: quality.GateStatusFail, ExitCode: 1}, wantStatus: "fail"},
		{name: "command exits 0 but writes no file", runnerRes: quality.GateResult{Status: quality.GateStatusPass}, wantStatus: "fail"},
		{name: "file exists but is not parseable", runnerRes: quality.GateResult{Status: quality.GateStatusPass}, writeReport: "not json [[[", wantStatus: "fail"},
		{name: "command exits 0 with a valid report", runnerRes: quality.GateResult{Status: quality.GateStatusPass}, writeReport: validDoc, wantStatus: "pass"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoDir := newTestGitRepo(t)
			spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard, BaseSHA: headSHAFor(t, repoDir)}
			g := &quality.Git{RepoDir: repoDir}

			runner := &fakeGateRunner{results: map[string]quality.GateResult{"visual": tt.runnerRes}}
			if tt.writeReport != "" {
				runner.writeFiles = map[string]map[string]string{"visual": {"tmp/visual/report.json": tt.writeReport}}
			}
			svc := &QualityService{repoDir: repoDir, runner: runner}

			checks, _, err := svc.runVisualChecks(context.Background(), g, visualTestConstitution(), spec, false)
			if err != nil {
				t.Fatalf("runVisualChecks: %v", err)
			}
			row := findVisualCheck(t, checks, "visual", "report")
			if row.Status != tt.wantStatus {
				t.Errorf("visual/report status = %q, want %q (summary=%q)", row.Status, tt.wantStatus, row.Summary)
			}
			if tt.wantStatus == "fail" {
				for _, name := range []string{"scope", "render", "console", "a11y", "compare", "reference-drift"} {
					r := findVisualCheck(t, checks, "visual", name)
					if r.Status != "skipped" {
						t.Errorf("visual/%s status = %q, want skipped when report failed", name, r.Status)
					}
				}
			}
		})
	}
}

// TestRunVisualChecks_ReportPathTracked covers the report_path-is-an-output
// guardrail, reusing prepareDeclaredOutput (P7): a tracked report_path is
// refused and the tracked file itself survives untouched.
func TestRunVisualChecks_ReportPathTracked(t *testing.T) {
	repoDir := newTestGitRepo(t)
	writePNG(t, repoDir, "tmp/visual/report.json", []byte("{}")) // content irrelevant, just needs to exist+be tracked
	commitAll(t, repoDir, "track tmp/visual/report.json")

	spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard, BaseSHA: headSHAFor(t, repoDir)}
	g := &quality.Git{RepoDir: repoDir}
	runner := &fakeGateRunner{}
	svc := &QualityService{repoDir: repoDir, runner: runner}

	checks, _, err := svc.runVisualChecks(context.Background(), g, visualTestConstitution(), spec, false)
	if err != nil {
		t.Fatalf("runVisualChecks: %v", err)
	}
	row := findVisualCheck(t, checks, "visual", "report")
	if row.Status != "fail" {
		t.Fatalf("visual/report status = %q, want fail (tracked report_path)", row.Status)
	}
	if !strings.Contains(row.Summary, "informe visual") {
		t.Errorf("visual/report summary = %q, want it to name %q", row.Summary, "informe visual")
	}
	if len(runner.calls) != 0 {
		t.Errorf("runner.calls = %v, want zero — a tracked report_path must never reach the runner", runner.calls)
	}
	if _, statErr := os.Stat(filepath.Join(repoDir, "tmp", "visual", "report.json")); statErr != nil {
		t.Errorf("tracked report file was removed: %v", statErr)
	}
}

// TestRunVisualChecks_StaleReportDeletedBeforeRun covers G16: a stale
// report from a PRIOR run must be deleted before the visual command
// executes.
func TestRunVisualChecks_StaleReportDeletedBeforeRun(t *testing.T) {
	repoDir := newTestGitRepo(t)
	staleDoc := visualV1Doc(visualTargetJSON{ID: "home-light", Rendered: true}, visualTargetJSON{ID: "home-dark", Rendered: true})
	writePNG(t, repoDir, "tmp/visual/report.json", []byte(staleDoc)) // untracked leftover

	spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard, BaseSHA: headSHAFor(t, repoDir)}
	g := &quality.Git{RepoDir: repoDir}
	runner := &fakeGateRunner{} // writes nothing
	svc := &QualityService{repoDir: repoDir, runner: runner}

	checks, _, err := svc.runVisualChecks(context.Background(), g, visualTestConstitution(), spec, false)
	if err != nil {
		t.Fatalf("runVisualChecks: %v", err)
	}
	row := findVisualCheck(t, checks, "visual", "report")
	if row.Status != "fail" {
		t.Fatalf("visual/report status = %q, want fail — the stale report must have been deleted before the (no-op) runner ran", row.Status)
	}
	if _, statErr := os.Stat(filepath.Join(repoDir, "tmp", "visual", "report.json")); !os.IsNotExist(statErr) {
		t.Errorf("stale report still exists (statErr=%v) — prepareDeclaredOutput must delete it before running", statErr)
	}
}

// TestRunVisualChecks_BudgetExceeded covers G17: a timeout is a `finding`
// `budget-exceeded`, DISTINCT from a plain non-zero exit (`fail`).
func TestRunVisualChecks_BudgetExceeded(t *testing.T) {
	repoDir := newTestGitRepo(t)
	spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard, BaseSHA: headSHAFor(t, repoDir)}
	g := &quality.Git{RepoDir: repoDir}
	runner := &fakeGateRunner{results: map[string]quality.GateResult{
		"visual": {Status: quality.GateStatusFail, ExitCode: -1, Summary: "timeout tras 15m0s"},
	}}
	svc := &QualityService{repoDir: repoDir, runner: runner}

	checks, _, err := svc.runVisualChecks(context.Background(), g, visualTestConstitution(), spec, false)
	if err != nil {
		t.Fatalf("runVisualChecks: %v", err)
	}
	row := findVisualCheck(t, checks, "visual", "report")
	if row.Status != "finding" || !strings.Contains(row.Summary, "budget-exceeded") {
		t.Fatalf("visual/report = %+v, want finding naming budget-exceeded", row)
	}
	for _, name := range []string{"scope", "render", "console", "a11y", "compare", "reference-drift"} {
		r := findVisualCheck(t, checks, "visual", name)
		if r.Status != "skipped" {
			t.Errorf("visual/%s status = %q, want skipped after budget-exceeded", name, r.Status)
		}
	}
}

// visualVerifyFixture creates a git repo, a spec whose BaseSHA is the
// repo's initial commit, and a fake runner that "writes" the given report
// document as its side effect — the standard rig every scope/console/a11y/
// compare test below shares.
func visualVerifyFixture(t *testing.T, reportDoc string) (repoDir string, spec *model.Spec, g *quality.Git, runner *fakeGateRunner) {
	t.Helper()
	repoDir = newTestGitRepo(t)
	spec = &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard, BaseSHA: headSHAFor(t, repoDir)}
	g = &quality.Git{RepoDir: repoDir}
	runner = &fakeGateRunner{writeFiles: map[string]map[string]string{"visual": {"tmp/visual/report.json": reportDoc}}}
	return repoDir, spec, g, runner
}

// TestRunVisualChecks_Scope covers G8a/G8b/G8c: a missing declared target
// fails naming it; an extra reported target is a finding naming it; an
// exact match passes.
func TestRunVisualChecks_Scope(t *testing.T) {
	tests := []struct {
		name       string
		targets    []string
		reported   []string
		wantStatus string
		wantNames  []string
	}{
		{name: "missing a declared target (G8a)", targets: []string{"a", "b", "c"}, reported: []string{"a", "b"}, wantStatus: "fail", wantNames: []string{"c"}},
		{name: "extra undeclared target (G8b)", targets: []string{"a", "b"}, reported: []string{"a", "b", "d"}, wantStatus: "finding", wantNames: []string{"d"}},
		{name: "exact match (G8c)", targets: []string{"a", "b"}, reported: []string{"a", "b"}, wantStatus: "pass"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var jsonTargets []visualTargetJSON
			for _, id := range tt.reported {
				jsonTargets = append(jsonTargets, visualTargetJSON{ID: id, Rendered: true})
			}
			doc := visualV1Doc(jsonTargets...)
			repoDir, spec, g, runner := visualVerifyFixture(t, doc)
			svc := &QualityService{repoDir: repoDir, runner: runner}

			checks, _, err := svc.runVisualChecks(context.Background(), g, visualTestConstitution(tt.targets...), spec, false)
			if err != nil {
				t.Fatalf("runVisualChecks: %v", err)
			}
			row := findVisualCheck(t, checks, "visual", "scope")
			if row.Status != tt.wantStatus {
				t.Fatalf("visual/scope status = %q, want %q (summary=%q)", row.Status, tt.wantStatus, row.Summary)
			}
			for _, name := range tt.wantNames {
				if !strings.Contains(row.Summary, name) {
					t.Errorf("visual/scope summary = %q, want it to name %q", row.Summary, name)
				}
			}
		})
	}
}

// TestRunVisualChecks_Console covers G10a/G10b: an uncaught exception fails
// REGARDLESS of fail_on_console_error; console.error only fails WHEN the
// project declared it should.
func TestRunVisualChecks_Console(t *testing.T) {
	doc := visualV1Doc(
		visualTargetJSON{ID: "a", Rendered: true, PageErrors: []string{"TypeError: boom"}},
		visualTargetJSON{ID: "b", Rendered: true, ConsoleError: 3},
	)

	// FailOnConsoleError=false: page error on "a" STILL fails (G10a);
	// console.error on "b" does NOT (G10b).
	repoDir, spec, g, runner := visualVerifyFixture(t, doc)
	svc := &QualityService{repoDir: repoDir, runner: runner}
	cfg := visualTestConstitution("a", "b")
	checks, _, err := svc.runVisualChecks(context.Background(), g, cfg, spec, false)
	if err != nil {
		t.Fatalf("runVisualChecks: %v", err)
	}
	row := findVisualCheck(t, checks, "visual", "console")
	if row.Status != "fail" || !strings.Contains(row.Summary, "a") {
		t.Fatalf("console (fail_on_console_error=false) = %+v, want fail naming 'a' (the page error)", row)
	}
	if strings.Contains(row.Summary, "console.error") {
		t.Errorf("console summary %q should not blame console.error at all when fail_on_console_error=false (G10b)", row.Summary)
	}

	// FailOnConsoleError=true: the SAME report now also fails "b".
	repoDir2, spec2, g2, runner2 := visualVerifyFixture(t, doc)
	svc2 := &QualityService{repoDir: repoDir2, runner: runner2}
	cfg2 := visualTestConstitution("a", "b")
	cfg2.Visual.FailOnConsoleError = true
	checks2, _, err := svc2.runVisualChecks(context.Background(), g2, cfg2, spec2, false)
	if err != nil {
		t.Fatalf("runVisualChecks: %v", err)
	}
	row2 := findVisualCheck(t, checks2, "visual", "console")
	if row2.Status != "fail" || !strings.Contains(row2.Summary, "b") {
		t.Fatalf("console (fail_on_console_error=true) = %+v, want fail naming 'b' too", row2)
	}
}

// TestRunVisualChecks_A11y covers G11a/G11b/G11c at the integration layer.
func TestRunVisualChecks_A11y(t *testing.T) {
	doc := visualV1Doc(
		visualTargetJSON{ID: "minor-only", Rendered: true, A11yReported: true, A11yEngine: "axe", A11yViolations: []string{"r1:minor"}},
		visualTargetJSON{ID: "serious", Rendered: true, A11yReported: true, A11yEngine: "axe", A11yViolations: []string{"r2:serious"}},
		visualTargetJSON{ID: "not-measured", Rendered: true},
	)
	repoDir, spec, g, runner := visualVerifyFixture(t, doc)
	svc := &QualityService{repoDir: repoDir, runner: runner}
	cfg := visualTestConstitution("minor-only", "serious", "not-measured")
	cfg.Visual.A11yFailImpacts = []quality.A11yImpact{quality.A11yCritical, quality.A11ySerious}

	checks, _, err := svc.runVisualChecks(context.Background(), g, cfg, spec, false)
	if err != nil {
		t.Fatalf("runVisualChecks: %v", err)
	}
	row := findVisualCheck(t, checks, "visual", "a11y")
	if row.Status != "fail" {
		t.Fatalf("visual/a11y status = %q, want fail", row.Status)
	}
	if strings.Contains(row.Summary, "minor-only") {
		t.Errorf("a11y summary %q should not name minor-only (G11a: outside declared impacts)", row.Summary)
	}
	if !strings.Contains(row.Summary, "serious") && !strings.Contains(row.Summary, "not-measured") {
		t.Errorf("a11y summary %q should name serious and/or not-measured", row.Summary)
	}
	// Engine/version go into the Detail (D6).
	var detail visualA11yDetail
	if err := json.Unmarshal([]byte(row.Detail), &detail); err != nil {
		t.Fatalf("unmarshal a11y detail: %v", err)
	}
	if detail.ByTarget["minor-only"].Engine != "axe" {
		t.Errorf("a11y detail engine = %q, want axe", detail.ByTarget["minor-only"].Engine)
	}
	if detail.ByTarget["not-measured"].Reported {
		t.Errorf("a11y detail for not-measured: Reported=true, want false")
	}
}

// TestRunVisualChecks_TargetRows covers AC23: three objetivos failing for
// DISTINCT reasons produce exactly three visual-target rows, and a target
// failing for TWO reasons produces ONE row with BOTH.
func TestRunVisualChecks_TargetRows(t *testing.T) {
	doc := visualV1Doc(
		visualTargetJSON{ID: "no-render", Rendered: false, Error: "boom"},
		visualTargetJSON{ID: "throws", Rendered: true, PageErrors: []string{"TypeError: x"}},
		visualTargetJSON{ID: "clean", Rendered: true},
	)
	repoDir, spec, g, runner := visualVerifyFixture(t, doc)
	svc := &QualityService{repoDir: repoDir, runner: runner}
	cfg := visualTestConstitution("no-render", "throws", "clean")

	checks, _, err := svc.runVisualChecks(context.Background(), g, cfg, spec, false)
	if err != nil {
		t.Fatalf("runVisualChecks: %v", err)
	}
	var targetRows []*model.QualityCheck
	for _, c := range checks {
		if c.Kind == "visual-target" {
			targetRows = append(targetRows, c)
		}
	}
	if len(targetRows) != 2 {
		t.Fatalf("len(visual-target rows) = %d, want 2 (no-render, throws) — clean must have NO row", len(targetRows))
	}
	names := map[string]bool{}
	for _, r := range targetRows {
		names[r.Name] = true
	}
	if !names["no-render"] || !names["throws"] {
		t.Errorf("target rows = %v, want no-render and throws", names)
	}
}

// TestRunVisualChecks_TargetRowCap covers G20: MaxVisualTargetRows+1
// failing targets emit exactly MaxVisualTargetRows rows, and visual/render
// names the REAL total in its summary.
func TestRunVisualChecks_TargetRowCap(t *testing.T) {
	n := quality.MaxVisualTargetRows + 1
	var targets []string
	var jsonTargets []visualTargetJSON
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("t%03d", i)
		targets = append(targets, id)
		jsonTargets = append(jsonTargets, visualTargetJSON{ID: id, Rendered: false, Error: "boom"})
	}
	doc := visualV1Doc(jsonTargets...)
	repoDir, spec, g, runner := visualVerifyFixture(t, doc)
	svc := &QualityService{repoDir: repoDir, runner: runner}
	cfg := visualTestConstitution(targets...)

	checks, _, err := svc.runVisualChecks(context.Background(), g, cfg, spec, false)
	if err != nil {
		t.Fatalf("runVisualChecks: %v", err)
	}
	count := 0
	for _, c := range checks {
		if c.Kind == "visual-target" {
			count++
		}
	}
	if count != quality.MaxVisualTargetRows {
		t.Fatalf("emitted %d visual-target rows, want exactly %d", count, quality.MaxVisualTargetRows)
	}
	renderRow := findVisualCheck(t, checks, "visual", "render")
	if !strings.Contains(renderRow.Summary, fmt.Sprintf("%d", n)) {
		t.Errorf("visual/render summary = %q, want it to name the real total %d", renderRow.Summary, n)
	}
}

// TestRunVisualChecks_CompareOff_DoesNotAffectLevel1 covers G24: with
// [visual.compare].enabled=false, rows 6-7 are skipped with THEIR OWN text,
// but rows 1-5 are evaluated EXACTLY as if compare did not exist.
func TestRunVisualChecks_CompareOff_DoesNotAffectLevel1(t *testing.T) {
	doc := visualV1Doc(visualTargetJSON{ID: "a", Rendered: true}, visualTargetJSON{ID: "b", Rendered: true})
	repoDir, spec, g, runner := visualVerifyFixture(t, doc)
	svc := &QualityService{repoDir: repoDir, runner: runner}
	cfg := visualTestConstitution("a", "b") // Compare.Enabled defaults false

	checks, _, err := svc.runVisualChecks(context.Background(), g, cfg, spec, false)
	if err != nil {
		t.Fatalf("runVisualChecks: %v", err)
	}
	for _, name := range []string{"report", "scope", "render", "console", "a11y"} {
		row := findVisualCheck(t, checks, "visual", name)
		if row.Status == "skipped" {
			t.Errorf("visual/%s status = skipped, want evaluated (compare being off must not disable nivel 1)", name)
		}
	}
	for _, name := range []string{"compare", "reference-drift"} {
		row := findVisualCheck(t, checks, "visual", name)
		if row.Status != "skipped" {
			t.Errorf("visual/%s status = %q, want skipped (compare off)", name, row.Status)
		}
	}
}

// TestRunVisualChecks_ReferenceMissing covers G18a/G18b (AC14): a missing
// reference is a grouped `finding`, never a `fail`, and produces NO
// visual-target row.
func TestRunVisualChecks_ReferenceMissing(t *testing.T) {
	doc := visualV1Doc(visualTargetJSON{ID: "a", Rendered: true})
	repoDir, spec, g, runner := visualVerifyFixture(t, doc)
	// Write the CAPTURE but never the reference.
	writePNG(t, repoDir, "tmp/visual/captures/a.png", encodeVisualTestPNG(t, 10, 10, 0))

	svc := &QualityService{repoDir: repoDir, runner: runner}
	cfg := visualTestConstitution("a")
	cfg.Visual.Compare = quality.VisualCompareConfig{Enabled: true, ReferenceDir: ".mneme/visual/reference", CaptureDir: "tmp/visual/captures", MaxDiffPct: 0.1}

	checks, _, err := svc.runVisualChecks(context.Background(), g, cfg, spec, false)
	if err != nil {
		t.Fatalf("runVisualChecks: %v", err)
	}
	row := findVisualCheck(t, checks, "visual", "compare")
	if row.Status != "finding" || !strings.Contains(row.Summary, "reference-missing") {
		t.Fatalf("visual/compare = %+v, want finding naming reference-missing", row)
	}
	for _, c := range checks {
		if c.Kind == "visual-target" {
			t.Errorf("unexpected visual-target row %+v — a missing reference must be GROUPED, never its own row (D8)", c)
		}
	}
}

// TestRunVisualChecks_CaptureMissing covers G18c: a missing CAPTURE (with
// comparison on) is `fail`, WITH its own visual-target row — the opposite
// severity of a missing reference.
func TestRunVisualChecks_CaptureMissing(t *testing.T) {
	doc := visualV1Doc(visualTargetJSON{ID: "a", Rendered: true})
	repoDir, spec, g, runner := visualVerifyFixture(t, doc)
	writePNG(t, repoDir, ".mneme/visual/reference/a.png", encodeVisualTestPNG(t, 10, 10, 0))
	// No capture written.

	svc := &QualityService{repoDir: repoDir, runner: runner}
	cfg := visualTestConstitution("a")
	cfg.Visual.Compare = quality.VisualCompareConfig{Enabled: true, ReferenceDir: ".mneme/visual/reference", CaptureDir: "tmp/visual/captures", MaxDiffPct: 0.1}

	checks, _, err := svc.runVisualChecks(context.Background(), g, cfg, spec, false)
	if err != nil {
		t.Fatalf("runVisualChecks: %v", err)
	}
	row := findVisualCheck(t, checks, "visual", "compare")
	if row.Status != "fail" {
		t.Fatalf("visual/compare status = %q, want fail (missing capture)", row.Status)
	}
	found := false
	for _, c := range checks {
		if c.Kind == "visual-target" && c.Name == "a" {
			found = true
		}
	}
	if !found {
		t.Errorf("no visual-target/a row — a missing CAPTURE must have its own row (G18c)")
	}
}

// TestRunVisualChecks_StaleCaptureDeletedBeforeRun covers G16: a capture
// left over from a PRIOR run — one that would otherwise MATCH the
// reference — must be deleted before the harness runs, so a fake runner
// that produces nothing this time leaves the row `fail` on a missing
// capture, never a false `pass` reading the stale file's contents.
func TestRunVisualChecks_StaleCaptureDeletedBeforeRun(t *testing.T) {
	doc := visualV1Doc(visualTargetJSON{ID: "a", Rendered: true})
	repoDir, spec, g, runner := visualVerifyFixture(t, doc) // writes only the report
	refBytes := encodeVisualTestPNG(t, 10, 10, 0)
	writePNG(t, repoDir, ".mneme/visual/reference/a.png", refBytes)
	// A STALE capture, identical to the reference — if not deleted before
	// the (no-op) runner "runs", the comparison would read it and pass.
	writePNG(t, repoDir, "tmp/visual/captures/a.png", refBytes)

	svc := &QualityService{repoDir: repoDir, runner: runner}
	cfg := visualTestConstitution("a")
	cfg.Visual.Compare = quality.VisualCompareConfig{Enabled: true, ReferenceDir: ".mneme/visual/reference", CaptureDir: "tmp/visual/captures", MaxDiffPct: 0.1}

	checks, _, err := svc.runVisualChecks(context.Background(), g, cfg, spec, false)
	if err != nil {
		t.Fatalf("runVisualChecks: %v", err)
	}
	row := findVisualCheck(t, checks, "visual", "compare")
	if row.Status != "fail" {
		t.Fatalf("visual/compare status = %q, want fail — the stale capture must have been deleted before the (no-op) runner ran", row.Status)
	}
	if _, statErr := os.Stat(filepath.Join(repoDir, "tmp/visual/captures/a.png")); !os.IsNotExist(statErr) {
		t.Errorf("stale capture still exists (statErr=%v) — prepareDeclaredOutput must delete it before running", statErr)
	}
}

// TestRunVisualChecks_PixelComparison covers D7/AC13 at the integration
// layer: identical images pass; a captured image over max_diff_pct fails
// with its own row; and the versioned reference is NEVER touched.
func TestRunVisualChecks_PixelComparison(t *testing.T) {
	const w, h = 40, 50 // 2000 pixels: 1 diff pixel = 0.05%, 4 = 0.2%

	tests := []struct {
		name       string
		diffPixels int
		wantStatus string
	}{
		{"identical images pass", 0, "pass"},
		{"0.05%% under 0.1%% tolerance passes", 1, "pass"},
		{"0.2%% over 0.1%% tolerance fails", 4, "fail"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := visualV1Doc(visualTargetJSON{ID: "a", Rendered: true})
			repoDir, spec, g, runner := visualVerifyFixture(t, doc)
			refBytes := encodeVisualTestPNG(t, w, h, 0)
			writePNG(t, repoDir, ".mneme/visual/reference/a.png", refBytes)
			// The CAPTURE is the harness's own OUTPUT (D4): mneme deletes
			// any pre-existing file at this path before the runner "runs",
			// so it must arrive as a side effect of running — exactly like
			// the report itself — never pre-seeded on disk.
			runner.writeFiles["visual"]["tmp/visual/captures/a.png"] = string(encodeVisualTestPNG(t, w, h, tt.diffPixels))

			svc := &QualityService{repoDir: repoDir, runner: runner}
			cfg := visualTestConstitution("a")
			cfg.Visual.Compare = quality.VisualCompareConfig{Enabled: true, ReferenceDir: ".mneme/visual/reference", CaptureDir: "tmp/visual/captures", MaxDiffPct: 0.1}

			checks, _, err := svc.runVisualChecks(context.Background(), g, cfg, spec, false)
			if err != nil {
				t.Fatalf("runVisualChecks: %v", err)
			}
			row := findVisualCheck(t, checks, "visual", "compare")
			if row.Status != tt.wantStatus {
				t.Fatalf("visual/compare status = %q, want %q (summary=%q)", row.Status, tt.wantStatus, row.Summary)
			}

			// The versioned reference must survive byte for byte (AC20).
			gotRef, readErr := os.ReadFile(filepath.Join(repoDir, ".mneme/visual/reference/a.png"))
			if readErr != nil {
				t.Fatalf("read back reference: %v", readErr)
			}
			if !bytes.Equal(gotRef, refBytes) {
				t.Errorf("reference image was modified — mneme must NEVER write a reference (D8)")
			}
		})
	}
}

// TestRunVisualChecks_ReferenceNeverDeletedWhenTracked covers G15, the
// fourth-most-important guardian of the spec: a reference file that IS
// tracked by git must survive a Verify run untouched — proven over a REAL
// repository with the reference actually committed, so IsTracked's own
// answer is real, not stubbed.
func TestRunVisualChecks_ReferenceNeverDeletedWhenTracked(t *testing.T) {
	doc := visualV1Doc(visualTargetJSON{ID: "a", Rendered: true})
	repoDir, spec, g, runner := visualVerifyFixture(t, doc)
	refBytes := encodeVisualTestPNG(t, 10, 10, 0)
	writePNG(t, repoDir, ".mneme/visual/reference/a.png", refBytes)
	commitAll(t, repoDir, "commit reference a.png")
	runner.writeFiles["visual"]["tmp/visual/captures/a.png"] = string(refBytes) // identical capture

	svc := &QualityService{repoDir: repoDir, runner: runner}
	cfg := visualTestConstitution("a")
	cfg.Visual.Compare = quality.VisualCompareConfig{Enabled: true, ReferenceDir: ".mneme/visual/reference", CaptureDir: "tmp/visual/captures", MaxDiffPct: 0.1}

	if _, _, err := svc.runVisualChecks(context.Background(), g, cfg, spec, false); err != nil {
		t.Fatalf("runVisualChecks: %v", err)
	}
	got, readErr := os.ReadFile(filepath.Join(repoDir, ".mneme/visual/reference/a.png"))
	if readErr != nil {
		t.Fatalf("reference disappeared: %v", readErr)
	}
	if !bytes.Equal(got, refBytes) {
		t.Errorf("tracked reference was modified")
	}
}

// TestRunVisualChecks_ReferenceDrift covers G13b/G14/AC15/AC16/AC18: base
// unknown is a finding (never pass); a reference modified within the range
// (anchored on the MERGE-BASE, never spec.BaseSHA raw) is a finding naming
// it; an untouched reference passes.
func TestRunVisualChecks_ReferenceDrift(t *testing.T) {
	t.Run("base-unknown: empty BaseSHA", func(t *testing.T) {
		doc := visualV1Doc(visualTargetJSON{ID: "a", Rendered: true})
		repoDir, spec, g, runner := visualVerifyFixture(t, doc)
		spec.BaseSHA = ""
		writePNG(t, repoDir, ".mneme/visual/reference/a.png", encodeVisualTestPNG(t, 10, 10, 0))
		runner.writeFiles["visual"]["tmp/visual/captures/a.png"] = string(encodeVisualTestPNG(t, 10, 10, 0))
		svc := &QualityService{repoDir: repoDir, runner: runner}
		cfg := visualTestConstitution("a")
		cfg.Visual.Compare = quality.VisualCompareConfig{Enabled: true, ReferenceDir: ".mneme/visual/reference", CaptureDir: "tmp/visual/captures", MaxDiffPct: 0.1}

		checks, _, err := svc.runVisualChecks(context.Background(), g, cfg, spec, false)
		if err != nil {
			t.Fatalf("runVisualChecks: %v", err)
		}
		row := findVisualCheck(t, checks, "visual", "reference-drift")
		if row.Status != "finding" || !strings.Contains(row.Summary, "base-unknown") {
			t.Fatalf("reference-drift = %+v, want finding naming base-unknown", row)
		}
	})

	t.Run("reference changed in range is a finding naming it", func(t *testing.T) {
		repoDir := newTestGitRepo(t)
		writePNG(t, repoDir, ".mneme/visual/reference/a.png", encodeVisualTestPNG(t, 10, 10, 0))
		commitAll(t, repoDir, "add reference a.png")
		base := headSHAFor(t, repoDir)

		writePNG(t, repoDir, ".mneme/visual/reference/a.png", encodeVisualTestPNG(t, 10, 10, 5))
		commitAll(t, repoDir, "change reference a.png")

		spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard, BaseSHA: base}
		g := &quality.Git{RepoDir: repoDir}
		doc := visualV1Doc(visualTargetJSON{ID: "a", Rendered: true})
		runner := &fakeGateRunner{writeFiles: map[string]map[string]string{"visual": {
			"tmp/visual/report.json":    doc,
			"tmp/visual/captures/a.png": string(encodeVisualTestPNG(t, 10, 10, 5)),
		}}}
		svc := &QualityService{repoDir: repoDir, runner: runner}
		cfg := visualTestConstitution("a")
		cfg.Visual.Compare = quality.VisualCompareConfig{Enabled: true, ReferenceDir: ".mneme/visual/reference", CaptureDir: "tmp/visual/captures", MaxDiffPct: 100}

		checks, _, err := svc.runVisualChecks(context.Background(), g, cfg, spec, false)
		if err != nil {
			t.Fatalf("runVisualChecks: %v", err)
		}
		row := findVisualCheck(t, checks, "visual", "reference-drift")
		if row.Status != "finding" || !strings.Contains(row.Summary, "reference-changed-in-range") || !strings.Contains(row.Summary, "a.png") {
			t.Fatalf("reference-drift = %+v, want finding naming reference-changed-in-range and a.png", row)
		}
	})

	t.Run("untouched reference passes", func(t *testing.T) {
		repoDir := newTestGitRepo(t)
		writePNG(t, repoDir, ".mneme/visual/reference/a.png", encodeVisualTestPNG(t, 10, 10, 0))
		commitAll(t, repoDir, "add reference a.png")
		base := headSHAFor(t, repoDir)

		// A commit that touches something UNRELATED, in range.
		writePNG(t, repoDir, "unrelated.txt", []byte("hello"))
		commitAll(t, repoDir, "unrelated change")

		spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard, BaseSHA: base}
		g := &quality.Git{RepoDir: repoDir}
		doc := visualV1Doc(visualTargetJSON{ID: "a", Rendered: true})
		runner := &fakeGateRunner{writeFiles: map[string]map[string]string{"visual": {
			"tmp/visual/report.json":    doc,
			"tmp/visual/captures/a.png": string(encodeVisualTestPNG(t, 10, 10, 0)),
		}}}
		svc := &QualityService{repoDir: repoDir, runner: runner}
		cfg := visualTestConstitution("a")
		cfg.Visual.Compare = quality.VisualCompareConfig{Enabled: true, ReferenceDir: ".mneme/visual/reference", CaptureDir: "tmp/visual/captures", MaxDiffPct: 0.1}

		checks, _, err := svc.runVisualChecks(context.Background(), g, cfg, spec, false)
		if err != nil {
			t.Fatalf("runVisualChecks: %v", err)
		}
		row := findVisualCheck(t, checks, "visual", "reference-drift")
		if row.Status != "pass" {
			t.Fatalf("reference-drift status = %q, want pass (reference untouched)", row.Status)
		}
	})
}

// TestRunVisualChecks_ReferenceDrift_AnchoredOnMergeBase is G13b's own
// dedicated guardian: the ONE fixture shape where MergeBase(baseSHA, HEAD)
// genuinely differs from baseSHA itself — baseSHA is NOT an ancestor of
// HEAD (a sibling branch's tip, mirroring SPEC-116's own discovery that a
// fork-then-merge-main fixture is a no-op for this purpose: when baseSHA IS
// an ancestor, merge-base(baseSHA, HEAD) always equals baseSHA exactly).
//
// Shape: a common ancestor commit; a SIBLING branch that changes the
// reference; and HEAD, which continues from the common ancestor WITHOUT
// ever merging the sibling's change. spec.BaseSHA is set to the sibling's
// tip. Anchored correctly on MergeBase(sibling tip, HEAD) — the common
// ancestor — the range never touches the reference (pass). Anchored
// incorrectly on the raw baseSHA (the sibling tip itself), the range
// diffs the SIBLING's tree (with the CHANGED reference) against HEAD's
// tree (with the ORIGINAL reference) and reports a false drift.
func TestRunVisualChecks_ReferenceDrift_AnchoredOnMergeBase(t *testing.T) {
	repoDir := newTestGitRepo(t)
	writePNG(t, repoDir, ".mneme/visual/reference/a.png", encodeVisualTestPNG(t, 10, 10, 0))
	commitAll(t, repoDir, "add reference a.png")
	commonAncestor := headSHAFor(t, repoDir)

	// Sibling branch: changes the reference, never merged into main.
	gitCmd(t, repoDir, "checkout", "-b", "sibling")
	writePNG(t, repoDir, ".mneme/visual/reference/a.png", encodeVisualTestPNG(t, 10, 10, 5))
	commitAll(t, repoDir, "sibling changes reference a.png")
	siblingTip := headSHAFor(t, repoDir)

	// Back to main, which never sees the sibling's change — an unrelated
	// commit is what THIS branch's own history actually adds.
	gitCmd(t, repoDir, "checkout", "main")
	writePNG(t, repoDir, "unrelated.txt", []byte("hello"))
	commitAll(t, repoDir, "unrelated change on main")

	spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard, BaseSHA: siblingTip}
	g := &quality.Git{RepoDir: repoDir}
	doc := visualV1Doc(visualTargetJSON{ID: "a", Rendered: true})
	runner := &fakeGateRunner{writeFiles: map[string]map[string]string{"visual": {
		"tmp/visual/report.json":    doc,
		"tmp/visual/captures/a.png": string(encodeVisualTestPNG(t, 10, 10, 0)),
	}}}
	svc := &QualityService{repoDir: repoDir, runner: runner}
	cfg := visualTestConstitution("a")
	cfg.Visual.Compare = quality.VisualCompareConfig{Enabled: true, ReferenceDir: ".mneme/visual/reference", CaptureDir: "tmp/visual/captures", MaxDiffPct: 0.1}

	checks, _, err := svc.runVisualChecks(context.Background(), g, cfg, spec, false)
	if err != nil {
		t.Fatalf("runVisualChecks: %v", err)
	}
	row := findVisualCheck(t, checks, "visual", "reference-drift")
	if row.Status != "pass" {
		t.Fatalf("reference-drift status = %q (summary=%q), want pass — anchored on MergeBase(%s, HEAD) = %s, which never touches the reference",
			row.Status, row.Summary, siblingTip, commonAncestor)
	}
}

// TestRunVisualChecks_Cascade covers AC26: a required gate in `fail` stops
// the visual command from ever running (zero invocations); with no cascade
// it runs (one invocation) — and, unlike mutation, a non-required gate
// alone sitting in `fail` is simply not a parameter this function accepts,
// so there is nothing that could stop it that way.
func TestRunVisualChecks_Cascade(t *testing.T) {
	doc := visualV1Doc(visualTargetJSON{ID: "a", Rendered: true}, visualTargetJSON{ID: "b", Rendered: true})

	t.Run("gatesStopped=true: zero invocations", func(t *testing.T) {
		repoDir, spec, g, runner := visualVerifyFixture(t, doc)
		svc := &QualityService{repoDir: repoDir, runner: runner}
		if _, _, err := svc.runVisualChecks(context.Background(), g, visualTestConstitution("a", "b"), spec, true); err != nil {
			t.Fatalf("runVisualChecks: %v", err)
		}
		if len(runner.calls) != 0 {
			t.Fatalf("runner.calls = %v, want zero when gatesStopped", runner.calls)
		}
	})

	t.Run("gatesStopped=false: one invocation, evaluated", func(t *testing.T) {
		repoDir, spec, g, runner := visualVerifyFixture(t, doc)
		svc := &QualityService{repoDir: repoDir, runner: runner}
		checks, _, err := svc.runVisualChecks(context.Background(), g, visualTestConstitution("a", "b"), spec, false)
		if err != nil {
			t.Fatalf("runVisualChecks: %v", err)
		}
		if len(runner.calls) != 1 {
			t.Fatalf("runner.calls = %v, want exactly one invocation", runner.calls)
		}
		row := findVisualCheck(t, checks, "visual", "report")
		if row.Status != "pass" {
			t.Errorf("visual/report status = %q, want pass", row.Status)
		}
	})
}

// writeConstitutionV6Visual writes a full schema_version=6 constitution —
// every prior section declared-and-off, [visual] configured with the given
// enabled/targets/command/compare-enabled — the mold of
// writeConstitutionV5Mutation, extended one schema further.
func writeConstitutionV6Visual(t *testing.T, repoDir string, targets []string, compareEnabled bool) {
	t.Helper()
	dir := filepath.Join(repoDir, ".mneme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir .mneme: %v", err)
	}
	quotedTargets := make([]string, len(targets))
	for i, id := range targets {
		quotedTargets[i] = fmt.Sprintf("%q", id)
	}
	doc := fmt.Sprintf(`
schema_version = 6
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
enabled = false
format = "mutants-v1"
command = ["true"]
report_path = "tmp/mutants.json"
timeout = "30m"
max_equivalent = 0
max_not_viable_pct = 25.0
[visual]
enabled = true
format = "visual-v1"
command = ["true"]
report_path = "tmp/visual/report.json"
timeout = "15m"
targets = [%s]
fail_on_console_error = false
a11y_fail_impacts = []
[visual.compare]
enabled = %v
reference_dir = ".mneme/visual/reference"
capture_dir = "tmp/visual/captures"
max_diff_pct = 0.1
`, strings.Join(quotedTargets, ", "), compareEnabled)
	if err := os.WriteFile(filepath.Join(dir, "quality.toml"), []byte(doc), 0o644); err != nil {
		t.Fatalf("write quality.toml: %v", err)
	}
}

// TestQualityService_Verify_Visual_RenderFailBlocks is G21's own guardian
// at the CERTIFICATE level (AC25): a target that fails to render degrades
// the whole certificate's verdict to `fail` via DeriveVerdict (verdict.go,
// untouched) — the mechanism's entire reason to exist is that this, and
// only this, is what SpecAdvance's ensureCertified later checks.
func TestQualityService_Verify_Visual_RenderFailBlocks(t *testing.T) {
	repoDir := newTestGitRepo(t)
	writeConstitutionV6Visual(t, repoDir, []string{"a"}, false)
	commitAll(t, repoDir, "add constitution")
	baseSHA := headSHAFor(t, repoDir)

	s := newTestQualityStore(t)
	spec := insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, baseSHA)

	doc := visualV1Doc(visualTargetJSON{ID: "a", Rendered: false, Error: "boom"})
	runner := &fakeGateRunner{writeFiles: map[string]map[string]string{"visual": {"tmp/visual/report.json": doc}}}
	svc := NewQualityService(s, "proj", repoDir, runner)

	cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: spec.ID})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if cert.Verdict != model.QualityVerdictFail {
		t.Fatalf("Verdict = %q, want fail (a target that never renders must block)", cert.Verdict)
	}
}

// TestQualityService_Verify_Visual_CleanReportPasses is the hermana: a
// clean report with matching declared targets produces a `pass` verdict.
func TestQualityService_Verify_Visual_CleanReportPasses(t *testing.T) {
	repoDir := newTestGitRepo(t)
	writeConstitutionV6Visual(t, repoDir, []string{"a"}, false)
	commitAll(t, repoDir, "add constitution")
	baseSHA := headSHAFor(t, repoDir)

	s := newTestQualityStore(t)
	spec := insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, baseSHA)

	doc := visualV1Doc(visualTargetJSON{ID: "a", Rendered: true})
	runner := &fakeGateRunner{writeFiles: map[string]map[string]string{"visual": {"tmp/visual/report.json": doc}}}
	svc := NewQualityService(s, "proj", repoDir, runner)

	cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: spec.ID})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if cert.Verdict != model.QualityVerdictPass {
		t.Fatalf("Verdict = %q, want pass", cert.Verdict)
	}
}

// TestQualityService_Verify_Visual_ReferenceMissingFinding_AckLiftsBlock
// covers AC14/AC25's own "finding, not fail, and ack-able" guardian: a
// missing reference blocks the certificate (`findings`, never `pass`)
// until a human Acks the row — the SAME store.AckCheck (untouched) every
// other mechanism's findings already use.
func TestQualityService_Verify_Visual_ReferenceMissingFinding_AckLiftsBlock(t *testing.T) {
	repoDir := newTestGitRepo(t)
	writeConstitutionV6Visual(t, repoDir, []string{"a"}, true)
	commitAll(t, repoDir, "add constitution")
	baseSHA := headSHAFor(t, repoDir)

	s := newTestQualityStore(t)
	spec := insertTestSpec(t, s, "SPEC-1", "proj", model.SpecStatusImplementing, baseSHA)

	doc := visualV1Doc(visualTargetJSON{ID: "a", Rendered: true})
	runner := &fakeGateRunner{writeFiles: map[string]map[string]string{"visual": {
		"tmp/visual/report.json":    doc,
		"tmp/visual/captures/a.png": string(encodeVisualTestPNG(t, 10, 10, 0)),
	}}}
	svc := NewQualityService(s, "proj", repoDir, runner)

	cert, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: spec.ID})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if cert.Verdict != model.QualityVerdictFindings {
		t.Fatalf("Verdict = %q, want findings (reference-missing, unsigned)", cert.Verdict)
	}

	checks, err := s.ListChecks(context.Background(), cert.ID)
	if err != nil {
		t.Fatalf("ListChecks: %v", err)
	}
	row := findVisualCheck(t, checks, "visual", "compare")
	if row.Status != "finding" {
		t.Fatalf("visual/compare status = %q, want finding", row.Status)
	}

	if err := svc.Ack(context.Background(), model.QualityAckRequest{
		CertificateID: cert.ID, Seq: row.Seq, By: "orchestrator", Justification: "referencia se commiteara en el mismo PR",
	}); err != nil {
		t.Fatalf("Ack: %v", err)
	}

	updated, err := s.GetLatestCertificate(context.Background(), "proj", spec.ID)
	if err != nil {
		t.Fatalf("GetLatestCertificate: %v", err)
	}
	if updated.Verdict != model.QualityVerdictPass {
		t.Fatalf("Verdict after Ack = %q, want pass", updated.Verdict)
	}
}
