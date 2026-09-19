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

// WorkReview records a review against the exact clean repository HEAD.
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
	phase := model.ReviewPhaseInitial
	switch aggregate.Contract.Status {
	case model.WorkStatusImplementing:
		if err := validateOpenBlockingResolutionSet(aggregate.Findings, req.Resolutions, false); err != nil {
			return model.WorkCapabilityResult{}, err
		}
	case model.WorkStatusCorrecting:
		phase = model.ReviewPhaseTargeted
		if err := validateOpenBlockingResolutionSet(aggregate.Findings, req.Resolutions, true); err != nil {
			return model.WorkCapabilityResult{}, err
		}
	default:
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
	var mandateCertificate *model.DeliveryCertificate
	if phase == model.ReviewPhaseTargeted {
		mandateCertificate, err = svc.store.GetLatestDeliveryCertificate(ctx, aggregate.Contract.Project, req.ID)
		if err != nil {
			return model.WorkCapabilityResult{}, err
		}
		if head == mandateCertificate.HeadSHA || mandateCertificate.BaseSHA != aggregate.Contract.BaseSHA || mandateCertificate.ContractRevision != aggregate.Contract.ContractRevision || mandateCertificate.ContractHash != aggregate.Contract.ContractHash {
			return model.WorkCapabilityResult{}, fmt.Errorf("%w: targeted review must use a new HEAD and the certificate that opened the correction", model.ErrInvalidContract)
		}
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
	blocking := existingBlocking - len(req.Resolutions)
	for _, finding := range findings {
		if finding.Category.Blocks() {
			blocking++
		}
	}
	blockingStatus := model.DeliveryCheckPass
	if blocking > 0 {
		blockingStatus = model.DeliveryCheckFail
	}
	reviewName := "initial"
	reviewDetail := "initial review supplied for the exact repository HEAD"
	if phase == model.ReviewPhaseTargeted {
		reviewName = "targeted"
	}
	if len(req.Resolutions) != 0 {
		resolutions := append([]model.WorkFindingResolutionInput(nil), req.Resolutions...)
		sort.Slice(resolutions, func(i, j int) bool { return resolutions[i].FindingSeq < resolutions[j].FindingSeq })
		raw, marshalErr := json.Marshal(struct {
			Resolutions []model.WorkFindingResolutionInput `json:"resolutions"`
		}{Resolutions: resolutions})
		if marshalErr != nil {
			return model.WorkCapabilityResult{}, marshalErr
		}
		reviewDetail = string(raw)
	}
	reviewChecks := []*model.DeliveryCheck{
		{
			Kind: "review", Name: reviewName, Status: model.DeliveryCheckPass,
			Effect: model.DeliveryEffectMeasures, Detail: reviewDetail,
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
	var decision store.InitialReviewResult
	if phase == model.ReviewPhaseInitial {
		decision, err = svc.store.InsertInitialReview(ctx, store.InitialReviewWrite{
			Certificate: evaluation.certificate, Checks: evaluation.checks,
			Observations: evaluation.observations, Findings: findings, Resolutions: req.Resolutions, By: req.By,
		})
	} else {
		decision, err = svc.store.InsertTargetedReview(ctx, store.TargetedReviewWrite{
			Certificate: evaluation.certificate, Checks: evaluation.checks,
			Observations: evaluation.observations, Findings: findings, Resolutions: req.Resolutions,
			ExpectedCertificateID: mandateCertificate.ID, By: req.By,
		})
	}
	if err != nil {
		return model.WorkCapabilityResult{}, err
	}
	svc.materializeWork(ctx, req.ID)
	work, err := svc.WorkGet(ctx, model.WorkGetRequest{ID: req.ID})
	if err != nil {
		return model.WorkCapabilityResult{}, err
	}
	result := model.WorkCapabilityResult{
		Work: work, Operation: "review", Available: true, Performed: true,
		Certificate: evaluation.certificate, Checks: deliveryCheckValues(evaluation.checks),
		ReviewPhase: phase, NextStatus: decision.Status,
	}
	if phase == model.ReviewPhaseInitial && decision.Status == model.WorkStatusCorrecting {
		result.CorrectionMandate = correctionMandate(work, evaluation.certificate, result.Checks)
	}
	return result, nil
}

func validateOpenBlockingResolutionSet(findings []model.WorkFinding, resolutions []model.WorkFindingResolutionInput, initialPhaseOnly bool) error {
	eligible := make(map[int]bool)
	for _, finding := range findings {
		phaseEligible := !initialPhaseOnly || finding.ReviewPhase == model.ReviewPhaseInitial
		if phaseEligible && finding.Status == model.FindingOpen && finding.Category.Blocks() {
			eligible[finding.Seq] = true
		}
	}
	if len(resolutions) != len(eligible) {
		return fmt.Errorf("%w: resolutions must cover every open blocking finding exactly once", model.ErrInvalidContract)
	}
	seen := make(map[int]bool, len(resolutions))
	for i, resolution := range resolutions {
		if !eligible[resolution.FindingSeq] || seen[resolution.FindingSeq] || (resolution.Status != model.FindingFixed && resolution.Status != model.FindingInvalid) || strings.TrimSpace(resolution.Evidence) == "" || (resolution.Status == model.FindingInvalid && strings.TrimSpace(resolution.Reason) == "") {
			return fmt.Errorf("%w: resolutions[%d]", model.ErrInvalidContract, i)
		}
		seen[resolution.FindingSeq] = true
	}
	return nil
}

func correctionMandate(work model.WorkGetResponse, cert *model.DeliveryCertificate, checks []model.DeliveryCheck) *model.CorrectionMandate {
	mandate := &model.CorrectionMandate{
		WorkID: work.Contract.ID, ContractRevision: work.Contract.ContractRevision,
		ContractHash: work.Contract.ContractHash, CertificateID: cert.ID,
		CertificateHeadSHA: cert.HeadSHA, CorrectionRound: work.Contract.CorrectionRounds,
	}
	for _, finding := range work.Findings {
		if finding.ReviewPhase == model.ReviewPhaseInitial && finding.Status == model.FindingOpen && finding.Category.Blocks() {
			mandate.BlockingFindings = append(mandate.BlockingFindings, finding)
		}
	}
	for _, check := range checks {
		if check.Effect == model.DeliveryEffectBlocks && check.Status != model.DeliveryCheckPass && check.Status != model.DeliveryCheckSkipped {
			mandate.BlockingChecks = append(mandate.BlockingChecks, check)
		}
	}
	return mandate
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
	for i, resolution := range req.Resolutions {
		if resolution.FindingSeq <= 0 || (resolution.Status != model.FindingFixed && resolution.Status != model.FindingInvalid) || strings.TrimSpace(resolution.Evidence) == "" || (resolution.Status == model.FindingInvalid && strings.TrimSpace(resolution.Reason) == "") {
			return fmt.Errorf("%w: resolutions[%d]", model.ErrInvalidContract, i)
		}
	}
	return nil
}
