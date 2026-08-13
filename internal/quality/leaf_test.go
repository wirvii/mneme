package quality

import (
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// allowedNonStdlib is the CLOSED set of non-stdlib imports internal/quality
// may use (AC1/D7): github.com/pelletier/go-toml/v2 for the constitution's
// TOML, and github.com/bmatcuk/doublestar/v4 for validating and evaluating
// `exclude` glob patterns — already the module's canonical glob engine
// (internal/rules), so this is a second use, not a new dependency of the
// PROJECT (V6), only of this leaf package.
var allowedNonStdlib = []string{
	"github.com/pelletier/go-toml/v2",
	"github.com/bmatcuk/doublestar/v4",
}

// TestLeafPackage_OnlyImportsStdlibAndTOML guards AC1: internal/quality must
// import only the standard library plus the two allowedNonStdlib entries —
// never internal/model, internal/store, internal/service, or any other
// internal/* package. Copy of internal/profile/leaf_test.go's pattern
// (itself following internal/enforcement's import guard, SPEC-056 D5).
func TestLeafPackage_OnlyImportsStdlibAndTOML(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile)

	pkg, err := build.ImportDir(dir, 0)
	if err != nil {
		t.Fatalf("build.ImportDir: %v", err)
	}

	// G1 positive anchor (plan P1/P3): without this, the guard below passes
	// vacuously the day the package is left with zero Go files, or stops
	// depending on one of allowedNonStdlib (e.g. a rewrite that hand-rolls
	// TOML parsing or glob matching). Neither should ever go unnoticed as
	// "still a valid leaf" — AND both entries must be present, not just
	// one: a registry of one is not the property this guard exists to
	// prove (SPEC-116 D7 — "el guardián de hoja se amplía a un conjunto de
	// dos, no se relaja").
	if len(pkg.GoFiles) == 0 {
		t.Fatal("internal/quality has zero non-test Go files — the leaf guard below would pass vacuously")
	}

	found := make(map[string]bool, len(allowedNonStdlib))
	for _, imp := range append(append([]string{}, pkg.Imports...), pkg.TestImports...) {
		allowed := false
		for _, a := range allowedNonStdlib {
			if imp == a {
				found[a] = true
				allowed = true
				break
			}
		}
		if allowed {
			continue
		}
		// No dot in the first path element: stdlib convention (e.g. "fmt",
		// "strings", "go/build"). Non-stdlib module paths always contain a
		// domain (a dot) in their first segment.
		first := imp
		if idx := strings.Index(imp, "/"); idx >= 0 {
			first = imp[:idx]
		}
		if !strings.Contains(first, ".") {
			continue
		}
		t.Errorf("internal/quality imports %q — leaf package must import only stdlib + %v", imp, allowedNonStdlib)
	}

	for _, a := range allowedNonStdlib {
		if !found[a] {
			t.Errorf("internal/quality no longer imports %s — update allowedNonStdlib and this anchor together if that is intentional", a)
		}
	}
}

// pureSourceFiles are the source-of-truth-free files that must NEVER shell
// out — the criteria vocabulary (SPEC-117) and the budget/symbol-delta
// machinery (SPEC-118 S4) are pure functions of already-collected tree
// facts; all I/O of any kind belongs in git.go, and ONLY git.go. Now
// complete with all four SPEC-118 files (budget.go/symbols.go/
// budgeteval.go/detections.go, P1/P3/P4/P5) — build.ImportDir aggregates
// the WHOLE package's imports (G1b), so the mutation that proves this
// guard is not vacuous must touch all four at once, not just one.
var pureSourceFiles = []string{
	"criteria.go", "evaluate.go", "report.go",
	"budget.go", "symbols.go", "budgeteval.go", "detections.go",
}

// TestPureFiles_NeverImportOSExec is AC1's negative hermana: proof, not
// assertion, that criteria.go/evaluate.go never import os/exec — the
// evaluator's whole reason for existing is to be testable with tables
// instead of a real git repo per row, and an accidental `exec.Command`
// slipped into either file would silently defeat that (G1b).
func TestPureFiles_NeverImportOSExec(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile)

	fset := token.NewFileSet()
	for _, name := range pureSourceFiles {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("pureSourceFiles names %s but it does not exist: %v", name, err)
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, imp := range f.Imports {
			if imp.Path.Value == `"os/exec"` {
				t.Errorf("%s imports os/exec — this file must stay a pure function of already-collected tree facts", name)
			}
		}
	}
}
