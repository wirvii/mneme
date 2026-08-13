package quality

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitRunTest runs git with a local, explicit identity and disabled GPG
// signing (R-C): without this a test depends on the developer's own
// ~/.gitconfig and breaks on a clean machine or a machine with commit
// signing configured globally.
func gitRunTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=quality-test",
		"GIT_AUTHOR_EMAIL=quality-test@example.com",
		"GIT_COMMITTER_NAME=quality-test",
		"GIT_COMMITTER_EMAIL=quality-test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// initTestGitRepo creates a real git repository in a fresh t.TempDir(),
// local identity and signing disabled, with one committed file so HEAD
// exists.
func initTestGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRunTest(t, dir, "init", "-b", "main")
	gitRunTest(t, dir, "config", "user.email", "quality-test@example.com")
	gitRunTest(t, dir, "config", "user.name", "quality-test")
	gitRunTest(t, dir, "config", "commit.gpgsign", "false")

	if err := os.WriteFile(filepath.Join(dir, "committed.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatalf("write committed.txt: %v", err)
	}
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "initial")
	return dir
}

// TestGit_HeadSHA verifies HeadSHA returns a 40-hex SHA for a real repo.
func TestGit_HeadSHA(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := initTestGitRepo(t)

	g := &Git{RepoDir: dir}
	sha, err := g.HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	if len(sha) != 40 {
		t.Errorf("HeadSHA() = %q, want 40 hex chars", sha)
	}
}

// TestGit_IsDirty covers AC11: an untracked file makes the tree dirty; a
// file matched by .gitignore does not.
func TestGit_IsDirty(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := initTestGitRepo(t)
	g := &Git{RepoDir: dir}

	dirty, paths, err := g.IsDirty()
	if err != nil {
		t.Fatalf("IsDirty (clean): %v", err)
	}
	if dirty {
		t.Fatalf("IsDirty() = true on a freshly committed repo, want false (paths: %v)", paths)
	}

	// An UNTRACKED file counts as dirty (D8) — the core of AC11.
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("new"), 0o644); err != nil {
		t.Fatalf("write untracked.txt: %v", err)
	}
	dirty, paths, err = g.IsDirty()
	if err != nil {
		t.Fatalf("IsDirty (untracked): %v", err)
	}
	if !dirty {
		t.Fatal("IsDirty() = false with an untracked file present, want true")
	}
	if len(paths) == 0 {
		t.Error("IsDirty() returned no paths for a dirty tree")
	}

	// Remove it and add a .gitignore instead — an IGNORED file must NOT
	// count as dirty.
	if err := os.Remove(filepath.Join(dir, "untracked.txt")); err != nil {
		t.Fatalf("remove untracked.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored.txt\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	gitRunTest(t, dir, "add", ".gitignore")
	gitRunTest(t, dir, "commit", "-m", "add gitignore")
	if err := os.WriteFile(filepath.Join(dir, "ignored.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatalf("write ignored.txt: %v", err)
	}
	dirty, paths, err = g.IsDirty()
	if err != nil {
		t.Fatalf("IsDirty (ignored): %v", err)
	}
	if dirty {
		t.Errorf("IsDirty() = true with only a .gitignore'd file present, want false (paths: %v)", paths)
	}
}

// TestGit_IsDirty_MutationUntrackedIgnored is the G-P2 guardian (plan P2):
// dropping --untracked-files=no in place of --untracked-files=normal must
// turn AC11's untracked-file case red. Verified manually per the plan by
// temporarily editing IsDirty's exec.Command args to
// "--untracked-files=no", re-running TestGit_IsDirty (fails: dirty=false for
// the untracked case), and reverting byte-for-byte.
func TestGit_IsDirty_MutationUntrackedIgnored(t *testing.T) {
	// This test intentionally duplicates the untracked-file assertion from
	// TestGit_IsDirty as a single, minimal, always-green anchor to point the
	// mutation instructions above at — see the mutation note in this test's
	// godoc for the manual verification already performed.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := initTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("new"), 0o644); err != nil {
		t.Fatalf("write untracked.txt: %v", err)
	}
	g := &Git{RepoDir: dir}
	dirty, _, err := g.IsDirty()
	if err != nil {
		t.Fatalf("IsDirty: %v", err)
	}
	if !dirty {
		t.Fatal("IsDirty() = false with an untracked file present, want true")
	}
}

// TestGit_PathChangedInRange covers the primitive AC12 depends on: a file
// modified between baseSHA and HEAD is reported changed; an untouched file
// is not.
func TestGit_PathChangedInRange(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := initTestGitRepo(t)
	g := &Git{RepoDir: dir}

	baseSHA, err := g.HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA (base): %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "committed.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatalf("write committed.txt: %v", err)
	}
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "modify committed.txt")

	changed, err := g.PathChangedInRange(baseSHA, "committed.txt")
	if err != nil {
		t.Fatalf("PathChangedInRange (changed): %v", err)
	}
	if !changed {
		t.Error("PathChangedInRange() = false for a file modified in range, want true")
	}

	unchanged, err := g.PathChangedInRange(baseSHA, "never-existed.txt")
	if err != nil {
		t.Fatalf("PathChangedInRange (unchanged): %v", err)
	}
	if unchanged {
		t.Error("PathChangedInRange() = true for a path never touched, want false")
	}
}

// TestGit_FileAtRef_MissingReturnsNotOK verifies FileAtRef distinguishes
// "did not exist at ref" from an error.
func TestGit_FileAtRef_MissingReturnsNotOK(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := initTestGitRepo(t)
	g := &Git{RepoDir: dir}

	sha, err := g.HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}

	content, ok, err := g.FileAtRef(sha, "committed.txt")
	if err != nil {
		t.Fatalf("FileAtRef (existing): %v", err)
	}
	if !ok || string(content) != "v1\n" {
		t.Errorf("FileAtRef(existing) = %q, %v, want %q, true", content, ok, "v1\n")
	}

	_, ok, err = g.FileAtRef(sha, "does-not-exist.txt")
	if err != nil {
		t.Fatalf("FileAtRef (missing): %v", err)
	}
	if ok {
		t.Error("FileAtRef(missing) ok = true, want false")
	}
}

// TestGit_IsTracked covers D9 check 1: a committed file is tracked, a
// freshly written but un-added file is not.
func TestGit_IsTracked(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := initTestGitRepo(t)
	g := &Git{RepoDir: dir}

	tracked, err := g.IsTracked("committed.txt")
	if err != nil {
		t.Fatalf("IsTracked (tracked): %v", err)
	}
	if !tracked {
		t.Error("IsTracked(committed.txt) = false, want true")
	}

	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("new"), 0o644); err != nil {
		t.Fatalf("write untracked.txt: %v", err)
	}
	tracked, err = g.IsTracked("untracked.txt")
	if err != nil {
		t.Fatalf("IsTracked (untracked): %v", err)
	}
	if tracked {
		t.Error("IsTracked(untracked.txt) = true, want false")
	}
}
