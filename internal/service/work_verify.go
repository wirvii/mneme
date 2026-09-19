package service

import (
	"context"
	"fmt"
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
	if strings.TrimSpace(svc.repoDir) == "" {
		return model.WorkCapabilityResult{}, fmt.Errorf("%w: repo_dir: required", model.ErrInvalidContract)
	}
	if svc.deliveryRunnerFactory == nil {
		return model.WorkCapabilityResult{}, fmt.Errorf("%w: delivery runner factory: required", model.ErrInvalidContract)
	}
	if strings.TrimSpace(svc.mnemeVersion) == "" {
		return model.WorkCapabilityResult{}, fmt.Errorf("%w: mneme version: required", model.ErrInvalidContract)
	}
	aggregate, err := svc.store.GetWorkAggregate(ctx, req.ID)
	if err != nil {
		return model.WorkCapabilityResult{}, err
	}
	if aggregate.Contract.Status != model.WorkStatusVerifying && aggregate.Contract.Status != model.WorkStatusTargetedVerifying {
		return model.WorkCapabilityResult{}, model.ErrInvalidWorkTransition
	}
	started := time.Now().UTC()
	head, err := (&quality.Git{RepoDir: svc.repoDir}).HeadSHA()
	if err != nil {
		return model.WorkCapabilityResult{}, err
	}
	finished := time.Now().UTC()
	check := &model.DeliveryCheck{
		Kind: "verification", Name: "phase-3-pending",
		Status: model.DeliveryCheckFail, Effect: model.DeliveryEffectBlocks,
		Detail: "verification has not evaluated the requested contract yet",
	}
	checks := []*model.DeliveryCheck{check}
	contract := aggregate.Contract
	cert := &model.DeliveryCertificate{
		Project: contract.Project, WorkID: contract.ID,
		ContractRevision: contract.ContractRevision, ContractHash: contract.ContractHash,
		HeadSHA: head, BaseSHA: contract.BaseSHA, Verdict: model.DeriveDeliveryVerdict([]model.DeliveryCheck{*check}),
		Evidence: "1 failed delivery check", MnemeVersion: svc.mnemeVersion,
		StartedAt: started, FinishedAt: finished, DurationMs: finished.Sub(started).Milliseconds(),
	}
	if err := svc.store.InsertDeliveryEvaluation(ctx, cert, checks, nil); err != nil {
		return model.WorkCapabilityResult{}, err
	}
	work, err := svc.WorkGet(ctx, model.WorkGetRequest{ID: req.ID})
	if err != nil {
		return model.WorkCapabilityResult{}, err
	}
	return model.WorkCapabilityResult{
		Work: work, Operation: "verify", Available: true, Performed: true,
		Certificate: cert, Checks: []model.DeliveryCheck{*check},
	}, nil
}
