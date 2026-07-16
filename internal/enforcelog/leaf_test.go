package enforcelog

import (
	"go/build"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestLeafPackage_OnlyImportsStdlib guards SPEC-086 D3: internal/enforcelog
// must import the standard library only — no config/model/store/service —
// mirroring internal/querylog and internal/enforcement's own leaf
// guardians. Mutation check: adding an import of, say,
// "github.com/wirvii/mneme/internal/config" turns this test red.
func TestLeafPackage_OnlyImportsStdlib(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile)

	pkg, err := build.ImportDir(dir, 0)
	if err != nil {
		t.Fatalf("build.ImportDir: %v", err)
	}

	for _, imp := range append(append([]string{}, pkg.Imports...), pkg.TestImports...) {
		first := imp
		if idx := strings.Index(imp, "/"); idx >= 0 {
			first = imp[:idx]
		}
		if !strings.Contains(first, ".") {
			// No dot in the first path element: stdlib convention.
			continue
		}
		t.Errorf("internal/enforcelog imports %q — leaf package must import only the standard library", imp)
	}
}
