// Package service — this file is SPEC-130 §2a D33's structural guardian: it
// proves that every store.SDDStore write method invoked from the SDD
// engine either materializes through its wrapper (sdd_export.go) or is a
// declared, reasoned exception (InsertLaneAudit), and that no wrapped call
// escapes back into sdd.go directly.
//
// Limits, inherited from SPEC-125 R4/R6 (see spec_freeze_guard_test.go's
// own godoc, which states the identical limit for its file): this guard
// sees exactly TWO files, internal/service/sdd.go and
// internal/service/sdd_export.go. A wrapper or a store call born in a
// third file of this package is invisible to it — sddExportGuardFiles is a
// list precisely so widening this later is a one-line change. It matches
// by SELECTOR name, not by receiver (`st := svc.store; st.CreateSpec(...)`
// would escape it) — the same trade-off spec_freeze_guard_test.go's
// funcCallsSelector already accepts, and for the same reason: closing it
// requires type analysis that costs more than the risk it removes. It does
// NOT verify that materialization is CORRECT, only that the call exists —
// correctness is AC15's job, end to end.
package service

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// sddStoreMutators is the CLOSED inventory of every store.SDDStore write
// method the SDD engine invokes (SPEC-130 D33/C1). Nine are `true` — they
// must go through their wrapper in sdd_export.go and materialize.
// InsertLaneAudit is the tenth entry, declared `false` WITH ITS REASON ON
// THE SAME LINE: lane_audits does not travel to the repository until
// BL-197 (etapa 4). A blanket "no write call may bypass a wrapper" rule
// cannot be written here — it would force InsertLaneAudit through a
// wrapper that materializes something BL-197 hasn't built yet, or invite a
// silent, undeclared exception. A `false` entry is a change a reviewer
// sees in the diff; a silent filter is not (V6/D33, spec.md §15 C1).
var sddStoreMutators = map[string]bool{
	"CreateBacklogItem":       true,
	"UpdateBacklogItem":       true,
	"AppendBacklogRefinement": true,
	"CreateSpec":              true,
	"UpdateSpecStatus":        true,
	"UpdateSpecBaseSHA":       true,
	"UpdateSpecLaneScope":     true,
	"CreatePushback":          true,
	"ResolvePushback":         true,
	"InsertLaneAudit":         false, // lane_audits does not travel to the repository until BL-197 (etapa 4)

	// SPEC-131 D49/D52: the importer's own seven write methods
	// (internal/store/sdd_import.go). None goes through a D33 wrapper —
	// the importer writes rows READ FROM the file; materializing them
	// would rewrite the very file the importer just read (D46).
	"CreateBacklogItemFromRecord": false, // el importador escribe filas leidas DEL archivo; materializarlas reescribiria el archivo que acaba de leer — SPEC-131 D46
	"UpdateBacklogItemFromRecord": false, // el importador escribe filas leidas DEL archivo; materializarlas reescribiria el archivo que acaba de leer — SPEC-131 D46
	"MergeBacklogRefinements":     false, // el importador escribe filas leidas DEL archivo; materializarlas reescribiria el archivo que acaba de leer — SPEC-131 D46
	"CreateSpecFromRecord":        false, // el importador escribe filas leidas DEL archivo; materializarlas reescribiria el archivo que acaba de leer — SPEC-131 D46
	"UpdateSpecFromRecord":        false, // el importador escribe filas leidas DEL archivo; materializarlas reescribiria el archivo que acaba de leer — SPEC-131 D46
	"MergeSpecHistory":            false, // el importador escribe filas leidas DEL archivo; materializarlas reescribiria el archivo que acaba de leer — SPEC-131 D46
	"MergeSpecPushbacks":          false, // el importador escribe filas leidas DEL archivo; materializarlas reescribiria el archivo que acaba de leer — SPEC-131 D46
}

// sddExportGuardFiles are the THREE files this guard parses (SPEC-131 P1
// widens this from two to three, alongside sdd_import.go's arrival) — see
// the package godoc above for why widening it is a one-line change to this
// slice, not a rewrite.
func sddExportGuardFiles(t *testing.T) (sddGo, sddExportGo, sddImportGo string) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("sdd export guard: runtime.Caller(0) failed")
	}
	dir := filepath.Dir(thisFile)
	return filepath.Join(dir, "sdd.go"), filepath.Join(dir, "sdd_export.go"), filepath.Join(dir, "sdd_import.go")
}

// sddStorePaths resolves internal/store/sdd.go AND internal/store/sdd_import.go
// relative to this test file (SPEC-131 P1 — the seven import methods live
// in a file of their own; parsing only sdd.go would make storeWriteMethods
// blind to all seven, and every "false" entry above would report as a
// stale, undeclared-call entry for the wrong reason), so the guard works
// regardless of the working directory `go test` was invoked from.
func sddStorePaths(t *testing.T) []string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("sdd export guard: runtime.Caller(0) failed")
	}
	// internal/service/sdd_export_guard_test.go -> internal/store/*.go
	dir := filepath.Join(filepath.Dir(thisFile), "..", "store")
	return []string{filepath.Join(dir, "sdd.go"), filepath.Join(dir, "sdd_import.go")}
}

// storeWriteMethods parses internal/store/sdd.go AND sdd_import.go and
// returns the set of *SDDStore methods whose body contains a call to
// ExecContext (on s.db or on a tx) — the deterministic, no-hand-written-list
// way D33 requires for discovering which store methods "write" (SPEC-130 §4
// "El inventario").
func storeWriteMethods(t *testing.T) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()

	writes := make(map[string]bool)
	for _, path := range sddStorePaths(t) {
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("sdd export guard: parse %s: %v", path, err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 {
				continue
			}
			star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			ident, ok := star.X.(*ast.Ident)
			if !ok || ident.Name != "SDDStore" {
				continue
			}
			if funcCallsSelector(fn, "ExecContext") {
				writes[fn.Name.Name] = true
			}
		}
	}
	return writes
}

// storeCallsInFile parses path and returns the set of method names called
// through a "svc.store.<Method>(...)" selector chain anywhere in the file
// — across every declaration, not just *SDDService methods, since
// sdd_export.go's wrappers are also *SDDService methods but this helper is
// deliberately receiver-agnostic beyond the literal "svc.store" prefix
// (see the package godoc's "casa por selector" limit).
func storeCallsInFile(t *testing.T, path string) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("sdd export guard: parse %s: %v", path, err)
	}

	calls := make(map[string]bool)
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		storeSel, ok := sel.X.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if storeSel.Sel.Name != "store" {
			return true
		}
		svcIdent, ok := storeSel.X.(*ast.Ident)
		if !ok || svcIdent.Name != "svc" {
			return true
		}
		calls[sel.Sel.Name] = true
		return true
	})
	return calls
}

// TestSDDStoreMutators_InventoryIsExact is AC9/AC14's first comprovación:
// the set of store.SDDStore WRITE methods that the SDD engine (sdd.go
// UNION sdd_export.go UNION sdd_import.go, SPEC-131 P1) invokes via
// "svc.store.<Method>(" must be IDENTICAL, in both directions, to
// sddStoreMutators' seventeen keys.
func TestSDDStoreMutators_InventoryIsExact(t *testing.T) {
	writeMethods := storeWriteMethods(t)

	sddGo, sddExportGo, sddImportGo := sddExportGuardFiles(t)
	calledInSddGo := storeCallsInFile(t, sddGo)
	calledInExport := storeCallsInFile(t, sddExportGo)
	calledInImport := storeCallsInFile(t, sddImportGo)

	actual := make(map[string]bool)
	for name := range calledInSddGo {
		if writeMethods[name] {
			actual[name] = true
		}
	}
	for name := range calledInExport {
		if writeMethods[name] {
			actual[name] = true
		}
	}
	for name := range calledInImport {
		if writeMethods[name] {
			actual[name] = true
		}
	}

	for name := range actual {
		if _, declared := sddStoreMutators[name]; !declared {
			t.Errorf("the SDD engine invokes store write method %s, but sddStoreMutators does not declare it "+
				"(SPEC-130 D33) — undeclared write call", name)
		}
	}
	for name := range sddStoreMutators {
		if !actual[name] {
			t.Errorf("sddStoreMutators declares %s, but no svc.store.%s( call was found in sdd.go, "+
				"sdd_export.go or sdd_import.go — remove the stale entry (SPEC-130 D33)", name, name)
		}
	}
}

// TestSDDStoreMutators_AllGoThroughWrappers is AC9's second comprobación:
// every entry declared `true` in sddStoreMutators must NOT be called
// directly from sdd.go or sdd_import.go — it must live only inside its
// wrapper in sdd_export.go. The importer's own seven methods are all
// declared `false` (D46/D52), so this loop skips them the same way it
// always skipped InsertLaneAudit.
func TestSDDStoreMutators_AllGoThroughWrappers(t *testing.T) {
	sddGo, _, sddImportGo := sddExportGuardFiles(t)
	calledInSddGo := storeCallsInFile(t, sddGo)
	calledInImport := storeCallsInFile(t, sddImportGo)

	for name, mustWrap := range sddStoreMutators {
		if !mustWrap {
			continue
		}
		if calledInSddGo[name] {
			t.Errorf("sdd.go calls store.%s(...) directly — it must go through its wrapper in "+
				"sdd_export.go instead (SPEC-130 D33)", name)
		}
		if calledInImport[name] {
			t.Errorf("sdd_import.go calls store.%s(...) directly — a materializing write method must "+
				"go through its wrapper in sdd_export.go instead (SPEC-130 D33)", name)
		}
	}
}

// wrapperNameFor derives a wrapper's method name from its store method
// name: lower-case the first rune. Every one of the nine `true` entries
// follows this exact convention (createBacklogItem, updateBacklogItem,
// appendBacklogRefinement, createSpec, updateSpecStatus, updateSpecBaseSHA,
// updateSpecLaneScope, createPushback, resolvePushback) — see
// sdd_export.go.
func wrapperNameFor(storeMethod string) string {
	if storeMethod == "" {
		return storeMethod
	}
	return strings.ToLower(storeMethod[:1]) + storeMethod[1:]
}

// sddExportMethods parses sdd_export.go and returns every top-level
// *SDDService method declared there, keyed by name.
func sddExportMethods(t *testing.T) map[string]*ast.FuncDecl {
	t.Helper()
	_, sddExportGo, _ := sddExportGuardFiles(t)
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, sddExportGo, nil, 0)
	if err != nil {
		t.Fatalf("sdd export guard: parse sdd_export.go: %v", err)
	}
	methods := make(map[string]*ast.FuncDecl)
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 {
			continue
		}
		star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		ident, ok := star.X.(*ast.Ident)
		if !ok || ident.Name != "SDDService" {
			continue
		}
		methods[fn.Name.Name] = fn
	}
	return methods
}

// TestSDDWrappers_AllMaterialize is AC9's third comprobación: each wrapper
// corresponding to a `true` entry must call materializeBacklogItem or
// materializeSpec somewhere in its body. Without this check, a wrapper
// could exist and simply forget to materialize, and the first two
// comprobaciones would both report green.
func TestSDDWrappers_AllMaterialize(t *testing.T) {
	methods := sddExportMethods(t)

	for storeMethod, mustWrap := range sddStoreMutators {
		if !mustWrap {
			continue
		}
		wrapperName := wrapperNameFor(storeMethod)
		fn, ok := methods[wrapperName]
		if !ok {
			t.Errorf("sddStoreMutators declares %s=true but sdd_export.go has no *SDDService method named %s",
				storeMethod, wrapperName)
			continue
		}
		if !funcCallsSelector(fn, "materializeBacklogItem") && !funcCallsSelector(fn, "materializeSpec") {
			t.Errorf("%s (wrapper for store.%s) does not call materializeBacklogItem or materializeSpec — "+
				"a wrapper that does not materialize defeats D33's entire purpose", wrapperName, storeMethod)
		}
	}
}

// Mutaciones exigidas (AC9, ejecutadas y revertidas byte a byte durante la
// implementacion; documentadas en changes.md con el resultado real de cada
// una):
//  1. Reintroducir `svc.store.UpdateSpecStatus(` directo en SpecAdvance
//     (en vez de `svc.updateSpecStatus(`) pone roja
//     TestSDDStoreMutators_AllGoThroughWrappers, nombrando UpdateSpecStatus.
//  2. Borrar la entrada "CreatePushback" de sddStoreMutators pone roja
//     TestSDDStoreMutators_InventoryIsExact por la direccion "llamada no
//     declarada".
//  3. Anadir una entrada "NoExisteEsteMetodo": true pone roja
//     TestSDDStoreMutators_InventoryIsExact por la direccion contraria
//     (entrada declarada, sin llamada real).
//  4. Quitar la llamada a materializeSpec de un envoltorio (p.ej.
//     updateSpecStatus) pone roja TestSDDWrappers_AllMaterialize.
//  5. Cambiar "InsertLaneAudit" de false a true pone roja
//     TestSDDStoreMutators_AllGoThroughWrappers, porque InsertLaneAudit
//     SIGUE llamandose directo desde sdd.go (LaneAudit) y ahora el
//     inventario exige que no lo haga — es la prueba de que la exencion es
//     real y no un hueco silencioso.
//
// Mutaciones exigidas por SPEC-131 AC14 (P1: las tres listas del
// inventario, ejecutadas y revertidas byte a byte durante la
// implementacion; resultado real en changes.md):
//  6. Llamar a `svc.store.UpdateSpecStatus` directo desde sdd_import.go
//     pone roja TestSDDStoreMutators_AllGoThroughWrappers, nombrando
//     "sdd_import.go calls store.UpdateSpecStatus(...) directly" — "no pasa
//     por su envoltorio".
//  7. Borrar la entrada "CreateSpecFromRecord" de sddStoreMutators pone
//     roja TestSDDStoreMutators_InventoryIsExact por "llamada no
//     declarada" — sdd_import.go sigue llamandola.
//  8. Poner "MergeSpecHistory": true pone roja
//     TestSDDStoreMutators_AllGoThroughWrappers, porque sdd_import.go la
//     llama DIRECTO y el inventario pasaria a exigir que no lo haga — la
//     prueba de que la exencion es real y no un hueco silencioso (la
//     leccion C1 de SPEC-130, aplicada aqui a las siete entradas nuevas).
