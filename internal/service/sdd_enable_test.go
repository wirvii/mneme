// Package service — tests for EnableSDDRepo/DisableSDDRepo/ExportSDDRepo/
// SDDStatus (SPEC-130 §2a commit 6): dry-run previews nothing, --apply
// exports everything and writes the marker, and the convergence guard
// (D45) refuses before writing a single byte.
package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/sddfile"
)

func TestSDDEnable_PreviewWritesNothing(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, "wirvii/mneme")
	ctx := context.Background()

	if _, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "x", Lane: model.LaneStandard, Project: "wirvii/mneme"}); err != nil {
		t.Fatalf("BacklogAdd: %v", err)
	}

	result, err := svc.EnableSDDRepo(ctx, repoDir, false)
	if err != nil {
		t.Fatalf("EnableSDDRepo (preview): %v", err)
	}
	if result.Applied {
		t.Fatal("preview must report Applied=false")
	}
	if result.Plan.BacklogCount != 1 {
		t.Errorf("Plan.BacklogCount = %d, want 1", result.Plan.BacklogCount)
	}
	if len(result.Warnings) != 4 {
		t.Errorf("len(Warnings) = %d, want 4 (D17/AC14)", len(result.Warnings))
	}

	if n := countFiles(t, filepath.Join(repoDir, ".mneme")); n != 0 {
		t.Errorf(".mneme has %d file(s) after a preview, want 0", n)
	}
}

// TestSDDEnable_WarningsContainTheRequiredPhrases is AC14, exercised at the
// service layer directly (the CLI-level test in commit 7 re-asserts the
// same substrings against the printed output).
func TestSDDEnable_WarningsContainTheRequiredPhrases(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, "wirvii/mneme")
	result, err := svc.EnableSDDRepo(context.Background(), repoDir, false)
	if err != nil {
		t.Fatalf("EnableSDDRepo (preview): %v", err)
	}

	joined := strings.Join(result.Warnings, "\n")
	mustContain := []string{
		"no puede determinar si el remoto es publico",
		"no ha escaneado el contenido",
		"revisarse en un pull request",
	}
	for _, want := range mustContain {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings do not contain %q:\n%s", want, joined)
		}
	}
}

func TestSDDEnable_ApplyExportsAllAndWritesMarker(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, "wirvii/mneme")
	ctx := context.Background()

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "x", Lane: model.LaneStandard, Project: "wirvii/mneme"})
	if err != nil {
		t.Fatalf("BacklogAdd: %v", err)
	}
	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "y", Lane: model.LaneStandard, Project: "wirvii/mneme"})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}

	result, err := svc.EnableSDDRepo(ctx, repoDir, true)
	if err != nil {
		t.Fatalf("EnableSDDRepo (apply): %v", err)
	}
	if !result.Applied {
		t.Fatal("apply must report Applied=true")
	}

	if _, err := sddfile.ReadRecord(sddfile.BacklogPath(repoDir, item.ID)); err != nil {
		t.Errorf("backlog record was not written: %v", err)
	}
	if _, err := sddfile.ReadRecord(sddfile.SpecRecordPath(repoDir, spec.ID)); err != nil {
		t.Errorf("spec record was not written: %v", err)
	}

	marker, err := sddfile.ReadMarker(repoDir)
	if err != nil {
		t.Fatalf("ReadMarker: %v", err)
	}
	if marker == nil {
		t.Fatal("marker was not written")
	}
	if marker.BacklogCount != 1 || marker.SpecCount != 1 {
		t.Errorf("marker counts = %+v, want BacklogCount=1 SpecCount=1", marker)
	}

	gitignore, err := os.ReadFile(filepath.Join(repoDir, ".mneme", ".gitignore"))
	if err != nil {
		t.Fatalf("read .mneme/.gitignore: %v", err)
	}
	if !strings.Contains(string(gitignore), "sdd.off") {
		t.Errorf(".mneme/.gitignore = %q, want it to contain sdd.off", gitignore)
	}
}

// TestSDDEnable_ApplyTwiceIsIdempotent is the write-through half of AC13
// (the CLI-level test in commit 7 owns the full criterion, including
// `git status --porcelain`): materializing the same DB state twice must
// produce byte-identical files — the deterministic order D36 guarantees.
func TestSDDEnable_ApplyTwiceIsIdempotent(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, "wirvii/mneme")
	ctx := context.Background()

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "x", Lane: model.LaneStandard, Project: "wirvii/mneme"})
	if err != nil {
		t.Fatalf("BacklogAdd: %v", err)
	}

	if _, err := svc.EnableSDDRepo(ctx, repoDir, true); err != nil {
		t.Fatalf("EnableSDDRepo (first): %v", err)
	}
	first, err := sddfile.ReadRecord(sddfile.BacklogPath(repoDir, item.ID))
	if err != nil {
		t.Fatalf("ReadRecord (first): %v", err)
	}

	if _, err := svc.EnableSDDRepo(ctx, repoDir, true); err != nil {
		t.Fatalf("EnableSDDRepo (second): %v", err)
	}
	second, err := sddfile.ReadRecord(sddfile.BacklogPath(repoDir, item.ID))
	if err != nil {
		t.Fatalf("ReadRecord (second): %v", err)
	}

	if string(first) != string(second) {
		t.Errorf("re-enabling produced different bytes:\nfirst=%s\nsecond=%s", first, second)
	}
}

func TestSDDDisable_PreviewWritesNothing(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, "wirvii/mneme")
	result, err := svc.DisableSDDRepo(context.Background(), repoDir, false)
	if err != nil {
		t.Fatalf("DisableSDDRepo (preview): %v", err)
	}
	if result.Applied {
		t.Fatal("preview must report Applied=false")
	}
	if _, err := os.Stat(sddOffPath(repoDir)); !os.IsNotExist(err) {
		t.Error("sdd.off must not exist after a preview")
	}
}

func TestSDDDisable_ApplyWritesOffMarkerWithoutDeletingRecords(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, "wirvii/mneme")
	ctx := context.Background()

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "x", Lane: model.LaneStandard, Project: "wirvii/mneme"})
	if err != nil {
		t.Fatalf("BacklogAdd: %v", err)
	}
	if _, err := svc.EnableSDDRepo(ctx, repoDir, true); err != nil {
		t.Fatalf("EnableSDDRepo: %v", err)
	}

	result, err := svc.DisableSDDRepo(ctx, repoDir, true)
	if err != nil {
		t.Fatalf("DisableSDDRepo (apply): %v", err)
	}
	if !result.Applied {
		t.Fatal("apply must report Applied=true")
	}

	if _, err := os.Stat(sddOffPath(repoDir)); err != nil {
		t.Errorf("sdd.off must exist after DisableSDDRepo apply: %v", err)
	}
	// The pre-existing record must survive untouched.
	if _, err := sddfile.ReadRecord(sddfile.BacklogPath(repoDir, item.ID)); err != nil {
		t.Errorf("disable must never delete existing records: %v", err)
	}

	// From here on, the wrapper must be inert.
	if _, err := svc.BacklogRefine(ctx, model.BacklogRefineRequest{ID: item.ID, Refinement: "after disable", By: "orchestrator"}); err != nil {
		t.Fatalf("BacklogRefine after disable: %v", err)
	}
	if state := ResolveSDDState(repoDir); state.Enabled {
		t.Error("mechanism must read as disabled after DisableSDDRepo apply")
	}
}

func TestSDDExport_RequiresEnabledFirst(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, "wirvii/mneme")
	_, err := svc.ExportSDDRepo(context.Background(), repoDir)
	if err == nil {
		t.Fatal("ExportSDDRepo on a never-enabled repo must fail")
	}
}

// TestSDDConvergence_RefusesForeignAnchor is the service-level half of
// AC17: a BL-050.md whose anchor is not in the local database makes both
// enable and export refuse WITHOUT writing anything, and the pre-existing
// file is left byte-for-byte untouched. The CLI-level test in commit 7
// (TestSDDEnable_RefusesForeignRecords) exercises the same guard through
// the real command surface, including the required mutation.
func TestSDDConvergence_RefusesForeignAnchor(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, "wirvii/mneme")
	ctx := context.Background()

	foreign := &sddfile.BacklogRecord{Item: &model.BacklogItem{
		ID: "BL-050", Title: "de otra maquina", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: "wirvii/mneme", Lane: model.LaneStandard,
		UUID: "01a044bc-7c25-7448-87e9-febc5c5982ee", // a real-shaped UUID this DB never minted
	}}
	data, err := sddfile.MarshalBacklog(foreign)
	if err != nil {
		t.Fatalf("MarshalBacklog fixture: %v", err)
	}
	path := sddfile.BacklogPath(repoDir, "BL-050")
	if err := sddfile.WriteRecord(path, data); err != nil {
		t.Fatalf("WriteRecord fixture: %v", err)
	}

	if _, err := svc.EnableSDDRepo(ctx, repoDir, true); err == nil {
		t.Fatal("EnableSDDRepo must refuse when a foreign anchor is present")
	}

	after, err := sddfile.ReadRecord(path)
	if err != nil {
		t.Fatalf("ReadRecord after refusal: %v", err)
	}
	if string(after) != string(data) {
		t.Error("the foreign file must be byte-for-byte untouched after a refused enable")
	}

	if n := countFiles(t, filepath.Join(repoDir, ".mneme", "sdd", "backlog")); n != 1 {
		t.Errorf("backlog dir has %d file(s) after refusal, want exactly the 1 fixture file", n)
	}
	if _, err := sddfile.ReadMarker(repoDir); err != nil {
		t.Fatalf("ReadMarker: %v", err)
	}
}

// TestSDDExport_Succeeds exercises ExportSDDRepo's full success path: an
// already-enabled repo re-materializes everything and its marker's
// LastExportAt advances.
func TestSDDExport_Succeeds(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, "wirvii/mneme")
	ctx := context.Background()

	if _, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "x", Lane: model.LaneStandard, Project: "wirvii/mneme"}); err != nil {
		t.Fatalf("BacklogAdd: %v", err)
	}
	if _, err := svc.EnableSDDRepo(ctx, repoDir, true); err != nil {
		t.Fatalf("EnableSDDRepo: %v", err)
	}
	before, err := sddfile.ReadMarker(repoDir)
	if err != nil || before == nil {
		t.Fatalf("ReadMarker (before): %v", err)
	}

	result, err := svc.ExportSDDRepo(ctx, repoDir)
	if err != nil {
		t.Fatalf("ExportSDDRepo: %v", err)
	}
	if result.Plan.BacklogCount != 1 {
		t.Errorf("ExportSDDRepo Plan.BacklogCount = %d, want 1", result.Plan.BacklogCount)
	}

	after, err := sddfile.ReadMarker(repoDir)
	if err != nil || after == nil {
		t.Fatalf("ReadMarker (after): %v", err)
	}
	if after.BacklogCount != 1 {
		t.Errorf("marker.BacklogCount after export = %d, want 1", after.BacklogCount)
	}
}

// TestSDDOperations_EmptyRepoRootFails covers the "repoRoot is required"
// guard shared by all four operator-surface verbs.
func TestSDDOperations_EmptyRepoRootFails(t *testing.T) {
	svc, _ := newSDDMaterializeService(t, "wirvii/mneme")
	ctx := context.Background()

	if _, err := svc.EnableSDDRepo(ctx, "", false); err == nil {
		t.Error("EnableSDDRepo(\"\") must fail")
	}
	if _, err := svc.DisableSDDRepo(ctx, "", false); err == nil {
		t.Error("DisableSDDRepo(\"\") must fail")
	}
	if _, err := svc.ExportSDDRepo(ctx, ""); err == nil {
		t.Error("ExportSDDRepo(\"\") must fail")
	}
	if _, err := svc.SDDStatus(ctx, ""); err == nil {
		t.Error("SDDStatus(\"\") must fail")
	}
}

// TestSDDEnable_NotGitRepoFails is the "si no es un repositorio git" branch
// of §9.1 step 1.
func TestSDDEnable_NotGitRepoFails(t *testing.T) {
	svc, _ := newSDDMaterializeService(t, "wirvii/mneme")
	notGit := t.TempDir()
	if _, err := svc.EnableSDDRepo(context.Background(), notGit, false); err == nil {
		t.Fatal("EnableSDDRepo on a non-git directory must fail")
	}
}

// TestSddGit_IsWorkTreeAndRemoteURL_NoRemote covers sddGit's own branches
// directly: a plain (non-git) directory is not a work tree, and a git repo
// with no configured remote reports "" rather than erroring.
func TestSddGit_IsWorkTreeAndRemoteURL_NoRemote(t *testing.T) {
	notGit := sddGit{RepoDir: t.TempDir()}
	if notGit.IsWorkTree() {
		t.Error("a plain directory must not report IsWorkTree=true")
	}
	if got := notGit.RemoteURL(); got != "" {
		t.Errorf("RemoteURL on a non-git dir = %q, want \"\"", got)
	}

	_, repoDir := newSDDMaterializeService(t, "wirvii/mneme") // real git repo, no remote configured
	g := sddGit{RepoDir: repoDir}
	if !g.IsWorkTree() {
		t.Error("a real git repo must report IsWorkTree=true")
	}
	if got := g.RemoteURL(); got != "" {
		t.Errorf("RemoteURL with no configured remote = %q, want \"\"", got)
	}
}

func TestSDDStatus_ReportsBrokenAndForeign(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, "wirvii/mneme")
	ctx := context.Background()

	brokenPath := sddfile.BacklogPath(repoDir, "BL-999")
	if err := sddfile.WriteRecord(brokenPath, []byte("not a valid record at all")); err != nil {
		t.Fatalf("write broken fixture: %v", err)
	}

	status, err := svc.SDDStatus(ctx, repoDir)
	if err != nil {
		t.Fatalf("SDDStatus: %v", err)
	}
	if status.Enabled {
		t.Error("SDDStatus reports Enabled=true with no marker written")
	}
	if len(status.Broken) != 1 {
		t.Errorf("Broken = %v, want exactly 1 entry", status.Broken)
	}
}

// TestSDDStatus_IgnoresSpecEntregables is SPEC-131 commit 2's own row: with
// the old heuristic (filepath.Base(path) == "record.md", W7) a plan.md
// deposited beside a spec's record.md would be misclassified as a backlog
// item and reported broken. ClassifyRecordPath (D63) ignores it instead.
func TestSDDStatus_IgnoresSpecEntregables(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, "wirvii/mneme")
	ctx := context.Background()

	planPath := filepath.Join(sddfile.SpecDir(repoDir, "SPEC-001"), "plan.md")
	if err := sddfile.WriteRecord(planPath, []byte("# Plan\n\nnot an SDD record at all")); err != nil {
		t.Fatalf("write plan.md fixture: %v", err)
	}

	status, err := svc.SDDStatus(ctx, repoDir)
	if err != nil {
		t.Fatalf("SDDStatus: %v", err)
	}
	if len(status.Broken) != 0 {
		t.Errorf("Broken = %v, want empty — plan.md must be ignored, not reported broken (W7/D63)", status.Broken)
	}
}
