package gitident

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// newTempGitRepo creates a temporary git repository with the given user.name
// and user.email set locally, and chdirs the test process into it for the
// duration of the test (restored via t.Cleanup). Pass "" for either value to
// leave it unconfigured.
//
// This never touches the real mneme repository or any global git config —
// `git config` (without --global) writes to <repo>/.git/config only.
func newTempGitRepo(t *testing.T, name, email string) string {
	t.Helper()

	// Isolate from the developer's real global/system git config so these
	// tests are deterministic regardless of the machine they run on — only
	// the repo-local config set below should ever be visible to gitConfig.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "nonexistent-gitconfig"))

	dir := t.TempDir()
	runGit(t, dir, "init")
	if name != "" {
		runGit(t, dir, "config", "user.name", name)
	}
	if email != "" {
		runGit(t, dir, "config", "user.email", email)
	}

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestAuthor_NameAndEmail(t *testing.T) {
	newTempGitRepo(t, "Ada Lovelace", "ada@example.com")
	Reset()
	t.Cleanup(Reset)

	got := Author()
	want := "Ada Lovelace <ada@example.com>"
	if got != want {
		t.Errorf("Author() = %q, want %q", got, want)
	}
}

func TestAuthor_NameOnly(t *testing.T) {
	newTempGitRepo(t, "Ada Lovelace", "")
	Reset()
	t.Cleanup(Reset)

	got := Author()
	want := "Ada Lovelace"
	if got != want {
		t.Errorf("Author() = %q, want %q", got, want)
	}
}

func TestAuthor_EmailOnly(t *testing.T) {
	newTempGitRepo(t, "", "ada@example.com")
	Reset()
	t.Cleanup(Reset)

	got := Author()
	want := "<ada@example.com>"
	if got != want {
		t.Errorf("Author() = %q, want %q", got, want)
	}
}

func TestAuthor_Cached(t *testing.T) {
	dir := newTempGitRepo(t, "Ada Lovelace", "ada@example.com")
	Reset()
	t.Cleanup(Reset)

	first := Author()

	// Change the local git identity after the first call — the cached
	// result must not change within the same process, proving the
	// sync.Once memoization documented on Author().
	runGit(t, dir, "config", "user.name", "Grace Hopper")

	second := Author()
	if second != first {
		t.Errorf("Author() changed after cache should have been populated: first=%q second=%q", first, second)
	}
}

func TestAuthor_NotConfigured(t *testing.T) {
	// A repo with no user.name/user.email at all (and no --global fallback
	// possible in a hermetic test environment is not guaranteed, so this test
	// only asserts the format degrades gracefully when explicitly unset via
	// an empty HOME/XDG environment is out of scope). We simulate "not
	// configured" by pointing at a directory that is not a git repository at
	// all, which is the other real-world trigger for gitConfig returning "".
	dir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	Reset()
	t.Cleanup(Reset)

	// Isolate from the developer's real global git config so this test is
	// deterministic in any environment.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "nonexistent-gitconfig"))

	got := Author()
	if got != "" {
		t.Errorf("Author() = %q, want empty string when git identity is not configured", got)
	}
}
