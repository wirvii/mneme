package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stubUpgradeDataDir installs a fake upgradeDataDir returning dir, restoring
// the original via t.Cleanup, so no test ever resolves the real ~/.mneme.
func stubUpgradeDataDir(t *testing.T, dir string) {
	t.Helper()
	original := upgradeDataDir
	upgradeDataDir = func() string { return dir }
	t.Cleanup(func() { upgradeDataDir = original })
}

// legacyProfileFixture is the set of traces the retired team profiles left:
// a repo with the pin, the lock and a backups directory, plus a data dir with
// a profile checkout.
type legacyProfileFixture struct {
	repoRoot string
	dataDir  string
}

func (f legacyProfileFixture) pin() string { return filepath.Join(f.repoRoot, ".mneme-profile") }
func (f legacyProfileFixture) lock() string {
	return filepath.Join(f.repoRoot, ".mneme", "profile.lock")
}
func (f legacyProfileFixture) backup() string {
	return filepath.Join(f.repoRoot, ".mneme", "backups", "x")
}
func (f legacyProfileFixture) store() string { return filepath.Join(f.dataDir, "profiles") }

// newLegacyProfileFixture builds a committed git repo carrying the retired
// profile traces and stubs both seams to point at it.
func newLegacyProfileFixture(t *testing.T) legacyProfileFixture {
	t.Helper()
	f := legacyProfileFixture{repoRoot: t.TempDir(), dataDir: t.TempDir()}
	gitInitUpgradeTestRepo(t, f.repoRoot)

	write := func(path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(f.pin())
	write(f.lock())
	write(f.backup())
	write(filepath.Join(f.store(), "acme", "mneme-profile.toml"))

	stubUpgradeRepoRoot(t, f.repoRoot, nil)
	stubUpgradeDataDir(t, f.dataDir)
	return f
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func setDevVersion(t *testing.T) {
	t.Helper()
	orig := Version
	t.Cleanup(func() { Version = orig })
	Version = "dev"
}

// TestRunUpgrade_LegacyProfile_YesRemoves covers SPEC-162 C5(a): "y" removes
// the pin, the lock and the host store, and leaves .mneme/backups/ alone.
func TestRunUpgrade_LegacyProfile_YesRemoves(t *testing.T) {
	setDevVersion(t)
	f := newLegacyProfileFixture(t)

	var w bytes.Buffer
	_ = runUpgrade(&w, strings.NewReader("y\n"), false)

	for _, gone := range []string{f.pin(), f.lock(), f.store()} {
		if pathExists(gone) {
			t.Errorf("%s still exists after a 'y' answer", gone)
		}
	}
	if !pathExists(f.backup()) {
		t.Error(".mneme/backups/x must never be deleted")
	}
	if !strings.Contains(w.String(), "revísalo a mano") {
		t.Errorf("expected the backups hint, got: %s", w.String())
	}
}

// TestRunUpgrade_LegacyProfile_NoOrEOFKeeps covers C5(b): "n", an empty line
// and EOF delete nothing and print the manual rm commands.
func TestRunUpgrade_LegacyProfile_NoOrEOFKeeps(t *testing.T) {
	setDevVersion(t)
	tests := []struct {
		name  string
		input string
	}{
		{"explicit n", "n\n"},
		{"empty line", "\n"},
		{"EOF (no input at all)", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newLegacyProfileFixture(t)

			var w bytes.Buffer
			_ = runUpgrade(&w, strings.NewReader(tt.input), false)

			for _, kept := range []string{f.pin(), f.lock(), f.store(), f.backup()} {
				if !pathExists(kept) {
					t.Errorf("%s missing after a %q answer, want it untouched", kept, tt.input)
				}
			}
			if !strings.Contains(w.String(), "rm ") {
				t.Errorf("expected a manual-removal hint, got: %s", w.String())
			}
		})
	}
}

// TestRunUpgrade_LegacyProfile_CheckOnlyLists covers C5(c): --check lists and
// never prompts nor deletes.
func TestRunUpgrade_LegacyProfile_CheckOnlyLists(t *testing.T) {
	setDevVersion(t)
	f := newLegacyProfileFixture(t)

	var w bytes.Buffer
	_ = runUpgrade(&w, strings.NewReader(""), true)

	if strings.Contains(w.String(), "Remove them?") {
		t.Errorf("--check must never prompt, got: %s", w.String())
	}
	if !strings.Contains(w.String(), ".mneme-profile") || !strings.Contains(w.String(), f.store()) {
		t.Errorf("expected --check to list the pin and the store, got: %s", w.String())
	}
	for _, kept := range []string{f.pin(), f.lock(), f.store(), f.backup()} {
		if !pathExists(kept) {
			t.Errorf("%s missing after --check", kept)
		}
	}
}

// TestRunUpgrade_LegacyProfile_RunsBeforeDevRefusal covers C5(d).
func TestRunUpgrade_LegacyProfile_RunsBeforeDevRefusal(t *testing.T) {
	setDevVersion(t)
	f := newLegacyProfileFixture(t)

	var w bytes.Buffer
	err := runUpgrade(&w, strings.NewReader("y\n"), false)
	if err == nil || !strings.Contains(err.Error(), "cannot upgrade a development build") {
		t.Fatalf("runUpgrade error = %v, want the dev-build refusal", err)
	}
	if pathExists(f.pin()) {
		t.Error("the cleanup must run BEFORE the dev-build refusal")
	}
}

// TestRunUpgrade_LegacyProfile_NeverTouchesGitIndex covers C5(e): the removed
// pin shows up as an unstaged deletion; nothing is staged and HEAD stays.
func TestRunUpgrade_LegacyProfile_NeverTouchesGitIndex(t *testing.T) {
	setDevVersion(t)
	f := newLegacyProfileFixture(t)

	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = f.repoRoot
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=upgrade-test", "GIT_AUTHOR_EMAIL=upgrade-test@example.com",
			"GIT_COMMITTER_NAME=upgrade-test", "GIT_COMMITTER_EMAIL=upgrade-test@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	runGit("add", ".")
	runGit("commit", "-m", "confirm legacy profile files")
	headBefore := strings.TrimSpace(runGit("rev-parse", "HEAD"))

	var w bytes.Buffer
	_ = runUpgrade(&w, strings.NewReader("y\n"), false)

	if headAfter := strings.TrimSpace(runGit("rev-parse", "HEAD")); headAfter != headBefore {
		t.Errorf("HEAD moved from %s to %s — the cleanup must never commit", headBefore, headAfter)
	}
	diffCached := exec.Command("git", "diff", "--cached", "--quiet")
	diffCached.Dir = f.repoRoot
	if err := diffCached.Run(); err != nil {
		t.Errorf("something was staged: %v", err)
	}
	if status := runGit("status", "--porcelain"); !strings.Contains(status, " D .mneme-profile") {
		t.Errorf("git status --porcelain does not show .mneme-profile as an unstaged deletion:\n%s", status)
	}
}

// TestRunUpgrade_LegacyProfile_OutsideGitStillOffersHostPart covers the
// "repo outside git" clause: the host store is still offered and removed.
func TestRunUpgrade_LegacyProfile_OutsideGitStillOffersHostPart(t *testing.T) {
	setDevVersion(t)
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "profiles", "acme"), 0o755); err != nil {
		t.Fatal(err)
	}
	stubUpgradeRepoRoot(t, "", os.ErrNotExist)
	stubUpgradeDataDir(t, dataDir)

	var w bytes.Buffer
	_ = runUpgrade(&w, strings.NewReader("y\n"), false)

	if pathExists(filepath.Join(dataDir, "profiles")) {
		t.Error("the host store should have been removed even outside a git repository")
	}
}

// TestRunUpgrade_LegacyQualityAndProfile_SharedInput covers SPEC-162 C6: both
// cleanup steps read their answer from the same input without one losing the
// other's line.
func TestRunUpgrade_LegacyQualityAndProfile_SharedInput(t *testing.T) {
	setDevVersion(t)
	tests := []struct {
		name         string
		input        string
		wantQuality  bool
		wantProfiles bool
	}{
		{"yes then yes removes both", "y\ny\n", false, false},
		{"no then yes removes only the profile traces", "n\ny\n", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newLegacyProfileFixture(t)
			writeLegacyQualityFilesForUpgradeTest(t, f.repoRoot)

			var w bytes.Buffer
			_ = runUpgrade(&w, strings.NewReader(tt.input), false)

			qualityKept := pathExists(filepath.Join(f.repoRoot, ".mneme", "quality.toml"))
			if qualityKept != tt.wantQuality {
				t.Errorf("quality.toml exists = %v, want %v", qualityKept, tt.wantQuality)
			}
			if profilesKept := pathExists(f.pin()); profilesKept != tt.wantProfiles {
				t.Errorf(".mneme-profile exists = %v, want %v", profilesKept, tt.wantProfiles)
			}
		})
	}
}

// TestRootHasNoProfileCommands covers SPEC-162 C2: the retired commands are
// gone and the unrelated `subagents` command is still there.
func TestRootHasNoProfileCommands(t *testing.T) {
	retired := map[string]bool{"profile": true, "project": true, "app": true, "scaffold": true}
	sawSubagents := false
	for _, c := range NewRootCmd().Commands() {
		if retired[c.Name()] {
			t.Errorf("root still registers the retired %q command", c.Name())
		}
		if c.Name() == "subagents" {
			sawSubagents = true
		}
	}
	if !sawSubagents {
		t.Fatal("root has no `subagents` command — this test proves nothing without its positive anchor")
	}
}
