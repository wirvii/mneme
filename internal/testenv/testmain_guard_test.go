package testenv_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// isolatedPackages lists every package (relative to the module root) that
// must declare its own TestMain calling testenv.Isolate (SPEC-085 D5b AC11).
// Keep this list in sync with the actual TestMain files it guards — this
// meta-test's entire purpose is to fail loudly the day one of them is
// deleted or a new environment-touching package is added without one.
var isolatedPackages = []string{
	"internal/service",
	"internal/cli",
	"internal/mcp",
	"internal/http",
	"internal/install",
	"internal/upgrade",
}

// TestAllIsolatedPackagesDeclareTestMain parses every *_test.go file in each
// package listed in isolatedPackages and fails if none of them declares a
// top-level `func TestMain(m *testing.M)`. This is a static, deterministic
// AST check — it never builds or runs the target packages, so it cannot be
// defeated by a TestMain that exists but forgot to call testenv.Isolate (that
// class of regression is instead caught by each package's own tests going
// red if isolation actually matters for them); its job is narrower and
// cheaper: catch the day a package's TestMain file itself disappears.
func TestAllIsolatedPackagesDeclareTestMain(t *testing.T) {
	repoRoot := moduleRoot(t)

	for _, pkgRelPath := range isolatedPackages {
		pkgDir := filepath.Join(repoRoot, filepath.FromSlash(pkgRelPath))
		if !packageDeclaresTestMain(t, pkgDir) {
			t.Errorf("%s: no func TestMain(m *testing.M) found — SPEC-085 D5b requires every package that touches ambient environment state (directly or via a production constructor) to call testenv.Isolate from its own TestMain", pkgRelPath)
		}
	}
}

// moduleRoot resolves the repository root from this test file's own path —
// <repoRoot>/internal/testenv/testmain_guard_test.go — so the check works
// regardless of the directory `go test` was invoked from.
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
}

// packageDeclaresTestMain reports whether any *_test.go file directly inside
// dir declares a top-level function named TestMain.
func packageDeclaresTestMain(t *testing.T, dir string) bool {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}

	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}

		path := filepath.Join(dir, e.Name())
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}

		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			if fn.Name.Name == "TestMain" {
				return true
			}
		}
	}

	return false
}
