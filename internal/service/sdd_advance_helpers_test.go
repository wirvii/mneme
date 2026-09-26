// Package service — this file holds the small SpecAdvance-walking helpers
// several test files share: sdd_frontier_test.go and sdd_reviewrange_test.go
// (SPEC-157) both depend on advanceToImplementing/advanceToQA/headSHA/
// trimNL, which used to live in the now-deleted sdd_quality_test.go
// (SPEC-158 commit 2 — every OTHER test in that file existed solely to
// exercise the retired ensureCertified). Moved here verbatim, under the
// same names, so neither dependent file changes.
package service

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/wirvii/mneme/internal/model"
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
	// SPEC-156 D2: speccing->specced requires a criteria.toml for a
	// standard-lane spec. Written up front — every caller of this helper
	// walks through that transition — so the guardian sees a declared
	// contract instead of blocking the fixture.
	writeSpecCriteria(t, svc, spec, "AC1")
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
// (implementing->qa) and returns the reloaded spec at qa. Since SPEC-158
// retired the certification mechanism entirely, this transition always
// succeeds unconditionally — it exists purely to reach qa, the status
// several tests across this package need.
func advanceToQA(t *testing.T, svc *SDDService, ctx context.Context, title string) *model.Spec {
	t.Helper()
	spec := advanceToImplementing(t, svc, ctx, title)
	spec, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "backend"})
	if err != nil {
		t.Fatalf("SpecAdvance (implementing->qa, status=%s): %v", spec.Status, err)
	}
	if spec.Status != model.SpecStatusQA {
		t.Fatalf("expected qa, got %s", spec.Status)
	}
	return spec
}

// commitAllFixture stages and commits everything in repoDir with a fixed,
// local, non-signing git identity — the general-purpose git-fixture helper
// several test files across this package share (renamed from
// commitAllQuality, whose only caller was the now-deleted
// sdd_quality_test.go).
func commitAllFixture(t *testing.T, repoDir, message string) {
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
