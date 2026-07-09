package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/juanftp/mneme/internal/model"
)

// runTeamMemoryEnableCmd builds a minimal Cobra tree so newTeamMemoryEnableCmd
// can be invoked in isolation (mirrors runTeamMemoryHooksCmd from
// team_memory_hooks_test.go), chdirs into cwd for the duration of the call
// (restored via t.Cleanup), and returns stdout, stderr, and any error.
func runTeamMemoryEnableCmd(t *testing.T, cwd string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	resetGlobalCLIFlags(t)
	root := &cobra.Command{Use: "mneme"}
	root.AddCommand(func() *cobra.Command {
		tm := &cobra.Command{Use: "team-memory"}
		tm.AddCommand(newTeamMemoryEnableCmd())
		return tm
	}())

	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)

	orig, wdErr := os.Getwd()
	if wdErr != nil {
		t.Fatalf("Getwd: %v", wdErr)
	}
	if chErr := os.Chdir(cwd); chErr != nil {
		t.Fatalf("Chdir %s: %v", cwd, chErr)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	root.SetArgs(append([]string{"team-memory", "enable"}, args...))
	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestTeamMemoryEnable_FreshRepo_CreatesMarkerAndInstallsHooks(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	stdout, _, err := runTeamMemoryEnableCmd(t, dir)
	if err != nil {
		t.Fatalf("enable: %v", err)
	}

	markerPath := filepath.Join(dir, ".mneme", "shared", ".mneme-vault")
	if _, statErr := os.Stat(markerPath); statErr != nil {
		t.Errorf("expected marker at %s: %v", markerPath, statErr)
	}

	hooksDir, hErr := gitHooksDir(dir)
	if hErr != nil {
		t.Fatalf("gitHooksDir: %v", hErr)
	}
	for _, hookName := range teamMemoryHooksTargetHooks {
		hookPath := filepath.Join(hooksDir, hookName)
		if !hookFileExists(t, hookPath) {
			t.Errorf("%s: expected executable hook file, not found", hookName)
		}
	}

	if !strings.Contains(stdout, "Team memory enabled") {
		t.Errorf("expected stdout to report enablement, got: %q", stdout)
	}
	if !strings.Contains(stdout, "PRIVACY NOTICE") {
		t.Errorf("expected the privacy notice to be printed, got: %q", stdout)
	}
}

func TestTeamMemoryEnable_Idempotent_ReportsAlreadyEnabled(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	if _, _, err := runTeamMemoryEnableCmd(t, dir); err != nil {
		t.Fatalf("first enable: %v", err)
	}

	stdout, _, err := runTeamMemoryEnableCmd(t, dir)
	if err != nil {
		t.Fatalf("second enable: %v", err)
	}
	if !strings.Contains(stdout, "already enabled") {
		t.Errorf("expected 'already enabled' on the second run, got: %q", stdout)
	}

	hooksDir, _ := gitHooksDir(dir)
	for _, hookName := range teamMemoryHooksTargetHooks {
		content := readHookFile(t, filepath.Join(hooksDir, hookName))
		count := strings.Count(content, teamMemoryHooksMarkerBegin)
		if count != 1 {
			t.Errorf("%s: expected exactly 1 begin-marker occurrence after 2 enables, got %d", hookName, count)
		}
	}
}

func TestTeamMemoryEnable_NotGitRepo(t *testing.T) {
	dir := t.TempDir() // plain directory, no git init

	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	_, _, err := runTeamMemoryEnableCmd(t, dir)
	if err == nil {
		t.Fatal("expected error for non-git directory, got nil")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("expected 'not a git repository' in error, got: %v", err)
	}
}

// TestTeamMemoryEnable_BakesExistingDurableMemory verifies the end-to-end CLI
// contract: a decision saved BEFORE "team-memory enable" runs is baked to
// shared=1 and exported to the shared vault by the command.
func TestTeamMemoryEnable_BakesExistingDurableMemory(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	resetGlobalCLIFlags(t)
	orig, wdErr := os.Getwd()
	if wdErr != nil {
		t.Fatalf("Getwd: %v", wdErr)
	}
	if chErr := os.Chdir(dir); chErr != nil {
		t.Fatalf("Chdir: %v", chErr)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	svc, cleanup, err := initService()
	if err != nil {
		t.Fatalf("initService: %v", err)
	}
	resp, saveErr := svc.Save(context.Background(), model.SaveRequest{
		Title:   "Pre-existing decision",
		Content: "Saved before enabling team-memory via the CLI.",
		Type:    model.TypeDecision,
	})
	cleanup()
	if saveErr != nil {
		t.Fatalf("Save: %v", saveErr)
	}

	stdout, _, err := runTeamMemoryEnableCmd(t, dir)
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !strings.Contains(stdout, "Baked 1 pre-existing") {
		t.Errorf("expected stdout to report 1 baked memory, got: %q", stdout)
	}

	path := filepath.Join(dir, ".mneme", "shared", "notes", resp.ID+".md")
	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("expected exported vault note at %s: %v", path, statErr)
	}
}

// TestNewTeamMemoryCmd_RegistersEnable is a light structural check that the
// "team-memory" parent command exposes the "enable" subcommand alongside
// "hooks" (SPEC-065).
func TestNewTeamMemoryCmd_RegistersEnable(t *testing.T) {
	cmd := newTeamMemoryCmd()
	found := false
	for _, sub := range cmd.Commands() {
		if sub.Use == "enable" {
			found = true
		}
	}
	if !found {
		t.Error("expected \"mneme team-memory\" to register an \"enable\" subcommand")
	}
}
