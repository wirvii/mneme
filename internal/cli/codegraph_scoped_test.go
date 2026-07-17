package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/codegraph"
	"github.com/wirvii/mneme/internal/service"
)

// ---------------------------------------------------------------------------
// Test scaffolding (SPEC-101). All git fixtures are local temp repos with an
// inline identity — no network, no shared state, no git stash/clean.
// ---------------------------------------------------------------------------

// gitCommit stages everything and commits with the given message inside dir,
// returning the resulting HEAD SHA.
func gitCommit(t *testing.T, dir, msg string) string {
	t.Helper()
	run := func(args ...string) {
		full := append([]string{"-C", dir}, args...)
		if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	run("add", "-A")
	run("commit", "-m", msg, "--no-gpg-sign")
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	return string(out[:len(out)-1]) // strip trailing newline
}

// writeFile writes content to dir/rel, creating parent directories.
func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// runScopedReindex chdirs into repo with flagDataDir/flagProject pointed at an
// isolated data dir + fixed slug, then calls runCodegraphHooksReindex directly
// (the standalone, testable entry point). It returns the function's error.
func runScopedReindex(t *testing.T, repo, dataDir, slug string) error {
	t.Helper()
	oldProject, oldDataDir := flagProject, flagDataDir
	flagProject, flagDataDir = slug, dataDir
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("Chdir %s: %v", repo, err)
	}
	t.Cleanup(func() {
		flagProject, flagDataDir = oldProject, oldDataDir
		_ = os.Chdir(orig)
	})
	return runCodegraphHooksReindex()
}

// openGraph opens a read-only service against the codegraph DB for slug so tests
// can inspect graph state and the persisted last_sha.
func openGraph(t *testing.T, dataDir, slug string) *service.CodeGraphService {
	t.Helper()
	svc, err := service.NewCodeGraphService(filepath.Join(dataDir, "projects"), slug)
	if err != nil {
		t.Fatalf("open graph service: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	return svc
}

// hasNodes reports whether the graph has any non-file node for relPath.
func hasNodes(t *testing.T, svc *service.CodeGraphService, relPath string) bool {
	t.Helper()
	files, err := svc.Files(relPath, "")
	if err != nil {
		t.Fatalf("Files(%q): %v", relPath, err)
	}
	return len(files) > 0
}

// ---------------------------------------------------------------------------
// End-to-end scoped re-index (AC 1,2,3,4,5)
// ---------------------------------------------------------------------------

// TestScopedReindex_EndToEnd drives a real two-commit git fixture: the first
// run-reindex full-scans and stamps last_sha; the second re-indexes only the
// delta (modify/add/delete/rename), purging removed paths.
func TestScopedReindex_EndToEnd(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo)
	dataDir := t.TempDir()
	slug := "scopedtest"

	// Commit 1: three source files.
	writeFile(t, repo, "a.go", "package p\n\nfunc A() {}\n")
	writeFile(t, repo, "b.go", "package p\n\nfunc B() {}\n")
	writeFile(t, repo, "keep.go", "package p\n\nfunc Keep() {}\n")
	head1 := gitCommit(t, repo, "initial")

	if err := runScopedReindex(t, repo, dataDir, slug); err != nil {
		t.Fatalf("first run-reindex: %v", err)
	}

	svc := openGraph(t, dataDir, slug)
	if got, _ := svc.LastIndexedSHA(); got != head1 {
		t.Errorf("last_sha after first run = %q, want %q", got, head1)
	}
	if !hasNodes(t, svc, "a.go") || !hasNodes(t, svc, "b.go") || !hasNodes(t, svc, "keep.go") {
		t.Fatal("first run should have indexed all three files")
	}

	// Commit 2: modify a.go, delete b.go, add c.go. keep.go untouched.
	writeFile(t, repo, "a.go", "package p\n\nfunc A() {}\n\nfunc A2() {}\n")
	if err := os.Remove(filepath.Join(repo, "b.go")); err != nil {
		t.Fatalf("remove b.go: %v", err)
	}
	writeFile(t, repo, "c.go", "package p\n\nfunc C() {}\n")
	head2 := gitCommit(t, repo, "second")

	if err := runScopedReindex(t, repo, dataDir, slug); err != nil {
		t.Fatalf("second run-reindex: %v", err)
	}

	svc2 := openGraph(t, dataDir, slug)
	if got, _ := svc2.LastIndexedSHA(); got != head2 {
		t.Errorf("last_sha after second run = %q, want %q", got, head2)
	}
	// AC2: deleted file purged.
	if hasNodes(t, svc2, "b.go") {
		t.Error("b.go: symbols must be purged after scoped delete")
	}
	// Added and modified files present.
	if !hasNodes(t, svc2, "c.go") {
		t.Error("c.go: expected symbols after scoped add")
	}
	if !hasNodes(t, svc2, "a.go") || !hasNodes(t, svc2, "keep.go") {
		t.Error("a.go/keep.go should remain in the graph")
	}
}

// TestScopedReindex_NoChanges verifies that a second run with no new commit
// short-circuits on the last==HEAD branch (no re-index, no re-stamp needed).
func TestScopedReindex_NoChanges(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo)
	dataDir := t.TempDir()
	slug := "nochanges"

	writeFile(t, repo, "a.go", "package p\n\nfunc A() {}\n")
	head := gitCommit(t, repo, "initial")

	if err := runScopedReindex(t, repo, dataDir, slug); err != nil {
		t.Fatalf("first run-reindex: %v", err)
	}
	// Second run with no intervening commit: last == HEAD path.
	if err := runScopedReindex(t, repo, dataDir, slug); err != nil {
		t.Fatalf("second run-reindex (no changes): %v", err)
	}
	svc := openGraph(t, dataDir, slug)
	if got, _ := svc.LastIndexedSHA(); got != head {
		t.Errorf("last_sha = %q, want %q (unchanged)", got, head)
	}
}

// TestScopedReindex_RenamePurgesOld drives a rename commit and asserts the old
// path is purged and the new path indexed in scoped mode.
func TestScopedReindex_RenamePurgesOld(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo)
	dataDir := t.TempDir()
	slug := "renametest"

	writeFile(t, repo, "old.go", "package p\n\nfunc Movable() {}\n")
	gitCommit(t, repo, "initial")
	if err := runScopedReindex(t, repo, dataDir, slug); err != nil {
		t.Fatalf("first run-reindex: %v", err)
	}

	// Rename old.go → sub/new.go via git mv so the diff reports R.
	if err := os.MkdirAll(filepath.Join(repo, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	if out, err := exec.Command("git", "-C", repo, "mv", "old.go", "sub/new.go").CombinedOutput(); err != nil {
		t.Fatalf("git mv: %s", out)
	}
	gitCommit(t, repo, "rename")
	if err := runScopedReindex(t, repo, dataDir, slug); err != nil {
		t.Fatalf("second run-reindex: %v", err)
	}

	svc := openGraph(t, dataDir, slug)
	if hasNodes(t, svc, "old.go") {
		t.Error("old.go: must be purged after rename")
	}
	if !hasNodes(t, svc, "sub/new.go") {
		t.Error("sub/new.go: expected symbols after rename")
	}
}

// TestScopedReindex_InvalidLastSHAFallsBack verifies that a bogus stored last_sha
// (as if the commit was garbage-collected) falls back to a full scan.
func TestScopedReindex_InvalidLastSHAFallsBack(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo)
	dataDir := t.TempDir()
	slug := "invalidsha"

	writeFile(t, repo, "a.go", "package p\n\nfunc A() {}\n")
	head := gitCommit(t, repo, "initial")

	// Pre-seed a nonexistent last_sha so gitCommitExists returns false.
	seed := openGraph(t, dataDir, slug)
	if err := seed.SetLastIndexedSHA("0000000000000000000000000000000000000000"); err != nil {
		t.Fatalf("seed bogus sha: %v", err)
	}
	_ = seed.Close()

	if err := runScopedReindex(t, repo, dataDir, slug); err != nil {
		t.Fatalf("run-reindex: %v", err)
	}

	svc := openGraph(t, dataDir, slug)
	if got, _ := svc.LastIndexedSHA(); got != head {
		t.Errorf("last_sha = %q, want %q (full-scan fallback then stamp)", got, head)
	}
	if !hasNodes(t, svc, "a.go") {
		t.Error("a.go: full-scan fallback should have indexed it")
	}
}

// TestScopedReindex_SkipsDuringMerge verifies the reindexInProgress guard fires
// before any lock is taken and leaves last_sha untouched.
func TestScopedReindex_SkipsDuringMerge(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo)
	dataDir := t.TempDir()
	slug := "mergeskip"

	writeFile(t, repo, "a.go", "package p\n\nfunc A() {}\n")
	gitCommit(t, repo, "initial")

	gd, err := gitDir(repo)
	if err != nil {
		t.Fatalf("gitDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gd, "MERGE_HEAD"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write MERGE_HEAD: %v", err)
	}

	if err := runScopedReindex(t, repo, dataDir, slug); err != nil {
		t.Fatalf("run-reindex during merge: %v", err)
	}
	// No lock should have been created and nothing indexed.
	if fileExists(filepath.Join(gd, reindexLockName)) {
		t.Error("lock must not be created while a merge is in progress")
	}
	svc := openGraph(t, dataDir, slug)
	if got, _ := svc.LastIndexedSHA(); got != "" {
		t.Errorf("last_sha = %q, want empty (merge guard skipped indexing)", got)
	}
}

// ---------------------------------------------------------------------------
// Coalesce lock + dirty catch-up (AC 6,7)
// ---------------------------------------------------------------------------

// TestScopedReindex_CoalesceLockout simulates a live holder by pre-creating the
// lockfile: the invocation must leave a dirty marker and exit 0 WITHOUT indexing.
// Removing the lock and re-running must consume the dirty marker via a catch-up.
func TestScopedReindex_CoalesceLockout(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo)
	dataDir := t.TempDir()
	slug := "coalesce"

	writeFile(t, repo, "a.go", "package p\n\nfunc A() {}\n")
	head1 := gitCommit(t, repo, "initial")
	if err := runScopedReindex(t, repo, dataDir, slug); err != nil {
		t.Fatalf("first run-reindex: %v", err)
	}

	gd, err := gitDir(repo)
	if err != nil {
		t.Fatalf("gitDir: %v", err)
	}
	lockPath := filepath.Join(gd, reindexLockName)
	dirtyPath := filepath.Join(gd, reindexDirtyName)

	// Simulate a live holder.
	if err := os.WriteFile(lockPath, []byte("99999\n0\n"), 0o600); err != nil {
		t.Fatalf("write fake lock: %v", err)
	}
	// Refresh mtime so it is NOT considered stale.
	now := time.Now()
	_ = os.Chtimes(lockPath, now, now)

	// Commit 2 arrives while the (fake) holder is busy.
	writeFile(t, repo, "b.go", "package p\n\nfunc B() {}\n")
	head2 := gitCommit(t, repo, "second")

	if err := runScopedReindex(t, repo, dataDir, slug); err != nil {
		t.Fatalf("locked-out run-reindex: %v", err)
	}
	// Must have recorded pending work and NOT indexed b.go or advanced last_sha.
	if !fileExists(dirtyPath) {
		t.Error("dirty marker must be created when locked out")
	}
	svc := openGraph(t, dataDir, slug)
	if got, _ := svc.LastIndexedSHA(); got != head1 {
		t.Errorf("last_sha = %q, want %q (locked out, no advance)", got, head1)
	}
	if hasNodes(t, svc, "b.go") {
		t.Error("b.go must not be indexed while locked out")
	}
	_ = svc.Close()

	// Holder finishes: remove the lock, run again. Catch-up consumes the dirty
	// marker; the first pass already covers HEAD2, the catch-up is a no-op.
	if err := os.Remove(lockPath); err != nil {
		t.Fatalf("remove fake lock: %v", err)
	}
	if err := runScopedReindex(t, repo, dataDir, slug); err != nil {
		t.Fatalf("holder run-reindex: %v", err)
	}
	if fileExists(dirtyPath) {
		t.Error("dirty marker must be consumed by the catch-up pass")
	}
	svc2 := openGraph(t, dataDir, slug)
	if got, _ := svc2.LastIndexedSHA(); got != head2 {
		t.Errorf("last_sha = %q, want %q after catch-up", got, head2)
	}
	if !hasNodes(t, svc2, "b.go") {
		t.Error("b.go: catch-up should have indexed the coalesced commit")
	}
	// Lock released.
	if fileExists(lockPath) {
		t.Error("lock must be released after the holder finishes")
	}
}

// ---------------------------------------------------------------------------
// Lock unit tests (portable pidfile + stale steal)
// ---------------------------------------------------------------------------

// TestAcquireReindexLock_ExclusiveAndRelease verifies the second acquire is
// denied while the first is held and succeeds again after release.
func TestAcquireReindexLock_ExclusiveAndRelease(t *testing.T) {
	gd := t.TempDir()

	first, err := acquireReindexLock(gd)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if !first.acquired {
		t.Fatal("first acquire must succeed")
	}

	second, err := acquireReindexLock(gd)
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if second.acquired {
		t.Fatal("second acquire must be denied while held")
	}

	first.release()
	if fileExists(filepath.Join(gd, reindexLockName)) {
		t.Error("release must remove the lockfile")
	}

	third, err := acquireReindexLock(gd)
	if err != nil {
		t.Fatalf("third acquire: %v", err)
	}
	if !third.acquired {
		t.Fatal("third acquire must succeed after release")
	}
	third.release()
}

// TestAcquireReindexLock_StealsStale verifies that a lockfile older than the TTL
// is treated as abandoned and stolen.
func TestAcquireReindexLock_StealsStale(t *testing.T) {
	gd := t.TempDir()
	lockPath := filepath.Join(gd, reindexLockName)

	if err := os.WriteFile(lockPath, []byte("12345\n0\n"), 0o600); err != nil {
		t.Fatalf("write stale lock: %v", err)
	}
	old := time.Now().Add(-2 * reindexLockTTL)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatalf("backdate lock: %v", err)
	}

	lock, err := acquireReindexLock(gd)
	if err != nil {
		t.Fatalf("acquire over stale: %v", err)
	}
	if !lock.acquired {
		t.Fatal("stale lock must be stolen and acquired")
	}
	lock.release()
}

// ---------------------------------------------------------------------------
// parseDiff (AC covering --name-status parsing)
// ---------------------------------------------------------------------------

func TestParseDiff(t *testing.T) {
	in := "A\tadded.go\n" +
		"M\tchanged.go\n" +
		"D\tremoved.go\n" +
		"R100\told.go\tnew.go\n" +
		"C75\tsrc.go\tcopy.go\n" +
		"T\ttypechange.go\n" +
		"\n" + // blank line ignored
		"X\tgarbage" // unknown status ignored

	got := parseDiff(in)
	want := []codegraph.ChangedFile{
		{Path: "added.go", Status: codegraph.ChangeAdded},
		{Path: "changed.go", Status: codegraph.ChangeModified},
		{Path: "removed.go", Status: codegraph.ChangeDeleted},
		{OldPath: "old.go", Path: "new.go", Status: codegraph.ChangeRenamed},
		{Path: "copy.go", Status: codegraph.ChangeAdded},
		{Path: "typechange.go", Status: codegraph.ChangeModified},
	}
	if len(got) != len(want) {
		t.Fatalf("parseDiff returned %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], w)
		}
	}
}

// TestParseDiff_EmptyIsNonNil verifies an empty diff yields a non-nil empty slice
// so callers stay on the scoped path (Changes != nil) instead of full-scanning.
func TestParseDiff_EmptyIsNonNil(t *testing.T) {
	got := parseDiff("")
	if got == nil {
		t.Fatal("parseDiff(\"\") must return a non-nil slice")
	}
	if len(got) != 0 {
		t.Errorf("parseDiff(\"\") len = %d, want 0", len(got))
	}
}
