package service

import (
	"context"
	"fmt"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/store"
)

// backfillBatchSize mirrors rebuildStore's own batch size
// (internal/service/rebuild.go) — the precedent this whole shape follows.
const backfillBatchSize = 500

// BackfillSDDRefs is SPEC-128 D7 mitad B: the one-shot pass that anchors
// mentions in memories written BEFORE this mechanism existed. Mitad A (the
// self-healing internal/db.ensureSDDUUIDs) already guarantees every
// backlog_items/specs row carries an anchor of its own; this half is what
// actually reaches the memories that cite them.
//
// Runs against svc.projectStore ONLY (D12: global.db's SDD tables exist but
// are always empty — nothing to anchor against there). A no-op, cheaply,
// when there's no SDD store wired or the marker already reports complete —
// the guard that keeps this safe to call from every single initService
// invocation, mirroring ensureSDDUUIDs' own posture at the db layer.
//
// Uses model.ParseSDDRefs via computeSDDRefs — the SAME function the live
// write path calls (bakeSDDRefs/bakeSDDRefsForUpdate) — a correctness
// property, not a convenience (D7): a second, independently-written notion
// of "what counts as a mention" would eventually disagree with the live
// path on the same text. Reconstructing the set with FTS5 instead was
// considered and rejected for exactly this reason (D7).
//
// A memory already carrying at least one SDD ref (m.SDDRefs, loaded by
// every store read path since the step-4 wiring — including List, used
// here) is skipped entirely: either the live write path already anchored
// it, or an earlier backfill pass did. This is what makes a second call
// create zero rows (AC8) without needing a per-memory existence query.
func (svc *MemoryService) BackfillSDDRefs(ctx context.Context) (scanned, created int, err error) {
	if svc.sddStore == nil {
		return 0, 0, nil
	}

	complete, err := svc.sddStore.IsSDDReferenceBackfillComplete(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("service: backfill sdd refs: check marker: %w", err)
	}
	if complete {
		return 0, 0, nil
	}

	memories, err := svc.projectStore.List(ctx, store.ListOptions{
		Limit:             100_000, // practical cap, matches rebuildStore's own.
		IncludeSuperseded: true,    // a superseded memory's text still cites real work.
	})
	if err != nil {
		return 0, 0, fmt.Errorf("service: backfill sdd refs: list memories: %w", err)
	}

	total := len(memories)
	for batchStart := 0; batchStart < total; batchStart += backfillBatchSize {
		end := batchStart + backfillBatchSize
		if end > total {
			end = total
		}

		batchCreated, batchErr := svc.backfillSDDRefsBatch(ctx, memories[batchStart:end])
		if batchErr != nil {
			return scanned, created, fmt.Errorf("service: backfill sdd refs: batch [%d:%d]: %w", batchStart, end, batchErr)
		}
		scanned += end - batchStart
		created += batchCreated
	}

	if err := svc.sddStore.MarkSDDReferenceBackfillComplete(ctx, scanned, created); err != nil {
		return scanned, created, fmt.Errorf("service: backfill sdd refs: mark complete: %w", err)
	}

	return scanned, created, nil
}

// backfillSDDRefsBatch processes one batch of memories, returning the
// number of memory_sdd_refs rows created.
func (svc *MemoryService) backfillSDDRefsBatch(ctx context.Context, batch []*model.Memory) (int, error) {
	created := 0
	for _, m := range batch {
		if len(m.SDDRefs) > 0 || !mayAnchor(m) {
			continue
		}

		refs := svc.computeSDDRefs(ctx, m.Title, m.Content)
		if len(refs) == 0 {
			continue
		}

		if err := svc.projectStore.SetSDDRefs(ctx, m.ID, refs); err != nil {
			return created, fmt.Errorf("set sdd refs for %s: %w", m.ID, err)
		}
		created += len(refs)
	}
	return created, nil
}
