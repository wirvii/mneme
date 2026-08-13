package service

import (
	"testing"

	"github.com/wirvii/mneme/internal/codegraph"
	"github.com/wirvii/mneme/internal/quality"
)

// newTestGraphStore opens an in-memory code graph database (R-C: never the
// real, host-level graph) and returns its Store, with cleanup registered on
// t.
func newTestGraphStore(t *testing.T) *codegraph.Store {
	t.Helper()
	cdb, err := codegraph.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB(:memory:): %v", err)
	}
	t.Cleanup(func() { _ = cdb.Close() })
	return codegraph.NewStore(cdb)
}

// TestGraphFactsAdapter_IncomingEdgesAndCalls seeds a tiny real graph (two
// callers, one of each kind) and verifies IncomingEdges/IncomingCalls
// resolve by (file, qualified name) and filter by edge kind correctly.
func TestGraphFactsAdapter_IncomingEdgesAndCalls(t *testing.T) {
	store := newTestGraphStore(t)
	adapter := newGraphFactsAdapter(store)

	target := codegraph.Node{
		ID: codegraph.NodeID("a.go", "Target"), Kind: codegraph.NodeKindFunction,
		Name: "Target", QualifiedName: "Target", FilePath: "a.go", Language: "go",
	}
	caller := codegraph.Node{
		ID: codegraph.NodeID("b.go", "Caller"), Kind: codegraph.NodeKindFunction,
		Name: "Caller", QualifiedName: "Caller", FilePath: "b.go", Language: "go",
	}
	referencer := codegraph.Node{
		ID: codegraph.NodeID("c.go", "Referencer"), Kind: codegraph.NodeKindFunction,
		Name: "Referencer", QualifiedName: "Referencer", FilePath: "c.go", Language: "go",
	}
	for _, n := range []codegraph.Node{target, caller, referencer} {
		if err := store.UpsertNode(n); err != nil {
			t.Fatalf("UpsertNode(%s): %v", n.Name, err)
		}
	}
	if err := store.UpsertEdge(codegraph.Edge{Source: caller.ID, Target: target.ID, Kind: codegraph.EdgeKindCalls}); err != nil {
		t.Fatalf("UpsertEdge(calls): %v", err)
	}
	if err := store.UpsertEdge(codegraph.Edge{Source: referencer.ID, Target: target.ID, Kind: codegraph.EdgeKindReferences}); err != nil {
		t.Fatalf("UpsertEdge(references): %v", err)
	}

	ref := quality.SymbolRef{QualifiedName: "Target", File: "a.go"}

	allEdges, err := adapter.IncomingEdges(ref)
	if err != nil {
		t.Fatalf("IncomingEdges: %v", err)
	}
	if len(allEdges) != 2 {
		t.Errorf("IncomingEdges = %+v, want 2 (calls + references)", allEdges)
	}

	calls, err := adapter.IncomingCalls(ref)
	if err != nil {
		t.Fatalf("IncomingCalls: %v", err)
	}
	if len(calls) != 1 || calls[0].QualifiedName != "Caller" {
		t.Errorf("IncomingCalls = %+v, want exactly [Caller]", calls)
	}

	// Positive hermana: a symbol with NO incoming edges reports empty, not
	// an error.
	orphanRef := quality.SymbolRef{QualifiedName: "NoSuchTarget", File: "z.go"}
	none, err := adapter.IncomingEdges(orphanRef)
	if err != nil {
		t.Fatalf("IncomingEdges(orphan): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("IncomingEdges(orphan) = %+v, want empty", none)
	}
}

// TestGraphFactsAdapter_TestReachable seeds a two-hop call chain from a
// test file to a target symbol and verifies TestReachable respects depth.
func TestGraphFactsAdapter_TestReachable(t *testing.T) {
	store := newTestGraphStore(t)
	adapter := newGraphFactsAdapter(store)

	target := codegraph.Node{ID: codegraph.NodeID("a.go", "Target"), Kind: codegraph.NodeKindFunction, Name: "Target", QualifiedName: "Target", FilePath: "a.go"}
	middle := codegraph.Node{ID: codegraph.NodeID("b.go", "Middle"), Kind: codegraph.NodeKindFunction, Name: "Middle", QualifiedName: "Middle", FilePath: "b.go"}
	testCaller := codegraph.Node{ID: codegraph.NodeID("a_test.go", "TestIt"), Kind: codegraph.NodeKindFunction, Name: "TestIt", QualifiedName: "TestIt", FilePath: "a_test.go"}
	for _, n := range []codegraph.Node{target, middle, testCaller} {
		if err := store.UpsertNode(n); err != nil {
			t.Fatalf("UpsertNode: %v", err)
		}
	}
	if err := store.UpsertEdge(codegraph.Edge{Source: middle.ID, Target: target.ID, Kind: codegraph.EdgeKindCalls}); err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}
	if err := store.UpsertEdge(codegraph.Edge{Source: testCaller.ID, Target: middle.ID, Kind: codegraph.EdgeKindCalls}); err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}

	ref := quality.SymbolRef{QualifiedName: "Target", File: "a.go"}
	testGlobs := []string{"**/*_test.go"}

	reachableAt3, err := adapter.TestReachable(ref, 3, testGlobs)
	if err != nil {
		t.Fatalf("TestReachable(depth=3): %v", err)
	}
	if !reachableAt3 {
		t.Error("TestReachable(depth=3) = false, want true (two hops away)")
	}

	reachableAt1, err := adapter.TestReachable(ref, 1, testGlobs)
	if err != nil {
		t.Fatalf("TestReachable(depth=1): %v", err)
	}
	if reachableAt1 {
		t.Error("TestReachable(depth=1) = true, want false (the test caller is two hops away)")
	}
}

// TestGraphFactsAdapter_SameNameAndSignature verifies the reinvention
// detection's raw material: a preexisting node sharing name+signature is
// returned; the SUBJECT symbol's own node (same file+qualified name) is
// excluded from its own candidate list.
func TestGraphFactsAdapter_SameNameAndSignature(t *testing.T) {
	store := newTestGraphStore(t)
	adapter := newGraphFactsAdapter(store)

	existing := codegraph.Node{
		ID: codegraph.NodeID("internal/other/parse.go", "Parse"), Kind: codegraph.NodeKindFunction,
		Name: "Parse", QualifiedName: "Parse", FilePath: "internal/other/parse.go", Signature: "func([]byte) error",
	}
	if err := store.UpsertNode(existing); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}

	subject := quality.Symbol{Name: "Parse", QualifiedName: "Parse", File: "internal/x/parse.go", Signature: "func([]byte) error"}
	candidates, err := adapter.SameNameAndSignature(subject)
	if err != nil {
		t.Fatalf("SameNameAndSignature: %v", err)
	}
	if len(candidates) != 1 || candidates[0].File != "internal/other/parse.go" {
		t.Errorf("candidates = %+v, want exactly [internal/other/parse.go]", candidates)
	}
}

// TestGraphFactsAdapter_IndexedContentHash verifies the D5 freshness
// primitive: a file's recorded ContentHash comes back verbatim, and an
// unindexed path reports indexed=false, never an error.
func TestGraphFactsAdapter_IndexedContentHash(t *testing.T) {
	store := newTestGraphStore(t)
	adapter := newGraphFactsAdapter(store)

	if err := store.UpsertFile(codegraph.FileRecord{Path: "a.go", ContentHash: "deadbeef", Language: "go"}); err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}

	hash, indexed, err := adapter.IndexedContentHash("a.go")
	if err != nil {
		t.Fatalf("IndexedContentHash: %v", err)
	}
	if !indexed || hash != "deadbeef" {
		t.Errorf("IndexedContentHash(a.go) = (%q, %v), want (deadbeef, true)", hash, indexed)
	}

	_, indexed2, err := adapter.IndexedContentHash("never-indexed.go")
	if err != nil {
		t.Fatalf("IndexedContentHash(never-indexed.go): %v", err)
	}
	if indexed2 {
		t.Error("IndexedContentHash(never-indexed.go) indexed = true, want false")
	}
}
