// Package service — tests for SPEC-181 D3: the review_required mark and the
// fresh-review guard. Table-driven where it helps, a real in-memory SQLite
// store and real files in a temp workflow directory throughout.
package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/store"
)

// newReviewTestService builds an SDDService over a real in-memory SQLite and
// a temp workflow directory, and also returns the *db.DB so a test can reach
// the schema directly (triggers, history rewrites). It is
// newContractTestService with the database exposed.
func newReviewTestService(t *testing.T, project string) (svc *SDDService, database *db.DB, workflowDir string) {
	t.Helper()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })

	cfg := config.Default()
	workflowDir = t.TempDir()
	cfg.Workflow.Dir = workflowDir
	svc = NewSDDService(store.NewSDDStore(database), cfg, project, nil)
	return svc, database, workflowDir
}

// advanceTo advances spec one step at a time until it reaches target,
// writing criteria and the reports the guard demands along the way, so a
// test can park a spec exactly at the transition it wants to probe.
func advanceTo(t *testing.T, svc *SDDService, spec *model.Spec, target model.SpecStatus) *model.Spec {
	t.Helper()
	ctx := context.Background()
	writeSpecCriteria(t, svc, spec, "AC1")
	for spec.Status != target {
		writeReviewReportFor(t, svc, spec)
		next, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: spec.ID, By: "test"})
		if err != nil {
			t.Fatalf("advanceTo %s (status=%s): %v", target, spec.Status, err)
		}
		spec = next
	}
	return spec
}

// reportPath resolves where kind's report lives for spec.
func reportPath(t *testing.T, svc *SDDService, spec *model.Spec, kind model.SpecDocKind) string {
	t.Helper()
	p, err := specDocPath(svc.config.WorkflowDir(), spec.Project, spec.ID, kind)
	if err != nil {
		t.Fatalf("specDocPath: %v", err)
	}
	return p
}

// setMtime stamps path's modification time.
func setMtime(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

// writeTaskFile writes a minimal tarea.md at the spec's task path and
// returns the path.
func writeTaskFile(t *testing.T, svc *SDDService, spec *model.Spec) string {
	t.Helper()
	p := reportPath(t, svc, spec, model.SpecDocKindTask)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte("# tarea\n"), 0o644); err != nil {
		t.Fatalf("write tarea.md: %v", err)
	}
	return p
}

func advance(ctx context.Context, svc *SDDService, id string) (*model.Spec, error) {
	return svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: id, By: "test"})
}

// TestReviewRequired covers SPEC-181 C5: which specs carry the mark and the
// two places that fail without leaving the spec half-moved.
func TestReviewRequired(t *testing.T) {
	ctx := context.Background()

	t.Run("standard draft to speccing marks the spec", func(t *testing.T) {
		svc, _, _ := newReviewTestService(t, "p")
		spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "s", Lane: model.LaneStandard})
		if err != nil {
			t.Fatalf("SpecNew: %v", err)
		}
		if spec.ReviewRequired {
			t.Fatal("a draft spec is already marked")
		}
		if _, err := advance(ctx, svc, spec.ID); err != nil {
			t.Fatalf("advance: %v", err)
		}
		got, err := svc.store.GetSpec(ctx, spec.ID)
		if err != nil || !got.ReviewRequired {
			t.Fatalf("ReviewRequired = %v (err %v), want true", got != nil && got.ReviewRequired, err)
		}
	})

	t.Run("trivial quick-start stays unmarked", func(t *testing.T) {
		svc, _, _ := newReviewTestService(t, "p")
		spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "t", Lane: model.LaneTrivial, Scope: "internal/**"})
		if err != nil {
			t.Fatalf("SpecNew: %v", err)
		}
		if _, err := svc.SpecQuick(ctx, model.SpecQuickRequest{ID: spec.ID, Rationale: "tiny", By: "orch"}); err != nil {
			t.Fatalf("SpecQuick: %v", err)
		}
		got, _ := svc.store.GetSpec(ctx, spec.ID)
		if got.ReviewRequired {
			t.Error("a trivial spec was marked")
		}
	})

	t.Run("reclassify trivial to standard marks the spec", func(t *testing.T) {
		svc, _, _ := newReviewTestService(t, "p")
		spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "t", Lane: model.LaneTrivial, Scope: "internal/**"})
		if err != nil {
			t.Fatalf("SpecNew: %v", err)
		}
		if _, err := svc.LaneReclassify(ctx, model.LaneReclassifyRequest{ID: spec.ID, Lane: model.LaneStandard, By: "orch"}); err != nil {
			t.Fatalf("LaneReclassify: %v", err)
		}
		got, _ := svc.store.GetSpec(ctx, spec.ID)
		if !got.ReviewRequired || got.Lane != model.LaneStandard || got.Status != model.SpecStatusSpeccing {
			t.Errorf("after reclassify: required=%v lane=%s status=%s", got.ReviewRequired, got.Lane, got.Status)
		}
	})

	t.Run("resolve keeps the mark either way", func(t *testing.T) {
		for _, want := range []bool{false, true} {
			svc, _, _ := newReviewTestService(t, "p")
			spec := insertContractTestSpec(t, svc, "SPEC-100", model.LaneStandard, model.SpecStatusSpeccing)
			if err := svc.store.SetSpecReviewRequired(ctx, spec.ID, want); err != nil {
				t.Fatalf("SetSpecReviewRequired: %v", err)
			}
			if _, err := svc.SpecPushback(ctx, model.SpecPushbackRequest{ID: spec.ID, FromAgent: "architect", Questions: []string{"q"}}); err != nil {
				t.Fatalf("SpecPushback: %v", err)
			}
			if _, err := svc.SpecResolve(ctx, model.SpecResolveRequest{ID: spec.ID, Resolution: "r"}); err != nil {
				t.Fatalf("SpecResolve: %v", err)
			}
			got, _ := svc.store.GetSpec(ctx, spec.ID)
			if got.ReviewRequired != want || got.Status != model.SpecStatusSpeccing {
				t.Errorf("want=%v: required=%v status=%s", want, got.ReviewRequired, got.Status)
			}
		}
	})

	const failTrigger = "CREATE TRIGGER fail_mark BEFORE UPDATE OF review_required ON specs BEGIN SELECT RAISE(ABORT,'forced'); END"

	t.Run("a failed mark leaves a draft in draft", func(t *testing.T) {
		svc, database, _ := newReviewTestService(t, "p")
		spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "s", Lane: model.LaneStandard})
		if err != nil {
			t.Fatalf("SpecNew: %v", err)
		}
		if _, err := database.Exec(failTrigger); err != nil {
			t.Fatalf("trigger: %v", err)
		}
		if _, err := advance(ctx, svc, spec.ID); err == nil {
			t.Fatal("SpecAdvance succeeded although the mark could not be written")
		}
		got, _ := svc.store.GetSpec(ctx, spec.ID)
		if got.Status != model.SpecStatusDraft {
			t.Errorf("status = %s, want draft", got.Status)
		}
	})

	t.Run("a failed mark leaves a reclassify untouched", func(t *testing.T) {
		svc, database, _ := newReviewTestService(t, "p")
		spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "t", Lane: model.LaneTrivial, Scope: "internal/**"})
		if err != nil {
			t.Fatalf("SpecNew: %v", err)
		}
		if _, err := database.Exec(failTrigger); err != nil {
			t.Fatalf("trigger: %v", err)
		}
		if _, err := svc.LaneReclassify(ctx, model.LaneReclassifyRequest{ID: spec.ID, Lane: model.LaneStandard, By: "orch"}); err == nil {
			t.Fatal("LaneReclassify succeeded although the mark could not be written")
		}
		got, _ := svc.store.GetSpec(ctx, spec.ID)
		if got.Lane != model.LaneTrivial || got.Status != model.SpecStatusDraft {
			t.Errorf("lane=%s status=%s, want trivial draft", got.Lane, got.Status)
		}
	})
}

// TestEnsureReviewReport_Spec covers SPEC-181 C6: the guard at speccing ->
// specced for the spec-moment report.
func TestEnsureReviewReport_Spec(t *testing.T) {
	ctx := context.Background()
	newSpeccing := func(t *testing.T) (*SDDService, *db.DB, *model.Spec) {
		t.Helper()
		svc, database, _ := newReviewTestService(t, "wirvii/mneme")
		spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "s", Lane: model.LaneStandard})
		if err != nil {
			t.Fatalf("SpecNew: %v", err)
		}
		spec, err = advance(ctx, svc, spec.ID)
		if err != nil {
			t.Fatalf("to speccing: %v", err)
		}
		writeSpecCriteria(t, svc, spec, "AC1")
		return svc, database, spec
	}

	t.Run("missing report fails naming path and kind", func(t *testing.T) {
		svc, _, spec := newSpeccing(t)
		_, err := advance(ctx, svc, spec.ID)
		if !errors.Is(err, model.ErrReviewReportNotFound) {
			t.Fatalf("err = %v, want ErrReviewReportNotFound", err)
		}
		path := reportPath(t, svc, spec, model.SpecDocKindReviewSpec)
		if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "revision-spec.toml") || !strings.Contains(err.Error(), "review-spec") {
			t.Errorf("error does not name the path and kind: %v", err)
		}
		got, _ := svc.store.GetSpec(ctx, spec.ID)
		if got.Status != model.SpecStatusSpeccing {
			t.Errorf("status = %s, want speccing", got.Status)
		}
	})

	t.Run("invalid report fails", func(t *testing.T) {
		svc, _, spec := newSpeccing(t)
		path := reportPath(t, svc, spec, model.SpecDocKindReviewSpec)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("schema_version = 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := advance(ctx, svc, spec.ID); !errors.Is(err, model.ErrInvalidReviewReport) {
			t.Fatalf("err = %v, want ErrInvalidReviewReport", err)
		}
	})

	t.Run("current report advances", func(t *testing.T) {
		svc, _, spec := newSpeccing(t)
		writeTaskFile(t, svc, spec)
		writeReviewReport(t, svc, spec, model.SpecDocKindReviewSpec)
		got, err := advance(ctx, svc, spec.ID)
		if err != nil || got.Status != model.SpecStatusSpecced {
			t.Fatalf("advance: status=%v err=%v", got, err)
		}
	})

	staleCases := []struct {
		name  string
		setup func(t *testing.T, svc *SDDService, spec *model.Spec, report string)
	}{
		{"report older than the last entry to speccing", func(t *testing.T, _ *SDDService, _ *model.Spec, report string) {
			setMtime(t, report, time.Now().Add(-10*time.Second))
		}},
		{"tarea.md newer than the report", func(t *testing.T, svc *SDDService, spec *model.Spec, report string) {
			setMtime(t, writeTaskFile(t, svc, spec), time.Now().Add(15*time.Second))
			setMtime(t, report, time.Now().Add(3*time.Second))
		}},
		{"criteria.toml newer than the report", func(t *testing.T, svc *SDDService, spec *model.Spec, report string) {
			setMtime(t, reportPath(t, svc, spec, model.SpecDocKindCriteria), time.Now().Add(15*time.Second))
			setMtime(t, report, time.Now().Add(3*time.Second))
		}},
	}
	for _, tc := range staleCases {
		t.Run("stale: "+tc.name, func(t *testing.T) {
			svc, _, spec := newSpeccing(t)
			writeReviewReport(t, svc, spec, model.SpecDocKindReviewSpec)
			report := reportPath(t, svc, spec, model.SpecDocKindReviewSpec)
			tc.setup(t, svc, spec, report)
			_, err := advance(ctx, svc, spec.ID)
			if !errors.Is(err, model.ErrReviewReportStale) {
				t.Fatalf("err = %v, want ErrReviewReportStale", err)
			}
			if !strings.Contains(err.Error(), report) {
				t.Errorf("error does not name the report path: %v", err)
			}
		})
	}

	t.Run("a report older than a re-entry to speccing stops counting", func(t *testing.T) {
		svc, database, spec := newSpeccing(t)
		// Move the first entry to speccing an hour back so a report stamped
		// half an hour back is newer than it but older than the re-entry.
		hourAgo := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
		if _, err := database.Exec(`UPDATE spec_history SET at = ? WHERE spec_id = ? AND to_status = 'speccing'`, hourAgo, spec.ID); err != nil {
			t.Fatalf("rewrite history: %v", err)
		}
		writeReviewReport(t, svc, spec, model.SpecDocKindReviewSpec)
		report := reportPath(t, svc, spec, model.SpecDocKindReviewSpec)
		setMtime(t, report, time.Now().Add(-30*time.Minute))
		setMtime(t, reportPath(t, svc, spec, model.SpecDocKindCriteria), time.Now().Add(-45*time.Minute))

		if _, err := svc.SpecPushback(ctx, model.SpecPushbackRequest{ID: spec.ID, FromAgent: "architect", Questions: []string{"q"}}); err != nil {
			t.Fatalf("SpecPushback: %v", err)
		}
		if _, err := svc.SpecResolve(ctx, model.SpecResolveRequest{ID: spec.ID, Resolution: "r"}); err != nil {
			t.Fatalf("SpecResolve: %v", err)
		}
		if _, err := advance(ctx, svc, spec.ID); !errors.Is(err, model.ErrReviewReportStale) {
			t.Fatalf("err = %v, want ErrReviewReportStale after needs_grill", err)
		}
	})

	t.Run("an unmarked spec advances without a report", func(t *testing.T) {
		svc, _, spec := newSpeccing(t)
		if err := svc.store.SetSpecReviewRequired(ctx, spec.ID, false); err != nil {
			t.Fatal(err)
		}
		got, err := advance(ctx, svc, spec.ID)
		if err != nil || got.Status != model.SpecStatusSpecced {
			t.Fatalf("advance: %v %v", got, err)
		}
	})

	t.Run("no workflow dir means no guard", func(t *testing.T) {
		svc, _, spec := newSpeccing(t)
		svc.config.Workflow.Dir = ""
		// criteria are also off without a workflow dir, so the transition is free.
		got, err := advance(ctx, svc, spec.ID)
		if err != nil || got.Status != model.SpecStatusSpecced {
			t.Fatalf("advance: %v %v", got, err)
		}
	})

	t.Run("no config means no guard", func(t *testing.T) {
		svc, _, spec := newSpeccing(t)
		svc.config = nil
		spec.ReviewRequired = true
		if err := svc.ensureReviewReport(ctx, spec, model.SpecStatusSpecced); err != nil {
			t.Fatalf("ensureReviewReport with nil config: %v", err)
		}
	})

	t.Run("history without the reference entry fails closed", func(t *testing.T) {
		svc, database, spec := newSpeccing(t)
		writeReviewReport(t, svc, spec, model.SpecDocKindReviewSpec)
		if _, err := database.Exec(`DELETE FROM spec_history WHERE spec_id = ? AND to_status = 'speccing'`, spec.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := advance(ctx, svc, spec.ID); !errors.Is(err, model.ErrReviewReportStale) {
			t.Fatalf("err = %v, want ErrReviewReportStale", err)
		}
	})

	t.Run("the error names the newest of the newer documents", func(t *testing.T) {
		svc, _, spec := newSpeccing(t)
		writeReviewReport(t, svc, spec, model.SpecDocKindReviewSpec)
		report := reportPath(t, svc, spec, model.SpecDocKindReviewSpec)
		setMtime(t, report, time.Now())
		task := writeTaskFile(t, svc, spec)
		setMtime(t, task, time.Now().Add(30*time.Second))
		setMtime(t, reportPath(t, svc, spec, model.SpecDocKindCriteria), time.Now().Add(10*time.Second))
		_, err := advance(ctx, svc, spec.ID)
		if !errors.Is(err, model.ErrReviewReportStale) || !strings.Contains(err.Error(), task) {
			t.Fatalf("err = %v, want stale naming %s", err, task)
		}
	})
}

// TestEnsureReviewReport_Plan covers SPEC-181 C7: the guard at specced ->
// planning (spec-moment report again) and at planned -> implementing
// (plan-moment report), plus the transitions it must leave alone.
func TestEnsureReviewReport_Plan(t *testing.T) {
	ctx := context.Background()
	newAt := func(t *testing.T, target model.SpecStatus) (*SDDService, *model.Spec) {
		t.Helper()
		svc, _, _ := newReviewTestService(t, "wirvii/mneme")
		spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "s", Lane: model.LaneStandard})
		if err != nil {
			t.Fatalf("SpecNew: %v", err)
		}
		return svc, advanceTo(t, svc, spec, target)
	}

	t.Run("specced to planning with a current report advances", func(t *testing.T) {
		svc, spec := newAt(t, model.SpecStatusSpecced)
		got, err := advance(ctx, svc, spec.ID)
		if err != nil || got.Status != model.SpecStatusPlanning {
			t.Fatalf("advance: %v %v", got, err)
		}
	})

	t.Run("specced to planning fails when a document changed in the gate", func(t *testing.T) {
		for _, kind := range []model.SpecDocKind{model.SpecDocKindTask, model.SpecDocKindCriteria} {
			svc, spec := newAt(t, model.SpecStatusSpecced)
			report := reportPath(t, svc, spec, model.SpecDocKindReviewSpec)
			if kind == model.SpecDocKindTask {
				writeTaskFile(t, svc, spec)
			}
			setMtime(t, reportPath(t, svc, spec, kind), time.Now().Add(20*time.Second))
			setMtime(t, report, time.Now().Add(3*time.Second))
			_, err := advance(ctx, svc, spec.ID)
			if !errors.Is(err, model.ErrReviewReportStale) {
				t.Fatalf("kind %s: err = %v, want ErrReviewReportStale", kind, err)
			}
			got, _ := svc.store.GetSpec(ctx, spec.ID)
			if got.Status != model.SpecStatusSpecced {
				t.Errorf("kind %s: status = %s, want specced", kind, got.Status)
			}
		}
	})

	t.Run("planned to implementing without a plan report fails", func(t *testing.T) {
		svc, spec := newAt(t, model.SpecStatusPlanned)
		_, err := advance(ctx, svc, spec.ID)
		if !errors.Is(err, model.ErrReviewReportNotFound) {
			t.Fatalf("err = %v, want ErrReviewReportNotFound", err)
		}
		if !strings.Contains(err.Error(), "revision-plan.toml") || !strings.Contains(err.Error(), "review-plan") {
			t.Errorf("error does not name the file and kind: %v", err)
		}
	})

	t.Run("planned to implementing stale reports", func(t *testing.T) {
		cases := []struct {
			name  string
			setup func(t *testing.T, svc *SDDService, spec *model.Spec, report string)
		}{
			{"older than the entry to planning", func(t *testing.T, _ *SDDService, _ *model.Spec, report string) {
				setMtime(t, report, time.Now().Add(-10*time.Second))
			}},
			{"tarea.md plan section edited afterwards", func(t *testing.T, svc *SDDService, spec *model.Spec, report string) {
				setMtime(t, writeTaskFile(t, svc, spec), time.Now().Add(20*time.Second))
				setMtime(t, report, time.Now().Add(3*time.Second))
			}},
			{"criteria.toml edited afterwards", func(t *testing.T, svc *SDDService, spec *model.Spec, report string) {
				setMtime(t, reportPath(t, svc, spec, model.SpecDocKindCriteria), time.Now().Add(20*time.Second))
				setMtime(t, report, time.Now().Add(3*time.Second))
			}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				svc, spec := newAt(t, model.SpecStatusPlanned)
				writeReviewReport(t, svc, spec, model.SpecDocKindReviewPlan)
				report := reportPath(t, svc, spec, model.SpecDocKindReviewPlan)
				tc.setup(t, svc, spec, report)
				_, err := advance(ctx, svc, spec.ID)
				if !errors.Is(err, model.ErrReviewReportStale) {
					t.Fatalf("err = %v, want ErrReviewReportStale", err)
				}
			})
		}
	})

	t.Run("planned to implementing with a current report advances", func(t *testing.T) {
		svc, spec := newAt(t, model.SpecStatusPlanned)
		writeReviewReport(t, svc, spec, model.SpecDocKindReviewPlan)
		got, err := advance(ctx, svc, spec.ID)
		if err != nil || got.Status != model.SpecStatusImplementing {
			t.Fatalf("advance: %v %v", got, err)
		}
	})

	t.Run("an unmarked spec needs no reports", func(t *testing.T) {
		svc, _, _ := newReviewTestService(t, "p")
		spec := insertContractTestSpec(t, svc, "SPEC-100", model.LaneStandard, model.SpecStatusSpecced)
		writeSpecCriteria(t, svc, spec, "AC1")
		for _, want := range []model.SpecStatus{model.SpecStatusPlanning, model.SpecStatusPlanned, model.SpecStatusImplementing} {
			got, err := advance(ctx, svc, spec.ID)
			if err != nil || got.Status != want {
				t.Fatalf("want %s: %v %v", want, got, err)
			}
		}
	})

	t.Run("the other transitions never ask for a report", func(t *testing.T) {
		svc, spec := newAt(t, model.SpecStatusSpecced)
		// Remove every report: only the three guarded transitions may care.
		for _, kind := range []model.SpecDocKind{model.SpecDocKindReviewSpec, model.SpecDocKindReviewPlan} {
			if err := os.Remove(reportPath(t, svc, spec, kind)); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
		// specced -> planning would fail; park the spec at planning through SQL-free means.
		if _, err := advance(ctx, svc, spec.ID); !errors.Is(err, model.ErrReviewReportNotFound) {
			t.Fatalf("specced->planning without the report: %v", err)
		}
		other, _, _ := newReviewTestService(t, "q")
		marked := insertContractTestSpec(t, other, "SPEC-200", model.LaneStandard, model.SpecStatusPlanning)
		if err := other.store.SetSpecReviewRequired(ctx, marked.ID, true); err != nil {
			t.Fatal(err)
		}
		for _, want := range []model.SpecStatus{model.SpecStatusPlanned} {
			got, err := advance(ctx, other, marked.ID)
			if err != nil || got.Status != want {
				t.Fatalf("planning->planned: %v %v", got, err)
			}
		}
		impl := insertContractTestSpec(t, other, "SPEC-201", model.LaneStandard, model.SpecStatusImplementing)
		if err := other.store.SetSpecReviewRequired(ctx, impl.ID, true); err != nil {
			t.Fatal(err)
		}
		writeSpecCriteria(t, other, impl, "AC1")
		if got, err := advance(ctx, other, impl.ID); err != nil || got.Status != model.SpecStatusQA {
			t.Fatalf("implementing->qa: %v %v", got, err)
		}
		if _, err := other.SpecReject(ctx, model.SpecRejectRequest{
			ID: impl.ID, By: "qa", Reason: "nope",
			Findings: []model.RejectFinding{{CriterionID: "AC1", Detail: "fails"}},
		}); err != nil {
			t.Fatalf("spec_reject qa->implementing: %v", err)
		}
		if _, err := other.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: impl.ID, By: "backend"}); err != nil {
			t.Fatalf("implementing->qa again: %v", err)
		}
		if got, err := advance(ctx, other, impl.ID); err != nil || got.Status != model.SpecStatusDone {
			t.Fatalf("qa->done: %v %v", got, err)
		}
	})
}
