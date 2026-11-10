package service

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/specreview"
)

// validateReviewDoc parses a fresh-review report before spec_doc_write puts
// it on disk (SPEC-181 D1/D2): review-spec content must be a valid report of
// the spec moment, review-plan content a valid report of the plan moment. Any
// other kind returns nil. The error wraps model.ErrInvalidReviewReport and
// keeps the specreview cause, so a rejected document never touches the disk.
func validateReviewDoc(kind model.SpecDocKind, content string) error {
	var moment specreview.Moment
	switch kind {
	case model.SpecDocKindReviewSpec:
		moment = specreview.MomentSpec
	case model.SpecDocKindReviewPlan:
		moment = specreview.MomentPlan
	default:
		return nil
	}
	if _, err := specreview.Parse([]byte(content), moment); err != nil {
		return fmt.Errorf("service: spec doc write: %w: %w", model.ErrInvalidReviewReport, err)
	}
	return nil
}

// reviewCheckpoint maps a guarded transition to the report it demands
// (SPEC-181 D3): the kind that writes it, its moment, the stage the report
// must not predate, and the moment number used in messages. ok is false for
// every transition the guard does not watch.
func reviewCheckpoint(from, next model.SpecStatus) (kind model.SpecDocKind, moment specreview.Moment, ref model.SpecStatus, n int, ok bool) {
	switch {
	case from == model.SpecStatusSpeccing && next == model.SpecStatusSpecced,
		from == model.SpecStatusSpecced && next == model.SpecStatusPlanning:
		return model.SpecDocKindReviewSpec, specreview.MomentSpec, model.SpecStatusSpeccing, 1, true
	case from == model.SpecStatusPlanned && next == model.SpecStatusImplementing:
		return model.SpecDocKindReviewPlan, specreview.MomentPlan, model.SpecStatusPlanning, 2, true
	default:
		return "", "", "", 0, false
	}
}

// latestEntry returns the time of the most recent history row that entered
// status, and whether there is one.
func latestEntry(history []*model.SpecHistory, status model.SpecStatus) (time.Time, bool) {
	var latest time.Time
	found := false
	for _, h := range history {
		if h.ToStatus != status {
			continue
		}
		if !found || h.At.After(latest) {
			latest = h.At
			found = true
		}
	}
	return latest, found
}

// staleCandidate is one thing a report must not predate, with the wording
// used to name it in the error.
type staleCandidate struct {
	at     time.Time
	reason string
}

// ensureReviewReport is the SPEC-181 D3 guard. It returns nil unless the spec
// is standard, marked review_required, the workflow mechanism is on and the
// transition is one of the three guarded ones; then the report must exist,
// parse for its moment and be current: not older than the last entry to the
// reference stage, nor than tarea.md or criteria.toml. All timestamps are
// truncated to the second before comparing (file systems round mtime and
// spec_history.at carries more precision) and a tie counts as current. It
// returns errors without a layer prefix; SpecAdvance adds its own.
func (svc *SDDService) ensureReviewReport(ctx context.Context, spec *model.Spec, next model.SpecStatus) error {
	if spec.Lane != model.LaneStandard || !spec.ReviewRequired {
		return nil
	}
	if svc.config == nil || svc.config.WorkflowDir() == "" {
		return nil
	}
	kind, moment, ref, n, ok := reviewCheckpoint(spec.Status, next)
	if !ok {
		return nil
	}

	workflowDir := svc.config.WorkflowDir()
	path, err := specDocPath(workflowDir, spec.Project, spec.ID, kind)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("falta %s — escribe el informe de revisión del momento %d con spec_doc_write kind %s (skill spec-review): %w",
				path, n, kind, model.ErrReviewReportNotFound)
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	if _, err := specreview.Parse(raw, moment); err != nil {
		return fmt.Errorf("%s no es un informe de revisión válido (skill spec-review): %w: %w", path, model.ErrInvalidReviewReport, err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	reportAt := info.ModTime().Truncate(time.Second)

	var newer []staleCandidate
	history, histErr := svc.store.GetSpecHistory(ctx, spec.ID)
	entered, entryFound := latestEntry(history, ref)
	switch {
	case histErr != nil || !entryFound:
		return fmt.Errorf("%s está caducado: es anterior a la historia de la spec (no se pudo leer la entrada a %s) — repite la revisión o reescribe el informe (skill spec-review): %w",
			path, ref, model.ErrReviewReportStale)
	case reportAt.Before(entered.Truncate(time.Second)):
		newer = append(newer, staleCandidate{entered, fmt.Sprintf("la entrada a %s del %s", ref, entered.UTC().Format(time.RFC3339))})
	}
	for _, docKind := range []model.SpecDocKind{model.SpecDocKindTask, model.SpecDocKindCriteria} {
		docPath, err := specDocPath(workflowDir, spec.Project, spec.ID, docKind)
		if err != nil {
			return err
		}
		docInfo, err := os.Stat(docPath)
		if err != nil {
			continue // a reference file that does not exist is not compared
		}
		if docAt := docInfo.ModTime(); reportAt.Before(docAt.Truncate(time.Second)) {
			newer = append(newer, staleCandidate{docAt, docPath})
		}
	}
	if len(newer) == 0 {
		return nil
	}
	latest := newer[0]
	for _, c := range newer[1:] {
		if c.at.After(latest.at) {
			latest = c
		}
	}
	return fmt.Errorf("%s está caducado: es anterior a %s — repite la revisión o reescribe el informe (skill spec-review): %w",
		path, latest.reason, model.ErrReviewReportStale)
}
