package laneaudit

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/wirvii/mneme/internal/gitrepo"
)

// fakeSymbolExtractor is a table-driven test double implementing
// SymbolExtractor without ever parsing real source — it looks up path in a
// pre-seeded map and counts every invocation.
type fakeSymbolExtractor struct {
	byPath map[string][]Symbol
	calls  int
}

func (f *fakeSymbolExtractor) Symbols(path string, content []byte) ([]Symbol, error) {
	f.calls++
	_ = content
	return f.byPath[path], nil
}

// TestCollectSymbols_SkipsAbsentFilesAndFiltersBudgetedKinds covers: only
// SymbolKinds symbols survive, and a path absent at ref (the normal shape of
// a created/deleted file) contributes nothing rather than erroring.
func TestCollectSymbols_SkipsAbsentFilesAndFiltersBudgetedKinds(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := initTestGitRepo(t)
	g := &gitrepo.Git{RepoDir: dir}

	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatalf("write a.go: %v", err)
	}
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "add a.go")

	ex := &fakeSymbolExtractor{byPath: map[string][]Symbol{
		"a.go": {
			{QualifiedName: "Foo", Kind: "function"},
			{QualifiedName: "Bar", Kind: "struct"},
			{QualifiedName: "N", Kind: "constant"},
			{QualifiedName: "param", Kind: "parameter"},
			{QualifiedName: "field", Kind: "field"},
		},
	}}

	symsByFile, err := CollectSymbols(g, "HEAD", []string{"a.go", "missing.go"}, ex)
	if err != nil {
		t.Fatalf("CollectSymbols: %v", err)
	}
	if _, ok := symsByFile["missing.go"]; ok {
		t.Errorf("symsByFile contains missing.go — an absent path must contribute nothing, not an empty entry")
	}
	syms := symsByFile["a.go"]
	if len(syms) != 3 {
		t.Fatalf("len(syms) = %d, want 3 (function+struct+constant only, param/field excluded): %+v", len(syms), syms)
	}
	for _, s := range syms {
		if s.Kind == "parameter" || s.Kind == "field" {
			t.Errorf("unexpected non-budgeted kind %q survived filtering", s.Kind)
		}
		if s.Key != SymbolKey("a.go", s.QualifiedName) {
			t.Errorf("Key = %q, want %q", s.Key, SymbolKey("a.go", s.QualifiedName))
		}
	}
}

// TestCollectSymbols_OnlyReadsGivenPaths: the extractor is invoked EXACTLY
// once per path handed in, never once per file in the ref's whole tree.
func TestCollectSymbols_OnlyReadsGivenPaths(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := initTestGitRepo(t)
	g := &gitrepo.Git{RepoDir: dir}

	for _, name := range []string{"a.go", "b.go", "c.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package x\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "three files")

	ex := &fakeSymbolExtractor{byPath: map[string][]Symbol{}}
	if _, err := CollectSymbols(g, "HEAD", []string{"a.go", "b.go"}, ex); err != nil {
		t.Fatalf("CollectSymbols: %v", err)
	}
	if ex.calls != 2 {
		t.Errorf("extractor invoked %d times, want exactly 2 (one per requested path, never the whole tree)", ex.calls)
	}
}

// TestDiffSymbols_FourClasses covers: created, deleted, modified (body
// changed), unchanged (must NOT appear anywhere), and moved (file renamed,
// same qualified name).
func TestDiffSymbols_FourClasses(t *testing.T) {
	created := Symbol{Key: SymbolKey("new.go", "Created"), QualifiedName: "Created", File: "new.go", StartLine: 1, EndLine: 3}
	deleted := Symbol{Key: SymbolKey("old.go", "Deleted"), QualifiedName: "Deleted", File: "old.go", StartLine: 1, EndLine: 3}
	modifiedBase := Symbol{Key: SymbolKey("mod.go", "Modified"), QualifiedName: "Modified", File: "mod.go", StartLine: 1, EndLine: 3}
	modifiedHead := Symbol{Key: SymbolKey("mod.go", "Modified"), QualifiedName: "Modified", File: "mod.go", StartLine: 1, EndLine: 5}
	untouched := Symbol{Key: SymbolKey("same.go", "Untouched"), QualifiedName: "Untouched", File: "same.go", StartLine: 10, EndLine: 12}
	movedBase := Symbol{Key: SymbolKey("before.go", "Moved"), QualifiedName: "Moved", File: "before.go", StartLine: 1, EndLine: 3}
	movedHead := Symbol{Key: SymbolKey("after.go", "Moved"), QualifiedName: "Moved", File: "after.go", StartLine: 1, EndLine: 3}

	base := map[string][]Symbol{
		"old.go":    {deleted},
		"mod.go":    {modifiedBase},
		"same.go":   {untouched},
		"before.go": {movedBase},
	}
	head := map[string][]Symbol{
		"new.go":   {created},
		"mod.go":   {modifiedHead},
		"same.go":  {untouched},
		"after.go": {movedHead},
	}
	renames := map[string]string{"after.go": "before.go"}
	changedLines := map[string][]int{"mod.go": {4, 5}}

	delta := DiffSymbols(base, head, renames, changedLines)

	if len(delta.Created) != 1 || delta.Created[0].QualifiedName != "Created" {
		t.Errorf("Created = %+v, want exactly [Created]", delta.Created)
	}
	if len(delta.Deleted) != 1 || delta.Deleted[0].QualifiedName != "Deleted" {
		t.Errorf("Deleted = %+v, want exactly [Deleted]", delta.Deleted)
	}
	if len(delta.Modified) != 1 || delta.Modified[0].QualifiedName != "Modified" {
		t.Errorf("Modified = %+v, want exactly [Modified]", delta.Modified)
	}
	if len(delta.Moved) != 1 || delta.Moved[0].QualifiedName != "Moved" ||
		delta.Moved[0].OldFile != "before.go" || delta.Moved[0].NewFile != "after.go" {
		t.Errorf("Moved = %+v, want exactly one {Moved, before.go, after.go}", delta.Moved)
	}

	for _, s := range delta.Created {
		if s.QualifiedName == "Untouched" {
			t.Error("Untouched symbol appeared in Created")
		}
	}
	for _, s := range delta.Modified {
		if s.QualifiedName == "Untouched" {
			t.Error("Untouched symbol appeared in Modified")
		}
	}
	for _, s := range delta.Deleted {
		if s.QualifiedName == "Untouched" {
			t.Error("Untouched symbol appeared in Deleted")
		}
	}
}

// TestDiffSymbols_ModifiedRequiresLineIntersection: a symbol in a changed
// file whose OWN line range does not intersect changedLines must NOT be
// classified as modified.
func TestDiffSymbols_ModifiedRequiresLineIntersection(t *testing.T) {
	touched := Symbol{Key: SymbolKey("f.go", "Touched"), QualifiedName: "Touched", File: "f.go", StartLine: 10, EndLine: 15}
	untouched := Symbol{Key: SymbolKey("f.go", "Untouched"), QualifiedName: "Untouched", File: "f.go", StartLine: 1, EndLine: 5}

	base := map[string][]Symbol{"f.go": {touched, untouched}}
	head := map[string][]Symbol{"f.go": {touched, untouched}}
	changedLines := map[string][]int{"f.go": {12}}

	delta := DiffSymbols(base, head, nil, changedLines)

	if len(delta.Modified) != 1 || delta.Modified[0].QualifiedName != "Touched" {
		t.Errorf("Modified = %+v, want exactly [Touched]", delta.Modified)
	}
}

// TestSymbolKey_IsFileColonQualifiedName pins SymbolKey's exact format.
func TestSymbolKey_IsFileColonQualifiedName(t *testing.T) {
	if got, want := SymbolKey("a/b.go", "Foo"), "a/b.go:Foo"; got != want {
		t.Errorf("SymbolKey() = %q, want %q", got, want)
	}
}

// TestMatchGlobs_DoublestarNesting covers `**` matching across directory
// boundaries.
func TestMatchGlobs_DoublestarNesting(t *testing.T) {
	if !MatchGlobs("internal/install/assets/agents/foo.md", []string{"internal/install/assets/**"}) {
		t.Error("MatchGlobs() = false, want true for a nested path under a ** glob")
	}
	if MatchGlobs("internal/other/foo.go", []string{"internal/install/assets/**"}) {
		t.Error("MatchGlobs() = true, want false for a path outside the glob")
	}
}

// gitRunTest and initTestGitRepo are local copies of the git-fixture
// helpers used across this package's tests (mirroring
// internal/gitrepo/git_test.go's own pair, kept independent so this
// package's tests never depend on gitrepo's test-only code).
func gitRunTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=laneaudit-test",
		"GIT_AUTHOR_EMAIL=laneaudit-test@example.com",
		"GIT_COMMITTER_NAME=laneaudit-test",
		"GIT_COMMITTER_EMAIL=laneaudit-test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func initTestGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRunTest(t, dir, "init", "-b", "main")
	gitRunTest(t, dir, "config", "user.email", "laneaudit-test@example.com")
	gitRunTest(t, dir, "config", "user.name", "laneaudit-test")
	gitRunTest(t, dir, "config", "commit.gpgsign", "false")

	if err := os.WriteFile(filepath.Join(dir, "committed.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatalf("write committed.txt: %v", err)
	}
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "initial")
	return dir
}
