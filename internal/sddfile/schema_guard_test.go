package sddfile

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"
)

// sddFileSchemaPath resolves internal/sddfile/schema.go relative to this
// test file's own location (runtime.Caller), the same mold
// leaf_test.go's TestSDDFilePackage_ImportsOnlyStdlibAndModel already uses
// — so the guard works regardless of the working directory `go test` was
// invoked from.
func sddFileSchemaPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("schema guard: runtime.Caller(0) failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "schema.go")
}

// schemaRangeConstOperand reports whether one of bin's two operands is a
// bare reference to MinFileSchema or CurrentFileSchema, and which one.
// Matching by identifier name alone (not by resolved type/value) is the
// same "casa por selector" trade-off internal/service's own structural
// guardians (spec_freeze_guard_test.go, sdd_export_guard_test.go) already
// accept: closing it would need full type-checking for a check that
// exists to catch an accidental, LOCAL edit to checkSchema's own body.
func schemaRangeConstOperand(bin *ast.BinaryExpr) (name string, ok bool) {
	for _, expr := range []ast.Expr{bin.X, bin.Y} {
		ident, isIdent := expr.(*ast.Ident)
		if !isIdent {
			continue
		}
		if ident.Name == "MinFileSchema" || ident.Name == "CurrentFileSchema" {
			return ident.Name, true
		}
	}
	return "", false
}

// TestSDDFileSchema_ComparisonStaysARange is AC4's structural half (SPEC-130,
// corrected after the human-approval gate): D28's whole argument — a schema
// comparison must be a genuine RANGE, never equality, because
// MinFileSchema == CurrentFileSchema == 1 today makes the two forms
// behave IDENTICALLY over the entire constructible domain (V13). No
// behavioural test can ever catch a regression to equality while that
// coincidence holds — the detection window would only open the day a
// second schema exists, which is exactly the SPEC-116 precedent this
// spec's own D28 godoc cites: internal/quality shipped an equality check
// against `.mneme/quality.toml`'s schema, and the jump to schema 3 would
// have bricked every existing constitution in the world had it not been
// caught and corrected to a range first. This test parses
// internal/sddfile/schema.go's own source with go/ast and fails if
// checkSchema's body:
//   - loses the `> CurrentFileSchema` comparison,
//   - loses the `< MinFileSchema` comparison, or
//   - gains an `==`/`!=` comparison against either constant.
//
// It is a guardian over EXISTING production code, not a hook for a new
// one: no constant becomes injectable, no function gains a parameter, so
// that this test can run. If making this test pass ever requires touching
// schema.go, that is the guardian failing on purpose — stop and say so.
func TestSDDFileSchema_ComparisonStaysARange(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, sddFileSchemaPath(t), nil, 0)
	if err != nil {
		t.Fatalf("schema guard: parse schema.go: %v", err)
	}

	var fn *ast.FuncDecl
	for _, decl := range f.Decls {
		d, ok := decl.(*ast.FuncDecl)
		if ok && d.Name.Name == "checkSchema" {
			fn = d
			break
		}
	}
	if fn == nil || fn.Body == nil {
		t.Fatal("schema guard: schema.go has no checkSchema function body")
	}

	var hasUpperRange, hasLowerRange bool
	var forbiddenEquality []string

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		bin, ok := n.(*ast.BinaryExpr)
		if !ok {
			return true
		}
		constName, matched := schemaRangeConstOperand(bin)
		if !matched {
			return true
		}
		switch bin.Op {
		case token.GTR:
			if constName == "CurrentFileSchema" {
				hasUpperRange = true
			}
		case token.LSS:
			if constName == "MinFileSchema" {
				hasLowerRange = true
			}
		case token.EQL, token.NEQ:
			forbiddenEquality = append(forbiddenEquality, constName)
		}
		return true
	})

	if len(forbiddenEquality) > 0 {
		t.Errorf("checkSchema compares %v with == or != instead of a range comparison — "+
			"D28/AC4: the range must never collapse into equality, because MinFileSchema == "+
			"CurrentFileSchema == 1 today would let an equality check pass unnoticed", forbiddenEquality)
	}
	if !hasUpperRange {
		t.Error("checkSchema is missing its `> CurrentFileSchema` range comparison — " +
			"a schema newer than this mneme understands would no longer be refused (D28/AC4)")
	}
	if !hasLowerRange {
		t.Error("checkSchema is missing its `< MinFileSchema` range comparison — " +
			"the range check is no longer genuine (D28/AC4)")
	}
}
