// Package service — this file is AC6's structural guardian: an AST check
// that none of the SDD mechanism's own files resolve the working
// directory, HOME, or git identity themselves. D38 fixed this by
// construction (repoRoot is always a caller-supplied parameter); this test
// is what makes that claim checkable rather than assumed.
package service

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"
)

// sddAmbientGuardFiles are the exact files AC6 names:
// internal/service/sdd_{export,state,enable}.go, plus internal/sddfile's
// own leaf test already covers that package's perimeter directly (AC5).
func sddAmbientGuardFiles(t *testing.T) []string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("sdd ambient guard: runtime.Caller(0) failed")
	}
	dir := filepath.Dir(thisFile)
	return []string{
		filepath.Join(dir, "sdd_export.go"),
		filepath.Join(dir, "sdd_state.go"),
		filepath.Join(dir, "sdd_enable.go"),
	}
}

// forbiddenAmbientSelectors is AC6's closed list: os.Getwd, os.UserHomeDir,
// and anything selected off the gitident package.
var forbiddenAmbientSelectors = map[string]bool{
	"Getwd":       true, // os.Getwd
	"UserHomeDir": true, // os.UserHomeDir
}

// TestSDDGitNative_NeverResolvesAmbientPaths is AC6: none of the three
// files reference os.Getwd, os.UserHomeDir, or any gitident.* selector.
func TestSDDGitNative_NeverResolvesAmbientPaths(t *testing.T) {
	fset := token.NewFileSet()
	for _, path := range sddAmbientGuardFiles(t) {
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}

		// gitident import at all is already disqualifying.
		for _, imp := range f.Imports {
			if imp.Path.Value == `"github.com/wirvii/mneme/internal/gitident"` {
				t.Errorf("%s imports internal/gitident — the SDD mechanism must never resolve git identity (D38)", path)
			}
		}

		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkgIdent, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if pkgIdent.Name == "os" && forbiddenAmbientSelectors[sel.Sel.Name] {
				t.Errorf("%s references os.%s — repoRoot must always be a parameter (D38)", path, sel.Sel.Name)
			}
			if pkgIdent.Name == "gitident" {
				t.Errorf("%s references gitident.%s — the SDD mechanism must never resolve git identity (D38)", path, sel.Sel.Name)
			}
			return true
		})
	}
}
