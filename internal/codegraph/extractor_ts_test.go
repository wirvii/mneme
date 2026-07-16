package codegraph

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// nodeJSAvailable reports whether the "node" binary is on PATH.
func nodeJSAvailable() bool {
	_, err := exec.LookPath("node")
	return err == nil
}

// typescriptAvailable reports whether the TypeScript package can be required.
// It sets NODE_PATH to include the global npm modules directory so that
// globally installed packages are found by modern Node.js versions.
func typescriptAvailable() bool {
	cmd := exec.Command("node", "-e", "require('typescript')")
	cmd.Env = os.Environ()
	if globalRoot, err := exec.Command("npm", "root", "-g").Output(); err == nil {
		rootPath := strings.TrimSpace(string(globalRoot))
		if rootPath != "" {
			existing := os.Getenv("NODE_PATH")
			if existing != "" {
				cmd.Env = append(cmd.Env, "NODE_PATH="+rootPath+string(os.PathListSeparator)+existing)
			} else {
				cmd.Env = append(cmd.Env, "NODE_PATH="+rootPath)
			}
		}
	}
	return cmd.Run() == nil
}

// skipIfNoTS skips the test if Node.js or the typescript package is
// unavailable — UNLESS MNEME_TEST_REQUIRE_TS=1 is set (SPEC-088 D7), in which
// case an unavailable toolchain is a hard test failure instead of a silent
// skip. CI exports this so an unpinned/incompatible typescript can never
// again make the whole TS test suite pass by skipping everything: that is
// exactly how typescript@7 reached main undetected (a green CI run where
// every real assertion had been silently skipped is the same shape of
// failure as the guard this spec adds — see 019f686b, the guardian that
// can't detect its own removal).
//
// D7 deliberately does NOT widen typescriptAvailable() to accept typescript@7
// — doing so would make this skip fire (correctly, per the letter of "is TS
// available") on a machine where the product is still broken.
func skipIfNoTS(t *testing.T) {
	t.Helper()
	requireTS := os.Getenv("MNEME_TEST_REQUIRE_TS") == "1"
	if !nodeJSAvailable() {
		if requireTS {
			t.Fatal("Node.js not available and MNEME_TEST_REQUIRE_TS=1 is set")
		}
		t.Skip("Node.js not available")
	}
	if !typescriptAvailable() {
		if requireTS {
			t.Fatal("typescript package not available and MNEME_TEST_REQUIRE_TS=1 is set")
		}
		t.Skip("typescript package not available")
	}
}

// TestTSExtractor_Language verifies the extractor identifies itself as "typescript".
func TestTSExtractor_Language(t *testing.T) {
	ext := NewTSExtractor()
	defer ext.Close()
	if got := ext.Language(); got != "typescript" {
		t.Errorf("Language() = %q, want %q", got, "typescript")
	}
}

// TestTSExtractor_ExtractsFunction verifies that function declarations are
// extracted with correct export status and async detection.
func TestTSExtractor_ExtractsFunction(t *testing.T) {
	skipIfNoTS(t)

	ext := NewTSExtractor()
	defer ext.Close()

	source := `export function greet(name: string): string {
    return "Hello " + name;
}

function helper() {
    return 42;
}
`
	result, err := ext.Extract("greet.ts", []byte(source))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	funcs := filterNodes(result.Nodes, NodeKindFunction)
	if len(funcs) < 2 {
		t.Fatalf("got %d functions, want >= 2; names: %v", len(funcs), nodeNames(funcs))
	}

	var greetFound, helperFound bool
	for _, f := range funcs {
		switch f.Name {
		case "greet":
			greetFound = true
			if !f.IsExported {
				t.Error("greet should be exported")
			}
			if f.Signature == "" {
				t.Error("greet should have a signature")
			}
		case "helper":
			helperFound = true
			if f.IsExported {
				t.Error("helper should NOT be exported")
			}
		}
	}
	if !greetFound {
		t.Error("function 'greet' not found")
	}
	if !helperFound {
		t.Error("function 'helper' not found")
	}
}

// TestTSExtractor_ExtractsClass verifies that class declarations with methods
// are correctly extracted including static and async modifiers.
func TestTSExtractor_ExtractsClass(t *testing.T) {
	skipIfNoTS(t)

	ext := NewTSExtractor()
	defer ext.Close()

	source := `export class UserService {
    private db: Database;

    constructor(db: Database) {
        this.db = db;
    }

    async findById(id: string): Promise<User> {
        return this.db.get(id);
    }

    static create(): UserService {
        return new UserService(new Database());
    }
}
`
	result, err := ext.Extract("service.ts", []byte(source))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	classes := filterNodes(result.Nodes, NodeKindClass)
	if len(classes) != 1 {
		t.Fatalf("got %d classes, want 1; names: %v", len(classes), nodeNames(classes))
	}
	if classes[0].Name != "UserService" {
		t.Errorf("classes[0].Name = %q, want %q", classes[0].Name, "UserService")
	}
	if !classes[0].IsExported {
		t.Error("UserService should be exported")
	}

	methods := filterNodes(result.Nodes, NodeKindMethod)
	if len(methods) < 2 {
		t.Errorf("got %d methods, want >= 2 (constructor, findById, create); names: %v", len(methods), nodeNames(methods))
	}

	for _, m := range methods {
		switch m.Name {
		case "findById":
			if !m.IsAsync {
				t.Error("findById should be async")
			}
			if m.QualifiedName != "UserService.findById" {
				t.Errorf("findById.QualifiedName = %q, want %q", m.QualifiedName, "UserService.findById")
			}
		case "create":
			if !m.IsStatic {
				t.Error("create should be static")
			}
		}
	}
}

// TestTSExtractor_ExtractsInterface verifies that interface declarations are extracted.
func TestTSExtractor_ExtractsInterface(t *testing.T) {
	skipIfNoTS(t)

	ext := NewTSExtractor()
	defer ext.Close()

	source := `export interface Repository {
    find(id: string): Promise<Entity>;
    save(entity: Entity): Promise<void>;
}
`
	result, err := ext.Extract("types.ts", []byte(source))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	ifaces := filterNodes(result.Nodes, NodeKindInterface)
	if len(ifaces) != 1 || ifaces[0].Name != "Repository" {
		t.Errorf("interfaces = %v, want [Repository]", nodeNames(ifaces))
	}
	if !ifaces[0].IsExported {
		t.Error("Repository should be exported")
	}
}

// TestTSExtractor_ExtractsEnum verifies that enum declarations are extracted.
func TestTSExtractor_ExtractsEnum(t *testing.T) {
	skipIfNoTS(t)

	ext := NewTSExtractor()
	defer ext.Close()

	source := `export enum Status {
    Active = "active",
    Inactive = "inactive",
}
`
	result, err := ext.Extract("status.ts", []byte(source))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	enums := filterNodes(result.Nodes, NodeKindEnum)
	if len(enums) != 1 || enums[0].Name != "Status" {
		t.Errorf("enums = %v, want [Status]", nodeNames(enums))
	}
}

// TestTSExtractor_ExtractsTypeAlias verifies that type alias declarations are extracted.
func TestTSExtractor_ExtractsTypeAlias(t *testing.T) {
	skipIfNoTS(t)

	ext := NewTSExtractor()
	defer ext.Close()

	source := `export type UserID = string;
type Config = { host: string; port: number };
`
	result, err := ext.Extract("types.ts", []byte(source))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	aliases := filterNodes(result.Nodes, NodeKindTypeAlias)
	if len(aliases) < 2 {
		t.Errorf("got %d type aliases, want >= 2; names: %v", len(aliases), nodeNames(aliases))
	}
}

// TestTSExtractor_ExtractsImports verifies that import declarations produce
// import nodes and imports edges.
func TestTSExtractor_ExtractsImports(t *testing.T) {
	skipIfNoTS(t)

	ext := NewTSExtractor()
	defer ext.Close()

	source := `import { Router } from 'express';
import * as path from 'path';
import fs from 'fs';

export function main() {}
`
	result, err := ext.Extract("app.ts", []byte(source))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	imports := filterNodes(result.Nodes, NodeKindImport)
	if len(imports) < 3 {
		t.Errorf("got %d imports, want >= 3; names: %v", len(imports), nodeNames(imports))
	}

	importsEdges := filterEdges(result.Edges, EdgeKindImports)
	if len(importsEdges) < 3 {
		t.Errorf("got %d imports edges, want >= 3", len(importsEdges))
	}
}

// TestTSExtractor_ContainsEdges verifies that the file node has contains edges
// to each top-level declaration.
func TestTSExtractor_ContainsEdges(t *testing.T) {
	skipIfNoTS(t)

	ext := NewTSExtractor()
	defer ext.Close()

	source := `export function add(a: number, b: number): number { return a + b; }`
	result, err := ext.Extract("math.ts", []byte(source))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	contains := filterEdges(result.Edges, EdgeKindContains)
	if len(contains) == 0 {
		t.Error("expected contains edges (file -> function)")
	}
}

// TestTSExtractor_CallEdges verifies that call expressions within function
// bodies are captured as calls edges or unresolved references.
func TestTSExtractor_CallEdges(t *testing.T) {
	skipIfNoTS(t)

	ext := NewTSExtractor()
	defer ext.Close()

	source := `function helper(): number { return 42; }

function main() {
    const x = helper();
    console.log(x);
}
`
	result, err := ext.Extract("calls.ts", []byte(source))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	callEdges := filterEdges(result.Edges, EdgeKindCalls)
	if len(callEdges) == 0 && len(result.UnresolvedRefs) == 0 {
		t.Error("expected at least one call edge or unresolved ref for function calls")
	}
}

// TestTSExtractor_ArrowFunction verifies that arrow functions assigned to
// const/let are extracted as function nodes.
func TestTSExtractor_ArrowFunction(t *testing.T) {
	skipIfNoTS(t)

	ext := NewTSExtractor()
	defer ext.Close()

	source := `export const greet = (name: string): string => {
    return "Hello " + name;
};

const add = (a: number, b: number) => a + b;
`
	result, err := ext.Extract("arrows.ts", []byte(source))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	funcs := filterNodes(result.Nodes, NodeKindFunction)
	if len(funcs) < 2 {
		t.Errorf("got %d functions, want >= 2 (greet, add); names: %v", len(funcs), nodeNames(funcs))
	}

	for _, f := range funcs {
		if f.Name == "greet" && !f.IsExported {
			t.Error("greet should be exported")
		}
	}
}

// TestTSExtractor_ExtendsImplements verifies that extends/implements heritage
// clauses produce the correct edge kinds.
func TestTSExtractor_ExtendsImplements(t *testing.T) {
	skipIfNoTS(t)

	ext := NewTSExtractor()
	defer ext.Close()

	source := `interface Animal {
    name: string;
}

interface Domestic {
    owner: string;
}

class Dog implements Animal, Domestic {
    name: string;
    owner: string;
}

class Poodle extends Dog {
    size: string;
}
`
	result, err := ext.Extract("animals.ts", []byte(source))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	extendsEdges := filterEdges(result.Edges, EdgeKindExtends)
	implementsEdges := filterEdges(result.Edges, EdgeKindImplements)

	if len(extendsEdges) == 0 {
		t.Error("expected at least one extends edge (Poodle -> Dog)")
	}
	if len(implementsEdges) == 0 {
		t.Error("expected at least one implements edge (Dog -> Animal or Dog -> Domestic)")
	}
}

// TestTSExtractor_JavaScript verifies that the extractor handles plain .js files.
func TestTSExtractor_JavaScript(t *testing.T) {
	skipIfNoTS(t)

	ext := NewTSExtractor()
	defer ext.Close()

	source := `const express = require('express');

function startServer(port) {
    const app = express();
    app.listen(port);
}

module.exports = { startServer };
`
	result, err := ext.Extract("server.js", []byte(source))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	funcs := filterNodes(result.Nodes, NodeKindFunction)
	if len(funcs) < 1 {
		t.Errorf("got %d functions, want >= 1; all nodes: %v", len(funcs), nodeNames(result.Nodes))
	}

	// File node should report language=javascript
	fileNodes := filterNodes(result.Nodes, NodeKindFile)
	if len(fileNodes) == 1 && fileNodes[0].Language != "javascript" {
		t.Errorf("file node language = %q, want javascript", fileNodes[0].Language)
	}
}

// TestTSExtractor_NodeID_MatchesGo verifies that the Node.js NodeID function
// produces the same IDs as the Go NodeID function.
func TestTSExtractor_NodeID_MatchesGo(t *testing.T) {
	skipIfNoTS(t)

	ext := NewTSExtractor()
	defer ext.Close()

	source := `export function hello(): void {}`
	result, err := ext.Extract("test.ts", []byte(source))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	// The file node should match Go's NodeID("test.ts", "test.ts")
	expectedFileID := NodeID("test.ts", "test.ts")
	fileNodes := filterNodes(result.Nodes, NodeKindFile)
	if len(fileNodes) != 1 {
		t.Fatalf("got %d file nodes, want 1", len(fileNodes))
	}
	if fileNodes[0].ID != expectedFileID {
		t.Errorf("file node ID = %q, want %q (Go NodeID)", fileNodes[0].ID, expectedFileID)
	}

	// The function node should match Go's NodeID("test.ts", "hello")
	expectedFuncID := NodeID("test.ts", "hello")
	funcs := filterNodes(result.Nodes, NodeKindFunction)
	if len(funcs) != 1 {
		t.Fatalf("got %d function nodes, want 1", len(funcs))
	}
	if funcs[0].ID != expectedFuncID {
		t.Errorf("function node ID = %q, want %q (Go NodeID)", funcs[0].ID, expectedFuncID)
	}
}

// TestTSExtractor_Docstring verifies that JSDoc comments are extracted.
func TestTSExtractor_Docstring(t *testing.T) {
	skipIfNoTS(t)

	ext := NewTSExtractor()
	defer ext.Close()

	source := `/** Greets a user by name. */
export function greet(name: string): string {
    return "Hello " + name;
}
`
	result, err := ext.Extract("doc.ts", []byte(source))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	funcs := filterNodes(result.Nodes, NodeKindFunction)
	for _, f := range funcs {
		if f.Name == "greet" {
			if f.Docstring == "" {
				t.Error("greet should have a docstring")
			}
			return
		}
	}
	t.Error("function 'greet' not found")
}

// ---------------------------------------------------------------------------
// SPEC-048 C11 — imported-binding calls emit unresolved_ref, not calls→import
// ---------------------------------------------------------------------------

// TestTSExtractor_ImportedNamedCallEmitsUnresolvedRef verifies AC1 (named import):
// a call to a named imported binding must NOT produce an edge to the import node;
// instead it must produce an unresolved_ref with reference_name equal to the
// binding name and reference_kind='calls'. The import node and its imports edge
// must still exist.
func TestTSExtractor_ImportedNamedCallEmitsUnresolvedRef(t *testing.T) {
	skipIfNoTS(t)

	ext := NewTSExtractor()
	defer ext.Close()

	source := `import { getUser } from './users';

export function handler() {
    const u = getUser('123');
    return u;
}
`
	result, err := ext.Extract("handler.ts", []byte(source))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	// Import node must still exist.
	imports := filterNodes(result.Nodes, NodeKindImport)
	if len(imports) == 0 {
		t.Fatal("expected import node for getUser")
	}
	var importNodeID string
	for _, n := range imports {
		if n.Name == "getUser" {
			importNodeID = n.ID
		}
	}
	if importNodeID == "" {
		t.Fatal("import node for 'getUser' not found")
	}

	// imports edge (file → import node) must still exist.
	importsEdges := filterEdges(result.Edges, EdgeKindImports)
	if len(importsEdges) == 0 {
		t.Error("expected at least one imports edge (file → import node)")
	}

	// NO calls edge to the import node.
	for _, e := range result.Edges {
		if e.Kind == EdgeKindCalls && e.Target == importNodeID {
			t.Errorf("found calls edge to import node %q — must not exist (should be unresolved_ref instead)", importNodeID)
		}
	}

	// Must have an unresolved_ref for 'getUser'.
	var found bool
	for _, ref := range result.UnresolvedRefs {
		if ref.ReferenceName == "getUser" && ref.ReferenceKind == EdgeKindCalls {
			found = true
			if ref.FilePath == "" {
				t.Error("unresolved_ref.file_path must not be empty")
			}
			if ref.Language == "" {
				t.Error("unresolved_ref.language must not be empty")
			}
		}
	}
	if !found {
		t.Errorf("no unresolved_ref with reference_name='getUser' found; got: %+v", result.UnresolvedRefs)
	}
}

// TestTSExtractor_ImportedDefaultCallEmitsUnresolvedRef verifies AC1 (default import):
// a call to a default imported binding emits an unresolved_ref, not a calls→import edge.
func TestTSExtractor_ImportedDefaultCallEmitsUnresolvedRef(t *testing.T) {
	skipIfNoTS(t)

	ext := NewTSExtractor()
	defer ext.Close()

	source := `import getPayloadClient from './payload';

export async function page() {
    const payload = await getPayloadClient();
    return payload;
}
`
	result, err := ext.Extract("page.ts", []byte(source))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	// import node must still exist.
	imports := filterNodes(result.Nodes, NodeKindImport)
	if len(imports) == 0 {
		t.Fatal("expected import node for default import")
	}
	var importNodeID string
	for _, n := range imports {
		if n.Name == "getPayloadClient" {
			importNodeID = n.ID
		}
	}
	if importNodeID == "" {
		t.Fatal("import node for 'getPayloadClient' not found")
	}

	// NO calls edge to import node.
	for _, e := range result.Edges {
		if e.Kind == EdgeKindCalls && e.Target == importNodeID {
			t.Errorf("found calls edge to import node %q — must not exist", importNodeID)
		}
	}

	// Must have unresolved_ref for 'getPayloadClient'.
	var found bool
	for _, ref := range result.UnresolvedRefs {
		if ref.ReferenceName == "getPayloadClient" && ref.ReferenceKind == EdgeKindCalls {
			found = true
		}
	}
	if !found {
		t.Errorf("no unresolved_ref with reference_name='getPayloadClient'; got: %+v", result.UnresolvedRefs)
	}
}

// TestTSExtractor_ImportedNamespaceCallEmitsUnresolvedRef verifies AC1 (namespace import):
// a call via a namespace binding (ns.member) emits an unresolved_ref with
// reference_name="ns.member", not a calls→import edge.
func TestTSExtractor_ImportedNamespaceCallEmitsUnresolvedRef(t *testing.T) {
	skipIfNoTS(t)

	ext := NewTSExtractor()
	defer ext.Close()

	source := `import * as utils from './utils';

export function run() {
    return utils.formatDate(new Date());
}
`
	result, err := ext.Extract("run.ts", []byte(source))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	// import node for 'utils' must still exist.
	imports := filterNodes(result.Nodes, NodeKindImport)
	var importNodeID string
	for _, n := range imports {
		if n.Name == "utils" {
			importNodeID = n.ID
		}
	}
	if importNodeID == "" {
		t.Fatal("import node for namespace 'utils' not found")
	}

	// NO calls edge to import node.
	for _, e := range result.Edges {
		if e.Kind == EdgeKindCalls && e.Target == importNodeID {
			t.Errorf("found calls edge to namespace import node %q — must not exist", importNodeID)
		}
	}

	// Must have unresolved_ref with reference_name="utils.formatDate".
	var found bool
	for _, ref := range result.UnresolvedRefs {
		if ref.ReferenceName == "utils.formatDate" && ref.ReferenceKind == EdgeKindCalls {
			found = true
		}
	}
	if !found {
		t.Errorf("no unresolved_ref with reference_name='utils.formatDate'; got: %+v", result.UnresolvedRefs)
	}
}

// TestTSExtractor_ImportedHoistingCoverage verifies R1 (hoisting): when an import
// declaration appears AFTER the function that uses it (valid TS hoisting), the
// pre-scan still identifies the binding and the call emits an unresolved_ref
// (not a calls→import edge).
func TestTSExtractor_ImportedHoistingCoverage(t *testing.T) {
	skipIfNoTS(t)

	ext := NewTSExtractor()
	defer ext.Close()

	// NOTE: In TS, class method bodies are walked during the first visit() pass.
	// The import appears AFTER the class declaration — the pre-scan must catch it
	// before visit() processes the class body.
	source := `export class Service {
    doWork() {
        return helper();
    }
}

import { helper } from './helpers';
`
	result, err := ext.Extract("service.ts", []byte(source))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	// import node must still exist.
	imports := filterNodes(result.Nodes, NodeKindImport)
	var importNodeID string
	for _, n := range imports {
		if n.Name == "helper" {
			importNodeID = n.ID
		}
	}
	if importNodeID == "" {
		t.Fatal("import node for 'helper' not found")
	}

	// NO calls edge to the import node.
	for _, e := range result.Edges {
		if e.Kind == EdgeKindCalls && e.Target == importNodeID {
			t.Errorf("found calls edge to import node %q (hoisting case) — must not exist", importNodeID)
		}
	}

	// Must have unresolved_ref for 'helper'.
	var found bool
	for _, ref := range result.UnresolvedRefs {
		if ref.ReferenceName == "helper" && ref.ReferenceKind == EdgeKindCalls {
			found = true
		}
	}
	if !found {
		t.Errorf("no unresolved_ref for 'helper' in hoisting case; got: %+v", result.UnresolvedRefs)
	}
}

// TestTSExtractor_SameFileCallStillUsesEdge verifies that calls to symbols
// declared in the SAME file (not imported) still produce direct calls edges,
// not unresolved_refs — the new importedBindings check must not affect same-file resolution.
func TestTSExtractor_SameFileCallStillUsesEdge(t *testing.T) {
	skipIfNoTS(t)

	ext := NewTSExtractor()
	defer ext.Close()

	source := `function helper(): number { return 42; }

function main() {
    const x = helper();
    return x;
}
`
	result, err := ext.Extract("same.ts", []byte(source))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	callEdges := filterEdges(result.Edges, EdgeKindCalls)
	if len(callEdges) == 0 {
		t.Error("expected a direct calls edge for same-file call to helper()")
	}

	// Verify the calls edge points to the helper function, not an import node.
	for _, e := range callEdges {
		// Find the target node.
		for _, n := range result.Nodes {
			if n.ID == e.Target && n.Kind == NodeKindImport {
				t.Errorf("calls edge target %q is an import node — expected a function node", e.Target)
			}
		}
	}
}

// TestTSExtractor_MultipleFiles verifies that the subprocess can handle
// multiple sequential extractions without restarting.
func TestTSExtractor_MultipleFiles(t *testing.T) {
	skipIfNoTS(t)

	ext := NewTSExtractor()
	defer ext.Close()

	files := []struct {
		path    string
		content string
	}{
		{"a.ts", "export function a(): void {}"},
		{"b.ts", "export function b(): void {}"},
		{"c.ts", "export function c(): void {}"},
	}

	for _, f := range files {
		result, err := ext.Extract(f.path, []byte(f.content))
		if err != nil {
			t.Fatalf("Extract(%q): %v", f.path, err)
		}
		funcs := filterNodes(result.Nodes, NodeKindFunction)
		if len(funcs) != 1 {
			t.Errorf("Extract(%q): got %d functions, want 1", f.path, len(funcs))
		}
	}
}

// ---------------------------------------------------------------------------
// SPEC-088 — guards for the typescript@7 silent-failure hotfix (G1, G3)
// ---------------------------------------------------------------------------

// writeFakeIncompatibleTypeScript writes a fake "typescript" package under
// <dir>/node_modules/typescript whose export surface matches typescript@7.0.2
// exactly: only `version`/`versionMajorMinor`, none of the classic Compiler
// API. Returns the node_modules directory (suitable for NODE_PATH). Real
// production behaviour depends on the *shape* of the export, not a
// hand-picked constant, so this fixture is deliberately minimal rather than
// a full fake module.
func writeFakeIncompatibleTypeScript(t *testing.T, dir string) string {
	t.Helper()
	tsDir := filepath.Join(dir, "node_modules", "typescript")
	if err := os.MkdirAll(tsDir, 0o755); err != nil {
		t.Fatalf("mkdir fake typescript: %v", err)
	}
	fakeTS := "module.exports = { version: '7.0.2', versionMajorMinor: '7.0' };\n"
	if err := os.WriteFile(filepath.Join(tsDir, "index.js"), []byte(fakeTS), 0o644); err != nil {
		t.Fatalf("write fake typescript/index.js: %v", err)
	}
	return filepath.Join(dir, "node_modules")
}

// TestExtractJS_IncompatibleTypeScriptAPI_ExitsNonZero is G1 (SPEC-088 D2/D3).
// It runs the REAL embedded extract.js — the exact byte array
// (`extractJS`) that ships inside the binary — as a subprocess against a
// typescript module shaped like typescript@7.0.2's export surface, and
// asserts the API-capability guard fires: exit 20, no stdout (the guard
// exits before the JSONL protocol even starts), and stderr naming the found
// version.
//
// Only requires `node` on PATH, not a real typescript install, so it runs
// even on machines where skipIfNoTS would otherwise skip everything.
//
// Mutation proof (SPEC-088 AC9, executed manually — see the implementation
// report): commenting out the `missingAPI.length > 0` guard block in
// js/extract.js turns this red, because node then proceeds into
// ts.createSourceFile against the fake module, throws inside extractFile's
// try/catch, and exits 0 with an empty-but-valid JSON result — exactly the
// silent failure this test exists to catch.
func TestExtractJS_IncompatibleTypeScriptAPI_ExitsNonZero(t *testing.T) {
	if !nodeJSAvailable() {
		t.Skip("Node.js not available")
	}

	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "extract.js")
	if err := os.WriteFile(scriptPath, extractJS, 0o644); err != nil {
		t.Fatalf("write extract.js: %v", err)
	}

	nodeModules := writeFakeIncompatibleTypeScript(t, tmpDir)

	cmd := exec.Command("node", scriptPath)
	cmd.Env = append(os.Environ(), "NODE_PATH="+nodeModules)
	cmd.Stdin = strings.NewReader(`{"path":"test.ts","content":"export function hello(): void {}"}` + "\n")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) {
		t.Fatalf("expected an *exec.ExitError, got %v (stdout=%q stderr=%q)", runErr, stdout.String(), stderr.String())
	}
	if exitErr.ExitCode() != 20 {
		t.Errorf("exit code = %d, want 20; stderr=%s", exitErr.ExitCode(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "7.0.2") {
		t.Errorf("stderr does not name the found version 7.0.2: %s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("expected no stdout (guard must exit before the JSONL protocol starts), got %q", stdout.String())
	}
}

// TestTSExtractor_IncompatibleTypeScript_ReturnsSentinel is G3 (SPEC-088 D4,
// D5). With NODE_PATH pointed at a typescript module shaped like
// typescript@7 (no Compiler API), Extract must:
//  1. return errors.Is(err, ErrExtractorIncompatible) — never (result, nil);
//  2. include what the D2 guard said on stderr in the error message.
//
// This test only works because of the D5 seam: an explicit NODE_PATH must
// win over the ambient global npm root, or the fake module set up here would
// never be resolved and the test would silently exercise the real global
// typescript instead. Mutation B below proves that dependency.
//
// Mutation proofs (SPEC-088 AC9, executed manually — see the implementation
// report):
//   - Mutation A: change tsIncompatibleExitCode from 20 to another value —
//     checkDeath's `exitErr.ExitCode() == tsIncompatibleExitCode` comparison
//     stops matching the guard's actual exit(20), so Extract falls through to
//     the ordinary "no response from node" error and errors.Is fails.
//   - Mutation B: revert the D5 NODE_PATH ordering (global root first again)
//     — the real global typescript@6.0.3 wins, the fake module set up by this
//     test is never resolved, extraction succeeds normally, and
//     errors.Is(err, ErrExtractorIncompatible) fails because err is nil.
func TestTSExtractor_IncompatibleTypeScript_ReturnsSentinel(t *testing.T) {
	if !nodeJSAvailable() {
		t.Skip("Node.js not available")
	}

	nodeModules := writeFakeIncompatibleTypeScript(t, t.TempDir())
	t.Setenv("NODE_PATH", nodeModules)

	ext := NewTSExtractor()
	defer ext.Close()

	result, err := ext.Extract("test.ts", []byte("export function hello(): void {}"))
	if result != nil {
		t.Errorf("result = %+v, want nil", result)
	}
	if !errors.Is(err, ErrExtractorIncompatible) {
		t.Fatalf("err = %v, want errors.Is ErrExtractorIncompatible", err)
	}
	if !strings.Contains(err.Error(), "7.0.2") {
		t.Errorf("err does not include the guard's stderr (found_version 7.0.2): %v", err)
	}
}
