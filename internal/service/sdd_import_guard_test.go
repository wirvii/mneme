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
// sddD33WrapperNames is the closed set of the nine *SDDService wrapper
// methods D33 requires every materializing write to go through
// (sdd_export.go) — the same nine sddStoreMutators (sdd_export_guard_test.go)
// declares `true` for. The importer must call NONE of them (D52): it
// writes through its own seven *FromRecord/Merge* methods instead, which
// never materialize.
var sddD33WrapperNames = map[string]bool{
	"createBacklogItem":       true,
	"updateBacklogItem":       true,
	"appendBacklogRefinement": true,
	"createSpec":              true,
	"updateSpecStatus":        true,
	"updateSpecBaseSHA":       true,
	"updateSpecLaneScope":     true,
	"createPushback":          true,
	"resolvePushback":         true,
}

// materializationGuardViolations walks f (a parsed sdd_import.go, or a
// fixture shaped like it) and reports how many times materializeBacklogItem
// / materializeSpec are called, whether either is called OUTSIDE a function
// named rewriteCompletedRecord, and which D33 wrapper names (if any) are
// called anywhere. Factored out of the test body so
// TestSDDImport_MaterializationGuardIgnoresComments can drive the exact
// same walk over a small fixture without duplicating the AST logic — the
// only way to prove the guard is not hysteric without risking the proof
// itself drifting from what the real guard checks.
func materializationGuardViolations(f *ast.File) (backlogCalls, specCalls int, outsideCalls, wrapperCalls []string) {
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		isRewrite := fn.Name.Name == "rewriteCompletedRecord"

		ast.Inspect(fn, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch {
			case sel.Sel.Name == "materializeBacklogItem":
				backlogCalls++
				if !isRewrite {
					outsideCalls = append(outsideCalls, "materializeBacklogItem in "+fn.Name.Name)
				}
			case sel.Sel.Name == "materializeSpec":
				specCalls++
				if !isRewrite {
					outsideCalls = append(outsideCalls, "materializeSpec in "+fn.Name.Name)
				}
			case sddD33WrapperNames[sel.Sel.Name]:
				wrapperCalls = append(wrapperCalls, sel.Sel.Name)
			}
			return true
		})
	}
	return backlogCalls, specCalls, outsideCalls, wrapperCalls
}

// TestSDDImport_HasExactlyOneMaterializationSite is AC13 (D52): sdd_import.go
// may call materializeBacklogItem/materializeSpec AT MOST ONCE EACH, ONLY
// from inside rewriteCompletedRecord, and must never call any of the nine
// D33 wrappers.
func TestSDDImport_HasExactlyOneMaterializationSite(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, sddImportGoPath(t), nil, 0)
	if err != nil {
		t.Fatalf("sdd import guard: parse sdd_import.go: %v", err)
	}

	backlogCalls, specCalls, outsideCalls, wrapperCalls := materializationGuardViolations(f)

	if backlogCalls > 1 {
		t.Errorf("materializeBacklogItem is called %d times in sdd_import.go, want at most 1 (SPEC-131 D52)", backlogCalls)
	}
	if specCalls > 1 {
		t.Errorf("materializeSpec is called %d times in sdd_import.go, want at most 1 (SPEC-131 D52)", specCalls)
	}
	for _, v := range outsideCalls {
		t.Errorf("%s is called outside rewriteCompletedRecord (SPEC-131 D52)", v)
	}
	for _, w := range wrapperCalls {
		t.Errorf("sdd_import.go calls %s(...) — the importer must never call a D33 wrapper (SPEC-131 D52)", w)
	}
}

// TestSDDImport_MaterializationGuardIgnoresComments proves the guard above
// is not hysteric (the technique SPEC-130's own schema-range guard used,
// applied here to a different guardian): a source that differs from a
// compliant shape ONLY by an added comment must still pass cleanly.
func TestSDDImport_MaterializationGuardIgnoresComments(t *testing.T) {
	const src = `package service

// rewriteCompletedRecord is documented here on purpose — a harmless
// comment change must never trip this guardian (the point of this test).
func (svc *SDDService) rewriteCompletedRecord() {
	svc.materializeBacklogItem(nil, "")
	svc.materializeSpec(nil, "")
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "fixture.go", src, 0)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}

	backlogCalls, specCalls, outsideCalls, wrapperCalls := materializationGuardViolations(f)
	if backlogCalls != 1 || specCalls != 1 || len(outsideCalls) != 0 || len(wrapperCalls) != 0 {
		t.Fatalf("a comment-only change must not trip the materialization guard: backlog=%d spec=%d outside=%v wrappers=%v",
			backlogCalls, specCalls, outsideCalls, wrapperCalls)
	}
}

// Mutaciones exigidas (AC13, ejecutadas y revertidas byte a byte durante la
// implementacion; resultado real en changes.md):
//  1. Anadir una segunda llamada a materializeSpec en otra funcion de
//     sdd_import.go -> TestSDDImport_HasExactlyOneMaterializationSite en
//     rojo por (a) el recuento > 1 y (c) la llamada fuera de
//     rewriteCompletedRecord.
//  2. Mover la llamada a materializeBacklogItem/materializeSpec a la rama
//     comun (fuera de rewriteCompletedRecord) -> rojo por (c).
//  3. Llamar a `svc.updateSpecStatus` (un envoltorio de D33) desde
//     importSpecRecord -> rojo por (d), nombrando "updateSpecStatus".
