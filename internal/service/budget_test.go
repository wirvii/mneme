package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/quality"
)

// gitRunBudgetTest runs git with a local, explicit identity and disabled
// GPG signing (R-C) — mirrors internal/quality/git_test.go's own helper so
// this real-repository guardian never depends on the machine's own
// ~/.gitconfig.
func gitRunBudgetTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=budget-test",
		"GIT_AUTHOR_EMAIL=budget-test@example.com",
		"GIT_COMMITTER_NAME=budget-test",
		"GIT_COMMITTER_EMAIL=budget-test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// TestRealGitChain_SymbolDelta is the D20 point 1 / plan-P8 guardian: a
// REAL git repository in t.TempDir(), REAL Go source, and the REAL Go
// extractor (via symbolExtractorAdapter) — not a fake — carried through
// Git.FileAtRef -> CollectSymbols -> DiffSymbols, asserting AC7's five
// classifications symbol by symbol: a function added, one deleted, one
// whose body changed (modified), one left untouched (must not appear
// anywhere), and a file renamed with its function unchanged inside (moved,
// never created+deleted). This is emphatically not the mutation-testing
// recursion the plan calls out: it is this repository's OWN, disposable
// fixture, never mneme's own source tree.
func TestRealGitChain_SymbolDelta(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}

	dir := t.TempDir()
	gitRunBudgetTest(t, dir, "init", "-b", "main")
	gitRunBudgetTest(t, dir, "config", "user.email", "budget-test@example.com")
	gitRunBudgetTest(t, dir, "config", "user.name", "budget-test")
	gitRunBudgetTest(t, dir, "config", "commit.gpgsign", "false")

	write := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	// Base commit: three files, each with one top-level function.
	write("deleted.go", "package fixture\n\nfunc ToBeDeleted() int {\n\treturn 1\n}\n")
	write("modified.go", "package fixture\n\nfunc Modified() int {\n\treturn 1\n}\n")
	write("untouched.go", "package fixture\n\nfunc Untouched() int {\n\treturn 1\n}\n")
	write("before.go", "package fixture\n\nfunc Moved() int {\n\treturn 1\n}\n")
	gitRunBudgetTest(t, dir, "add", ".")
	gitRunBudgetTest(t, dir, "commit", "-m", "base")
	base := strings.TrimSpace(gitRunBudgetTest(t, dir, "rev-parse", "HEAD"))

	// HEAD commit: delete deleted.go, change modified.go's body, add
	// created.go, leave untouched.go alone, and rename before.go->after.go
	// WITHOUT touching its function body.
	if err := os.Remove(filepath.Join(dir, "deleted.go")); err != nil {
		t.Fatalf("remove deleted.go: %v", err)
	}
	write("modified.go", "package fixture\n\nfunc Modified() int {\n\treturn 2\n}\n")
	write("created.go", "package fixture\n\nfunc Created() int {\n\treturn 1\n}\n")
	if err := os.Rename(filepath.Join(dir, "before.go"), filepath.Join(dir, "after.go")); err != nil {
		t.Fatalf("rename before.go: %v", err)
	}
	gitRunBudgetTest(t, dir, "add", "-A")
	gitRunBudgetTest(t, dir, "commit", "-m", "head")

	g := &quality.Git{RepoDir: dir}
	changes, err := g.ChangedFilesInRange(base, "HEAD")
	if err != nil {
		t.Fatalf("ChangedFilesInRange: %v", err)
	}

	renames := map[string]string{}
	var basePaths, headPaths []string
	for _, c := range changes {
		switch c.Status {
		case quality.FileStatusRenamed:
			renames[c.Path] = c.OldPath
			basePaths = append(basePaths, c.OldPath)
			headPaths = append(headPaths, c.Path)
		case quality.FileStatusDeleted:
			basePaths = append(basePaths, c.Path)
		case quality.FileStatusAdded:
			headPaths = append(headPaths, c.Path)
		default: // modified
			basePaths = append(basePaths, c.Path)
			headPaths = append(headPaths, c.Path)
		}
	}
	// untouched.go was NOT reported by git as changed — proving AC9's own
	// property (only the delta's own files are ever read) requires it to
	// be absent from headPaths/basePaths, which it already is (git itself
	// never reported it).

	changedLines, err := g.ChangedLines(base, "HEAD")
	if err != nil {
		t.Fatalf("ChangedLines: %v", err)
	}

	ex := symbolExtractorAdapter{}
	baseSymbols, baseRefs, err := quality.CollectSymbols(g, base, basePaths, ex)
	if err != nil {
		t.Fatalf("CollectSymbols(base): %v", err)
	}
	headSymbols, headRefs, err := quality.CollectSymbols(g, "HEAD", headPaths, ex)
	if err != nil {
		t.Fatalf("CollectSymbols(HEAD): %v", err)
	}
	_ = baseRefs
	_ = headRefs

	delta := quality.DiffSymbols(baseSymbols, headSymbols, renames, changedLines, nil)

	assertContains := func(t *testing.T, syms []quality.Symbol, name string) {
		t.Helper()
		for _, s := range syms {
			if s.QualifiedName == name {
				return
			}
		}
		t.Errorf("expected %q in %+v", name, syms)
	}
	assertAbsent := func(t *testing.T, name string) {
		t.Helper()
		for _, s := range delta.Created {
			if s.QualifiedName == name {
				t.Errorf("%q must not appear in Created", name)
			}
		}
		for _, s := range delta.Modified {
			if s.QualifiedName == name {
				t.Errorf("%q must not appear in Modified", name)
			}
		}
		for _, s := range delta.Deleted {
			if s.QualifiedName == name {
				t.Errorf("%q must not appear in Deleted", name)
			}
		}
	}

	assertContains(t, delta.Created, "Created")
	assertContains(t, delta.Deleted, "ToBeDeleted")
	assertContains(t, delta.Modified, "Modified")
	assertAbsent(t, "Untouched")

	if len(delta.Moved) != 1 || delta.Moved[0].QualifiedName != "Moved" {
		t.Fatalf("Moved = %+v, want exactly one {Moved}", delta.Moved)
	}
	if delta.Moved[0].OldFile != "before.go" || delta.Moved[0].NewFile != "after.go" {
		t.Errorf("Moved = %+v, want OldFile=before.go NewFile=after.go", delta.Moved[0])
	}
}
