package service

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/quality"
)

// advanceToImplementing walks a fresh standard-lane spec through
// draft→speccing→specced→planning→planned→implementing (5 SpecAdvance
// calls) and returns the reloaded spec at implementing.
func advanceToImplementing(t *testing.T, svc *SDDService, ctx context.Context, title string) *model.Spec {
	t.Helper()
	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: title, Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}
	for _, by := range []string{"orch", "arch", "arch", "arch", "backend"} {
		spec, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: by})
		if err != nil {
			t.Fatalf("SpecAdvance (to implementing, status=%s): %v", spec.Status, err)
		}
	}
	if spec.Status != model.SpecStatusImplementing {
		t.Fatalf("expected implementing, got %s", spec.Status)
	}
	return spec
}

// advanceToQA extends advanceToImplementing with ONE more SpecAdvance call
// (implementing->qa) and returns the reloaded spec at qa. SPEC-137 D1
// removed the standard lane's implementing->qa certificate requirement
// entirely, so this transition always succeeds regardless of repoDir,
// constitution, or certificate state — it exists purely to reach qa, the
// status every remaining "blocks" fixture in this file now needs, since
// the ONLY standard-lane leg ensureCertified still gates is qa->done.
func advanceToQA(t *testing.T, svc *SDDService, ctx context.Context, title string) *model.Spec {
	t.Helper()
	spec := advanceToImplementing(t, svc, ctx, title)
	spec, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "backend"})
	if err != nil {
		t.Fatalf("SpecAdvance (implementing->qa, now unconditional, status=%s): %v", spec.Status, err)
	}
	if spec.Status != model.SpecStatusQA {
		t.Fatalf("expected qa, got %s", spec.Status)
	}
	return spec
}

// writeTestConstitution writes a minimal, valid constitution at
// repoDir/.mneme/quality.toml with the given enabled value.
func writeTestConstitution(t *testing.T, repoDir string, enabled bool) {
	t.Helper()
	dir := filepath.Join(repoDir, ".mneme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir .mneme: %v", err)
	}
	doc := `
schema_version = 1
enabled = ` + boolTOML(enabled) + `
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

func boolTOML(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func commitAllQuality(t *testing.T, repoDir, message string) {
	t.Helper()
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", message}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func headSHA(t *testing.T, repoDir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	return trimNL(string(out))
}

func trimNL(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// --- AC16 pair: repoDir empty (mechanism off) vs repoDir set + enabled with
// no certificate (blocks). Same fixture shape, only repoDir/constitution
// differ — R-B's mandated pairing so the "passes" row can never be green
// merely because the whole mechanism is dead. ---

// TestEnsureCertified_RepoDirEmpty_Passes is the "passes" half of the AC16
// pair: repoDir=="" -> the quality mechanism is off, SpecAdvance proceeds.
func TestEnsureCertified_RepoDirEmpty_Passes(t *testing.T) {
	svc := newTestSDDService(t, "proj")
	ctx := context.Background()

	spec := advanceToImplementing(t, svc, ctx, "AC16 repoDir empty")

	// svc.repoDir is deliberately left unset (""). Even though a real
	// constitution with enabled=true exists in the current process's cwd
	// (SPEC-085's own dogfooded repo, if run from inside it), the quality
	// path must NEVER fall back to os.Getwd() — that is the whole point of
	// D13/G6, and the reason this is the test that protects the real DB.
	advanced, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "backend"})
	if err != nil {
		t.Fatalf("SpecAdvance with repoDir=='' should pass (mechanism off), got: %v", err)
	}
	if advanced.Status != model.SpecStatusQA {
		t.Errorf("status = %s, want qa", advanced.Status)
	}
}

// TestEnsureCertified_RepoDirSetEnabled_BlocksWithoutCertificate is the
// "blocks" hermana of the AC16 pair: SAME fixture (a standard-lane spec at
// implementing), but repoDir now points at a real repo with enabled=true and
// NO certificate — SpecAdvance must block. Without this row,
// TestEnsureCertified_RepoDirEmpty_Passes would be green even if
// ensureCertified were deleted outright.
func TestEnsureCertified_RepoDirSetEnabled_BlocksWithoutCertificate(t *testing.T) {
	svc := newTestSDDService(t, "proj")
	ctx := context.Background()

	spec := advanceToImplementing(t, svc, ctx, "AC16 repoDir set enabled")

	repoDir := newTestGitRepo(t)
	writeTestConstitution(t, repoDir, true)
	commitAllQuality(t, repoDir, "add constitution")
	svc.WithRepoDir(repoDir)

	// SPEC-137 D1: implementing->qa is unconditional now — it must succeed
	// even though repoDir points at an enabled constitution with no
	// certificate at all. The certificate requirement moved to qa->done.
	advanced, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "backend"})
	if err != nil {
		t.Fatalf("SpecAdvance implementing->qa should be unconditional, got: %v", err)
	}

	_, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: advanced.ID, By: "orchestrator"})
	if !errors.Is(err, model.ErrCertificateMissing) {
		t.Errorf("SpecAdvance qa->done error = %v, want ErrCertificateMissing", err)
	}
}

// TestEnsureCertified_AC1_ImplementingToQA_NeverRequiresCertificate covers
// AC1 (SPEC-137 D1) directly and by name: a standard-lane spec with
// enabled=true and ZERO certificates anywhere advances implementing->qa
// without error. Its mutation (reintroducing the old implementing->qa leg
// in standardGate) turns this exact assertion red with ErrCertificateMissing.
func TestEnsureCertified_AC1_ImplementingToQA_NeverRequiresCertificate(t *testing.T) {
	svc := newTestSDDService(t, "proj")
	ctx := context.Background()

	repoDir := newTestGitRepo(t)
	writeTestConstitution(t, repoDir, true)
	commitAllQuality(t, repoDir, "add constitution")
	svc.WithRepoDir(repoDir)

	spec := advanceToImplementing(t, svc, ctx, "AC1 implementing to qa")

	advanced, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "backend"})
	if err != nil {
		t.Fatalf("SpecAdvance implementing->qa = %v, want no error (AC1)", err)
	}
	if advanced.Status != model.SpecStatusQA {
		t.Errorf("status = %s, want qa", advanced.Status)
	}
}

// TestEnsureCertified_AC2_QAToDone_StillRequiresCertificate covers AC2
// directly and by name: the SAME spec, now at qa, does NOT advance to done
// without a certificate — ErrCertificateMissing. Its mutation (deleting the
// qa->done leg of standardGate) turns this red by letting the advance
// through.
func TestEnsureCertified_AC2_QAToDone_StillRequiresCertificate(t *testing.T) {
	svc := newTestSDDService(t, "proj")
	ctx := context.Background()

	repoDir := newTestGitRepo(t)
	writeTestConstitution(t, repoDir, true)
	commitAllQuality(t, repoDir, "add constitution")
	svc.WithRepoDir(repoDir)

	spec := advanceToQA(t, svc, ctx, "AC2 qa to done")

	_, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "orchestrator"})
	if !errors.Is(err, model.ErrCertificateMissing) {
		t.Errorf("SpecAdvance qa->done error = %v, want ErrCertificateMissing (AC2)", err)
	}
}

// --- AC24 pair (SPEC-118 retarget of S1's own AC15 — the mechanism
// ABSORBS the trivial lane, D12; this fixture has schema_version=1, so
// [budget] cannot even be declared, which is AC24's own "apagado por
// omision" row): trivial lane advances without a certificate vs standard
// lane, otherwise identical fixture, blocks. ---

// TestEnsureCertified_TrivialLane_Passes is the "passes" half of AC24: a
// trivial-lane spec's implementing→audit transition is UNCHANGED while
// [budget] is not declared/enabled — the retargeted half of S1's own AC15
// (which used to say trivial is out of this mechanism ENTIRELY; SPEC-118
// D12 absorbs it, conditionally on [budget].enabled — see
// TestEnsureCertified_TrivialLane_Absorbed_BlocksWithoutCertificate for the
// OTHER half, encendido).
func TestEnsureCertified_TrivialLane_Passes(t *testing.T) {
	svc := newTestSDDService(t, "proj")
	ctx := context.Background()

	repoDir := newTestGitRepo(t)
	writeTestConstitution(t, repoDir, true)
	commitAllQuality(t, repoDir, "add constitution")
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
		t.Fatalf("trivial-lane SpecAdvance should pass without any certificate, got: %v", err)
	}
	if advanced.Status != model.SpecStatusAudit {
		t.Errorf("status = %s, want audit", advanced.Status)
	}
}

// TestEnsureCertified_StandardLane_BlocksWithIdenticalFixture is the
// "blocks" hermana of the AC15 pair: the EXACT SAME repo/constitution as
// TestEnsureCertified_TrivialLane_Passes, but a STANDARD-lane spec — which
// must block for lack of a certificate. Without this row, the trivial-lane
// pass-through would be indistinguishable from the mechanism being dead
// entirely.
func TestEnsureCertified_StandardLane_BlocksWithIdenticalFixture(t *testing.T) {
	svc := newTestSDDService(t, "proj")
	ctx := context.Background()

	repoDir := newTestGitRepo(t)
	writeTestConstitution(t, repoDir, true)
	commitAllQuality(t, repoDir, "add constitution")
	svc.WithRepoDir(repoDir)

	spec := advanceToQA(t, svc, ctx, "AC15 standard lane")

	_, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "orchestrator"})
	if !errors.Is(err, model.ErrCertificateMissing) {
		t.Errorf("SpecAdvance qa->done error = %v, want ErrCertificateMissing", err)
	}
}

// writeTestConstitutionV4BudgetEnabled writes a schema_version=4
// constitution with [budget].enabled = budgetEnabled and every other
// section declared-and-off — the AC25 fixture (SPEC-118 D12's absorption
// switch specifically, distinct from writeTestConstitution's own
// top-level `enabled`).
func writeTestConstitutionV4BudgetEnabled(t *testing.T, repoDir string, budgetEnabled bool) {
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
enabled = ` + boolTOML(budgetEnabled) + `
timeout = "2m"
test_globs = ["**/*_test.go"]
test_reach_depth = 3
`
	if err := os.WriteFile(filepath.Join(dir, "quality.toml"), []byte(doc), 0o644); err != nil {
		t.Fatalf("write quality.toml: %v", err)
	}
}

// TestEnsureCertified_TrivialLane_BudgetOff_Passes covers AC24's OTHER two
// fixtures (schema 4 with [budget].enabled = false, and — via
// TestEnsureCertified_TrivialLane_Passes above — schema_version=1 where
// [budget] cannot even be declared): a trivial spec still advances
// implementing→audit without any certificate. Top-level `enabled` is
// FALSE in this fixture too, on purpose — proving the trivial gate reads
// [budget].enabled specifically, never the top-level flag.
func TestEnsureCertified_TrivialLane_BudgetOff_Passes(t *testing.T) {
	svc := newTestSDDService(t, "proj")
	ctx := context.Background()

	repoDir := newTestGitRepo(t)
	writeTestConstitutionV4BudgetEnabled(t, repoDir, false)
	commitAllQuality(t, repoDir, "add constitution")
	svc.WithRepoDir(repoDir)

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "trivial", Lane: model.LaneTrivial, Scope: "internal/**/*.go"})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}
	spec, err = svc.SpecQuick(ctx, model.SpecQuickRequest{ID: spec.ID, Rationale: "tiny fix", By: "orch"})
	if err != nil {
		t.Fatalf("SpecQuick: %v", err)
	}

	advanced, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "backend"})
	if err != nil {
		t.Fatalf("trivial-lane SpecAdvance with [budget].enabled=false should pass without any certificate, got: %v", err)
	}
	if advanced.Status != model.SpecStatusAudit {
		t.Errorf("status = %s, want audit", advanced.Status)
	}
}

// TestEnsureCertified_TrivialLane_Absorbed_BlocksWithoutCertificate covers
// AC25: with schema 4 and [budget].enabled = true, a trivial spec's
// implementing→audit transition NOW requires a usable certificate, just
// like the standard lane's implementing→qa — the absorption D12 mandates.
// Without this test (and without runCriteriaChecks' own lane-aware skip,
// G27, verified elsewhere), turning this mechanism on would brick every
// trivial spec in the repository forever (U-I).
func TestEnsureCertified_TrivialLane_Absorbed_BlocksWithoutCertificate(t *testing.T) {
	svc := newTestSDDService(t, "proj")
	ctx := context.Background()

	repoDir := newTestGitRepo(t)
	writeTestConstitutionV4BudgetEnabled(t, repoDir, true)
	commitAllQuality(t, repoDir, "add constitution")
	svc.WithRepoDir(repoDir)

	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "trivial", Lane: model.LaneTrivial, Scope: "internal/**/*.go"})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}
	spec, err = svc.SpecQuick(ctx, model.SpecQuickRequest{ID: spec.ID, Rationale: "tiny fix", By: "orch"})
	if err != nil {
		t.Fatalf("SpecQuick: %v", err)
	}

	_, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "backend"})
	if !errors.Is(err, model.ErrCertificateMissing) {
		t.Errorf("SpecAdvance error = %v, want ErrCertificateMissing (G26b)", err)
	}
}

// --- AC13: the five D3 states. The "absent" and "enabled=false" rows are
// vacuous alone (R-B) — they are exactly the two rows already covered above
// (RepoDirEmpty passes without a repo at all; here we add absent/disabled
// WITH a real repo configured, still passing, alongside the two blocking
// states: unparseable and ablated). ---

// TestEnsureCertified_ConstitutionAbsent_WithRealRepo_Passes covers D3 row 1
// with repoDir actually configured (unlike the RepoDirEmpty case above) —
// the absence of the FILE itself, not of repoDir, is what is under test.
func TestEnsureCertified_ConstitutionAbsent_WithRealRepo_Passes(t *testing.T) {
	svc := newTestSDDService(t, "proj")
	ctx := context.Background()

	repoDir := newTestGitRepo(t)
	svc.WithRepoDir(repoDir)

	spec := advanceToImplementing(t, svc, ctx, "AC13 constitution absent")
	advanced, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "backend"})
	if err != nil {
		t.Fatalf("SpecAdvance with no constitution file should pass, got: %v", err)
	}
	if advanced.Status != model.SpecStatusQA {
		t.Errorf("status = %s, want qa", advanced.Status)
	}
}

// TestEnsureCertified_ConstitutionDisabled_Passes covers D3 row 2: a present
// constitution with enabled=false does not block.
func TestEnsureCertified_ConstitutionDisabled_Passes(t *testing.T) {
	svc := newTestSDDService(t, "proj")
	ctx := context.Background()

	repoDir := newTestGitRepo(t)
	writeTestConstitution(t, repoDir, false)
	commitAllQuality(t, repoDir, "add disabled constitution")
	svc.WithRepoDir(repoDir)

	spec := advanceToImplementing(t, svc, ctx, "AC13 constitution disabled")
	advanced, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "backend"})
	if err != nil {
		t.Fatalf("SpecAdvance with enabled=false should pass, got: %v", err)
	}
	if advanced.Status != model.SpecStatusQA {
		t.Errorf("status = %s, want qa", advanced.Status)
	}
}

// TestEnsureCertified_ConstitutionUnparseable_Blocks covers D3 row 4: an
// unparseable constitution fails CLOSED with ErrInvalidConstitution.
func TestEnsureCertified_ConstitutionUnparseable_Blocks(t *testing.T) {
	svc := newTestSDDService(t, "proj")
	ctx := context.Background()

	repoDir := newTestGitRepo(t)
	if err := os.MkdirAll(filepath.Join(repoDir, ".mneme"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".mneme", "quality.toml"), []byte("not [ valid toml"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	commitAllQuality(t, repoDir, "add broken constitution")
	svc.WithRepoDir(repoDir)

	spec := advanceToQA(t, svc, ctx, "AC13 constitution unparseable")
	_, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "orchestrator"})
	if !errors.Is(err, model.ErrInvalidConstitution) {
		t.Errorf("SpecAdvance qa->done error = %v, want ErrInvalidConstitution", err)
	}
}

// TestEnsureCertified_ConstitutionAblated_Blocks covers D3 row 5: enabled at
// spec.BaseSHA but absent/disabled NOW — must block with
// ErrConstitutionAblated, closing the "just turn it off mid-spec" hole.
func TestEnsureCertified_ConstitutionAblated_Blocks(t *testing.T) {
	svc := newTestSDDService(t, "proj")
	ctx := context.Background()

	repoDir := newTestGitRepo(t)
	writeTestConstitution(t, repoDir, true)
	commitAllQuality(t, repoDir, "add enabled constitution")
	baseSHA := headSHA(t, repoDir)

	// Now disable it — simulating an implementer ablating the mechanism
	// mid-spec.
	writeTestConstitution(t, repoDir, false)
	commitAllQuality(t, repoDir, "disable constitution mid-spec")

	svc.WithRepoDir(repoDir)

	spec := advanceToImplementing(t, svc, ctx, "AC13 constitution ablated")
	// advanceToImplementing captured base_sha via the REAL svc.repoDir at the
	// implementing transition, which is baseSHA (constitution was enabled at
	// that point) since WithRepoDir was set before advancing. Overwrite
	// base_sha explicitly to the captured commit for a deterministic fixture
	// regardless of exactly which commit captureBaseSHA landed on.
	if err := svc.store.UpdateSpecBaseSHA(ctx, spec.ID, baseSHA); err != nil {
		t.Fatalf("UpdateSpecBaseSHA: %v", err)
	}

	// SPEC-137 D1: implementing->qa no longer reads the constitution at
	// all, so it succeeds unconditionally even with the mechanism ablated
	// mid-spec. The ablation check now only fires on qa->done.
	advanced, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "backend"})
	if err != nil {
		t.Fatalf("SpecAdvance implementing->qa should be unconditional, got: %v", err)
	}

	_, err = svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: advanced.ID, By: "orchestrator"})
	if !errors.Is(err, model.ErrConstitutionAblated) {
		t.Errorf("SpecAdvance qa->done error = %v, want ErrConstitutionAblated", err)
	}
}

// --- AC14: the four remaining certificate-usability causes, each with its
// own sentinel and a remedy command in the message. ---

func newQualifyingSpecWithCertificate(t *testing.T, svc *SDDService, ctx context.Context, repoDir string, mutate func(cert *model.QualityCertificate)) *model.Spec {
	t.Helper()
	// SPEC-137 D1: the only standard-lane leg ensureCertified still gates
	// is qa->done, so every fixture that inserts a certificate for
	// ensureCertified to evaluate must already be AT qa, not implementing.
	spec := advanceToQA(t, svc, ctx, "AC14 fixture")

	sha := headSHA(t, repoDir)
	raw, err := os.ReadFile(filepath.Join(repoDir, ".mneme", "quality.toml"))
	if err != nil {
		t.Fatalf("read constitution: %v", err)
	}

	cert := &model.QualityCertificate{
		Project: "proj", SpecID: spec.ID, HeadSHA: sha, BaseSHA: spec.BaseSHA,
		ConstitutionHash: quality.HashBytes(raw), SchemaVersion: 1, Verdict: model.QualityVerdictPass,
	}
	if mutate != nil {
		mutate(cert)
	}
	if err := svc.store.InsertCertificate(ctx, cert, []*model.QualityCheck{{Kind: "gate", Name: "build", Status: "pass"}}); err != nil {
		t.Fatalf("InsertCertificate: %v", err)
	}
	return spec
}

// TestEnsureCertified_CertificateNotGreen_Blocks covers ErrCertificateNotGreen.
func TestEnsureCertified_CertificateNotGreen_Blocks(t *testing.T) {
	svc := newTestSDDService(t, "proj")
	ctx := context.Background()

	repoDir := newTestGitRepo(t)
	writeTestConstitution(t, repoDir, true)
	commitAllQuality(t, repoDir, "add constitution")
	svc.WithRepoDir(repoDir)

	spec := newQualifyingSpecWithCertificate(t, svc, ctx, repoDir, func(c *model.QualityCertificate) {
		c.Verdict = model.QualityVerdictFail
	})

	_, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "orchestrator"})
	if !errors.Is(err, model.ErrCertificateNotGreen) {
		t.Errorf("SpecAdvance qa->done error = %v, want ErrCertificateNotGreen", err)
	}
}

// TestEnsureCertified_CertificateStale_Blocks covers ErrCertificateStale:
// HEAD moved after the certificate was issued.
func TestEnsureCertified_CertificateStale_Blocks(t *testing.T) {
	svc := newTestSDDService(t, "proj")
	ctx := context.Background()

	repoDir := newTestGitRepo(t)
	writeTestConstitution(t, repoDir, true)
	commitAllQuality(t, repoDir, "add constitution")
	svc.WithRepoDir(repoDir)

	spec := newQualifyingSpecWithCertificate(t, svc, ctx, repoDir, nil)

	// Move HEAD after the certificate was issued.
	if err := os.WriteFile(filepath.Join(repoDir, "extra.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write extra.txt: %v", err)
	}
	commitAllQuality(t, repoDir, "move HEAD after certification")

	_, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "orchestrator"})
	if !errors.Is(err, model.ErrCertificateStale) {
		t.Errorf("SpecAdvance qa->done error = %v, want ErrCertificateStale", err)
	}
}

// TestEnsureCertified_ConstitutionChanged_Blocks covers ErrConstitutionChanged:
// the constitution's hash no longer matches the certificate's.
func TestEnsureCertified_ConstitutionChanged_Blocks(t *testing.T) {
	svc := newTestSDDService(t, "proj")
	ctx := context.Background()

	repoDir := newTestGitRepo(t)
	writeTestConstitution(t, repoDir, true)
	commitAllQuality(t, repoDir, "add constitution")
	svc.WithRepoDir(repoDir)

	spec := newQualifyingSpecWithCertificate(t, svc, ctx, repoDir, func(c *model.QualityCertificate) {
		c.ConstitutionHash = "stale-hash-does-not-match-current-file"
	})

	_, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "orchestrator"})
	if !errors.Is(err, model.ErrConstitutionChanged) {
		t.Errorf("SpecAdvance qa->done error = %v, want ErrConstitutionChanged", err)
	}
}

// TestEnsureCertified_WorktreeDirty_Blocks covers ErrWorktreeDirty:
// certificate matches HEAD/constitution exactly, but the tree is dirty NOW.
func TestEnsureCertified_WorktreeDirty_Blocks(t *testing.T) {
	svc := newTestSDDService(t, "proj")
	ctx := context.Background()

	repoDir := newTestGitRepo(t)
	writeTestConstitution(t, repoDir, true)
	commitAllQuality(t, repoDir, "add constitution")
	svc.WithRepoDir(repoDir)

	spec := newQualifyingSpecWithCertificate(t, svc, ctx, repoDir, nil)

	if err := os.WriteFile(filepath.Join(repoDir, "untracked.txt"), []byte("oops"), 0o644); err != nil {
		t.Fatalf("write untracked.txt: %v", err)
	}

	_, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "orchestrator"})
	if !errors.Is(err, model.ErrWorktreeDirty) {
		t.Errorf("SpecAdvance qa->done error = %v, want ErrWorktreeDirty", err)
	}
}

// TestEnsureCertified_WorktreeDirty_NamesBothGroupsSeparately covers AC18
// (SPEC-137 D9): a worktree dirty with BOTH an untracked file under
// .mneme/shared/notes/ AND a modified project file must name the two
// groups SEPARATELY, and never offer to discard mneme's own group — the
// paths come from the fixture's REAL `git status --porcelain` output,
// never a list written in this test.
func TestEnsureCertified_WorktreeDirty_NamesBothGroupsSeparately(t *testing.T) {
	svc := newTestSDDService(t, "proj")
	ctx := context.Background()

	repoDir := newTestGitRepo(t)
	writeTestConstitution(t, repoDir, true)
	commitAllQuality(t, repoDir, "add constitution")
	svc.WithRepoDir(repoDir)

	spec := newQualifyingSpecWithCertificate(t, svc, ctx, repoDir, nil)

	// A dirty PROJECT file.
	if err := os.WriteFile(filepath.Join(repoDir, "untracked.txt"), []byte("oops"), 0o644); err != nil {
		t.Fatalf("write untracked.txt: %v", err)
	}
	// A dirty MNEME file, under one of D9's own declared prefixes.
	sharedNotesDir := filepath.Join(repoDir, ".mneme", "shared", "notes")
	if err := os.MkdirAll(sharedNotesDir, 0o755); err != nil {
		t.Fatalf("mkdir shared notes: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sharedNotesDir, "abc123.md"), []byte("# nota"), 0o644); err != nil {
		t.Fatalf("write shared note: %v", err)
	}

	_, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "orchestrator"})
	if !errors.Is(err, model.ErrWorktreeDirty) {
		t.Fatalf("SpecAdvance qa->done error = %v, want ErrWorktreeDirty", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "proyecto") {
		t.Errorf("error message does not name the project group:\n%s", msg)
	}
	if !strings.Contains(msg, "mneme") {
		t.Errorf("error message does not name the mneme group:\n%s", msg)
	}
	// The mneme group's own remedy segment must never offer to discard.
	mnemeIdx := strings.Index(msg, "de mneme")
	if mnemeIdx < 0 {
		t.Fatalf("error message has no 'de mneme' segment:\n%s", msg)
	}
	if strings.Contains(msg[mnemeIdx:], "descarta") {
		t.Errorf("error message offers to discard mneme's own paths, which deletes memories:\n%s", msg)
	}
}

// TestEnsureCertified_Usable_AllowsAdvance is the positive control: a
// certificate matching HEAD, matching constitution hash, verdict pass, and a
// clean tree — SpecAdvance proceeds through qa->done, the ONLY standard-lane
// leg SPEC-137 D1 still gates.
func TestEnsureCertified_Usable_AllowsAdvance(t *testing.T) {
	svc := newTestSDDService(t, "proj")
	ctx := context.Background()

	repoDir := newTestGitRepo(t)
	writeTestConstitution(t, repoDir, true)
	commitAllQuality(t, repoDir, "add constitution")
	svc.WithRepoDir(repoDir)

	spec := newQualifyingSpecWithCertificate(t, svc, ctx, repoDir, nil)

	advanced, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "orchestrator"})
	if err != nil {
		t.Fatalf("SpecAdvance qa->done with a usable certificate should pass, got: %v", err)
	}
	if advanced.Status != model.SpecStatusDone {
		t.Errorf("status = %s, want done", advanced.Status)
	}
}

// TestEnsureCertified_QAToDone_UsesTheSameCertificateImplementingQANeverNeeded
// covers D1's own consequence directly: implementing->qa is now completely
// unconditional (advanceToQA proves it, reaching qa with NO certificate at
// all inserted yet), and the ONE certificate this test inserts — bound to
// qa's own HEAD/base_sha — is what qa->done alone requires and accepts.
func TestEnsureCertified_QAToDone_UsesTheSameCertificateImplementingQANeverNeeded(t *testing.T) {
	svc := newTestSDDService(t, "proj")
	ctx := context.Background()

	repoDir := newTestGitRepo(t)
	writeTestConstitution(t, repoDir, true)
	commitAllQuality(t, repoDir, "add constitution")
	svc.WithRepoDir(repoDir)

	spec := newQualifyingSpecWithCertificate(t, svc, ctx, repoDir, nil)
	if spec.Status != model.SpecStatusQA {
		t.Fatalf("fixture spec.Status = %s, want qa (implementing->qa must have been unconditional)", spec.Status)
	}

	done, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "orchestrator"})
	if err != nil {
		t.Fatalf("SpecAdvance qa->done with a usable certificate: %v", err)
	}
	if done.Status != model.SpecStatusDone {
		t.Errorf("status = %s, want done", done.Status)
	}
}

// TestEnsureCertified_RatchetFinding_BlocksThenAckUnblocks covers AC28
// end-to-end, with ensureCertified/sdd.go completely UNCHANGED (D4/V2): a
// certificate whose ONLY imperfection is a `ratchet/global-line-pct`
// finding still blocks implementing->qa via the exact same generic
// verdict-derived mechanism S1 built (ensureCertified never learned about
// "ratchet" — DeriveVerdict already treats any un-acked finding as
// verdict=findings, degrading CertificateUsable just like any other
// finding kind), and acking that ONE row flips the SAME certificate's
// verdict to pass, letting the SAME transition through.
func TestEnsureCertified_RatchetFinding_BlocksThenAckUnblocks(t *testing.T) {
	svc := newTestSDDService(t, "proj")
	ctx := context.Background()

	repoDir := newTestGitRepo(t)
	writeTestConstitution(t, repoDir, true)
	commitAllQuality(t, repoDir, "add constitution")
	svc.WithRepoDir(repoDir)

	// SPEC-137 D1: implementing->qa is unconditional now — the certificate
	// this test builds is bound to qa's own HEAD/base_sha and is only ever
	// evaluated on the qa->done transition below.
	spec := advanceToQA(t, svc, ctx, "AC28 ratchet finding")

	sha := headSHA(t, repoDir)
	raw, err := os.ReadFile(filepath.Join(repoDir, ".mneme", "quality.toml"))
	if err != nil {
		t.Fatalf("read constitution: %v", err)
	}

	cert := &model.QualityCertificate{
		Project: "proj", SpecID: spec.ID, HeadSHA: sha, BaseSHA: spec.BaseSHA,
		ConstitutionHash: quality.HashBytes(raw), SchemaVersion: 2, Verdict: model.QualityVerdictFindings,
	}
	checks := []*model.QualityCheck{
		{Kind: "gate", Name: "build", Status: "pass"},
		{Kind: "ratchet", Name: "global-line-pct", Status: "finding", Summary: "cobertura global cayo 3 puntos"},
	}
	if err := svc.store.InsertCertificate(ctx, cert, checks); err != nil {
		t.Fatalf("InsertCertificate: %v", err)
	}

	if _, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "orchestrator"}); !errors.Is(err, model.ErrCertificateNotGreen) {
		t.Fatalf("SpecAdvance qa->done with a ratchet finding = %v, want ErrCertificateNotGreen", err)
	}

	// The finding row is checks[1] (seq=2, 1-based, per InsertCertificate's
	// own contract).
	if err := svc.store.AckCheck(ctx, cert.ID, 2, "human-reviewer", "caida legitima, codigo de arranque nuevo"); err != nil {
		t.Fatalf("AckCheck: %v", err)
	}

	advanced, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "orchestrator"})
	if err != nil {
		t.Fatalf("SpecAdvance qa->done after acking the ratchet finding: %v", err)
	}
	if advanced.Status != model.SpecStatusDone {
		t.Errorf("status = %s, want done", advanced.Status)
	}
}
