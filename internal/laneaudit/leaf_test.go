package laneaudit

import (
	"go/build"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// allowedNonStdlib is the CLOSED set of non-stdlib imports internal/laneaudit
// may use: doublestar for glob matching, and its sibling leaf
// internal/gitrepo for git primitives.
var allowedNonStdlib = []string{
	"github.com/bmatcuk/doublestar/v4",
	"github.com/wirvii/mneme/internal/gitrepo",
}

// TestLeafPackage_Imports guards SPEC-158 D1.b: internal/laneaudit must
// import only the standard library plus the two allowedNonStdlib entries —
// both anchored present — never internal/model or any other internal/*
// package besides internal/gitrepo.
func TestLeafPackage_Imports(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile)

	pkg, err := build.ImportDir(dir, 0)
	if err != nil {
		t.Fatalf("build.ImportDir: %v", err)
	}

	if len(pkg.GoFiles) == 0 {
		t.Fatal("internal/laneaudit has zero non-test Go files — the leaf guard below would pass vacuously")
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
		first := imp
		if idx := strings.Index(imp, "/"); idx >= 0 {
			first = imp[:idx]
		}
		if !strings.Contains(first, ".") {
			continue
		}
		t.Errorf("internal/laneaudit imports %q — leaf package must import only stdlib + %v", imp, allowedNonStdlib)
	}

	for _, a := range allowedNonStdlib {
		if !found[a] {
			t.Errorf("internal/laneaudit no longer imports %s — update allowedNonStdlib and this anchor together if that is intentional", a)
		}
	}
}

// TestLeafPackage_NeverImportsOSExec guards the same purity property
// internal/gitrepo itself must have: os/exec belongs exclusively to the git
// primitives package, never to the pure symbol-delta/budget arithmetic
// here.
func TestLeafPackage_NeverImportsOSExec(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile)

	entries, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	fset := token.NewFileSet()
	for _, path := range entries {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, imp := range f.Imports {
			if imp.Path.Value == `"os/exec"` {
				t.Errorf("%s imports os/exec — internal/laneaudit's own files must stay pure functions over already-collected facts (git I/O belongs in internal/gitrepo)", path)
			}
		}
	}
}
