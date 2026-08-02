package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/profile"
	"github.com/wirvii/mneme/internal/subagents"
)

// TestBackupDisplaced_NeverOverwrites (SPEC-105 DD12) verifies that two
// displacements landing on the exact same backup destination — same
// repoRoot, same at instant, same target — never collide: the second call
// receives a "-1" suffix, and the path it returns is the one actually used
// on disk, so the caller (materializeAgents/materializeSkills) never needs
// to reconstruct the name.
func TestBackupDisplaced_NeverOverwrites(t *testing.T) {
	repoRoot := t.TempDir()
	at := time.Date(2026, 8, 2, 19, 11, 3, 0, time.UTC)

	agentPath := filepath.Join(repoRoot, ".claude", "agents", "backend.md")
	if err := os.MkdirAll(filepath.Dir(agentPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(agentPath, []byte("version one"), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}

	first, err := backupDisplaced(repoRoot, at, agentPath)
	if err != nil {
		t.Fatalf("backupDisplaced (first): %v", err)
	}

	if err := os.WriteFile(agentPath, []byte("version two"), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	second, err := backupDisplaced(repoRoot, at, agentPath)
	if err != nil {
		t.Fatalf("backupDisplaced (second): %v", err)
	}

	if first == second {
		t.Fatalf("expected distinct backup paths, both were %q", first)
	}
	if !strings.HasSuffix(second, "-1.md") {
		t.Errorf("expected the second backup to carry a -1 suffix before the extension, got %q", second)
	}

	firstData, err := os.ReadFile(first)
	if err != nil {
		t.Fatalf("read first backup: %v", err)
	}
	if string(firstData) != "version one" {
		t.Errorf("first backup content: got %q, want %q", firstData, "version one")
	}
	secondData, err := os.ReadFile(second)
	if err != nil {
		t.Fatalf("read second backup: %v", err)
	}
	if string(secondData) != "version two" {
		t.Errorf("second backup content: got %q, want %q", secondData, "version two")
	}
}

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

// TestCopyFSDir_SourceMissingReturnsErr verifies that copyFSDir propagates
// the error fs.WalkDir reports for a nonexistent source root.
func TestCopyFSDir_SourceMissingReturnsErr(t *testing.T) {
	src := t.TempDir()
	err := copyFSDir(os.DirFS(src), "does-not-exist", t.TempDir())
	if err == nil {
		t.Fatal("expected an error copying a nonexistent source directory")
	}
}

// TestCopyFSDir_ReadFileErrorViaBrokenSymlink verifies that copyFSDir
// propagates an fs.ReadFile failure for a broken symlink inside the source
// tree.
func TestCopyFSDir_ReadFileErrorViaBrokenSymlink(t *testing.T) {
	src := t.TempDir()
	link := filepath.Join(src, "broken")
	if err := os.Symlink(filepath.Join(src, "does-not-exist-target"), link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := copyFSDir(os.DirFS(src), ".", t.TempDir()); err == nil {
		t.Fatal("expected an error copying a source directory containing a broken symlink")
	}
}

// TestCopyFSDir_WriteFileError verifies that copyFSDir propagates an
// os.WriteFile failure (here: the destination path is pre-occupied by a
// directory).
func TestCopyFSDir_WriteFileError(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	dst := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dst, "a.txt"), 0o755); err != nil {
		t.Fatalf("pre-occupy destination with a directory: %v", err)
	}

	if err := copyFSDir(os.DirFS(src), ".", dst); err == nil {
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

// TestEnsureMnemeGitignore_CreatesFileWithEntriesInOrder verifies that
// ensureMnemeGitignore creates <repoRoot>/.mneme/.gitignore containing every
// entry given, in order — profile.lock before backups/ (SPEC-105 DD24).
func TestEnsureMnemeGitignore_CreatesFileWithEntriesInOrder(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, ".mneme"), 0o755); err != nil {
		t.Fatalf("mkdir .mneme: %v", err)
	}

	if err := ensureMnemeGitignore(repoRoot, lockGitignoreEntry, backupsGitignoreEntry); err != nil {
		t.Fatalf("ensureMnemeGitignore: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(repoRoot, ".mneme", ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	got := string(data)
	lockIdx := strings.Index(got, lockGitignoreEntry)
	backupsIdx := strings.Index(got, backupsGitignoreEntry)
	if lockIdx == -1 || backupsIdx == -1 {
		t.Fatalf("expected both entries present, got %q", got)
	}
	if lockIdx > backupsIdx {
		t.Errorf("expected %q before %q, got %q", lockGitignoreEntry, backupsGitignoreEntry, got)
	}
}

// TestEnsureMnemeGitignore_IdempotentAndPreservesExistingContent verifies
// that calling ensureMnemeGitignore twice against a .gitignore with
// pre-existing, hand-authored content never duplicates any entry and never
// disturbs what was already there.
func TestEnsureMnemeGitignore_IdempotentAndPreservesExistingContent(t *testing.T) {
	repoRoot := t.TempDir()
	mnemeDir := filepath.Join(repoRoot, ".mneme")
	if err := os.MkdirAll(mnemeDir, 0o755); err != nil {
		t.Fatalf("mkdir .mneme: %v", err)
	}
	gitignorePath := filepath.Join(mnemeDir, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte("# hand-authored note\nsome-other-entry\n"), 0o644); err != nil {
		t.Fatalf("seed .gitignore: %v", err)
	}

	if err := ensureMnemeGitignore(repoRoot, lockGitignoreEntry, backupsGitignoreEntry); err != nil {
		t.Fatalf("ensureMnemeGitignore (first call): %v", err)
	}
	if err := ensureMnemeGitignore(repoRoot, lockGitignoreEntry, backupsGitignoreEntry); err != nil {
		t.Fatalf("ensureMnemeGitignore (second call): %v", err)
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
	if strings.Count(got, backupsGitignoreEntry) != 1 {
		t.Errorf("expected exactly one %q entry after two calls, got %q", backupsGitignoreEntry, got)
	}
}

// TestEnsureMnemeGitignore_OneEntryAlreadyPresentOnlyAddsMissing verifies
// that when one of the entries already exists, only the missing one is
// appended — not both re-written.
func TestEnsureMnemeGitignore_OneEntryAlreadyPresentOnlyAddsMissing(t *testing.T) {
	repoRoot := t.TempDir()
	mnemeDir := filepath.Join(repoRoot, ".mneme")
	if err := os.MkdirAll(mnemeDir, 0o755); err != nil {
		t.Fatalf("mkdir .mneme: %v", err)
	}
	gitignorePath := filepath.Join(mnemeDir, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte(lockGitignoreEntry+"\n"), 0o644); err != nil {
		t.Fatalf("seed .gitignore: %v", err)
	}

	if err := ensureMnemeGitignore(repoRoot, lockGitignoreEntry, backupsGitignoreEntry); err != nil {
		t.Fatalf("ensureMnemeGitignore: %v", err)
	}

	data, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	got := string(data)
	if strings.Count(got, lockGitignoreEntry) != 1 {
		t.Errorf("expected exactly one %q entry, got %q", lockGitignoreEntry, got)
	}
	if !strings.Contains(got, backupsGitignoreEntry) {
		t.Errorf("expected the missing %q entry to be appended, got %q", backupsGitignoreEntry, got)
	}
}

// TestEnsureMnemeGitignore_ReadErrorPropagates verifies that a read failure
// (here: the .gitignore path is itself a directory) is surfaced, not
// silently swallowed.
func TestEnsureMnemeGitignore_ReadErrorPropagates(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, ".mneme", ".gitignore"), 0o755); err != nil {
		t.Fatalf("mkdir .gitignore as directory: %v", err)
	}

	if err := ensureMnemeGitignore(repoRoot, lockGitignoreEntry, backupsGitignoreEntry); err == nil {
		t.Fatal("expected an error when the .gitignore path is a directory")
	}
}

// TestEnsureMnemeGitignore_WriteErrorPropagates verifies that a write
// failure (here: a read-only .mneme directory) is surfaced. Skipped when
// running as root, since permission checks are bypassed for root and the
// injected failure would never occur.
func TestEnsureMnemeGitignore_WriteErrorPropagates(t *testing.T) {
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

	if err := ensureMnemeGitignore(repoRoot, lockGitignoreEntry, backupsGitignoreEntry); err == nil {
		t.Fatal("expected an error writing .gitignore into a read-only .mneme directory")
	}
}
