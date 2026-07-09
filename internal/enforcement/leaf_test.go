package enforcement

import (
	"go/build"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestLeafPackage_OnlyImportsStdlibAndShell guards AC14: internal/enforcement
// must import only the standard library plus internal/shell — no
// config/db/project/service, so it never gains an os.Exit, a database
// handle, or any other I/O dependency by accident.
func TestLeafPackage_OnlyImportsStdlibAndShell(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile)

	pkg, err := build.ImportDir(dir, 0)
	if err != nil {
		t.Fatalf("build.ImportDir: %v", err)
	}

	const allowedNonStdlib = "github.com/juanftp/mneme/internal/shell"

	for _, imp := range append(append([]string{}, pkg.Imports...), pkg.TestImports...) {
		if imp == allowedNonStdlib {
			continue
		}
		if !strings.Contains(imp, ".") {
			// No dot in the first path element: stdlib convention
			// (e.g. "fmt", "strings", "go/build"). Non-stdlib module
			// paths always contain a domain (contain a dot) in their
			// first segment.
			first := imp
			if idx := strings.Index(imp, "/"); idx >= 0 {
				first = imp[:idx]
			}
			if !strings.Contains(first, ".") {
				continue
			}
		}
		t.Errorf("internal/enforcement imports %q — leaf package must import only stdlib + %s", imp, allowedNonStdlib)
	}
}
