package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/model"
)

// statsReportWithFindings is a valid spec-moment report with 3 completed
// reviewers and 3 findings: 2 corregir (blocker, major) and 1 escalar (nit).
const statsReportWithFindings = `schema_version = 1
re_revision = false

[[revisor]]
papel = "comprension"
estado = "completo"
parafrasis_propuesta = "a"
parafrasis_hoy = "b"

[[revisor]]
papel = "diseno"
estado = "completo"

[[revisor]]
papel = "hechos"
estado = "completo"

[[hallazgo]]
id = "H1"
revisor = "hechos"
severidad = "blocker"
texto = "t"
salida = "corregir"
resolucion = "r"

[[hallazgo]]
id = "H2"
revisor = "diseno"
severidad = "major"
texto = "t"
salida = "corregir"
resolucion = "r"

[[hallazgo]]
id = "H3"
revisor = "hechos"
severidad = "nit"
texto = "t"
salida = "escalar"
`

// statsReportNoneRan is a valid spec-moment report where nobody ran.
const statsReportNoneRan = `schema_version = 1

[[revisor]]
papel = "comprension"
estado = "no_corrio"
nota = "sin revisor"

[[revisor]]
papel = "diseno"
estado = "no_corrio"
nota = "sin revisor"

[[revisor]]
papel = "hechos"
estado = "no_corrio"
nota = "sin revisor"
`

// seedHistory inserts a spec_history row with a fixed timestamp.
func seedHistory(t *testing.T, database *db.DB, specID, from, to string, at time.Time) {
	t.Helper()
	id := fmt.Sprintf("h-%s-%s-%s-%d", specID, from, to, at.UnixNano())
	_, err := database.Exec(`INSERT INTO spec_history (id, spec_id, from_status, to_status, by, reason, at, reviewed_sha)
		VALUES (?, ?, ?, ?, 'test', '', ?, '')`, id, specID, from, to, at.UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatalf("seed history: %v", err)
	}
}

// writeRawReport writes content at the spec's report path without validation.
func writeRawReport(t *testing.T, svc *SDDService, spec *model.Spec, kind model.SpecDocKind, content string) {
	t.Helper()
	p := reportPath(t, svc, spec, kind)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newStatsFixture builds the fixed fixture of C15: four standard specs and
// one trivial, with controlled creation dates, reports, history and items.
func newStatsFixture(t *testing.T) (*SDDService, time.Time) {
	t.Helper()
	ctx := context.Background()
	svc, database, _ := newReviewTestService(t, "wirvii/mneme")
	base := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)

	mk := func(id string, lane model.Lane, status model.SpecStatus, createdDays int, required bool) *model.Spec {
		spec := insertContractTestSpec(t, svc, id, lane, status)
		created := base.AddDate(0, 0, createdDays).UTC().Format(time.RFC3339Nano)
		if _, err := database.Exec(`UPDATE specs SET created_at = ? WHERE id = ?`, created, id); err != nil {
			t.Fatalf("set created_at: %v", err)
		}
		if err := svc.store.SetSpecReviewRequired(ctx, id, required); err != nil {
			t.Fatalf("mark: %v", err)
		}
		return spec
	}
	s1 := mk("SPEC-181", model.LaneStandard, model.SpecStatusDone, 0, true)
	s2 := mk("SPEC-182", model.LaneStandard, model.SpecStatusImplementing, 1, true)
	s3 := mk("SPEC-183", model.LaneStandard, model.SpecStatusSpecced, 2, true)
	s4 := mk("SPEC-184", model.LaneStandard, model.SpecStatusSpecced, 3, false)
	mk("SPEC-185", model.LaneTrivial, model.SpecStatusDone, 4, false)

	// S1: two qa rejections, one reopening, done twice (the LAST entry counts).
	seedHistory(t, database, s1.ID, "implementing", "qa", base)
	seedHistory(t, database, s1.ID, "qa", "implementing", base.Add(1*time.Hour))
	seedHistory(t, database, s1.ID, "qa", "implementing", base.Add(2*time.Hour))
	seedHistory(t, database, s1.ID, "qa", "done", base.Add(3*time.Hour))
	seedHistory(t, database, s1.ID, "done", "implementing", base.Add(4*time.Hour))
	seedHistory(t, database, s1.ID, "qa", "done", base.Add(5*time.Hour))

	writeRawReport(t, svc, s1, model.SpecDocKindReviewSpec, statsReportWithFindings)
	writeRawReport(t, svc, s1, model.SpecDocKindReviewPlan, validReviewPlanTOML)
	writeRawReport(t, svc, s3, model.SpecDocKindReviewSpec, "not a report {{{")
	writeRawReport(t, svc, s4, model.SpecDocKindReviewSpec, statsReportNoneRan)
	_ = s2

	doneAt := base.Add(5 * time.Hour)
	addItem := func(title, description, refinement string, createdAt time.Time) {
		item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: title, Description: description, Lane: model.LaneStandard})
		if err != nil {
			t.Fatalf("BacklogAdd: %v", err)
		}
		if refinement != "" {
			if _, err := svc.BacklogRefine(ctx, model.BacklogRefineRequest{ID: item.ID, Refinement: refinement}); err != nil {
				t.Fatalf("BacklogRefine: %v", err)
			}
		}
		if _, err := database.Exec(`UPDATE backlog_items SET created_at = ? WHERE id = ?`, createdAt.UTC().Format(time.RFC3339Nano), item.ID); err != nil {
			t.Fatalf("set item created_at: %v", err)
		}
	}
	addItem("bug tras SPEC-181", "", "", doneAt.Add(24*time.Hour))                // BL-001: title, inside the window
	addItem("otra cosa", "viene de SPEC-181", "", doneAt.Add(48*time.Hour))       // BL-002: description
	addItem("tercera", "", "ver SPEC-181 y su informe", doneAt.Add(72*time.Hour)) // BL-003: refinement
	addItem("antes de done SPEC-181", "", "", doneAt.Add(-24*time.Hour))          // BL-004: before done
	addItem("muy tarde SPEC-181", "", "", doneAt.Add(22*24*time.Hour))            // BL-005: after 21 days
	addItem("solo prefijo SPEC-18", "", "", doneAt.Add(24*time.Hour))             // BL-006: id prefix only
	addItem("sin mencion", "", "", doneAt.Add(24*time.Hour))                      // BL-007: no mention
	addItem("justo en el borde SPEC-181", "", "", doneAt.Add(followUpWindow))     // BL-008: exactly 21 days: inside
	addItem("de SPEC-1810 no cuenta", "", "", doneAt.Add(24*time.Hour))           // BL-009: longer id
	return svc, doneAt
}

// TestSpecReviewStats covers SPEC-181 C15 on a fixed fixture.
func TestSpecReviewStats(t *testing.T) {
	ctx := context.Background()
	svc, doneAt := newStatsFixture(t)

	t.Run("trivial excluded, newest created first, limit 0 returns all", func(t *testing.T) {
		rows, err := svc.SpecReviewStats(ctx, "", 0)
		if err != nil {
			t.Fatalf("SpecReviewStats: %v", err)
		}
		var ids []string
		for _, r := range rows {
			ids = append(ids, r.SpecID)
		}
		want := []string{"SPEC-184", "SPEC-183", "SPEC-182", "SPEC-181"}
		if !reflect.DeepEqual(ids, want) {
			t.Fatalf("ids = %v, want %v", ids, want)
		}
	})

	t.Run("limit trims the newest", func(t *testing.T) {
		rows, err := svc.SpecReviewStats(ctx, "", 2)
		if err != nil || len(rows) != 2 || rows[0].SpecID != "SPEC-184" || rows[1].SpecID != "SPEC-183" {
			t.Fatalf("rows = %v err = %v", rows, err)
		}
	})

	rows, err := svc.SpecReviewStats(ctx, "", 0)
	if err != nil {
		t.Fatalf("SpecReviewStats: %v", err)
	}
	byID := map[string]*model.SpecReviewStats{}
	for _, r := range rows {
		byID[r.SpecID] = r
	}

	t.Run("a reviewed spec carries its counts, rejections and follow-ups", func(t *testing.T) {
		r := byID["SPEC-181"]
		if r.DoneAt == nil || !r.DoneAt.Equal(doneAt) {
			t.Errorf("DoneAt = %v, want %v", r.DoneAt, doneAt)
		}
		if !r.ReviewRequired || !r.Reviewed || r.Status != "done" {
			t.Errorf("flags: required=%v reviewed=%v status=%s", r.ReviewRequired, r.Reviewed, r.Status)
		}
		wantSpec := model.ReviewMomentStats{Present: true, Requested: 3, Completed: 3, Blocker: 1, Major: 1, Nit: 1, Fix: 2, Escalate: 1}
		if r.SpecReview != wantSpec {
			t.Errorf("SpecReview = %+v, want %+v", r.SpecReview, wantSpec)
		}
		wantPlan := model.ReviewMomentStats{Present: true, Requested: 2, Completed: 2}
		if r.PlanReview != wantPlan {
			t.Errorf("PlanReview = %+v, want %+v", r.PlanReview, wantPlan)
		}
		if r.QARejections != 2 || r.DoneReopenings != 1 {
			t.Errorf("rejections = %d reopenings = %d, want 2 and 1", r.QARejections, r.DoneReopenings)
		}
		wantItems := []string{"BL-001", "BL-002", "BL-003", "BL-008"}
		if !reflect.DeepEqual(r.FollowUpItems, wantItems) {
			t.Errorf("FollowUpItems = %v, want %v", r.FollowUpItems, wantItems)
		}
	})

	t.Run("a spec that never reached done has no done_at and an empty, non-nil follow-up list", func(t *testing.T) {
		r := byID["SPEC-182"]
		if r.DoneAt != nil {
			t.Errorf("DoneAt = %v, want nil", r.DoneAt)
		}
		if r.FollowUpItems == nil || len(r.FollowUpItems) != 0 {
			t.Errorf("FollowUpItems = %#v, want empty non-nil", r.FollowUpItems)
		}
	})

	t.Run("missing reports are absent without error", func(t *testing.T) {
		r := byID["SPEC-182"]
		if r.SpecReview != (model.ReviewMomentStats{}) || r.PlanReview != (model.ReviewMomentStats{}) || r.Reviewed {
			t.Errorf("spec without reports: %+v", r)
		}
	})

	t.Run("an unparseable report shows its error and does not fail the row", func(t *testing.T) {
		r := byID["SPEC-183"]
		if !r.SpecReview.Present || r.SpecReview.Error == "" || r.Reviewed {
			t.Errorf("SpecReview = %+v reviewed=%v, want present with error and not reviewed", r.SpecReview, r.Reviewed)
		}
		if !strings.Contains(r.SpecReview.Error, "specreview") {
			t.Errorf("error text = %q", r.SpecReview.Error)
		}
	})

	t.Run("reviewed is false when no reviewer completed", func(t *testing.T) {
		r := byID["SPEC-184"]
		if !r.SpecReview.Present || r.SpecReview.DidNotRun != 3 || r.SpecReview.Completed != 0 || r.Reviewed {
			t.Errorf("SpecReview = %+v reviewed=%v", r.SpecReview, r.Reviewed)
		}
		if r.ReviewRequired {
			t.Error("SPEC-184 was seeded unmarked")
		}
	})

	t.Run("without a workflow dir both moments say so and the rest is still computed", func(t *testing.T) {
		svc.config.Workflow.Dir = ""
		rows, err := svc.SpecReviewStats(ctx, "", 0)
		if err != nil {
			t.Fatalf("SpecReviewStats: %v", err)
		}
		for _, r := range rows {
			if r.SpecReview.Present || r.SpecReview.Error != "sin directorio de workflow" || r.PlanReview.Error != "sin directorio de workflow" {
				t.Errorf("%s: moments = %+v / %+v", r.SpecID, r.SpecReview, r.PlanReview)
			}
		}
		for _, r := range rows {
			if r.SpecID == "SPEC-181" && (r.QARejections != 2 || len(r.FollowUpItems) != 4) {
				t.Errorf("rejections and items must still be computed: %+v", r)
			}
		}
	})

	t.Run("a nil config behaves like no workflow dir", func(t *testing.T) {
		svc.config = nil
		rows, err := svc.SpecReviewStats(ctx, "", 1)
		if err != nil || len(rows) != 1 || rows[0].SpecReview.Error != "sin directorio de workflow" {
			t.Fatalf("rows = %+v err = %v", rows, err)
		}
	})
}

// TestCountRejections covers SPEC-181 C15: the shared derivation, and that
// LaneStatus.RejectionCount still equals FromQA + FromAudit.
func TestCountRejections(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	history := []*model.SpecHistory{
		{FromStatus: model.SpecStatusSpeccing, ToStatus: model.SpecStatusSpecced, At: at},
		{FromStatus: model.SpecStatusQA, ToStatus: model.SpecStatusImplementing, At: at},
		{FromStatus: model.SpecStatusQA, ToStatus: model.SpecStatusImplementing, At: at},
		{FromStatus: model.SpecStatusAudit, ToStatus: model.SpecStatusImplementing, At: at},
		{FromStatus: model.SpecStatusDone, ToStatus: model.SpecStatusImplementing, At: at},
		{FromStatus: model.SpecStatusImplementing, ToStatus: model.SpecStatusQA, At: at},
	}
	want := rejectionCounts{FromQA: 2, FromAudit: 1, FromDone: 1}
	if got := countRejections(history); got != want {
		t.Fatalf("countRejections = %+v, want %+v", got, want)
	}

	svc, database, _ := newReviewTestService(t, "p")
	spec := insertContractTestSpec(t, svc, "SPEC-100", model.LaneStandard, model.SpecStatusImplementing)
	for i, h := range history {
		seedHistory(t, database, spec.ID, string(h.FromStatus), string(h.ToStatus), at.Add(time.Duration(i)*time.Minute))
	}
	resp, err := svc.LaneStatus(context.Background(), spec.ID)
	if err != nil {
		t.Fatalf("LaneStatus: %v", err)
	}
	if resp.RejectionCount != 3 {
		t.Errorf("LaneStatus.RejectionCount = %d, want 3", resp.RejectionCount)
	}
}
