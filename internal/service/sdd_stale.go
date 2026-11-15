package service

import (
	"context"
	"fmt"
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

// specAges classifies every spec of project (SPEC-186 §4, amended by E2).
// When pageIsComplete is true, page already holds the project's full,
// unwindowed spec list and no second read of the specs table happens;
// otherwise page is ignored and the full list is read here. The unreadable
// relation of whichever list was classified is counted into
// StaleReport.UnreadableCount: SPEC-133 D2 made it a return value so every
// caller has to do something visible with it.
//
// ages and frozen cover the whole project, not the page. Cost: at most one
// read of specs, one of spec_history and one of backlog_items. Read-only:
// it never writes.
func (svc *SDDService) specAges(ctx context.Context, project string,
	page []*model.Spec, pageUnreadable []model.UnreadableRow, pageIsComplete bool,
) (ages map[string]model.SpecAge, report model.StaleReport, frozen map[string]model.SpecFreeze, err error) {
	all, unreadable := page, pageUnreadable
	if !pageIsComplete {
		all, _, unreadable, err = svc.store.ListSpecs(ctx, project, "", 0)
		if err != nil {
			return nil, model.StaleReport{}, nil, fmt.Errorf("service: spec ages: %w", err)
		}
	}

	lastHistory, err := svc.store.LatestSpecHistoryAt(ctx, project)
	if err != nil {
		return nil, model.StaleReport{}, nil, fmt.Errorf("service: spec ages: %w", err)
	}

	frozen, err = svc.freezeIndex(ctx, all)
	if err != nil {
		return nil, model.StaleReport{}, nil, fmt.Errorf("service: spec ages: %w", err)
	}

	ages, report = classifyStaleness(all, lastHistory, frozen, svc.now(), svc.staleAfterDays())
	report.UnreadableCount = len(unreadable)
	return ages, report, frozen, nil
}

// StaleSpecs reports every open spec of project that has not moved for more
// than [spec] stale_after_days (SPEC-186), for the session-start hook. It is
// SpecList's whole-project report, so the hook and the listings can never
// disagree. project == "" means the service's own project. Read-only: it
// never writes (SPEC-186 D3).
func (svc *SDDService) StaleSpecs(ctx context.Context, project string) (model.StaleReport, error) {
	resp, err := svc.SpecList(ctx, model.SpecListRequest{Project: project})
	if err != nil {
		return model.StaleReport{}, fmt.Errorf("service: stale specs: %w", err)
	}
	return resp.Stale, nil
}
