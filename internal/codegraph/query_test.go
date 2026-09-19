package codegraph

import (
	"encoding/json"
	"sort"
	"testing"
	"time"
)

// buildTestGraph creates the following graph in an in-memory DB and returns
// a QueryEngine and Store backed by it.
//
//	file:a.go
//	  └─ contains → func:A
//	  └─ contains → func:B
//	file:b.go
//	  └─ contains → func:C
//	  └─ contains → func:D
//	func:A
//	  └─ calls → func:B
//	  └─ calls → func:C
//	func:C
//	  └─ calls → func:D
func buildTestGraph(t *testing.T) (*QueryEngine, *Store) {
	t.Helper()
	cdb, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { cdb.Close() })
	s := NewStore(cdb)

	now := time.Now().UnixMilli()
	nodes := []Node{
		{ID: "file_a", Kind: NodeKindFile, Name: "a.go", QualifiedName: "a.go", FilePath: "a.go", Language: "go", StartLine: 1, EndLine: 50, UpdatedAt: now},
		{ID: "func_a", Kind: NodeKindFunction, Name: "A", QualifiedName: "pkg.A", FilePath: "a.go", Language: "go", StartLine: 3, EndLine: 10, IsExported: true, UpdatedAt: now},
		{ID: "func_b", Kind: NodeKindFunction, Name: "B", QualifiedName: "pkg.B", FilePath: "a.go", Language: "go", StartLine: 12, EndLine: 20, IsExported: true, UpdatedAt: now},
		{ID: "file_b", Kind: NodeKindFile, Name: "b.go", QualifiedName: "b.go", FilePath: "b.go", Language: "go", StartLine: 1, EndLine: 50, UpdatedAt: now},
		{ID: "func_c", Kind: NodeKindFunction, Name: "C", QualifiedName: "pkg.C", FilePath: "b.go", Language: "go", StartLine: 3, EndLine: 10, IsExported: true, UpdatedAt: now},
		{ID: "func_d", Kind: NodeKindFunction, Name: "D", QualifiedName: "pkg.D", FilePath: "b.go", Language: "go", StartLine: 12, EndLine: 20, IsExported: true, UpdatedAt: now},
	}
	for _, n := range nodes {
		if err := s.UpsertNode(n); err != nil {
			t.Fatal(err)
		}
	}
	edges := []Edge{
		{Source: "file_a", Target: "func_a", Kind: EdgeKindContains},
		{Source: "file_a", Target: "func_b", Kind: EdgeKindContains},
		{Source: "file_b", Target: "func_c", Kind: EdgeKindContains},
		{Source: "file_b", Target: "func_d", Kind: EdgeKindContains},
		{Source: "func_a", Target: "func_b", Kind: EdgeKindCalls, Line: 5},
		{Source: "func_a", Target: "func_c", Kind: EdgeKindCalls, Line: 7},
		{Source: "func_c", Target: "func_d", Kind: EdgeKindCalls, Line: 5},
	}
	for _, e := range edges {
		if err := s.UpsertEdge(e); err != nil {
			t.Fatal(err)
		}
	}
	return NewQueryEngine(s), s
}

// nodeIDs extracts and sorts the IDs from a slice of nodes for deterministic
// assertion.
func nodeIDs(nodes []Node) []string {
	ids := make([]string, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}
	sort.Strings(ids)
	return ids
}

// TestQuery_Callers verifies that callers of func_b at depth 1 returns func_a.
func TestQuery_Callers(t *testing.T) {
	q, _ := buildTestGraph(t)
	got, err := q.Callers("func_b", 1, 0)
	if err != nil {
		t.Fatalf("Callers: %v", err)
	}
	want := []string{"func_a"}
	if got, want := nodeIDs(got), want; !equalStringSlices(got, want) {
		t.Errorf("Callers(func_b, 1) = %v, want %v", got, want)
	}
}

// TestQuery_Callers_OfC verifies that callers of func_c at depth 1 returns func_a.
func TestQuery_Callers_OfC(t *testing.T) {
	q, _ := buildTestGraph(t)
	got, err := q.Callers("func_c", 1, 0)
	if err != nil {
		t.Fatalf("Callers: %v", err)
	}
	want := []string{"func_a"}
	if got, want := nodeIDs(got), want; !equalStringSlices(got, want) {
		t.Errorf("Callers(func_c, 1) = %v, want %v", got, want)
	}
}

// TestQuery_Callees verifies that callees of func_a returns func_b and func_c.
func TestQuery_Callees(t *testing.T) {
	q, _ := buildTestGraph(t)
	got, err := q.Callees("func_a", 1, 0)
	if err != nil {
		t.Fatalf("Callees: %v", err)
	}
	want := []string{"func_b", "func_c"}
	if got := nodeIDs(got); !equalStringSlices(got, want) {
		t.Errorf("Callees(func_a, 1) = %v, want %v", got, want)
	}
}

// TestQuery_CallersDepth2 verifies that callers of func_d at depth 2 returns
// both func_c (direct) and func_a (transitive).
func TestQuery_CallersDepth2(t *testing.T) {
	q, _ := buildTestGraph(t)
	got, err := q.Callers("func_d", 2, 0)
	if err != nil {
		t.Fatalf("Callers: %v", err)
	}
	want := []string{"func_a", "func_c"}
	if got := nodeIDs(got); !equalStringSlices(got, want) {
		t.Errorf("Callers(func_d, 2) = %v, want %v", got, want)
	}
}

// TestQuery_Impact verifies that the blast radius of func_d includes func_c
// and func_a (transitive callers).
func TestQuery_Impact(t *testing.T) {
	q, _ := buildTestGraph(t)
	// default depth=0 -> uses defaultDepth (10), default limit=0 -> 50
	got, err := q.Impact("func_d", 0, 0)
	if err != nil {
		t.Fatalf("Impact: %v", err)
	}
	want := []string{"func_a", "func_c"}
	if got := nodeIDs(got); !equalStringSlices(got, want) {
		t.Errorf("Impact(func_d) = %v, want %v", got, want)
	}
}

// TestCodegraphAffected_ReverseTraversal catches regressions that drop one of
// the three declared incoming edge kinds, revisit a cycle, seed only the first
// changed file, or apply the result limit before deterministic ordering.
func TestCodegraphAffected_ReverseTraversal(t *testing.T) {
	cdb, err := OpenDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cdb.Close() })
	store := NewStore(cdb)

	nodes := []Node{
		{ID: "file_a", Kind: NodeKindFile, Name: "a.go", QualifiedName: "a.go", FilePath: "a.go", Language: "go"},
		{ID: "seed_a", Kind: NodeKindFunction, Name: "A", QualifiedName: "pkg.A", FilePath: "a.go", Language: "go"},
		{ID: "file_b", Kind: NodeKindFile, Name: "b.go", QualifiedName: "b.go", FilePath: "b.go", Language: "go"},
		{ID: "seed_b", Kind: NodeKindFunction, Name: "B", QualifiedName: "pkg.B", FilePath: "b.go", Language: "go"},
		{ID: "caller", Kind: NodeKindFunction, Name: "Caller", QualifiedName: "pkg.Caller", FilePath: "consumer.go", Language: "go"},
		{ID: "deep", Kind: NodeKindFunction, Name: "Deep", QualifiedName: "pkg.Deep", FilePath: "deep.go", Language: "go"},
		{ID: "importer", Kind: NodeKindFile, Name: "importer.go", QualifiedName: "importer.go", FilePath: "importer.go", Language: "go"},
	}
	for _, node := range nodes {
		if err := store.UpsertNode(node); err != nil {
			t.Fatal(err)
		}
	}
	edges := []Edge{
		{Source: "file_a", Target: "seed_a", Kind: EdgeKindContains},
		{Source: "file_b", Target: "seed_b", Kind: EdgeKindContains},
		{Source: "caller", Target: "seed_a", Kind: EdgeKindCalls},
		{Source: "importer", Target: "seed_b", Kind: EdgeKindImports},
		{Source: "deep", Target: "caller", Kind: EdgeKindCalls},
		{Source: "seed_a", Target: "deep", Kind: EdgeKindCalls},
	}
	for _, edge := range edges {
		if err := store.UpsertEdge(edge); err != nil {
			t.Fatal(err)
		}
	}

	result, err := NewQueryEngine(store).Affected([]string{"b.go", "a.go"}, 3, 3)
	if err != nil {
		t.Fatalf("Affected: %v", err)
	}
	if got, want := result.Inputs, []string{"a.go", "b.go"}; !equalStringSlices(got, want) {
		t.Fatalf("inputs = %v, want %v", got, want)
	}
	if result.Total != 5 || !result.Truncated {
		t.Fatalf("total/truncated = %d/%v, want 5/true", result.Total, result.Truncated)
	}
	want := []AffectedNode{
		{ID: "file_a", Kind: NodeKindFile, Name: "a.go", QualifiedName: "a.go", FilePath: "a.go", Language: "go", Relation: EdgeKindContains, Depth: 1},
		{ID: "file_b", Kind: NodeKindFile, Name: "b.go", QualifiedName: "b.go", FilePath: "b.go", Language: "go", Relation: EdgeKindContains, Depth: 1},
		{ID: "caller", Kind: NodeKindFunction, Name: "Caller", QualifiedName: "pkg.Caller", FilePath: "consumer.go", Language: "go", Relation: EdgeKindCalls, Depth: 1},
	}
	if gotJSON, wantJSON := mustJSON(t, result.AffectedNodes), mustJSON(t, want); gotJSON != wantJSON {
		t.Errorf("affected nodes = %s, want %s", gotJSON, wantJSON)
	}
}

func TestAffectedContracts_JSON(t *testing.T) {
	request := AffectedRequest{Paths: []string{"b.go", "a.go"}, Base: "base", Head: "head", Depth: 2, Limit: 1}
	if got, want := mustJSON(t, request), `{"paths":["b.go","a.go"],"base":"base","head":"head","depth":2,"limit":1}`; got != want {
		t.Errorf("request JSON = %s, want %s", got, want)
	}

	result := AffectedResult{
		Inputs:         []string{"a.go", "b.go"},
		MissingPaths:   []string{"gone.go"},
		UntrackedPaths: []string{},
		AffectedNodes: []AffectedNode{{
			ID: "caller", Kind: NodeKindFunction, Name: "Caller", QualifiedName: "pkg.Caller",
			FilePath: "caller.go", Language: "go", Relation: EdgeKindCalls, Depth: 1,
		}},
		Total: 1, Truncated: false, Stale: true, MissingGraph: false, IndexedSHA: "old",
	}
	const want = `{"inputs":["a.go","b.go"],"missing_paths":["gone.go"],"untracked_paths":[],"affected_nodes":[{"id":"caller","kind":"function","name":"Caller","qualified_name":"pkg.Caller","file_path":"caller.go","language":"go","relation":"calls","depth":1}],"total":1,"truncated":false,"stale":true,"missing_graph":false,"indexed_sha":"old"}`
	if got := mustJSON(t, result); got != want {
		t.Errorf("result JSON = %s, want %s", got, want)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestQuery_Trace verifies that the path from func_a to func_d is
// [func_a, func_c, func_d] (shortest via calls edges).
func TestQuery_Trace(t *testing.T) {
	q, _ := buildTestGraph(t)
	nodes, edges, err := q.Trace("func_a", "func_d", 10)
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}
	wantIDs := []string{"func_a", "func_c", "func_d"}
	gotIDs := make([]string, len(nodes))
	for i, n := range nodes {
		gotIDs[i] = n.ID
	}
	if !equalStringSlices(gotIDs, wantIDs) {
		t.Errorf("Trace nodes = %v, want %v", gotIDs, wantIDs)
	}
	// edges count should be len(nodes)-1
	if len(edges) != len(nodes)-1 {
		t.Errorf("Trace edges count = %d, want %d", len(edges), len(nodes)-1)
	}
}

// TestQuery_Trace_Direct verifies that the direct path from func_a to func_b
// is just [func_a, func_b].
func TestQuery_Trace_Direct(t *testing.T) {
	q, _ := buildTestGraph(t)
	nodes, edges, err := q.Trace("func_a", "func_b", 10)
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}
	wantIDs := []string{"func_a", "func_b"}
	gotIDs := make([]string, len(nodes))
	for i, n := range nodes {
		gotIDs[i] = n.ID
	}
	if !equalStringSlices(gotIDs, wantIDs) {
		t.Errorf("Trace nodes = %v, want %v", gotIDs, wantIDs)
	}
	if len(edges) != 1 {
		t.Errorf("Trace edges count = %d, want 1", len(edges))
	}
}

// TestQuery_TraceNoPath verifies that tracing from func_d to func_a returns
// empty slices (no outgoing calls path exists).
func TestQuery_TraceNoPath(t *testing.T) {
	q, _ := buildTestGraph(t)
	nodes, edges, err := q.Trace("func_d", "func_a", 10)
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}
	if len(nodes) != 0 || len(edges) != 0 {
		t.Errorf("Trace(func_d->func_a) = %v nodes, %v edges; want empty", len(nodes), len(edges))
	}
}

// TestQuery_Limit verifies that passing limit=1 caps results at one node.
func TestQuery_Limit(t *testing.T) {
	q, _ := buildTestGraph(t)
	got, err := q.Callees("func_a", 1, 1)
	if err != nil {
		t.Fatalf("Callees: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("Callees with limit=1 returned %d nodes, want 1", len(got))
	}
}

// TestQuery_NoCycles verifies that adding a cycle edge (func_d → func_a)
// does not cause an infinite loop in BFS traversal.
func TestQuery_NoCycles(t *testing.T) {
	q, s := buildTestGraph(t)
	// Add a back-edge creating a cycle: D → A
	if err := s.UpsertEdge(Edge{Source: "func_d", Target: "func_a", Kind: EdgeKindCalls}); err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}
	// Callers of func_a would now include func_d transitively — but more importantly
	// it must terminate and not loop forever.
	got, err := q.Callers("func_a", 10, 0)
	if err != nil {
		t.Fatalf("Callers with cycle: %v", err)
	}
	// func_d and func_c are now callers of func_a (func_d directly, func_c via func_d→func_a is not a caller of func_a)
	// The exact set is less important than termination, but we sanity-check it includes func_d.
	ids := nodeIDs(got)
	found := false
	for _, id := range ids {
		if id == "func_d" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Callers with cycle: expected func_d in results, got %v", ids)
	}
}

// equalStringSlices returns true when two sorted string slices are identical.
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
