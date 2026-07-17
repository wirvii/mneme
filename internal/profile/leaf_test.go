package profile

import (
	"go/build"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestLeafPackage_OnlyImportsStdlibAndTOML guards AC1: internal/profile must
// import only the standard library plus github.com/pelletier/go-toml/v2 —
// never internal/model, internal/store, internal/service, or any other
// internal/* package. Same perimeter and same pattern as
// internal/enforcement's import guard (SPEC-056 D5).
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

	const allowedNonStdlib = "github.com/pelletier/go-toml/v2"

	for _, imp := range append(append([]string{}, pkg.Imports...), pkg.TestImports...) {
		if imp == allowedNonStdlib {
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
		t.Errorf("internal/profile imports %q — leaf package must import only stdlib + %s", imp, allowedNonStdlib)
	}
}
