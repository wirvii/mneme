// Package service — this file is SPEC-125 D6/AC26-AC30's structural
// guardian: it proves, by parsing internal/service/sdd.go's own syntax
// tree, that every function mutating a spec's status enters through
// loadMutableSpec (the SPEC-125 D4 freeze gate), and that the inventory
// below is exact — neither missing a verb nor carrying a stale one.
//
// SPEC-126 DD3 widens this file's job: it ALSO proves that exactly two
// functions in sdd.go know what an archived backlog item is —
// BacklogArchive (the veto) and specFreeze (the single freeze predicate) —
// so a listing (spec_list) can never disagree with a verb (loadMutableSpec)
// about whether a spec is frozen.
//
// Limit, inherited from SPEC-125 R4/R6: today this guard parses ONE file,
// internal/service/sdd.go. A third definition born in another file of this
// package would not be seen. It is written to admit a list of files rather
// than a single path so widening it later is a one-line change, not a
// rewrite.
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

// backlogArchivedReferences is the CLOSED inventory of every function in
// internal/service/sdd.go that references the identifier
// model.BacklogStatusArchived at all — comparison OR assignment, not just a
// call (SPEC-126 DD3). Exactly TWO are admissible, and they answer two
// different questions: BacklogArchive is the veto that refuses a repeat
// archive AND the assignment that performs one; specFreeze is the single
// place that DECIDES whether a spec is frozen because of it. A third
// function referencing this identifier would be a second, undeclared
// definition of "archived" — exactly what would let a listing and a verb
// disagree.
var backlogArchivedReferences = map[string]string{
	"BacklogArchive": "SPEC-125 D3's veto (refuse to re-archive) plus the assignment that archives",
	"specFreeze":     "SPEC-126 DD3: the single definition of a spec's freeze",
}

// sddAllFuncs parses sdd.go and returns EVERY top-level function
// declaration, methods AND free functions alike, keyed by name. It is a
// superset of sddServiceMethods — needed here because specFreeze (SPEC-126
// DD3) is a free function with no receiver, so sddServiceMethods' *SDDService
// filter would never see it.
//
// sddServiceMethods itself is left untouched: the three SPEC-125 guard
// tests read through it exactly as before this spec.
func sddAllFuncs(t *testing.T) map[string]*ast.FuncDecl {
	t.Helper()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, sddGoPath(t), nil, 0)
	if err != nil {
		t.Fatalf("spec freeze guard: parse sdd.go: %v", err)
	}

	funcs := make(map[string]*ast.FuncDecl)
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		funcs[fn.Name.Name] = fn
	}
	return funcs
}

// funcReferencesIdent reports whether fn's body contains ANY reference to
// pkg.name — as opposed to funcCallsSelector, which only matches CALLS.
// BacklogArchive uses model.BacklogStatusArchived in both an equality
// comparison and a plain field assignment; neither is a call, so
// funcCallsSelector cannot see either of them. ast.Inspect walks every node
// in the body regardless of the statement kind it sits in, so this matches
// both forms (and any other, e.g. a future switch case) without needing to
// enumerate statement types.
func funcReferencesIdent(fn *ast.FuncDecl, pkg, name string) bool {
	if fn.Body == nil {
		return false
	}
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if found {
			return false
		}
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok || ident.Name != pkg || sel.Sel.Name != name {
			return true
		}
		found = true
		return false
	})
	return found
}

// TestBacklogArchivedReferences_InventoryIsExact is SPEC-126 AC22: the set
// of sdd.go functions that reference model.BacklogStatusArchived must be
// IDENTICAL, in both directions, to backlogArchivedReferences' keys. A third
// function that starts comparing against it fails until declared; a
// declared name that stops referencing it fails too — the same two-way,
// exact-set shape TestSpecStatusMutators_InventoryIsExact already uses
// above, so an empty or partial set can never coincide with the two real
// names (SPEC-125 DD6's anti-vacuity argument, restated for this pair).
func TestBacklogArchivedReferences_InventoryIsExact(t *testing.T) {
	funcs := sddAllFuncs(t)

	var actual []string
	for name, fn := range funcs {
		if funcReferencesIdent(fn, "model", "BacklogStatusArchived") {
			actual = append(actual, name)
		}
	}
	sort.Strings(actual)

	declared := make([]string, 0, len(backlogArchivedReferences))
	for name := range backlogArchivedReferences {
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
			t.Errorf("sdd.go's %s references model.BacklogStatusArchived, but backlogArchivedReferences does not "+
				"declare it — a new function that knows what an archived item is must be added to the inventory "+
				"(SPEC-126 DD3), or the reference removed", name)
		}
	}
	for _, name := range declared {
		if !actualSet[name] {
			t.Errorf("backlogArchivedReferences declares %s, but sdd.go's %s no longer references "+
				"model.BacklogStatusArchived — remove the stale entry (SPEC-126 DD3)", name, name)
		}
	}
}

// TestSpecList_NoPerSpecBacklogQuery is SPEC-126 AC14: SpecList's body must
// never call GetBacklogItem — that would be the N+1 R4 forbids (up to 50 on
// an MCP page, up to 127 unwindowed in this repo today) — and its call to
// BacklogStatusIndex must not sit inside any for/range statement, so a page
// of N specs never costs more than ONE extra query regardless of N.
func TestSpecList_NoPerSpecBacklogQuery(t *testing.T) {
	funcs := sddAllFuncs(t)
	fn, ok := funcs["SpecList"]
	if !ok {
		t.Fatal("sdd.go has no SpecList function")
	}

	if funcCallsSelector(fn, "GetBacklogItem") {
		t.Error("SpecList calls GetBacklogItem — that is the per-spec N+1 SPEC-126 DD4 forbids")
	}

	// For every for/range statement anywhere in SpecList's body, look INSIDE
	// that loop's own body for a BacklogStatusIndex call. This scopes the
	// search to "inside a loop" correctly (a call found only after a loop,
	// as a sibling statement, must not trip this check).
	callInsideLoop := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		var loopBody ast.Node
		switch s := n.(type) {
		case *ast.ForStmt:
			loopBody = s.Body
		case *ast.RangeStmt:
			loopBody = s.Body
		default:
			return true
		}
		ast.Inspect(loopBody, func(inner ast.Node) bool {
			call, ok := inner.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if ok && sel.Sel.Name == "BacklogStatusIndex" {
				callInsideLoop = true
			}
			return true
		})
		return true
	})
	if callInsideLoop {
		t.Error("SpecList calls BacklogStatusIndex inside a for/range — that reintroduces the N+1 SPEC-126 DD4 forbids")
	}
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
//
// Mutation guards (SPEC-126 AC23, manually verified during implementation,
// same pattern as the SPEC-125 block above — all three reverted byte for
// byte immediately after each check):
//
//   - Adding a third function in sdd.go that references
//     model.BacklogStatusArchived (verified against SpecHistory: a plain
//     `_ = someStatus == model.BacklogStatusArchived` inside its body) turns
//     TestBacklogArchivedReferences_InventoryIsExact red, naming SpecHistory
//     as an undeclared referencer.
//   - Removing the "specFreeze" entry from backlogArchivedReferences turns
//     the same test red, naming specFreeze as an undeclared referencer —
//     the other direction of the same two-way comparison.
//   - Rewriting SpecList to call svc.store.GetBacklogItem(ctx,
//     spec.BacklogID) inside its decoration loop turns
//     TestSpecList_NoPerSpecBacklogQuery red, naming the GetBacklogItem call
//     directly (the funcCallsSelector check fires before the loop-scoped
//     one even runs, since ANY GetBacklogItem call in the function body —
//     inside a loop or not — is already the N+1 DD4 forbids).
