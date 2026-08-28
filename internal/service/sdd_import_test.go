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
	items, total, err := svc.store.ListBacklogItems(ctx, importTestProject, "", 0)
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
