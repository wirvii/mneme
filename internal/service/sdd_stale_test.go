package service

import (
	"reflect"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/model"
)

// staleTestDay is one whole day, the unit every staleness fixture is
// expressed in.
const staleTestDay = 24 * time.Hour

// staleTestNow is the fixed clock every staleness test measures against.
var staleTestNow = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

// daysAgo returns the instant n whole days before staleTestNow.
func daysAgo(n int) time.Time {
	return staleTestNow.Add(-time.Duration(n) * staleTestDay)
}

// TestClassifyStaleness pins SPEC-186 C1: the pure classifier with an
// injected clock and threshold 14 — the strict whole-day boundary, the
// future clamp, done and frozen specs never stale, a spec without a
// BacklogID never frozen, the report order and ByStatus, and 0 disabling
// everything.
func TestClassifyStaleness(t *testing.T) {
	const threshold = 14
	specs := []*model.Spec{
		{ID: "SPEC-010", Title: "fifteen", Status: model.SpecStatusQA, UpdatedAt: daysAgo(15)},
		{ID: "SPEC-011", Title: "thirteen", Status: model.SpecStatusDraft, UpdatedAt: daysAgo(13)},
		{ID: "SPEC-012", Title: "edge", Status: model.SpecStatusDraft, UpdatedAt: daysAgo(15).Add(time.Nanosecond)},
		{ID: "SPEC-013", Title: "future", Status: model.SpecStatusDraft, UpdatedAt: staleTestNow.Add(2 * staleTestDay)},
		{ID: "SPEC-014", Title: "done", Status: model.SpecStatusDone, UpdatedAt: daysAgo(90)},
		{ID: "SPEC-015", Title: "frozen", Status: model.SpecStatusSpecced, BacklogID: "BL-001", UpdatedAt: daysAgo(90)},
		{ID: "SPEC-017", Title: "no backlog b", Status: model.SpecStatusSpecced, UpdatedAt: daysAgo(90)},
		{ID: "SPEC-016", Title: "no backlog a", Status: model.SpecStatusSpecced, UpdatedAt: daysAgo(90)},
		{ID: "SPEC-018", Title: "qa twenty", Status: model.SpecStatusQA, UpdatedAt: daysAgo(20)},
	}
	frozen := map[string]model.SpecFreeze{
		"SPEC-015": {State: model.SpecFreezeArchived, BacklogID: "BL-001"},
	}

	ages, report := classifyStaleness(specs, nil, frozen, staleTestNow, threshold)

	cases := []struct {
		id    string
		stale bool
		days  int
	}{
		{"SPEC-010", true, 15},
		{"SPEC-011", false, 13},
		{"SPEC-012", false, 14},
		{"SPEC-013", false, 0},
		{"SPEC-014", false, 90},
		{"SPEC-015", false, 90},
		{"SPEC-016", true, 90},
		{"SPEC-017", true, 90},
		{"SPEC-018", true, 20},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			got, ok := ages[tc.id]
			if !ok {
				t.Fatalf("no age for %s", tc.id)
			}
			want := model.SpecAge{DaysSinceLastMove: tc.days, Stale: tc.stale}
			if got != want {
				t.Errorf("age = %+v, want %+v", got, want)
			}
		})
	}
	if len(ages) != len(specs) {
		t.Errorf("len(ages) = %d, want one per spec (%d)", len(ages), len(specs))
	}

	t.Run("Report", func(t *testing.T) {
		wantIDs := []string{"SPEC-016", "SPEC-017", "SPEC-018", "SPEC-010"}
		var gotIDs []string
		for _, s := range report.Specs {
			gotIDs = append(gotIDs, s.ID)
		}
		if !reflect.DeepEqual(gotIDs, wantIDs) {
			t.Errorf("report order = %v, want %v (days desc, then ID asc)", gotIDs, wantIDs)
		}
		if report.Count != 4 || report.Count != len(report.Specs) {
			t.Errorf("Count = %d (len %d), want 4", report.Count, len(report.Specs))
		}
		wantBy := map[model.SpecStatus]int{model.SpecStatusSpecced: 2, model.SpecStatusQA: 2}
		if !reflect.DeepEqual(report.ByStatus, wantBy) {
			t.Errorf("ByStatus = %v, want %v", report.ByStatus, wantBy)
		}
		if report.FrozenCount != 1 {
			t.Errorf("FrozenCount = %d, want 1", report.FrozenCount)
		}
		if report.ThresholdDays != threshold {
			t.Errorf("ThresholdDays = %d, want %d", report.ThresholdDays, threshold)
		}
		if len(report.Specs) == 0 {
			t.Fatal("report has no stale specs")
		}
		first := report.Specs[0]
		if first.Title != "no backlog a" || first.Status != model.SpecStatusSpecced || first.DaysSinceLastMove != 90 {
			t.Errorf("first row = %+v, want SPEC-016 specced 90 d", first)
		}
	})

	t.Run("ThresholdZeroDisables", func(t *testing.T) {
		ages0, report0 := classifyStaleness(specs, nil, frozen, staleTestNow, 0)
		for id, age := range ages0 {
			if age.Stale {
				t.Errorf("%s stale with threshold 0", id)
			}
		}
		if report0.Count != 0 || len(report0.Specs) != 0 || len(report0.ByStatus) != 0 {
			t.Errorf("report with threshold 0 = %+v, want nothing stale", report0)
		}
		if report0.ThresholdDays != 0 {
			t.Errorf("ThresholdDays = %d, want 0", report0.ThresholdDays)
		}
		if ages0["SPEC-014"].DaysSinceLastMove != 90 {
			t.Errorf("ages are still computed with threshold 0: got %+v", ages0["SPEC-014"])
		}
	})

	t.Run("UsesLatestHistoryOverUpdatedAt", func(t *testing.T) {
		one := []*model.Spec{{ID: "SPEC-020", Status: model.SpecStatusQA, UpdatedAt: daysAgo(40)}}
		history := map[string]time.Time{"SPEC-020": daysAgo(3)}
		agesH, reportH := classifyStaleness(one, history, nil, staleTestNow, threshold)
		if got := agesH["SPEC-020"]; got != (model.SpecAge{DaysSinceLastMove: 3}) {
			t.Errorf("age = %+v, want 3 days, not stale (history is the later mark)", got)
		}
		if reportH.Count != 0 {
			t.Errorf("Count = %d, want 0", reportH.Count)
		}
	})
}

// TestStaleAfterDays pins where the threshold comes from: the service's
// config, 0 respected as "disabled", and the default when a test service
// was built without a config.
func TestStaleAfterDays(t *testing.T) {
	svc := newTestSDDService(t, "stale-threshold")
	if got := svc.staleAfterDays(); got != 14 {
		t.Errorf("default config threshold = %d, want 14", got)
	}
	svc.config.Spec.StaleAfterDays = 0
	if got := svc.staleAfterDays(); got != 0 {
		t.Errorf("configured 0 = %d, want 0 (disabled, not replaced by the default)", got)
	}
	bare := &SDDService{}
	if got := bare.staleAfterDays(); got != 14 {
		t.Errorf("nil config threshold = %d, want the default 14", got)
	}
}
