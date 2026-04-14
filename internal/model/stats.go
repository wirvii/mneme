package model

import "time"

// StatsResponse carries aggregate statistics about a project's memory store.
// It is designed for the CLI `mneme stats` command and administrative tooling.
// Providing counts by type and scope helps users understand the composition of
// their memory and identify categories that are over- or under-represented.
type StatsResponse struct {
	// Project is the project slug these stats belong to, or "global" for the
	// global/org database.
	Project string `json:"project"`

	// TotalMemories is the count of all memories (active + superseded + forgotten),
	// excluding hard-deleted records.
	TotalMemories int `json:"total_memories"`

	// ByType breaks down the total by MemoryType for active memories.
	ByType map[MemoryType]int `json:"by_type"`

	// ByScope breaks down the total by Scope for active memories.
	ByScope map[Scope]int `json:"by_scope"`

	// Active is the count of memories that are neither superseded nor soft-deleted.
	Active int `json:"active"`

	// Superseded is the count of memories that have been replaced by newer ones.
	Superseded int `json:"superseded"`

	// Forgotten is the count of soft-deleted memories retained for history.
	Forgotten int `json:"forgotten"`

	// DBSizeBytes is the current on-disk size of the SQLite database file in bytes.
	// Helps users decide when to run vacuum or archive old memories.
	DBSizeBytes int64 `json:"db_size_bytes"`

	// OldestMemory is the creation timestamp of the oldest active memory.
	// Nil when there are no active memories.
	OldestMemory *time.Time `json:"oldest_memory,omitempty"`

	// NewestMemory is the creation timestamp of the most recently created active memory.
	// Nil when there are no active memories.
	NewestMemory *time.Time `json:"newest_memory,omitempty"`

	// AvgImportance is the mean importance score across all active memories.
	// A low average may indicate the decay subsystem is not being reset by access.
	AvgImportance float64 `json:"avg_importance"`

	// EmbeddingsCount is the number of memories that have a stored vector
	// embedding. Used to monitor backfill coverage and diagnose search quality.
	EmbeddingsCount int `json:"embeddings_count"`
}
