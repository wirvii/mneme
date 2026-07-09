package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// resetGlobalCLIFlags saves the current values of the package-level
// flagDataDir/flagProject vars, resets them to "", and restores the original
// values via t.Cleanup. These vars are normally populated by Cobra's
// persistent-flag binding on the real root command, but this file builds a
// minimal Cobra tree that never registers those flags — so, without this
// reset, a leftover value set by an EARLIER test in this package (e.g.
// codegraph_test.go or subagents_test.go, which do exercise the real root
// command with --data-dir/--project) would silently leak into initService()
// here, defeating the HOME-based isolation these tests rely on.
func resetGlobalCLIFlags(t *testing.T) {
	t.Helper()
	origDataDir, origProject := flagDataDir, flagProject
	flagDataDir, flagProject = "", ""
	t.Cleanup(func() { flagDataDir, flagProject = origDataDir, origProject })
}

// runTeamMemoryHooksCmd is the team-memory analogue of runHooksCmd
// (codegraph_hooks_test.go): it builds a minimal Cobra tree so
// newTeamMemoryHooksCmd() can be invoked in isolation, chdirs into cwd for
// the duration of the call (restored via t.Cleanup), and returns stdout,
// stderr, and any error.
func runTeamMemoryHooksCmd(t *testing.T, cwd string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	resetGlobalCLIFlags(t)
	root := &cobra.Command{Use: "mneme"}
	root.AddCommand(func() *cobra.Command {
		tm := &cobra.Command{Use: "team-memory"}
		tm.AddCommand(newTeamMemoryHooksCmd())
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

	root.SetArgs(append([]string{"team-memory", "hooks"}, args...))
	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

// writeSharedVaultMarker writes a .mneme-vault JSON marker at
// <repoDir>/.mneme/shared, with an empty project so checkImportMarker never
// rejects it regardless of what project.Detector resolves for repoDir (no
// git remote is configured in these tests).
func writeSharedVaultMarker(t *testing.T, repoDir string) string {
	t.Helper()
	sharedRoot := filepath.Join(repoDir, ".mneme", "shared")
	if err := os.MkdirAll(sharedRoot, 0o755); err != nil {
		t.Fatalf("mkdir shared vault root: %v", err)
	}
	marker := `{"vault_version":1,"project":"","scope":"shared"}`
	if err := os.WriteFile(filepath.Join(sharedRoot, ".mneme-vault"), []byte(marker), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	return sharedRoot
}

// writeSharedNoteForHookTest writes a minimal, valid team-memory vault note.
func writeSharedNoteForHookTest(t *testing.T, sharedRoot, id, topicKey, title, content string) {
	t.Helper()
	notesDir := filepath.Join(sharedRoot, "notes")
	if err := os.MkdirAll(notesDir, 0o755); err != nil {
		t.Fatalf("mkdir notes dir: %v", err)
	}
	fm := fmt.Sprintf(`---
id: %s
type: decision
scope: project
title: %q
topic_key: %s
importance: 0.80
confidence: 0.80
decay_rate: 0.01
created_at: 2026-01-01T00:00:00Z
updated_at: %s
revision_count: 0
shared: 1
author: Peer <peer@example.com>
---

%s
`, id, title, topicKey, time.Now().UTC().Format(time.RFC3339Nano), content)

	path := filepath.Join(notesDir, id+".md")
	if err := os.WriteFile(path, []byte(fm), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// readTeamMemoryHookLog reads ~/.mneme/team-memory-hooks.log under the
// given fake HOME, returning "" if it does not exist.
func readTeamMemoryHookLog(t *testing.T, fakeHome string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fakeHome, ".mneme", "team-memory-hooks.log"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatalf("read hook log: %v", err)
	}
	return string(data)
}

func TestTeamMemoryHooksInstall_FreshRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	_, _, err := runTeamMemoryHooksCmd(t, dir, "install")
	if err != nil {
		t.Fatalf("install: %v", err)
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
		content := readHookFile(t, hookPath)
		if !strings.HasPrefix(content, "#!/bin/sh") {
			t.Errorf("%s: expected #!/bin/sh shebang, got: %q", hookName, content[:min(30, len(content))])
		}
		if !strings.Contains(content, teamMemoryHooksMarkerBegin) {
			t.Errorf("%s: missing begin marker", hookName)
		}
		if !strings.Contains(content, teamMemoryHooksMarkerEnd) {
			t.Errorf("%s: missing end marker", hookName)
		}
		if !strings.Contains(content, "run-import") {
			t.Errorf("%s: expected run-import invocation", hookName)
		}
	}
}

func TestTeamMemoryHooksInstall_Idempotent(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	for i := 0; i < 2; i++ {
		_, _, err := runTeamMemoryHooksCmd(t, dir, "install")
		if err != nil {
			t.Fatalf("install round %d: %v", i+1, err)
		}
	}

	hooksDir, _ := gitHooksDir(dir)
	for _, hookName := range teamMemoryHooksTargetHooks {
		content := readHookFile(t, filepath.Join(hooksDir, hookName))
		count := strings.Count(content, teamMemoryHooksMarkerBegin)
		if count != 1 {
			t.Errorf("%s: expected 1 begin-marker occurrence, got %d", hookName, count)
		}
	}
}

func TestTeamMemoryHooksInstall_AppendsToExisting(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	hooksDir, _ := gitHooksDir(dir)
	hookPath := filepath.Join(hooksDir, "post-merge")

	existing := "#!/bin/sh\necho 'user hook'\n"
	if err := os.WriteFile(hookPath, []byte(existing), 0o755); err != nil {
		t.Fatalf("write existing hook: %v", err)
	}

	_, _, err := runTeamMemoryHooksCmd(t, dir, "install")
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	content := readHookFile(t, hookPath)
	if !strings.Contains(content, "echo 'user hook'") {
		t.Errorf("original content not preserved: %q", content)
	}
	if !strings.Contains(content, teamMemoryHooksMarkerBegin) {
		t.Errorf("mneme block not appended")
	}
}

func TestTeamMemoryHooksRemove_RemovesOnlyMarkedBlock(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	_, _, err := runTeamMemoryHooksCmd(t, dir, "install")
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	hooksDir, _ := gitHooksDir(dir)
	hookPath := filepath.Join(hooksDir, "post-merge")

	f, err := os.OpenFile(hookPath, os.O_APPEND|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatalf("open hook for append: %v", err)
	}
	_, _ = f.WriteString("# user post-merge logic\n")
	_ = f.Close()

	_, _, err = runTeamMemoryHooksCmd(t, dir, "remove")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}

	content := readHookFile(t, hookPath)
	if strings.Contains(content, teamMemoryHooksMarkerBegin) {
		t.Errorf("mneme begin-marker still present after remove")
	}
	if strings.Contains(content, teamMemoryHooksMarkerEnd) {
		t.Errorf("mneme end-marker still present after remove")
	}
	if !strings.Contains(content, "user post-merge logic") {
		t.Errorf("user content removed unexpectedly: %q", content)
	}
}

func TestTeamMemoryHooksRemove_NoBlock_NoOp(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	hooksDir, _ := gitHooksDir(dir)
	hookPath := filepath.Join(hooksDir, "post-merge")

	original := "#!/bin/sh\necho hello\n"
	if err := os.WriteFile(hookPath, []byte(original), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}

	_, _, err := runTeamMemoryHooksCmd(t, dir, "remove")
	if err != nil {
		t.Fatalf("remove (no-block): %v", err)
	}

	content := readHookFile(t, hookPath)
	if content != original {
		t.Errorf("file modified when no block present: got %q, want %q", content, original)
	}
}

func TestTeamMemoryHooksInstall_NotGitRepo(t *testing.T) {
	dir := t.TempDir() // plain directory, no git init

	_, _, err := runTeamMemoryHooksCmd(t, dir, "install")
	if err == nil {
		t.Fatal("expected error for non-git directory, got nil")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("expected 'not a git repository' in error, got: %v", err)
	}
}

// TestRunTeamMemoryHooksImport_SkipsDuringRebase verifies that run-import
// exits 0 and performs no import when a rebase-merge sentinel is present.
func TestRunTeamMemoryHooksImport_SkipsDuringRebase(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	gd, err := gitDir(dir)
	if err != nil {
		t.Fatalf("gitDir: %v", err)
	}
	sentinel := filepath.Join(gd, "rebase-merge")
	if err := os.WriteFile(sentinel, []byte(""), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	_, _, err = runTeamMemoryHooksCmd(t, dir, "run-import")
	if err != nil {
		t.Fatalf("run-import with rebase sentinel: expected exit 0, got %v", err)
	}

	if log := readTeamMemoryHookLog(t, fakeHome); log != "" {
		t.Errorf("expected no log entries when skipping during rebase, got: %q", log)
	}
}

// TestRunTeamMemoryHooksImport_LogsFailure_MissingVault verifies that
// run-import exits 0 but appends an error line to team-memory-hooks.log when
// the repository has no .mneme/shared vault to import from.
func TestRunTeamMemoryHooksImport_LogsFailure_MissingVault(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	_, _, err := runTeamMemoryHooksCmd(t, dir, "run-import")
	if err != nil {
		t.Fatalf("run-import: expected exit 0 on failure, got %v", err)
	}

	log := readTeamMemoryHookLog(t, fakeHome)
	if !strings.Contains(log, "event=error") {
		t.Errorf("expected an event=error line in the hook log, got: %q", log)
	}
}

// TestRunTeamMemoryHooksImport_Success verifies the full wiring: a
// well-formed shared vault is imported into the real project database, and
// no failure is logged.
func TestRunTeamMemoryHooksImport_Success(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	sharedRoot := writeSharedVaultMarker(t, dir)
	writeSharedNoteForHookTest(t, sharedRoot,
		"01938f1b-abcd-7abc-8def-000000000099", "team/hook-imported-decision",
		"Hook-imported decision", "This memory arrived via the post-merge hook.")

	_, _, err := runTeamMemoryHooksCmd(t, dir, "run-import")
	if err != nil {
		t.Fatalf("run-import: %v", err)
	}

	log := readTeamMemoryHookLog(t, fakeHome)
	if strings.Contains(log, "event=error") {
		t.Errorf("unexpected error logged: %q", log)
	}
}

// TestRunTeamMemoryHooksImport_ReportsConflictCandidates verifies that a
// successful import reporting potential conflicts (SPEC-053 D6) writes an
// event=conflict_report line to the hook log, since git hooks discard
// stdout/stderr.
func TestRunTeamMemoryHooksImport_ReportsConflictCandidates(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	sharedRoot := writeSharedVaultMarker(t, dir)
	writeSharedNoteForHookTest(t, sharedRoot,
		"01938f1b-abcd-7abc-8def-000000000097", "team/hook-conflict-a",
		"JWT authentication token", "We use JWT tokens for authentication in the API gateway")
	writeSharedNoteForHookTest(t, sharedRoot,
		"01938f1b-abcd-7abc-8def-000000000098", "team/hook-conflict-b",
		"Token authentication system", "Authentication using signed tokens for API access control")

	_, _, err := runTeamMemoryHooksCmd(t, dir, "run-import")
	if err != nil {
		t.Fatalf("run-import: %v", err)
	}

	log := readTeamMemoryHookLog(t, fakeHome)
	if !strings.Contains(log, "event=conflict_report") {
		t.Errorf("expected a conflict_report line in the hook log, got: %q", log)
	}
	if !strings.Contains(log, "mneme conflicts scan") {
		t.Errorf("expected the conflict_report hint to mention 'mneme conflicts scan', got: %q", log)
	}
}

// TestNewTeamMemoryCmd_RegistersHooks is a light structural check that the
// "team-memory" parent command exposes the "hooks" subcommand group.
func TestNewTeamMemoryCmd_RegistersHooks(t *testing.T) {
	cmd := newTeamMemoryCmd()
	found := false
	for _, sub := range cmd.Commands() {
		if sub.Use == "hooks" {
			found = true
		}
	}
	if !found {
		t.Error("expected \"mneme team-memory\" to register a \"hooks\" subcommand")
	}
}

// TestTeamMemoryHooksRunImport_Hidden verifies run-import is hidden from help.
func TestTeamMemoryHooksRunImport_Hidden(t *testing.T) {
	cmd := newTeamMemoryHooksCmd()
	for _, sub := range cmd.Commands() {
		if sub.Use == "run-import" && !sub.Hidden {
			t.Error("run-import must be Hidden")
		}
	}
}
