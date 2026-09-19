package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/quality"
)

// WorkVerify evaluates one locked delivery-v2 contract without changing its
// lifecycle state and persists the factual certificate it produces.
func (svc *SDDService) WorkVerify(ctx context.Context, req model.WorkActionRequest) (model.WorkCapabilityResult, error) {
	if err := svc.requireDeliveryV2(); err != nil {
		return model.WorkCapabilityResult{}, err
	}
	if strings.TrimSpace(req.ID) == "" {
		return model.WorkCapabilityResult{}, fmt.Errorf("%w: id: required", model.ErrInvalidContract)
	}
	aggregate, err := svc.store.GetWorkAggregate(ctx, req.ID)
	if err != nil {
		return model.WorkCapabilityResult{}, err
	}
	if aggregate.Contract.Status != model.WorkStatusVerifying && aggregate.Contract.Status != model.WorkStatusTargetedVerifying {
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
	evaluation, err := svc.evaluateDelivery(ctx, aggregate, deliveryEvaluationOptions{preserveCurrentReview: true})
	if err != nil {
		return model.WorkCapabilityResult{}, err
	}
	if err := svc.store.InsertDeliveryEvaluation(ctx, evaluation.certificate, evaluation.checks, evaluation.observations); err != nil {
		return model.WorkCapabilityResult{}, err
	}
	work, err := svc.WorkGet(ctx, model.WorkGetRequest(req))
	if err != nil {
		return model.WorkCapabilityResult{}, err
	}
	return model.WorkCapabilityResult{
		Work: work, Operation: "verify", Available: true, Performed: true,
		Certificate: evaluation.certificate, Checks: deliveryCheckValues(evaluation.checks),
	}, nil
}

type deliveryEvaluation struct {
	certificate  *model.DeliveryCertificate
	checks       []*model.DeliveryCheck
	observations []model.CriterionObservation
}

type deliveryEvaluationOptions struct {
	architectureChecks    []*model.DeliveryCheck
	reviewChecks          []*model.DeliveryCheck
	preserveCurrentReview bool
}

func (svc *SDDService) evaluateDelivery(ctx context.Context, aggregate *model.WorkAggregate, options deliveryEvaluationOptions) (deliveryEvaluation, error) {
	started := time.Now().UTC()
	g := &quality.Git{RepoDir: svc.repoDir}
	head, err := g.HeadSHA()
	if err != nil {
		return deliveryEvaluation{}, err
	}
	dirty, dirtyPaths, err := g.IsDirty()
	if err != nil {
		return deliveryEvaluation{}, err
	}
	var checks []*model.DeliveryCheck
	var observations []model.CriterionObservation
	if dirty {
		checks = []*model.DeliveryCheck{{
			Kind: "environment", Name: "clean-worktree", Status: model.DeliveryCheckFail, Effect: model.DeliveryEffectBlocks,
			Detail: fmt.Sprintf("worktree has %d uncommitted path(s): %s", len(dirtyPaths), strings.Join(dirtyPaths, ", ")),
		}}
	} else {
		constitution, constitutionErr := svc.deliveryConstitution(aggregate.Contract.Verification)
		tailBytes := 0
		if constitution != nil {
			tailBytes = constitution.Execution.OutputTailBytes
		}
		runner := svc.deliveryRunnerFactory(tailBytes)
		if runner == nil {
			return deliveryEvaluation{}, fmt.Errorf("%w: delivery runner: required", model.ErrInvalidContract)
		}
		checks, observations, err = svc.evaluateDeliveryCriteria(ctx, aggregate, g, head, runner)
		if err != nil {
			return deliveryEvaluation{}, err
		}
		checks = append(checks, runRequestedDeliveryGates(ctx, runner, svc.repoDir, aggregate.Contract.Verification, constitution, constitutionErr, deliveryChecksBlocked(checks))...)
	}
	architectureChecks := options.architectureChecks
	reviewChecks := options.reviewChecks
	if options.preserveCurrentReview && !dirty && architectureChecks == nil && reviewChecks == nil {
		architectureChecks, reviewChecks, err = svc.currentReviewRows(ctx, aggregate, head)
		if err != nil {
			return deliveryEvaluation{}, err
		}
	}
	if architectureChecks == nil {
		checks = append(checks, deliveryArchitectureChecks(aggregate.Constraints, checks)...)
	} else {
		checks = append(checks, architectureChecks...)
	}
	checks = append(checks, reviewChecks...)
	checks = append(checks, deliveryTDDCheck(aggregate.Contract))
	finished := time.Now().UTC()
	if len(checks) == 0 {
		checks = []*model.DeliveryCheck{{
			Kind: "verification", Name: "phase-3-pending",
			Status: model.DeliveryCheckFail, Effect: model.DeliveryEffectBlocks,
			Detail: "verification has not evaluated the requested contract yet",
		}}
	}
	checkValues := deliveryCheckValues(checks)
	contract := aggregate.Contract
	cert := &model.DeliveryCertificate{
		Project: contract.Project, WorkID: contract.ID,
		ContractRevision: contract.ContractRevision, ContractHash: contract.ContractHash,
		HeadSHA: head, BaseSHA: contract.BaseSHA, Verdict: model.DeriveDeliveryVerdict(checkValues), Dirty: dirty,
		Evidence: deliveryEvidence(checkValues), MnemeVersion: svc.mnemeVersion,
		StartedAt: started, FinishedAt: finished, DurationMs: finished.Sub(started).Milliseconds(),
	}
	return deliveryEvaluation{certificate: cert, checks: checks, observations: observations}, nil
}

func (svc *SDDService) currentReviewRows(ctx context.Context, aggregate *model.WorkAggregate, head string) ([]*model.DeliveryCheck, []*model.DeliveryCheck, error) {
	contract := aggregate.Contract
	cert, err := svc.store.GetLatestDeliveryCertificate(ctx, contract.Project, contract.ID)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	if cert.HeadSHA != head || cert.BaseSHA != contract.BaseSHA || cert.ContractRevision != contract.ContractRevision || cert.ContractHash != contract.ContractHash {
		return nil, nil, nil
	}
	stored, err := svc.store.ListDeliveryChecks(ctx, cert.ID)
	if err != nil {
		return nil, nil, err
	}
	markerName := "initial"
	markerDetail := "initial review supplied for the exact repository HEAD"
	if contract.Status == model.WorkStatusTargetedVerifying {
		markerName = "targeted"
	}
	marked := false
	for _, check := range stored {
		if check.Kind == "review" && check.Name == markerName && check.Status == model.DeliveryCheckPass && check.Effect == model.DeliveryEffectMeasures {
			marked = true
			markerDetail = check.Detail
			break
		}
	}
	if !marked {
		return nil, nil, nil
	}
	architecture := make([]*model.DeliveryCheck, 0)
	for _, check := range stored {
		if check.Kind != "architecture" {
			continue
		}
		copy := check
		copy.ID = 0
		copy.CertificateID = ""
		copy.Seq = 0
		copy.CreatedAt = time.Time{}
		architecture = append(architecture, &copy)
	}
	blocking, err := svc.store.CountOpenBlockingFindings(ctx, contract.ID)
	if err != nil {
		return nil, nil, err
	}
	status := model.DeliveryCheckPass
	if blocking > 0 {
		status = model.DeliveryCheckFail
	}
	review := []*model.DeliveryCheck{
		{Kind: "review", Name: markerName, Status: model.DeliveryCheckPass, Effect: model.DeliveryEffectMeasures, Detail: markerDetail},
		{Kind: "review", Name: "open-blocking-findings", Status: status, Effect: model.DeliveryEffectBlocks, Detail: fmt.Sprintf("%d open blocking finding(s)", blocking)},
	}
	return architecture, review, nil
}

func deliveryArchitectureChecks(constraints []model.WorkConstraint, checks []*model.DeliveryCheck) []*model.DeliveryCheck {
	missing := model.MissingConstraintVerdicts(constraints, deliveryCheckValues(checks))
	rows := make([]*model.DeliveryCheck, 0, len(missing))
	for _, key := range missing {
		rows = append(rows, &model.DeliveryCheck{
			Kind: "architecture", Name: key, Status: model.DeliveryCheckNotReviewed, Effect: model.DeliveryEffectBlocks,
			Detail: "the approved constraint has no architecture verdict in this certificate",
		})
	}
	return rows
}

func deliveryTDDCheck(contract *model.WorkContract) *model.DeliveryCheck {
	check := &model.DeliveryCheck{Kind: "tdd-evidence", Name: "red-test"}
	if contract.DevelopmentMethod != model.DevelopmentMethodTDD {
		check.Status = model.DeliveryCheckSkipped
		check.Effect = model.DeliveryEffectAbsent
		check.Detail = "development_method=standard; red-test evidence is not applicable"
		return check
	}
	check.Effect = model.DeliveryEffectMeasures
	if contract.DevEvidence == nil || !contract.DevEvidence.Present() {
		check.Status = model.DeliveryCheckNotReviewed
		check.Detail = "development_method=tdd but no red-test evidence is stored"
		return check
	}
	check.Status = model.DeliveryCheckPass
	check.Detail = fmt.Sprintf("red-test evidence recorded at %s with exit_code=%d", contract.DevEvidence.TakenAt.UTC().Format(time.RFC3339Nano), contract.DevEvidence.ExitCode)
	check.OutputTail = contract.DevEvidence.OutputTail
	return check
}

func (svc *SDDService) evaluateDeliveryCriteria(ctx context.Context, aggregate *model.WorkAggregate, g *quality.Git, head string, runner quality.Runner) ([]*model.DeliveryCheck, []model.CriterionObservation, error) {
	if !requiresAcceptance(aggregate.Contract.Verification) {
		if len(aggregate.Criteria) == 0 {
			return nil, nil, nil
		}
		return []*model.DeliveryCheck{{Kind: "acceptance", Name: "not-requested", Status: model.DeliveryCheckSkipped, Effect: model.DeliveryEffectAbsent, Detail: "stored criteria were not requested by the contract"}}, nil, nil
	}
	doc, err := parseStoredDeliveryCriteria(aggregate.Criteria)
	if err != nil {
		return []*model.DeliveryCheck{{Kind: "acceptance", Name: "parse", Status: model.DeliveryCheckFail, Effect: model.DeliveryEffectBlocks, Detail: err.Error()}}, nil, nil
	}
	checks := []*model.DeliveryCheck{{Kind: "acceptance", Name: "parse", Status: model.DeliveryCheckPass, Effect: model.DeliveryEffectBlocks, Detail: fmt.Sprintf("%d criteria parsed in stored order", len(doc.Criteria))}}
	headFacts, err := collectTreeFacts(g, head, doc.Criteria)
	if err != nil {
		return nil, nil, err
	}
	baseKnown := false
	var baseFacts quality.TreeFacts
	if aggregate.Contract.BaseSHA != "" {
		if mergeBase, mergeErr := g.MergeBase(aggregate.Contract.BaseSHA, head); mergeErr == nil {
			if baseFacts, err = collectTreeFacts(g, mergeBase, doc.Criteria); err != nil {
				return nil, nil, err
			}
			baseKnown = true
		}
	}

	stored := make(map[string]model.WorkCriterion, len(aggregate.Criteria))
	for _, criterion := range aggregate.Criteria {
		stored[criterion.Key] = criterion
	}
	observedAt := time.Now().UTC()
	observations := make([]model.CriterionObservation, 0, len(doc.Criteria))
	criteriaBlocked := false
	for _, criterion := range doc.Criteria {
		if criteriaBlocked {
			checks = append(checks, &model.DeliveryCheck{
				Kind: "criterion", Name: criterion.ID, Status: model.DeliveryCheckSkipped, Effect: model.DeliveryEffectStopped,
				Detail: "stopped after an earlier blocking criterion result",
			})
			continue
		}
		row := stored[criterion.ID]
		check, observation := evaluateStoredCriterion(ctx, runner, svc.repoDir, criterion, row, headFacts, baseFacts, baseKnown, observedAt)
		checks = append(checks, check)
		criteriaBlocked = check.Effect == model.DeliveryEffectBlocks && check.Status != model.DeliveryCheckPass
		if observation != nil {
			observations = append(observations, *observation)
		}
	}
	return checks, observations, nil
}

func (svc *SDDService) deliveryConstitution(verification []model.VerificationKind) (*quality.Constitution, error) {
	if !hasExternalDeliveryChecks(verification) {
		return nil, nil
	}
	raw, err := os.ReadFile(filepath.Join(svc.repoDir, constitutionRelPath))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", constitutionRelPath, err)
	}
	constitution, err := quality.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", constitutionRelPath, err)
	}
	return constitution, nil
}

func hasExternalDeliveryChecks(verification []model.VerificationKind) bool {
	for _, kind := range verification {
		if kind == model.VerificationAffectedTests || kind == model.VerificationBuild || kind == model.VerificationLint {
			return true
		}
	}
	return false
}

func deliveryChecksBlocked(checks []*model.DeliveryCheck) bool {
	for _, check := range checks {
		if check.Effect == model.DeliveryEffectBlocks && check.Status != model.DeliveryCheckPass {
			return true
		}
	}
	return false
}

func runRequestedDeliveryGates(ctx context.Context, runner quality.Runner, repoDir string, verification []model.VerificationKind, constitution *quality.Constitution, constitutionErr error, blocked bool) []*model.DeliveryCheck {
	requested := make(map[model.VerificationKind]bool, len(verification))
	for _, kind := range verification {
		requested[kind] = true
	}
	gates := map[string]quality.Gate{}
	if constitution != nil {
		for _, gate := range constitution.Gates {
			gates[gate.Name] = gate
		}
	}
	mapping := []struct {
		kind model.VerificationKind
		gate string
	}{
		{model.VerificationAffectedTests, "test"},
		{model.VerificationBuild, "build"},
		{model.VerificationLint, "lint"},
	}
	var checks []*model.DeliveryCheck
	for _, item := range mapping {
		if !requested[item.kind] {
			continue
		}
		gate, found := gates[item.gate]
		if constitutionErr != nil || !found {
			detail := fmt.Sprintf("required gate %q is absent", item.gate)
			if constitutionErr != nil {
				detail = constitutionErr.Error()
			}
			checks = append(checks, &model.DeliveryCheck{Kind: "configuration", Name: string(item.kind), Status: model.DeliveryCheckFail, Effect: model.DeliveryEffectBlocks, Detail: detail})
			blocked = true
			continue
		}
		if blocked {
			checks = append(checks, &model.DeliveryCheck{Kind: "gate", Name: string(item.kind), Status: model.DeliveryCheckSkipped, Effect: model.DeliveryEffectStopped, Detail: "stopped after an earlier blocking result"})
			continue
		}
		result := runner.Run(ctx, gate, repoDir)
		status := model.DeliveryCheckPass
		if result.Status != quality.GateStatusPass {
			status = model.DeliveryCheckFail
			blocked = true
		}
		checks = append(checks, &model.DeliveryCheck{
			Kind: "gate", Name: string(item.kind), Status: status, Effect: model.DeliveryEffectBlocks,
			Detail:     fmt.Sprintf("constitution gate %q exit_code=%d", gate.Name, result.ExitCode),
			DurationMs: result.DurationMs, OutputSHA256: result.OutputSHA256, OutputTail: result.OutputTail,
		})
	}
	return checks
}

func parseStoredDeliveryCriteria(criteria []model.WorkCriterion) (*quality.CriteriaDoc, error) {
	var document strings.Builder
	document.WriteString("schema_version = 1\n")
	for _, criterion := range criteria {
		document.WriteString("\n")
		document.WriteString(criterion.Declaration)
		document.WriteString("\n")
	}
	doc, err := quality.ParseCriteria([]byte(document.String()))
	if err != nil {
		return nil, fmt.Errorf("stored acceptance criteria do not parse: %w", err)
	}
	if len(doc.Criteria) != len(criteria) {
		return nil, fmt.Errorf("stored acceptance criterion count %d does not match parsed count %d", len(criteria), len(doc.Criteria))
	}
	for i := range criteria {
		if criteria[i].Key != doc.Criteria[i].ID {
			return nil, fmt.Errorf("stored acceptance criterion %d key %q does not match parsed id %q", i, criteria[i].Key, doc.Criteria[i].ID)
		}
	}
	return doc, nil
}

func evaluateStoredCriterion(ctx context.Context, runner quality.Runner, repoDir string, criterion quality.Criterion, stored model.WorkCriterion, headFacts, baseFacts quality.TreeFacts, baseKnown bool, observedAt time.Time) (*model.DeliveryCheck, *model.CriterionObservation) {
	signed := stored.Status == model.CriterionSigned && stored.Evidence != "" && stored.CheckedBy != "" && stored.CheckedAt != nil
	check := &model.DeliveryCheck{Kind: "criterion", Name: criterion.ID, Effect: model.DeliveryEffectBlocks}
	observation := &model.CriterionObservation{CriterionID: stored.ID, CheckedBy: "mneme", CheckedAt: observedAt}
	detail := criterionDetail{Mode: string(criterion.Mode), Text: criterion.Text}

	switch criterion.Mode {
	case quality.ModeAssert:
		outcome, why := quality.EvaluateCriterion(criterion, headFacts, baseFacts, baseKnown)
		check.Detail = assertDetailFor(criterion, outcome, why)
		observation.Evidence = why
		switch outcome {
		case quality.OutcomePass:
			check.Status = model.DeliveryCheckPass
			observation.Status = model.CriterionPass
		case quality.OutcomeFail:
			check.Status = model.DeliveryCheckFail
			observation.Status = model.CriterionFail
		case quality.OutcomeVacuous:
			check.Status = model.DeliveryCheckNotReviewed
			observation.Status = model.CriterionVacuous
		default:
			check.Status = model.DeliveryCheckNotReviewed
			observation.Status = model.CriterionFail
		}

	case quality.ModeCommand:
		result := runner.Run(ctx, quality.Gate{Name: "criterion-" + criterion.ID, Command: criterion.Command, Timeout: criterion.Timeout}, repoDir)
		check.DurationMs = result.DurationMs
		check.OutputSHA256 = result.OutputSHA256
		check.OutputTail = result.OutputTail
		detail.Command = criterion.Command
		detail.Timeout = criterion.Timeout.String()
		if result.Status != quality.GateStatusPass {
			check.Status = model.DeliveryCheckFail
			detail.Outcome = "fail"
			detail.Why = fmt.Sprintf("exit_code=%d", result.ExitCode)
			observation.Status = model.CriterionFail
			observation.Evidence = result.OutputTail
		} else if signed {
			check.Status = model.DeliveryCheckPass
			detail.Outcome = "pass"
			detail.Why = "command passed and a signed observation establishes its base review"
			observation = nil
		} else {
			check.Status = model.DeliveryCheckNotReviewed
			detail.Outcome = "vacuity-unprovable"
			detail.Why = "command passed at HEAD but has no signed base review"
			observation.Status = model.CriterionVacuous
			observation.Evidence = detail.Why
		}
		check.Detail = marshalCriterionDetail(detail)

	case quality.ModeManual:
		detail.EvidenceRequired = criterion.EvidenceRequired
		if signed {
			check.Status = model.DeliveryCheckPass
			detail.Outcome = "pass"
			detail.Why = "signed observation with evidence"
			observation = nil
		} else {
			check.Status = model.DeliveryCheckNotReviewed
			detail.Outcome = "manual-unverified"
			detail.Why = "manual criterion lacks a complete signed observation"
			observation = nil
		}
		check.Detail = marshalCriterionDetail(detail)
	}
	return check, observation
}

func marshalCriterionDetail(detail criterionDetail) string {
	raw, _ := json.Marshal(detail) //nolint:errcheck // fixed scalar/slice shape cannot fail
	return string(raw)
}

func deliveryCheckValues(checks []*model.DeliveryCheck) []model.DeliveryCheck {
	values := make([]model.DeliveryCheck, len(checks))
	for i, check := range checks {
		values[i] = *check
	}
	return values
}

func deliveryEvidence(checks []model.DeliveryCheck) string {
	counts := map[model.DeliveryCheckStatus]int{}
	for _, check := range checks {
		counts[check.Status]++
	}
	return fmt.Sprintf("%d pass, %d fail, %d not reviewed, %d skipped", counts[model.DeliveryCheckPass], counts[model.DeliveryCheckFail], counts[model.DeliveryCheckNotReviewed], counts[model.DeliveryCheckSkipped])
}
