package codegraph

import (
	"testing"
	"time"
)

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
	result, err := r.Resolve()
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
	result, err := r.Resolve()
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
	result, err := r.Resolve()
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
	result, err := r.Resolve()
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
	result, err := r.Resolve()
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
	result1, err := r.Resolve()
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
	result2, err := r.Resolve()
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
