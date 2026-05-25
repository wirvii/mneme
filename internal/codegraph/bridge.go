package codegraph

import "fmt"

// Bridge provides cross-query between mneme memory entities and code graph nodes.
// It enables callers to jump from a memory entity identified by a file path or
// module name (as stored in the mneme knowledge graph) to the corresponding code
// symbols in the code graph, without duplicating data between the two systems.
//
// Lookups are based on string matching only — no deep semantic analysis is
// performed. This keeps the bridge lightweight and free of import cycles.
type Bridge struct {
	store *Store
}

// NewBridge creates a Bridge backed by the given code graph store.
func NewBridge(store *Store) *Bridge {
	return &Bridge{store: store}
}

// FindCodeContext returns code nodes that match nameOrPath. The lookup strategy
// is:
//  1. Exact match on nodes.file_path = nameOrPath: if a file node exists for that
//     path, return all non-file nodes contained in that file (via EdgeKindContains
//     edges). This handles the common case of a memory entity pointing to a source
//     file.
//  2. If no file node matches, fall back to an exact qualified_name search for
//     nameOrPath, then a name search as a last resort.
//
// The returned slice is empty (never nil) when no match is found. Errors from the
// store are propagated as-is with additional context.
func (b *Bridge) FindCodeContext(nameOrPath string) ([]Node, error) {
	// Step 1: look for a file node with file_path = nameOrPath.
	fileNode, err := b.store.FindNodeByQualifiedName(nameOrPath)
	if err != nil {
		return nil, fmt.Errorf("codegraph: bridge: find code context: %w", err)
	}

	if fileNode != nil && fileNode.Kind == NodeKindFile {
		// Found a file node — return all symbols it contains.
		edges, err := b.store.GetEdgesFrom(fileNode.ID, string(EdgeKindContains))
		if err != nil {
			return nil, fmt.Errorf("codegraph: bridge: find code context: get contains edges: %w", err)
		}

		results := make([]Node, 0, len(edges))
		for _, edge := range edges {
			n, err := b.store.GetNode(edge.Target)
			if err != nil {
				return nil, fmt.Errorf("codegraph: bridge: find code context: get node %q: %w", edge.Target, err)
			}
			if n == nil || n.Kind == NodeKindFile {
				continue
			}
			results = append(results, *n)
		}
		return results, nil
	}

	// Step 2: try qualified_name match (already fetched above but may be non-file
	// or nil). If we got a non-file node from the qualified_name lookup, return it.
	if fileNode != nil {
		return []Node{*fileNode}, nil
	}

	// Step 3: partial match — check nodes whose file_path = nameOrPath directly
	// (covers the case where the file node's qualified_name differs from its path).
	symbols, err := b.store.GetNodesByFilePath(nameOrPath)
	if err != nil {
		return nil, fmt.Errorf("codegraph: bridge: find code context: get by file path: %w", err)
	}
	if len(symbols) > 0 {
		return symbols, nil
	}

	// Step 4: short-name fallback.
	n, err := b.store.FindNodeByName(nameOrPath)
	if err != nil {
		return nil, fmt.Errorf("codegraph: bridge: find code context: find by name: %w", err)
	}
	if n != nil {
		return []Node{*n}, nil
	}

	return []Node{}, nil
}

// HasCodeContext reports whether any code graph node is associated with the given
// name or path. It is a lightweight existence check for annotating memory search
// results — it does not load full node data.
//
// Returns true when at least one node has file_path = nameOrPath, or when a node
// with qualified_name = nameOrPath exists.
func (b *Bridge) HasCodeContext(nameOrPath string) bool {
	exists, err := b.store.NodeExistsForPath(nameOrPath)
	if err != nil || exists {
		return exists
	}

	// Also check qualified_name in case nameOrPath is a symbol identifier.
	n, err := b.store.FindNodeByQualifiedName(nameOrPath)
	if err != nil {
		return false
	}
	return n != nil
}

// FileSummary returns all non-file symbols defined in the given source file,
// ordered by start_line ascending. It is useful for quickly communicating the
// structure of a file without reading its actual content.
//
// Returns an empty slice (never nil) when no symbols are found for filePath.
func (b *Bridge) FileSummary(filePath string) ([]Node, error) {
	nodes, err := b.store.GetNodesByFilePath(filePath)
	if err != nil {
		return nil, fmt.Errorf("codegraph: bridge: file summary: %w", err)
	}
	if nodes == nil {
		return []Node{}, nil
	}
	return nodes, nil
}
