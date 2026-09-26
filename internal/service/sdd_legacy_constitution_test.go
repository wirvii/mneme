// Package service — this file covers SPEC-158 AC1/AC2: a legacy
// .mneme/quality.toml (from the now-retired EPIC-calidad certification
// mechanism) that is still present and even declares itself enabled must
// never block a spec's advance, in either lane. Before SPEC-158, exactly
// this fixture (an enabled constitution with zero certificates) blocked
// both qa->done for standard and implementing->audit for trivial with
// [budget].enabled — see the git history of the now-deleted
// sdd_quality_test.go for the tests that used to assert the opposite.
package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/wirvii/mneme/internal/model"
)

// gitInitFixtureRepo turns dir into a fresh git repository with a local,
// non-signing identity — newTestSDDServiceWithRepoDir's own repoDir is a
// plain t.TempDir(), never initialised as a repository, since not every
// caller needs git.
func gitInitFixtureRepo(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", "-b", "main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
}

// writeLegacyConstitution writes a literal, still-parseable
// .mneme/quality.toml at repoDir — schema_version=1, enabled=true, one
// declared gate — the worst case for AC1: the retired mechanism's own
// on-switch, left flipped on, with nothing else present.
func writeLegacyConstitution(t *testing.T, repoDir string) {
	t.Helper()
	dir := filepath.Join(repoDir, ".mneme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir .mneme: %v", err)
	}
	doc := `
schema_version = 1
enabled = true
[execution]
output_tail_bytes = 4096
[[gate]]
name = "build"
command = ["true"]
timeout = "5m"
required = true
`
	if err := os.WriteFile(filepath.Join(dir, "quality.toml"), []byte(doc), 0o644); err != nil {
		t.Fatalf("write quality.toml: %v", err)
	}
}

// writeLegacyBudgetConstitution writes a literal schema_version=4
// .mneme/quality.toml with [budget].enabled = true and everything else
// declared-and-off — the worst case for AC2: the retired mechanism's
// trivial-lane absorption switch, left flipped on.
func writeLegacyBudgetConstitution(t *testing.T, repoDir string) {
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

// TestSpecAdvance_QAToDone_LegacyConstitutionNeverBlocks covers AC1: with a
// legacy quality.toml ENCENDIDO (enabled=true, one declared gate) confirmed
// in the repository and zero certificates anywhere, a standard-lane spec
// advances from qa to done. Mutation check: reintroducing the deleted call
// to ensureCertified in SpecAdvance would turn this red with
// model.ErrCertificateMissing — verified structurally impossible to
// reintroduce, since ensureCertified no longer exists in this package.
func TestSpecAdvance_QAToDone_LegacyConstitutionNeverBlocks(t *testing.T) {
	svc, _, repoDir := newTestSDDServiceWithRepoDir(t, "wirvii/mneme")
	ctx := context.Background()

	gitInitFixtureRepo(t, repoDir)
	writeLegacyConstitution(t, repoDir)
	commitAllFixture(t, repoDir, "add legacy enabled constitution")
	svc.WithRepoDir(repoDir)

	spec := advanceToQA(t, svc, ctx, "AC1 legacy constitution never blocks")

	done, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "orchestrator"})
	if err != nil {
		t.Fatalf("SpecAdvance qa->done with a legacy enabled constitution and no certificate: %v", err)
	}
	if done.Status != model.SpecStatusDone {
		t.Errorf("status = %s, want done", done.Status)
	}
}

// TestSpecAdvance_ImplementingToAudit_LegacyBudgetNeverBlocks covers AC2's
// SpecAdvance half: with a legacy quality.toml declaring [budget].enabled =
// true and zero certificates, a trivial-lane spec advances from implementing
// to audit (lane_audit itself is covered separately in
// TestLaneAudit_LegacyBudgetConstitution_RunsDirect).
func TestSpecAdvance_ImplementingToAudit_LegacyBudgetNeverBlocks(t *testing.T) {
	svc, _, repoDir := newTestSDDServiceWithRepoDir(t, "wirvii/mneme")
	ctx := context.Background()

	gitInitFixtureRepo(t, repoDir)
	writeLegacyBudgetConstitution(t, repoDir)
	commitAllFixture(t, repoDir, "add legacy budget-enabled constitution")
	svc.WithRepoDir(repoDir)

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "trivial", Lane: model.LaneTrivial, Scope: "internal/**/*.go"})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}
	spec, err = svc.SpecQuick(ctx, model.SpecQuickRequest{ID: spec.ID, Rationale: "tiny fix", By: "orch"})
	if err != nil {
		t.Fatalf("SpecQuick: %v", err)
	}
	if spec.Status != model.SpecStatusImplementing {
		t.Fatalf("expected implementing, got %s", spec.Status)
	}

	advanced, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "backend"})
	if err != nil {
		t.Fatalf("SpecAdvance implementing->audit with a legacy [budget].enabled=true constitution and no certificate: %v", err)
	}
	if advanced.Status != model.SpecStatusAudit {
		t.Errorf("status = %s, want audit", advanced.Status)
	}
}
