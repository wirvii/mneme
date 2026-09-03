package gitattrs

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// isStdlibImport reports whether path looks like a standard library
// import: no dot in the first path segment. Same heuristic
// internal/sddfile/leaf_test.go and internal/quality/leaf_test.go already
// use for their own perimeter guardians.
func isStdlibImport(path string) bool {
	first := path
	if idx := strings.Index(path, "/"); idx >= 0 {
		first = path[:idx]
	}
	return !strings.Contains(first, ".")
}

// TestGitattrsPackage_ImportsOnlyStdlib proves internal/gitattrs stays a
// strict leaf: stdlib only, no internal/model, no internal/service —
// nothing that could resolve the environment or grow this package into a
// second consumer's junk drawer. Every non-test .go file in this directory
// is parsed and its import list checked.
func TestGitattrsPackage_ImportsOnlyStdlib(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("gitattrs leaf test: could not resolve its own source path")
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
			if !isStdlibImport(importPath) {
				t.Errorf("%s imports %q, which is outside internal/gitattrs's declared perimeter (stdlib only)", name, importPath)
			}
		}
	}
}
