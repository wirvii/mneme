package service

import (
	"context"
	"fmt"
	"log"
	"os"
	"regexp"
	"sort"
	"strings"
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

// followUpWindow is how long after a spec reaches done a new backlog item
// still counts as a possible bug on that spec (SPEC-181 P1-c).
const followUpWindow = 21 * 24 * time.Hour

// noWorkflowDirMessage is the error text of a moment when the workflow
// mechanism is off, so there is nowhere to look for reports.
const noWorkflowDirMessage = "sin directorio de workflow"

// backlogMention is one backlog item flattened for citation search: when it
// was created and all the text a person could have cited a spec id in.
type backlogMention struct {
	id        string
	createdAt time.Time
	text      string
}

// SpecReviewStats returns one row per standard spec of the project, newest
// created first, for the EXPERIMENTAL SPEC-181 P1 measurement. limit <= 0
// returns every spec. Trivial specs never appear. It computes nothing about
// the decision rule: it only dumps the numbers (reports, rejections,
// follow-up items) the owner reads by hand.
func (svc *SDDService) SpecReviewStats(ctx context.Context, project string, limit int) ([]*model.SpecReviewStats, error) {
	if project == "" {
		project = svc.project
	}
	specs, _, unreadableSpecs, err := svc.store.ListSpecs(ctx, project, "", 0)
	if err != nil {
		return nil, fmt.Errorf("service: spec review stats: list specs: %w", err)
	}
	logUnreadableRows("spec review stats: specs", unreadableSpecs)

	var standard []*model.Spec
	for i := len(specs) - 1; i >= 0; i-- { // ListSpecs is oldest first: reverse so ties keep the newest first
		if specs[i].Lane == model.LaneStandard {
			standard = append(standard, specs[i])
		}
	}
	sort.SliceStable(standard, func(i, j int) bool { return standard[i].CreatedAt.After(standard[j].CreatedAt) })
	if limit > 0 && len(standard) > limit {
		standard = standard[:limit]
	}

	mentions, err := svc.backlogMentions(ctx, project)
	if err != nil {
		return nil, err
	}

	rows := make([]*model.SpecReviewStats, 0, len(standard))
	for _, spec := range standard {
		history, err := svc.store.GetSpecHistory(ctx, spec.ID)
		if err != nil {
			return nil, fmt.Errorf("service: spec review stats: history of %s: %w", spec.ID, err)
		}
		row := &model.SpecReviewStats{
			SpecID:         spec.ID,
			Title:          spec.Title,
			Status:         string(spec.Status),
			ReviewRequired: spec.ReviewRequired,
			FollowUpItems:  []string{},
		}
		rejections := countRejections(history)
		row.QARejections = rejections.FromQA + rejections.FromAudit
		row.DoneReopenings = rejections.FromDone

		row.SpecReview = svc.reviewMomentStats(spec, model.SpecDocKindReviewSpec, specreview.MomentSpec)
		row.PlanReview = svc.reviewMomentStats(spec, model.SpecDocKindReviewPlan, specreview.MomentPlan)
		row.Reviewed = row.SpecReview.Present && row.SpecReview.Error == "" && row.SpecReview.Completed > 0

		if doneAt, ok := latestEntry(history, model.SpecStatusDone); ok {
			row.DoneAt = &doneAt
			row.FollowUpItems = followUps(spec.ID, doneAt, mentions)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// reviewMomentStats reads and parses one moment's report for spec. A missing
// report is absent without error; an unreadable or unparseable one is present
// with the error as text, so a single bad file never fails the whole table.
func (svc *SDDService) reviewMomentStats(spec *model.Spec, kind model.SpecDocKind, moment specreview.Moment) model.ReviewMomentStats {
	if svc.config == nil || svc.config.WorkflowDir() == "" {
		return model.ReviewMomentStats{Error: noWorkflowDirMessage}
	}
	path, err := specDocPath(svc.config.WorkflowDir(), spec.Project, spec.ID, kind)
	if err != nil {
		return model.ReviewMomentStats{Error: err.Error()}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return model.ReviewMomentStats{}
		}
		return model.ReviewMomentStats{Present: true, Error: err.Error()}
	}
	report, err := specreview.Parse(raw, moment)
	if err != nil {
		return model.ReviewMomentStats{Present: true, Error: err.Error()}
	}
	c := report.Counts()
	return model.ReviewMomentStats{
		Present:      true,
		Requested:    c.Requested,
		Completed:    c.Completed,
		DidNotRun:    c.DidNotRun,
		Blocker:      c.Blocker,
		Major:        c.Major,
		Nit:          c.Nit,
		Fix:          c.Fix,
		Refute:       c.Refute,
		Escalate:     c.Escalate,
		Discard:      c.Discard,
		AcceptedRisk: c.AcceptedRisk,
		ReReview:     c.ReReview,
	}
}

// backlogMentions reads every backlog item of the project once, with its
// refinements, flattened to the text that can cite a spec id.
func (svc *SDDService) backlogMentions(ctx context.Context, project string) ([]backlogMention, error) {
	items, _, unreadableItems, err := svc.store.ListBacklogItems(ctx, project, "", 0)
	if err != nil {
		return nil, fmt.Errorf("service: spec review stats: list backlog: %w", err)
	}
	logUnreadableRows("spec review stats: backlog", unreadableItems)
	mentions := make([]backlogMention, 0, len(items))
	for _, item := range items {
		var b strings.Builder
		b.WriteString(item.Title)
		b.WriteString("\n")
		b.WriteString(item.Description)
		refinements, err := svc.store.ListBacklogRefinements(ctx, item.ID)
		if err != nil {
			return nil, fmt.Errorf("service: spec review stats: refinements of %s: %w", item.ID, err)
		}
		for _, r := range refinements {
			b.WriteString("\n")
			b.WriteString(r.Body)
		}
		mentions = append(mentions, backlogMention{id: item.ID, createdAt: item.CreatedAt, text: b.String()})
	}
	return mentions, nil
}

// logUnreadableRows reports rows that could not be fully read and are
// therefore missing from the measurement. SpecReviewStats returns a plain row
// slice, so the log is where they stay visible (SPEC-133 forbids discarding
// them silently).
func logUnreadableRows(where string, rows []model.UnreadableRow) {
	for _, r := range rows {
		log.Printf("service: %s: %s %s unreadable (%s): %s", where, r.Kind, r.ID, r.Column, r.Reason)
	}
}

// followUps returns the ids of the items created in (doneAt, doneAt+21d]
// whose text cites specID as a whole word: SPEC-18 never matches SPEC-181.
// Ids are local to this machine. The result is never nil.
func followUps(specID string, doneAt time.Time, mentions []backlogMention) []string {
	cite := regexp.MustCompile(`\b` + regexp.QuoteMeta(specID) + `\b`)
	out := []string{}
	for _, m := range mentions {
		if !m.createdAt.After(doneAt) || m.createdAt.After(doneAt.Add(followUpWindow)) {
			continue
		}
		if cite.MatchString(m.text) {
			out = append(out, m.id)
		}
	}
	return out
}
