// Package service — a targeted addition (QA rejection, round 4): D50's own
// anchorIndex is a SNAPSHOT taken once, before ANY write in the batch
// (deliberately — see D64's own reasoning, sdd_import.go's top-level
// comment), so it structurally cannot see two files of the SAME batch that
// happen to carry the IDENTICAL anchor. Until this round, the second file
// hit a raw, unclassified database error (a genuine UNIQUE-constraint
// violation on idx_backlog_items_uuid/idx_specs_uuid, migration 019) —
// closing importSpecRecord's own last coverage gap, but surfacing as
// "roto" with no hint of WHY. Now recognized specifically
// (isDuplicateAnchorInBatch) and reported as its own reason,
// "ancla-duplicada-en-la-misma-tanda" — the real shape D16's own accepted
// risk (two machines completing the same hand-authored file and minting
// different anchors, or a file copied by hand) takes on this side.
package service

import (
	"context"
	"testing"

	"github.com/wirvii/mneme/internal/model"
)

// TestSDDImport_TwoSpecFilesShareAnchorInSameBatch closes
// importSpecRecord's own remaining gap (CreateSpecFromRecord's failure
// branch inside CASE A) via a genuine, real UNIQUE-constraint violation —
// not fault injection, not a new interface: two spec files, both new to
// the local base, carrying the identical anchor.
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

	// Exactly one of the two claims the anchor (whichever this batch's
	// own walk order reaches first); the other is skipped with the new,
	// specific diagnosis — never the generic "roto".
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

// TestIsDuplicateAnchorInBatch is a direct unit test of the recognition
// predicate itself — the closed-vocabulary boundary QA's own examination
// checklist asks for: it must recognize the real error text for BOTH
// tables, and must NOT misclassify an unrelated error (e.g. a different
// UNIQUE violation, or nil) as this one specific shape.
func TestIsDuplicateAnchorInBatch(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"backlog uuid collision", errFromString("store: create backlog item from record: constraint failed: UNIQUE constraint failed: backlog_items.uuid (2067)"), true},
		{"spec uuid collision", errFromString("store: create spec from record: constraint failed: UNIQUE constraint failed: specs.uuid (2067)"), true},
		{"unrelated unique violation (id, not uuid)", errFromString("store: create spec from record: constraint failed: UNIQUE constraint failed: specs.id (2067)"), false},
		{"unrelated error", errFromString("store: create spec from record: some other failure"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDuplicateAnchorInBatch(tt.err); got != tt.want {
				t.Errorf("isDuplicateAnchorInBatch(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

type stringError string

func (e stringError) Error() string { return string(e) }

func errFromString(s string) error { return stringError(s) }
