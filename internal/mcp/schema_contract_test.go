package mcp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// Package mcp's request/schema contract guard (SPEC-113).
//
// WHY THIS EXISTS: `subagent_write` accepted `areas_complete` — the field
// that activates role containment (SPEC-086 D4/D5/D11) — for a long time
// without ever declaring it in the tool's published InputSchema. A client
// drafting JSON arguments from the published contract had no type hint and
// sent a string where a bool was expected; json.Unmarshal rejected it. The
// earlier manual audit that found this ("47 campos, 46 declarados") only
// looked at named `*Request` structs in two files — it silently missed every
// tool whose handler decodes into a type from another package or an inline
// anonymous struct, which is most of the surface. A hand-maintained
// tool→struct table has the same blind spot by construction: the day
// someone adds a tool and forgets an entry, the table stays "complete" and
// says nothing.
//
// WHY DERIVED, NOT MAINTAINED: this file extracts the tool→handler mapping
// by parsing the `switch` in handleToolCall (internal/mcp/handlers.go) — the
// one seam every dispatched tool must pass through — and extracts each
// handler's request fields by parsing its `json.Unmarshal(raw, &X)` calls
// and resolving X's type, wherever it lives (this package, internal/model,
// internal/service, ...). Nothing here is a table an author could forget to
// update; the mapping IS the dispatch code.
//
// D6.1: every construction this guard cannot resolve is a t.Fatal, never a
// silent skip. A guardian that skips what it doesn't understand protects
// exactly the part it does understand, and that's invisible from outside
// (see the repo-wide antipattern memory
// testing/antipatron-guardian-que-no-detecta-su-eliminacion).

// modulePrefix is this repository's module path — used to turn an import
// path into a filesystem directory under moduleRoot without invoking the Go
// build system (this guard is pure AST, no compilation).
const modulePrefix = "github.com/wirvii/mneme/"

// predeclaredBasicTypes are Go's built-in types that can legally appear as
// an unmarshal target's resolved leaf type (e.g. `var args map[string]any`
// bottoms out at `any`). None of them carry a static field set, so resolving
// one yields zero requestFields rather than a resolution failure.
var predeclaredBasicTypes = map[string]bool{
	"bool": true, "string": true, "any": true, "error": true, "byte": true, "rune": true,
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"float32": true, "float64": true,
}

// undeclaredFieldWaivers lists the request fields deliberately NOT published
// in their tool's InputSchema (D7, option 3 — the last-resort disposition,
// used only when declaring the field or excluding it via json:"-" is not
// viable). Every entry requires a written reason (D7.1) and is itself
// verified against the live request struct and schema on every run (D7.3):
// a waiver naming a field that no longer exists, or that is now declared, is
// a stale exemption that would otherwise mask a fresh regression reusing the
// same field name.
var undeclaredFieldWaivers = map[string]map[string]string{
	// SPEC-113 C4.1: model.SaveRequest.Shared (internal/model/memory.go)
	// decides a memory's team-memory sharing level. It is functional, not
	// dead — internal/service/teammemory.go's bakeTeamMemoryFields reads it
	// ("reqShared mirrors SaveRequest.Shared: nil defers to the type-based
	// default"). json:"-" is NOT viable: internal/http/server.go's
	// handleMemories (the real POST /v1/memories handler, not just a test)
	// decodes the request body straight into a model.SaveRequest, so making
	// the field structurally unsettable from JSON would silently narrow the
	// HTTP frontend's contract — a behaviour change this spec's scope
	// explicitly excludes (D7.2/C4.1). Declaring it in the schema was also
	// rejected: it is not a parameter an agent should choose at save time
	// (it is set by mem_promote, or by Go call sites building SaveRequest
	// directly, e.g. vault.Frontmatter.ToSaveRequest) — publishing it would
	// invite an agent to set it ad hoc.
	"mem_save": {"shared": "team-memory sharing level; set via mem_promote or Go call sites (ToSaveRequest), not an agent-facing save-time choice; json:\"-\" rejected because internal/http's handleMemories decodes SaveRequest from JSON and would be silently narrowed (SPEC-113 D7.2/C4.1)"},
}

// requestField is one JSON-visible field discovered (via AST) on a tool's
// request type.
type requestField struct {
	// JSONName is the name this field is addressed by over the wire — the
	// json tag's name, or the Go field name when no tag is present.
	JSONName string
	// IsBool is true when the Go field type is literally bool or *bool
	// (D6.3's narrow, AST-safe type check).
	IsBool bool
}

// toolDispatch is one `case "<tool>": return h.<Handler>(...)` arm of
// handleToolCall's switch.
type toolDispatch struct {
	Tool    string
	Handler string
}

// typeInfo pairs a type declaration with the file it was declared in, so a
// qualified reference inside that type's definition (e.g. an embedded field
// from yet another package) can resolve its own imports correctly.
type typeInfo struct {
	Spec *ast.TypeSpec
	File *ast.File
}

// pkgTypes indexes every top-level type declaration in one directory
// (non-test .go files only) by name. Loaded once per package directory and
// reused across every field resolved against that package within a test run.
type pkgTypes struct {
	dir   string
	specs map[string]typeInfo
}

// methodInfo pairs a *handlers method declaration with the file it lives in.
type methodInfo struct {
	Decl *ast.FuncDecl
	File *ast.File
}

// schemaContractModuleRoot resolves the repository root from this test
// file's own path, following the pattern established by
// internal/testenv/testmain_guard_test.go — this file lives at
// <root>/internal/mcp/schema_contract_test.go.
func schemaContractModuleRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("schema contract guard: runtime.Caller(0) failed")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
}

// loadPkgTypes parses every non-test .go file directly inside dir and
// indexes its top-level type declarations by name. It never recurses into
// subdirectories — Go packages aren't nested that way.
func loadPkgTypes(t *testing.T, dir string) *pkgTypes {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("schema contract guard: read dir %s: %v", dir, err)
	}

	fset := token.NewFileSet()
	pt := &pkgTypes{dir: dir, specs: map[string]typeInfo{}}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("schema contract guard: parse %s: %v", path, err)
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				pt.specs[ts.Name.Name] = typeInfo{Spec: ts, File: f}
			}
		}
	}
	return pt
}

// loadPkgTypesForImport resolves importPath to a directory under moduleRoot
// and loads (or returns the cached) pkgTypes for it. It only ever looks
// inside this module — every request type this guard needs to resolve is
// part of the mneme source tree, never a third-party dependency.
func loadPkgTypesForImport(t *testing.T, cache map[string]*pkgTypes, moduleRoot, importPath string) *pkgTypes {
	t.Helper()
	if pt, ok := cache[importPath]; ok {
		return pt
	}
	if !strings.HasPrefix(importPath, modulePrefix) {
		t.Fatalf("schema contract guard: import path %q is outside this module — cannot resolve its types from source; a request type now depends on an external package, update the resolver", importPath)
	}
	dir := filepath.Join(moduleRoot, filepath.FromSlash(strings.TrimPrefix(importPath, modulePrefix)))
	pt := loadPkgTypes(t, dir)
	cache[importPath] = pt
	return pt
}

// importPathForAlias resolves the import path that alias refers to within
// file f's import block, or "" when no import matches. alias is either the
// import's explicit name (`import m "foo/bar"`) or the last path segment
// (Go's default package name inference — this guard doesn't need the real
// declared package name since every request type lives in a package whose
// import alias already equals its directory name in this codebase).
func importPathForAlias(f *ast.File, alias string) string {
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		name := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		if name == alias {
			return path
		}
	}
	return ""
}

// parseToolDispatch parses internal/mcp/handlers.go, locates handleToolCall,
// and extracts every `case "<tool>": return h.<Handler>(ctx, params.Arguments)`
// arm of its switch (D4/D5 step 2). It t.Fatal's — never skips — on any
// shape it doesn't recognise (D6.1): a `default` clause is the only
// exception (it dispatches nothing, by design).
func parseToolDispatch(t *testing.T, moduleRoot string) []toolDispatch {
	t.Helper()

	path := filepath.Join(moduleRoot, "internal", "mcp", "handlers.go")
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("schema contract guard: parse %s: %v", path, err)
	}

	var target *ast.FuncDecl
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == "handleToolCall" {
			target = fn
			break
		}
	}
	if target == nil {
		t.Fatalf("schema contract guard: no handleToolCall method found in %s — the tool dispatch seam moved; update parseToolDispatch to find it (D4)", path)
	}

	var sw *ast.SwitchStmt
	ast.Inspect(target.Body, func(n ast.Node) bool {
		if sw != nil {
			return false
		}
		if s, ok := n.(*ast.SwitchStmt); ok {
			sw = s
			return false
		}
		return true
	})
	if sw == nil {
		t.Fatal("schema contract guard: handleToolCall has no switch statement — the dispatch seam changed shape (e.g. to a map[string]handlerFunc); D6.1 requires updating parseToolDispatch instead of silently producing an empty mapping")
	}

	var dispatch []toolDispatch
	for _, stmt := range sw.Body.List {
		cc, ok := stmt.(*ast.CaseClause)
		if !ok {
			t.Fatalf("schema contract guard: unexpected statement %T in handleToolCall's switch body — expected only case clauses", stmt)
		}
		if cc.List == nil {
			// The `default:` clause — dispatches nothing (unknown-tool
			// error path). Not a tool, nothing to record.
			continue
		}
		if len(cc.List) != 1 {
			t.Fatalf("schema contract guard: case clause with %d values (%v) — expected exactly one string literal per case (D4's uniform shape)", len(cc.List), cc.List)
		}
		lit, ok := cc.List[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			t.Fatalf("schema contract guard: case value is not a string literal: %#v", cc.List[0])
		}
		toolName, err := strconv.Unquote(lit.Value)
		if err != nil {
			t.Fatalf("schema contract guard: cannot unquote case literal %s: %v", lit.Value, err)
		}
		dispatch = append(dispatch, toolDispatch{Tool: toolName, Handler: handlerFromCaseBody(t, toolName, cc.Body)})
	}
	return dispatch
}

// handlerFromCaseBody extracts the `<Method>` name from a case body shaped
// exactly as `return h.<Method>(ctx, params.Arguments)` (D4's uniform arm).
// Any other shape is a t.Fatal (D6.1): a case that dispatches differently is
// exactly the kind of surface this guard exists to not silently miss.
func handlerFromCaseBody(t *testing.T, toolName string, body []ast.Stmt) string {
	t.Helper()
	for _, stmt := range body {
		ret, ok := stmt.(*ast.ReturnStmt)
		if !ok || len(ret.Results) == 0 {
			continue
		}
		call, ok := ret.Results[0].(*ast.CallExpr)
		if !ok {
			continue
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "h" {
			return sel.Sel.Name
		}
	}
	t.Fatalf("schema contract guard: case %q body does not match `return h.<Handler>(ctx, params.Arguments)` (D4) — update handlerFromCaseBody for the new dispatch shape", toolName)
	return ""
}

// loadHandlerMethods scans every declaration pt indexed... no — scans every
// non-test .go file's top-level FuncDecls in the internal/mcp directory (via
// mcpTypes, which already parsed them for type declarations) and returns
// every method whose receiver is *handlers, keyed by method name. A method
// can live in any handlers*.go file — this guard doesn't care which.
func loadHandlerMethods(t *testing.T, moduleRoot string) map[string]methodInfo {
	t.Helper()

	dir := filepath.Join(moduleRoot, "internal", "mcp")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("schema contract guard: read dir %s: %v", dir, err)
	}

	fset := token.NewFileSet()
	methods := map[string]methodInfo{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("schema contract guard: parse %s: %v", path, err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 {
				continue
			}
			recvType := fn.Recv.List[0].Type
			if star, ok := recvType.(*ast.StarExpr); ok {
				recvType = star.X
			}
			if ident, ok := recvType.(*ast.Ident); ok && ident.Name == "handlers" {
				methods[fn.Name.Name] = methodInfo{Decl: fn, File: f}
			}
		}
	}
	return methods
}

// secondParamName returns the name of a handler's second parameter — by
// convention `raw json.RawMessage`, but this reads the actual identifier
// rather than assuming it, so a rename doesn't silently break resolution.
func secondParamName(t *testing.T, fn *ast.FuncDecl) string {
	t.Helper()
	var names []string
	if fn.Type.Params != nil {
		for _, field := range fn.Type.Params.List {
			if len(field.Names) == 0 {
				names = append(names, "_")
				continue
			}
			for _, n := range field.Names {
				names = append(names, n.Name)
			}
		}
	}
	if len(names) < 2 {
		t.Fatalf("schema contract guard: %s does not have at least 2 named parameters — expected (ctx context.Context, raw json.RawMessage)", fn.Name.Name)
	}
	return names[1]
}

// findLocalVarType locates, anywhere in fn's body (regardless of nesting —
// several handlers unmarshal inside an `if len(raw) > 0 {` guard), the
// declaration of a local variable named name and returns its type
// expression. Supports both `var name Type` and `name := Type{}`, the only
// two shapes this codebase's handlers use.
func findLocalVarType(fn *ast.FuncDecl, name string) ast.Expr {
	var found ast.Expr
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		switch s := n.(type) {
		case *ast.ValueSpec:
			for _, id := range s.Names {
				if id.Name == name && s.Type != nil {
					found = s.Type
					return false
				}
			}
		case *ast.AssignStmt:
			if s.Tok != token.DEFINE {
				return true
			}
			for i, lhs := range s.Lhs {
				id, ok := lhs.(*ast.Ident)
				if !ok || id.Name != name || i >= len(s.Rhs) {
					continue
				}
				if lit, ok := s.Rhs[i].(*ast.CompositeLit); ok {
					found = lit.Type
					return false
				}
			}
		}
		return true
	})
	return found
}

// isBoolType reports whether expr is literally `bool` or `*bool` — the
// narrow, AST-only type check D6.3 requires (it deliberately does not
// attempt to map Go's full type system to JSON Schema).
func isBoolType(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name == "bool"
	case *ast.StarExpr:
		return isBoolType(e.X)
	default:
		return false
	}
}

// jsonFieldName reads the `json:"..."` tag and returns the field's wire
// name. ok is false when the tag excludes the field (`json:"-"`, exactly,
// with no following comma — encoding/json's own rule). When no json tag is
// present at all, name is "" and the caller falls back to the Go field name
// — an exported field without a json tag is still real input surface.
func jsonFieldName(tag reflect.StructTag) (name string, ok bool) {
	raw, present := tag.Lookup("json")
	if !present {
		return "", true
	}
	if raw == "-" {
		return "", false
	}
	if idx := strings.Index(raw, ","); idx >= 0 {
		return raw[:idx], true
	}
	return raw, true
}

// embeddedTypeName extracts the bare type identifier of an embedded field's
// type expression — used only to decide what json tag override (if any)
// applies to it; the promoted fields themselves come from resolving the
// full type via fieldsFromTypeExpr.
func embeddedTypeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return embeddedTypeName(e.X)
	case *ast.SelectorExpr:
		return e.Sel.Name
	default:
		return ""
	}
}

// fieldsFromStruct extracts requestFields from a struct type's field list,
// applying encoding/json's promotion rule for embedded fields (D5.5): an
// embedded field with no json tag (or a tag with no name override) is
// flattened into the parent's field set; one with an explicit name is
// treated as an ordinary named field instead; one tagged json:"-" is
// dropped entirely.
func fieldsFromStruct(t *testing.T, st *ast.StructType, file *ast.File, pkg *pkgTypes, cache map[string]*pkgTypes, moduleRoot string) []requestField {
	t.Helper()
	if st.Fields == nil {
		return nil
	}

	var out []requestField
	for _, field := range st.Fields.List {
		var tag reflect.StructTag
		if field.Tag != nil {
			tag = reflect.StructTag(strings.Trim(field.Tag.Value, "`"))
		}

		if len(field.Names) == 0 {
			// Embedded field.
			name, ok := jsonFieldName(tag)
			if !ok {
				continue // json:"-" on the embedded field itself.
			}
			if name != "" {
				// Explicit name override: NOT promoted, behaves as an
				// ordinary named field addressed by that name.
				out = append(out, requestField{JSONName: name, IsBool: isBoolType(field.Type)})
				continue
			}
			_ = embeddedTypeName(field.Type) // documents intent; the real resolution is the recursive call below.
			out = append(out, fieldsFromTypeExpr(t, field.Type, file, pkg, cache, moduleRoot)...)
			continue
		}

		name, ok := jsonFieldName(tag)
		if !ok {
			continue // json:"-"
		}
		isBool := isBoolType(field.Type)
		for _, id := range field.Names {
			if !id.IsExported() {
				continue // encoding/json ignores unexported fields regardless of tag.
			}
			jsonName := name
			if jsonName == "" {
				jsonName = id.Name
			}
			out = append(out, requestField{JSONName: jsonName, IsBool: isBool})
		}
	}
	return out
}

// fieldsFromTypeExpr resolves expr — the type of a json.Unmarshal
// destination, or of a struct field found while resolving one — down to its
// JSON-visible fields (D5 step 3). It follows pointers and named types
// (both local to pkg and package-qualified) until it reaches a struct, a
// dynamic container (map/interface — no static fields to check), or a
// predeclared basic type (also no fields). Any other shape is a t.Fatal
// (D6.1): silently returning nil for something this guard doesn't actually
// understand would be indistinguishable from "correctly found no fields".
func fieldsFromTypeExpr(t *testing.T, expr ast.Expr, file *ast.File, pkg *pkgTypes, cache map[string]*pkgTypes, moduleRoot string) []requestField {
	t.Helper()

	switch e := expr.(type) {
	case *ast.StarExpr:
		return fieldsFromTypeExpr(t, e.X, file, pkg, cache, moduleRoot)

	case *ast.StructType:
		return fieldsFromStruct(t, e, file, pkg, cache, moduleRoot)

	case *ast.MapType, *ast.InterfaceType, *ast.ArrayType:
		// Dynamic container (e.g. `var args map[string]any`): no static
		// field set exists to check against the schema. This is a
		// legitimate absence of structure, not a resolution failure.
		return nil

	case *ast.Ident:
		if predeclaredBasicTypes[e.Name] {
			return nil
		}
		info, ok := pkg.specs[e.Name]
		if !ok {
			t.Fatalf("schema contract guard: type %q not found under %s — update the resolver if request types moved", e.Name, pkg.dir)
		}
		return fieldsFromTypeExpr(t, info.Spec.Type, info.File, pkg, cache, moduleRoot)

	case *ast.SelectorExpr:
		pkgIdent, ok := e.X.(*ast.Ident)
		if !ok {
			t.Fatalf("schema contract guard: qualified type %#v has a non-identifier package selector; update the resolver", e)
		}
		importPath := importPathForAlias(file, pkgIdent.Name)
		if importPath == "" {
			t.Fatalf("schema contract guard: cannot resolve import path for package alias %q referenced in %s", pkgIdent.Name, file.Name.Name)
		}
		otherPkg := loadPkgTypesForImport(t, cache, moduleRoot, importPath)
		info, ok := otherPkg.specs[e.Sel.Name]
		if !ok {
			t.Fatalf("schema contract guard: type %s.%s not found under %s", pkgIdent.Name, e.Sel.Name, otherPkg.dir)
		}
		return fieldsFromTypeExpr(t, info.Spec.Type, info.File, otherPkg, cache, moduleRoot)

	default:
		t.Fatalf("schema contract guard: unsupported type expression %#v when resolving request fields — update fieldsFromTypeExpr for this shape (D6.1)", expr)
		return nil
	}
}

// rawParamUseCount counts how many times name appears as an identifier
// anywhere in body. Used to distinguish "this handler never reads its raw
// payload" (a legitimate zero-input handler) from "this handler reads it in
// a shape resolveHandlerFields doesn't recognise" (D6.1: the latter must
// fail loudly, the former must not be mistaken for it).
func rawParamUseCount(body *ast.BlockStmt, name string) int {
	if name == "_" {
		return 0
	}
	count := 0
	ast.Inspect(body, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			count++
		}
		return true
	})
	return count
}

// resolveHandlerFields finds every json.Unmarshal(<2nd param>, &X) call in
// fn's body (D5 step 3 — a union, since a couple of handlers decode in two
// phases) and resolves each X's JSON-visible fields, deduplicating by JSON
// name (a later call's field wins, matching the fact that a second-phase
// unmarshal typically refines the same payload).
//
// A handful of handlers (handleSkillsList, handleProfileList,
// handleCodegraphStatus, ...) take no input at all: some spell that with a
// blank second parameter (`_`), others keep the `raw` name but never
// reference it. Both are AST-visible, unambiguous signals — verified via
// rawParamUseCount rather than assumed — that zero fields is the correct
// answer, not a shape this guard failed to understand. Any OTHER handler
// with no json.Unmarshal call on a payload it does reference is a t.Fatal
// (D6.1): every dispatched handler that actually uses its raw payload is
// expected to decode it this way, and silently returning zero fields for
// one that doesn't would be indistinguishable from a real omission.
func resolveHandlerFields(t *testing.T, mi methodInfo, pkg *pkgTypes, cache map[string]*pkgTypes, moduleRoot string) []requestField {
	t.Helper()

	rawParam := secondParamName(t, mi.Decl)
	seen := map[string]requestField{}
	found := false

	ast.Inspect(mi.Decl.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkgIdent, ok := sel.X.(*ast.Ident)
		if !ok || pkgIdent.Name != "json" || sel.Sel.Name != "Unmarshal" {
			return true
		}
		if len(call.Args) != 2 {
			t.Fatalf("schema contract guard: %s: json.Unmarshal call with %d args, want 2", mi.Decl.Name.Name, len(call.Args))
		}
		argIdent, ok := call.Args[0].(*ast.Ident)
		if !ok || argIdent.Name != rawParam {
			return true // Not unmarshalling the tool's raw request payload.
		}
		found = true

		unary, ok := call.Args[1].(*ast.UnaryExpr)
		if !ok || unary.Op != token.AND {
			t.Fatalf("schema contract guard: %s: json.Unmarshal(%s, X) — X is not an &-expression (%#v); update resolveHandlerFields for this shape", mi.Decl.Name.Name, rawParam, call.Args[1])
		}
		destIdent, ok := unary.X.(*ast.Ident)
		if !ok {
			t.Fatalf("schema contract guard: %s: json.Unmarshal target is not a plain identifier (%#v); update resolveHandlerFields for this shape", mi.Decl.Name.Name, unary.X)
		}

		typeExpr := findLocalVarType(mi.Decl, destIdent.Name)
		if typeExpr == nil {
			t.Fatalf("schema contract guard: %s: cannot find the declaration of %q to resolve its type", mi.Decl.Name.Name, destIdent.Name)
		}

		for _, rf := range fieldsFromTypeExpr(t, typeExpr, mi.File, pkg, cache, moduleRoot) {
			seen[rf.JSONName] = rf
		}
		return true
	})

	if !found {
		if uses := rawParamUseCount(mi.Decl.Body, rawParam); uses > 0 {
			t.Fatalf("schema contract guard: %s: %q is referenced %d time(s) but never via json.Unmarshal(%s, &...) — this handler reads its raw payload in a shape resolveHandlerFields doesn't recognise; update it instead of leaving the guard blind (D6.1)", mi.Decl.Name.Name, rawParam, uses, rawParam)
		}
		return nil
	}

	out := make([]requestField, 0, len(seen))
	for _, rf := range seen {
		out = append(out, rf)
	}
	return out
}

// schemaPropertiesByTool reads InputSchema["properties"] for every tool in
// tools, in runtime (D5 step 6 — cheap, zero fragility, always current).
func schemaPropertiesByTool(t *testing.T, tools []ToolDefinition) map[string]map[string]any {
	t.Helper()
	out := make(map[string]map[string]any, len(tools))
	for _, tool := range tools {
		schema, ok := tool.InputSchema.(map[string]any)
		if !ok {
			t.Fatalf("schema contract guard: %s InputSchema is not map[string]any", tool.Name)
		}
		props, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("schema contract guard: %s InputSchema.properties is not map[string]any", tool.Name)
		}
		out[tool.Name] = props
	}
	return out
}

// schemaContractFixture bundles everything derived once per test: the
// tool→handler mapping, every handler's resolved request fields, and the
// live schema's declared properties. Building it is deterministic AST
// parsing plus one allTools() call — safe and cheap to redo per test.
type schemaContractFixture struct {
	dispatch []toolDispatch
	fields   map[string][]requestField // tool name -> resolved request fields
	props    map[string]map[string]any // tool name -> InputSchema.properties
}

// buildSchemaContractFixture resolves the full picture this guard's
// assertions read from. Any handler this fixture can't resolve fails the
// calling test immediately (D6.1) — there is no partial/best-effort mode.
func buildSchemaContractFixture(t *testing.T) schemaContractFixture {
	t.Helper()

	moduleRoot := schemaContractModuleRoot(t)
	dispatch := parseToolDispatch(t, moduleRoot)
	methods := loadHandlerMethods(t, moduleRoot)
	mcpPkg := loadPkgTypes(t, filepath.Join(moduleRoot, "internal", "mcp"))
	cache := map[string]*pkgTypes{modulePrefix + "internal/mcp": mcpPkg}

	tools := allTools()
	props := schemaPropertiesByTool(t, tools)

	fields := make(map[string][]requestField, len(dispatch))
	for _, d := range dispatch {
		mi, ok := methods[d.Handler]
		if !ok {
			t.Fatalf("schema contract guard: handleToolCall dispatches %q to h.%s, but no such method exists on *handlers in internal/mcp", d.Tool, d.Handler)
		}
		fields[d.Tool] = resolveHandlerFields(t, mi, mcpPkg, cache, moduleRoot)
	}

	return schemaContractFixture{dispatch: dispatch, fields: fields, props: props}
}

// TestSwitchToolsMatchSchemaTools is D6.2/AC6's free invariant: the set of
// tool names handleToolCall's switch dispatches and the set allTools()
// publishes must be identical — compared as sets, never against a magic
// count like the historical "79". This catches a tool that's callable but
// undocumented, or documented but undispatchable, independent of anything
// else in this file.
func TestSwitchToolsMatchSchemaTools(t *testing.T) {
	moduleRoot := schemaContractModuleRoot(t)
	dispatch := parseToolDispatch(t, moduleRoot)

	dispatched := make(map[string]bool, len(dispatch))
	for _, d := range dispatch {
		dispatched[d.Tool] = true
	}

	published := make(map[string]bool)
	for _, tool := range allTools() {
		published[tool.Name] = true
	}

	for name := range dispatched {
		if !published[name] {
			t.Errorf("handleToolCall dispatches %q but allTools() does not declare it", name)
		}
	}
	for name := range published {
		if !dispatched[name] {
			t.Errorf("allTools() declares %q but handleToolCall's switch has no case for it", name)
		}
	}
}

// TestEveryRequestFieldIsDeclaredInInputSchema is SPEC-113's central
// guardrail (AC3): every JSON-visible field on every tool's request type
// must either be declared in that tool's InputSchema.properties, or carry a
// justified, non-stale waiver in undeclaredFieldWaivers (D7). This is the
// regression class areas_complete belongs to — a client following the
// published contract must never be missing a type hint for a field the
// handler actually accepts.
func TestEveryRequestFieldIsDeclaredInInputSchema(t *testing.T) {
	fx := buildSchemaContractFixture(t)

	for _, d := range fx.dispatch {
		props, ok := fx.props[d.Tool]
		if !ok {
			t.Fatalf("schema contract guard: tool %q has no resolved schema properties", d.Tool)
		}
		waivers := undeclaredFieldWaivers[d.Tool]
		for _, f := range fx.fields[d.Tool] {
			if _, declared := props[f.JSONName]; declared {
				continue
			}
			reason, waived := waivers[f.JSONName]
			if !waived {
				t.Errorf("%s: request field %q is not declared in InputSchema.properties and has no waiver (D7) — declare it, exclude it with json:\"-\" if callers must never set it, or add a justified entry to undeclaredFieldWaivers", d.Tool, f.JSONName)
				continue
			}
			if reason == "" {
				t.Errorf("undeclaredFieldWaivers[%q][%q]: empty reason — every waiver must be justified (D7.1)", d.Tool, f.JSONName)
			}
		}
	}

	// D7.3: a waiver naming a field that no longer exists, or that is now
	// declared, is stale and must fail loudly — otherwise the table rots
	// and ends up shielding a brand-new field that happens to reuse the
	// same name.
	for tool, byField := range undeclaredFieldWaivers {
		fieldSet := make(map[string]bool, len(fx.fields[tool]))
		for _, f := range fx.fields[tool] {
			fieldSet[f.JSONName] = true
		}
		props := fx.props[tool]
		for field := range byField {
			if !fieldSet[field] {
				t.Errorf("undeclaredFieldWaivers[%q][%q]: field does not exist on the resolved request struct — stale waiver (D7.3), remove it", tool, field)
				continue
			}
			if _, declared := props[field]; declared {
				t.Errorf("undeclaredFieldWaivers[%q][%q]: field is now declared in the schema — stale waiver (D7.3), remove it", tool, field)
			}
		}
	}
}

// TestBoolRequestFieldsArePublishedAsBoolean is D6.3/AC7: for every request
// field whose Go type is literally bool or *bool AND whose JSON name IS
// published in its tool's schema, the published property must declare
// `"type": "boolean"`. This is the exact shape of the areas_complete defect
// — a bool field with a schema entry of the wrong declared type — and is
// deliberately the only cross-type-system check this guard performs (D6.3
// explains why: mapping all of Go's type system to JSON Schema from the AST
// is disproportionate to the one failure mode actually observed).
func TestBoolRequestFieldsArePublishedAsBoolean(t *testing.T) {
	fx := buildSchemaContractFixture(t)

	for tool, fields := range fx.fields {
		props := fx.props[tool]
		for _, f := range fields {
			if !f.IsBool {
				continue
			}
			prop, declared := props[f.JSONName]
			if !declared {
				continue // Undeclared is D7's concern, asserted elsewhere.
			}
			propMap, ok := prop.(map[string]any)
			if !ok {
				t.Errorf("%s.%s: schema property is not map[string]any (%#v)", tool, f.JSONName, prop)
				continue
			}
			if propMap["type"] != "boolean" {
				t.Errorf("%s.%s is a Go bool field but its published schema declares type %v, want \"boolean\" (D6.3)", tool, f.JSONName, propMap["type"])
			}
		}
	}
}
