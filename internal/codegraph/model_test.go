package codegraph

import "testing"

// TestNodeKind_Valid verifies that Valid() returns true for all recognised
// NodeKind constants and false for unknown values.
func TestNodeKind_Valid(t *testing.T) {
	cases := []struct {
		name  string
		kinds []NodeKind
		want  bool
	}{
		{
			name: "all valid constants",
			kinds: []NodeKind{
				NodeKindFile, NodeKindModule, NodeKindClass, NodeKindStruct,
				NodeKindInterface, NodeKindTrait, NodeKindProtocol, NodeKindFunction,
				NodeKindMethod, NodeKindProperty, NodeKindField, NodeKindVariable,
				NodeKindConstant, NodeKindEnum, NodeKindEnumMember, NodeKindTypeAlias,
				NodeKindNamespace, NodeKindParameter, NodeKindImport, NodeKindExport,
				NodeKindRoute, NodeKindComponent,
			},
			want: true,
		},
		{
			name:  "invalid kinds return false",
			kinds: []NodeKind{"", "unknown", "CLASS", "File", "FUNCTION"},
			want:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range tc.kinds {
				got := k.Valid()
				if got != tc.want {
					t.Errorf("NodeKind(%q).Valid() = %v, want %v", k, got, tc.want)
				}
			}
		})
	}
}

// TestEdgeKind_Valid verifies that Valid() returns true for all recognised
// EdgeKind constants and false for unknown values.
func TestEdgeKind_Valid(t *testing.T) {
	cases := []struct {
		name  string
		kinds []EdgeKind
		want  bool
	}{
		{
			name: "all valid constants",
			kinds: []EdgeKind{
				EdgeKindContains, EdgeKindCalls, EdgeKindImports, EdgeKindExports,
				EdgeKindExtends, EdgeKindImplements, EdgeKindReferences, EdgeKindTypeOf,
				EdgeKindReturns, EdgeKindInstantiates, EdgeKindOverrides, EdgeKindDecorates,
			},
			want: true,
		},
		{
			name:  "invalid kinds return false",
			kinds: []EdgeKind{"", "unknown", "CALLS", "Contains", "uses"},
			want:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range tc.kinds {
				got := k.Valid()
				if got != tc.want {
					t.Errorf("EdgeKind(%q).Valid() = %v, want %v", k, got, tc.want)
				}
			}
		})
	}
}

// TestNodeID_Deterministic verifies that NodeID produces consistent and
// distinguishable outputs: same inputs always produce the same ID, and
// different inputs produce different IDs.
func TestNodeID_Deterministic(t *testing.T) {
	t.Run("same inputs produce same output", func(t *testing.T) {
		filePath := "internal/service/search.go"
		qualifiedName := "(*MemoryService).Search"

		id1 := NodeID(filePath, qualifiedName)
		id2 := NodeID(filePath, qualifiedName)

		if id1 != id2 {
			t.Errorf("NodeID(%q, %q) produced different results: %q vs %q", filePath, qualifiedName, id1, id2)
		}
	})

	t.Run("different file paths produce different output", func(t *testing.T) {
		qualifiedName := "Foo"
		id1 := NodeID("internal/a/a.go", qualifiedName)
		id2 := NodeID("internal/b/b.go", qualifiedName)

		if id1 == id2 {
			t.Errorf("NodeID with different file paths produced same ID: %q", id1)
		}
	})

	t.Run("different qualified names produce different output", func(t *testing.T) {
		filePath := "internal/service/search.go"
		id1 := NodeID(filePath, "FuncA")
		id2 := NodeID(filePath, "FuncB")

		if id1 == id2 {
			t.Errorf("NodeID with different qualified names produced same ID: %q", id1)
		}
	})

	t.Run("output is 16 hex chars", func(t *testing.T) {
		id := NodeID("cmd/mneme/main.go", "main")
		if len(id) != 16 {
			t.Errorf("NodeID length = %d, want 16", len(id))
		}
		for _, c := range id {
			isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
			if !isHex {
				t.Errorf("NodeID contains non-hex character %q in %q", c, id)
				break
			}
		}
	})
}
