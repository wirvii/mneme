// Package model — rebuild.go defines types for the graph rebuild operation.
package model

// RebuildRequest specifies parameters for the graph rebuild operation.
// Graph rebuild extracts entities from existing memories using 4 heuristics
// (topic_key, file paths, code symbols, wikilinks), links them to their
// memories, and creates co-occurrence related_to relations between memories
// that share >= MinShared entities.
type RebuildRequest struct {
	// Project is the project slug to rebuild. Empty uses the service's default.
	Project string

	// Scope restricts which stores to process: "project" (default), "global",
	// or "all" (both stores sequentially). Cross-scope relations are never
	// created regardless of this value.
	Scope string

	// MinShared is the minimum number of shared entities required to create
	// a related_to relation between two memories. Default: 2.
	MinShared int

	// MaxRelationsPerMemory caps the number of relations created per memory.
	// The top MaxRelationsPerMemory pairs by shared count are kept.
	// Default: 50.
	MaxRelationsPerMemory int

	// BatchSize is the number of memories processed per transaction.
	// Default: 500.
	BatchSize int

	// Force deletes all existing related_to relations scoped to the project
	// before rebuilding. Only related_to relations are removed — explicit
	// relations (depends_on, implements, supersedes, part_of, uses,
	// conflicts_with, references) are never touched.
	Force bool

	// DryRun performs the full extraction and pair-generation analysis
	// without writing any entities, links, or relations to the database.
	// The returned RebuildResult counts reflect what would have been written.
	DryRun bool

	// ProgressFn is called after each batch with the current progress.
	// phase is a human-readable label (e.g. "extraction", "relations").
	// current and total are the number of items processed so far and
	// the total expected. May be nil.
	ProgressFn func(phase string, current, total int)
}

// RebuildResult summarises the outcome of a graph rebuild operation.
type RebuildResult struct {
	// MemoriesScanned is the total number of memories processed.
	MemoriesScanned int

	// EntitiesExtracted is the total number of entity extractions attempted
	// across all memories (one per unique entity name per memory).
	EntitiesExtracted int

	// EntitiesCreated is the number of new entities inserted into the
	// entities table.
	EntitiesCreated int

	// EntitiesExisting is the number of entity resolutions that found an
	// already-existing row (FindOrCreateEntity returned existing).
	EntitiesExisting int

	// LinksCreated is the number of new memory_entities rows inserted.
	LinksCreated int

	// LinksExisting is the number of memory_entity link operations that
	// found an already-existing row (INSERT OR IGNORE was a no-op).
	LinksExisting int

	// RelationsCreated is the number of new related_to relations inserted.
	RelationsCreated int

	// RelationsExisting is the number of candidate pairs skipped because a
	// related_to relation already existed in either direction.
	RelationsExisting int

	// RelationsDeleted is the number of related_to relations removed by
	// --force before the rebuild. Zero when Force is false.
	RelationsDeleted int

	// RelationsSkippedCap is the number of candidate pairs skipped because
	// the source memory had already reached MaxRelationsPerMemory.
	RelationsSkippedCap int
}
