package codegraph

import (
	"testing"
	"time"
)

// insertFileNode inserts a node of kind=file at the given path and returns it.
func insertFileNode(t *testing.T, s *Store, filePath string) Node {
	t.Helper()
	id := NodeID(filePath, filePath)
	n := Node{
		ID:            id,
		Kind:          NodeKindFile,
		Name:          filePath,
		QualifiedName: filePath,
		FilePath:      filePath,
		Language:      "go",
		StartLine:     1,
		EndLine:       1,
		UpdatedAt:     time.Now().Unix(),
	}
	if err := s.UpsertNode(n); err != nil {
		t.Fatalf("insertFileNode(%s): %v", filePath, err)
	}
	return n
}

// insertSymbolNode inserts a non-file node belonging to filePath and returns it.
func insertSymbolNode(t *testing.T, s *Store, name, qualifiedName, filePath string, startLine int) Node {
	t.Helper()
	id := NodeID(filePath, qualifiedName)
	n := Node{
		ID:            id,
		Kind:          NodeKindFunction,
		Name:          name,
		QualifiedName: qualifiedName,
		FilePath:      filePath,
		Language:      "go",
		StartLine:     startLine,
		EndLine:       startLine + 5,
		UpdatedAt:     time.Now().Unix(),
	}
	if err := s.UpsertNode(n); err != nil {
		t.Fatalf("insertSymbolNode(%s): %v", qualifiedName, err)
	}
	return n
}

// insertContainsEdge inserts an EdgeKindContains edge from source to target.
func insertContainsEdge(t *testing.T, s *Store, sourceID, targetID string) {
	t.Helper()
	e := Edge{
		Source:     sourceID,
		Target:     targetID,
		Kind:       EdgeKindContains,
		Provenance: "test",
	}
	if err := s.UpsertEdge(e); err != nil {
		t.Fatalf("insertContainsEdge(%s->%s): %v", sourceID, targetID, err)
	}
}

// TestBridge_FindCodeContext_ByFilePath verifies that querying by an exact file
// path returns the symbols contained in that file (not the file node itself).
func TestBridge_FindCodeContext_ByFilePath(t *testing.T) {
	s := newTestStore(t)
	b := NewBridge(s)

	const filePath = "internal/service/search.go"

	fileNode := insertFileNode(t, s, filePath)
	fn1 := insertSymbolNode(t, s, "Search", "service.Search", filePath, 10)
	fn2 := insertSymbolNode(t, s, "SearchByTag", "service.SearchByTag", filePath, 30)
	fn3 := insertSymbolNode(t, s, "SearchByScope", "service.SearchByScope", filePath, 50)

	insertContainsEdge(t, s, fileNode.ID, fn1.ID)
	insertContainsEdge(t, s, fileNode.ID, fn2.ID)
	insertContainsEdge(t, s, fileNode.ID, fn3.ID)

	nodes, err := b.FindCodeContext(filePath)
	if err != nil {
		t.Fatalf("FindCodeContext: %v", err)
	}

	if len(nodes) != 3 {
		t.Errorf("len(nodes) = %d, want 3", len(nodes))
	}

	// Verify none of the returned nodes is the file node itself.
	for _, n := range nodes {
		if n.ID == fileNode.ID {
			t.Errorf("file node must not appear in results")
		}
	}

	// All returned nodes must belong to the queried file.
	for _, n := range nodes {
		if n.FilePath != filePath {
			t.Errorf("node %q file_path = %q, want %q", n.Name, n.FilePath, filePath)
		}
	}
}

// TestBridge_FindCodeContext_ByName verifies that querying by a qualified name
// returns the matching node when no file node matches the exact path.
func TestBridge_FindCodeContext_ByName(t *testing.T) {
	s := newTestStore(t)
	b := NewBridge(s)

	insertSymbolNode(t, s, "Search", "service.Search", "internal/service/search.go", 10)

	nodes, err := b.FindCodeContext("service.Search")
	if err != nil {
		t.Fatalf("FindCodeContext: %v", err)
	}

	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(nodes))
	}
	if nodes[0].QualifiedName != "service.Search" {
		t.Errorf("QualifiedName = %q, want %q", nodes[0].QualifiedName, "service.Search")
	}
}

// TestBridge_FindCodeContext_NoMatch verifies that querying a non-existent path
// returns an empty slice without error.
func TestBridge_FindCodeContext_NoMatch(t *testing.T) {
	s := newTestStore(t)
	b := NewBridge(s)

	nodes, err := b.FindCodeContext("does/not/exist.go")
	if err != nil {
		t.Fatalf("FindCodeContext: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("len(nodes) = %d, want 0", len(nodes))
	}
}

// TestBridge_HasCodeContext_True verifies that HasCodeContext returns true when a
// node with a matching file_path exists in the store.
func TestBridge_HasCodeContext_True(t *testing.T) {
	s := newTestStore(t)
	b := NewBridge(s)

	insertFileNode(t, s, "main.go")

	if !b.HasCodeContext("main.go") {
		t.Error("HasCodeContext(\"main.go\") = false, want true")
	}
}

// TestBridge_HasCodeContext_False verifies that HasCodeContext returns false when
// no node matches the given path.
func TestBridge_HasCodeContext_False(t *testing.T) {
	s := newTestStore(t)
	b := NewBridge(s)

	if b.HasCodeContext("nonexistent.go") {
		t.Error("HasCodeContext(\"nonexistent.go\") = true, want false")
	}
}

// TestBridge_FileSummary verifies that FileSummary returns all non-file nodes for
// a given file, ordered by start_line.
func TestBridge_FileSummary(t *testing.T) {
	s := newTestStore(t)
	b := NewBridge(s)

	const filePath = "main.go"

	insertFileNode(t, s, filePath)
	insertSymbolNode(t, s, "main", "main.main", filePath, 5)
	insertSymbolNode(t, s, "helper", "main.helper", filePath, 20)
	insertSymbolNode(t, s, "init", "main.init", filePath, 1)

	nodes, err := b.FileSummary(filePath)
	if err != nil {
		t.Fatalf("FileSummary: %v", err)
	}

	if len(nodes) != 3 {
		t.Fatalf("len(nodes) = %d, want 3", len(nodes))
	}

	// Results must be ordered by start_line ascending.
	for i := 1; i < len(nodes); i++ {
		if nodes[i].StartLine < nodes[i-1].StartLine {
			t.Errorf("nodes not ordered by start_line: nodes[%d].StartLine=%d < nodes[%d].StartLine=%d",
				i, nodes[i].StartLine, i-1, nodes[i-1].StartLine)
		}
	}

	// File node must not appear in results.
	for _, n := range nodes {
		if n.Kind == NodeKindFile {
			t.Errorf("file node must not appear in FileSummary results")
		}
	}
}
