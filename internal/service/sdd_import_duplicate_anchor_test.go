// Package service — a targeted addition (QA rejection, round 4, revised in
// round 6): D50's own anchorIndex is a SNAPSHOT taken once, before ANY
// write in the batch (deliberately — see D64's own reasoning,
// sdd_import.go's top-level comment), so it structurally cannot see two
// files of the SAME batch that happen to carry the IDENTICAL anchor.
//
// Round 4 recognized the resulting raw database error reactively (a
// predicate matching the sqlite driver's own UNIQUE-constraint text) and
// gave it its own reason, "ancla-duplicada-en-la-misma-tanda", instead of
// the generic "roto" — but that detection only ever ran at the moment of
// the REAL write (apply=true), so a PREVIEW (`mneme sdd import --dry-run`,
// or `mneme sdd status`, which always runs apply=false) reported BOTH
// colliding files as "would be created", when only one ever could be —
// exactly the kind of preview-lies-about-what-will-happen defect this
// whole mechanism exists to prevent (D17). It also left the
// `Conflicted` case this reason was given in SDDStatus's own switch
// (sdd_enable.go) as dead code: SDDStatus always runs in preview mode, so
// the reactive, write-time-only detection could never fire through it.
//
// Round 6 replaces the reactive detection with a PROACTIVE one:
// batchBacklogAnchor/batchSpecAnchor (built once in ImportSDDFromRepo,
// from the batch's own incoming files, compared against EACH OTHER
// rather than against the database) decide the SAME outcome BEFORE any
// write — apply=false and apply=true now compute the identical decision,
// so the preview never lies about this collision, and the reason is
// reachable through SDDStatus too. The old reactive predicate
// (isDuplicateAnchorInBatch, matching driver error text) is retired
// entirely rather than kept as untestable dead code alongside the new
// check — every constructible scenario now reaches the proactive check
// first, so the reactive one could never be exercised by anything short
// of genuine cross-process concurrency this repository has no
// infrastructure to simulate deterministically.
package service

import (
	"context"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/store"
)

// newSDDServiceAt is newSDDMaterializeService's own sibling for tests that
// need TWO separate service instances (separate databases, so one call's
// own writes cannot affect the other's) pointed at the SAME repository
// directory — needed to compare a dry-run preview against a real apply
// over the identical set of files.
func newSDDServiceAt(t *testing.T, project, repoDir string) *SDDService {
	t.Helper()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })

	sddStore := store.NewSDDStore(database)
	cfg := config.Default()
	svc := NewSDDService(sddStore, cfg, project, nil)
	svc.WithRepoDir(repoDir)
	return svc
}

// TestSDDImport_TwoSpecFilesShareAnchorInSameBatch closes
// importSpecRecord's own remaining gap (the batch-anchor check inside
// CASE A) via a real anchor collision — two spec files, both new to the
// local base, carrying the identical anchor. Applies for real
// (apply=true); TestSDDImport_DryRunAgreesWithApply_SpecAnchorCollision
// is this same scenario's own dry-run/apply agreement check.
func TestSDDImport_TwoSpecFilesShareAnchorInSameBatch(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, importTestProject)
	enableSDD(t, repoDir, importTestProject)
	ctx := context.Background()

	sharedUUID := "0198f000-0000-7000-8000-000000000900"
	writeSpecFixture(t, repoDir, &model.Spec{
		ID: "SPEC-900", UUID: sharedUUID, Title: "first claimant", Status: model.SpecStatusDraft,
		Project: importTestProject, Lane: model.LaneStandard,
	}, nil, nil)
	writeSpecFixture(t, repoDir, &model.Spec{
		ID: "SPEC-901", UUID: sharedUUID, Title: "second claimant", Status: model.SpecStatusDraft,
		Project: importTestProject, Lane: model.LaneStandard,
	}, nil, nil)

	result, err := svc.ImportSDDFromRepo(ctx, repoDir, true)
	if err != nil {
		t.Fatalf("ImportSDDFromRepo must never abort the batch over this collision (D22): %v", err)
	}

	byID := map[string]string{}
	for _, s := range result.Skipped {
		byID[s.ID] = s.Reason
	}

	// Exactly one of the two claims the anchor (deterministically the
	// first in this batch's own walk order, SPEC-900 — ListRecords walks
	// lexically); the other is skipped with the new, specific diagnosis
	// — never the generic "roto".
	createdCount, skippedCount := 0, 0
	for _, id := range []string{"SPEC-900", "SPEC-901"} {
		if _, gErr := svc.store.GetSpec(ctx, id); gErr == nil {
			createdCount++
		}
		if reason, ok := byID[id]; ok {
			skippedCount++
			if reason != "ancla-duplicada-en-la-misma-tanda" {
				t.Errorf("%s reason = %q, want ancla-duplicada-en-la-misma-tanda", id, reason)
			}
		}
	}
	if createdCount != 1 {
		t.Errorf("createdCount = %d, want exactly 1 (one claimant must win)", createdCount)
	}
	if skippedCount != 1 {
		t.Errorf("skippedCount = %d, want exactly 1 (the other claimant must be reported, not silently dropped)", skippedCount)
	}
}

// TestSDDImport_TwoBacklogFilesShareAnchorInSameBatch is
// TestSDDImport_TwoSpecFilesShareAnchorInSameBatch's backlog-side sibling
// — the same fix applies symmetrically (idx_backlog_items_uuid is the
// same kind of unique index as idx_specs_uuid), so both sides need their
// own coverage rather than assuming symmetry holds untested.
func TestSDDImport_TwoBacklogFilesShareAnchorInSameBatch(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, importTestProject)
	enableSDD(t, repoDir, importTestProject)
	ctx := context.Background()

	sharedUUID := "0198f000-0000-7000-8000-000000000901"
	writeBacklogFixture(t, repoDir, &model.BacklogItem{
		ID: "BL-900", UUID: sharedUUID, Title: "first claimant", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: importTestProject, Lane: model.LaneStandard,
	}, nil)
	writeBacklogFixture(t, repoDir, &model.BacklogItem{
		ID: "BL-901", UUID: sharedUUID, Title: "second claimant", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: importTestProject, Lane: model.LaneStandard,
	}, nil)

	result, err := svc.ImportSDDFromRepo(ctx, repoDir, true)
	if err != nil {
		t.Fatalf("ImportSDDFromRepo must never abort the batch over this collision (D22): %v", err)
	}

	byID := map[string]string{}
	for _, s := range result.Skipped {
		byID[s.ID] = s.Reason
	}

	createdCount, skippedCount := 0, 0
	for _, id := range []string{"BL-900", "BL-901"} {
		if _, gErr := svc.store.GetBacklogItem(ctx, id); gErr == nil {
			createdCount++
		}
		if reason, ok := byID[id]; ok {
			skippedCount++
			if reason != "ancla-duplicada-en-la-misma-tanda" {
				t.Errorf("%s reason = %q, want ancla-duplicada-en-la-misma-tanda", id, reason)
			}
		}
	}
	if createdCount != 1 {
		t.Errorf("createdCount = %d, want exactly 1 (one claimant must win)", createdCount)
	}
	if skippedCount != 1 {
		t.Errorf("skippedCount = %d, want exactly 1 (the other claimant must be reported, not silently dropped)", skippedCount)
	}
}

// TestSDDImport_DryRunAgreesWithApply_SpecAnchorCollision is round 6's own
// core requirement: a dry-run preview over the exact same batch that
// TestSDDImport_TwoSpecFilesShareAnchorInSameBatch applies for real must
// report the IDENTICAL decision — SPEC-900 "would be created", SPEC-901
// skipped with the SAME specific reason — never "both would be created",
// which is what this exact scenario reported before round 6's fix (D17:
// a preview must never say something different from what will happen).
// Two SEPARATE service instances over the SAME repository directory (one
// per call), since ImportSDDFromRepo's own apply=true call would mutate
// the database an apply=false call needs to observe unchanged.
func TestSDDImport_DryRunAgreesWithApply_SpecAnchorCollision(t *testing.T) {
	repoDir := newSDDGitRepo(t)
	sharedUUID := "0198f000-0000-7000-8000-000000000910"
	writeSpecFixture(t, repoDir, &model.Spec{
		ID: "SPEC-910", UUID: sharedUUID, Title: "first claimant", Status: model.SpecStatusDraft,
		Project: importTestProject, Lane: model.LaneStandard,
	}, nil, nil)
	writeSpecFixture(t, repoDir, &model.Spec{
		ID: "SPEC-911", UUID: sharedUUID, Title: "second claimant", Status: model.SpecStatusDraft,
		Project: importTestProject, Lane: model.LaneStandard,
	}, nil, nil)

	enableSDD(t, repoDir, importTestProject)
	ctx := context.Background()

	previewSvc := newSDDServiceAt(t, importTestProject, repoDir)
	previewResult, err := previewSvc.ImportSDDFromRepo(ctx, repoDir, false)
	if err != nil {
		t.Fatalf("ImportSDDFromRepo (dry-run): %v", err)
	}

	applySvc := newSDDServiceAt(t, importTestProject, repoDir)
	applyResult, err := applySvc.ImportSDDFromRepo(ctx, repoDir, true)
	if err != nil {
		t.Fatalf("ImportSDDFromRepo (apply): %v", err)
	}

	previewSkipped := map[string]string{}
	for _, s := range previewResult.Skipped {
		previewSkipped[s.ID] = s.Reason
	}
	applySkipped := map[string]string{}
	for _, s := range applyResult.Skipped {
		applySkipped[s.ID] = s.Reason
	}

	// The preview must NOT claim both would be created — exactly the
	// defect round-6 QA found: before the fix, previewResult.Created
	// contained BOTH SPEC-910 and SPEC-911.
	if len(previewResult.Created) != 1 {
		t.Fatalf("dry-run Created = %v, want exactly 1 entry (only one claimant can ever be created)", previewResult.Created)
	}
	if len(applyResult.Created) != 1 {
		t.Fatalf("apply Created = %v, want exactly 1 entry", applyResult.Created)
	}

	// The dry-run's own "would create" ID and the apply's own actually-
	// created ID must be the SAME one — the preview does not just avoid
	// claiming two winners, it names the SAME winner the real run picks.
	previewWinner := previewResult.Created[0][:8] // "SPEC-910" / "SPEC-911"
	applyWinner := applyResult.Created[0][:8]
	if previewWinner != applyWinner {
		t.Errorf("dry-run said %q would be created, apply actually created %q — preview disagrees with reality",
			previewWinner, applyWinner)
	}

	if previewSkipped["SPEC-911"] != "ancla-duplicada-en-la-misma-tanda" && previewSkipped["SPEC-910"] != "ancla-duplicada-en-la-misma-tanda" {
		t.Errorf("dry-run Skipped = %v, want one entry reasoned ancla-duplicada-en-la-misma-tanda", previewResult.Skipped)
	}
	if applySkipped["SPEC-911"] != "ancla-duplicada-en-la-misma-tanda" && applySkipped["SPEC-910"] != "ancla-duplicada-en-la-misma-tanda" {
		t.Errorf("apply Skipped = %v, want one entry reasoned ancla-duplicada-en-la-misma-tanda", applyResult.Skipped)
	}
}
