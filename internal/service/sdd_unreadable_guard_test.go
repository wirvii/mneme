// Package service — this file is SPEC-133 AC12's structural guardian: it
// walks the AST of every non-test .go file in the repository, finds every
// call to ListBacklogItems, ListSpecs, or RecentlyCompletedSpecs, and fails
// if the destination for that call's unreadable-rows return value is `_`
// (discarded) or the call's results are dropped entirely.
//
// It is written this way, and not as a hand-written list of the sites named
// in spec.md §3.1, on purpose (spec.md §9 Forma 4): the population is
// DERIVED from the AST, so a caller nobody thought to add to a list is
// still caught. This is the same reason spec.md §3.1's own site count grew
// from three to ten to eleven while this spec was only being READ, before
// a single line of code moved — an enumerated list is exactly the kind of
// criterion that stops discriminating the moment the codebase grows.
//
// Test files are deliberately excluded (spec.md R3): a test that only
// wants the item list has a legitimate reason to discard the unreadable
// relation, and that boundary is declared here, not accidental.
package service

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// sddUnreadableRepoRoot returns the absolute path of the mneme repository
// root, derived from this test file's own location (two levels up from
// internal/service) — the same technique sddGoPath/sddImportGoPath use for
// a single file, widened here to a whole-tree walk.
func sddUnreadableRepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("sdd unreadable guard: runtime.Caller(0) failed")
	}
	// internal/service/sdd_unreadable_guard_test.go → repo root = ../../
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

// sddUnreadableWatchedSelectors maps a watched method name to the index
// (0-based) its call's LHS/return position carries the []model.UnreadableRow
// value at, per the signatures spec.md D2 declares:
//
//	ListBacklogItems(ctx, project, status, limit) (items, total, unreadable, error)      -> index 2
//	ListSpecs(ctx, project, status, limit)        (items, total, unreadable, error)      -> index 2
//	RecentlyCompletedSpecs(ctx, project, n)       (specs, unreadable, error)             -> index 1
var sddUnreadableWatchedSelectors = map[string]int{
	"ListBacklogItems":       2,
	"ListSpecs":              2,
	"RecentlyCompletedSpecs": 1,
}

// sddUnreadableGoFiles returns every .go file under root that is not a
// _test.go file — the population AC12 derives its check from, instead of a
// hand-written list of sites.
func sddUnreadableGoFiles(t *testing.T, root string) []string {
	t.Helper()

	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip directories git itself and the build output never need
			// walking; .git can be large and is never Go source anyway.
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		t.Fatalf("sdd unreadable guard: walk repo: %v", err)
	}
	sort.Strings(files)
	return files
}

// sddUnreadableViolation names one call site the guard rejects.
type sddUnreadableViolation struct {
	file   string
	line   int
	method string
	reason string
}

// findSDDUnreadableViolations parses one file and reports every call to a
// watched selector whose unreadable-rows return value is discarded — either
// assigned to `_`, or dropped entirely because the call's results were
// never captured at all.
func findSDDUnreadableViolations(fset *token.FileSet, f *ast.File, path string) []sddUnreadableViolation {
	var violations []sddUnreadableViolation

	// isWatchedCall reports the watched index when call is a call to one of
	// sddUnreadableWatchedSelectors, ok=false otherwise.
	isWatchedCall := func(call *ast.CallExpr) (idx int, name string, ok bool) {
		sel, isSel := call.Fun.(*ast.SelectorExpr)
		if !isSel {
			return 0, "", false
		}
		idx, watched := sddUnreadableWatchedSelectors[sel.Sel.Name]
		return idx, sel.Sel.Name, watched
	}

	ast.Inspect(f, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.AssignStmt:
			if len(stmt.Rhs) != 1 {
				return true
			}
			call, isCall := stmt.Rhs[0].(*ast.CallExpr)
			if !isCall {
				return true
			}
			idx, name, watched := isWatchedCall(call)
			if !watched {
				return true
			}
			if idx >= len(stmt.Lhs) {
				// Shape mismatch (e.g. a stray build error elsewhere) —
				// not this guard's concern; let the compiler catch it.
				return true
			}
			ident, isIdent := stmt.Lhs[idx].(*ast.Ident)
			if isIdent && ident.Name == "_" {
				violations = append(violations, sddUnreadableViolation{
					file:   path,
					line:   fset.Position(stmt.Pos()).Line,
					method: name,
					reason: "unreadable rows discarded with _",
				})
			}
		case *ast.ExprStmt:
			call, isCall := stmt.X.(*ast.CallExpr)
			if !isCall {
				return true
			}
			_, name, watched := isWatchedCall(call)
			if !watched {
				return true
			}
			violations = append(violations, sddUnreadableViolation{
				file:   path,
				line:   fset.Position(stmt.Pos()).Line,
				method: name,
				reason: "call result (including unreadable rows) dropped entirely",
			})
		}
		return true
	})

	return violations
}

// TestSDDUnreadable_NeverDiscardedAtCallSite is SPEC-133 AC12: no call to
// ListBacklogItems, ListSpecs, or RecentlyCompletedSpecs anywhere in the
// repository's non-test Go source may discard the unreadable-rows value
// with `_`, or drop the call's results entirely. The population under test
// is derived from parsing every non-test .go file in the repository, not
// from a hand-written list of the sites spec.md §3.1 names — a future
// caller is covered automatically.
func TestSDDUnreadable_NeverDiscardedAtCallSite(t *testing.T) {
	root := sddUnreadableRepoRoot(t)
	files := sddUnreadableGoFiles(t, root)
	if len(files) == 0 {
		t.Fatal("sdd unreadable guard: found zero non-test .go files — the walk is broken")
	}

	fset := token.NewFileSet()
	var all []sddUnreadableViolation
	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("sdd unreadable guard: parse %s: %v", path, err)
		}
		all = append(all, findSDDUnreadableViolations(fset, f, path)...)
	}

	for _, v := range all {
		rel, relErr := filepath.Rel(root, v.file)
		if relErr != nil {
			rel = v.file
		}
		t.Errorf("%s:%d: call to %s %s (SPEC-133 AC12/D2)", rel, v.line, v.method, v.reason)
	}
}

// Mutation guard (manually verified during implementation, spec.md's own
// convention for this repository, e.g. spec_freeze_guard_test.go's
// bottom-of-file comment block): reintroducing
// `items, total, _, err := svc.store.ListBacklogItems(...)` at
// sdd_import.go's computeOnlyInBase site turns this test red, naming that
// exact file, line, and method. Verified and reverted byte for byte during
// implementation; the real transcript is recorded in changes.md.
