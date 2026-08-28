// Package service — this file is SPEC-130 §2a's write-through seam (D33):
// nine private wrappers, one per store.SDDStore mutator that must
// materialize, sitting between sdd.go's business logic and the store. Each
// wrapper performs the SAME store call sdd.go used to make directly, then
// calls materializeBacklogItem or materializeSpec so the on-disk record
// (internal/sddfile) stays in sync with the database — the single
// definition of "which mutations travel to the repository" this package
// has.
//
// In THIS commit the two materializers are stubs that return immediately:
// this is a pure refactor of the 18 call sites named in spec.md V6, with no
// behavioural change yet (nothing writes to disk). The real
// materialization lands in the next commit, isolated on purpose — see
// plan.md §6 "El que más romperá algo ajeno: commit 4" for why keeping this
// commit inert makes it revertible on its own if something about moving 19
// call sites turns out wrong, without dragging the format or the CLI down
// with it.
//
// InsertLaneAudit (sdd.go) is the SDD store's 19th write call and
// deliberately has NO wrapper here: lane_audits does not travel to the
// repository until BL-197 (etapa 4) — see sdd_export_guard_test.go's
// inventory for the exemption, declared with its reason on the line beside
// it (C1).
package service

import (
	"context"

	"github.com/wirvii/mneme/internal/model"
)

// --- backlog wrappers ---

// createBacklogItem wraps store.CreateBacklogItem and materializes the new
// item's record.
func (svc *SDDService) createBacklogItem(ctx context.Context, item *model.BacklogItem) error {
	if err := svc.store.CreateBacklogItem(ctx, item); err != nil {
		return err
	}
	svc.materializeBacklogItem(ctx, item.ID)
	return nil
}

// updateBacklogItem wraps store.UpdateBacklogItem and materializes the
// updated item's record.
func (svc *SDDService) updateBacklogItem(ctx context.Context, item *model.BacklogItem) error {
	if err := svc.store.UpdateBacklogItem(ctx, item); err != nil {
		return err
	}
	svc.materializeBacklogItem(ctx, item.ID)
	return nil
}

// appendBacklogRefinement wraps store.AppendBacklogRefinement and
// materializes the item's record (the refinement is part of the same
// aggregate, D14).
func (svc *SDDService) appendBacklogRefinement(
	ctx context.Context, itemID string, expected, next model.BacklogStatus, body, by string,
) (*model.BacklogRefinement, error) {
	r, err := svc.store.AppendBacklogRefinement(ctx, itemID, expected, next, body, by)
	if err != nil {
		return nil, err
	}
	svc.materializeBacklogItem(ctx, itemID)
	return r, nil
}

// --- spec wrappers ---

// createSpec wraps store.CreateSpec and materializes the new spec's record.
func (svc *SDDService) createSpec(ctx context.Context, spec *model.Spec) error {
	if err := svc.store.CreateSpec(ctx, spec); err != nil {
		return err
	}
	svc.materializeSpec(ctx, spec.ID)
	return nil
}

// updateSpecStatus wraps store.UpdateSpecStatus and materializes the spec's
// record. This is the busiest of the nine wrappers — nine call sites in
// sdd.go route through it (SpecAdvance, SpecPushback, SpecReject,
// SpecResolve, SpecQuick ×2, LaneAudit, LaneReclassify, LaneOverride).
func (svc *SDDService) updateSpecStatus(ctx context.Context, specID string, from, to model.SpecStatus, by, reason string) error {
	if err := svc.store.UpdateSpecStatus(ctx, specID, from, to, by, reason); err != nil {
		return err
	}
	svc.materializeSpec(ctx, specID)
	return nil
}

// updateSpecBaseSHA wraps store.UpdateSpecBaseSHA and materializes the
// spec's record.
func (svc *SDDService) updateSpecBaseSHA(ctx context.Context, specID, sha string) error {
	if err := svc.store.UpdateSpecBaseSHA(ctx, specID, sha); err != nil {
		return err
	}
	svc.materializeSpec(ctx, specID)
	return nil
}

// updateSpecLaneScope wraps store.UpdateSpecLaneScope and materializes the
// spec's record.
func (svc *SDDService) updateSpecLaneScope(ctx context.Context, specID string, lane model.Lane, scope string) error {
	if err := svc.store.UpdateSpecLaneScope(ctx, specID, lane, scope); err != nil {
		return err
	}
	svc.materializeSpec(ctx, specID)
	return nil
}

// --- pushback wrappers ---

// createPushback wraps store.CreatePushback and materializes the owning
// spec's record (pushbacks are part of the spec aggregate, D14).
func (svc *SDDService) createPushback(ctx context.Context, pb *model.SpecPushback) error {
	if err := svc.store.CreatePushback(ctx, pb); err != nil {
		return err
	}
	svc.materializeSpec(ctx, pb.SpecID)
	return nil
}

// resolvePushback wraps store.ResolvePushback and materializes the owning
// spec's record. specID is NOT part of store.ResolvePushback's own
// signature (it only takes the pushback's own id) — the caller (SpecResolve)
// already has the spec loaded, so it is passed through here rather than
// looked up again.
func (svc *SDDService) resolvePushback(ctx context.Context, pushbackID, resolution, specID string) error {
	if err := svc.store.ResolvePushback(ctx, pushbackID, resolution); err != nil {
		return err
	}
	svc.materializeSpec(ctx, specID)
	return nil
}

// --- materializers (stubs in this commit — see package godoc above) ---

// materializeBacklogItem will, from the next commit onward, reread itemID's
// complete aggregate (item + refinements) from the store and write its
// on-disk record (internal/sddfile). In THIS commit it is an inert stub: no
// behaviour, no disk access, so this commit's 19-site refactor can be
// reviewed and reverted on its own (plan.md §6).
func (svc *SDDService) materializeBacklogItem(_ context.Context, _ string) {
}

// materializeSpec is materializeBacklogItem's sibling for specs (spec +
// history + pushbacks). Same inert-stub posture in this commit.
func (svc *SDDService) materializeSpec(_ context.Context, _ string) {
}
