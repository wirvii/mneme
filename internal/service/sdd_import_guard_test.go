// Package service — this file is SPEC-131 §2b's structural guardians for
// the importer: D59's "never compares timestamps" (AC6b) lands here in
// commit 4, the file's own creation; D52's "exactly one materialization
// site" (AC13) arrives in the commit that adds rewriteCompletedRecord.
package service

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"
)

// sddImportGoPath resolves internal/service/sdd_import.go relative to this
// test file, so the guard works regardless of the working directory
// `go test` was invoked from.
func sddImportGoPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("sdd import guard: runtime.Caller(0) failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "sdd_import.go")
}

// TestSDDImport_NeverComparesTimestamps is AC6b (D59): sdd_import.go must
// never call a `.After(` or `.Before(` selector anywhere in its source.
// The importer copies created_at/updated_at VERBATIM (D49) — it has no use
// for either comparison, and this guardian makes sure one never creeps
// back in disguised as an optimization.
//
// This is the structural half of D59; AC6a (TestSDDImport_FileWinsEvenWhenOlder)
// is the behavioural half, over an actual older-file fixture — a property a
// guardian over source text alone cannot observe.
func TestSDDImport_NeverComparesTimestamps(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, sddImportGoPath(t), nil, 0)
	if err != nil {
		t.Fatalf("sdd import guard: parse sdd_import.go: %v", err)
	}

	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name == "After" || sel.Sel.Name == "Before" {
			t.Errorf("sdd_import.go calls .%s(...) — the importer must never compare timestamps (SPEC-131 D59)",
				sel.Sel.Name)
		}
		return true
	})
}

// Mutacion exigida (AC6b): anadir
// `if !fileTS.After(row.UpdatedAt) { skip }` en sdd_import.go pone esta
// prueba en rojo, nombrando "After". Ejecutada y revertida durante la
// implementacion; resultado real en changes.md.
