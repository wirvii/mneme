package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/vault"
)

// TestInitService_WiresTeamMemory is the SPEC-085 AC3 regression guard: the
// ONE production call site that is allowed to opt a MemoryService into
// team-memory auto-detection is initService (see the WithTeamMemory call
// added there alongside NewMemoryService's default-OFF change). Without a
// test asserting this, a future refactor could silently drop the opt-in and
// team-memory would stop working in production without any test going red
// (SPEC-085 R1, the most serious risk identified in the design).
//
// This drives the real initService() — not a hand-built test service — with
// HOME sandboxed to an isolated temp directory (so --data-dir and the
// config/DB paths it resolves never touch the developer's real ~/.mneme)
// and cwd chdir'd into a fresh temporary git repository carrying an active
// shared vault marker, mirroring exactly the production shape
// DetectTeamMemory() resolves against.
func TestInitService_WiresTeamMemory(t *testing.T) {
	// Package-level flags are normally populated by cobra's persistent-flag
	// binding; initService reads them directly. Save/restore so this test
	// never leaks state into any test that runs after it in this binary.
	origProject, origDataDir, origLogLevel := flagProject, flagDataDir, flagLogLevel
	flagProject, flagDataDir, flagLogLevel = "", "", ""
	t.Cleanup(func() { flagProject, flagDataDir, flagLogLevel = origProject, origDataDir, origLogLevel })

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "nonexistent-gitconfig"))

	repoDir := t.TempDir()
	mustRunGit(t, repoDir, "init")
	mustRunGit(t, repoDir, "config", "user.name", "Team Member")
	mustRunGit(t, repoDir, "config", "user.email", "team@example.com")

	sharedRoot := filepath.Join(repoDir, ".mneme", "shared")
	writeVaultMarker(t, sharedRoot, "")

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir into temp repo: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	svc, cleanup, err := initService()
	if err != nil {
		t.Fatalf("initService: unexpected error: %v", err)
	}
	defer cleanup()

	ctx := context.Background()
	resp, err := svc.Save(ctx, model.SaveRequest{
		Title:   "initService must wire team-memory in production",
		Content: "A decision saved through the real production constructor.",
		Type:    model.TypeDecision,
	})
	if err != nil {
		t.Fatalf("Save: unexpected error: %v", err)
	}

	mem, err := svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Get: unexpected error: %v", err)
	}
	if mem.Shared != 1 {
		t.Errorf("expected Shared=1 — initService must opt in to team-memory via WithTeamMemory(DetectTeamMemory()), got %d", mem.Shared)
	}
	if mem.Author == "" {
		t.Error("expected a non-empty Author baked from the local git identity")
	}

	notePath := filepath.Join(repoDir, ".mneme", "shared", "notes", resp.ID+".md")
	if _, statErr := os.Stat(notePath); statErr != nil {
		t.Errorf("expected write-through materialization at %s (proves team-memory is active end-to-end in production), stat err: %v", notePath, statErr)
	}
}

// mustRunGit runs the package's runGitCmd helper and fails the test on error.
func mustRunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := runGitCmd(dir, args...); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}

// writeVaultMarker writes a minimal .mneme-vault JSON marker at vaultRoot,
// activating team-memory detection for any process whose cwd resolves to the
// enclosing git repository.
func writeVaultMarker(t *testing.T, vaultRoot, project string) {
	t.Helper()
	if err := os.MkdirAll(vaultRoot, 0o755); err != nil {
		t.Fatalf("mkdir vault root: %v", err)
	}
	marker := vault.VaultMarker{
		VaultVersion: 1,
		Project:      project,
		Scope:        "shared",
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
		LastExportAt: time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		t.Fatalf("marshal marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vaultRoot, vault.MarkerFileName), data, 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
}
