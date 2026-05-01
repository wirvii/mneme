package model

// ExploreRequest specifies the parameters for a graph exploration starting
// from a seed memory. The exploration performs a prioritised BFS traversal
// following strong relations and returns connected memories with their
// distance from the seed and accumulated path weight.
type ExploreRequest struct {
	// Seed identifies the starting memory. Accepts a full UUID (36 chars),
	// a short UUID prefix (8+ hex chars without hyphens), or a topic_key.
	Seed string `json:"seed"`

	// Depth is the maximum number of hops from the seed. When nil, the
	// configured ExploreDefaultDepth (2) is used. Range: 0–5.
	// Use a pointer so that 0 (valid: returns no neighbors) can be
	// distinguished from "not specified".
	Depth *int `json:"depth,omitempty"`

	// Budget is the maximum token estimate for returned memories. When zero,
	// falls back to the configured ExploreDefaultBudget (4000).
	Budget int `json:"budget,omitempty"`

	// Threshold is the minimum relation weight to follow. Relations below this
	// value are skipped. Defaults to the configured ExpansionThreshold (0.3).
	// Range: 0.0–1.0.
	Threshold float64 `json:"threshold,omitempty"`

	// Project restricts seed resolution to this project slug. Defaults to the
	// detected project. Also controls which store is used for the exploration.
	Project string `json:"project,omitempty"`
}

// ExploreResponse is the result of a graph exploration from a seed memory.
// Nodes are sorted by (distance ASC, accumulated_weight DESC) so the most
// directly and strongly connected memories appear first.
type ExploreResponse struct {
	// SeedID is the full UUID of the seed memory.
	SeedID string `json:"seed_id"`

	// SeedTitle is the title of the seed memory for display purposes.
	SeedTitle string `json:"seed_title"`

	// Nodes holds all discovered memories, excluding the seed itself.
	Nodes []ExploreNode `json:"nodes"`

	// TotalNodes is len(Nodes).
	TotalNodes int `json:"total_nodes"`

	// TokensUsed is the sum of TokenEstimate for all returned nodes, plus the
	// seed's token estimate.
	TokensUsed int `json:"tokens_used"`

	// MaxDepthReached is the maximum distance value among returned nodes.
	// Zero when no nodes were discovered.
	MaxDepthReached int `json:"max_depth_reached"`
}

// ExploreNode represents a memory discovered during graph exploration.
// It carries traversal metadata (distance, accumulated path weight, parent)
// in addition to the memory's own metadata.
type ExploreNode struct {
	// MemoryID is the full UUID of this memory.
	MemoryID string `json:"memory_id"`

	// ParentMemoryID is the UUID of the node that led to this one during the
	// BFS traversal. Empty for depth-1 nodes whose parent is the seed.
	// The CLI uses this to reconstruct the tree structure.
	ParentMemoryID string `json:"parent_memory_id,omitempty"`

	// Title is the memory's title.
	Title string `json:"title"`

	// TopicKey is the memory's topic_key, if set.
	TopicKey string `json:"topic_key,omitempty"`

	// Type is the memory's type (decision, discovery, etc.).
	Type MemoryType `json:"type"`

	// Distance is the number of hops from the seed (1 for direct neighbours).
	Distance int `json:"distance"`

	// AccumulatedWeight is the product of all relation weights along the best
	// path from the seed to this node. Always in (0.0, 1.0].
	AccumulatedWeight float64 `json:"accumulated_weight"`

	// RelationType is the type of the relation that connected this node to its
	// parent during the BFS traversal.
	RelationType RelationType `json:"relation_type"`

	// TokenEstimate is the rough token count for this memory's title + content,
	// computed as runeCount / 3.0. Used for budget tracking.
	TokenEstimate int `json:"token_estimate"`
}
