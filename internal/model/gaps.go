package model

import "time"

// Gap represents an aggregated knowledge gap: a topic_key referenced via
// [[wikilinks]] in one or more memories that does not yet have a corresponding
// memory in the store. Gaps are derived from the unresolved_references table
// by grouping on target_topic_key.
type Gap struct {
	// TargetTopicKey is the topic_key that was referenced but has no memory.
	TargetTopicKey string `json:"target_topic_key"`

	// TotalMentions is the sum of mention_count across all source memories
	// that reference this topic_key. Higher values indicate more urgent gaps.
	TotalMentions int `json:"total_mentions"`

	// SourceCount is the number of distinct source memories that reference
	// this topic_key. Higher values indicate broader relevance.
	SourceCount int `json:"source_count"`

	// FirstSeenAt is the earliest first_seen_at across all references.
	FirstSeenAt time.Time `json:"first_seen_at"`

	// LastSeenAt is the most recent last_seen_at across all references.
	LastSeenAt time.Time `json:"last_seen_at"`

	// Samples is a short list of source memories that reference this gap.
	// At most 3 entries, ordered by mention_count descending. Empty when
	// include_samples is false or all source memories have been hard-deleted.
	Samples []GapSample `json:"samples,omitempty"`
}

// GapSample is a lightweight reference to a source memory that mentions an
// unresolved topic_key. It avoids embedding the full Memory struct to keep
// the Gap response compact — callers that need full content should mem_get by ID.
type GapSample struct {
	// MemoryID is the UUIDv7 of the source memory.
	MemoryID string `json:"memory_id"`

	// Title is the source memory's title.
	Title string `json:"title"`

	// TopicKey is the source memory's own topic_key, if any.
	TopicKey string `json:"topic_key,omitempty"`
}

// GapsRequest parameterises a Gaps query. The zero value is valid and produces
// project-scoped results with a default limit of 20 and all gaps included.
type GapsRequest struct {
	// Project restricts results to a specific project slug. When empty the
	// service uses the project associated with the MemoryService instance.
	Project string `json:"project,omitempty"`

	// Scope controls which stores to query: "project" (default), "global",
	// or "all" (both stores, results merged). Follows the pattern of
	// ListRulesOptions.Scope in service/memory.go.
	Scope string `json:"scope,omitempty"`

	// Limit caps the number of gaps returned. Defaults to 20, max 100.
	Limit int `json:"limit,omitempty"`

	// MinMentions filters out gaps with total_mentions below this threshold.
	// Defaults to 1 (show all). Useful for focusing on high-signal gaps.
	MinMentions int `json:"min_mentions,omitempty"`

	// IncludeSamples controls whether to load sample source memories for each
	// gap. Nil means default true. Explicit false skips the sample queries,
	// which is faster for aggregate-only use cases.
	//
	// Uses *bool so omitted (nil) is distinguishable from explicit false,
	// following the same pattern as SearchRequest.IncludeGraph.
	IncludeSamples *bool `json:"include_samples,omitempty"`
}

// GapsResponse is the envelope returned by the Gaps service method and the
// mem_gaps MCP tool.
type GapsResponse struct {
	// Gaps is the ordered list of knowledge gaps, sorted by total_mentions
	// descending then source_count descending.
	Gaps []Gap `json:"gaps"`

	// Total is the count of distinct gap topic_keys before limit was applied.
	Total int `json:"total"`

	// Project is the project slug these gaps belong to.
	Project string `json:"project"`
}

// KnowledgeGaps summarises unresolved wikilink references in the store. It is
// embedded in StatsResponse to give agents quick visibility of missing knowledge
// without a separate mem_gaps call.
type KnowledgeGaps struct {
	// Total is the count of distinct target_topic_key values with unresolved
	// references in the store.
	Total int `json:"total"`

	// Top is the top 5 gaps by total_mentions, each with up to 3 samples.
	// Empty when there are no unresolved references.
	Top []Gap `json:"top,omitempty"`
}
