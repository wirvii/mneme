package codegraph

import (
	"testing"
	"time"
)

// insertImportNode inserts a NodeKindImport node representing an import
// declaration, with the given importPath as Name/QualifiedName and alias as
// ImportAlias. Returns the inserted node.
func insertImportNode(t *testing.T, s *Store, filePath, importPath, alias string) Node {
	t.Helper()
	id := NodeID(filePath, importPath)
	n := Node{
		ID:            id,
		Kind:          NodeKindImport,
		Name:          importPath,
		QualifiedName: importPath,
		FilePath:      filePath,
		Language:      "go",
		StartLine:     1,
		EndLine:       1,
		UpdatedAt:     time.Now().Unix(),
		ImportAlias:   alias,
	}
	if err := s.UpsertNode(n); err != nil {
		t.Fatalf("insertImportNode(%s): %v", importPath, err)
	}
	return n
}

// sampleUnresolvedRef returns a minimal UnresolvedRef for test setup.
func sampleUnresolvedRef(fromNodeID, referenceName string, kind EdgeKind) UnresolvedRef {
	return UnresolvedRef{
		FromNodeID:    fromNodeID,
		ReferenceName: referenceName,
		ReferenceKind: kind,
		Line:          1,
		Col:           0,
		FilePath:      "internal/foo/foo.go",
		Language:      "go",
	}
}

// insertNode is a test helper that upserts a node and returns it.
func insertNode(t *testing.T, s *Store, id, name, qualifiedName, filePath string) Node {
	t.Helper()
	n := Node{
		ID:            id,
		Kind:          NodeKindFunction,
		Name:          name,
		QualifiedName: qualifiedName,
		FilePath:      filePath,
		Language:      "go",
		StartLine:     1,
		EndLine:       10,
		UpdatedAt:     time.Now().Unix(),
	}
	if err := s.UpsertNode(n); err != nil {
		t.Fatalf("UpsertNode(%s): %v", id, err)
	}
	return n
}

// TestResolver_ResolvesCallByQualifiedName verifies that an unresolved ref whose
// ReferenceName exactly matches a node's QualifiedName is resolved to a calls edge.
func TestResolver_ResolvesCallByQualifiedName(t *testing.T) {
	s := newTestStore(t)

	// Insert the callee node.
	callee := insertNode(t, s, "aaaa0001", "Foo", "pkg.Foo", "internal/pkg/foo.go")

	// Insert the caller node.
	caller := insertNode(t, s, "bbbb0002", "caller", "main.caller", "cmd/main.go")

	// Insert an unresolved ref from caller → pkg.Foo (exact qualified name match).
	ref := sampleUnresolvedRef(caller.ID, "pkg.Foo", EdgeKindCalls)
	if err := s.UpsertUnresolvedRef(ref); err != nil {
		t.Fatalf("UpsertUnresolvedRef: %v", err)
	}

	r := NewResolver(s)
	result, err := r.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if result.Resolved != 1 {
		t.Errorf("Resolved = %d, want 1", result.Resolved)
	}
	if result.Unresolved != 0 {
		t.Errorf("Unresolved = %d, want 0", result.Unresolved)
	}

	// Edge must exist: caller → callee with kind=calls.
	edges, err := s.GetEdgesFrom(caller.ID, string(EdgeKindCalls))
	if err != nil {
		t.Fatalf("GetEdgesFrom: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("want 1 calls edge from caller, got %d", len(edges))
	}
	if edges[0].Target != callee.ID {
		t.Errorf("edge target = %q, want %q", edges[0].Target, callee.ID)
	}

	// Unresolved ref must be gone.
	refs, err := s.ListUnresolvedRefs()
	if err != nil {
		t.Fatalf("ListUnresolvedRefs: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("unresolved_refs still has %d rows, want 0", len(refs))
	}
}

// TestResolver_ResolvesCallByNameFallback verifies that when exact qualified name
// matching fails, the resolver falls back to matching nodes.name.
func TestResolver_ResolvesCallByNameFallback(t *testing.T) {
	s := newTestStore(t)

	// Node with short name "Bar" but qualified name "other.Bar" (not matching the ref).
	callee := insertNode(t, s, "cccc0003", "Bar", "other.Bar", "internal/other/bar.go")
	caller := insertNode(t, s, "dddd0004", "caller2", "main.caller2", "cmd/main.go")

	// Unresolved ref uses only the short name "Bar".
	ref := sampleUnresolvedRef(caller.ID, "Bar", EdgeKindCalls)
	if err := s.UpsertUnresolvedRef(ref); err != nil {
		t.Fatalf("UpsertUnresolvedRef: %v", err)
	}

	r := NewResolver(s)
	result, err := r.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if result.Resolved != 1 {
		t.Errorf("Resolved = %d, want 1", result.Resolved)
	}

	edges, err := s.GetEdgesFrom(caller.ID, string(EdgeKindCalls))
	if err != nil {
		t.Fatalf("GetEdgesFrom: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("want 1 calls edge, got %d", len(edges))
	}
	if edges[0].Target != callee.ID {
		t.Errorf("edge target = %q, want %q", edges[0].Target, callee.ID)
	}
}

// TestResolver_ResolvesCallBySuffixMatch verifies that a reference like "pkg.Foo"
// resolves a node whose qualified_name ends in ".pkg.Foo" (suffix match).
func TestResolver_ResolvesCallBySuffixMatch(t *testing.T) {
	s := newTestStore(t)

	// Node with a longer qualified name that has "store.Create" as a suffix.
	callee := insertNode(t, s, "eeee0005", "Create", "internal/store.Create", "internal/store/store.go")
	caller := insertNode(t, s, "ffff0006", "handler", "http.handler", "internal/http/handler.go")

	// Ref uses a partial qualified name that should match as a suffix.
	ref := sampleUnresolvedRef(caller.ID, "store.Create", EdgeKindCalls)
	if err := s.UpsertUnresolvedRef(ref); err != nil {
		t.Fatalf("UpsertUnresolvedRef: %v", err)
	}

	r := NewResolver(s)
	result, err := r.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if result.Resolved != 1 {
		t.Errorf("Resolved = %d, want 1", result.Resolved)
	}

	edges, err := s.GetEdgesFrom(caller.ID, string(EdgeKindCalls))
	if err != nil {
		t.Fatalf("GetEdgesFrom: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("want 1 edge, got %d", len(edges))
	}
	if edges[0].Target != callee.ID {
		t.Errorf("edge target = %q, want %q", edges[0].Target, callee.ID)
	}
}

// TestResolver_UnresolvableStays verifies that a ref with no matching node is
// left in the database and counted as Unresolved.
func TestResolver_UnresolvableStays(t *testing.T) {
	s := newTestStore(t)

	// Only the caller node exists; there is no matching callee.
	caller := insertNode(t, s, "aaaa0010", "orphan", "main.orphan", "cmd/main.go")

	ref := sampleUnresolvedRef(caller.ID, "unknown.DoesNotExist", EdgeKindCalls)
	if err := s.UpsertUnresolvedRef(ref); err != nil {
		t.Fatalf("UpsertUnresolvedRef: %v", err)
	}

	r := NewResolver(s)
	result, err := r.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if result.Resolved != 0 {
		t.Errorf("Resolved = %d, want 0", result.Resolved)
	}
	if result.Unresolved != 1 {
		t.Errorf("Unresolved = %d, want 1", result.Unresolved)
	}

	// Ref must still exist.
	refs, err := s.ListUnresolvedRefs()
	if err != nil {
		t.Fatalf("ListUnresolvedRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Errorf("unresolved_refs count = %d, want 1", len(refs))
	}
}

// TestResolver_MultipleRefs verifies that with 3 refs (2 resolvable, 1 not),
// the counters are Resolved=2 and Unresolved=1.
func TestResolver_MultipleRefs(t *testing.T) {
	s := newTestStore(t)

	caller := insertNode(t, s, "aaaa0020", "multi", "main.multi", "cmd/main.go")
	targetA := insertNode(t, s, "bbbb0021", "Alpha", "pkg.Alpha", "internal/pkg/alpha.go")
	targetB := insertNode(t, s, "cccc0022", "Beta", "pkg.Beta", "internal/pkg/beta.go")

	refs := []UnresolvedRef{
		sampleUnresolvedRef(caller.ID, "pkg.Alpha", EdgeKindCalls), // resolvable (exact)
		sampleUnresolvedRef(caller.ID, "pkg.Beta", EdgeKindCalls),  // resolvable (exact)
		sampleUnresolvedRef(caller.ID, "ghost.Nope", EdgeKindCalls), // unresolvable
	}
	for _, ref := range refs {
		if err := s.UpsertUnresolvedRef(ref); err != nil {
			t.Fatalf("UpsertUnresolvedRef: %v", err)
		}
	}

	r := NewResolver(s)
	result, err := r.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if result.Resolved != 2 {
		t.Errorf("Resolved = %d, want 2", result.Resolved)
	}
	if result.Unresolved != 1 {
		t.Errorf("Unresolved = %d, want 1", result.Unresolved)
	}

	// Both resolvable targets should have an incoming calls edge.
	for _, target := range []Node{targetA, targetB} {
		edges, err := s.GetEdgesTo(target.ID, string(EdgeKindCalls))
		if err != nil {
			t.Fatalf("GetEdgesTo(%s): %v", target.ID, err)
		}
		if len(edges) != 1 {
			t.Errorf("node %s: want 1 incoming calls edge, got %d", target.Name, len(edges))
		}
	}
}

// TestResolver_DoesNotCreateDuplicateEdges verifies that running Resolve twice
// does not create duplicate edges when the same ref exists across two runs.
// This simulates the scenario where Resolve is called a second time after a
// partial run left some refs in the DB (which should not happen in practice)
// or when a caller deliberately invokes Resolve multiple times.
func TestResolver_DoesNotCreateDuplicateEdges(t *testing.T) {
	s := newTestStore(t)

	callee := insertNode(t, s, "aaaa0030", "Zap", "pkg.Zap", "internal/pkg/zap.go")
	caller := insertNode(t, s, "bbbb0031", "user", "main.user", "cmd/main.go")

	ref := sampleUnresolvedRef(caller.ID, "pkg.Zap", EdgeKindCalls)
	if err := s.UpsertUnresolvedRef(ref); err != nil {
		t.Fatalf("UpsertUnresolvedRef: %v", err)
	}

	r := NewResolver(s)

	// First resolve — should create 1 edge.
	result1, err := r.Resolve("")
	if err != nil {
		t.Fatalf("first Resolve: %v", err)
	}
	if result1.Resolved != 1 {
		t.Fatalf("first Resolve: Resolved = %d, want 1", result1.Resolved)
	}

	// Re-insert the ref to simulate a second resolve pass on the same ref.
	ref2 := sampleUnresolvedRef(caller.ID, "pkg.Zap", EdgeKindCalls)
	if err := s.UpsertUnresolvedRef(ref2); err != nil {
		t.Fatalf("UpsertUnresolvedRef (second): %v", err)
	}

	// Second resolve — should resolve again but NOT create a duplicate edge.
	result2, err := r.Resolve("")
	if err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	if result2.Resolved != 1 {
		t.Fatalf("second Resolve: Resolved = %d, want 1", result2.Resolved)
	}

	// Exactly 1 edge should exist — no duplicates.
	edges, err := s.GetEdgesFrom(caller.ID, string(EdgeKindCalls))
	if err != nil {
		t.Fatalf("GetEdgesFrom: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("want exactly 1 calls edge, got %d (duplicate edge created)", len(edges))
	}
	if edges[0].Target != callee.ID {
		t.Errorf("edge target = %q, want %q", edges[0].Target, callee.ID)
	}
}

// ---------------------------------------------------------------------------
// SPEC-047 C3 — import-guided cross-package resolution (AC4–AC7)
// ---------------------------------------------------------------------------

// TestResolver_ImportGuidedGoUnique verifies AC4: when package a imports
// package b and calls b.Foo(), and exactly one Foo exists in b's directory,
// the resolver creates a calls edge with provenance="import".
func TestResolver_ImportGuidedGoUnique(t *testing.T) {
	s := newTestStore(t)

	// Caller in package a.
	caller := insertNode(t, s, "aaaa1001", "Run", "Run", "internal/a/a.go")

	// Callee: unique Foo in internal/b.
	callee := insertNode(t, s, "bbbb1002", "Foo", "Foo", "internal/b/b.go")

	// Import node: file a.go imports "internal/b" with binding "b".
	insertImportNode(t, s, "internal/a/a.go", "internal/b", "b")

	// Unresolved ref: caller calls b.Foo.
	ref := UnresolvedRef{
		FromNodeID:    caller.ID,
		ReferenceName: "b.Foo",
		ReferenceKind: EdgeKindCalls,
		Line:          5,
		Col:           0,
		FilePath:      "internal/a/a.go",
		Language:      "go",
	}
	if err := s.UpsertUnresolvedRef(ref); err != nil {
		t.Fatalf("UpsertUnresolvedRef: %v", err)
	}

	r := NewResolver(s)
	result, err := r.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if result.Resolved != 1 {
		t.Errorf("Resolved = %d, want 1", result.Resolved)
	}
	if result.Unresolved != 0 {
		t.Errorf("Unresolved = %d, want 0", result.Unresolved)
	}

	edges, err := s.GetEdgesFrom(caller.ID, string(EdgeKindCalls))
	if err != nil {
		t.Fatalf("GetEdgesFrom: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("want 1 calls edge, got %d", len(edges))
	}
	if edges[0].Target != callee.ID {
		t.Errorf("edge target = %q, want %q", edges[0].Target, callee.ID)
	}
	if edges[0].Provenance != "import" {
		t.Errorf("provenance = %q, want \"import\"", edges[0].Provenance)
	}
}

// TestResolver_ImportGuidedGoAmbiguous verifies AC5: when two functions named
// Foo exist in the target package's directory, the import-guided tier does NOT
// create an edge with provenance="import" (candidato-único-o-nada). A later tier
// may still resolve the reference — the key invariant is that no import-guided
// edge is created when there are 2+ candidates.
func TestResolver_ImportGuidedGoAmbiguous(t *testing.T) {
	s := newTestStore(t)

	caller := insertNode(t, s, "aaaa1010", "Run", "Run", "internal/a/a.go")

	// Two Foo nodes in internal/b — ambiguous for import-guided resolution.
	if err := s.UpsertNode(Node{
		ID: "bbbb1011", Kind: NodeKindFunction, Name: "Foo",
		QualifiedName: "b1.Foo", FilePath: "internal/b/b1.go",
		Language: "go", StartLine: 1, EndLine: 5, UpdatedAt: time.Now().Unix(),
	}); err != nil {
		t.Fatalf("UpsertNode b1.Foo: %v", err)
	}
	if err := s.UpsertNode(Node{
		ID: "cccc1012", Kind: NodeKindFunction, Name: "Foo",
		QualifiedName: "b2.Foo", FilePath: "internal/b/b2.go",
		Language: "go", StartLine: 1, EndLine: 5, UpdatedAt: time.Now().Unix(),
	}); err != nil {
		t.Fatalf("UpsertNode b2.Foo: %v", err)
	}

	insertImportNode(t, s, "internal/a/a.go", "internal/b", "b")

	ref := UnresolvedRef{
		FromNodeID:    caller.ID,
		ReferenceName: "b.Foo",
		ReferenceKind: EdgeKindCalls,
		Line:          5,
		Col:           0,
		FilePath:      "internal/a/a.go",
		Language:      "go",
	}
	if err := s.UpsertUnresolvedRef(ref); err != nil {
		t.Fatalf("UpsertUnresolvedRef: %v", err)
	}

	r := NewResolver(s)
	if _, err := r.Resolve(""); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// The key invariant: no edge created with provenance="import" (ambiguous).
	allEdges, err := s.GetEdgesFrom(caller.ID, string(EdgeKindCalls))
	if err != nil {
		t.Fatalf("GetEdgesFrom: %v", err)
	}
	for _, e := range allEdges {
		if e.Provenance == "import" {
			t.Errorf("found edge with provenance=import despite ambiguous candidates (want none)")
		}
	}
}

// TestResolver_ImportGuidedGoAlias verifies AC6: an explicit import alias
// (e.g. `import p "path/pkg"`) is used as the qualifier in the reference name.
func TestResolver_ImportGuidedGoAlias(t *testing.T) {
	s := newTestStore(t)

	caller := insertNode(t, s, "aaaa1020", "Init", "Init", "cmd/main.go")
	callee := insertNode(t, s, "bbbb1021", "New", "New", "internal/store/store.go")

	// Alias import: import p "internal/store" → binding = "p".
	insertImportNode(t, s, "cmd/main.go", "internal/store", "p")

	ref := UnresolvedRef{
		FromNodeID:    caller.ID,
		ReferenceName: "p.New",
		ReferenceKind: EdgeKindCalls,
		Line:          10,
		Col:           0,
		FilePath:      "cmd/main.go",
		Language:      "go",
	}
	if err := s.UpsertUnresolvedRef(ref); err != nil {
		t.Fatalf("UpsertUnresolvedRef: %v", err)
	}

	r := NewResolver(s)
	result, err := r.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if result.Resolved != 1 {
		t.Errorf("Resolved = %d, want 1", result.Resolved)
	}

	edges, err := s.GetEdgesFrom(caller.ID, string(EdgeKindCalls))
	if err != nil {
		t.Fatalf("GetEdgesFrom: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("want 1 edge, got %d", len(edges))
	}
	if edges[0].Target != callee.ID {
		t.Errorf("edge target = %q, want %q", edges[0].Target, callee.ID)
	}
	if edges[0].Provenance != "import" {
		t.Errorf("provenance = %q, want \"import\"", edges[0].Provenance)
	}
}

// TestResolver_ImportGuidedTSNamespace verifies AC7a: a TS namespace import
// (`import * as ns from './m'`) resolves ns.foo() to the foo symbol in ./m.
func TestResolver_ImportGuidedTSNamespace(t *testing.T) {
	s := newTestStore(t)

	caller := insertNode(t, s, "aaaa1030", "handler", "handler", "src/routes/index.ts")
	callee := Node{
		ID:            NodeID("src/lib/utils.ts", "formatDate"),
		Kind:          NodeKindFunction,
		Name:          "formatDate",
		QualifiedName: "formatDate",
		FilePath:      "src/lib/utils.ts",
		Language:      "typescript",
		StartLine:     1,
		EndLine:       5,
		UpdatedAt:     time.Now().Unix(),
	}
	if err := s.UpsertNode(callee); err != nil {
		t.Fatalf("UpsertNode callee: %v", err)
	}

	// Namespace import node: import * as utils from '../lib/utils'
	// QualifiedName format: "import:<name>:<source>"
	// refFile is "src/routes/index.ts"; callee is in "src/lib/utils.ts"
	// so the relative path from src/routes/ to src/lib/ is "../lib/utils".
	importQN := "import:utils:../lib/utils"
	importNode := Node{
		ID:            NodeID("src/routes/index.ts", importQN),
		Kind:          NodeKindImport,
		Name:          "utils",
		QualifiedName: importQN,
		FilePath:      "src/routes/index.ts",
		Language:      "typescript",
		StartLine:     1,
		EndLine:       1,
		UpdatedAt:     time.Now().Unix(),
		ImportAlias:   "utils",
	}
	if err := s.UpsertNode(importNode); err != nil {
		t.Fatalf("UpsertNode import: %v", err)
	}

	ref := UnresolvedRef{
		FromNodeID:    caller.ID,
		ReferenceName: "utils.formatDate",
		ReferenceKind: EdgeKindCalls,
		Line:          8,
		Col:           2,
		FilePath:      "src/routes/index.ts",
		Language:      "typescript",
	}
	if err := s.UpsertUnresolvedRef(ref); err != nil {
		t.Fatalf("UpsertUnresolvedRef: %v", err)
	}

	r := NewResolver(s)
	result, err := r.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if result.Resolved != 1 {
		t.Errorf("Resolved = %d, want 1", result.Resolved)
	}

	edges, err := s.GetEdgesFrom(caller.ID, string(EdgeKindCalls))
	if err != nil {
		t.Fatalf("GetEdgesFrom: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("want 1 edge, got %d", len(edges))
	}
	if edges[0].Target != callee.ID {
		t.Errorf("edge target = %q, want %q", edges[0].Target, callee.ID)
	}
	if edges[0].Provenance != "import" {
		t.Errorf("provenance = %q, want \"import\"", edges[0].Provenance)
	}
}

// TestResolver_ImportGuidedTSBareDoesNotBreak verifies AC7b: a bare/npm import
// (e.g. `import x from 'react'`) does not cause an error and leaves the ref
// unresolved (no edge created, no panic).
func TestResolver_ImportGuidedTSBareDoesNotBreak(t *testing.T) {
	s := newTestStore(t)

	caller := insertNode(t, s, "aaaa1040", "App", "App", "src/app.tsx")

	// Bare import node: import React from 'react'
	importQN := "import:React:react"
	importNode := Node{
		ID:            NodeID("src/app.tsx", importQN),
		Kind:          NodeKindImport,
		Name:          "React",
		QualifiedName: importQN,
		FilePath:      "src/app.tsx",
		Language:      "typescript",
		StartLine:     1,
		EndLine:       1,
		UpdatedAt:     time.Now().Unix(),
		ImportAlias:   "React",
	}
	if err := s.UpsertNode(importNode); err != nil {
		t.Fatalf("UpsertNode import: %v", err)
	}

	ref := UnresolvedRef{
		FromNodeID:    caller.ID,
		ReferenceName: "React.useState",
		ReferenceKind: EdgeKindCalls,
		Line:          5,
		Col:           4,
		FilePath:      "src/app.tsx",
		Language:      "typescript",
	}
	if err := s.UpsertUnresolvedRef(ref); err != nil {
		t.Fatalf("UpsertUnresolvedRef: %v", err)
	}

	r := NewResolver(s)
	result, err := r.Resolve("")
	if err != nil {
		t.Fatalf("Resolve must not error on bare npm imports: %v", err)
	}

	// Bare import → external package → no node in repo → stays unresolved.
	if result.Resolved != 0 {
		t.Errorf("Resolved = %d, want 0 (bare import has no repo node)", result.Resolved)
	}
	if result.Unresolved != 1 {
		t.Errorf("Unresolved = %d, want 1", result.Unresolved)
	}

	edges, err := s.GetEdgesFrom(caller.ID, string(EdgeKindCalls))
	if err != nil {
		t.Fatalf("GetEdgesFrom: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("want 0 edges for bare import, got %d", len(edges))
	}
}

// TestResolver_ShortNameAmbiguousStaysUnresolved verifies that T4 (short-name)
// does NOT link when two or more nodes share the same name. The ref must
// remain unresolved (candidato-único-o-nada).
func TestResolver_ShortNameAmbiguousStaysUnresolved(t *testing.T) {
	s := newTestStore(t)

	// Two distinct nodes that share the short name "Close" — different packages.
	insertNode(t, s, "aa000001", "Close", "internal/db.Close", "internal/db/db.go")
	insertNode(t, s, "aa000002", "Close", "internal/codegraph.Close", "internal/codegraph/db.go")
	caller := insertNode(t, s, "aa000003", "writeMemory", "internal/vault.writeMemory", "internal/vault/writer.go")

	ref := sampleUnresolvedRef(caller.ID, "Close", EdgeKindCalls)
	if err := s.UpsertUnresolvedRef(ref); err != nil {
		t.Fatalf("UpsertUnresolvedRef: %v", err)
	}

	r := NewResolver(s)
	result, err := r.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if result.Resolved != 0 {
		t.Errorf("Resolved = %d, want 0 (ambiguous short name must not be linked)", result.Resolved)
	}
	if result.Unresolved != 1 {
		t.Errorf("Unresolved = %d, want 1", result.Unresolved)
	}

	edges, err := s.GetEdgesFrom(caller.ID, string(EdgeKindCalls))
	if err != nil {
		t.Fatalf("GetEdgesFrom: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("want 0 edges (ambiguous), got %d", len(edges))
	}
}

// TestResolver_ShortNameUniqueResolves verifies that T4 (short-name) DOES link
// when exactly one node has the given name (candidato-único).
func TestResolver_ShortNameUniqueResolves(t *testing.T) {
	s := newTestStore(t)

	callee := insertNode(t, s, "bb000001", "UniqueFunc", "internal/pkg.UniqueFunc", "internal/pkg/func.go")
	caller := insertNode(t, s, "bb000002", "callsite", "main.callsite", "cmd/main.go")

	ref := sampleUnresolvedRef(caller.ID, "UniqueFunc", EdgeKindCalls)
	if err := s.UpsertUnresolvedRef(ref); err != nil {
		t.Fatalf("UpsertUnresolvedRef: %v", err)
	}

	r := NewResolver(s)
	result, err := r.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if result.Resolved != 1 {
		t.Errorf("Resolved = %d, want 1", result.Resolved)
	}

	edges, err := s.GetEdgesFrom(caller.ID, string(EdgeKindCalls))
	if err != nil {
		t.Fatalf("GetEdgesFrom: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("want 1 edge, got %d", len(edges))
	}
	if edges[0].Target != callee.ID {
		t.Errorf("edge target = %q, want %q", edges[0].Target, callee.ID)
	}
}

// TestResolver_SuffixAmbiguousStaysUnresolved verifies that T3 (suffix match)
// does NOT link when two or more nodes share the same qualified_name suffix.
// The ref must remain unresolved (candidato-único-o-nada).
func TestResolver_SuffixAmbiguousStaysUnresolved(t *testing.T) {
	s := newTestStore(t)

	// Two nodes whose qualified_name both end in ".pkg.Valid" — different roots.
	insertNode(t, s, "cc000001", "Valid", "internal/a/pkg.Valid", "internal/a/pkg/v.go")
	insertNode(t, s, "cc000002", "Valid", "internal/b/pkg.Valid", "internal/b/pkg/v.go")
	caller := insertNode(t, s, "cc000003", "caller", "main.caller", "cmd/main.go")

	// The ref "pkg.Valid" suffix-matches both nodes.
	ref := sampleUnresolvedRef(caller.ID, "pkg.Valid", EdgeKindCalls)
	if err := s.UpsertUnresolvedRef(ref); err != nil {
		t.Fatalf("UpsertUnresolvedRef: %v", err)
	}

	r := NewResolver(s)
	result, err := r.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if result.Resolved != 0 {
		t.Errorf("Resolved = %d, want 0 (ambiguous suffix must not be linked)", result.Resolved)
	}
	if result.Unresolved != 1 {
		t.Errorf("Unresolved = %d, want 1", result.Unresolved)
	}

	edges, err := s.GetEdgesFrom(caller.ID, string(EdgeKindCalls))
	if err != nil {
		t.Fatalf("GetEdgesFrom: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("want 0 edges (ambiguous suffix), got %d", len(edges))
	}
}

// TestResolver_SuffixUniqueResolves verifies that T3 (suffix match) DOES link
// when exactly one node has the given qualified_name suffix (candidato-único).
func TestResolver_SuffixUniqueResolves(t *testing.T) {
	s := newTestStore(t)

	callee := insertNode(t, s, "dd000001", "Create", "internal/store.Create", "internal/store/store.go")
	caller := insertNode(t, s, "dd000002", "handler", "http.handler", "internal/http/handler.go")

	ref := sampleUnresolvedRef(caller.ID, "store.Create", EdgeKindCalls)
	if err := s.UpsertUnresolvedRef(ref); err != nil {
		t.Fatalf("UpsertUnresolvedRef: %v", err)
	}

	r := NewResolver(s)
	result, err := r.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if result.Resolved != 1 {
		t.Errorf("Resolved = %d, want 1", result.Resolved)
	}

	edges, err := s.GetEdgesFrom(caller.ID, string(EdgeKindCalls))
	if err != nil {
		t.Fatalf("GetEdgesFrom: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("want 1 edge, got %d", len(edges))
	}
	if edges[0].Target != callee.ID {
		t.Errorf("edge target = %q, want %q", edges[0].Target, callee.ID)
	}
}
