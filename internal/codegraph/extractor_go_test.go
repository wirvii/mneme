package codegraph

import (
	"strings"
	"testing"
)

// testGoSource is the canonical source fixture used across all GoExtractor tests.
// It exercises functions, methods, structs, interfaces, imports, and call sites.
const testGoSource = `package service

import (
	"context"
	"fmt"

	"github.com/juanftp/mneme/internal/store"
)

// MemoryService orchestrates memory operations.
type MemoryService struct {
	store *store.MemoryStore
}

// Save persists a new memory.
func (s *MemoryService) Save(ctx context.Context, title string) error {
	fmt.Println("saving")
	return s.store.Create(ctx, title)
}

// Search finds memories matching a query.
func Search(query string) []string {
	return nil
}

type Searcher interface {
	Search(q string) []string
}
`

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func filterNodes(nodes []Node, kind NodeKind) []Node {
	var out []Node
	for _, n := range nodes {
		if n.Kind == kind {
			out = append(out, n)
		}
	}
	return out
}

func filterEdges(edges []Edge, kind EdgeKind) []Edge {
	var out []Edge
	for _, e := range edges {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func nodeNames(nodes []Node) []string {
	names := make([]string, len(nodes))
	for i, n := range nodes {
		names[i] = n.Name
	}
	return names
}

func extractTestResult(t *testing.T) *ExtractionResult {
	t.Helper()
	e := NewGoExtractor()
	result, err := e.Extract("service/memory.go", []byte(testGoSource))
	if err != nil {
		t.Fatalf("Extract returned unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("Extract returned nil result")
	}
	return result
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestGoExtractor_Language verifies the extractor identifies itself as "go".
func TestGoExtractor_Language(t *testing.T) {
	e := NewGoExtractor()
	if got := e.Language(); got != "go" {
		t.Errorf("Language() = %q, want %q", got, "go")
	}
}

// TestGoExtractor_ExtractsFunctions verifies that top-level functions are
// extracted with kind=function and the correct export status.
func TestGoExtractor_ExtractsFunctions(t *testing.T) {
	result := extractTestResult(t)
	fns := filterNodes(result.Nodes, NodeKindFunction)
	names := nodeNames(fns)

	found := false
	for _, n := range fns {
		if n.Name == "Search" {
			found = true
			if !n.IsExported {
				t.Errorf("Search should be exported")
			}
			if n.Kind != NodeKindFunction {
				t.Errorf("Search.Kind = %q, want %q", n.Kind, NodeKindFunction)
			}
		}
	}
	if !found {
		t.Errorf("function Search not found; got: %v", names)
	}
}

// TestGoExtractor_ExtractsMethods verifies that receiver functions are extracted
// as methods with the correct QualifiedName (ReceiverType.MethodName).
func TestGoExtractor_ExtractsMethods(t *testing.T) {
	result := extractTestResult(t)
	methods := filterNodes(result.Nodes, NodeKindMethod)

	var save *Node
	for i := range methods {
		if methods[i].Name == "Save" {
			save = &methods[i]
			break
		}
	}
	if save == nil {
		t.Fatalf("method Save not found; got methods: %v", nodeNames(methods))
	}
	if save.QualifiedName != "MemoryService.Save" {
		t.Errorf("Save.QualifiedName = %q, want %q", save.QualifiedName, "MemoryService.Save")
	}
}

// TestGoExtractor_ExtractsStructs verifies that struct type declarations are
// extracted with kind=struct.
func TestGoExtractor_ExtractsStructs(t *testing.T) {
	result := extractTestResult(t)
	structs := filterNodes(result.Nodes, NodeKindStruct)
	names := nodeNames(structs)

	found := false
	for _, n := range structs {
		if n.Name == "MemoryService" {
			found = true
		}
	}
	if !found {
		t.Errorf("struct MemoryService not found; got: %v", names)
	}
}

// TestGoExtractor_ExtractsInterfaces verifies that interface type declarations
// are extracted with kind=interface.
func TestGoExtractor_ExtractsInterfaces(t *testing.T) {
	result := extractTestResult(t)
	ifaces := filterNodes(result.Nodes, NodeKindInterface)
	names := nodeNames(ifaces)

	found := false
	for _, n := range ifaces {
		if n.Name == "Searcher" {
			found = true
		}
	}
	if !found {
		t.Errorf("interface Searcher not found; got: %v", names)
	}
}

// TestGoExtractor_ExtractsImports verifies that import statements are extracted.
// The test source has 3 imports: "context", "fmt", and the internal store package.
func TestGoExtractor_ExtractsImports(t *testing.T) {
	result := extractTestResult(t)
	imports := filterNodes(result.Nodes, NodeKindImport)
	if len(imports) != 3 {
		t.Errorf("expected 3 import nodes, got %d: %v", len(imports), nodeNames(imports))
	}
}

// TestGoExtractor_ContainsEdges verifies that the file node has contains edges
// pointing to each top-level declaration.
func TestGoExtractor_ContainsEdges(t *testing.T) {
	result := extractTestResult(t)
	containsEdges := filterEdges(result.Edges, EdgeKindContains)
	if len(containsEdges) == 0 {
		t.Error("expected at least one contains edge, got none")
	}
}

// TestGoExtractor_CallEdges verifies that call sites inside function bodies are
// recorded as calls edges. Save calls fmt.Println at minimum.
func TestGoExtractor_CallEdges(t *testing.T) {
	result := extractTestResult(t)
	callEdges := filterEdges(result.Edges, EdgeKindCalls)

	// At minimum one call edge from Save (fmt.Println or s.store.Create).
	if len(callEdges) == 0 && len(result.UnresolvedRefs) == 0 {
		t.Error("expected at least one call edge or unresolved ref for function calls")
	}

	// Verify that the call to fmt.Println is captured — either as a resolved
	// calls edge or as an unresolved ref to "fmt.Println".
	found := false
	for _, e := range callEdges {
		if strings.Contains(e.Target, "fmt.Println") || strings.Contains(e.Target, "Println") {
			found = true
			break
		}
	}
	if !found {
		for _, ref := range result.UnresolvedRefs {
			if strings.Contains(ref.ReferenceName, "fmt.Println") || strings.Contains(ref.ReferenceName, "Println") {
				found = true
				break
			}
		}
	}
	if !found {
		t.Errorf("call to fmt.Println not found in call edges or unresolved refs; "+
			"call edges: %d, unresolved: %d", len(callEdges), len(result.UnresolvedRefs))
	}
}

// TestGoExtractor_Docstring verifies that documentation comments are attached to
// the corresponding nodes.
func TestGoExtractor_Docstring(t *testing.T) {
	result := extractTestResult(t)
	structs := filterNodes(result.Nodes, NodeKindStruct)

	for _, n := range structs {
		if n.Name == "MemoryService" {
			if n.Docstring == "" {
				t.Error("MemoryService should have a docstring")
			}
			return
		}
	}
	t.Error("MemoryService struct node not found")
}

// TestGoExtractor_Signature verifies that function and method nodes carry a
// non-empty signature describing their parameters and return types.
func TestGoExtractor_Signature(t *testing.T) {
	result := extractTestResult(t)

	// Check Save or Search for a non-empty signature.
	for _, n := range result.Nodes {
		if (n.Name == "Save" || n.Name == "Search") && n.Signature != "" {
			return
		}
	}
	t.Error("expected at least one function/method node with a non-empty signature")
}
