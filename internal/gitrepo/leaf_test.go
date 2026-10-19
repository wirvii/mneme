package gitrepo

import (
	"go/build"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestLeafPackage_Imports guards SPEC-158 D1.a: internal/gitrepo must import
// only the standard library — never internal/model, internal/service, or
// any other internal/* package.
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

	// Positive anchor: without this, the guard below passes vacuously the
	// day the package is left with zero Go files.
	if len(pkg.GoFiles) == 0 {
		t.Fatal("internal/gitrepo has zero non-test Go files — the leaf guard below would pass vacuously")
	}

	for _, imp := range append(append([]string{}, pkg.Imports...), pkg.TestImports...) {
		first := imp
		if idx := strings.Index(imp, "/"); idx >= 0 {
			first = imp[:idx]
		}
		if strings.Contains(first, ".") {
			t.Errorf("internal/gitrepo imports %q — leaf package must import only the standard library", imp)
		}
	}
}
