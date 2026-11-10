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
	"time"

	"github.com/wirvii/mneme/internal/model"
)

// validReviewSpecTOML and validReviewPlanTOML are minimal valid reports of each
// moment, shared by the service tests of SPEC-181. The CLI and MCP test
// packages declare byte-identical copies, because these constants are not
// visible across packages.
const validReviewSpecTOML = `schema_version = 1

[[revisor]]
papel = "comprension"
estado = "completo"
parafrasis_propuesta = "Propone revisar la spec sin contexto."
parafrasis_hoy = "Hoy nadie la revisa."

[[revisor]]
papel = "diseno"
estado = "completo"

[[revisor]]
papel = "hechos"
estado = "completo"
`

const validReviewPlanTOML = `schema_version = 1

[[revisor]]
papel = "preparacion"
estado = "completo"

[[revisor]]
papel = "hechos"
estado = "completo"
`

// writeReviewReport writes a valid fresh-review report of the given kind for
// spec through SpecDocWrite and then stamps its mtime at
// max(now, mtime(tarea.md), mtime(criteria.toml)) + 2s, so the guard always
// sees it as newer than the stage entry and the documents even when the file
// system rounds mtimes (SPEC-181 plan, decision 9). A file that does not
// exist does not count.
func writeReviewReport(t *testing.T, svc *SDDService, spec *model.Spec, kind model.SpecDocKind) {
	t.Helper()
	content := validReviewSpecTOML
	if kind == model.SpecDocKindReviewPlan {
		content = validReviewPlanTOML
	}
	resp, err := svc.SpecDocWrite(context.Background(), model.SpecDocWriteRequest{ID: spec.ID, Kind: kind, Content: content})
	if err != nil {
		t.Fatalf("writeReviewReport(%s): %v", kind, err)
	}
	stamp := time.Now()
	for _, docKind := range []model.SpecDocKind{model.SpecDocKindTask, model.SpecDocKindCriteria} {
		docPath, err := specDocPath(svc.config.WorkflowDir(), spec.Project, spec.ID, docKind)
		if err != nil {
			t.Fatalf("specDocPath(%s): %v", docKind, err)
		}
		if info, err := os.Stat(docPath); err == nil && info.ModTime().After(stamp) {
			stamp = info.ModTime()
		}
	}
	stamp = stamp.Add(2 * time.Second)
	if err := os.Chtimes(resp.Path, stamp, stamp); err != nil {
		t.Fatalf("chtimes %s: %v", resp.Path, err)
	}
}

// writeReviewReportFor writes the report the guard will demand for spec's
// NEXT SpecAdvance, if any: review-spec while the spec is in speccing (the
// speccing -> specced transition) and review-plan while it is planned (the
// planned -> implementing transition). Any other status needs no report, so
// it is safe to call before every SpecAdvance in a walking loop.
func writeReviewReportFor(t *testing.T, svc *SDDService, spec *model.Spec) {
	t.Helper()
	switch spec.Status {
	case model.SpecStatusSpeccing:
		writeReviewReport(t, svc, spec, model.SpecDocKindReviewSpec)
	case model.SpecStatusPlanned:
		writeReviewReport(t, svc, spec, model.SpecDocKindReviewPlan)
	}
}

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
		writeReviewReportFor(t, svc, spec)
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
