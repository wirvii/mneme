package quality

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
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

// TestGit_PathChangedInRange_Deletion covers the "borrarlo" half of AC12 at
// the primitive level: PathChangedInRange must report a path as changed when
// it existed at baseSHA and was DELETED (not merely edited) somewhere within
// baseSHA..HEAD, so the deletion is never silently treated as "unchanged".
// This is the level where a deletion is genuinely, distinctly observable —
// `git diff --name-only` compares the two commit endpoints directly, so a
// service-level test that deletes-then-recreates the file with different
// content exercises the exact same code path TestQualityService_Verify_
// ConstitutionChangedInRange already covers (the endpoint content differs
// either way); only a real absence at HEAD is a distinct case, and only
// this primitive — not QualityService.Verify, which requires the current
// constitution to exist and parse before it can reach any check — can
// observe it directly.
func TestGit_PathChangedInRange_Deletion(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := initTestGitRepo(t)
	g := &Git{RepoDir: dir}

	baseSHA, err := g.HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA (base): %v", err)
	}

	if err := os.Remove(filepath.Join(dir, "committed.txt")); err != nil {
		t.Fatalf("remove committed.txt: %v", err)
	}
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "delete committed.txt")

	changed, err := g.PathChangedInRange(baseSHA, "committed.txt")
	if err != nil {
		t.Fatalf("PathChangedInRange (deleted): %v", err)
	}
	if !changed {
		t.Error("PathChangedInRange() = false for a file deleted in range, want true")
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

// TestGit_ChangedLines_HostileConfigDoesNotChangeResult covers AC10: the
// SAME repo, the SAME commit range, computed once under the repo's default
// config and once under a HOSTILE config (core.quotePath=true,
// diff.noprefix=true) that would otherwise change the literal text git
// emits, must produce IDENTICAL maps — proving ChangedLines' fixed flags
// make the result independent of whoever's .gitconfig runs it.
func TestGit_ChangedLines_HostileConfigDoesNotChangeResult(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := initTestGitRepo(t)
	g := &Git{RepoDir: dir}

	baseSHA, err := g.HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA (base): %v", err)
	}

	nonASCII := "café.txt"
	if err := os.WriteFile(filepath.Join(dir, nonASCII), []byte("uno\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", nonASCII, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plain.txt"), []byte("uno\n"), 0o644); err != nil {
		t.Fatalf("write plain.txt: %v", err)
	}
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "add files")

	before, err := g.ChangedLines(baseSHA, "HEAD")
	if err != nil {
		t.Fatalf("ChangedLines (default config): %v", err)
	}

	// The non-ASCII path must appear LITERAL, never C-style quoted/escaped
	// — this is the property "-c core.quotePath=false" exists to force,
	// regardless of what an ambient .gitconfig says (git's own UNSET
	// default for core.quotePath is actually true, i.e. quoted).
	if _, ok := before[nonASCII]; !ok {
		t.Fatalf("ChangedLines (default config) = %v, want a literal (unquoted) %q key", before, nonASCII)
	}

	gitRunTest(t, dir, "config", "core.quotePath", "true")
	gitRunTest(t, dir, "config", "diff.noprefix", "true")

	after, err := g.ChangedLines(baseSHA, "HEAD")
	if err != nil {
		t.Fatalf("ChangedLines (hostile config): %v", err)
	}

	if len(before) == 0 {
		t.Fatal("ChangedLines (default config) returned nothing — fixture is broken")
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("ChangedLines differs under a hostile .gitconfig: default=%v hostile=%v", before, after)
	}
}

// TestGit_ChangedLines_MutationDroppedQuotePathFlag is the G4 guardian
// (plan P2): dropping "-c core.quotePath=false" must turn the case above
// red — verified manually per the plan by temporarily removing that flag
// from ChangedLines' exec.Command args, re-running
// TestGit_ChangedLines_HostileConfigDoesNotChangeResult (fails: with no
// local repo config set at all, git's own UNSET default for
// core.quotePath is true, so even the "before" run parses café.txt as the
// C-quoted literal "caf\303\251.txt" instead of the literal filename — the
// direct nonASCII-key assertion above catches this where a mere
// before-vs-after comparison would not, since a hostile config value that
// happens to match git's own default leaves before==after trivially true
// under the mutation), and reverting byte-for-byte.
func TestGit_ChangedLines_MutationDroppedQuotePathFlag(t *testing.T) {
	// This test intentionally duplicates the assertion above as a single,
	// minimal, always-green anchor to point the mutation instructions at —
	// see this test's godoc for the manual verification already performed.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := initTestGitRepo(t)
	g := &Git{RepoDir: dir}
	baseSHA, err := g.HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "café.txt"), []byte("uno\n"), 0o644); err != nil {
		t.Fatalf("write café.txt: %v", err)
	}
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "add café.txt")

	changed, err := g.ChangedLines(baseSHA, "HEAD")
	if err != nil {
		t.Fatalf("ChangedLines: %v", err)
	}
	if _, ok := changed["café.txt"]; !ok {
		t.Fatalf("ChangedLines() = %v, want a literal (unquoted) entry for café.txt", changed)
	}
}

// buildMergeBaseFixture builds the ONLY git topology where MergeBase can
// possibly change ChangedLines' result: one where the recorded BaseSHA is
// NOT actually an ancestor of HEAD. (When BaseSHA IS an ancestor of HEAD —
// the common, linear "fork then merge main" case — merge-base(BaseSHA,
// HEAD) is BaseSHA itself, by git's own definition of "best common
// ancestor"; D8's own text says exactly this: "en el caso lineal la
// merge-base es BaseSHA y no cambia nada".)
//
// The fixture: otro.go lands on the shared history (c1); a SIBLING branch
// then DELETES it (becoming the — stale/wrong — recorded "BaseSHA": think a
// base_sha captured against a since-abandoned reference); the real HEAD
// forks from the same c1 and adds mio.go, never touching otro.go. Then:
//   - MergeBase(baseSHA, HEAD) = c1, whose tree STILL has otro.go — so
//     diffing from there to HEAD shows no otro.go change at all (a).
//   - The raw two-dot diff(baseSHA, HEAD) compares baseSHA's tree (otro.go
//     deleted) against HEAD's tree (otro.go present) directly — an
//     ancestor-blind tree comparison — and DOES show otro.go as newly
//     added (b).
//
// Returns the repo dir, baseSHA (the sibling tip), and headRef ("HEAD" on
// the branch actually checked out at the end).
func buildMergeBaseFixture(t *testing.T, dir string, g *Git) (baseSHA string) {
	t.Helper()

	// c1: otro.go lands on shared history.
	if err := os.WriteFile(filepath.Join(dir, "otro.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write otro.go: %v", err)
	}
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "otro.go lands on shared history")
	c1, err := g.HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA (c1): %v", err)
	}

	// Sibling branch: deletes otro.go. Its tip becomes the (stale) BaseSHA.
	gitRunTest(t, dir, "checkout", "-b", "sibling-stale-base", c1)
	if err := os.Remove(filepath.Join(dir, "otro.go")); err != nil {
		t.Fatalf("remove otro.go: %v", err)
	}
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "sibling deletes otro.go")
	baseSHA, err = g.HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA (sibling/baseSHA): %v", err)
	}

	// Real HEAD: forks from c1 (NOT from the sibling), adds mio.go, never
	// touches otro.go.
	gitRunTest(t, dir, "checkout", "-b", "feature", c1)
	if err := os.WriteFile(filepath.Join(dir, "mio.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write mio.go: %v", err)
	}
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "mio.go on the feature branch")

	return baseSHA
}

// TestGit_MergeBase_IsLoadBearing covers AC11: (a) ChangedLines computed
// from MergeBase(baseSHA, HEAD) excludes otro.go — the recorded baseSHA is
// stale and not an ancestor of HEAD, but the merge-base still is, and its
// tree already has otro.go, so no change is attributed to it; (b) the SAME
// computation via a raw two-dot range from baseSHA DOES attribute otro.go
// to this spec, because it compares two trees with no regard for ancestry.
// Without (b), (a) would stay green even with MergeBase silently deleted
// from the implementation (it would just be comparing baseSHA to HEAD
// directly, which happens to also exclude otro.go in THIS specific
// direction only because of how the fixture is built — (b) is what proves
// the two computations genuinely differ).
func TestGit_MergeBase_IsLoadBearing(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := initTestGitRepo(t)
	g := &Git{RepoDir: dir}

	baseSHA := buildMergeBaseFixture(t, dir, g)

	mergeBase, err := g.MergeBase(baseSHA, "HEAD")
	if err != nil {
		t.Fatalf("MergeBase: %v", err)
	}
	if mergeBase == baseSHA {
		t.Fatalf("MergeBase(baseSHA, HEAD) = baseSHA (%s) — fixture is not testing what it needs to: baseSHA must NOT be an ancestor of HEAD", baseSHA)
	}

	fromMergeBase, err := g.ChangedLines(mergeBase, "HEAD")
	if err != nil {
		t.Fatalf("ChangedLines(mergeBase, HEAD): %v", err)
	}
	if _, ok := fromMergeBase["otro.go"]; ok {
		t.Errorf("ChangedLines(mergeBase, HEAD) = %v, want it to EXCLUDE otro.go (not this spec's work)", fromMergeBase)
	}
	if _, ok := fromMergeBase["mio.go"]; !ok {
		t.Errorf("ChangedLines(mergeBase, HEAD) = %v, want it to include mio.go", fromMergeBase)
	}

	// (b): the SAME computation from the raw two-dot BaseSHA range DOES
	// attribute otro.go to this spec — without this row, (a) would still be
	// green even if MergeBase were quietly deleted from the implementation.
	fromBaseSHATwoDot, err := g.ChangedLines(baseSHA, "HEAD")
	if err != nil {
		t.Fatalf("ChangedLines(baseSHA, HEAD): %v", err)
	}
	if _, ok := fromBaseSHATwoDot["otro.go"]; !ok {
		t.Fatalf("ChangedLines(baseSHA, HEAD) = %v, want it to include otro.go (this is the row merge-base fixes)", fromBaseSHATwoDot)
	}
}

// TestGit_MergeBase_MutationUsingBaseSHADirectly is the G5 guardian
// (plan P2): substituting MergeBase(base, HEAD) with base directly must
// make otro.go appear (the mergeBase-scoped computation degenerates into
// the same raw baseSHA computation, which DOES contain otro.go per (b)
// above) — verified manually per the plan by temporarily replacing this
// test's call to g.MergeBase with its baseSHA argument returned verbatim,
// re-running it (fails: the "excludes otro.go" assertion now sees otro.go
// present), and reverting byte-for-byte.
func TestGit_MergeBase_MutationUsingBaseSHADirectly(t *testing.T) {
	// This test intentionally duplicates the "must exclude otro.go"
	// assertion as a single, minimal, always-green anchor to point the
	// mutation instructions above at — see this test's godoc.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := initTestGitRepo(t)
	g := &Git{RepoDir: dir}

	baseSHA := buildMergeBaseFixture(t, dir, g)

	mergeBase, err := g.MergeBase(baseSHA, "HEAD")
	if err != nil {
		t.Fatalf("MergeBase: %v", err)
	}
	changed, err := g.ChangedLines(mergeBase, "HEAD")
	if err != nil {
		t.Fatalf("ChangedLines: %v", err)
	}
	if _, ok := changed["otro.go"]; ok {
		t.Fatalf("ChangedLines(mergeBase, HEAD) = %v, want it to exclude otro.go", changed)
	}
	if _, ok := changed["mio.go"]; !ok {
		t.Fatalf("ChangedLines(mergeBase, HEAD) = %v, want mio.go", changed)
	}
}

// TestGit_IsAncestor covers the primitive AC21's baseline-comparable check
// depends on: an ancestor commit reports true; a commit on a sibling branch
// reports false.
func TestGit_IsAncestor(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := initTestGitRepo(t)
	g := &Git{RepoDir: dir}

	baseSHA, err := g.HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA (base): %v", err)
	}

	gitRunTest(t, dir, "checkout", "-b", "sibling")
	if err := os.WriteFile(filepath.Join(dir, "sibling.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write sibling.txt: %v", err)
	}
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "sibling commit")
	siblingSHA, err := g.HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA (sibling): %v", err)
	}

	gitRunTest(t, dir, "checkout", "main")
	if err := os.WriteFile(filepath.Join(dir, "main2.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write main2.txt: %v", err)
	}
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "main commit")
	mainHeadSHA, err := g.HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA (main head): %v", err)
	}

	isAncestor, err := g.IsAncestor(baseSHA, mainHeadSHA)
	if err != nil {
		t.Fatalf("IsAncestor(base, main head): %v", err)
	}
	if !isAncestor {
		t.Error("IsAncestor(base, main head) = false, want true")
	}

	isAncestor, err = g.IsAncestor(siblingSHA, mainHeadSHA)
	if err != nil {
		t.Fatalf("IsAncestor(sibling, main head): %v", err)
	}
	if isAncestor {
		t.Error("IsAncestor(sibling, main head) = true, want false (sibling branch, not an ancestor)")
	}
}

// TestListFilesAtRef covers AC11's ListFilesAtRef half: HEAD sees a file
// added in a later commit; a base ref taken before that commit does not.
func TestListFilesAtRef(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := initTestGitRepo(t)
	g := &Git{RepoDir: dir}

	baseSHA, err := g.HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA (base): %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "newfile.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatalf("write newfile.go: %v", err)
	}
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "add newfile")

	headFiles, err := g.ListFilesAtRef("HEAD")
	if err != nil {
		t.Fatalf("ListFilesAtRef(HEAD): %v", err)
	}
	if !containsString(headFiles, "newfile.go") {
		t.Errorf("ListFilesAtRef(HEAD) = %v, want it to contain newfile.go", headFiles)
	}

	baseFiles, err := g.ListFilesAtRef(baseSHA)
	if err != nil {
		t.Fatalf("ListFilesAtRef(base): %v", err)
	}
	if containsString(baseFiles, "newfile.go") {
		t.Errorf("ListFilesAtRef(base) = %v, want it NOT to contain newfile.go (added after base)", baseFiles)
	}
	if !containsString(baseFiles, "committed.txt") {
		t.Errorf("ListFilesAtRef(base) = %v, want it to contain committed.txt (present at both refs)", baseFiles)
	}
}

// TestListFilesAtRef_SpecialCharacters covers R-E: a filename containing a
// colon and one containing a space are both reported intact — proof the -z/
// NUL parsing never cuts on ':' or relies on whitespace.
func TestListFilesAtRef_SpecialCharacters(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := initTestGitRepo(t)
	g := &Git{RepoDir: dir}

	if err := os.WriteFile(filepath.Join(dir, "weird:name.go"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write weird:name.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "with space.go"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write with space.go: %v", err)
	}
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "special names")

	files, err := g.ListFilesAtRef("HEAD")
	if err != nil {
		t.Fatalf("ListFilesAtRef: %v", err)
	}
	if !containsString(files, "weird:name.go") {
		t.Errorf("ListFilesAtRef = %v, want it to contain %q intact", files, "weird:name.go")
	}
	if !containsString(files, "with space.go") {
		t.Errorf("ListFilesAtRef = %v, want it to contain %q intact", files, "with space.go")
	}
}

// TestGrepLinesAtRef covers AC11/AC13's GrepLinesAtRef half: line counts
// (not occurrence counts, D3 point 3), -F literal matching, the exit-1
// "nothing matched" case, and R-E's filename-with-colon/space fixture.
func TestGrepLinesAtRef(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := initTestGitRepo(t)
	g := &Git{RepoDir: dir}

	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("foo\nfoo bar\nbaz\n"), 0o644); err != nil {
		t.Fatalf("write a.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "with space.go"), []byte("foo here\n"), 0o644); err != nil {
		t.Fatalf("write with space.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "weird:name.go"), []byte("foo there\n"), 0o644); err != nil {
		t.Fatalf("write weird:name.go: %v", err)
	}
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "grep fixture")

	counts, err := g.GrepLinesAtRef("HEAD", "foo", false)
	if err != nil {
		t.Fatalf("GrepLinesAtRef: %v", err)
	}
	// a.go has TWO lines containing "foo" ("foo" and "foo bar") — a LINE
	// count, not an occurrence count (D3 point 3): if -c ever became -o this
	// would jump to a different number without "foo" appearing twice on any
	// single line.
	if counts["a.go"] != 2 {
		t.Errorf("counts[a.go] = %d, want 2 (two matching LINES)", counts["a.go"])
	}
	if counts["with space.go"] != 1 {
		t.Errorf("counts[%q] = %d, want 1", "with space.go", counts["with space.go"])
	}
	if counts["weird:name.go"] != 1 {
		t.Errorf("counts[%q] = %d, want 1 (colon in filename must not break parsing)", "weird:name.go", counts["weird:name.go"])
	}

	// A file with NO match at all must be absent from the map, never a
	// zero-count entry — the exit-1 "nothing matched at all" case (when
	// EVERY file misses) is exercised by the "no matches anywhere" row
	// below; this row exercises "matches somewhere, but not in every file".
	if err := os.WriteFile(filepath.Join(dir, "nomatch.go"), []byte("nothing to see\n"), 0o644); err != nil {
		t.Fatalf("write nomatch.go: %v", err)
	}
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "add nomatch")
	counts, err = g.GrepLinesAtRef("HEAD", "foo", false)
	if err != nil {
		t.Fatalf("GrepLinesAtRef (with nomatch.go present): %v", err)
	}
	if _, ok := counts["nomatch.go"]; ok {
		t.Error("counts contains nomatch.go, want it absent (zero matches, not a zero-count entry)")
	}

	// No matches ANYWHERE: git grep exits 1 — must be an empty map, nil
	// error, never propagated as a failure.
	counts, err = g.GrepLinesAtRef("HEAD", "nonexistent-needle-xyz", false)
	if err != nil {
		t.Fatalf("GrepLinesAtRef (no matches anywhere): %v", err)
	}
	if len(counts) != 0 {
		t.Errorf("counts = %v, want empty map for a needle matching nothing", counts)
	}
}

// TestGrepLinesAtRef_Word covers AC12: `-w` is load-bearing — without it
// "Foo" would match inside "FooBar".
func TestGrepLinesAtRef_Word(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := initTestGitRepo(t)
	g := &Git{RepoDir: dir}

	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("var FooBar int\n"), 0o644); err != nil {
		t.Fatalf("write a.go: %v", err)
	}
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "word fixture")

	withWord, err := g.GrepLinesAtRef("HEAD", "Foo", true)
	if err != nil {
		t.Fatalf("GrepLinesAtRef(word=true): %v", err)
	}
	if len(withWord) != 0 {
		t.Errorf("GrepLinesAtRef(word=true) = %v, want empty (FooBar is not the whole word Foo)", withWord)
	}

	withoutWord, err := g.GrepLinesAtRef("HEAD", "Foo", false)
	if err != nil {
		t.Fatalf("GrepLinesAtRef(word=false): %v", err)
	}
	if withoutWord["a.go"] != 1 {
		t.Errorf("GrepLinesAtRef(word=false)[a.go] = %d, want 1 (substring match inside FooBar)", withoutWord["a.go"])
	}
}

// TestGrepLinesAtRef_LiteralNotRegex covers D3 point 1: -F treats needle as
// a literal string — a regex metacharacter in the search text must not be
// interpreted as one.
func TestGrepLinesAtRef_LiteralNotRegex(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := initTestGitRepo(t)
	g := &Git{RepoDir: dir}

	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("a.b.c\naxbxc\n"), 0o644); err != nil {
		t.Fatalf("write a.go: %v", err)
	}
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "literal fixture")

	counts, err := g.GrepLinesAtRef("HEAD", "a.b.c", false)
	if err != nil {
		t.Fatalf("GrepLinesAtRef: %v", err)
	}
	// If "." were interpreted as a regex wildcard, "axbxc" would ALSO match
	// — -F must keep it literal, matching only the exact "a.b.c" line.
	if counts["a.go"] != 1 {
		t.Errorf("counts[a.go] = %d, want 1 (literal match only, . must not be a regex wildcard)", counts["a.go"])
	}
}

// containsString reports whether s is present in list — a tiny helper kept
// local to this test file rather than pulled from slices.Contains so this
// file's own imports stay minimal.
func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// --- SPEC-118 EPIC-calidad S4 P2: file-delta primitives ---

// TestParseNameStatus covers the closed vocabulary ParseNameStatus accepts:
// added, modified, deleted, type-change, rename (G8's three-record shape),
// copy, and an unrecognised status skipped rather than erroring.
func TestParseNameStatus(t *testing.T) {
	build := func(records ...string) []byte {
		return []byte(strings.Join(records, "\x00") + "\x00")
	}

	tests := []struct {
		name string
		data []byte
		want []FileChange
	}{
		{
			name: "added",
			data: build("A", "new.go"),
			want: []FileChange{{Path: "new.go", Status: FileStatusAdded}},
		},
		{
			name: "modified",
			data: build("M", "existing.go"),
			want: []FileChange{{Path: "existing.go", Status: FileStatusModified}},
		},
		{
			name: "deleted",
			data: build("D", "gone.go"),
			want: []FileChange{{Path: "gone.go", Status: FileStatusDeleted}},
		},
		{
			name: "type change treated as modified",
			data: build("T", "link.go"),
			want: []FileChange{{Path: "link.go", Status: FileStatusModified}},
		},
		{
			name: "rename is THREE records, not two (G8)",
			data: build("R100", "old.go", "new.go"),
			want: []FileChange{{OldPath: "old.go", Path: "new.go", Status: FileStatusRenamed}},
		},
		{
			name: "copy is also three records",
			data: build("C100", "src.go", "dst.go"),
			want: []FileChange{{OldPath: "src.go", Path: "dst.go", Status: FileStatusCopied}},
		},
		{
			name: "unknown status skipped, not an error",
			data: build("U", "conflicted.go", "A", "after.go"),
			want: []FileChange{{Path: "after.go", Status: FileStatusAdded}},
		},
		{
			name: "multiple records in sequence",
			data: build("A", "a.go", "M", "b.go", "D", "c.go"),
			want: []FileChange{
				{Path: "a.go", Status: FileStatusAdded},
				{Path: "b.go", Status: FileStatusModified},
				{Path: "c.go", Status: FileStatusDeleted},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseNameStatus(tt.data)
			if err != nil {
				t.Fatalf("ParseNameStatus() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseNameStatus() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestParseNumStat covers the numstat -z record shape: a regular file, a
// binary file ("-\t-", D2 point 3, migrated from lane.parseNumStatLine),
// and a rename's three-record shape (mirroring ParseNameStatus's own).
func TestParseNumStat(t *testing.T) {
	build := func(records ...string) []byte {
		return []byte(strings.Join(records, "\x00") + "\x00")
	}

	tests := []struct {
		name string
		data []byte
		want []FileStat
	}{
		{
			name: "regular file",
			data: build("5\t3\tfoo.go"),
			want: []FileStat{{Path: "foo.go", Added: 5, Removed: 3}},
		},
		{
			name: "binary file reports zero lines, not an error",
			data: build("-\t-\timage.png"),
			want: []FileStat{{Path: "image.png", Added: 0, Removed: 0}},
		},
		{
			name: "rename: empty path field, then two path records",
			data: build("2\t1\t", "old.go", "new.go"),
			want: []FileStat{{Path: "new.go", Added: 2, Removed: 1}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseNumStat(tt.data)
			if err != nil {
				t.Fatalf("ParseNumStat() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseNumStat() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestGit_ChangedFilesInRange_RenameIsNotDeletePlusAdd is G8 over a REAL
// repository: renaming a file between two commits must classify as ONE
// FileStatusRenamed entry, never a delete of the old path plus an add of
// the new one.
func TestGit_ChangedFilesInRange_RenameIsNotDeletePlusAdd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := initTestGitRepo(t)
	g := &Git{RepoDir: dir}

	content := []byte("package foo\n\nfunc Foo() {}\n")
	if err := os.WriteFile(filepath.Join(dir, "foo.go"), content, 0o644); err != nil {
		t.Fatalf("write foo.go: %v", err)
	}
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "add foo.go")

	// base is captured AFTER foo.go exists, so the rename below has
	// something to be detected against — diffing from before foo.go
	// existed would see only a plain add of bar.go, since foo.go is absent
	// on BOTH sides of that wider range (the bug this comment protects
	// against: the fix was verified against exactly that mistake).
	base := strings.TrimSpace(gitRunTest(t, dir, "rev-parse", "HEAD"))

	if err := os.Rename(filepath.Join(dir, "foo.go"), filepath.Join(dir, "bar.go")); err != nil {
		t.Fatalf("rename: %v", err)
	}
	gitRunTest(t, dir, "add", "-A")
	gitRunTest(t, dir, "commit", "-m", "rename foo.go to bar.go")

	changes, err := g.ChangedFilesInRange(base, "HEAD")
	if err != nil {
		t.Fatalf("ChangedFilesInRange: %v", err)
	}

	var renamed *FileChange
	for i := range changes {
		if changes[i].Status == FileStatusRenamed {
			renamed = &changes[i]
		}
		if changes[i].Status == FileStatusAdded || changes[i].Status == FileStatusDeleted {
			t.Errorf("changes contains a %s entry (%+v) — a pure rename must not appear as delete+add", changes[i].Status, changes[i])
		}
	}
	if renamed == nil {
		t.Fatalf("no FileStatusRenamed entry found in %+v", changes)
	}
	if renamed.OldPath != "foo.go" || renamed.Path != "bar.go" {
		t.Errorf("renamed = %+v, want OldPath=foo.go Path=bar.go", renamed)
	}
}

// TestGit_NumStat_BinaryFile covers D2 point 3 over a real repository: a
// binary file added between two commits reports zero lines, never an
// error.
func TestGit_NumStat_BinaryFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := initTestGitRepo(t)
	g := &Git{RepoDir: dir}

	base := strings.TrimSpace(gitRunTest(t, dir, "rev-parse", "HEAD"))

	// A NUL byte in the content forces git to treat the file as binary.
	if err := os.WriteFile(filepath.Join(dir, "blob.bin"), []byte{0x00, 0x01, 0x02, 0x03}, 0o644); err != nil {
		t.Fatalf("write blob.bin: %v", err)
	}
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "add binary")

	stats, err := g.NumStat(base, "HEAD")
	if err != nil {
		t.Fatalf("NumStat: %v", err)
	}
	found := false
	for _, s := range stats {
		if s.Path == "blob.bin" {
			found = true
			if s.Added != 0 || s.Removed != 0 {
				t.Errorf("blob.bin stat = %+v, want Added=0 Removed=0", s)
			}
		}
	}
	if !found {
		t.Fatalf("blob.bin not found in %+v", stats)
	}
}
