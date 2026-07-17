package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/profile"
	"github.com/wirvii/mneme/internal/subagents"
)

// TestRemoveArtifact_AgentOtherErrorReturnsErr verifies that removeArtifact
// propagates an os.Remove failure that is NOT os.IsNotExist (the only
// swallowed case) — here, the agent path's parent is a regular file, so
// os.Remove fails with ENOTDIR, not ENOENT.
func TestRemoveArtifact_AgentOtherErrorReturnsErr(t *testing.T) {
	base := t.TempDir()
	blocker := filepath.Join(base, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}

	err := removeArtifact(profile.LockArtifact{Kind: "agent", Path: filepath.Join(blocker, "backend.md")})
	if err == nil {
		t.Fatal("expected an error removing an agent path whose parent is not a directory")
	}
}

// TestRemoveArtifact_SkillOtherErrorReturnsErr verifies that removeArtifact
// propagates an os.RemoveAll failure for the "skill" kind.
func TestRemoveArtifact_SkillOtherErrorReturnsErr(t *testing.T) {
	base := t.TempDir()
	blocker := filepath.Join(base, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}

	err := removeArtifact(profile.LockArtifact{Kind: "skill", Path: filepath.Join(blocker, "sub")})
	if err == nil {
		t.Fatal("expected an error removing a skill directory whose parent is not a directory")
	}
}

// TestRemoveArtifact_BlockOtherErrorReturnsErr verifies that removeArtifact
// propagates a managedblock.Remove failure for the "block" kind (here: the
// path is itself a directory, so the underlying os.ReadFile fails).
func TestRemoveArtifact_BlockOtherErrorReturnsErr(t *testing.T) {
	dir := t.TempDir()
	err := removeArtifact(profile.LockArtifact{Kind: "block", Path: dir, Marker: "profile"})
	if err == nil {
		t.Fatal("expected an error removing a block whose path is a directory")
	}
}

// TestRemoveArtifact_UnknownKindReturnsErr verifies removeArtifact's default
// case for a Kind it does not recognise.
func TestRemoveArtifact_UnknownKindReturnsErr(t *testing.T) {
	err := removeArtifact(profile.LockArtifact{Kind: "bogus", Path: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "unknown artifact kind") {
		t.Fatalf("expected an unknown-artifact-kind error, got %v", err)
	}
}

// TestWriteLock_MkdirAllError verifies that writeLock surfaces an
// os.MkdirAll failure (here: .mneme already exists as a regular file).
func TestWriteLock_MkdirAllError(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoRoot, ".mneme"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write .mneme blocker: %v", err)
	}

	err := writeLock(repoRoot, profile.Lock{SchemaVersion: profile.LockSchemaVersion, Profile: "acme"})
	if err == nil {
		t.Fatal("expected an error when .mneme exists as a regular file")
	}
}

// TestWriteLock_WriteTempFileError verifies that writeLock surfaces an
// os.WriteFile failure writing the temp lock (here: the temp path is
// pre-occupied by a directory).
func TestWriteLock_WriteTempFileError(t *testing.T) {
	repoRoot := t.TempDir()
	tmpPath := profile.LockPath(repoRoot) + ".tmp"
	if err := os.MkdirAll(tmpPath, 0o755); err != nil {
		t.Fatalf("mkdir temp lock path as directory: %v", err)
	}

	err := writeLock(repoRoot, profile.Lock{SchemaVersion: profile.LockSchemaVersion, Profile: "acme"})
	if err == nil {
		t.Fatal("expected an error when the temp lock path is pre-occupied by a directory")
	}
}

// TestWriteLock_RenameError verifies that writeLock surfaces an os.Rename
// failure (here: the final lock path is pre-occupied by a non-empty
// directory, which cannot be replaced by a plain file rename).
func TestWriteLock_RenameError(t *testing.T) {
	repoRoot := t.TempDir()
	lockPath := profile.LockPath(repoRoot)
	if err := os.MkdirAll(lockPath, 0o755); err != nil {
		t.Fatalf("mkdir lock path as directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lockPath, "occupant"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write occupant: %v", err)
	}

	err := writeLock(repoRoot, profile.Lock{SchemaVersion: profile.LockSchemaVersion, Profile: "acme"})
	if err == nil {
		t.Fatal("expected an error when the final lock path is a non-empty directory")
	}
}

// TestCopyDir_SourceMissingReturnsErr verifies that copyDir propagates the
// error filepath.WalkDir reports for a nonexistent source root.
func TestCopyDir_SourceMissingReturnsErr(t *testing.T) {
	err := copyDir(filepath.Join(t.TempDir(), "does-not-exist"), t.TempDir())
	if err == nil {
		t.Fatal("expected an error copying a nonexistent source directory")
	}
}

// TestCopyDir_ReadFileErrorViaBrokenSymlink verifies that copyDir propagates
// an os.ReadFile failure for a broken symlink inside the source tree.
func TestCopyDir_ReadFileErrorViaBrokenSymlink(t *testing.T) {
	src := t.TempDir()
	link := filepath.Join(src, "broken")
	if err := os.Symlink(filepath.Join(src, "does-not-exist-target"), link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := copyDir(src, t.TempDir()); err == nil {
		t.Fatal("expected an error copying a source directory containing a broken symlink")
	}
}

// TestCopyDir_WriteFileError verifies that copyDir propagates an
// os.WriteFile failure (here: the destination path is pre-occupied by a
// directory).
func TestCopyDir_WriteFileError(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	dst := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dst, "a.txt"), 0o755); err != nil {
		t.Fatalf("pre-occupy destination with a directory: %v", err)
	}

	if err := copyDir(src, dst); err == nil {
		t.Fatal("expected an error writing over a destination path that is a directory")
	}
}

// TestFuseAgent_EmptyProjectProfileDegradesToLayer1Alone verifies that
// fuseAgent degrades to layer1 unchanged when pp is non-nil but carries
// nothing relevant to role — the "no repo facts, no matching areas" branch
// distinct from the pp == nil branch already covered by
// TestFuseAgent_DegradesCleanlyWithoutProjectProfile.
func TestFuseAgent_EmptyProjectProfileDegradesToLayer1Alone(t *testing.T) {
	svc := NewProfileService(t.TempDir(), false)

	got, err := svc.fuseAgent([]byte("layer1 content"), "backend", &ProjectProfile{})
	if err != nil {
		t.Fatalf("fuseAgent: %v", err)
	}
	if got != "layer1 content" {
		t.Errorf("expected fuseAgent to degrade to layer1 unchanged for an empty ProjectProfile, got %q", got)
	}
}

// TestRenderProfileFusionSection_AllFieldsRendered exercises every optional
// field renderProfileFusionSection renders (Lang, Layout, CrossRules) beyond
// the subset TestFuseAgent_FusesWhenProjectProfileExists already covers
// (Org, Commits, one mapped area).
func TestRenderProfileFusionSection_AllFieldsRendered(t *testing.T) {
	pp := ProjectProfile{
		Org: "acme-corp",
		Repo: ProjectProfileRepo{
			Commits:    "Conventional Commits",
			Lang:       "Go 1.25",
			Layout:     "Clean Architecture",
			CrossRules: []string{"lint clean", "85% coverage"},
		},
		Mapping: []ProjectProfileMapping{{App: "apps/core", Role: subagents.Role("backend")}},
	}

	got := renderProfileFusionSection(pp, "backend")

	for _, want := range []string{
		"acme-corp", "Conventional Commits", "Go 1.25", "Clean Architecture",
		"lint clean", "85% coverage", "apps/core",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected rendered section to contain %q, got %q", want, got)
		}
	}
}

// TestEnsureLockGitignore_CreatesFileWithEntry verifies that
// ensureLockGitignore creates <repoRoot>/.mneme/.gitignore containing
// exactly lockGitignoreEntry when none existed yet.
func TestEnsureLockGitignore_CreatesFileWithEntry(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, ".mneme"), 0o755); err != nil {
		t.Fatalf("mkdir .mneme: %v", err)
	}

	if err := ensureLockGitignore(repoRoot); err != nil {
		t.Fatalf("ensureLockGitignore: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(repoRoot, ".mneme", ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if strings.TrimSpace(string(data)) != lockGitignoreEntry {
		t.Errorf("expected .gitignore to contain only %q, got %q", lockGitignoreEntry, string(data))
	}
}

// TestEnsureLockGitignore_IdempotentAndPreservesExistingContent verifies
// that calling ensureLockGitignore twice against a .gitignore with
// pre-existing, hand-authored content never duplicates the entry and never
// disturbs what was already there.
func TestEnsureLockGitignore_IdempotentAndPreservesExistingContent(t *testing.T) {
	repoRoot := t.TempDir()
	mnemeDir := filepath.Join(repoRoot, ".mneme")
	if err := os.MkdirAll(mnemeDir, 0o755); err != nil {
		t.Fatalf("mkdir .mneme: %v", err)
	}
	gitignorePath := filepath.Join(mnemeDir, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte("# hand-authored note\nsome-other-entry\n"), 0o644); err != nil {
		t.Fatalf("seed .gitignore: %v", err)
	}

	if err := ensureLockGitignore(repoRoot); err != nil {
		t.Fatalf("ensureLockGitignore (first call): %v", err)
	}
	if err := ensureLockGitignore(repoRoot); err != nil {
		t.Fatalf("ensureLockGitignore (second call): %v", err)
	}

	data, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "some-other-entry") {
		t.Errorf("expected pre-existing entry preserved, got %q", got)
	}
	if strings.Count(got, lockGitignoreEntry) != 1 {
		t.Errorf("expected exactly one %q entry after two calls, got %q", lockGitignoreEntry, got)
	}
}

// TestEnsureLockGitignore_ReadErrorPropagates verifies that a read failure
// (here: the .gitignore path is itself a directory) is surfaced, not
// silently swallowed.
func TestEnsureLockGitignore_ReadErrorPropagates(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, ".mneme", ".gitignore"), 0o755); err != nil {
		t.Fatalf("mkdir .gitignore as directory: %v", err)
	}

	if err := ensureLockGitignore(repoRoot); err == nil {
		t.Fatal("expected an error when the .gitignore path is a directory")
	}
}

// TestEnsureLockGitignore_WriteErrorPropagates verifies that a write
// failure (here: a read-only .mneme directory) is surfaced. Skipped when
// running as root, since permission checks are bypassed for root and the
// injected failure would never occur.
func TestEnsureLockGitignore_WriteErrorPropagates(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission-based write-failure injection does not apply")
	}

	repoRoot := t.TempDir()
	mnemeDir := filepath.Join(repoRoot, ".mneme")
	if err := os.MkdirAll(mnemeDir, 0o755); err != nil {
		t.Fatalf("mkdir .mneme: %v", err)
	}
	if err := os.Chmod(mnemeDir, 0o555); err != nil {
		t.Fatalf("chmod .mneme read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(mnemeDir, 0o755) })

	if err := ensureLockGitignore(repoRoot); err == nil {
		t.Fatal("expected an error writing .gitignore into a read-only .mneme directory")
	}
}
