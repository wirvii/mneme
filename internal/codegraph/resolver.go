package codegraph

import (
	"fmt"
	"strings"
)

// ResolveResult summarises a single resolution pass over unresolved_refs.
// Resolved is the number of refs that were matched to a node and converted to
// edges. Unresolved is the number of refs for which no matching node was found
// and which remain in the database.
type ResolveResult struct {
	// Resolved is the count of refs that were successfully matched and promoted
	// to real edges.
	Resolved int

	// Unresolved is the count of refs that could not be matched against any
	// node in the current graph. They remain in unresolved_refs for future
	// resolution passes or inspection.
	Unresolved int
}

// Resolver resolves cross-file references after all files in a project have
// been indexed. It reads every row from the unresolved_refs table and attempts
// to match each against existing nodes using a three-tier lookup strategy:
//
//  1. Exact match on nodes.qualified_name.
//  2. Suffix match: nodes.qualified_name LIKE '%.' + referenceName.
//  3. Short-name fallback on nodes.name.
//
// When a match is found the resolver creates a directed edge from the referring
// node to the matched node with the stored reference kind, then deletes the ref.
// Refs that cannot be matched are left in the table and counted as Unresolved.
type Resolver struct {
	store *Store
}

// NewResolver constructs a Resolver backed by the given Store.
func NewResolver(store *Store) *Resolver {
	return &Resolver{store: store}
}

// Resolve iterates over all rows in the unresolved_refs table and attempts to
// promote each to a real edge. It returns a ResolveResult summarising how many
// refs were resolved versus how many remain unresolvable with the current graph.
//
// Resolve is safe to call multiple times; already-resolved refs will not produce
// duplicate edges because EdgeExists is consulted before each insertion.
func (r *Resolver) Resolve() (*ResolveResult, error) {
	refs, err := r.store.ListUnresolvedRefs()
	if err != nil {
		return nil, fmt.Errorf("resolver: list unresolved refs: %w", err)
	}

	result := &ResolveResult{}

	for _, ref := range refs {
		node, err := r.findNode(ref.ReferenceName)
		if err != nil {
			return nil, fmt.Errorf("resolver: find node for ref %q: %w", ref.ReferenceName, err)
		}

		if node == nil {
			// No matching node found — leave the ref for a future pass.
			result.Unresolved++
			continue
		}

		// Guard against duplicate edges before inserting.
		kind := ref.ReferenceKind
		exists, err := r.store.EdgeExists(ref.FromNodeID, node.ID, kind)
		if err != nil {
			return nil, fmt.Errorf("resolver: check edge exists: %w", err)
		}
		if !exists {
			edge := Edge{
				Source:     ref.FromNodeID,
				Target:     node.ID,
				Kind:       kind,
				Line:       ref.Line,
				Col:        ref.Col,
				Provenance: "resolver",
			}
			if err := r.store.UpsertEdge(edge); err != nil {
				return nil, fmt.Errorf("resolver: upsert edge: %w", err)
			}
		}

		// Remove the resolved ref from the database.
		if err := r.store.DeleteUnresolvedRef(ref.ID); err != nil {
			return nil, fmt.Errorf("resolver: delete unresolved ref %d: %w", ref.ID, err)
		}

		result.Resolved++
	}

	return result, nil
}

// findNode attempts to locate a node matching referenceName using a three-tier
// strategy. It returns nil when no node matches in any tier.
//
//  1. Exact match on qualified_name.
//  2. Suffix match: qualified_name ends with "." + referenceName.
//  3. Name match: nodes.name equals the last component after the last dot.
func (r *Resolver) findNode(referenceName string) (*Node, error) {
	// Tier 1: exact match on qualified_name.
	node, err := r.store.FindNodeByQualifiedName(referenceName)
	if err != nil {
		return nil, err
	}
	if node != nil {
		return node, nil
	}

	// Tier 2: suffix match — qualified_name LIKE '%.' + referenceName.
	// Only attempted when the name contains a dot (otherwise it is a plain
	// short name and tier 3 is the right strategy).
	if strings.Contains(referenceName, ".") {
		node, err = r.store.FindNodeBySuffix(referenceName)
		if err != nil {
			return nil, err
		}
		if node != nil {
			return node, nil
		}
	}

	// Tier 3: match on the short name (last component after the final dot).
	shortName := referenceName
	if idx := strings.LastIndex(referenceName, "."); idx >= 0 {
		shortName = referenceName[idx+1:]
	}
	if shortName == "" {
		return nil, nil
	}
	return r.store.FindNodeByName(shortName)
}
