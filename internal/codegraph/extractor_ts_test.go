package codegraph

import (
	"os"
	"os/exec"
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

// skipIfNoTS skips the test if Node.js or the typescript package is unavailable.
func skipIfNoTS(t *testing.T) {
	t.Helper()
	if !nodeJSAvailable() {
		t.Skip("Node.js not available")
	}
	if !typescriptAvailable() {
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
	if len(classes) != 1 || classes[0].Name != "UserService" {
		t.Errorf("classes = %v, want [UserService]", nodeNames(classes))
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
