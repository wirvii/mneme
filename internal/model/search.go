package model

import "time"

// SearchRequest specifies what the agent wants to find. Query is required;
// all filters are optional and narrow the result set when provided.
type SearchRequest struct {
	// Query is the full-text search string. Required.
	Query string `json:"query"`

	// Project restricts results to a specific project slug.
	// When empty the search spans all accessible scopes.
	Project string `json:"project,omitempty"`

	// Scope filters by memory scope when non-nil.
	Scope *Scope `json:"scope,omitempty"`

	// Type filters by memory type when non-nil.
	Type *MemoryType `json:"type,omitempty"`

	// Limit caps the number of results returned. Zero means the service default.
	Limit int `json:"limit,omitempty"`

	// IncludeSuperseded includes memories that have been superseded by newer ones.
	// Normally these are hidden to reduce noise.
	IncludeSuperseded bool `json:"include_superseded,omitempty"`
}

// SearchResult wraps a Memory with retrieval-specific metadata that helps the
// agent understand why this memory was surfaced and how relevant it is.
type SearchResult struct {
	// Memory is the matched domain entity.
	*Memory

	// Preview is a short excerpt of the content with the matching terms
	// highlighted (plain text, no HTML). Useful for quick scanning.
	Preview string `json:"preview"`

	// RelevanceScore is the combined normalised score (0.0–1.0) blending BM25,
	// importance, recency, and access frequency.
	RelevanceScore float64 `json:"relevance_score"`

	// BM25Score is the raw FTS5 BM25 ranking before blending. Exposed so callers
	// can inspect the text-match component independently.
	BM25Score float64 `json:"bm25_score"`

	// VectorScore is the cosine similarity between the query embedding and the
	// memory's stored embedding. Zero when embeddings are disabled or the memory
	// has not been embedded yet.
	VectorScore float64 `json:"vector_score,omitempty"`
}

// SearchResponse is the envelope returned by mem_search. It includes pagination
// metadata so the agent can decide whether to request more results.
type SearchResponse struct {
	// Results is the ordered list of matches, best match first.
	Results []SearchResult `json:"results"`

	// Total is the total number of matches before Limit was applied.
	Total int `json:"total"`

	// Query echoes the original search query for logging and debugging.
	Query string `json:"query"`
}

// ContextRequest asks mneme to build a curated context package for an agent
// session. Budget controls token usage; Focus narrows retrieval to a topic.
// This is the primary way agents prime themselves at session start.
type ContextRequest struct {
	// Project identifies which project's memories to include.
	Project string `json:"project"`

	// Budget is the maximum token estimate the caller can accept.
	// Zero means the service default (typically 4000 tokens).
	Budget int `json:"budget,omitempty"`

	// Focus is an optional topic or question that biases memory selection
	// toward the most relevant memories for the current task.
	Focus string `json:"focus,omitempty"`
}

// ContextResponse is the curated memory bundle returned by mem_context.
// It is designed to be injected directly into an agent's context window.
type ContextResponse struct {
	// Project is the project slug for which context was built.
	Project string `json:"project"`

	// Memories is the ordered set of memories selected within Budget.
	Memories []Memory `json:"memories"`

	// TokenEstimate is the estimated token count of all returned memories.
	TokenEstimate int `json:"token_estimate"`

	// TotalAvailable is the total number of active memories for this project,
	// before budget filtering. Tells the agent how much was left out.
	TotalAvailable int `json:"total_available"`

	// Included is the count of memories actually returned.
	Included int `json:"included"`

	// LastSession is the most recent session summary for this project, nil if none.
	// Helps the agent quickly understand what was happening last time.
	LastSession *SessionSummary `json:"last_session,omitempty"`
}

// SessionSummary is a lightweight view of the last session's summary memory.
// It avoids embedding the full Memory struct in ContextResponse when only a
// brief "what happened last time" is needed.
type SessionSummary struct {
	// ID is the UUIDv7 of the underlying session_summary Memory.
	ID string `json:"id"`

	// Summary is the full content of the session summary memory.
	Summary string `json:"summary"`

	// EndedAt is when the session ended, nil if the session is still active.
	EndedAt *time.Time `json:"ended_at,omitempty"`
}
