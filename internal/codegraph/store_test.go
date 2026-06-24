package codegraph

import (
	"fmt"
	"testing"
	"time"
)

// newTestStore opens an in-memory CodeGraphDB and returns a Store wrapping it.
// The database is closed automatically when the test ends.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	cdb, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { cdb.Close() })
	return NewStore(cdb)
}

// sampleNode returns a minimal valid Node with the given id, kind, name and
// file path. The caller may override individual fields before using it.
func sampleNode(id, kind, name, filePath string) Node {
	return Node{
		ID:            id,
		Kind:          NodeKind(kind),
		Name:          name,
		QualifiedName: filePath + "." + name,
		FilePath:      filePath,
		Language:      "go",
		StartLine:     1,
		EndLine:       10,
		StartColumn:   0,
		EndColumn:     0,
		UpdatedAt:     time.Now().Unix(),
	}
}

// sampleEdge returns a minimal Edge from source to target with the given kind.
func sampleEdge(source, target string, kind EdgeKind) Edge {
	return Edge{
		Source:     source,
		Target:     target,
		Kind:       kind,
		Provenance: "test",
	}
}

// TestStore_UpsertNode verifies that UpsertNode persists a node and GetNode
// retrieves it with all fields intact.
func TestStore_UpsertNode(t *testing.T) {
	s := newTestStore(t)

	n := sampleNode("aabbccddeeff0011", "function", "Search", "internal/store/search.go")
	n.Docstring = "Search finds memories."
	n.Signature = "func Search(ctx context.Context) []Result"
	n.IsExported = true
	n.Decorators = []string{"@deprecated"}
	n.TypeParameters = []string{"T"}

	if err := s.UpsertNode(n); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}

	got, err := s.GetNode(n.ID)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if got == nil {
		t.Fatal("GetNode returned nil; want node")
	}

	if got.ID != n.ID {
		t.Errorf("ID: got %q; want %q", got.ID, n.ID)
	}
	if got.Name != n.Name {
		t.Errorf("Name: got %q; want %q", got.Name, n.Name)
	}
	if got.Kind != n.Kind {
		t.Errorf("Kind: got %q; want %q", got.Kind, n.Kind)
	}
	if got.FilePath != n.FilePath {
		t.Errorf("FilePath: got %q; want %q", got.FilePath, n.FilePath)
	}
	if got.Language != n.Language {
		t.Errorf("Language: got %q; want %q", got.Language, n.Language)
	}
	if got.Docstring != n.Docstring {
		t.Errorf("Docstring: got %q; want %q", got.Docstring, n.Docstring)
	}
	if got.Signature != n.Signature {
		t.Errorf("Signature: got %q; want %q", got.Signature, n.Signature)
	}
	if got.IsExported != n.IsExported {
		t.Errorf("IsExported: got %v; want %v", got.IsExported, n.IsExported)
	}
	if len(got.Decorators) != 1 || got.Decorators[0] != "@deprecated" {
		t.Errorf("Decorators: got %v; want [@deprecated]", got.Decorators)
	}
	if len(got.TypeParameters) != 1 || got.TypeParameters[0] != "T" {
		t.Errorf("TypeParameters: got %v; want [T]", got.TypeParameters)
	}
}

// TestStore_UpsertNode_Replace verifies that upserting a node with the same ID
// replaces the stored record (INSERT OR REPLACE semantics).
func TestStore_UpsertNode_Replace(t *testing.T) {
	s := newTestStore(t)

	n := sampleNode("aabbccddeeff0011", "function", "OldName", "pkg/foo.go")
	if err := s.UpsertNode(n); err != nil {
		t.Fatalf("first UpsertNode: %v", err)
	}

	n.Name = "NewName"
	n.Docstring = "Updated docstring."
	if err := s.UpsertNode(n); err != nil {
		t.Fatalf("second UpsertNode: %v", err)
	}

	got, err := s.GetNode(n.ID)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if got.Name != "NewName" {
		t.Errorf("Name after replace: got %q; want %q", got.Name, "NewName")
	}
	if got.Docstring != "Updated docstring." {
		t.Errorf("Docstring after replace: got %q; want %q", got.Docstring, "Updated docstring.")
	}
}

// TestStore_GetNode_NotFound verifies that GetNode returns nil, nil when the node
// does not exist.
func TestStore_GetNode_NotFound(t *testing.T) {
	s := newTestStore(t)

	got, err := s.GetNode("doesnotexist")
	if err != nil {
		t.Fatalf("GetNode: unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("GetNode: got %+v; want nil", got)
	}
}

// TestStore_UpsertEdge verifies that UpsertEdge persists an edge and
// GetEdgesFrom retrieves it.
func TestStore_UpsertEdge(t *testing.T) {
	s := newTestStore(t)

	src := sampleNode("src0000000000001", "function", "Caller", "pkg/a.go")
	dst := sampleNode("dst0000000000001", "function", "Callee", "pkg/b.go")
	if err := s.UpsertNode(src); err != nil {
		t.Fatalf("UpsertNode src: %v", err)
	}
	if err := s.UpsertNode(dst); err != nil {
		t.Fatalf("UpsertNode dst: %v", err)
	}

	e := sampleEdge(src.ID, dst.ID, EdgeKindCalls)
	e.Line = 42
	if err := s.UpsertEdge(e); err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}

	edges, err := s.GetEdgesFrom(src.ID, "")
	if err != nil {
		t.Fatalf("GetEdgesFrom: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("GetEdgesFrom: got %d edges; want 1", len(edges))
	}
	if edges[0].Source != src.ID {
		t.Errorf("Source: got %q; want %q", edges[0].Source, src.ID)
	}
	if edges[0].Target != dst.ID {
		t.Errorf("Target: got %q; want %q", edges[0].Target, dst.ID)
	}
	if edges[0].Kind != EdgeKindCalls {
		t.Errorf("Kind: got %q; want %q", edges[0].Kind, EdgeKindCalls)
	}
	if edges[0].Line != 42 {
		t.Errorf("Line: got %d; want 42", edges[0].Line)
	}
}

// TestStore_GetEdgesFrom_KindFilter verifies that passing a non-empty kind to
// GetEdgesFrom filters results to only edges of that kind.
func TestStore_GetEdgesFrom_KindFilter(t *testing.T) {
	s := newTestStore(t)

	src := sampleNode("src0000000000002", "file", "a.go", "pkg/a.go")
	dst1 := sampleNode("dst0000000000002", "function", "Fn1", "pkg/b.go")
	dst2 := sampleNode("dst0000000000003", "function", "Fn2", "pkg/c.go")
	for _, n := range []Node{src, dst1, dst2} {
		if err := s.UpsertNode(n); err != nil {
			t.Fatalf("UpsertNode %s: %v", n.ID, err)
		}
	}

	if err := s.UpsertEdge(sampleEdge(src.ID, dst1.ID, EdgeKindContains)); err != nil {
		t.Fatalf("UpsertEdge contains: %v", err)
	}
	if err := s.UpsertEdge(sampleEdge(src.ID, dst2.ID, EdgeKindCalls)); err != nil {
		t.Fatalf("UpsertEdge calls: %v", err)
	}

	// Filter by "contains" — should return exactly 1 edge.
	edges, err := s.GetEdgesFrom(src.ID, string(EdgeKindContains))
	if err != nil {
		t.Fatalf("GetEdgesFrom with kind: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("GetEdgesFrom(contains): got %d; want 1", len(edges))
	}
	if edges[0].Kind != EdgeKindContains {
		t.Errorf("Kind: got %q; want %q", edges[0].Kind, EdgeKindContains)
	}
}

// TestStore_GetEdgesTo verifies that GetEdgesTo returns edges whose target
// matches the given nodeID.
func TestStore_GetEdgesTo(t *testing.T) {
	s := newTestStore(t)

	src1 := sampleNode("src0000000000010", "function", "A", "pkg/a.go")
	src2 := sampleNode("src0000000000011", "function", "B", "pkg/b.go")
	dst := sampleNode("dst0000000000010", "function", "Target", "pkg/t.go")
	for _, n := range []Node{src1, src2, dst} {
		if err := s.UpsertNode(n); err != nil {
			t.Fatalf("UpsertNode %s: %v", n.ID, err)
		}
	}

	if err := s.UpsertEdge(sampleEdge(src1.ID, dst.ID, EdgeKindCalls)); err != nil {
		t.Fatalf("UpsertEdge A->T: %v", err)
	}
	if err := s.UpsertEdge(sampleEdge(src2.ID, dst.ID, EdgeKindCalls)); err != nil {
		t.Fatalf("UpsertEdge B->T: %v", err)
	}

	edges, err := s.GetEdgesTo(dst.ID, "")
	if err != nil {
		t.Fatalf("GetEdgesTo: %v", err)
	}
	if len(edges) != 2 {
		t.Fatalf("GetEdgesTo: got %d edges; want 2", len(edges))
	}
	for _, e := range edges {
		if e.Target != dst.ID {
			t.Errorf("Target: got %q; want %q", e.Target, dst.ID)
		}
	}
}

// TestStore_SearchNodes_FTS5 inserts three nodes and verifies that a FTS5
// query matching the word "Memory" returns at least two results.
func TestStore_SearchNodes_FTS5(t *testing.T) {
	s := newTestStore(t)

	nodes := []Node{
		{
			ID:            "fts00000000000001",
			Kind:          NodeKindFunction,
			Name:          "SaveMemory",
			QualifiedName: "store.SaveMemory",
			FilePath:      "internal/store/memory.go",
			Language:      "go",
			StartLine:     1, EndLine: 10,
			Docstring: "SaveMemory persists a new memory to the store.",
			UpdatedAt: time.Now().Unix(),
		},
		{
			ID:            "fts00000000000002",
			Kind:          NodeKindFunction,
			Name:          "GetMemory",
			QualifiedName: "store.GetMemory",
			FilePath:      "internal/store/memory.go",
			Language:      "go",
			StartLine:     12, EndLine: 20,
			Docstring: "GetMemory retrieves a memory by ID.",
			UpdatedAt: time.Now().Unix(),
		},
		{
			ID:            "fts00000000000003",
			Kind:          NodeKindFunction,
			Name:          "OpenDB",
			QualifiedName: "db.OpenDB",
			FilePath:      "internal/db/db.go",
			Language:      "go",
			StartLine:     1, EndLine: 30,
			Docstring: "OpenDB opens the SQLite database.",
			UpdatedAt: time.Now().Unix(),
		},
	}

	for _, n := range nodes {
		if err := s.UpsertNode(n); err != nil {
			t.Fatalf("UpsertNode %s: %v", n.ID, err)
		}
	}

	results, err := s.SearchNodes("Memory", nil, nil, 10)
	if err != nil {
		t.Fatalf("SearchNodes: %v", err)
	}
	if len(results) < 2 {
		t.Errorf("SearchNodes('Memory'): got %d results; want >= 2", len(results))
	}
}

// TestStore_SearchNodes_KindFilter verifies that SearchNodes with a non-nil kinds
// slice restricts results to nodes of those kinds.
func TestStore_SearchNodes_KindFilter(t *testing.T) {
	s := newTestStore(t)

	nodes := []Node{
		{
			ID:            "kflt0000000000001",
			Kind:          NodeKindFunction,
			Name:          "DoSomething",
			QualifiedName: "pkg.DoSomething",
			FilePath:      "pkg/a.go",
			Language:      "go",
			StartLine:     1, EndLine: 5,
			UpdatedAt: time.Now().Unix(),
		},
		{
			ID:            "kflt0000000000002",
			Kind:          NodeKindStruct,
			Name:          "DoSomethingRequest",
			QualifiedName: "pkg.DoSomethingRequest",
			FilePath:      "pkg/a.go",
			Language:      "go",
			StartLine:     7, EndLine: 12,
			UpdatedAt: time.Now().Unix(),
		},
	}
	for _, n := range nodes {
		if err := s.UpsertNode(n); err != nil {
			t.Fatalf("UpsertNode %s: %v", n.ID, err)
		}
	}

	results, err := s.SearchNodes("DoSomething", []NodeKind{NodeKindFunction}, nil, 10)
	if err != nil {
		t.Fatalf("SearchNodes with kind filter: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("SearchNodes(kind=function): got %d; want 1", len(results))
	}
	if results[0].Kind != NodeKindFunction {
		t.Errorf("Kind: got %q; want function", results[0].Kind)
	}
}

// TestStore_DeleteNodesByFile verifies that DeleteNodesByFile removes all nodes
// whose file_path matches and returns the deleted count.
func TestStore_DeleteNodesByFile(t *testing.T) {
	s := newTestStore(t)

	// Two nodes in the same file, one in a different file.
	n1 := sampleNode("del00000000000001", "function", "Fn1", "pkg/target.go")
	n2 := sampleNode("del00000000000002", "function", "Fn2", "pkg/target.go")
	n3 := sampleNode("del00000000000003", "function", "Fn3", "pkg/other.go")
	for _, n := range []Node{n1, n2, n3} {
		if err := s.UpsertNode(n); err != nil {
			t.Fatalf("UpsertNode %s: %v", n.ID, err)
		}
	}

	deleted, err := s.DeleteNodesByFile("pkg/target.go")
	if err != nil {
		t.Fatalf("DeleteNodesByFile: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted count: got %d; want 2", deleted)
	}

	// n1 and n2 must be gone.
	for _, id := range []string{n1.ID, n2.ID} {
		got, err := s.GetNode(id)
		if err != nil {
			t.Fatalf("GetNode(%s): %v", id, err)
		}
		if got != nil {
			t.Errorf("node %s still exists after DeleteNodesByFile", id)
		}
	}

	// n3 must still be present.
	got, err := s.GetNode(n3.ID)
	if err != nil {
		t.Fatalf("GetNode(n3): %v", err)
	}
	if got == nil {
		t.Error("node n3 was deleted unexpectedly")
	}
}

// TestStore_DeleteNodesByFile_CascadesEdges verifies that deleting a node via
// DeleteNodesByFile also removes edges that reference it (via ON DELETE CASCADE).
func TestStore_DeleteNodesByFile_CascadesEdges(t *testing.T) {
	s := newTestStore(t)

	src := sampleNode("cas00000000000001", "function", "Src", "pkg/src.go")
	dst := sampleNode("cas00000000000002", "function", "Dst", "pkg/dst.go")
	if err := s.UpsertNode(src); err != nil {
		t.Fatalf("UpsertNode src: %v", err)
	}
	if err := s.UpsertNode(dst); err != nil {
		t.Fatalf("UpsertNode dst: %v", err)
	}
	if err := s.UpsertEdge(sampleEdge(src.ID, dst.ID, EdgeKindCalls)); err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}

	// Verify edge exists before deletion.
	before, err := s.GetEdgesFrom(src.ID, "")
	if err != nil {
		t.Fatalf("GetEdgesFrom before delete: %v", err)
	}
	if len(before) != 1 {
		t.Fatalf("expected 1 edge before delete; got %d", len(before))
	}

	// Delete the source node's file.
	if _, err := s.DeleteNodesByFile("pkg/src.go"); err != nil {
		t.Fatalf("DeleteNodesByFile: %v", err)
	}

	// Edge must be gone due to CASCADE.
	after, err := s.GetEdgesFrom(src.ID, "")
	if err != nil {
		t.Fatalf("GetEdgesFrom after delete: %v", err)
	}
	if len(after) != 0 {
		t.Errorf("expected 0 edges after cascade delete; got %d", len(after))
	}
}

// TestStore_UpsertFile verifies that UpsertFile persists a FileRecord and
// GetFile retrieves it with all fields intact.
func TestStore_UpsertFile(t *testing.T) {
	s := newTestStore(t)

	f := FileRecord{
		Path:        "internal/store/memory.go",
		ContentHash: "abc123def456",
		Language:    "go",
		Size:        4096,
		ModifiedAt:  time.Now().Unix(),
		IndexedAt:   time.Now().Unix(),
		NodeCount:   42,
		Errors:      "",
	}

	if err := s.UpsertFile(f); err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}

	got, err := s.GetFile(f.Path)
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if got == nil {
		t.Fatal("GetFile returned nil; want record")
	}
	if got.Path != f.Path {
		t.Errorf("Path: got %q; want %q", got.Path, f.Path)
	}
	if got.ContentHash != f.ContentHash {
		t.Errorf("ContentHash: got %q; want %q", got.ContentHash, f.ContentHash)
	}
	if got.Language != f.Language {
		t.Errorf("Language: got %q; want %q", got.Language, f.Language)
	}
	if got.NodeCount != f.NodeCount {
		t.Errorf("NodeCount: got %d; want %d", got.NodeCount, f.NodeCount)
	}
}

// TestStore_UpsertFile_Replace verifies that upserting the same path twice
// replaces the stored record (INSERT OR REPLACE semantics).
func TestStore_UpsertFile_Replace(t *testing.T) {
	s := newTestStore(t)

	f := FileRecord{
		Path:        "pkg/a.go",
		ContentHash: "hash1",
		Language:    "go",
		Size:        100,
		ModifiedAt:  1000,
		IndexedAt:   1000,
		NodeCount:   1,
	}
	if err := s.UpsertFile(f); err != nil {
		t.Fatalf("first UpsertFile: %v", err)
	}

	f.ContentHash = "hash2"
	f.NodeCount = 5
	if err := s.UpsertFile(f); err != nil {
		t.Fatalf("second UpsertFile: %v", err)
	}

	got, err := s.GetFile(f.Path)
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if got.ContentHash != "hash2" {
		t.Errorf("ContentHash after replace: got %q; want hash2", got.ContentHash)
	}
	if got.NodeCount != 5 {
		t.Errorf("NodeCount after replace: got %d; want 5", got.NodeCount)
	}
}

// TestStore_GetFile_NotFound verifies that GetFile returns nil, nil when the
// path does not exist.
func TestStore_GetFile_NotFound(t *testing.T) {
	s := newTestStore(t)

	got, err := s.GetFile("nonexistent.go")
	if err != nil {
		t.Fatalf("GetFile: unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("GetFile: got %+v; want nil", got)
	}
}

// TestStore_ListFiles verifies that ListFiles returns all stored file records.
func TestStore_ListFiles(t *testing.T) {
	s := newTestStore(t)

	files := []FileRecord{
		{Path: "pkg/a.go", ContentHash: "h1", Language: "go", Size: 100, ModifiedAt: 1000, IndexedAt: 1000},
		{Path: "pkg/b.go", ContentHash: "h2", Language: "go", Size: 200, ModifiedAt: 2000, IndexedAt: 2000},
	}
	for _, f := range files {
		if err := s.UpsertFile(f); err != nil {
			t.Fatalf("UpsertFile %s: %v", f.Path, err)
		}
	}

	list, err := s.ListFiles()
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("ListFiles: got %d; want 2", len(list))
	}
}

// TestStore_DeleteFile verifies that DeleteFile removes the file record and
// subsequent GetFile returns nil.
func TestStore_DeleteFile(t *testing.T) {
	s := newTestStore(t)

	f := FileRecord{
		Path:        "pkg/todelete.go",
		ContentHash: "hx",
		Language:    "go",
		Size:        50,
		ModifiedAt:  500,
		IndexedAt:   500,
	}
	if err := s.UpsertFile(f); err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	if err := s.DeleteFile(f.Path); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}

	got, err := s.GetFile(f.Path)
	if err != nil {
		t.Fatalf("GetFile after delete: %v", err)
	}
	if got != nil {
		t.Errorf("GetFile after delete: got %+v; want nil", got)
	}
}

// TestStore_GetStats verifies that GetStats returns accurate aggregate counts
// after inserting nodes and files.
func TestStore_GetStats(t *testing.T) {
	s := newTestStore(t)

	// Insert 3 nodes: 2 functions, 1 struct.
	nodes := []Node{
		sampleNode("st0000000000001", "function", "Fn1", "pkg/a.go"),
		sampleNode("st0000000000002", "function", "Fn2", "pkg/b.go"),
		sampleNode("st0000000000003", "struct", "S1", "pkg/a.go"),
	}
	for _, n := range nodes {
		if err := s.UpsertNode(n); err != nil {
			t.Fatalf("UpsertNode %s: %v", n.ID, err)
		}
	}

	// Insert 1 file record.
	if err := s.UpsertFile(FileRecord{
		Path:     "pkg/a.go",
		ContentHash: "hA",
		Language: "go",
		Size:     100,
		ModifiedAt: 1000,
		IndexedAt:  1000,
	}); err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}

	stats, err := s.GetStats()
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if stats.NodeCount != 3 {
		t.Errorf("NodeCount: got %d; want 3", stats.NodeCount)
	}
	if stats.FileCount != 1 {
		t.Errorf("FileCount: got %d; want 1", stats.FileCount)
	}
	if stats.NodesByKind["function"] != 2 {
		t.Errorf("NodesByKind[function]: got %d; want 2", stats.NodesByKind["function"])
	}
	if stats.NodesByKind["struct"] != 1 {
		t.Errorf("NodesByKind[struct]: got %d; want 1", stats.NodesByKind["struct"])
	}
}

// TestStore_BatchUpsertNodes verifies that BatchUpsertNodes inserts all nodes
// within a single transaction.
func TestStore_BatchUpsertNodes(t *testing.T) {
	s := newTestStore(t)

	const count = 100
	nodes := make([]Node, count)
	for i := range nodes {
		id := fmt.Sprintf("%016x", i+1)
		nodes[i] = sampleNode(id, "function", fmt.Sprintf("Fn%d", i), "pkg/batch.go")
	}

	if err := s.BatchUpsertNodes(nodes); err != nil {
		t.Fatalf("BatchUpsertNodes: %v", err)
	}

	// Spot-check: every node must be retrievable.
	for i, n := range nodes {
		got, err := s.GetNode(n.ID)
		if err != nil {
			t.Fatalf("GetNode[%d] %s: %v", i, n.ID, err)
		}
		if got == nil {
			t.Errorf("GetNode[%d] %s: nil", i, n.ID)
		}
	}
}

// TestStore_ImportAliasPersistence verifies that Node.ImportAlias round-trips
// through UpsertNode / GetNode correctly (SPEC-047 D-GO2).
func TestStore_ImportAliasPersistence(t *testing.T) {
	s := newTestStore(t)

	n := sampleNode("impaliasnode0001", "import", "internal/store", "cmd/main.go")
	n.Kind = NodeKindImport
	n.ImportAlias = "store"

	if err := s.UpsertNode(n); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}

	got, err := s.GetNode(n.ID)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if got == nil {
		t.Fatal("GetNode: nil result")
	}
	if got.ImportAlias != "store" {
		t.Errorf("ImportAlias = %q; want %q", got.ImportAlias, "store")
	}
}

// TestStore_OpenDBIdempotent verifies that opening an on-disk database twice
// (simulating a binary upgrade that adds import_alias) does not fail. This
// covers SPEC-047 AC8 risk R3.
func TestStore_OpenDBIdempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/test-codegraph.db"

	// First open: creates schema with import_alias column.
	cdb1, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("first OpenDB: %v", err)
	}
	s1 := NewStore(cdb1)

	n := sampleNode("idemp000000001", "function", "Hello", "pkg/a.go")
	n.ImportAlias = ""
	if err := s1.UpsertNode(n); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	if err := cdb1.Close(); err != nil {
		t.Fatalf("Close first DB: %v", err)
	}

	// Second open: must succeed even though import_alias already exists.
	cdb2, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("second OpenDB: %v", err)
	}
	s2 := NewStore(cdb2)
	defer cdb2.Close()

	got, err := s2.GetNode(n.ID)
	if err != nil {
		t.Fatalf("GetNode after second open: %v", err)
	}
	if got == nil {
		t.Fatal("GetNode after second open: nil result")
	}
}

// TestStore_BatchUpsertEdges verifies that BatchUpsertEdges inserts all edges
// within a single transaction and they are queryable afterward.
func TestStore_BatchUpsertEdges(t *testing.T) {
	s := newTestStore(t)

	// One source, many targets.
	src := sampleNode("batchsrc00000001", "function", "Hub", "pkg/hub.go")
	if err := s.UpsertNode(src); err != nil {
		t.Fatalf("UpsertNode src: %v", err)
	}

	const count = 10
	targets := make([]Node, count)
	edges := make([]Edge, count)
	for i := range targets {
		id := fmt.Sprintf("batchtgt%08d", i+1)
		targets[i] = sampleNode(id, "function", fmt.Sprintf("T%d", i), "pkg/t.go")
		if err := s.UpsertNode(targets[i]); err != nil {
			t.Fatalf("UpsertNode target[%d]: %v", i, err)
		}
		edges[i] = sampleEdge(src.ID, targets[i].ID, EdgeKindCalls)
	}

	if err := s.BatchUpsertEdges(edges); err != nil {
		t.Fatalf("BatchUpsertEdges: %v", err)
	}

	got, err := s.GetEdgesFrom(src.ID, "")
	if err != nil {
		t.Fatalf("GetEdgesFrom after batch: %v", err)
	}
	if len(got) != count {
		t.Errorf("GetEdgesFrom: got %d; want %d", len(got), count)
	}
}
