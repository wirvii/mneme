package quality

import (
	"go/build"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestLeafPackage_OnlyImportsStdlibAndTOML guards AC1: internal/quality must
// import only the standard library plus github.com/pelletier/go-toml/v2 —
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

	// G1 positive anchor (plan P1): without this, the guard below passes
	// vacuously the day the package is left with zero Go files, or stops
	// depending on go-toml/v2 (e.g. a rewrite that hand-rolls TOML parsing).
	// Neither should ever go unnoticed as "still a valid leaf".
	if len(pkg.GoFiles) == 0 {
		t.Fatal("internal/quality has zero non-test Go files — the leaf guard below would pass vacuously")
	}

	const allowedNonStdlib = "github.com/pelletier/go-toml/v2"

	foundTOML := false
	for _, imp := range append(append([]string{}, pkg.Imports...), pkg.TestImports...) {
		if imp == allowedNonStdlib {
			foundTOML = true
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
		t.Errorf("internal/quality imports %q — leaf package must import only stdlib + %s", imp, allowedNonStdlib)
	}

	if !foundTOML {
		t.Error("internal/quality no longer imports go-toml/v2 — update the guard's allowedNonStdlib and this anchor together if that is intentional")
	}
}
