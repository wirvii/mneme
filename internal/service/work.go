package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/quality"
)

func (svc *SDDService) requireDeliveryV2() error {
	if svc.config == nil || svc.config.Workflow.Engine != config.WorkflowEngineDeliveryV2 {
		return model.ErrWorkflowEngineDisabled
	}
	return nil
}

// WorkBegin creates a complete delivery-v2 contract.
func (svc *SDDService) WorkBegin(ctx context.Context, req model.WorkBeginRequest) (model.WorkGetResponse, error) {
	if err := svc.requireDeliveryV2(); err != nil {
		return model.WorkGetResponse{}, err
	}
	project := req.Project
	if project == "" {
		project = svc.project
	}
	workflow := req.Workflow
	if workflow == "" {
		workflow = svc.config.Workflow.Default
	}
	var sourceType model.WorkSourceType
	switch workflow {
	case config.WorkflowDefaultOrganic:
		if req.SpecID != "" {
			return model.WorkGetResponse{}, fmt.Errorf("%w: spec_id is forbidden for organic work", model.ErrInvalidContract)
		}
		sourceType = model.WorkSourceOrganic
	case config.WorkflowDefaultSDD:
		if strings.TrimSpace(req.SpecID) == "" {
			return model.WorkGetResponse{}, fmt.Errorf("%w: spec_id is required for sdd work", model.ErrInvalidContract)
		}
		sourceType = model.WorkSourceSpec
	default:
		return model.WorkGetResponse{}, fmt.Errorf("%w: workflow %q is invalid", model.ErrInvalidContract, workflow)
	}
	method := req.DevelopmentMethod
	if method == "" {
		method = model.DevelopmentMethod(svc.config.Workflow.DevelopmentMethod)
	}
	maxRounds := svc.config.Workflow.MaxCorrectionRounds
	if req.MaxCorrectionRounds != nil {
		maxRounds = *req.MaxCorrectionRounds
	}
	if err := validateWorkScope(req.Scope); err != nil {
		return model.WorkGetResponse{}, err
	}
	criteria, err := parseWorkCriteria(req.Criteria)
	if err != nil {
		return model.WorkGetResponse{}, err
	}
	if requiresAcceptance(req.Verification) && len(criteria) == 0 {
		return model.WorkGetResponse{}, fmt.Errorf("%w: acceptance verification requires criteria", model.ErrInvalidContract)
	}
	constraints := toWorkConstraints(req.Constraints)
	id, err := svc.store.NextWorkID(ctx, project)
	if err != nil {
		return model.WorkGetResponse{}, err
	}
	contract := &model.WorkContract{
		ID: id, Project: project, SourceType: sourceType, SourceID: req.SpecID,
		Status: model.WorkStatusDraft, Goal: req.Goal, Scope: append([]string(nil), req.Scope...),
		Verification:      append([]model.VerificationKind(nil), req.Verification...),
		DevelopmentMethod: method, MaxCorrectionRounds: maxRounds, CreatedBy: req.CreatedBy,
	}
	if err := svc.store.CreateWork(ctx, contract, criteria, constraints); err != nil {
		return model.WorkGetResponse{}, err
	}
	svc.materializeWork(ctx, id)
	return svc.WorkGet(ctx, model.WorkGetRequest{ID: id})
}

// WorkGet reads a public projection under either workflow engine.
func (svc *SDDService) WorkGet(ctx context.Context, req model.WorkGetRequest) (model.WorkGetResponse, error) {
	if req.ID == "" {
		return model.WorkGetResponse{}, fmt.Errorf("%w: id: required", model.ErrInvalidContract)
	}
	aggregate, err := svc.store.GetWorkAggregate(ctx, req.ID)
	if err != nil {
		return model.WorkGetResponse{}, err
	}
	return publicWork(aggregate), nil
}

// WorkLock freezes a draft against the repository HEAD and starts implementation.
func (svc *SDDService) WorkLock(ctx context.Context, req model.WorkLockRequest) (model.WorkGetResponse, error) {
	if err := svc.requireDeliveryV2(); err != nil {
		return model.WorkGetResponse{}, err
	}
	if strings.TrimSpace(req.ID) == "" {
		return model.WorkGetResponse{}, fmt.Errorf("%w: id: required", model.ErrInvalidContract)
	}
	if strings.TrimSpace(svc.repoDir) == "" {
		return model.WorkGetResponse{}, fmt.Errorf("%w: repo_dir: required", model.ErrInvalidContract)
	}
	head, err := (&quality.Git{RepoDir: svc.repoDir}).HeadSHA()
	if err != nil {
		return model.WorkGetResponse{}, err
	}
	if strings.TrimSpace(head) == "" {
		return model.WorkGetResponse{}, fmt.Errorf("%w: base_sha: required", model.ErrInvalidContract)
	}
	if err := svc.store.LockWorkAndStart(ctx, req.ID, head, req.By); err != nil {
		return model.WorkGetResponse{}, err
	}
	svc.materializeWork(ctx, req.ID)
	return svc.WorkGet(ctx, model.WorkGetRequest{ID: req.ID})
}

// WorkAmend replaces every normative contract field through one auditable operation.
func (svc *SDDService) WorkAmend(ctx context.Context, req model.WorkAmendRequest) (model.WorkGetResponse, error) {
	if err := svc.requireDeliveryV2(); err != nil {
		return model.WorkGetResponse{}, err
	}
	if strings.TrimSpace(req.ID) == "" {
		return model.WorkGetResponse{}, fmt.Errorf("%w: id: required", model.ErrInvalidContract)
	}
	if strings.TrimSpace(req.By) == "" {
		return model.WorkGetResponse{}, fmt.Errorf("%w: by: required", model.ErrInvalidContract)
	}
	if strings.TrimSpace(req.Reason) == "" {
		return model.WorkGetResponse{}, model.ErrReasonRequired
	}
	if err := validateWorkScope(req.Scope); err != nil {
		return model.WorkGetResponse{}, err
	}
	criteria, err := parseWorkCriteria(req.Criteria)
	if err != nil {
		return model.WorkGetResponse{}, err
	}
	if requiresAcceptance(req.Verification) && len(criteria) == 0 {
		return model.WorkGetResponse{}, fmt.Errorf("%w: acceptance verification requires criteria", model.ErrInvalidContract)
	}
	constraints := toWorkConstraints(req.Constraints)
	current, err := svc.store.GetWork(ctx, req.ID)
	if err != nil {
		return model.WorkGetResponse{}, err
	}
	prospective := *current
	prospective.Goal = req.Goal
	prospective.Scope = append([]string(nil), req.Scope...)
	prospective.Verification = append([]model.VerificationKind(nil), req.Verification...)
	prospective.DevelopmentMethod = req.DevelopmentMethod
	prospective.ContractRevision++
	prospective.Status = model.WorkStatusImplementing
	if err := prospective.Validate(criteria, constraints); err != nil {
		return model.WorkGetResponse{}, err
	}
	if err := svc.store.AmendWork(ctx, model.AmendWorkRequest{
		WorkID: req.ID, Goal: req.Goal, Scope: req.Scope, Verification: req.Verification,
		DevelopmentMethod: req.DevelopmentMethod, Criteria: criteria, Constraints: constraints,
		By: req.By, Reason: req.Reason,
	}); err != nil {
		return model.WorkGetResponse{}, err
	}
	svc.materializeWork(ctx, req.ID)
	return svc.WorkGet(ctx, model.WorkGetRequest{ID: req.ID})
}

// WorkComplete closes work using the latest persisted delivery evidence.
func (svc *SDDService) WorkComplete(ctx context.Context, req model.WorkCompleteRequest) (model.WorkCapabilityResult, error) {
	if err := svc.requireDeliveryV2(); err != nil {
		return model.WorkCapabilityResult{}, err
	}
	if strings.TrimSpace(req.ID) == "" || strings.TrimSpace(req.By) == "" {
		return model.WorkCapabilityResult{}, fmt.Errorf("%w: id and by are required", model.ErrInvalidContract)
	}
	if strings.TrimSpace(svc.repoDir) == "" {
		return model.WorkCapabilityResult{}, fmt.Errorf("%w: repo_dir: required", model.ErrInvalidContract)
	}
	git := &quality.Git{RepoDir: svc.repoDir}
	head, err := git.HeadSHA()
	if err != nil {
		return model.WorkCapabilityResult{}, err
	}
	dirty, _, err := git.IsDirty()
	if err != nil {
		return model.WorkCapabilityResult{}, err
	}
	if dirty {
		return model.WorkCapabilityResult{}, fmt.Errorf("%w: worktree must be clean for completion", model.ErrInvalidContract)
	}
	closed, err := svc.store.CompleteWork(ctx, req.ID, head, req.By)
	if err != nil {
		return model.WorkCapabilityResult{}, err
	}
	svc.materializeWork(ctx, req.ID)
	work, err := svc.WorkGet(ctx, model.WorkGetRequest{ID: req.ID})
	if err != nil {
		return model.WorkCapabilityResult{}, err
	}
	return model.WorkCapabilityResult{
		Work: work, Operation: "complete", Available: true, Performed: true,
		Certificate: closed.Certificate, Checks: closed.Checks, NextStatus: model.WorkStatusDone,
	}, nil
}

// WorkResume returns escalated work to implementation after an explicit human decision.
func (svc *SDDService) WorkResume(ctx context.Context, req model.WorkResumeRequest) (model.WorkGetResponse, error) {
	if err := svc.requireDeliveryV2(); err != nil {
		return model.WorkGetResponse{}, err
	}
	if strings.TrimSpace(req.ID) == "" || strings.TrimSpace(req.By) == "" {
		return model.WorkGetResponse{}, fmt.Errorf("%w: id and by are required", model.ErrInvalidContract)
	}
	if strings.TrimSpace(req.Reason) == "" {
		return model.WorkGetResponse{}, model.ErrReasonRequired
	}
	if err := svc.store.ResumeWork(ctx, req.ID, req.By, req.Reason); err != nil {
		return model.WorkGetResponse{}, err
	}
	svc.materializeWork(ctx, req.ID)
	return svc.WorkGet(ctx, model.WorkGetRequest{ID: req.ID})
}

func publicWork(aggregate *model.WorkAggregate) model.WorkGetResponse {
	contract := aggregate.Contract
	var evidence *model.RedTestEvidence
	if contract.DevEvidence != nil {
		copy := *contract.DevEvidence
		copy.Command = append([]string(nil), contract.DevEvidence.Command...)
		evidence = &copy
	}
	view := model.WorkContractView{
		ID: contract.ID, Project: contract.Project, SourceType: contract.SourceType, SourceID: contract.SourceID,
		Status: contract.Status, Goal: contract.Goal, Scope: append([]string(nil), contract.Scope...),
		Verification: append([]model.VerificationKind(nil), contract.Verification...), DevelopmentMethod: contract.DevelopmentMethod,
		BaseSHA: contract.BaseSHA, ContractRevision: contract.ContractRevision, ContractHash: contract.ContractHash,
		CorrectionRounds: contract.CorrectionRounds, MaxCorrectionRounds: contract.MaxCorrectionRounds,
		DevEvidence: evidence, CreatedBy: contract.CreatedBy, CreatedAt: contract.CreatedAt,
		UpdatedAt: contract.UpdatedAt, LockedAt: contract.LockedAt, CompletedAt: contract.CompletedAt,
	}
	criteria := make([]model.WorkCriterionView, len(aggregate.Criteria))
	for i, criterion := range aggregate.Criteria {
		criteria[i] = model.WorkCriterionView{Seq: criterion.Seq, Key: criterion.Key, Declaration: criterion.Declaration, Status: criterion.Status, Evidence: criterion.Evidence, CheckedBy: criterion.CheckedBy, CheckedAt: criterion.CheckedAt, CreatedAt: criterion.CreatedAt}
	}
	constraints := make([]model.WorkConstraintView, len(aggregate.Constraints))
	for i, constraint := range aggregate.Constraints {
		constraints[i] = model.WorkConstraintView{Seq: constraint.Seq, Key: constraint.Key, Text: constraint.Text, Source: constraint.Source, CreatedAt: constraint.CreatedAt}
	}
	return model.WorkGetResponse{
		Contract: view, Criteria: criteria, Constraints: constraints,
		Findings: append([]model.WorkFinding(nil), aggregate.Findings...),
		History:  append([]model.WorkHistoryEntry(nil), aggregate.History...),
	}
}

func validateWorkScope(scope []string) error {
	if len(scope) == 0 {
		return fmt.Errorf("%w: scope: required", model.ErrInvalidContract)
	}
	for i, raw := range scope {
		pattern := strings.TrimSpace(raw)
		if pattern == "" {
			return fmt.Errorf("%w: scope[%d]: empty", model.ErrInvalidContract, i)
		}
		if _, err := doublestar.Match(pattern, "probe"); err != nil {
			return fmt.Errorf("%w: scope[%d] %q: %v", model.ErrInvalidContract, i, raw, err)
		}
	}
	return nil
}

func toWorkConstraints(inputs []model.WorkConstraintInput) []model.WorkConstraint {
	if len(inputs) == 0 {
		return nil
	}
	constraints := make([]model.WorkConstraint, len(inputs))
	for i, input := range inputs {
		constraints[i] = model.WorkConstraint{Key: input.Key, Text: input.Text, Source: input.Source}
	}
	return constraints
}

func requiresAcceptance(kinds []model.VerificationKind) bool {
	for _, kind := range kinds {
		if kind == model.VerificationAcceptance {
			return true
		}
	}
	return false
}

func parseWorkCriteria(inputs []model.WorkCriterionInput) ([]model.WorkCriterion, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	var document strings.Builder
	document.WriteString("schema_version = 1\n")
	for _, input := range inputs {
		document.WriteString("\n")
		document.WriteString(input.Declaration)
		document.WriteString("\n")
	}
	parsed, err := quality.ParseCriteria([]byte(document.String()))
	if err != nil {
		return nil, fmt.Errorf("%w: criterion declarations: %v", model.ErrInvalidCriteria, err)
	}
	if len(parsed.Criteria) != len(inputs) {
		return nil, fmt.Errorf("%w: criterion count %d does not match input count %d", model.ErrInvalidCriteria, len(parsed.Criteria), len(inputs))
	}
	criteria := make([]model.WorkCriterion, len(inputs))
	for i, input := range inputs {
		if parsed.Criteria[i].ID != input.Key {
			return nil, fmt.Errorf("%w: criterion %d key %q does not match parsed id %q", model.ErrInvalidCriteria, i, input.Key, parsed.Criteria[i].ID)
		}
		criteria[i] = model.WorkCriterion{Key: input.Key, Declaration: input.Declaration}
	}
	return criteria, nil
}
