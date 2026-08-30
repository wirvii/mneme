// Package service — tests for ImportSDDFromRepo (SPEC-131 §2b commit 4):
// identity/timestamp preservation, the file-wins-without-comparing-dates
// rule, the anchor-based CASE A/B/C decision, child merging, idempotency,
// broken-file resilience, the disabled/foreign-project fatal paths, and
// the frozen-spec rule (D64).
package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/sddfile"
)

// writeBacklogFixture marshals and writes a complete, canonical backlog
// record. Used whenever the fixture only needs to be VALID, not
// deliberately malformed.
func writeBacklogFixture(t *testing.T, repoDir string, item *model.BacklogItem, refs []*model.BacklogRefinement) {
	t.Helper()
	data, err := sddfile.MarshalBacklog(&sddfile.BacklogRecord{Item: item, Refinements: refs})
	if err != nil {
		t.Fatalf("MarshalBacklog(%s): %v", item.ID, err)
	}
	if err := sddfile.WriteRecord(sddfile.BacklogPath(repoDir, item.ID), data); err != nil {
		t.Fatalf("WriteRecord(%s): %v", item.ID, err)
	}
}

// writeSpecFixture is writeBacklogFixture's sibling for specs.
func writeSpecFixture(t *testing.T, repoDir string, spec *model.Spec, hist []*model.SpecHistory, pbs []*model.SpecPushback) {
	t.Helper()
	data, err := sddfile.MarshalSpec(&sddfile.SpecRecord{Spec: spec, History: hist, Pushbacks: pbs})
	if err != nil {
		t.Fatalf("MarshalSpec(%s): %v", spec.ID, err)
	}
	if err := sddfile.WriteRecord(sddfile.SpecRecordPath(repoDir, spec.ID), data); err != nil {
		t.Fatalf("WriteRecord(%s): %v", spec.ID, err)
	}
}

// writeRawSDDFile writes content verbatim — for fixtures that must be
// malformed on purpose (conflict markers, an out-of-range schema, a record
// with no title) and therefore cannot go through Marshal's own round-trip
// check.
func writeRawSDDFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

const importTestProject = "wirvii/mneme"

// TestSDDImport_PreservesIdentityAndTimestamps is AC5.
func TestSDDImport_PreservesIdentityAndTimestamps(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, importTestProject)
	enableSDD(t, repoDir, importTestProject)
	ctx := context.Background()

	oldTS := time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC)

	item1 := &model.BacklogItem{
		ID: "BL-001", UUID: "0198f000-0000-7000-8000-0000000000a1",
		Title: "first", Status: model.BacklogStatusRaw, Priority: model.PriorityMedium,
		Project: importTestProject, Lane: model.LaneStandard,
		CreatedAt: oldTS, UpdatedAt: oldTS,
	}
	item2 := &model.BacklogItem{
		ID: "BL-002", UUID: "0198f000-0000-7000-8000-0000000000a2",
		Title: "second", Status: model.BacklogStatusRaw, Priority: model.PriorityMedium,
		Project: importTestProject, Lane: model.LaneStandard,
		CreatedAt: oldTS, UpdatedAt: oldTS,
	}
	spec1 := &model.Spec{
		ID: "SPEC-001", UUID: "0198f000-0000-7000-8000-0000000000a3",
		Title: "spec one", Status: model.SpecStatusDraft, Project: importTestProject,
		Lane: model.LaneStandard, CreatedAt: oldTS, UpdatedAt: oldTS,
	}
	writeBacklogFixture(t, repoDir, item1, nil)
	writeBacklogFixture(t, repoDir, item2, nil)
	writeSpecFixture(t, repoDir, spec1, nil, nil)

	result, err := svc.ImportSDDFromRepo(ctx, repoDir, true)
	if err != nil {
		t.Fatalf("ImportSDDFromRepo: %v", err)
	}
	if len(result.Created) != 3 {
		t.Fatalf("Created = %v, want 3 entries", result.Created)
	}

	got1, err := svc.store.GetBacklogItem(ctx, "BL-001")
	if err != nil {
		t.Fatalf("GetBacklogItem: %v", err)
	}
	if got1.UUID != item1.UUID {
		t.Errorf("BL-001 UUID = %s, want %s", got1.UUID, item1.UUID)
	}
	if !got1.CreatedAt.Equal(oldTS) || !got1.UpdatedAt.Equal(oldTS) {
		t.Errorf("BL-001 dates not preserved: created=%s updated=%s", got1.CreatedAt, got1.UpdatedAt)
	}

	gotSpec, err := svc.store.GetSpec(ctx, "SPEC-001")
	if err != nil {
		t.Fatalf("GetSpec: %v", err)
	}
	if gotSpec.UUID != spec1.UUID {
		t.Errorf("SPEC-001 UUID = %s, want %s", gotSpec.UUID, spec1.UUID)
	}
	if !gotSpec.CreatedAt.Equal(oldTS) || !gotSpec.UpdatedAt.Equal(oldTS) {
		t.Errorf("SPEC-001 dates not preserved: created=%s updated=%s", gotSpec.CreatedAt, gotSpec.UpdatedAt)
	}
}

// TestSDDImport_FileWinsEvenWhenOlder is AC6a: the local row's updated_at
// is NEWER than the file's, and the file still wins.
func TestSDDImport_FileWinsEvenWhenOlder(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, importTestProject)
	enableSDD(t, repoDir, importTestProject)
	ctx := context.Background()

	local := &model.BacklogItem{
		ID: "BL-010", Title: "local newer title", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: importTestProject, Lane: model.LaneStandard,
	}
	if err := svc.store.CreateBacklogItem(ctx, local); err != nil {
		t.Fatalf("CreateBacklogItem: %v", err)
	}
	// local.UpdatedAt is "now" (just stamped); the file below is
	// deliberately from 2020 — much older.
	oldTS := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	fromRepo := &model.BacklogItem{
		ID: local.ID, UUID: local.UUID, Title: "from repo, older but wins",
		Status: model.BacklogStatusRaw, Priority: model.PriorityMedium,
		Project: importTestProject, Lane: model.LaneStandard,
		CreatedAt: oldTS, UpdatedAt: oldTS,
	}
	writeBacklogFixture(t, repoDir, fromRepo, nil)

	if _, err := svc.ImportSDDFromRepo(ctx, repoDir, true); err != nil {
		t.Fatalf("ImportSDDFromRepo: %v", err)
	}

	got, err := svc.store.GetBacklogItem(ctx, local.ID)
	if err != nil {
		t.Fatalf("GetBacklogItem: %v", err)
	}
	if got.Title != "from repo, older but wins" {
		t.Errorf("Title = %q, want the file's title even though it is older", got.Title)
	}
}

// TestSDDImport_SkipsCorrelativeClaimedByTwoAnchors is AC7 (C2-corrected):
// no anchor is ever printed in the skip message — only the two titles and
// a BL-202 pointer.
func TestSDDImport_SkipsCorrelativeClaimedByTwoAnchors(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, importTestProject)
	enableSDD(t, repoDir, importTestProject)
	ctx := context.Background()

	local := &model.BacklogItem{
		ID: "BL-050", Title: "lo mio", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: importTestProject, Lane: model.LaneStandard,
	}
	if err := svc.store.CreateBacklogItem(ctx, local); err != nil {
		t.Fatalf("CreateBacklogItem: %v", err)
	}
	anchorB := local.UUID

	fromRepo := &model.BacklogItem{
		ID: "BL-050", UUID: "0198f000-0000-7000-8000-0000000000aa", // anchor A, different
		Title: "lo del companero", Status: model.BacklogStatusRaw, Priority: model.PriorityMedium,
		Project: importTestProject, Lane: model.LaneStandard,
	}
	writeBacklogFixture(t, repoDir, fromRepo, nil)
	rawBefore, err := sddfile.ReadRecord(sddfile.BacklogPath(repoDir, "BL-050"))
	if err != nil {
		t.Fatalf("read fixture before import: %v", err)
	}

	result, err := svc.ImportSDDFromRepo(ctx, repoDir, true)
	if err != nil {
		t.Fatalf("ImportSDDFromRepo: %v", err)
	}

	got, err := svc.store.GetBacklogItem(ctx, "BL-050")
	if err != nil {
		t.Fatalf("GetBacklogItem: %v", err)
	}
	if got.Title != "lo mio" || got.UUID != anchorB {
		t.Errorf("local row was overwritten: title=%q uuid=%s, want unchanged", got.Title, got.UUID)
	}

	rawAfter, err := sddfile.ReadRecord(sddfile.BacklogPath(repoDir, "BL-050"))
	if err != nil {
		t.Fatalf("read fixture after import: %v", err)
	}
	if string(rawBefore) != string(rawAfter) {
		t.Errorf("file was rewritten, want byte-identical")
	}

	if len(result.Skipped) != 1 {
		t.Fatalf("Skipped = %v, want exactly 1 entry", result.Skipped)
	}
	skip := result.Skipped[0]
	if skip.ID != "BL-050" {
		t.Errorf("Skipped[0].ID = %s, want BL-050", skip.ID)
	}
	if !strings.Contains(skip.Reason, "lo mio") || !strings.Contains(skip.Reason, "lo del companero") {
		t.Errorf("Reason = %q, want both titles present", skip.Reason)
	}
	if !strings.Contains(skip.Reason, "BL-202") {
		t.Errorf("Reason = %q, want a BL-202 pointer", skip.Reason)
	}
	if strings.Contains(skip.Reason, anchorB) || strings.Contains(skip.Reason, fromRepo.UUID) {
		t.Errorf("Reason = %q, must NEVER contain either anchor (SPEC-128 D9)", skip.Reason)
	}
}

// TestSDDImport_MergesChildrenNeverDeletes is AC8.
func TestSDDImport_MergesChildrenNeverDeletes(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, importTestProject)
	enableSDD(t, repoDir, importTestProject)
	ctx := context.Background()

	item := &model.BacklogItem{
		ID: "BL-060", Title: "x", Status: model.BacklogStatusRefined,
		Priority: model.PriorityMedium, Project: importTestProject, Lane: model.LaneStandard,
	}
	if err := svc.store.CreateBacklogItem(ctx, item); err != nil {
		t.Fatalf("CreateBacklogItem: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := svc.store.AppendBacklogRefinement(ctx, item.ID,
			model.BacklogStatusRefined, model.BacklogStatusRefined, "local body", "orchestrator"); err != nil {
			t.Fatalf("AppendBacklogRefinement: %v", err)
		}
	}

	fromRepo := &model.BacklogItem{
		ID: item.ID, UUID: item.UUID, Title: "x", Status: model.BacklogStatusRefined,
		Priority: model.PriorityMedium, Project: importTestProject, Lane: model.LaneStandard,
	}
	refs := []*model.BacklogRefinement{
		{ItemID: item.ID, Seq: 1, Body: "local body", By: "orchestrator", At: time.Now().UTC()},
		{ItemID: item.ID, Seq: 2, Body: "changed from repo", By: "architect", At: time.Now().UTC()},
	}
	writeBacklogFixture(t, repoDir, fromRepo, refs)

	if _, err := svc.ImportSDDFromRepo(ctx, repoDir, true); err != nil {
		t.Fatalf("ImportSDDFromRepo: %v", err)
	}

	got, err := svc.store.ListBacklogRefinements(ctx, item.ID)
	if err != nil {
		t.Fatalf("ListBacklogRefinements: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len(refinements) = %d, want 3 (seq 3 must survive)", len(got))
	}
	if got[1].Body != "changed from repo" {
		t.Errorf("seq 2 body = %q, want updated from repo", got[1].Body)
	}
	if got[2].Body != "local body" {
		t.Errorf("seq 3 body = %q, want untouched", got[2].Body)
	}

	// Same property for a spec's pushback.
	spec := &model.Spec{
		ID: "SPEC-060", Title: "x", Status: model.SpecStatusSpeccing,
		Project: importTestProject, Lane: model.LaneStandard,
	}
	if err := svc.store.CreateSpec(ctx, spec); err != nil {
		t.Fatalf("CreateSpec: %v", err)
	}
	localPB := &model.SpecPushback{SpecID: spec.ID, FromAgent: "architect", Questions: []string{"local q"}}
	if err := svc.store.CreatePushback(ctx, localPB); err != nil {
		t.Fatalf("CreatePushback: %v", err)
	}

	fromRepoSpec := &model.Spec{
		ID: spec.ID, UUID: spec.UUID, Title: "x", Status: model.SpecStatusSpeccing,
		Project: importTestProject, Lane: model.LaneStandard,
	}
	writeSpecFixture(t, repoDir, fromRepoSpec, nil, nil) // file brings NO pushbacks at all

	if _, err := svc.ImportSDDFromRepo(ctx, repoDir, true); err != nil {
		t.Fatalf("ImportSDDFromRepo (2nd call): %v", err)
	}

	pbs, err := svc.store.GetAllPushbacks(ctx, spec.ID)
	if err != nil {
		t.Fatalf("GetAllPushbacks: %v", err)
	}
	if len(pbs) != 1 {
		t.Fatalf("len(pushbacks) = %d, want 1 (local pushback must survive an import that omits it)", len(pbs))
	}
}

// TestSDDImport_TwiceCreatesNoDuplicates is AC9.
func TestSDDImport_TwiceCreatesNoDuplicates(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, importTestProject)
	enableSDD(t, repoDir, importTestProject)
	ctx := context.Background()

	item := &model.BacklogItem{
		ID: "BL-070", UUID: "0198f000-0000-7000-8000-0000000000b1",
		Title: "x", Status: model.BacklogStatusRaw, Priority: model.PriorityMedium,
		Project: importTestProject, Lane: model.LaneStandard,
	}
	refs := []*model.BacklogRefinement{
		{ItemID: item.ID, Seq: 1, Body: "b1", By: "x", At: time.Now().UTC()},
	}
	writeBacklogFixture(t, repoDir, item, refs)

	spec := &model.Spec{
		ID: "SPEC-070", UUID: "0198f000-0000-7000-8000-0000000000b2",
		Title: "s", Status: model.SpecStatusDraft, Project: importTestProject, Lane: model.LaneStandard,
	}
	hist := []*model.SpecHistory{
		{ID: "0198f000-0000-7000-8000-0000000000b3", SpecID: spec.ID, FromStatus: "", ToStatus: model.SpecStatusDraft,
			By: "system", Reason: "created", At: time.Now().UTC()},
	}
	writeSpecFixture(t, repoDir, spec, hist, nil)

	if _, err := svc.ImportSDDFromRepo(ctx, repoDir, true); err != nil {
		t.Fatalf("ImportSDDFromRepo (1st): %v", err)
	}
	if _, err := svc.ImportSDDFromRepo(ctx, repoDir, true); err != nil {
		t.Fatalf("ImportSDDFromRepo (2nd): %v", err)
	}

	gotRefs, err := svc.store.ListBacklogRefinements(ctx, item.ID)
	if err != nil {
		t.Fatalf("ListBacklogRefinements: %v", err)
	}
	if len(gotRefs) != 1 {
		t.Errorf("len(refinements) = %d, want 1", len(gotRefs))
	}

	gotHist, err := svc.store.GetSpecHistory(ctx, spec.ID)
	if err != nil {
		t.Fatalf("GetSpecHistory: %v", err)
	}
	if len(gotHist) != 1 {
		t.Errorf("len(history) = %d, want 1", len(gotHist))
	}
}

// TestSDDImport_BrokenFileNeverAbortsTheBatch is AC10.
func TestSDDImport_BrokenFileNeverAbortsTheBatch(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, importTestProject)
	enableSDD(t, repoDir, importTestProject)
	ctx := context.Background()

	// 1. Conflict markers.
	writeRawSDDFile(t, sddfile.BacklogPath(repoDir, "BL-101"), "---\n"+
		"id: BL-101\ntitle: \"x\"\nstatus: raw\n---\n\n"+
		"<<<<<<< HEAD\nmine\n=======\ntheirs\n>>>>>>> branch\n")

	// 2. Schema out of range.
	writeRawSDDFile(t, sddfile.BacklogPath(repoDir, "BL-102"), "---\n"+
		"schema: 2\nid: BL-102\ntitle: \"x\"\nstatus: raw\n---\n\ndescription\n")

	// 3. No title.
	writeRawSDDFile(t, sddfile.BacklogPath(repoDir, "BL-103"), "---\n"+
		"id: BL-103\nstatus: raw\n---\n\ndescription\n")

	// 4. Healthy.
	healthy := &model.BacklogItem{
		ID: "BL-104", Title: "healthy", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: importTestProject, Lane: model.LaneStandard,
	}
	writeBacklogFixture(t, repoDir, healthy, nil)

	result, err := svc.ImportSDDFromRepo(ctx, repoDir, true)
	if err != nil {
		t.Fatalf("ImportSDDFromRepo must never return an error for broken files: %v", err)
	}

	if len(result.Created) != 1 || !strings.HasPrefix(result.Created[0], "BL-104") {
		t.Errorf("Created = %v, want exactly [BL-104 (...)]", result.Created)
	}
	if len(result.Skipped) != 3 {
		t.Fatalf("Skipped = %v, want 3 entries", result.Skipped)
	}
	byID := map[string]string{}
	for _, s := range result.Skipped {
		byID[s.ID] = s.Reason
	}
	if byID["BL-101"] != "roto" {
		t.Errorf("BL-101 reason = %q, want roto", byID["BL-101"])
	}
	if byID["BL-102"] != "roto" {
		t.Errorf("BL-102 reason = %q, want roto", byID["BL-102"])
	}
	if byID["BL-103"] != "sin-titulo" {
		t.Errorf("BL-103 reason = %q, want sin-titulo", byID["BL-103"])
	}

	if _, gErr := svc.store.GetBacklogItem(ctx, "BL-104"); gErr != nil {
		t.Errorf("BL-104 was not created: %v", gErr)
	}
}

// TestSDDImport_SpecSideSkipsAndClassificationGap is a targeted addition
// (QA rejection fix): ImportSDDFromRepo's own top-level classify/parse
// loop sat with several real branches never exercised by any existing
// test — AC10's own fixture only ever covers the BACKLOG-side roto/
// sin-titulo/proyecto-distinto checks, leaving their spec-side siblings
// (UnmarshalSpec failing, an empty spec title, a spec whose project
// differs from svc.project), the "!ok" branch ClassifyRecordPath's own
// KindIgnored return takes (an entregable like plan.md sitting next to a
// real record.md — exactly D63/W7's own scenario, not fabricated), and
// ReadRecord's OS-level failure (a path ClassifyRecordPath still names as
// a record, but which is actually a DIRECTORY — a real, if unusual,
// failure a bad merge or manual mistake could produce) all untested.
func TestSDDImport_SpecSideSkipsAndClassificationGap(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, importTestProject)
	enableSDD(t, repoDir, importTestProject)
	ctx := context.Background()

	// 1. Backlog record whose project differs from svc.project.
	writeBacklogFixture(t, repoDir, &model.BacklogItem{
		ID: "BL-200", Title: "wrong project", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: "some/other-project", Lane: model.LaneStandard,
	}, nil)

	// 2. Spec record with malformed content (roto — UnmarshalSpec fails).
	writeRawSDDFile(t, sddfile.SpecRecordPath(repoDir, "SPEC-201"),
		"---\nid: SPEC-201\nstatus: draft\n---\n\n<<<<<<< HEAD\nmine\n=======\ntheirs\n>>>>>>> b\n")

	// 3. Spec record with no title.
	writeRawSDDFile(t, sddfile.SpecRecordPath(repoDir, "SPEC-202"),
		"---\nid: SPEC-202\nstatus: draft\n---\n\ndescription\n")

	// 4. Spec record whose project differs from svc.project.
	writeSpecFixture(t, repoDir, &model.Spec{
		ID: "SPEC-203", Title: "wrong project spec", Status: model.SpecStatusDraft,
		Project: "some/other-project", Lane: model.LaneStandard,
	}, nil, nil)

	// 5. A plan.md sitting next to a healthy spec's own record.md — D63's
	// own "ignored, not broken" case: ClassifyRecordPath returns ok=false
	// for it, taking the "!ok { continue }" branch.
	healthySpec := &model.Spec{
		ID: "SPEC-204", Title: "healthy spec", Status: model.SpecStatusDraft,
		Project: importTestProject, Lane: model.LaneStandard,
	}
	writeSpecFixture(t, repoDir, healthySpec, nil, nil)
	writeRawSDDFile(t, filepath.Join(sddfile.SpecDir(repoDir, "SPEC-204"), "plan.md"), "not a record, an entregable\n")

	// NOTE: a "path shaped like a record but is actually a directory"
	// fixture was tried here and removed — ListRecords' own WalkDir
	// callback explicitly skips directories (`if d.IsDir() { return nil }`,
	// internal/sddfile/io.go) before a path is ever handed to
	// ClassifyRecordPath/ReadRecord, so that scenario can never reach
	// ImportSDDFromRepo's ReadRecord-error branch at all through the
	// normal walk — confirmed by running exactly this fixture and
	// observing zero effect, not assumed. See changes.md's coverage
	// disclosure for this finding.

	result, err := svc.ImportSDDFromRepo(ctx, repoDir, true)
	if err != nil {
		t.Fatalf("ImportSDDFromRepo must never return an error for these skips: %v", err)
	}

	byID := map[string]string{}
	for _, s := range result.Skipped {
		byID[s.ID] = s.Reason
	}
	if byID["BL-200"] != "proyecto-distinto" {
		t.Errorf("BL-200 reason = %q, want proyecto-distinto", byID["BL-200"])
	}
	if byID["SPEC-201"] != "roto" {
		t.Errorf("SPEC-201 reason = %q, want roto", byID["SPEC-201"])
	}
	if byID["SPEC-202"] != "sin-titulo" {
		t.Errorf("SPEC-202 reason = %q, want sin-titulo", byID["SPEC-202"])
	}
	if byID["SPEC-203"] != "proyecto-distinto" {
		t.Errorf("SPEC-203 reason = %q, want proyecto-distinto", byID["SPEC-203"])
	}

	// SPEC-204 imported cleanly — the plan.md sitting beside it never
	// touched anything.
	if _, gErr := svc.store.GetSpec(ctx, "SPEC-204"); gErr != nil {
		t.Errorf("SPEC-204 was not created: %v", gErr)
	}
	for _, id := range []string{"SPEC-204"} {
		if _, skipped := byID[id]; skipped {
			t.Errorf("%s must not appear in Skipped", id)
		}
	}
}

// TestSDDImport_AnchorRenumberedOnAnotherMachine is a targeted addition
// (QA rejection fix): D50's "ancla-renumerada-en-otra-maquina" skip —
// CASE A's own guard against a file whose UUID is already claimed by a
// DIFFERENT correlative in the local database (a real, previously
// undescribed shape: a machine that renumbered its own copy of the same
// item) — was never exercised by any test, for either record kind.
func TestSDDImport_AnchorRenumberedOnAnotherMachine(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, importTestProject)
	enableSDD(t, repoDir, importTestProject)
	ctx := context.Background()

	localItem := &model.BacklogItem{
		ID: "BL-300", Title: "local copy", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: importTestProject, Lane: model.LaneStandard,
	}
	if err := svc.store.CreateBacklogItem(ctx, localItem); err != nil {
		t.Fatalf("CreateBacklogItem: %v", err)
	}
	renamed := &model.BacklogItem{
		ID: "BL-301", UUID: localItem.UUID, Title: "same item, renumbered elsewhere",
		Status: model.BacklogStatusRaw, Priority: model.PriorityMedium,
		Project: importTestProject, Lane: model.LaneStandard,
	}
	writeBacklogFixture(t, repoDir, renamed, nil)

	localSpec := &model.Spec{
		ID: "SPEC-300", Title: "local spec copy", Status: model.SpecStatusDraft,
		Project: importTestProject, Lane: model.LaneStandard,
	}
	if err := svc.store.CreateSpec(ctx, localSpec); err != nil {
		t.Fatalf("CreateSpec: %v", err)
	}
	renamedSpec := &model.Spec{
		ID: "SPEC-301", UUID: localSpec.UUID, Title: "same spec, renumbered elsewhere",
		Status: model.SpecStatusDraft, Project: importTestProject, Lane: model.LaneStandard,
	}
	writeSpecFixture(t, repoDir, renamedSpec, nil, nil)

	result, err := svc.ImportSDDFromRepo(ctx, repoDir, true)
	if err != nil {
		t.Fatalf("ImportSDDFromRepo: %v", err)
	}

	byID := map[string]string{}
	for _, s := range result.Skipped {
		byID[s.ID] = s.Reason
	}
	if byID["BL-301"] != "ancla-renumerada-en-otra-maquina" {
		t.Errorf("BL-301 reason = %q, want ancla-renumerada-en-otra-maquina", byID["BL-301"])
	}
	if byID["SPEC-301"] != "ancla-renumerada-en-otra-maquina" {
		t.Errorf("SPEC-301 reason = %q, want ancla-renumerada-en-otra-maquina", byID["SPEC-301"])
	}
	// BL-300/SPEC-300, the row that actually owns the anchor, is
	// untouched — no BL-301/SPEC-301 row was ever created.
	if _, gErr := svc.store.GetBacklogItem(ctx, "BL-301"); gErr == nil {
		t.Error("BL-301 must not have been created")
	}
	if _, gErr := svc.store.GetSpec(ctx, "SPEC-301"); gErr == nil {
		t.Error("SPEC-301 must not have been created")
	}
}

// TestSDDImport_SpecSkipsCorrelativeClaimedByTwoAnchors is
// TestSDDImport_SkipsCorrelativeClaimedByTwoAnchors' spec-side sibling
// (a targeted addition, QA rejection fix): D50's CASE C — a correlative
// already claimed locally by one anchor, disputed by a file carrying a
// DIFFERENT one — had a backlog-side test but no spec-side equivalent.
func TestSDDImport_SpecSkipsCorrelativeClaimedByTwoAnchors(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, importTestProject)
	enableSDD(t, repoDir, importTestProject)
	ctx := context.Background()

	local := &model.Spec{
		ID: "SPEC-050", Title: "lo mio", Status: model.SpecStatusDraft,
		Project: importTestProject, Lane: model.LaneStandard,
	}
	if err := svc.store.CreateSpec(ctx, local); err != nil {
		t.Fatalf("CreateSpec: %v", err)
	}
	anchorB := local.UUID

	fromRepo := &model.Spec{
		ID: "SPEC-050", UUID: "0198f000-0000-7000-8000-0000000000bb",
		Title: "lo del companero", Status: model.SpecStatusDraft,
		Project: importTestProject, Lane: model.LaneStandard,
	}
	writeSpecFixture(t, repoDir, fromRepo, nil, nil)

	result, err := svc.ImportSDDFromRepo(ctx, repoDir, true)
	if err != nil {
		t.Fatalf("ImportSDDFromRepo: %v", err)
	}

	got, err := svc.store.GetSpec(ctx, "SPEC-050")
	if err != nil {
		t.Fatalf("GetSpec: %v", err)
	}
	if got.Title != "lo mio" || got.UUID != anchorB {
		t.Errorf("local row was overwritten: title=%q uuid=%s, want unchanged", got.Title, got.UUID)
	}

	byID := map[string]string{}
	for _, s := range result.Skipped {
		byID[s.ID] = s.Reason
	}
	reason, ok := byID["SPEC-050"]
	if !ok {
		t.Fatalf("SPEC-050 does not appear in Skipped: %v", result.Skipped)
	}
	if !strings.Contains(reason, "correlativo-reclamado-por-dos-elementos") || !strings.Contains(reason, "BL-202") {
		t.Errorf("reason = %q, want the collision message naming BL-202", reason)
	}
	if strings.Contains(reason, anchorB) || strings.Contains(reason, fromRepo.UUID) {
		t.Errorf("reason must never contain either anchor (SPEC-128 D9): %q", reason)
	}
}

// TestSDDImport_DryRunReportsWithoutWriting is a targeted addition (QA
// rejection fix): ImportSDDFromRepo(apply=false)'s own preview branches
// for BOTH record kinds and BOTH CASE A/B — used internally by
// SDDStatus's Conflicted/FrozenBlocked derivation, but never asserted on
// directly, and the backlog CASE B (`!apply` inside an update) and BOTH
// spec-side `!apply` branches were never exercised by anything at all.
func TestSDDImport_DryRunReportsWithoutWriting(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, importTestProject)
	enableSDD(t, repoDir, importTestProject)
	ctx := context.Background()

	// An existing backlog item and spec, both about to be UPDATED by the
	// dry run (CASE B `!apply`).
	existingItem := &model.BacklogItem{
		ID: "BL-400", Title: "before", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: importTestProject, Lane: model.LaneStandard,
	}
	if err := svc.store.CreateBacklogItem(ctx, existingItem); err != nil {
		t.Fatalf("CreateBacklogItem: %v", err)
	}
	updatedItem := &model.BacklogItem{
		ID: "BL-400", UUID: existingItem.UUID, Title: "after",
		Status: model.BacklogStatusRefined, Priority: model.PriorityMedium,
		Project: importTestProject, Lane: model.LaneStandard,
	}
	writeBacklogFixture(t, repoDir, updatedItem, nil)

	existingSpec := &model.Spec{
		ID: "SPEC-400", Title: "before", Status: model.SpecStatusDraft,
		Project: importTestProject, Lane: model.LaneStandard,
	}
	if err := svc.store.CreateSpec(ctx, existingSpec); err != nil {
		t.Fatalf("CreateSpec: %v", err)
	}
	updatedSpec := &model.Spec{
		ID: "SPEC-400", UUID: existingSpec.UUID, Title: "after",
		Status: model.SpecStatusSpeccing, Project: importTestProject, Lane: model.LaneStandard,
	}
	writeSpecFixture(t, repoDir, updatedSpec, nil, nil)

	// A brand-new backlog item and spec, about to be CREATED (CASE A
	// `!apply`).
	newItem := &model.BacklogItem{
		ID: "BL-401", Title: "brand new", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: importTestProject, Lane: model.LaneStandard,
	}
	writeBacklogFixture(t, repoDir, newItem, nil)
	newSpec := &model.Spec{
		ID: "SPEC-401", Title: "brand new spec", Status: model.SpecStatusDraft,
		Project: importTestProject, Lane: model.LaneStandard,
	}
	writeSpecFixture(t, repoDir, newSpec, nil, nil)

	result, err := svc.ImportSDDFromRepo(ctx, repoDir, false)
	if err != nil {
		t.Fatalf("ImportSDDFromRepo (dry-run): %v", err)
	}

	// Nothing was actually written: the local rows still say "before",
	// and BL-401/SPEC-401 do not exist in the database.
	gotItem, gErr := svc.store.GetBacklogItem(ctx, "BL-400")
	if gErr != nil || gotItem.Title != "before" {
		t.Errorf("BL-400 was mutated by a dry run: title=%q err=%v", gotItem.Title, gErr)
	}
	gotSpec, gErr := svc.store.GetSpec(ctx, "SPEC-400")
	if gErr != nil || gotSpec.Title != "before" {
		t.Errorf("SPEC-400 was mutated by a dry run: title=%q err=%v", gotSpec.Title, gErr)
	}
	if _, gErr := svc.store.GetBacklogItem(ctx, "BL-401"); gErr == nil {
		t.Error("BL-401 must not exist after a dry run")
	}
	if _, gErr := svc.store.GetSpec(ctx, "SPEC-401"); gErr == nil {
		t.Error("SPEC-401 must not exist after a dry run")
	}

	// The preview still reports what WOULD happen.
	joinedCreated := strings.Join(result.Created, "\n")
	joinedUpdated := strings.Join(result.Updated, "\n")
	if !strings.Contains(joinedCreated, "BL-401") {
		t.Errorf("Created = %v, want it to mention BL-401", result.Created)
	}
	if !strings.Contains(joinedCreated, "SPEC-401") {
		t.Errorf("Created = %v, want it to mention SPEC-401", result.Created)
	}
	if !strings.Contains(joinedUpdated, "BL-400") {
		t.Errorf("Updated = %v, want it to mention BL-400", result.Updated)
	}
	if !strings.Contains(joinedUpdated, "SPEC-400") {
		t.Errorf("Updated = %v, want it to mention SPEC-400", result.Updated)
	}
}

// TestSDDImport_IsIdempotent is AC11.
func TestSDDImport_IsIdempotent(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, importTestProject)
	enableSDD(t, repoDir, importTestProject)
	ctx := context.Background()

	item := &model.BacklogItem{
		ID: "BL-080", UUID: "0198f000-0000-7000-8000-0000000000c1",
		Title: "x", Status: model.BacklogStatusRaw, Priority: model.PriorityMedium,
		Project: importTestProject, Lane: model.LaneStandard,
		CreatedAt: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
	}
	writeBacklogFixture(t, repoDir, item, nil)

	if _, err := svc.ImportSDDFromRepo(ctx, repoDir, true); err != nil {
		t.Fatalf("ImportSDDFromRepo (1st): %v", err)
	}
	gitRunSDDTest(t, repoDir, "add", ".")
	gitRunSDDTest(t, repoDir, "commit", "-m", "sdd fixtures")

	before, err := svc.store.GetBacklogItem(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetBacklogItem (before 2nd pass): %v", err)
	}

	if _, err := svc.ImportSDDFromRepo(ctx, repoDir, true); err != nil {
		t.Fatalf("ImportSDDFromRepo (2nd): %v", err)
	}

	after, err := svc.store.GetBacklogItem(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetBacklogItem (after 2nd pass): %v", err)
	}
	if before.UpdatedAt != after.UpdatedAt || before.Title != after.Title {
		t.Errorf("a second import changed the aggregate: before=%+v after=%+v", before, after)
	}

	status := gitRunSDDTest(t, repoDir, "status", "--porcelain", "--", ".mneme/sdd")
	if status != "" {
		t.Errorf("git status --porcelain -- .mneme/sdd is not empty after a no-op second import:\n%s", status)
	}
}

// TestSDDImport_DisabledIsANoOp is AC26.
func TestSDDImport_DisabledIsANoOp(t *testing.T) {
	ctx := context.Background()

	t.Run("no marker at all", func(t *testing.T) {
		svc, repoDir := newSDDMaterializeService(t, importTestProject)
		result, err := svc.ImportSDDFromRepo(ctx, repoDir, true)
		if err != nil {
			t.Fatalf("ImportSDDFromRepo: %v", err)
		}
		if result.NoOpReason == "" {
			t.Error("NoOpReason is empty, want a reason")
		}
	})

	t.Run("marker present but sdd.off set", func(t *testing.T) {
		svc, repoDir := newSDDMaterializeService(t, importTestProject)
		enableSDD(t, repoDir, importTestProject)
		if err := os.WriteFile(filepath.Join(repoDir, ".mneme", "sdd.off"), nil, 0o644); err != nil {
			t.Fatalf("write sdd.off: %v", err)
		}
		result, err := svc.ImportSDDFromRepo(ctx, repoDir, true)
		if err != nil {
			t.Fatalf("ImportSDDFromRepo: %v", err)
		}
		if result.NoOpReason == "" {
			t.Error("NoOpReason is empty, want a reason")
		}
	})

	t.Run("full cycle plus import leaves nothing behind", func(t *testing.T) {
		svc, repoDir := newSDDMaterializeService(t, importTestProject)
		runFullSDDCycle(t, ctx, svc)
		if _, err := svc.ImportSDDFromRepo(ctx, repoDir, true); err != nil {
			t.Fatalf("ImportSDDFromRepo: %v", err)
		}
		status := gitRunSDDTest(t, repoDir, "status", "--porcelain")
		if status != "" {
			t.Errorf("git status --porcelain is not empty:\n%s", status)
		}
		if n := countFiles(t, filepath.Join(repoDir, ".mneme")); n != 0 {
			t.Errorf(".mneme has %d file(s), want 0", n)
		}
	})
}

// TestSDDImport_RefusesForeignProjectMarker is AC27.
func TestSDDImport_RefusesForeignProjectMarker(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, importTestProject)
	enableSDD(t, repoDir, "some/other-project")
	ctx := context.Background()

	item := &model.BacklogItem{
		ID: "BL-090", Title: "foreign", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: "some/other-project", Lane: model.LaneStandard,
	}
	writeBacklogFixture(t, repoDir, item, nil)

	_, err := svc.ImportSDDFromRepo(ctx, repoDir, true)
	if err == nil {
		t.Fatal("ImportSDDFromRepo must fail for a foreign project marker")
	}
	if !strings.Contains(err.Error(), "some/other-project") || !strings.Contains(err.Error(), importTestProject) {
		t.Errorf("error = %q, want both project names", err.Error())
	}

	if _, gErr := svc.store.GetBacklogItem(ctx, "BL-090"); gErr == nil {
		t.Error("BL-090 must not have been created")
	}
	items, total, _, err := svc.store.ListBacklogItems(ctx, importTestProject, "", 0)
	if err != nil {
		t.Fatalf("ListBacklogItems: %v", err)
	}
	if total != 0 || len(items) != 0 {
		t.Errorf("ListBacklogItems = %d items, want 0 — nothing may be created on a foreign-marker refusal", total)
	}
}

// TestSDDImport_EmptyRepoRootIsAnError is AC28b.
func TestSDDImport_EmptyRepoRootIsAnError(t *testing.T) {
	svc, _ := newSDDMaterializeService(t, importTestProject)
	if _, err := svc.ImportSDDFromRepo(context.Background(), "", true); err == nil {
		t.Error("ImportSDDFromRepo(\"\") must fail")
	}
}

// TestSDDImport_FrozenSpecKeepsItsStatus is AC32.
func TestSDDImport_FrozenSpecKeepsItsStatus(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, importTestProject)
	enableSDD(t, repoDir, importTestProject)
	ctx := context.Background()

	// Row 1: a frozen spec whose file brings a DIFFERENT status — must be
	// skipped, the rest of the batch must still go through.
	frozenItem := &model.BacklogItem{
		ID: "BL-201", Title: "frozen origin", Status: model.BacklogStatusArchived,
		ArchiveReason: "superseded", Priority: model.PriorityMedium,
		Project: importTestProject, Lane: model.LaneStandard,
	}
	if err := svc.store.CreateBacklogItem(ctx, frozenItem); err != nil {
		t.Fatalf("CreateBacklogItem (frozen origin): %v", err)
	}
	frozenSpec := &model.Spec{
		ID: "SPEC-201", Title: "frozen spec", Status: model.SpecStatusImplementing,
		Project: importTestProject, BacklogID: frozenItem.ID, Lane: model.LaneStandard,
	}
	if err := svc.store.CreateSpec(ctx, frozenSpec); err != nil {
		t.Fatalf("CreateSpec (frozen): %v", err)
	}
	frozenFromRepo := &model.Spec{
		ID: frozenSpec.ID, UUID: frozenSpec.UUID, Title: "frozen spec", Status: model.SpecStatusDone,
		Project: importTestProject, BacklogID: frozenItem.ID, Lane: model.LaneStandard,
	}
	writeSpecFixture(t, repoDir, frozenFromRepo, nil, nil)

	// An unrelated healthy record that MUST still be imported.
	healthy := &model.BacklogItem{
		ID: "BL-205", Title: "healthy sibling", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: importTestProject, Lane: model.LaneStandard,
	}
	writeBacklogFixture(t, repoDir, healthy, nil)

	result, err := svc.ImportSDDFromRepo(ctx, repoDir, true)
	if err != nil {
		t.Fatalf("ImportSDDFromRepo (row 1): %v", err)
	}

	got, err := svc.store.GetSpec(ctx, frozenSpec.ID)
	if err != nil {
		t.Fatalf("GetSpec (frozen): %v", err)
	}
	if got.Status != model.SpecStatusImplementing {
		t.Errorf("frozen spec status = %s, want unchanged implementing", got.Status)
	}
	foundSkip := false
	for _, s := range result.Skipped {
		if s.ID == frozenSpec.ID && s.Reason == "spec-congelada" {
			foundSkip = true
		}
	}
	if !foundSkip {
		t.Errorf("Skipped = %v, want an entry for %s with reason spec-congelada", result.Skipped, frozenSpec.ID)
	}
	if _, hErr := svc.store.GetBacklogItem(ctx, "BL-205"); hErr != nil {
		t.Errorf("the healthy sibling was not imported: %v", hErr)
	}

	// Row 2: same frozen spec, but the file brings the SAME status — must
	// NOT be skipped; the title still applies normally.
	svc2, repoDir2 := newSDDMaterializeService(t, importTestProject)
	enableSDD(t, repoDir2, importTestProject)
	frozenItem2 := &model.BacklogItem{
		ID: "BL-203", Title: "frozen origin 2", Status: model.BacklogStatusArchived,
		ArchiveReason: "superseded", Priority: model.PriorityMedium,
		Project: importTestProject, Lane: model.LaneStandard,
	}
	if err := svc2.store.CreateBacklogItem(ctx, frozenItem2); err != nil {
		t.Fatalf("CreateBacklogItem (row 2): %v", err)
	}
	frozenSpec2 := &model.Spec{
		ID: "SPEC-203", Title: "frozen spec 2", Status: model.SpecStatusImplementing,
		Project: importTestProject, BacklogID: frozenItem2.ID, Lane: model.LaneStandard,
	}
	if err := svc2.store.CreateSpec(ctx, frozenSpec2); err != nil {
		t.Fatalf("CreateSpec (row 2): %v", err)
	}
	sameStatusFromRepo := &model.Spec{
		ID: frozenSpec2.ID, UUID: frozenSpec2.UUID, Title: "renamed by repo", Status: model.SpecStatusImplementing,
		Project: importTestProject, BacklogID: frozenItem2.ID, Lane: model.LaneStandard,
	}
	writeSpecFixture(t, repoDir2, sameStatusFromRepo, nil, nil)

	if _, err := svc2.ImportSDDFromRepo(ctx, repoDir2, true); err != nil {
		t.Fatalf("ImportSDDFromRepo (row 2): %v", err)
	}
	got2, err := svc2.store.GetSpec(ctx, frozenSpec2.ID)
	if err != nil {
		t.Fatalf("GetSpec (row 2): %v", err)
	}
	if got2.Title != "renamed by repo" {
		t.Errorf("row 2: title = %q, want applied normally (same status)", got2.Title)
	}
	if got2.Status != model.SpecStatusImplementing {
		t.Errorf("row 2: status = %s, want unchanged implementing (file brought the SAME status)", got2.Status)
	}

	// Row 3: the snapshot. The item archives AND its spec moves in the SAME
	// batch — both must apply, because the spec was NOT frozen when this
	// import began.
	svc3, repoDir3 := newSDDMaterializeService(t, importTestProject)
	enableSDD(t, repoDir3, importTestProject)
	item3 := &model.BacklogItem{
		ID: "BL-204", Title: "about to archive", Status: model.BacklogStatusRefined,
		Priority: model.PriorityMedium, Project: importTestProject, Lane: model.LaneStandard,
	}
	if err := svc3.store.CreateBacklogItem(ctx, item3); err != nil {
		t.Fatalf("CreateBacklogItem (row 3): %v", err)
	}
	spec3 := &model.Spec{
		ID: "SPEC-204", Title: "moving spec", Status: model.SpecStatusSpeccing,
		Project: importTestProject, BacklogID: item3.ID, Lane: model.LaneStandard,
	}
	if err := svc3.store.CreateSpec(ctx, spec3); err != nil {
		t.Fatalf("CreateSpec (row 3): %v", err)
	}

	archivedFromRepo := &model.BacklogItem{
		ID: item3.ID, UUID: item3.UUID, Title: "about to archive", Status: model.BacklogStatusArchived,
		ArchiveReason: "done via spec", Priority: model.PriorityMedium,
		Project: importTestProject, Lane: model.LaneStandard,
	}
	writeBacklogFixture(t, repoDir3, archivedFromRepo, nil)
	movedSpecFromRepo := &model.Spec{
		ID: spec3.ID, UUID: spec3.UUID, Title: "moving spec", Status: model.SpecStatusSpecced,
		Project: importTestProject, BacklogID: item3.ID, Lane: model.LaneStandard,
	}
	writeSpecFixture(t, repoDir3, movedSpecFromRepo, nil, nil)

	if _, err := svc3.ImportSDDFromRepo(ctx, repoDir3, true); err != nil {
		t.Fatalf("ImportSDDFromRepo (row 3): %v", err)
	}
	gotItem3, err := svc3.store.GetBacklogItem(ctx, item3.ID)
	if err != nil {
		t.Fatalf("GetBacklogItem (row 3): %v", err)
	}
	if gotItem3.Status != model.BacklogStatusArchived {
		t.Errorf("row 3: item status = %s, want archived", gotItem3.Status)
	}
	gotSpec3, err := svc3.store.GetSpec(ctx, spec3.ID)
	if err != nil {
		t.Fatalf("GetSpec (row 3): %v", err)
	}
	if gotSpec3.Status != model.SpecStatusSpecced {
		t.Errorf("row 3: spec status = %s, want specced — the pair arrived together and the spec was not "+
			"frozen at the START of this import (D64's snapshot rule)", gotSpec3.Status)
	}
}

// writeNonCanonicalBacklogFixture writes a COMPLETE backlog record that
// omits the `schema:` line (D28: absence is legal and means schema 1) —
// AC12's discriminator fixture. Marshals normally (so every other field is
// exactly what mneme itself would produce) then strips the one line by
// hand, since Marshal's own round-trip check would otherwise refuse to
// return bytes missing a field it always writes.
func writeNonCanonicalBacklogFixture(t *testing.T, repoDir string, item *model.BacklogItem) {
	t.Helper()
	data, err := sddfile.MarshalBacklog(&sddfile.BacklogRecord{Item: item})
	if err != nil {
		t.Fatalf("MarshalBacklog(%s): %v", item.ID, err)
	}
	stripped := strings.Replace(string(data), "schema: 1\n", "", 1)
	if stripped == string(data) {
		t.Fatalf("fixture setup: expected a schema: 1 line to strip in:\n%s", data)
	}
	if err := sddfile.WriteRecord(sddfile.BacklogPath(repoDir, item.ID), []byte(stripped)); err != nil {
		t.Fatalf("WriteRecord(%s): %v", item.ID, err)
	}
}

// TestSDDImport_OnlyRewritesIncompleteFiles is AC12 — EL criterio de esta
// spec (D46/D52). Three fixtures: BL-001 complete and canonical (written
// by mneme itself), BL-002 complete but NOT canonical (missing the
// `schema:` line — legal per D28, the DISCRIMINATOR: if the reader touches
// it, it gains `schema: 1` and the byte comparison sees it), BL-003
// incomplete (only title + a description body).
func TestSDDImport_OnlyRewritesIncompleteFiles(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, importTestProject)
	enableSDD(t, repoDir, importTestProject)
	ctx := context.Background()

	fixedTS := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	canonical := &model.BacklogItem{
		ID: "BL-001", UUID: "0198f000-0000-7000-8000-0000000000d1",
		Title: "canonical", Status: model.BacklogStatusRaw, Priority: model.PriorityMedium,
		Project: importTestProject, Lane: model.LaneStandard,
		CreatedAt: fixedTS, UpdatedAt: fixedTS,
	}
	writeBacklogFixture(t, repoDir, canonical, nil)

	nonCanonical := &model.BacklogItem{
		ID: "BL-002", UUID: "0198f000-0000-7000-8000-0000000000d2",
		Title: "non canonical", Status: model.BacklogStatusRaw, Priority: model.PriorityMedium,
		Project: importTestProject, Lane: model.LaneStandard,
		CreatedAt: fixedTS, UpdatedAt: fixedTS,
	}
	writeNonCanonicalBacklogFixture(t, repoDir, nonCanonical)

	writeRawSDDFile(t, sddfile.BacklogPath(repoDir, "BL-003"),
		"---\ntitle: \"incomplete item\"\n---\n\njust a description, nothing else\n")

	before1, err := sddfile.ReadRecord(sddfile.BacklogPath(repoDir, "BL-001"))
	if err != nil {
		t.Fatalf("read BL-001 before import: %v", err)
	}
	before2, err := sddfile.ReadRecord(sddfile.BacklogPath(repoDir, "BL-002"))
	if err != nil {
		t.Fatalf("read BL-002 before import: %v", err)
	}

	result, err := svc.ImportSDDFromRepo(ctx, repoDir, true)
	if err != nil {
		t.Fatalf("ImportSDDFromRepo: %v", err)
	}

	after1, err := sddfile.ReadRecord(sddfile.BacklogPath(repoDir, "BL-001"))
	if err != nil {
		t.Fatalf("read BL-001 after import: %v", err)
	}
	if string(before1) != string(after1) {
		t.Errorf("BL-001.md (complete, canonical) was rewritten, want byte-identical")
	}

	after2, err := sddfile.ReadRecord(sddfile.BacklogPath(repoDir, "BL-002"))
	if err != nil {
		t.Fatalf("read BL-002 after import: %v", err)
	}
	if string(before2) != string(after2) {
		t.Errorf("BL-002.md (complete, non-canonical) was rewritten, want byte-identical")
	}
	if strings.Contains(string(after2), "schema:") {
		t.Errorf("BL-002.md gained a schema: line — the reader touched a complete-but-non-canonical file")
	}

	got3, err := svc.store.GetBacklogItem(ctx, "BL-003")
	if err != nil {
		t.Fatalf("GetBacklogItem(BL-003): %v", err)
	}
	if got3.UUID == "" {
		t.Error("BL-003 has no anchor after import, want one minted")
	}
	if got3.Project != importTestProject || got3.Status != model.BacklogStatusRaw ||
		got3.Priority != model.PriorityMedium || got3.Lane != model.LaneStandard {
		t.Errorf("BL-003 defaults not applied: %+v", got3)
	}

	var completed *SDDImportCompleted
	for i := range result.Completed {
		if result.Completed[i].ID == "BL-003" {
			completed = &result.Completed[i]
		}
	}
	if completed == nil {
		t.Fatalf("Completed = %v, want an entry for BL-003", result.Completed)
	}
	wantFields := []string{"uuid", "project", "status", "priority", "lane", "created_at", "updated_at"}
	for _, want := range wantFields {
		found := false
		for _, got := range completed.Fields {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("Completed[BL-003].Fields = %v, missing %q", completed.Fields, want)
		}
	}

	// All three elements exist in the base regardless of which file was
	// rewritten.
	for _, id := range []string{"BL-001", "BL-002", "BL-003"} {
		if _, gErr := svc.store.GetBacklogItem(ctx, id); gErr != nil {
			t.Errorf("%s does not exist in the base: %v", id, gErr)
		}
	}
}

// Mutaciones exigidas (documentadas aqui; ejecutadas y revertidas durante
// la implementacion, resultado real en changes.md):
//   - AC5: estampar now() en updated_at dentro de importBacklogRecord/
//     importSpecRecord -> TestSDDImport_PreservesIdentityAndTimestamps en
//     rojo (las fechas de la fixture son de 2024/2026 y "ahora" no lo es).
//   - AC6a/AC6b: anadir `if !fileTS.After(row.UpdatedAt) { skip }` ->
//     TestSDDImport_FileWinsEvenWhenOlder Y TestSDDImport_NeverComparesTimestamps
//     en rojo.
//   - AC7: decidir por item.ID en vez de por ancla ->
//     TestSDDImport_SkipsCorrelativeClaimedByTwoAnchors en rojo (la fila
//     pasaria a decir "lo del companero").
//   - AC8: reemplazar MergeBacklogRefinements por un borrado+insercion en
//     bloque -> TestSDDImport_MergesChildrenNeverDeletes en rojo (desaparece
//     el seq 3).
//   - AC9: quitar la condicion "si no existe" de MergeSpecHistory ->
//     TestSDDImport_TwiceCreatesNoDuplicates en rojo (el historial se
//     duplica).
//   - AC27: quitar la comprobacion de marker.Project ->
//     TestSDDImport_RefusesForeignProjectMarker en rojo (BL-090 se crea).
//   - AC32: evaluar el congelado contra el estado vivo (recalcular
//     freezeIndex dentro del bucle en vez de usar la instantanea) -> rojo
//     SOLO en la fila 3 de TestSDDImport_FrozenSpecKeepsItsStatus. Quitar
//     la comprobacion entera -> rojo en la fila 1.
//   - AC12 (la mutacion clave): quitar la condicion "missing != nil" antes
//     de llamar a reportIfCompleted (hacerla incondicional) ->
//     TestSDDImport_OnlyRewritesIncompleteFiles en rojo, nombrando BL-002
//     (gana una linea schema: que antes no tenia).

// TestApplySpecDefaults is a targeted addition (SPEC-131 commit 13, AC29):
// the first coverage pass left applySpecDefaults at 30% — every fixture in
// this file already sets Status/Lane explicitly, so the gap-filling
// branches (an empty field falling back to `existing`, or to D53's fixed
// defaults when there is no existing row) never ran. Table-driven, one
// subtest per branch, directly against the unexported function rather
// than through a full import (which would leave the SAME gaps: the point
// is exercising applySpecDefaults itself, not re-proving the import path).
func TestApplySpecDefaults(t *testing.T) {
	existing := &model.Spec{Status: model.SpecStatusImplementing, Lane: model.LaneTrivial}

	tests := []struct {
		name       string
		spec       *model.Spec
		existing   *model.Spec
		wantProj   string
		wantStatus model.SpecStatus
		wantLane   model.Lane
	}{
		{
			name:       "empty project is filled from svc.project",
			spec:       &model.Spec{Status: model.SpecStatusDraft, Lane: model.LaneStandard},
			existing:   nil,
			wantProj:   "wirvii/mneme",
			wantStatus: model.SpecStatusDraft,
			wantLane:   model.LaneStandard,
		},
		{
			name:       "non-empty project is left alone",
			spec:       &model.Spec{Project: "other/project", Status: model.SpecStatusDraft, Lane: model.LaneStandard},
			existing:   nil,
			wantProj:   "other/project",
			wantStatus: model.SpecStatusDraft,
			wantLane:   model.LaneStandard,
		},
		{
			name:       "empty status with no existing row defaults to draft",
			spec:       &model.Spec{Lane: model.LaneStandard},
			existing:   nil,
			wantProj:   "wirvii/mneme",
			wantStatus: model.SpecStatusDraft,
			wantLane:   model.LaneStandard,
		},
		{
			name:       "empty status with an existing row falls back to it, never to draft",
			spec:       &model.Spec{Lane: model.LaneStandard},
			existing:   existing,
			wantProj:   "wirvii/mneme",
			wantStatus: model.SpecStatusImplementing,
			wantLane:   model.LaneStandard,
		},
		{
			name:       "non-empty status is left alone even with an existing row",
			spec:       &model.Spec{Status: model.SpecStatusQA, Lane: model.LaneStandard},
			existing:   existing,
			wantProj:   "wirvii/mneme",
			wantStatus: model.SpecStatusQA,
			wantLane:   model.LaneStandard,
		},
		{
			name:       "empty lane with no existing row defaults to standard",
			spec:       &model.Spec{Status: model.SpecStatusDraft},
			existing:   nil,
			wantProj:   "wirvii/mneme",
			wantStatus: model.SpecStatusDraft,
			wantLane:   model.LaneStandard,
		},
		{
			name:       "empty lane with an existing row falls back to it, never to standard",
			spec:       &model.Spec{Status: model.SpecStatusDraft},
			existing:   existing,
			wantProj:   "wirvii/mneme",
			wantStatus: model.SpecStatusDraft,
			wantLane:   model.LaneTrivial,
		},
		{
			name:       "non-empty lane is left alone even with an existing row",
			spec:       &model.Spec{Status: model.SpecStatusDraft, Lane: model.LaneStandard},
			existing:   existing,
			wantProj:   "wirvii/mneme",
			wantStatus: model.SpecStatusDraft,
			wantLane:   model.LaneStandard,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			applySpecDefaults("wirvii/mneme", tt.spec, tt.existing)
			if tt.spec.Project != tt.wantProj {
				t.Errorf("Project = %q, want %q", tt.spec.Project, tt.wantProj)
			}
			if tt.spec.Status != tt.wantStatus {
				t.Errorf("Status = %q, want %q", tt.spec.Status, tt.wantStatus)
			}
			if tt.spec.Lane != tt.wantLane {
				t.Errorf("Lane = %q, want %q", tt.spec.Lane, tt.wantLane)
			}
		})
	}
}

// TestApplyBacklogDefaults is applyBacklogDefaults' own targeted addition
// (QA rejection fix, mirroring TestApplySpecDefaults above): the function
// sat at 78.6% for the same reason applySpecDefaults did before its own
// fix — every fixture elsewhere already sets Status/Priority/Lane
// explicitly, so several gap-fill branches never ran.
func TestApplyBacklogDefaults(t *testing.T) {
	existing := &model.BacklogItem{
		Status: model.BacklogStatusPromoted, Priority: model.PriorityHigh, Lane: model.LaneTrivial,
	}

	tests := []struct {
		name         string
		item         *model.BacklogItem
		existing     *model.BacklogItem
		wantProj     string
		wantStatus   model.BacklogStatus
		wantPriority model.Priority
		wantLane     model.Lane
	}{
		{
			name:         "empty project is filled from svc.project",
			item:         &model.BacklogItem{Status: model.BacklogStatusRaw, Priority: model.PriorityMedium, Lane: model.LaneStandard},
			existing:     nil,
			wantProj:     "wirvii/mneme",
			wantStatus:   model.BacklogStatusRaw,
			wantPriority: model.PriorityMedium,
			wantLane:     model.LaneStandard,
		},
		{
			name:         "empty status with no existing row defaults to raw",
			item:         &model.BacklogItem{Priority: model.PriorityMedium, Lane: model.LaneStandard},
			existing:     nil,
			wantProj:     "wirvii/mneme",
			wantStatus:   model.BacklogStatusRaw,
			wantPriority: model.PriorityMedium,
			wantLane:     model.LaneStandard,
		},
		{
			name:         "empty status with an existing row falls back to it",
			item:         &model.BacklogItem{Priority: model.PriorityMedium, Lane: model.LaneStandard},
			existing:     existing,
			wantProj:     "wirvii/mneme",
			wantStatus:   model.BacklogStatusPromoted,
			wantPriority: model.PriorityMedium,
			wantLane:     model.LaneStandard,
		},
		{
			name:         "empty priority with no existing row defaults to medium",
			item:         &model.BacklogItem{Status: model.BacklogStatusRaw, Lane: model.LaneStandard},
			existing:     nil,
			wantProj:     "wirvii/mneme",
			wantStatus:   model.BacklogStatusRaw,
			wantPriority: model.PriorityMedium,
			wantLane:     model.LaneStandard,
		},
		{
			name:         "empty priority with an existing row falls back to it",
			item:         &model.BacklogItem{Status: model.BacklogStatusRaw, Lane: model.LaneStandard},
			existing:     existing,
			wantProj:     "wirvii/mneme",
			wantStatus:   model.BacklogStatusRaw,
			wantPriority: model.PriorityHigh,
			wantLane:     model.LaneStandard,
		},
		{
			name:         "empty lane with no existing row defaults to standard",
			item:         &model.BacklogItem{Status: model.BacklogStatusRaw, Priority: model.PriorityMedium},
			existing:     nil,
			wantProj:     "wirvii/mneme",
			wantStatus:   model.BacklogStatusRaw,
			wantPriority: model.PriorityMedium,
			wantLane:     model.LaneStandard,
		},
		{
			name:         "empty lane with an existing row falls back to it",
			item:         &model.BacklogItem{Status: model.BacklogStatusRaw, Priority: model.PriorityMedium},
			existing:     existing,
			wantProj:     "wirvii/mneme",
			wantStatus:   model.BacklogStatusRaw,
			wantPriority: model.PriorityMedium,
			wantLane:     model.LaneTrivial,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			applyBacklogDefaults("wirvii/mneme", tt.item, tt.existing)
			if tt.item.Project != tt.wantProj {
				t.Errorf("Project = %q, want %q", tt.item.Project, tt.wantProj)
			}
			if tt.item.Status != tt.wantStatus {
				t.Errorf("Status = %q, want %q", tt.item.Status, tt.wantStatus)
			}
			if tt.item.Priority != tt.wantPriority {
				t.Errorf("Priority = %q, want %q", tt.item.Priority, tt.wantPriority)
			}
			if tt.item.Lane != tt.wantLane {
				t.Errorf("Lane = %q, want %q", tt.item.Lane, tt.wantLane)
			}
		})
	}
}

// TestRelSDDPath is relSDDPath's own targeted addition (QA rejection fix):
// the function sat at 75.0% because nothing ever exercised its error
// branch — filepath.Rel genuinely fails (on every OS this repo targets)
// when one argument is absolute and the other is not, which is exactly the
// shape a caller-error would take, not a fabricated scenario.
func TestRelSDDPath(t *testing.T) {
	tests := []struct {
		name     string
		repoRoot string
		path     string
		want     string
	}{
		{
			name:     "path under repoRoot's SDD dir is made relative",
			repoRoot: "/repo",
			path:     "/repo/.mneme/sdd/backlog/BL-001.md",
			want:     "backlog/BL-001.md",
		},
		{
			name:     "a relative path against an absolute repoRoot cannot be resolved and is returned verbatim",
			repoRoot: "/repo",
			path:     "backlog/BL-001.md",
			want:     "backlog/BL-001.md",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := relSDDPath(tt.repoRoot, tt.path)
			if got != tt.want {
				t.Errorf("relSDDPath(%q, %q) = %q, want %q", tt.repoRoot, tt.path, got, tt.want)
			}
		})
	}
}

// TestComputeOnlyInBase_CapsAtMaxAndReportsRealTotal is computeOnlyInBase's
// own targeted addition (QA rejection fix): the function sat at 77.8%
// because nothing exercised D62's own cap (maxOnlyInBaseListed=20) — every
// other test in this file seeds far fewer than 21 uncovered correlatives.
// Seeds 25 backlog items covered by no file at all (covered=empty map) and
// asserts the returned slice is capped at 20 while Total still reports 25.
func TestComputeOnlyInBase_CapsAtMaxAndReportsRealTotal(t *testing.T) {
	svc := newTestSDDService(t, "wirvii/mneme")
	ctx := context.Background()

	for i := 0; i < 25; i++ {
		if _, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{
			Title: "seed", Lane: model.LaneStandard,
		}); err != nil {
			t.Fatalf("BacklogAdd (seed %d): %v", i, err)
		}
	}

	missing, total, _, err := svc.computeOnlyInBase(ctx, map[string]bool{})
	if err != nil {
		t.Fatalf("computeOnlyInBase: %v", err)
	}
	if total != 25 {
		t.Errorf("total = %d, want 25 (the real count, uncapped)", total)
	}
	if len(missing) != 20 {
		t.Errorf("len(missing) = %d, want 20 (capped at maxOnlyInBaseListed)", len(missing))
	}
}
