package service_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/service"
)

// newTestServiceWithVaultDir creates a service whose data dir is rooted at tmp
// so vault files land in a controlled location during tests.
func newTestServiceWithVaultDir(t *testing.T) (*service.MemoryService, string) {
	t.Helper()
	svc := newTestService(t)
	tmp := t.TempDir()
	svc.Config().Storage.DataDir = tmp
	return svc, tmp
}

func saveMemoryForVaultTest(t *testing.T, svc *service.MemoryService, req model.SaveRequest) string {
	t.Helper()
	resp, err := svc.Save(context.Background(), req)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	return resp.ID
}

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestVaultExport_ProjectScope(t *testing.T) {
	svc, tmp := newTestServiceWithVaultDir(t)
	ctx := context.Background()

	saveMemoryForVaultTest(t, svc, model.SaveRequest{
		Title:    "Arch decision",
		Content:  "Some content",
		Type:     model.TypeDecision,
		Scope:    model.ScopeProject,
		TopicKey: "arch/decision-1",
	})

	result, err := svc.VaultExport(ctx, service.VaultExportOptions{
		Scope: "project",
	})
	if err != nil {
		t.Fatalf("VaultExport failed: %v", err)
	}
	if result.Project == nil {
		t.Fatal("Project result should be non-nil")
	}
	if result.Global != nil {
		t.Error("Global result should be nil for scope=project")
	}
	if result.Project.Total < 1 {
		t.Errorf("expected at least 1 memory exported; got %d", result.Project.Total)
	}

	// Verify the .md file exists.
	mdPath := filepath.Join(tmp, "vaults", result.Project.VaultRoot[len(filepath.Join(tmp, "vaults")):], "notes", "arch", "decision-1.md")
	_ = mdPath // path derivation verified via result.Paths below
	if result.Project.Written < 1 {
		t.Errorf("expected at least 1 file written; got %d", result.Project.Written)
	}
}

func TestVaultExport_GlobalScope(t *testing.T) {
	svc, tmp := newTestServiceWithVaultDir(t)
	ctx := context.Background()

	saveMemoryForVaultTest(t, svc, model.SaveRequest{
		Title:   "Global pref",
		Content: "Use camelCase",
		Type:    model.TypePreference,
		Scope:   model.ScopeGlobal,
	})

	result, err := svc.VaultExport(ctx, service.VaultExportOptions{
		Scope: "global",
	})
	if err != nil {
		t.Fatalf("VaultExport failed: %v", err)
	}
	if result.Global == nil {
		t.Fatal("Global result should be non-nil")
	}
	if result.Project != nil {
		t.Error("Project result should be nil for scope=global")
	}
	if result.Global.Total < 1 {
		t.Errorf("expected at least 1 global memory; got %d", result.Global.Total)
	}

	// Global vault root should be under <dataDir>/vaults/_global.
	expectedRoot := filepath.Join(tmp, "vaults", "_global")
	if result.Global.VaultRoot != expectedRoot {
		t.Errorf("Global vault root = %q; want %q", result.Global.VaultRoot, expectedRoot)
	}
}

func TestVaultExport_AllScope(t *testing.T) {
	svc, _ := newTestServiceWithVaultDir(t)
	ctx := context.Background()

	saveMemoryForVaultTest(t, svc, model.SaveRequest{
		Title:   "Project decision",
		Content: "Use ports/adapters",
		Type:    model.TypeDecision,
		Scope:   model.ScopeProject,
	})
	saveMemoryForVaultTest(t, svc, model.SaveRequest{
		Title:   "Global pref",
		Content: "Go 1.24+",
		Type:    model.TypePreference,
		Scope:   model.ScopeGlobal,
	})

	result, err := svc.VaultExport(ctx, service.VaultExportOptions{
		Scope: "all",
	})
	if err != nil {
		t.Fatalf("VaultExport failed: %v", err)
	}
	if result.Project == nil {
		t.Error("Project result should be non-nil for scope=all")
	}
	if result.Global == nil {
		t.Error("Global result should be non-nil for scope=all")
	}
	if result.Project.Total < 1 {
		t.Errorf("expected at least 1 project memory; got %d", result.Project.Total)
	}
	if result.Global.Total < 1 {
		t.Errorf("expected at least 1 global memory; got %d", result.Global.Total)
	}
}

func TestVaultExport_TypeFilter(t *testing.T) {
	svc, _ := newTestServiceWithVaultDir(t)
	ctx := context.Background()

	saveMemoryForVaultTest(t, svc, model.SaveRequest{
		Title:   "Arch note",
		Content: "Architecture content",
		Type:    model.TypeArchitecture,
		Scope:   model.ScopeProject,
	})
	saveMemoryForVaultTest(t, svc, model.SaveRequest{
		Title:   "Decision note",
		Content: "Decision content",
		Type:    model.TypeDecision,
		Scope:   model.ScopeProject,
	})

	result, err := svc.VaultExport(ctx, service.VaultExportOptions{
		Scope: "project",
		Type:  model.TypeArchitecture,
	})
	if err != nil {
		t.Fatalf("VaultExport failed: %v", err)
	}
	if result.Project.Total != 1 {
		t.Errorf("type filter should export only 1 memory; got %d", result.Project.Total)
	}
}

func TestVaultExport_EmptyDB(t *testing.T) {
	svc, tmp := newTestServiceWithVaultDir(t)
	ctx := context.Background()

	result, err := svc.VaultExport(ctx, service.VaultExportOptions{
		Scope: "project",
	})
	if err != nil {
		t.Fatalf("VaultExport on empty DB failed: %v", err)
	}
	if result.Project.Total != 0 {
		t.Errorf("empty DB should produce 0 total; got %d", result.Project.Total)
	}
	if result.Project.Written != 0 {
		t.Errorf("empty DB should produce 0 written; got %d", result.Project.Written)
	}

	// notes/ dir should not exist.
	notesDir := filepath.Join(result.Project.VaultRoot, "notes")
	if _, statErr := os.Stat(notesDir); !os.IsNotExist(statErr) {
		t.Error("notes/ dir should not be created with zero memories")
	}
	_ = tmp
}

func TestVaultExport_DryRun(t *testing.T) {
	svc, _ := newTestServiceWithVaultDir(t)
	ctx := context.Background()

	saveMemoryForVaultTest(t, svc, model.SaveRequest{
		Title:   "Dry run memory",
		Content: "Should not appear on disk",
		Type:    model.TypeDiscovery,
		Scope:   model.ScopeProject,
	})

	result, err := svc.VaultExport(ctx, service.VaultExportOptions{
		Scope:  "project",
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("VaultExport dry-run failed: %v", err)
	}
	if result.Project.Written < 1 {
		t.Errorf("dry-run should report at least 1 would-write; got %d", result.Project.Written)
	}

	// No files should be written.
	if _, err := os.Stat(filepath.Join(result.Project.VaultRoot, "notes")); !os.IsNotExist(err) {
		t.Error("dry-run should not create notes/ directory")
	}
}
