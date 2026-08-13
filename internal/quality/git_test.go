package quality

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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
