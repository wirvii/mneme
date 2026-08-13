package cli

import (
	"os"
	"testing"
)

// TestInitSDDService_FixesRepoDir covers AC17 (SPEC-115 D13/P6): the ONE
// production construction site for SDDService — serving both the CLI and
// the MCP server (internal/cli/mcp.go) — must resolve and fix repoDir, so
// the quality mechanism never runs dormant (repoDir=="") in production.
//
// This is also guardian G6: SDDService.RepoDir() returns the field RAW, with
// no os.Getwd() fallback of its own — a fallback there would make this test
// pass unconditionally and prove nothing. Mutation (verified manually per
// the plan): removing the `sddSvc.WithRepoDir(root)` call from
// initSDDService turns this test red (RepoDir() empty).
func TestInitSDDService_FixesRepoDir(t *testing.T) {
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	origDataDir, origProject := flagDataDir, flagProject
	flagDataDir = t.TempDir()
	flagProject = "quality-wiring-test"
	t.Cleanup(func() { flagDataDir, flagProject = origDataDir, origProject })

	sddSvc, cleanup, err := initSDDService()
	if err != nil {
		t.Fatalf("initSDDService: %v", err)
	}
	defer cleanup()

	if sddSvc.RepoDir() == "" {
		t.Fatal("initSDDService did not fix repoDir — SDDService.RepoDir() is empty")
	}

	got, err := os.Stat(sddSvc.RepoDir())
	if err != nil {
		t.Fatalf("stat RepoDir(): %v", err)
	}
	want, err := os.Stat(repoDir)
	if err != nil {
		t.Fatalf("stat repoDir fixture: %v", err)
	}
	if !os.SameFile(got, want) {
		t.Errorf("RepoDir() = %q, want it to resolve to the same directory as %q", sddSvc.RepoDir(), repoDir)
	}
}

// TestInitQualityService_WiresRunnerAndRepoDir is the P9 wiring test (G10,
// same mould as G6): initQualityService() must return a QualityService with
// a non-nil runner and a non-empty repoDir. Mutation (verified manually per
// the plan): removing the runner argument (passing nil instead of
// &quality.ExecRunner{}) from initQualityService's NewQualityService call
// turns HasRunner() false, and this test red.
func TestInitQualityService_WiresRunnerAndRepoDir(t *testing.T) {
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	origDataDir, origProject := flagDataDir, flagProject
	flagDataDir = t.TempDir()
	flagProject = "quality-wiring-test-2"
	t.Cleanup(func() { flagDataDir, flagProject = origDataDir, origProject })

	qualitySvc, cleanup, err := initQualityService()
	if err != nil {
		t.Fatalf("initQualityService: %v", err)
	}
	defer cleanup()

	if !qualitySvc.HasRunner() {
		t.Error("initQualityService did not wire a runner — HasRunner() is false")
	}
	if qualitySvc.RepoDir() == "" {
		t.Error("initQualityService did not fix repoDir — RepoDir() is empty")
	}
	if qualitySvc.WorkflowDir() == "" {
		t.Error("initQualityService did not wire WorkflowDir (SPEC-117 P10) — WorkflowDir() is empty")
	}
}
