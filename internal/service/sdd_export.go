// Package service — this file is SPEC-130 §2a's write-through seam (D33):
// nine private wrappers, one per store.SDDStore mutator that must
// materialize, sitting between sdd.go's business logic and the store. Each
// wrapper performs the SAME store call sdd.go used to make directly, then
// calls materializeBacklogItem or materializeSpec so the on-disk record
// (internal/sddfile) stays in sync with the database — the single
// definition of "which mutations travel to the repository" this package
// has.
//
// InsertLaneAudit (sdd.go) is the SDD store's 19th write call and
// deliberately has NO wrapper here: lane_audits does not travel to the
// repository until BL-197 (etapa 4) — see sdd_export_guard_test.go's
// inventory for the exemption, declared with its reason on the line beside
// it (C1).
package service

import (
	"context"
	"log/slog"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/sddfile"
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

// --- materializers ---

// materializeBacklogItem rereads itemID's COMPLETE aggregate (item +
// refinements, D14) from the store and writes its on-disk record
// (internal/sddfile.BacklogPath), when the SDD mechanism is active for
// svc.repoDir (D29). Best-effort (D33/§9.2): any failure — mechanism off,
// reload error, marshal error (D27(b)'s round-trip refusal), write error —
// is logged via slog and NEVER propagated to the caller. The same
// criterion materializeTeamMemory already established for this package: a
// caller's BacklogAdd/BacklogRefine/... must never fail because a file on
// disk could not be written.
//
// svc.repoDir is read here rather than threaded as a parameter through
// every one of the nine wrappers (D38 is still honoured: repoDir is a
// parameter — it is just SDDService's OWN constructor parameter,
// svc.WithRepoDir, set once by the frontend at startup, the same
// precedent ensureCertified and LaneAudit already use for the identical
// field). An empty svc.repoDir means the mechanism is off — never a
// fallback to os.Getwd().
func (svc *SDDService) materializeBacklogItem(ctx context.Context, itemID string) {
	repoRoot := svc.repoDir
	if repoRoot == "" {
		return
	}
	if !ResolveSDDState(repoRoot).Enabled {
		return
	}

	item, err := svc.store.GetBacklogItem(ctx, itemID)
	if err != nil {
		slog.ErrorContext(ctx, "sdd_materialize_error", "kind", "backlog", "id", itemID, "step", "reload", "error", err)
		return
	}
	refinements, err := svc.store.ListBacklogRefinements(ctx, itemID)
	if err != nil {
		slog.ErrorContext(ctx, "sdd_materialize_error", "kind", "backlog", "id", itemID, "step", "refinements", "error", err)
		return
	}

	data, err := sddfile.MarshalBacklog(&sddfile.BacklogRecord{Item: item, Refinements: refinements})
	if err != nil {
		slog.ErrorContext(ctx, "sdd_materialize_error", "kind", "backlog", "id", itemID, "step", "marshal", "error", err)
		return
	}

	if err := sddfile.WriteRecord(sddfile.BacklogPath(repoRoot, itemID), data); err != nil {
		slog.ErrorContext(ctx, "sdd_materialize_error", "kind", "backlog", "id", itemID, "step", "write", "error", err)
	}
}

// materializeSpec is materializeBacklogItem's sibling for specs: rereads
// specID's complete aggregate (spec + history + pushbacks, D14) and writes
// sddfile.SpecRecordPath. Same active-mechanism check, same best-effort
// posture.
func (svc *SDDService) materializeSpec(ctx context.Context, specID string) {
	repoRoot := svc.repoDir
	if repoRoot == "" {
		return
	}
	if !ResolveSDDState(repoRoot).Enabled {
		return
	}

	spec, err := svc.store.GetSpec(ctx, specID)
	if err != nil {
		slog.ErrorContext(ctx, "sdd_materialize_error", "kind", "spec", "id", specID, "step", "reload", "error", err)
		return
	}
	history, err := svc.store.GetSpecHistory(ctx, specID)
	if err != nil {
		slog.ErrorContext(ctx, "sdd_materialize_error", "kind", "spec", "id", specID, "step", "history", "error", err)
		return
	}
	pushbacks, err := svc.store.GetAllPushbacks(ctx, specID)
	if err != nil {
		slog.ErrorContext(ctx, "sdd_materialize_error", "kind", "spec", "id", specID, "step", "pushbacks", "error", err)
		return
	}

	data, err := sddfile.MarshalSpec(&sddfile.SpecRecord{Spec: spec, History: history, Pushbacks: pushbacks})
	if err != nil {
		slog.ErrorContext(ctx, "sdd_materialize_error", "kind", "spec", "id", specID, "step", "marshal", "error", err)
		return
	}

	if err := sddfile.WriteRecord(sddfile.SpecRecordPath(repoRoot, specID), data); err != nil {
		slog.ErrorContext(ctx, "sdd_materialize_error", "kind", "spec", "id", specID, "step", "write", "error", err)
	}
}
