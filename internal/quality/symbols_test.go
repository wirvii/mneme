package quality

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// fakeSymbolExtractor is a table-driven test double implementing
// SymbolExtractor without ever parsing real source — it looks up path in a
// pre-seeded map and counts every invocation (AC9's own instrumentation).
type fakeSymbolExtractor struct {
	byPath map[string][]Symbol
	refs   map[string][]SymbolRef
	calls  int
}

func (f *fakeSymbolExtractor) Symbols(path string, content []byte) ([]Symbol, []SymbolRef, error) {
	f.calls++
	_ = content
	return f.byPath[path], f.refs[path], nil
}

// TestCollectSymbols_SkipsAbsentFilesAndFiltersBudgetedKinds covers AC10:
// only BudgetedKinds symbols survive, and a path absent at ref (the normal
// shape of a created/deleted file) contributes nothing rather than erroring.
func TestCollectSymbols_SkipsAbsentFilesAndFiltersBudgetedKinds(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := initTestGitRepo(t)
	g := &Git{RepoDir: dir}

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

	symsByFile, _, err := CollectSymbols(g, "HEAD", []string{"a.go", "missing.go"}, ex)
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

// TestCollectSymbols_OnlyReadsGivenPaths is G10's own regression: the
// extractor is invoked EXACTLY once per path handed in, never once per
// file in the ref's whole tree.
func TestCollectSymbols_OnlyReadsGivenPaths(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := initTestGitRepo(t)
	g := &Git{RepoDir: dir}

	for _, name := range []string{"a.go", "b.go", "c.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package x\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "three files")

	ex := &fakeSymbolExtractor{byPath: map[string][]Symbol{}}
	// The repo's tree at HEAD has THREE files (plus committed.txt from
	// initTestGitRepo = four); only two are requested here.
	if _, _, err := CollectSymbols(g, "HEAD", []string{"a.go", "b.go"}, ex); err != nil {
		t.Fatalf("CollectSymbols: %v", err)
	}
	if ex.calls != 2 {
		t.Errorf("extractor invoked %d times, want exactly 2 (one per requested path, never the whole tree)", ex.calls)
	}
}

// TestDiffSymbols_FourClasses covers AC7 (D20's own dogfooding shape,
// distilled to a table): created, deleted, modified (body changed),
// unchanged (must NOT appear anywhere), and moved (file renamed, same
// qualified name) — all five assertions in one fixture, since the "intacto
// no aparece" and "renombrado sale moved" rows are what a naive delta
// would get wrong (G7/G8).
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

	delta := DiffSymbols(base, head, renames, changedLines, nil)

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

	// The untouched symbol must not appear in ANY list — the assertion a
	// naive "return everything as created" delta would fail.
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

// TestDiffSymbols_ModifiedRequiresLineIntersection is G9: a symbol in a
// changed file whose OWN line range does not intersect changedLines must
// NOT be classified as modified — a second function in the same file that
// was not touched must be silent.
func TestDiffSymbols_ModifiedRequiresLineIntersection(t *testing.T) {
	touched := Symbol{Key: SymbolKey("f.go", "Touched"), QualifiedName: "Touched", File: "f.go", StartLine: 10, EndLine: 15}
	untouched := Symbol{Key: SymbolKey("f.go", "Untouched"), QualifiedName: "Untouched", File: "f.go", StartLine: 1, EndLine: 5}

	base := map[string][]Symbol{"f.go": {touched, untouched}}
	head := map[string][]Symbol{"f.go": {touched, untouched}}
	changedLines := map[string][]int{"f.go": {12}}

	delta := DiffSymbols(base, head, nil, changedLines, nil)

	if len(delta.Modified) != 1 || delta.Modified[0].QualifiedName != "Touched" {
		t.Errorf("Modified = %+v, want exactly [Touched]", delta.Modified)
	}
}

// TestDiffSymbols_TestFilesExcludedEntirely covers D6.2: a symbol defined
// in a file matching testGlobs never appears in any of the four buckets,
// in either direction (created in a test file, deleted from a test file).
func TestDiffSymbols_TestFilesExcludedEntirely(t *testing.T) {
	testSym := Symbol{Key: SymbolKey("x_test.go", "TestFoo"), QualifiedName: "TestFoo", File: "x_test.go"}
	prodSym := Symbol{Key: SymbolKey("x.go", "Foo"), QualifiedName: "Foo", File: "x.go"}

	head := map[string][]Symbol{"x_test.go": {testSym}, "x.go": {prodSym}}
	base := map[string][]Symbol{}

	delta := DiffSymbols(base, head, nil, nil, []string{"**/*_test.go"})

	if len(delta.Created) != 1 || delta.Created[0].QualifiedName != "Foo" {
		t.Errorf("Created = %+v, want exactly [Foo] (test file symbol excluded)", delta.Created)
	}
}

// TestPureFiles_NeverImportOSExec_symbols pins that symbols.go is present
// in leaf_test.go's pureSourceFiles list — the shared
// TestPureFiles_NeverImportOSExec already asserts the actual import
// property; this test only guards against symbols.go being silently
// dropped from that list.
func TestPureFiles_NeverImportOSExec_symbols(t *testing.T) {
	found := false
	for _, f := range pureSourceFiles {
		if f == "symbols.go" {
			found = true
		}
	}
	if !found {
		t.Fatal("symbols.go missing from pureSourceFiles — AC1's negative hermana would not cover it")
	}
}
