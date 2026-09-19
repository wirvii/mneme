package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/quality"
	"github.com/wirvii/mneme/internal/store"
)

// WorkReview records the single initial review against the exact clean repository HEAD.
func (svc *SDDService) WorkReview(ctx context.Context, req model.WorkReviewRequest) (model.WorkCapabilityResult, error) {
	if err := svc.requireDeliveryV2(); err != nil {
		return model.WorkCapabilityResult{}, err
	}
	if err := validateWorkReviewRequest(req); err != nil {
		return model.WorkCapabilityResult{}, err
	}
	aggregate, err := svc.store.GetWorkAggregate(ctx, req.ID)
	if err != nil {
		return model.WorkCapabilityResult{}, err
	}
	if aggregate.Contract.Status != model.WorkStatusImplementing {
		return model.WorkCapabilityResult{}, model.ErrInvalidWorkTransition
	}
	if strings.TrimSpace(svc.repoDir) == "" {
		return model.WorkCapabilityResult{}, fmt.Errorf("%w: repo_dir: required", model.ErrInvalidContract)
	}
	if svc.deliveryRunnerFactory == nil {
		return model.WorkCapabilityResult{}, fmt.Errorf("%w: delivery runner factory: required", model.ErrInvalidContract)
	}
	if strings.TrimSpace(svc.mnemeVersion) == "" {
		return model.WorkCapabilityResult{}, fmt.Errorf("%w: mneme version: required", model.ErrInvalidContract)
	}
	g := &quality.Git{RepoDir: svc.repoDir}
	head, err := g.HeadSHA()
	if err != nil {
		return model.WorkCapabilityResult{}, err
	}
	if head != req.HeadSHA {
		return model.WorkCapabilityResult{}, fmt.Errorf("%w: head_sha does not match repository HEAD", model.ErrInvalidContract)
	}
	dirty, _, err := g.IsDirty()
	if err != nil {
		return model.WorkCapabilityResult{}, err
	}
	if dirty {
		return model.WorkCapabilityResult{}, fmt.Errorf("%w: worktree must be clean for review", model.ErrInvalidContract)
	}
	findings := make([]*model.WorkFinding, len(req.Findings))
	for i, input := range req.Findings {
		findings[i] = &model.WorkFinding{
			Category: input.Category, Severity: input.Severity, Description: input.Description,
			Location: input.Location, Evidence: input.Evidence,
		}
	}
	architectureChecks, architectureFindings, err := architectureReview(aggregate.Constraints, req.ArchitectureVerdicts)
	if err != nil {
		return model.WorkCapabilityResult{}, err
	}
	findings = append(findings, architectureFindings...)
	existingBlocking, err := svc.store.CountOpenBlockingFindings(ctx, req.ID)
	if err != nil {
		return model.WorkCapabilityResult{}, err
	}
	blocking := existingBlocking
	for _, finding := range findings {
		if finding.Category.Blocks() {
			blocking++
		}
	}
	blockingStatus := model.DeliveryCheckPass
	if blocking > 0 {
		blockingStatus = model.DeliveryCheckFail
	}
	reviewChecks := []*model.DeliveryCheck{
		{
			Kind: "review", Name: "initial", Status: model.DeliveryCheckPass,
			Effect: model.DeliveryEffectMeasures, Detail: "initial review supplied for the exact repository HEAD",
		},
		{
			Kind: "review", Name: "open-blocking-findings", Status: blockingStatus,
			Effect: model.DeliveryEffectBlocks, Detail: fmt.Sprintf("%d open blocking finding(s)", blocking),
		},
	}
	evaluation, err := svc.evaluateDelivery(ctx, aggregate, deliveryEvaluationOptions{architectureChecks: architectureChecks, reviewChecks: reviewChecks})
	if err != nil {
		return model.WorkCapabilityResult{}, err
	}
	if err := svc.store.InsertInitialReview(ctx, store.InitialReviewWrite{
		Certificate: evaluation.certificate, Checks: evaluation.checks,
		Observations: evaluation.observations, Findings: findings, By: req.By,
	}); err != nil {
		return model.WorkCapabilityResult{}, err
	}
	work, err := svc.WorkGet(ctx, model.WorkGetRequest{ID: req.ID})
	if err != nil {
		return model.WorkCapabilityResult{}, err
	}
	return model.WorkCapabilityResult{
		Work: work, Operation: "review", Available: true, Performed: true,
		Certificate: evaluation.certificate, Checks: deliveryCheckValues(evaluation.checks),
	}, nil
}

type reviewEvidenceDetail struct {
	EvidenceKind model.ReviewEvidenceKind `json:"evidence_kind"`
	Evidence     string                   `json:"evidence"`
}

func architectureReview(constraints []model.WorkConstraint, verdicts []model.WorkArchitectureVerdictInput) ([]*model.DeliveryCheck, []*model.WorkFinding, error) {
	declared := make(map[string]bool, len(constraints))
	for _, constraint := range constraints {
		declared[constraint.Key] = true
	}
	byKey := make(map[string]model.WorkArchitectureVerdictInput, len(verdicts))
	for _, verdict := range verdicts {
		if !declared[verdict.ConstraintKey] || byKey[verdict.ConstraintKey].ConstraintKey != "" {
			return nil, nil, fmt.Errorf("%w: architecture verdict key %q is unknown or duplicated", model.ErrInvalidContract, verdict.ConstraintKey)
		}
		byKey[verdict.ConstraintKey] = verdict
	}
	keys := make([]string, 0, len(constraints))
	for _, constraint := range constraints {
		keys = append(keys, constraint.Key)
	}
	sort.Strings(keys)
	checks := make([]*model.DeliveryCheck, 0, len(keys))
	var findings []*model.WorkFinding
	for _, key := range keys {
		verdict, ok := byKey[key]
		if !ok {
			checks = append(checks, &model.DeliveryCheck{
				Kind: "architecture", Name: key, Status: model.DeliveryCheckNotReviewed,
				Effect: model.DeliveryEffectBlocks, Detail: "the approved constraint has no architecture verdict in this certificate",
			})
			continue
		}
		raw, _ := json.Marshal(reviewEvidenceDetail{EvidenceKind: verdict.EvidenceKind, Evidence: verdict.Evidence}) //nolint:errcheck // fixed strings cannot fail
		checks = append(checks, &model.DeliveryCheck{
			Kind: "architecture", Name: key, Status: verdict.Status,
			Effect: model.DeliveryEffectBlocks, Detail: string(raw),
		})
		if verdict.Status == model.DeliveryCheckFail {
			location := "constraint:" + key
			if strings.TrimSpace(verdict.Location) != "" {
				location += "; " + verdict.Location
			}
			findings = append(findings, &model.WorkFinding{
				Category: model.FindingArchitectureViolation, Severity: verdict.Severity,
				Description: verdict.Description, Location: location, Evidence: verdict.Evidence,
			})
		}
	}
	return checks, findings, nil
}

func validateWorkReviewRequest(req model.WorkReviewRequest) error {
	for name, value := range map[string]string{"id": req.ID, "by": req.By, "head_sha": req.HeadSHA} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%w: %s: required", model.ErrInvalidContract, name)
		}
	}
	for i, finding := range req.Findings {
		if finding.Category != model.FindingContractViolation && finding.Category != model.FindingRegression && finding.Category != model.FindingDiscovery && finding.Category != model.FindingImprovement {
			return fmt.Errorf("%w: findings[%d].category", model.ErrInvalidContract, i)
		}
		if !finding.Severity.Valid() || strings.TrimSpace(finding.Description) == "" || strings.TrimSpace(finding.Evidence) == "" {
			return fmt.Errorf("%w: findings[%d]", model.ErrInvalidContract, i)
		}
	}
	for i, verdict := range req.ArchitectureVerdicts {
		if strings.TrimSpace(verdict.ConstraintKey) == "" || (verdict.Status != model.DeliveryCheckPass && verdict.Status != model.DeliveryCheckFail) || !verdict.EvidenceKind.Valid() || strings.TrimSpace(verdict.Evidence) == "" {
			return fmt.Errorf("%w: architecture_verdicts[%d]", model.ErrInvalidContract, i)
		}
		if verdict.Status == model.DeliveryCheckFail {
			if !verdict.Severity.Valid() || strings.TrimSpace(verdict.Description) == "" {
				return fmt.Errorf("%w: architecture_verdicts[%d]: failed verdict requires severity and description", model.ErrInvalidContract, i)
			}
			continue
		}
		if verdict.Severity != "" || strings.TrimSpace(verdict.Description) != "" || strings.TrimSpace(verdict.Location) != "" {
			return fmt.Errorf("%w: architecture_verdicts[%d]: passing verdict forbids failure fields", model.ErrInvalidContract, i)
		}
	}
	return nil
}
