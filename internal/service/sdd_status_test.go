// Package service — tests for SDDStatus's extended, fully-derived fields
// (SPEC-131 §2b commit 10, D54): Conflicted, Incomplete, Divergent,
// HooksInstalled, OnlyInBaseCount, FrozenBlocked.
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

// TestSDDStatus_DerivesEverything is AC24: every one of D54's ten signals
// reports correctly from a single constructed scenario, and — since
// nothing here is stored anywhere — two consecutive calls produce an
// identical answer.
func TestSDDStatus_DerivesEverything(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, importTestProject)
	ctx := context.Background()

	// A frozen spec: its originating item archived, the spec itself still
	// "implementing".
	frozenItem := &model.BacklogItem{
		ID: "BL-050", Title: "frozen origin", Status: model.BacklogStatusArchived,
		ArchiveReason: "superseded", Priority: model.PriorityMedium,
		Project: importTestProject, Lane: model.LaneStandard,
	}
	if err := svc.store.CreateBacklogItem(ctx, frozenItem); err != nil {
		t.Fatalf("CreateBacklogItem(frozenItem): %v", err)
	}
	frozenSpec := &model.Spec{
		ID: "SPEC-050", Title: "frozen spec", Status: model.SpecStatusImplementing,
		Project: importTestProject, BacklogID: frozenItem.ID, Lane: model.LaneStandard,
	}
	if err := svc.store.CreateSpec(ctx, frozenSpec); err != nil {
		t.Fatalf("CreateSpec(frozenSpec): %v", err)
	}

	// An item that will exist only in the base (no file on this branch).
	onlyInBaseItem := &model.BacklogItem{
		ID: "BL-060", Title: "only in base", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: importTestProject, Lane: model.LaneStandard,
	}
	if err := svc.store.CreateBacklogItem(ctx, onlyInBaseItem); err != nil {
		t.Fatalf("CreateBacklogItem(onlyInBaseItem): %v", err)
	}

	// An item whose correlative will be disputed by a foreign anchor.
	conflictLocal := &model.BacklogItem{
		ID: "BL-080", Title: "lo mio", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: importTestProject, Lane: model.LaneStandard,
	}
	if err := svc.store.CreateBacklogItem(ctx, conflictLocal); err != nil {
		t.Fatalf("CreateBacklogItem(conflictLocal): %v", err)
	}

	// Enable: exports everything created above, writes the marker, and
	// installs this machine's own hooks.
	if _, err := svc.EnableSDDRepo(ctx, repoDir, true); err != nil {
		t.Fatalf("EnableSDDRepo: %v", err)
	}

	// (a) OnlyInBase: remove the exported file for onlyInBaseItem.
	if err := os.Remove(sddfile.BacklogPath(repoDir, onlyInBaseItem.ID)); err != nil {
		t.Fatalf("remove onlyInBaseItem file: %v", err)
	}

	// (b) HooksInstalled = false: remove the hooks EnableSDDRepo just installed.
	if err := svc.RemoveSDDHooks(repoDir); err != nil {
		t.Fatalf("RemoveSDDHooks: %v", err)
	}

	// (c) Broken.
	writeRawSDDFile(t, sddfile.BacklogPath(repoDir, "BL-070"), "not a valid record at all")

	// (d) Conflicted: overwrite BL-080's exported file with a DIFFERENT anchor.
	conflictFile := &model.BacklogItem{
		ID: "BL-080", UUID: "0198f000-0000-7000-8000-0000000000cc",
		Title: "lo del companero", Status: model.BacklogStatusRaw, Priority: model.PriorityMedium,
		Project: importTestProject, Lane: model.LaneStandard,
	}
	writeBacklogFixture(t, repoDir, conflictFile, nil)

	// (e) Incomplete.
	writeRawSDDFile(t, sddfile.BacklogPath(repoDir, "BL-090"), "---\ntitle: \"incomplete item\"\n---\n\ndescription only\n")

	// (f) Divergent: hand-edit frozenItem's own exported file without
	// updating the database.
	itemPath := sddfile.BacklogPath(repoDir, frozenItem.ID)
	itemRaw, err := sddfile.ReadRecord(itemPath)
	if err != nil {
		t.Fatalf("read frozenItem file: %v", err)
	}
	tamperedItem := strings.Replace(string(itemRaw), "frozen origin", "TAMPERED TITLE", 1)
	if tamperedItem == string(itemRaw) {
		t.Fatal("fixture setup: expected to find 'frozen origin' to tamper")
	}
	if err := sddfile.WriteRecord(itemPath, []byte(tamperedItem)); err != nil {
		t.Fatalf("write tampered item: %v", err)
	}

	// (g) FrozenBlocked: hand-edit frozenSpec's own exported file's status.
	specPath := sddfile.SpecRecordPath(repoDir, frozenSpec.ID)
	specRaw, err := sddfile.ReadRecord(specPath)
	if err != nil {
		t.Fatalf("read frozenSpec file: %v", err)
	}
	tamperedSpec := strings.Replace(string(specRaw), "status: implementing", "status: done", 1)
	if tamperedSpec == string(specRaw) {
		t.Fatal("fixture setup: expected to find 'status: implementing' to tamper")
	}
	if err := sddfile.WriteRecord(specPath, []byte(tamperedSpec)); err != nil {
		t.Fatalf("write tampered spec: %v", err)
	}

	status, err := svc.SDDStatus(ctx, repoDir)
	if err != nil {
		t.Fatalf("SDDStatus: %v", err)
	}

	if len(status.Broken) == 0 {
		t.Error("Broken = [], want at least one entry")
	}
	if len(status.Conflicted) == 0 {
		t.Error("Conflicted = [], want at least one entry")
	}
	if len(status.Incomplete) == 0 {
		t.Error("Incomplete = [], want at least one entry")
	}
	if len(status.Divergent) == 0 {
		t.Error("Divergent = [], want at least one entry")
	}
	if len(status.FrozenBlocked) == 0 {
		t.Error("FrozenBlocked = [], want at least one entry")
	}
	if status.OnlyInBaseCount == 0 {
		t.Error("OnlyInBaseCount = 0, want > 0")
	}
	if status.HooksInstalled {
		t.Error("HooksInstalled = true, want false (hooks were removed)")
	}

	// Two calls back to back must agree — nothing here is persisted.
	status2, err := svc.SDDStatus(ctx, repoDir)
	if err != nil {
		t.Fatalf("SDDStatus (2nd): %v", err)
	}
	if len(status.Broken) != len(status2.Broken) ||
		len(status.Conflicted) != len(status2.Conflicted) ||
		len(status.Incomplete) != len(status2.Incomplete) ||
		len(status.Divergent) != len(status2.Divergent) ||
		len(status.FrozenBlocked) != len(status2.FrozenBlocked) ||
		status.OnlyInBaseCount != status2.OnlyInBaseCount ||
		status.HooksInstalled != status2.HooksInstalled {
		t.Errorf("two consecutive SDDStatus calls disagree:\nfirst=%+v\nsecond=%+v", status, status2)
	}

	// The set of files under <repo>/.mneme is exactly what the mechanism
	// itself writes — no new state file was introduced by this commit.
	mnemeRoot := filepath.Join(repoDir, ".mneme")
	walkErr := filepath.WalkDir(mnemeRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(mnemeRoot, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if rel != ".gitignore" && rel != "sdd/.mneme-sdd" && !strings.HasSuffix(rel, ".md") {
			t.Errorf("unexpected file under .mneme: %s (D54: no new state file)", rel)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk .mneme: %v", walkErr)
	}
}
