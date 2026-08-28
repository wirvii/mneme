package sddfile

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// sddfileAllowedImports is the exact perimeter D23 declares for this
// package: the standard library plus internal/model — the same perimeter
// internal/vault has (V1), not a strict leaf's stdlib-only rule.
var sddfileAllowedImports = map[string]bool{
	"github.com/wirvii/mneme/internal/model": true,
}

// isStdlibImport reports whether path looks like a standard library
// import: no dot in the first path segment. The same heuristic
// internal/quality/leaf_test.go and internal/profile/leaf_test.go already
// use for their own perimeter guardians.
func isStdlibImport(path string) bool {
	first := path
	if idx := strings.Index(path, "/"); idx >= 0 {
		first = path[:idx]
	}
	return !strings.Contains(first, ".")
}

// TestSDDFilePackage_ImportsOnlyStdlibAndModel is AC5: internal/sddfile
// imports ONLY the standard library plus internal/model — no
// internal/gitident, no internal/store, no internal/service, nothing that
// could resolve the environment (cwd, HOME, git). Every non-test .go file
// in this directory is parsed and its import list checked.
func TestSDDFilePackage_ImportsOnlyStdlibAndModel(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("sddfile leaf test: could not resolve its own source path")
	}
	dir := filepath.Dir(thisFile)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}

	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, imp := range f.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			if isStdlibImport(importPath) {
				continue
			}
			if sddfileAllowedImports[importPath] {
				continue
			}
			t.Errorf("%s imports %q, which is outside internal/sddfile's declared perimeter "+
				"(stdlib + internal/model only, D23)", name, importPath)
		}
	}
}
