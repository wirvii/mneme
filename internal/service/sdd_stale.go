package service

import (
	"sort"
	"time"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/model"
)

// classifyStaleness is SPEC-186's single definition of "stale". now is the
// injected clock; frozen holds the IDs specFreeze already marked. Each spec
// goes through reviewStatus(thresholdDays, lastMove, now) (review.go), with
// lastMove the later of its updated_at and lastHistory[id].
//
// It returns the age of EVERY spec received (done ones included, never
// stale) and the report: stale specs ordered by days descending then ID
// ascending, ByStatus with only statuses that have at least one, and
// FrozenCount over every open frozen spec whatever its age. A threshold of
// 0 disables the whole announcement: reviewStatus never reports due for a
// deadline <= 0. Pure: no clock, no I/O. It is the only function that
// compares against the threshold; no frontend computes it again.
func classifyStaleness(specs []*model.Spec, lastHistory map[string]time.Time,
	frozen map[string]model.SpecFreeze, now time.Time, thresholdDays int,
) (map[string]model.SpecAge, model.StaleReport) {
	ages := make(map[string]model.SpecAge, len(specs))
	report := model.StaleReport{ThresholdDays: thresholdDays}
	for _, spec := range specs {
		lastMove := spec.UpdatedAt
		if h, ok := lastHistory[spec.ID]; ok && h.After(lastMove) {
			lastMove = h
		}
		due, ageDays := reviewStatus(thresholdDays, lastMove, now)
		days := max(0, ageDays)
		_, isFrozen := frozen[spec.ID]
		open := !spec.Status.IsFinal()
		stale := due && open && !isFrozen
		ages[spec.ID] = model.SpecAge{DaysSinceLastMove: days, Stale: stale}

		if isFrozen && open {
			report.FrozenCount++
		}
		if !stale {
			continue
		}
		if report.ByStatus == nil {
			report.ByStatus = make(map[model.SpecStatus]int)
		}
		report.ByStatus[spec.Status]++
		report.Specs = append(report.Specs, model.StaleSpec{
			ID:                spec.ID,
			Title:             spec.Title,
			Status:            spec.Status,
			DaysSinceLastMove: days,
		})
	}
	sort.Slice(report.Specs, func(i, j int) bool {
		a, b := report.Specs[i], report.Specs[j]
		if a.DaysSinceLastMove != b.DaysSinceLastMove {
			return a.DaysSinceLastMove > b.DaysSinceLastMove
		}
		return a.ID < b.ID
	})
	report.Count = len(report.Specs)
	return ages, report
}

// staleAfterDays is the configured [spec] stale_after_days, or its default
// when the service was built without a config (test services). 0 is
// returned as is: it means disabled.
func (svc *SDDService) staleAfterDays() int {
	if svc.config == nil {
		return config.DefaultStaleAfterDays
	}
	return svc.config.Spec.StaleAfterDays
}
