package acceptance

import (
	"go/build"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// allowedNonStdlib is the CLOSED set of non-stdlib imports internal/acceptance
// may use: github.com/pelletier/go-toml/v2 for the criteria document's TOML.
var allowedNonStdlib = []string{
	"github.com/pelletier/go-toml/v2",
}

// TestLeafPackage_Imports guards SPEC-158 D1.c: internal/acceptance must
// import only the standard library plus go-toml/v2 — never internal/model
// or any other internal/* package.
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
		t.Fatal("internal/acceptance has zero non-test Go files — the leaf guard below would pass vacuously")
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
		t.Errorf("internal/acceptance imports %q — leaf package must import only stdlib + %v", imp, allowedNonStdlib)
	}

	for _, a := range allowedNonStdlib {
		if !found[a] {
			t.Errorf("internal/acceptance no longer imports %s — update allowedNonStdlib and this anchor together if that is intentional", a)
		}
	}
}
