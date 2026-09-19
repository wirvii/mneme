package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/wirvii/mneme/internal/model"
)

const (
	defaultWorkMetricLimit = 20
	maxWorkMetricLimit     = 50
)

var workMetricIDPattern = regexp.MustCompile(`^WORK-[0-9]+$`)

func normalizeWorkMetricRequest(req model.WorkMetricsRequest, defaultProject string) (string, []string, int, error) {
	project := strings.TrimSpace(req.Project)
	if project == "" {
		project = defaultProject
	}
	if req.Limit < 0 {
		return "", nil, 0, fmt.Errorf("%w: limit: must be non-negative", model.ErrInvalidContract)
	}
	if len(req.IDs) > 0 && req.Limit != 0 {
		return "", nil, 0, fmt.Errorf("%w: limit: forbidden with ids", model.ErrInvalidContract)
	}
	if len(req.IDs) > maxWorkMetricLimit {
		return "", nil, 0, fmt.Errorf("%w: ids: maximum is %d", model.ErrInvalidContract, maxWorkMetricLimit)
	}
	ids := append([]string(nil), req.IDs...)
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !workMetricIDPattern.MatchString(id) {
			return "", nil, 0, fmt.Errorf("%w: ids: invalid %q", model.ErrInvalidContract, id)
		}
		if seen[id] {
			return "", nil, 0, fmt.Errorf("%w: ids: duplicate %s", model.ErrInvalidContract, id)
		}
		seen[id] = true
	}
	if len(ids) > 0 {
		return project, ids, len(ids), nil
	}
	limit := req.Limit
	if limit == 0 {
		limit = defaultWorkMetricLimit
	}
	if limit > maxWorkMetricLimit {
		limit = maxWorkMetricLimit
	}
	return project, ids, limit, nil
}

// WorkMetrics returns derived measurements without changing work, repository, or certificate state.
func (svc *SDDService) WorkMetrics(ctx context.Context, req model.WorkMetricsRequest) (model.WorkMetricsResponse, error) {
	project, ids, limit, err := normalizeWorkMetricRequest(req, svc.project)
	if err != nil {
		return model.WorkMetricsResponse{}, err
	}
	facts, total, unreadable, err := svc.store.ListWorkMetricFacts(ctx, project, ids)
	if err != nil {
		return model.WorkMetricsResponse{}, err
	}
	details := make([]model.WorkMetric, 0, len(facts))
	for _, fact := range facts {
		metric, deriveErr := model.DeriveWorkMetric(fact)
		if deriveErr != nil {
			unreadable = append(unreadable, model.UnreadableRow{Kind: "work", ID: fact.Contract.ID, Column: "metrics", Reason: deriveErr.Error()})
			continue
		}
		details = append(details, metric)
	}
	if len(ids) > 0 {
		byID := make(map[string]model.WorkMetric, len(details))
		for _, detail := range details {
			byID[detail.ID] = detail
		}
		ordered := make([]model.WorkMetric, 0, len(details))
		for _, id := range ids {
			if detail, ok := byID[id]; ok {
				ordered = append(ordered, detail)
			}
		}
		details = ordered
	}
	response := model.WorkMetricsResponse{
		Project:  project,
		Total:    total,
		Included: len(details),
		Summary:  model.SummarizeWorkMetrics(details),
	}
	response.Unreadable, response.UnreadableCount = capMetricUnreadable(unreadable)
	if len(details) > limit {
		details = details[:limit]
	}
	response.Details = details
	return response, nil
}

func capMetricUnreadable(rows []model.UnreadableRow) ([]model.UnreadableRow, int) {
	total := len(rows)
	if total > model.MaxUnreadableListed {
		rows = rows[:model.MaxUnreadableListed]
	}
	return append([]model.UnreadableRow(nil), rows...), total
}
