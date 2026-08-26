// Package service — this file is SPEC-125 D6/AC26-AC30's structural
// guardian: it proves, by parsing internal/service/sdd.go's own syntax
// tree, that every function mutating a spec's status enters through
// loadMutableSpec (the SPEC-125 D4 freeze gate), and that the inventory
// below is exact — neither missing a verb nor carrying a stale one.
package service

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

// specStatusMutators is the CLOSED inventory of every function in
// internal/service/sdd.go that changes a spec's status, mapped to whether it
// must pass through loadMutableSpec (SPEC-125 D4, as widened by the owner to
// all eight verbs — spec.md §3).
//
// The map is the declaration, not a filter: the guard compares it against
// what the file ACTUALLY contains, in both directions (AC27). A ninth verb
// fails the test until it is listed here; a listed name that no longer
// mutates status fails it too. Every value is true today — nothing is
// exempt (D6: with the owner's widening to eight, the exemption list that
// used to exist here is gone, because a list that ends up empty is a
// guardian that would pass without checking anything). A future exemption
// is possible — set one to false with the reason on the line beside it —
// and that is a change a reviewer sees in the diff, which is the whole
// point of keeping this a literal map instead of a filter function.
var specStatusMutators = map[string]bool{
	"SpecAdvance":    true,
	"SpecPushback":   true,
	"SpecReject":     true,
	"SpecResolve":    true,
	"SpecQuick":      true,
	"LaneAudit":      true,
	"LaneOverride":   true,
	"LaneReclassify": true,
}

// sddGoPath resolves the absolute path to sdd.go from this test file's own
// location (runtime.Caller), so the guard works regardless of the working
// directory `go test` was invoked from.
func sddGoPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("spec freeze guard: runtime.Caller(0) failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "sdd.go")
}

// sddServiceMethods parses sdd.go and returns every top-level function
// declared with a *SDDService receiver, keyed by method name.
func sddServiceMethods(t *testing.T) (*ast.File, map[string]*ast.FuncDecl) {
	t.Helper()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, sddGoPath(t), nil, 0)
	if err != nil {
		t.Fatalf("spec freeze guard: parse sdd.go: %v", err)
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
	return f, methods
}

// funcCallsSelector reports whether fn's body contains a call whose
// selector's method name matches name — e.g. "UpdateSpecStatus" for a
// svc.store.UpdateSpecStatus(...) call, or "loadMutableSpec" for a
// svc.loadMutableSpec(...) call. It deliberately matches on the selector
// name alone (not the full receiver chain): the question this guard asks
// is "does this function's body reference this call", not "through which
// exact variable".
func funcCallsSelector(fn *ast.FuncDecl, name string) bool {
	if fn.Body == nil {
		return false
	}
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name == name {
			found = true
			return false
		}
		return true
	})
	return found
}

// mutatorFunctionNames returns the sorted names of every *SDDService method
// in sdd.go whose body calls UpdateSpecStatus — the ACTUAL set the file
// contains, independent of specStatusMutators.
func mutatorFunctionNames(methods map[string]*ast.FuncDecl) []string {
	var names []string
	for name, fn := range methods {
		if funcCallsSelector(fn, "UpdateSpecStatus") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// TestSpecStatusMutators_InventoryIsExact is SPEC-125 AC27: the set of
// sdd.go functions that call UpdateSpecStatus must be IDENTICAL to the set
// of specStatusMutators keys, in both directions. A verb added to sdd.go
// without being declared here fails; a stale entry left behind after a
// verb is removed or renamed fails too.
func TestSpecStatusMutators_InventoryIsExact(t *testing.T) {
	_, methods := sddServiceMethods(t)
	actual := mutatorFunctionNames(methods)

	declared := make([]string, 0, len(specStatusMutators))
	for name := range specStatusMutators {
		declared = append(declared, name)
	}
	sort.Strings(declared)

	actualSet := make(map[string]bool, len(actual))
	for _, name := range actual {
		actualSet[name] = true
	}
	declaredSet := make(map[string]bool, len(declared))
	for _, name := range declared {
		declaredSet[name] = true
	}

	for _, name := range actual {
		if !declaredSet[name] {
			t.Errorf("sdd.go defines %s, which calls UpdateSpecStatus, but specStatusMutators does not declare it — "+
				"a new spec-status-mutating verb must be added to the inventory (SPEC-125 D6)", name)
		}
	}
	for _, name := range declared {
		if !actualSet[name] {
			t.Errorf("specStatusMutators declares %s, but sdd.go's %s no longer calls UpdateSpecStatus — "+
				"remove the stale entry (SPEC-125 D6)", name, name)
		}
	}
}

// TestSpecStatusMutators_AllTrueEntriesGateThroughLoadMutableSpec is
// SPEC-125 AC28: every entry declared true in specStatusMutators must call
// loadMutableSpec somewhere in its body — the freeze gate DD5 requires.
func TestSpecStatusMutators_AllTrueEntriesGateThroughLoadMutableSpec(t *testing.T) {
	_, methods := sddServiceMethods(t)

	for name, mustGate := range specStatusMutators {
		if !mustGate {
			continue
		}
		fn, ok := methods[name]
		if !ok {
			t.Errorf("specStatusMutators declares %s but sdd.go has no *SDDService method with that name", name)
			continue
		}
		if !funcCallsSelector(fn, "loadMutableSpec") {
			t.Errorf("%s is declared true in specStatusMutators but its body never calls loadMutableSpec "+
				"(SPEC-125 D4/D5 freeze gate missing)", name)
		}
	}
}

// TestSpecStatusMutators_TodayAllEightAreTrue is SPEC-125 AC26: the
// inventory has exactly eight entries and every one of them is declared
// true today — the owner's widening in spec.md §3 left no verb exempt.
// Lowering one to false is possible (a future, reasoned exemption) but
// requires touching this test and writing down why.
func TestSpecStatusMutators_TodayAllEightAreTrue(t *testing.T) {
	if len(specStatusMutators) != 8 {
		t.Fatalf("expected exactly 8 entries in specStatusMutators, got %d", len(specStatusMutators))
	}
	for name, mustGate := range specStatusMutators {
		if !mustGate {
			t.Errorf("%s is declared false; today all eight verbs must be true (spec.md §3)", name)
		}
	}
}

// Mutation guards (SPEC-125 AC29/AC30, manually verified during
// implementation — the established pattern in this package, e.g.
// lane_audit_test.go's "Mutation guard (manually verified)" comments):
//
//   - AC29: removing the `svc.loadMutableSpec(ctx, req.ID)` call from any ONE
//     of the eight true entries (restoring the plain `svc.store.GetSpec` call
//     it replaced) turns TestSpecStatusMutators_AllTrueEntriesGateThroughLoadMutableSpec
//     red, naming that exact function — verified against LaneReclassify, the
//     verb DD5 calls out as the easiest to get wrong (UpdateSpecLaneScope
//     runs eight lines after the load, before the transition). Change
//     reverted byte for byte immediately after.
//   - AC30: removing the "LaneOverride" entry from specStatusMutators turns
//     TestSpecStatusMutators_InventoryIsExact red, naming LaneOverride as an
//     undeclared mutator; separately adding a "NotARealVerb": true entry
//     turns the same test red, naming NotARealVerb as declared-but-absent.
//     Both changes reverted byte for byte immediately after. This is what
//     makes an empty or partial inventory structurally unable to pass
//     (AC27): with only 7 (or 9) names, an exact two-way set comparison
//     against sdd.go's real 8 mutators can never agree.
