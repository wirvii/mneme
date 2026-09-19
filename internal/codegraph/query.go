package codegraph

import "sort"

// QueryEngine provides graph traversal operations — callers, callees, impact
// analysis, and path tracing — on top of the codegraph Store.
//
// All traversals use BFS with a visited set to handle cycles correctly.
// The engine queries edges from the Store and fetches full Node records for
// each discovered symbol; it never holds a long-lived cursor.
type QueryEngine struct {
	store *Store
}

// NewQueryEngine constructs a QueryEngine backed by the given Store.
func NewQueryEngine(store *Store) *QueryEngine {
	return &QueryEngine{store: store}
}

const (
	defaultDepth = 10
	defaultLimit = 50
)

// normalise applies the per-method defaults for depth and limit.
func normalise(depth, limit int) (int, int) {
	if depth <= 0 {
		depth = defaultDepth
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	return depth, limit
}

// Callers returns the nodes that call or reference the given node via incoming
// "calls" edges. BFS is used so that the traversal respects the depth budget
// and terminates even in graphs with cycles.
//
// depth = 0 → use the internal default (10 hops).
// limit = 0 → use the internal default (50 results).
func (q *QueryEngine) Callers(nodeID string, depth, limit int) ([]Node, error) {
	depth, limit = normalise(depth, limit)
	return q.bfsIncoming(nodeID, depth, limit, []string{string(EdgeKindCalls)})
}

// Callees returns the nodes that the given node calls or references via outgoing
// "calls" edges.
//
// depth = 0 → use the internal default (10 hops).
// limit = 0 → use the internal default (50 results).
func (q *QueryEngine) Callees(nodeID string, depth, limit int) ([]Node, error) {
	depth, limit = normalise(depth, limit)
	return q.bfsOutgoing(nodeID, depth, limit, []string{string(EdgeKindCalls)})
}

// Impact returns the transitive set of nodes that are affected by a change to
// the given symbol — i.e., the "blast radius". It follows incoming edges of
// kinds calls, imports, extends, and implements, giving the full set of
// dependants at any depth.
//
// depth = 0 → use the internal default (10 hops).
// limit = 0 → use the internal default (50 results).
func (q *QueryEngine) Impact(nodeID string, depth, limit int) ([]Node, error) {
	depth, limit = normalise(depth, limit)
	kinds := []string{
		string(EdgeKindCalls),
		string(EdgeKindImports),
		string(EdgeKindExtends),
		string(EdgeKindImplements),
	}
	return q.bfsIncoming(nodeID, depth, limit, kinds)
}

// Affected returns nodes that depend on symbols defined by paths. It follows
// only incoming calls, imports, and contains edges. The current graph models
// imports as they were indexed; this traversal does not infer module consumers
// that have no corresponding edge.
//
// Results are ordered by shortest depth, file path, qualified name, then node
// ID. Total is computed before the limit is applied.
func (q *QueryEngine) Affected(paths []string, depth, limit int) (AffectedResult, error) {
	if depth <= 0 {
		depth = 3
	}
	if limit <= 0 {
		limit = defaultLimit
	}

	inputs := sortedUnique(paths)
	result := AffectedResult{
		Inputs:         inputs,
		MissingPaths:   make([]string, 0),
		UntrackedPaths: make([]string, 0),
		AffectedNodes:  make([]AffectedNode, 0),
	}

	type state struct {
		nodeID string
		depth  int
	}
	visited := make(map[string]bool)
	queue := make([]state, 0)
	for _, path := range inputs {
		nodes, err := q.store.GetNodesByFilePath(path)
		if err != nil {
			return AffectedResult{}, err
		}
		if len(nodes) == 0 {
			result.MissingPaths = append(result.MissingPaths, path)
			continue
		}
		for _, node := range nodes {
			if visited[node.ID] {
				continue
			}
			visited[node.ID] = true
			queue = append(queue, state{nodeID: node.ID})
		}
	}

	kinds := []EdgeKind{EdgeKindCalls, EdgeKindImports, EdgeKindContains}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current.depth >= depth {
			continue
		}
		for _, kind := range kinds {
			edges, err := q.store.GetEdgesTo(current.nodeID, string(kind))
			if err != nil {
				return AffectedResult{}, err
			}
			for _, edge := range edges {
				if visited[edge.Source] {
					continue
				}
				visited[edge.Source] = true
				node, err := q.store.GetNode(edge.Source)
				if err != nil {
					return AffectedResult{}, err
				}
				if node == nil {
					continue
				}
				nextDepth := current.depth + 1
				result.AffectedNodes = append(result.AffectedNodes, affectedNode(*node, kind, nextDepth))
				queue = append(queue, state{nodeID: edge.Source, depth: nextDepth})
			}
		}
	}

	sort.Slice(result.AffectedNodes, func(i, j int) bool {
		left, right := result.AffectedNodes[i], result.AffectedNodes[j]
		if left.Depth != right.Depth {
			return left.Depth < right.Depth
		}
		if left.FilePath != right.FilePath {
			return left.FilePath < right.FilePath
		}
		if left.QualifiedName != right.QualifiedName {
			return left.QualifiedName < right.QualifiedName
		}
		return left.ID < right.ID
	})
	result.Total = len(result.AffectedNodes)
	if result.Total > limit {
		result.AffectedNodes = result.AffectedNodes[:limit]
		result.Truncated = true
	}
	return result, nil
}

func affectedNode(node Node, relation EdgeKind, depth int) AffectedNode {
	return AffectedNode{
		ID:            node.ID,
		Kind:          node.Kind,
		Name:          node.Name,
		QualifiedName: node.QualifiedName,
		FilePath:      node.FilePath,
		Language:      node.Language,
		Relation:      relation,
		Depth:         depth,
	}
}

func sortedUnique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

// Trace finds the shortest path between two nodes using BFS on outgoing "calls"
// edges. It returns the ordered sequence of nodes and the edges connecting them.
// When no path is found within maxDepth hops, both slices are empty (nil).
func (q *QueryEngine) Trace(fromID, toID string, maxDepth int) ([]Node, []Edge, error) {
	if maxDepth <= 0 {
		maxDepth = defaultDepth
	}

	type state struct {
		nodeID string
		depth  int
	}

	visited := make(map[string]bool)
	parent := make(map[string]traceArrival)
	queue := []state{{nodeID: fromID, depth: 0}}
	visited[fromID] = true

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		if cur.depth >= maxDepth {
			continue
		}

		edges, err := q.store.GetEdgesFrom(cur.nodeID, string(EdgeKindCalls))
		if err != nil {
			return nil, nil, err
		}

		for _, e := range edges {
			if visited[e.Target] {
				continue
			}
			visited[e.Target] = true
			parent[e.Target] = traceArrival{parentID: cur.nodeID, edge: e}

			if e.Target == toID {
				// Reconstruct path from toID back to fromID.
				return q.reconstructPath(fromID, toID, parent)
			}

			queue = append(queue, state{nodeID: e.Target, depth: cur.depth + 1})
		}
	}

	// No path found.
	return nil, nil, nil
}

// traceArrival records how BFS arrived at a node during Trace traversal.
type traceArrival struct {
	parentID string
	edge     Edge
}

// reconstructPath walks the parent map backwards from toID to fromID and
// returns the forward-ordered node and edge slices.
func (q *QueryEngine) reconstructPath(fromID, toID string, parent map[string]traceArrival) ([]Node, []Edge, error) {
	// Walk backwards to collect node IDs and edges in reverse order.
	var revNodeIDs []string
	var revEdges []Edge

	cur := toID
	for cur != fromID {
		arr := parent[cur]
		revNodeIDs = append(revNodeIDs, cur)
		revEdges = append(revEdges, arr.edge)
		cur = arr.parentID
	}
	revNodeIDs = append(revNodeIDs, fromID)

	// Reverse to get forward order.
	n := len(revNodeIDs)
	nodeIDs := make([]string, n)
	for i, id := range revNodeIDs {
		nodeIDs[n-1-i] = id
	}

	edges := make([]Edge, len(revEdges))
	for i, e := range revEdges {
		edges[len(revEdges)-1-i] = e
	}

	// Fetch full Node records.
	nodes := make([]Node, 0, n)
	for _, id := range nodeIDs {
		node, err := q.store.GetNode(id)
		if err != nil {
			return nil, nil, err
		}
		if node != nil {
			nodes = append(nodes, *node)
		}
	}

	return nodes, edges, nil
}

// bfsIncoming performs a BFS traversal following incoming edges of the given
// kinds (edges where target = current node). It collects all source nodes
// discovered up to depth hops from the start, excluding the start node itself,
// and respects the limit cap.
func (q *QueryEngine) bfsIncoming(startID string, depth, limit int, kinds []string) ([]Node, error) {
	type state struct {
		nodeID string
		d      int
	}

	visited := make(map[string]bool)
	visited[startID] = true
	queue := []state{{nodeID: startID, d: 0}}

	var results []Node

	for len(queue) > 0 && len(results) < limit {
		cur := queue[0]
		queue = queue[1:]

		if cur.d >= depth {
			continue
		}

		for _, kind := range kinds {
			edges, err := q.store.GetEdgesTo(cur.nodeID, kind)
			if err != nil {
				return nil, err
			}
			for _, e := range edges {
				if visited[e.Source] {
					continue
				}
				visited[e.Source] = true

				node, err := q.store.GetNode(e.Source)
				if err != nil {
					return nil, err
				}
				if node == nil {
					continue
				}
				results = append(results, *node)
				if len(results) >= limit {
					return results, nil
				}
				queue = append(queue, state{nodeID: e.Source, d: cur.d + 1})
			}
		}
	}

	return results, nil
}

// bfsOutgoing performs a BFS traversal following outgoing edges of the given
// kinds (edges where source = current node). It collects all target nodes
// discovered up to depth hops from the start, excluding the start node itself,
// and respects the limit cap.
func (q *QueryEngine) bfsOutgoing(startID string, depth, limit int, kinds []string) ([]Node, error) {
	type state struct {
		nodeID string
		d      int
	}

	visited := make(map[string]bool)
	visited[startID] = true
	queue := []state{{nodeID: startID, d: 0}}

	var results []Node

	for len(queue) > 0 && len(results) < limit {
		cur := queue[0]
		queue = queue[1:]

		if cur.d >= depth {
			continue
		}

		for _, kind := range kinds {
			edges, err := q.store.GetEdgesFrom(cur.nodeID, kind)
			if err != nil {
				return nil, err
			}
			for _, e := range edges {
				if visited[e.Target] {
					continue
				}
				visited[e.Target] = true

				node, err := q.store.GetNode(e.Target)
				if err != nil {
					return nil, err
				}
				if node == nil {
					continue
				}
				results = append(results, *node)
				if len(results) >= limit {
					return results, nil
				}
				queue = append(queue, state{nodeID: e.Target, d: cur.d + 1})
			}
		}
	}

	return results, nil
}
