// Package service — this file tests LaneAudit/runLaneAuditEngine after
// SPEC-118 P11 moved the trivial lane onto the SAME engine the standard
// lane's budget mechanism uses. Every threshold case migrated from the
// deleted internal/lane/audit_test.go (R-F) is represented here, now
// exercised through a REAL git repository instead of a fabricated
// DiffStats — the engine underneath changed from lane.GitDiffer/lane.Audit
// to quality.Git/quality.EvaluateTrivialBudget, and this file is the proof
// the migration preserves behaviour.
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/quality"
)

// gitRunLaneTest mirrors gitRunBudgetTest — local identity, signing off
// (R-C).
func gitRunLaneTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=lane-test", "GIT_AUTHOR_EMAIL=lane-test@example.com",
		"GIT_COMMITTER_NAME=lane-test", "GIT_COMMITTER_EMAIL=lane-test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// laneAuditFixture creates a real git repo with a base commit, then
// applies mutate to produce the HEAD commit whose diff runLaneAuditEngine
// evaluates.
func laneAuditFixture(t *testing.T, mutate func(dir string)) (dir, base string) {
	t.Helper()
	dir = t.TempDir()
	gitRunLaneTest(t, dir, "init", "-b", "main")
	gitRunLaneTest(t, dir, "config", "user.email", "lane-test@example.com")
	gitRunLaneTest(t, dir, "config", "user.name", "lane-test")
	gitRunLaneTest(t, dir, "config", "commit.gpgsign", "false")

	if err := os.MkdirAll(filepath.Join(dir, "internal/store"), 0o755); err != nil {
		t.Fatalf("mkdir internal/store: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal/store/existing.go"), []byte("package store\n\nfunc Existing() {}\n"), 0o644); err != nil {
		t.Fatalf("write existing.go: %v", err)
	}
	gitRunLaneTest(t, dir, "add", ".")
	gitRunLaneTest(t, dir, "commit", "-m", "base")
	base = strings.TrimSpace(gitRunLaneTest(t, dir, "rev-parse", "HEAD"))

	mutate(dir)
	gitRunLaneTest(t, dir, "add", "-A")
	gitRunLaneTest(t, dir, "commit", "-m", "head")
	return dir, base
}

// writeLaneFile is a small helper for laneAuditFixture's mutate closures.
func writeLaneFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// TestRunLaneAuditEngine_Thresholds covers AC26's migrated table (R-F):
// too many files, too many lines, a forbidden path, an out-of-scope file,
// an exported symbol change, and the obligatory clean-pass hermana.
func TestRunLaneAuditEngine_Thresholds(t *testing.T) {
	svc := &SDDService{}

	t.Run("too many files", func(t *testing.T) {
		dir, base := laneAuditFixture(t, func(dir string) {
			for _, name := range []string{"a.go", "b.go", "c.go", "d.go"} {
				writeLaneFile(t, dir, "internal/store/"+name, "package store\n")
			}
		})
		result, breaches, err := svc.runLaneAuditEngine(dir, base, "internal/store/**")
		if err != nil {
			t.Fatalf("runLaneAuditEngine: %v", err)
		}
		if result.Passed {
			t.Error("Passed = true, want false (4 files)")
		}
		if !containsSubstr(breaches, "file count") {
			t.Errorf("breaches = %v, want a file-count breach", breaches)
		}
	})

	t.Run("too many lines", func(t *testing.T) {
		var body strings.Builder
		body.WriteString("package store\n\n")
		for i := 0; i < 25; i++ {
			body.WriteString("var _ = 1\n")
		}
		dir, base := laneAuditFixture(t, func(dir string) {
			writeLaneFile(t, dir, "internal/store/big.go", body.String())
		})
		result, breaches, err := svc.runLaneAuditEngine(dir, base, "internal/store/**")
		if err != nil {
			t.Fatalf("runLaneAuditEngine: %v", err)
		}
		if result.Passed {
			t.Error("Passed = true, want false (>20 lines)")
		}
		if !containsSubstr(breaches, "line count") {
			t.Errorf("breaches = %v, want a line-count breach", breaches)
		}
	})

	t.Run("forbidden path", func(t *testing.T) {
		dir, base := laneAuditFixture(t, func(dir string) {
			writeLaneFile(t, dir, "internal/db/migrations/001.sql", "-- migration\n")
		})
		result, breaches, err := svc.runLaneAuditEngine(dir, base, "")
		if err != nil {
			t.Fatalf("runLaneAuditEngine: %v", err)
		}
		if result.Passed {
			t.Error("Passed = true, want false (forbidden path)")
		}
		if len(result.ForbiddenPaths) != 1 {
			t.Errorf("ForbiddenPaths = %v, want exactly one entry", result.ForbiddenPaths)
		}
		if !containsSubstr(breaches, "forbidden path modified") {
			t.Errorf("breaches = %v, want a forbidden-path breach", breaches)
		}
	})

	t.Run("out of scope", func(t *testing.T) {
		dir, base := laneAuditFixture(t, func(dir string) {
			writeLaneFile(t, dir, "internal/other/x.go", "package other\n")
		})
		result, breaches, err := svc.runLaneAuditEngine(dir, base, "internal/store/**")
		if err != nil {
			t.Fatalf("runLaneAuditEngine: %v", err)
		}
		if result.Passed {
			t.Error("Passed = true, want false (out of scope)")
		}
		if len(result.OutOfScopeFiles) != 1 || result.OutOfScopeFiles[0] != "internal/other/x.go" {
			t.Errorf("OutOfScopeFiles = %v, want exactly [internal/other/x.go]", result.OutOfScopeFiles)
		}
		if !containsSubstr(breaches, "out of scope") {
			t.Errorf("breaches = %v, want an out-of-scope breach", breaches)
		}
	})

	t.Run("exported symbol change", func(t *testing.T) {
		dir, base := laneAuditFixture(t, func(dir string) {
			writeLaneFile(t, dir, "internal/store/pub.go", "package store\n\nfunc NewPublicFunc() {}\n")
		})
		result, breaches, err := svc.runLaneAuditEngine(dir, base, "internal/store/**")
		if err != nil {
			t.Fatalf("runLaneAuditEngine: %v", err)
		}
		if result.Passed {
			t.Error("Passed = true, want false (exported symbol added)")
		}
		if len(result.PublicSymbolChanges) != 1 || result.PublicSymbolChanges[0] != "NewPublicFunc" {
			t.Errorf("PublicSymbolChanges = %v, want exactly [NewPublicFunc]", result.PublicSymbolChanges)
		}
		if !containsSubstr(breaches, "public symbol changed") {
			t.Errorf("breaches = %v, want a public-symbol breach", breaches)
		}
	})

	t.Run("clean spec passes (positive)", func(t *testing.T) {
		dir, base := laneAuditFixture(t, func(dir string) {
			writeLaneFile(t, dir, "internal/store/small.go", "package store\n\nfunc unexportedHelper() {}\n")
		})
		result, breaches, err := svc.runLaneAuditEngine(dir, base, "internal/store/**")
		if err != nil {
			t.Fatalf("runLaneAuditEngine: %v", err)
		}
		if !result.Passed {
			t.Errorf("Passed = false, want true (clean, small, in-scope, unexported): breaches=%v", breaches)
		}
	})
}

// containsSubstr reports whether any element of list contains substr.
func containsSubstr(list []string, substr string) bool {
	for _, s := range list {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

// TestLaneAudit_NoBaseRefReturnsError covers P11 point 4: without
// spec.BaseSHA and without an explicit req.BaseRef, LaneAudit returns a
// clear error instead of guessing a default (the deleted
// GitDiffer.DefaultBaseRef behaviour).
func TestLaneAudit_NoBaseRefReturnsError(t *testing.T) {
	svc, _, repoDir := newTestSDDServiceWithRepoDir(t, "wirvii/mneme")
	ctx := context.Background()

	gitRunLaneTest(t, repoDir, "init", "-b", "main")
	gitRunLaneTest(t, repoDir, "config", "user.email", "lane-test@example.com")
	gitRunLaneTest(t, repoDir, "config", "user.name", "lane-test")
	writeLaneFile(t, repoDir, "README.md", "x\n")
	gitRunLaneTest(t, repoDir, "add", ".")
	gitRunLaneTest(t, repoDir, "commit", "-m", "base")

	// Seeded directly at `audit` status with an EMPTY base_sha — the exact
	// state under test, bypassing the full SpecAdvance lifecycle this test
	// does not need (the same pattern quality_test.go's insertTestSpec
	// already establishes).
	spec := &model.Spec{
		ID: "SPEC-001", Title: "Trivial spec", Status: model.SpecStatusAudit,
		Project: "wirvii/mneme", Lane: model.LaneTrivial, Scope: "internal/store/**", BaseSHA: "",
	}
	if err := svc.store.CreateSpec(ctx, spec); err != nil {
		t.Fatalf("create spec: %v", err)
	}

	_, err := svc.LaneAudit(ctx, model.LaneAuditRequest{ID: spec.ID})
	if err == nil {
		t.Fatal("LaneAudit with no base ref: want error, got nil")
	}
	if !strings.Contains(err.Error(), "no base ref") {
		t.Errorf("error = %q, want it to name the missing base ref", err.Error())
	}
}

// TestLaneAudit_HappyPath_AdvancesToDone covers the full LaneAudit path
// end to end: a clean trivial-lane spec (small, in-scope, unexported)
// advances from audit to done and inserts a passing lane_audits row.
func TestLaneAudit_HappyPath_AdvancesToDone(t *testing.T) {
	svc, _, repoDir := newTestSDDServiceWithRepoDir(t, "wirvii/mneme")
	ctx := context.Background()

	gitRunLaneTest(t, repoDir, "init", "-b", "main")
	gitRunLaneTest(t, repoDir, "config", "user.email", "lane-test@example.com")
	gitRunLaneTest(t, repoDir, "config", "user.name", "lane-test")
	writeLaneFile(t, repoDir, "internal/store/existing.go", "package store\n\nfunc Existing() {}\n")
	gitRunLaneTest(t, repoDir, "add", ".")
	gitRunLaneTest(t, repoDir, "commit", "-m", "base")
	base := strings.TrimSpace(gitRunLaneTest(t, repoDir, "rev-parse", "HEAD"))

	writeLaneFile(t, repoDir, "internal/store/small.go", "package store\n\nfunc unexportedHelper() {}\n")
	gitRunLaneTest(t, repoDir, "add", "-A")
	gitRunLaneTest(t, repoDir, "commit", "-m", "head")

	spec := &model.Spec{
		ID: "SPEC-002", Title: "Trivial spec", Status: model.SpecStatusAudit,
		Project: "wirvii/mneme", Lane: model.LaneTrivial, Scope: "internal/store/**", BaseSHA: base,
	}
	if err := svc.store.CreateSpec(ctx, spec); err != nil {
		t.Fatalf("create spec: %v", err)
	}

	result, err := svc.LaneAudit(ctx, model.LaneAuditRequest{ID: spec.ID})
	if err != nil {
		t.Fatalf("LaneAudit: %v", err)
	}
	if !result.Passed {
		t.Fatalf("Passed = false, want true: %+v", result)
	}

	updated, err := svc.store.GetSpec(ctx, spec.ID)
	if err != nil {
		t.Fatalf("GetSpec: %v", err)
	}
	if updated.Status != model.SpecStatusDone {
		t.Errorf("Status = %q, want done", updated.Status)
	}

	latest, err := svc.store.LatestLaneAudit(ctx, spec.ID)
	if err != nil {
		t.Fatalf("LatestLaneAudit: %v", err)
	}
	if latest == nil || !latest.Passed || latest.BaseSHA != base {
		t.Errorf("LatestLaneAudit = %+v, want Passed=true BaseSHA=%s", latest, base)
	}
}

// writeQuality4BudgetOnConstitution writes a schema_version=4 constitution
// with [budget].enabled = true (and everything else declared-and-off) at
// repoDir/.mneme/quality.toml — the AC25/D12 absorption fixture for
// LaneAudit's own gate (distinct from the ensureCertified-focused
// writeTestConstitutionV4BudgetEnabled in sdd_quality_test.go, kept local
// to this file to avoid a cross-file test-only dependency).
func writeQuality4BudgetOnConstitution(t *testing.T, repoDir string) {
	t.Helper()
	dir := filepath.Join(repoDir, ".mneme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir .mneme: %v", err)
	}
	doc := `
schema_version = 4
enabled = false
[execution]
output_tail_bytes = 4096
[[gate]]
name = "build"
command = ["true"]
timeout = "5m"
required = true
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
enabled = true
timeout = "2m"
test_globs = ["**/*_test.go"]
test_reach_depth = 3
`
	if err := os.WriteFile(filepath.Join(dir, "quality.toml"), []byte(doc), 0o644); err != nil {
		t.Fatalf("write quality.toml: %v", err)
	}
}

// TestLaneAudit_Absorbed_RequiresCertificate covers D12/AC25's own half
// for LaneAudit specifically (point 4 of P12): with [budget].enabled =
// true, LaneAudit refuses without a usable certificate
// (ErrCertificateMissing, naming `mneme quality verify`), and passes once
// one exists.
func TestLaneAudit_Absorbed_RequiresCertificate(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}

	svc, workflowDir, repoDir := newTestSDDServiceWithRepoDir(t, "wirvii/mneme")
	ctx := context.Background()

	gitRunLaneTest(t, repoDir, "init", "-b", "main")
	gitRunLaneTest(t, repoDir, "config", "user.email", "lane-test@example.com")
	gitRunLaneTest(t, repoDir, "config", "user.name", "lane-test")
	writeLaneFile(t, repoDir, "internal/store/existing.go", "package store\n\nfunc Existing() {}\n")
	writeQuality4BudgetOnConstitution(t, repoDir)
	gitRunLaneTest(t, repoDir, "add", ".")
	gitRunLaneTest(t, repoDir, "commit", "-m", "base")
	base := strings.TrimSpace(gitRunLaneTest(t, repoDir, "rev-parse", "HEAD"))

	// A comment-only change: no NEW symbol is created, so the six graph
	// detections (which only ever inspect delta.Created) have nothing to
	// report — the certificate can be genuinely green. Creating a new
	// symbol here would legitimately fire orphan/untested-reach against
	// noopGraphFacts (which reports zero edges/reachability for
	// everything), which is correct detector behaviour, not something
	// this fixture should paper over.
	writeLaneFile(t, repoDir, "internal/store/existing.go", "package store\n\n// Existing does something.\nfunc Existing() {}\n")
	gitRunLaneTest(t, repoDir, "add", "-A")
	gitRunLaneTest(t, repoDir, "commit", "-m", "head")

	spec := &model.Spec{
		ID: "SPEC-003", Title: "Trivial spec", Status: model.SpecStatusAudit,
		Project: "wirvii/mneme", Lane: model.LaneTrivial, Scope: "internal/store/**", BaseSHA: base,
	}
	if err := svc.store.CreateSpec(ctx, spec); err != nil {
		t.Fatalf("create spec: %v", err)
	}

	// No certificate yet — must refuse.
	_, err := svc.LaneAudit(ctx, model.LaneAuditRequest{ID: spec.ID})
	if !errors.Is(err, model.ErrCertificateMissing) {
		t.Fatalf("LaneAudit error = %v, want ErrCertificateMissing", err)
	}

	// Verify to produce a green certificate for HEAD, sharing the SAME
	// store svc itself uses. WithGraphFacts is required for a truly GREEN
	// certificate: without an injected graph, budget/graph-index is a
	// firmable `finding` (D5), which alone keeps the certificate at
	// "findings", never "pass" — CertificateUsable requires "pass"
	// specifically. The fake must also report the CORRECT content hash
	// for the one changed file, or D5's freshness check itself finds it
	// divergent.
	g := &quality.Git{RepoDir: repoDir}
	headContent, ok, ferr := g.FileAtRef("HEAD", "internal/store/existing.go")
	if ferr != nil || !ok {
		t.Fatalf("FileAtRef: ok=%v err=%v", ok, ferr)
	}
	sum := sha256.Sum256(headContent)
	facts := &noopGraphFacts{contentHash: map[string]string{"internal/store/existing.go": hex.EncodeToString(sum[:])}}

	qsvc := NewQualityService(svc.store, "wirvii/mneme", repoDir, &fakeGateRunner{},
		WithWorkflowDir(workflowDir), WithGraphFacts(facts))
	cert, err := qsvc.Verify(ctx, model.QualityVerifyRequest{ID: spec.ID})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if cert.Verdict != model.QualityVerdictPass {
		checks, _ := svc.store.ListChecks(ctx, cert.ID)
		for _, c := range checks {
			t.Logf("check: kind=%s name=%s status=%s summary=%s", c.Kind, c.Name, c.Status, c.Summary)
		}
		t.Fatalf("Verify produced verdict=%q, want pass", cert.Verdict)
	}

	result, err := svc.LaneAudit(ctx, model.LaneAuditRequest{ID: spec.ID})
	if err != nil {
		t.Fatalf("LaneAudit (with certificate): %v", err)
	}
	if !result.Passed {
		t.Errorf("Passed = false, want true: %+v", result)
	}
}

// TestLaneAudit_ScopeBreachFailsIndependentlyOfGreenCertificate covers AC12
// (SPEC-137 D5): with [budget].enabled = true, LaneAudit's own verdict
// comes from EvaluateTrivialBudget via runLaneAuditEngine — a DIFFERENT
// code path than the certificate's DeriveVerdict — so a real out-of-scope
// change must still fail the audit even when the certificate covering the
// exact same commit is `pass`. detection/out-of-radius (the row that
// observes the breach) is kind="detection", whose effect is `measures`
// since SPEC-137 D4/D5: it can report `fail` on that ONE row and the
// certificate still comes out green, because that row never counted
// toward the verdict in the first place — proving the two mechanisms are
// genuinely independent, not "usually agree by coincidence".
func TestLaneAudit_ScopeBreachFailsIndependentlyOfGreenCertificate(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}

	svc, workflowDir, repoDir := newTestSDDServiceWithRepoDir(t, "wirvii/mneme")
	ctx := context.Background()

	gitRunLaneTest(t, repoDir, "init", "-b", "main")
	gitRunLaneTest(t, repoDir, "config", "user.email", "lane-test@example.com")
	gitRunLaneTest(t, repoDir, "config", "user.name", "lane-test")
	writeLaneFile(t, repoDir, "internal/store/existing.go", "package store\n\nfunc Existing() {}\n")
	writeQuality4BudgetOnConstitution(t, repoDir)
	gitRunLaneTest(t, repoDir, "add", ".")
	gitRunLaneTest(t, repoDir, "commit", "-m", "base")
	base := strings.TrimSpace(gitRunLaneTest(t, repoDir, "rev-parse", "HEAD"))

	// The declared scope is internal/store/** (below) — this file is
	// OUTSIDE it, a real scope breach EvaluateRadius must catch.
	writeLaneFile(t, repoDir, "internal/other/new.go", "package other\n\nfunc unexportedHelper() {}\n")
	gitRunLaneTest(t, repoDir, "add", "-A")
	gitRunLaneTest(t, repoDir, "commit", "-m", "head, out of scope")

	spec := &model.Spec{
		ID: "SPEC-004", Title: "Trivial spec", Status: model.SpecStatusAudit,
		Project: "wirvii/mneme", Lane: model.LaneTrivial, Scope: "internal/store/**", BaseSHA: base,
	}
	if err := svc.store.CreateSpec(ctx, spec); err != nil {
		t.Fatalf("create spec: %v", err)
	}

	// No graphFacts injected AT ALL (unlike the sibling test above): the
	// six graph-dependent detections skip as EffectStopped, and
	// detection/out-of-radius needs no graph — it is pure git/scope
	// arithmetic (D9's own "the two that need no graph at all").
	qsvc := NewQualityService(svc.store, "wirvii/mneme", repoDir, &fakeGateRunner{}, WithWorkflowDir(workflowDir))
	cert, err := qsvc.Verify(ctx, model.QualityVerifyRequest{ID: spec.ID})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if cert.Verdict != model.QualityVerdictPass {
		checks, _ := svc.store.ListChecks(ctx, cert.ID)
		for _, c := range checks {
			t.Logf("check: kind=%s name=%s status=%s effect=%s summary=%s", c.Kind, c.Name, c.Status, c.Effect, c.Summary)
		}
		t.Fatalf("Verify produced verdict=%q, want pass (the scope breach is a measures-effect row, it must not block the certificate)", cert.Verdict)
	}

	checks, err := svc.store.ListChecks(ctx, cert.ID)
	if err != nil {
		t.Fatalf("ListChecks: %v", err)
	}
	sawFailingOutOfRadius := false
	for _, c := range checks {
		if c.Kind == "detection" && c.Name == "out-of-radius" {
			if c.Status != "fail" {
				t.Fatalf("detection/out-of-radius status = %q, want fail (the fixture's own breach)", c.Status)
			}
			sawFailingOutOfRadius = true
		}
	}
	if !sawFailingOutOfRadius {
		t.Fatal("no detection/out-of-radius row found — this test proves nothing without it")
	}

	result, err := svc.LaneAudit(ctx, model.LaneAuditRequest{ID: spec.ID})
	if !errors.Is(err, model.ErrAuditFailed) {
		t.Fatalf("LaneAudit (with a green certificate) error = %v, want ErrAuditFailed", err)
	}
	if result.Passed {
		t.Errorf("Passed = true, want false — LaneAudit's own engine must catch the scope breach regardless of the certificate's verdict: %+v", result)
	}
}

// --- SPEC-125: freezing a spec via its archived backlog item ---
//
// These three tests exercise the exact defect the owner's pushback (spec.md
// §3) widened D4 to catch: LaneAudit/LaneOverride reaching `done` — and
// saving a completion memory asserting the work finished — over a spec
// whose backlog item was archived (i.e. the work was discarded). Because
// loadMutableSpec's freeze gate is the FIRST thing each of these three
// verbs does, none of the git/memory/lane_audits side effects below it ever
// run: there is no repo fixture here because LaneAudit/LaneOverride never
// reach the code that would need one.

// TestLaneAudit_Frozen_NoCompletionMemoryNoAuditRow is SPEC-125 AC16/AC17/
// AC18: a frozen spec's LaneAudit call saves no completion memory, inserts
// no lane_audits row, and leaves the spec's status unchanged.
func TestLaneAudit_Frozen_NoCompletionMemoryNoAuditRow(t *testing.T) {
	svc := newTestSDDServiceWithMemory(t, "project")
	ctx := context.Background()

	_, spec := newFrozenSpecFixture(t, svc, ctx, "SPEC-frozen-audit", model.SpecStatusAudit, model.LaneTrivial, "internal/model/*.go", "")

	before, err := svc.memorySvc.Search(ctx, model.SearchRequest{Query: spec.ID, Project: "project"})
	if err != nil {
		t.Fatalf("Search (before): %v", err)
	}

	_, err = svc.LaneAudit(ctx, model.LaneAuditRequest{ID: spec.ID})
	if !errors.Is(err, model.ErrSpecFrozen) {
		t.Fatalf("expected ErrSpecFrozen, got %v", err)
	}

	after, err := svc.memorySvc.Search(ctx, model.SearchRequest{Query: spec.ID, Project: "project"})
	if err != nil {
		t.Fatalf("Search (after): %v", err)
	}
	if len(after.Results) != len(before.Results) {
		t.Errorf("expected no completion memory saved: before=%d after=%d", len(before.Results), len(after.Results))
	}

	latest, err := svc.store.LatestLaneAudit(ctx, spec.ID)
	if err != nil {
		t.Fatalf("LatestLaneAudit: %v", err)
	}
	if latest != nil {
		t.Errorf("expected no lane_audits row for a frozen spec, got %+v", latest)
	}

	updated, err := svc.store.GetSpec(ctx, spec.ID)
	if err != nil {
		t.Fatalf("GetSpec: %v", err)
	}
	if updated.Status != model.SpecStatusAudit {
		t.Errorf("expected status unchanged (audit), got %s", updated.Status)
	}
}

// TestLaneOverride_Frozen_NoMemoriesNoStatusChange is SPEC-125 AC19: a
// frozen spec's LaneOverride call saves neither the override memory nor the
// completion memory, and leaves the spec's status unchanged.
func TestLaneOverride_Frozen_NoMemoriesNoStatusChange(t *testing.T) {
	svc := newTestSDDServiceWithMemory(t, "project")
	ctx := context.Background()

	_, spec := newFrozenSpecFixture(t, svc, ctx, "SPEC-frozen-override", model.SpecStatusAudit, model.LaneTrivial, "internal/model/*.go", "")

	before, err := svc.memorySvc.Search(ctx, model.SearchRequest{Query: spec.ID, Project: "project"})
	if err != nil {
		t.Fatalf("Search (before): %v", err)
	}

	_, err = svc.LaneOverride(ctx, model.LaneOverrideRequest{ID: spec.ID, Reason: "force it through", By: "orchestrator"})
	if !errors.Is(err, model.ErrSpecFrozen) {
		t.Fatalf("expected ErrSpecFrozen, got %v", err)
	}

	after, err := svc.memorySvc.Search(ctx, model.SearchRequest{Query: spec.ID, Project: "project"})
	if err != nil {
		t.Fatalf("Search (after): %v", err)
	}
	if len(after.Results) != len(before.Results) {
		t.Errorf("expected no override/completion memory saved: before=%d after=%d", len(before.Results), len(after.Results))
	}

	updated, err := svc.store.GetSpec(ctx, spec.ID)
	if err != nil {
		t.Fatalf("GetSpec: %v", err)
	}
	if updated.Status != model.SpecStatusAudit {
		t.Errorf("expected status unchanged (audit), got %s", updated.Status)
	}
}

// TestLaneReclassify_Frozen_LaneAndScopeUnchanged is SPEC-125 AC20: the
// direct proof that the freeze gate sits BEFORE any effect — including
// UpdateSpecLaneScope, which LaneReclassify calls eight lines before its
// own status transition (sdd.go). A gate placed after that call would leave
// lane/scope changed even though the transition itself was refused.
func TestLaneReclassify_Frozen_LaneAndScopeUnchanged(t *testing.T) {
	svc := newTestSDDService(t, "project")
	ctx := context.Background()

	_, spec := newFrozenSpecFixture(t, svc, ctx, "SPEC-frozen-reclassify", model.SpecStatusDraft, model.LaneTrivial, "internal/model/*.go", "")

	_, err := svc.LaneReclassify(ctx, model.LaneReclassifyRequest{
		ID: spec.ID, Lane: model.LaneStandard, Scope: "internal/**", By: "orchestrator",
	})
	if !errors.Is(err, model.ErrSpecFrozen) {
		t.Fatalf("expected ErrSpecFrozen, got %v", err)
	}

	updated, err := svc.store.GetSpec(ctx, spec.ID)
	if err != nil {
		t.Fatalf("GetSpec: %v", err)
	}
	if updated.Lane != model.LaneTrivial {
		t.Errorf("expected lane unchanged (trivial), got %s", updated.Lane)
	}
	if updated.Scope != "internal/model/*.go" {
		t.Errorf("expected scope unchanged, got %q", updated.Scope)
	}
}
