package risk

import (
	"go/build"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// allowedNonStdlib is the CLOSED set of non-stdlib imports internal/risk may
// use: none. internal/enforcement (SPEC-185) imports this package, and its
// own leaf test admits only internal/shell, so anything beyond the standard
// library here would break that guard or create a cycle.
var allowedNonStdlib = []string{}

// forbiddenImports keeps the package free of I/O: it decides what a
// declaration or a pattern table means and never reads a file or runs a
// process, so it stays safe to call from a hook's decision layer.
var forbiddenImports = []string{"os", "os/exec"}

func packageDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(thisFile)
}

// TestLeafPackage_Imports guards SPEC-184 Dp1: internal/risk imports only
// the standard library — never internal/model or any other internal/*
// package — and none of its non-test files imports os or os/exec.
func TestLeafPackage_Imports(t *testing.T) {
	dir := packageDir(t)

	pkg, err := build.ImportDir(dir, 0)
	if err != nil {
		t.Fatalf("build.ImportDir: %v", err)
	}
	if len(pkg.GoFiles) == 0 {
		t.Fatal("internal/risk has zero non-test Go files — the leaf guard below would pass vacuously")
	}

	for _, imp := range append(append([]string{}, pkg.Imports...), pkg.TestImports...) {
		allowed := false
		for _, a := range allowedNonStdlib {
			if imp == a {
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
		if strings.Contains(first, ".") {
			t.Errorf("internal/risk imports %q — leaf package must import only the standard library", imp)
		}
	}

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
			for _, bad := range forbiddenImports {
				if imp.Path.Value == `"`+bad+`"` {
					t.Errorf("%s imports %s — internal/risk must stay free of I/O", path, bad)
				}
			}
		}
	}
}
