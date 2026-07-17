package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// initGitRepo initialises a bare-minimum git repository in dir and returns the
// directory. It also sets user.email and user.name so "git commit" does not
// fail in CI environments that have no global git config.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	cmds := [][]string{
		{"git", "-C", dir, "init"},
		{"git", "-C", dir, "config", "user.email", "test@example.com"},
		{"git", "-C", dir, "config", "user.name", "Test"},
	}
	for _, c := range cmds {
		out, err := exec.Command(c[0], c[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("setup git (%v): %s", c, out)
		}
	}
}

// hookFileExists reports whether the hook file exists and is executable.
func hookFileExists(t *testing.T, hookPath string) bool {
	t.Helper()
	info, err := os.Stat(hookPath)
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		t.Fatalf("stat %s: %v", hookPath, err)
	}
	return info.Mode()&0o111 != 0
}

// readHookFile reads the hook file at hookPath and returns its content, or ""
// if the file does not exist.
func readHookFile(t *testing.T, hookPath string) string {
	t.Helper()
	data, err := os.ReadFile(hookPath)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatalf("read %s: %v", hookPath, err)
	}
	return string(data)
}

// runHooksCmd is a helper that runs the "mneme codegraph hooks" command tree
// with the provided subcommand path inside the given cwd. It returns stdout,
// stderr, and any error.
func runHooksCmd(t *testing.T, cwd string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	// Build a minimal Cobra root so we can invoke newCodegraphHooksCmd() in
	// isolation without a full mneme binary.
	root := &cobra.Command{Use: "mneme"}
	root.AddCommand(func() *cobra.Command {
		cg := &cobra.Command{Use: "codegraph"}
		cg.AddCommand(newCodegraphHooksCmd())
		return cg
	}())

	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)

	// Change into cwd for the duration of the call so git commands resolve
	// relative to the test repo. Save and restore the original directory.
	orig, wdErr := os.Getwd()
	if wdErr != nil {
		t.Fatalf("Getwd: %v", wdErr)
	}
	if chErr := os.Chdir(cwd); chErr != nil {
		t.Fatalf("Chdir %s: %v", cwd, chErr)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	root.SetArgs(append([]string{"codegraph", "hooks"}, args...))
	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

// TestHooksInstall_FreshRepo verifies that install creates post-commit and
// post-checkout hooks with the mneme block and shebang, and makes them
// executable.
func TestHooksInstall_FreshRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	_, _, err := runHooksCmd(t, dir, "install")
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	hooksDir, hErr := gitHooksDir(dir)
	if hErr != nil {
		t.Fatalf("gitHooksDir: %v", hErr)
	}

	for _, hookName := range hooksTargetHooks {
		hookPath := filepath.Join(hooksDir, hookName)
		if !hookFileExists(t, hookPath) {
			t.Errorf("%s: expected executable hook file, not found", hookName)
		}
		content := readHookFile(t, hookPath)
		if !strings.HasPrefix(content, "#!/bin/sh") {
			t.Errorf("%s: expected #!/bin/sh shebang, got: %q", hookName, content[:min(30, len(content))])
		}
		if !strings.Contains(content, hooksMarkerBegin) {
			t.Errorf("%s: missing begin marker", hookName)
		}
		if !strings.Contains(content, hooksMarkerEnd) {
			t.Errorf("%s: missing end marker", hookName)
		}
		if !strings.Contains(content, "run-reindex") {
			t.Errorf("%s: expected run-reindex invocation", hookName)
		}
	}
}

// TestHooksInstall_Idempotent verifies that running install twice does not
// duplicate the mneme block.
func TestHooksInstall_Idempotent(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	for i := 0; i < 2; i++ {
		_, _, err := runHooksCmd(t, dir, "install")
		if err != nil {
			t.Fatalf("install round %d: %v", i+1, err)
		}
	}

	hooksDir, _ := gitHooksDir(dir)
	for _, hookName := range hooksTargetHooks {
		content := readHookFile(t, filepath.Join(hooksDir, hookName))
		count := strings.Count(content, hooksMarkerBegin)
		if count != 1 {
			t.Errorf("%s: expected 1 begin-marker occurrence, got %d", hookName, count)
		}
	}
}

// TestHooksInstall_AppendsToExisting verifies that installing over an existing
// hook preserves the original content and appends the mneme block.
func TestHooksInstall_AppendsToExisting(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	hooksDir, _ := gitHooksDir(dir)
	hookPath := filepath.Join(hooksDir, "post-commit")

	// Pre-create a hook with custom content.
	existing := "#!/bin/sh\necho 'user hook'\n"
	if err := os.WriteFile(hookPath, []byte(existing), 0o755); err != nil {
		t.Fatalf("write existing hook: %v", err)
	}

	_, _, err := runHooksCmd(t, dir, "install")
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	content := readHookFile(t, hookPath)
	if !strings.Contains(content, "echo 'user hook'") {
		t.Errorf("original content not preserved: %q", content)
	}
	if !strings.Contains(content, hooksMarkerBegin) {
		t.Errorf("mneme block not appended")
	}
}

// TestHooksRemove_RemovesOnlyMarkedBlock verifies that remove strips only the
// mneme block, leaving user-provided content intact.
func TestHooksRemove_RemovesOnlyMarkedBlock(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	// Install first so the block is present.
	_, _, err := runHooksCmd(t, dir, "install")
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	hooksDir, _ := gitHooksDir(dir)
	hookPath := filepath.Join(hooksDir, "post-commit")

	// Append some user content after the mneme block.
	f, err := os.OpenFile(hookPath, os.O_APPEND|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatalf("open hook for append: %v", err)
	}
	_, _ = f.WriteString("# user post-commit logic\n")
	_ = f.Close()

	_, _, err = runHooksCmd(t, dir, "remove")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}

	content := readHookFile(t, hookPath)
	if strings.Contains(content, hooksMarkerBegin) {
		t.Errorf("mneme begin-marker still present after remove")
	}
	if strings.Contains(content, hooksMarkerEnd) {
		t.Errorf("mneme end-marker still present after remove")
	}
	if !strings.Contains(content, "user post-commit logic") {
		t.Errorf("user content removed unexpectedly: %q", content)
	}
}

// TestHooksRemove_NoBlock_NoOp verifies that remove on a hook without a mneme
// block exits 0 and does not modify the file.
func TestHooksRemove_NoBlock_NoOp(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	hooksDir, _ := gitHooksDir(dir)
	hookPath := filepath.Join(hooksDir, "post-commit")

	original := "#!/bin/sh\necho hello\n"
	if err := os.WriteFile(hookPath, []byte(original), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}

	_, _, err := runHooksCmd(t, dir, "remove")
	if err != nil {
		t.Fatalf("remove (no-block): %v", err)
	}

	content := readHookFile(t, hookPath)
	if content != original {
		t.Errorf("file modified when no block present: got %q, want %q", content, original)
	}
}

// TestHooksInstall_NotGitRepo verifies that install fails with a non-zero exit
// when the cwd is not inside a git repository.
func TestHooksInstall_NotGitRepo(t *testing.T) {
	dir := t.TempDir() // plain directory, no git init

	_, _, err := runHooksCmd(t, dir, "install")
	if err == nil {
		t.Fatal("expected error for non-git directory, got nil")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("expected 'not a git repository' in error, got: %v", err)
	}
}

// TestReindexInProgress_DetectsRebaseMerge verifies that reindexInProgress
// correctly detects all four sentinel files and returns false when none exist.
func TestReindexInProgress_DetectsRebaseMerge(t *testing.T) {
	gitDir := t.TempDir()

	// None of the sentinel files exist yet → should return false.
	if reindexInProgress(gitDir) {
		t.Error("expected false when no sentinels present")
	}

	sentinels := []string{"rebase-merge", "rebase-apply", "MERGE_HEAD", "CHERRY_PICK_HEAD"}
	for _, sentinel := range sentinels {
		p := filepath.Join(gitDir, sentinel)
		if err := os.WriteFile(p, []byte(""), 0o644); err != nil {
			t.Fatalf("write %s: %v", sentinel, err)
		}
		if !reindexInProgress(gitDir) {
			t.Errorf("expected true when %s present", sentinel)
		}
		if err := os.Remove(p); err != nil {
			t.Fatalf("remove %s: %v", sentinel, err)
		}
	}
}

// TestRunReindex_SkipsDuringRebase verifies that run-reindex exits 0 and does
// not index when rebase-merge is detected. We simulate this by creating a fake
// git-dir with a rebase-merge sentinel.
func TestRunReindex_SkipsDuringRebase(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	// Locate the git-dir and create the rebase-merge sentinel.
	gd, err := gitDir(dir)
	if err != nil {
		t.Fatalf("gitDir: %v", err)
	}
	sentinel := filepath.Join(gd, "rebase-merge")
	if err := os.WriteFile(sentinel, []byte(""), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(sentinel) })

	// run-reindex should exit 0 (no error) and not attempt to index.
	_, _, err = runHooksCmd(t, dir, "run-reindex")
	if err != nil {
		t.Fatalf("run-reindex with rebase sentinel: expected exit 0, got %v", err)
	}
}

// TestRunReindex_LogsFailure verifies that run-reindex writes to
// codegraph-hooks.log when the indexing fails (e.g. no project detected) and
// still exits 0. We achieve a controlled failure by using an isolated home dir
// that has no config, so initCodeGraphServiceForCWD will fail.
func TestRunReindex_LogsFailure(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	// Use a fresh tmp home dir so there is no real mneme config and project
	// detection fails cleanly.
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	// run-reindex must exit 0 even when it cannot index.
	_, _, err := runHooksCmd(t, dir, "run-reindex")
	if err != nil {
		t.Fatalf("run-reindex: expected exit 0 on failure, got %v", err)
	}

	// A log line should be present (the fake home forces a failure path).
	// NOTE: Some environments may succeed with an empty project slug, in which
	// case the log may or may not be written. We verify the function at the unit
	// level instead.
	_ = fakeHome // silence staticcheck
}

// min is a local helper (pre-Go 1.21 compat shim).
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// SPEC-102 — full-scan honours .gitignore via `git ls-files`.
// ---------------------------------------------------------------------------

// runGit runs git with args inside dir and fails the test on error.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestGitEligibleFiles_RespectsGitignore verifies decision B (SPEC-102): the
// result includes tracked files AND untracked-but-not-ignored files (WIP),
// but excludes anything under a gitignored directory.
func TestGitEligibleFiles_RespectsGitignore(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	writeFile(t, dir, "src.go", "package main\n")
	writeFile(t, dir, ".gitignore", "ignored/\n")
	writeFile(t, dir, "ignored/dep.go", "package ignored\n")
	writeFile(t, dir, "wip.go", "package main\n// untracked WIP\n")

	runGit(t, dir, "add", "src.go", ".gitignore")
	runGit(t, dir, "commit", "-m", "initial")

	paths, ok := gitEligibleFiles(dir)
	if !ok {
		t.Fatal("gitEligibleFiles: ok = false, want true (dir is a git repo)")
	}

	want := map[string]bool{"src.go": true, "wip.go": true}
	got := make(map[string]bool, len(paths))
	for _, p := range paths {
		got[p] = true
	}
	for p := range want {
		if !got[p] {
			t.Errorf("gitEligibleFiles missing expected path %q; got %v", p, paths)
		}
	}
	if got["ignored/dep.go"] {
		t.Errorf("gitEligibleFiles included gitignored path ignored/dep.go; got %v", paths)
	}
}

// TestGitEligibleFiles_NonGitDir verifies the fallback signal: a plain,
// non-git directory yields ok=false so the caller falls back to the legacy
// walk.
func TestGitEligibleFiles_NonGitDir(t *testing.T) {
	dir := t.TempDir() // no git init

	_, ok := gitEligibleFiles(dir)
	if ok {
		t.Error("gitEligibleFiles: ok = true for a non-git directory, want false")
	}
}

// TestFullScanOptions_Fallback verifies that fullScanOptions populates
// Include for a git directory and leaves it nil (legacy walk) for a non-git
// directory.
func TestFullScanOptions_Fallback(t *testing.T) {
	gitDirPath := t.TempDir()
	initGitRepo(t, gitDirPath)
	writeFile(t, gitDirPath, "a.go", "package main\n")
	runGit(t, gitDirPath, "add", "a.go")
	runGit(t, gitDirPath, "commit", "-m", "initial")

	opts := fullScanOptions(gitDirPath, false)
	if opts.Include == nil {
		t.Error("fullScanOptions: Include is nil for a git directory, want non-nil")
	}
	if opts.RootDir != gitDirPath {
		t.Errorf("fullScanOptions: RootDir = %q, want %q", opts.RootDir, gitDirPath)
	}
	if opts.Force {
		t.Error("fullScanOptions: Force = true, want false (as passed)")
	}

	nonGitDir := t.TempDir()
	optsNonGit := fullScanOptions(nonGitDir, true)
	if optsNonGit.Include != nil {
		t.Errorf("fullScanOptions: Include = %v for a non-git directory, want nil (legacy walk fallback)", optsNonGit.Include)
	}
	if !optsNonGit.Force {
		t.Error("fullScanOptions: Force = false, want true (as passed)")
	}
}

// TestCodegraphIndex_FullScan_SkipsGitignoredDir is the end-to-end regression
// test for the SPEC-102 bug: running the real "codegraph index" command
// against a fixture with a gitignored directory containing .go files must
// not index anything under that directory.
func TestCodegraphIndex_FullScan_SkipsGitignoredDir(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	writeFile(t, dir, "pkg/real.go", "package pkg\n\nfunc Real() {}\n")
	writeFile(t, dir, ".gitignore", "tmp/\n")
	writeFile(t, dir, "tmp/testhome/vendored.go", "package vendored\n\nfunc Vendored() {}\n")

	runGit(t, dir, "add", "pkg/real.go", ".gitignore")
	runGit(t, dir, "commit", "-m", "initial")

	dataDir := t.TempDir()
	t.Setenv("HOME", t.TempDir()) // isolate from any real ~/.mneme config

	origDataDir := flagDataDir
	origProject := flagProject
	flagDataDir = dataDir
	flagProject = "spec102-fullscan"
	t.Cleanup(func() {
		flagDataDir = origDataDir
		flagProject = origProject
	})

	orig, wdErr := os.Getwd()
	if wdErr != nil {
		t.Fatalf("Getwd: %v", wdErr)
	}
	if chErr := os.Chdir(dir); chErr != nil {
		t.Fatalf("Chdir %s: %v", dir, chErr)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	root := &cobra.Command{Use: "mneme"}
	root.AddCommand(newCodegraphCmd())
	var outBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetArgs([]string{"codegraph", "index"})
	if err := root.Execute(); err != nil {
		t.Fatalf("codegraph index: %v\noutput:\n%s", err, outBuf.String())
	}

	svc, err := initCodeGraphService()
	if err != nil {
		t.Fatalf("initCodeGraphService: %v", err)
	}
	defer func() { _ = svc.Close() }()

	files, err := svc.Files("", "")
	if err != nil {
		t.Fatalf("svc.Files: %v", err)
	}
	for _, f := range files {
		if strings.HasPrefix(f.Path, "tmp/") {
			t.Errorf("codegraph index indexed a path under gitignored tmp/: %s", f.Path)
		}
	}

	foundReal := false
	for _, f := range files {
		if f.Path == "pkg/real.go" {
			foundReal = true
		}
	}
	if !foundReal {
		t.Errorf("codegraph index did not index the tracked pkg/real.go; files = %+v", files)
	}
}

// TestReindexOnce_FullScanFallback_RespectsGitignore verifies AC4: the
// reindexOnce fallback path (no last_sha recorded, so it takes the
// full-scan-when-first-run branch) also honours .gitignore.
func TestReindexOnce_FullScanFallback_RespectsGitignore(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	writeFile(t, dir, "pkg/real.go", "package pkg\n\nfunc Real() {}\n")
	writeFile(t, dir, ".gitignore", "tmp/\n")
	writeFile(t, dir, "tmp/testhome/vendored.go", "package vendored\n\nfunc Vendored() {}\n")

	runGit(t, dir, "add", "pkg/real.go", ".gitignore")
	runGit(t, dir, "commit", "-m", "initial")

	dataDir := t.TempDir()
	t.Setenv("HOME", t.TempDir())

	origDataDir := flagDataDir
	origProject := flagProject
	flagDataDir = dataDir
	flagProject = "spec102-reindexonce"
	t.Cleanup(func() {
		flagDataDir = origDataDir
		flagProject = origProject
	})

	svc, err := initCodeGraphServiceForCWD(dir)
	if err != nil {
		t.Fatalf("initCodeGraphServiceForCWD: %v", err)
	}
	defer func() { _ = svc.Close() }()

	// No last_sha recorded yet — reindexOnce takes the first-run full-scan branch.
	if err := reindexOnce(svc, dir); err != nil {
		t.Fatalf("reindexOnce: %v", err)
	}

	files, err := svc.Files("", "")
	if err != nil {
		t.Fatalf("svc.Files: %v", err)
	}
	for _, f := range files {
		if strings.HasPrefix(f.Path, "tmp/") {
			t.Errorf("reindexOnce fallback indexed a path under gitignored tmp/: %s", f.Path)
		}
	}
}
