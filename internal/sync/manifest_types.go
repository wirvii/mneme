package sync

import (
	"time"

	"github.com/juanftp/mneme/internal/model"
)

// ManifestVersion is the only manifest specification version this implementation
// produces and accepts. Any archive with a different version string must be
// rejected rather than parsed with best-effort semantics (D5, SPEC-026).
const ManifestVersion = "1.0"

// ManifestRoot is the root document embedded as manifest.json inside a
// .manifest.tar.gz archive. It carries all four data types (memories, entities,
// relations, sessions) together with provenance metadata so consumers can verify
// compatibility before processing.
//
// JSON field names use snake_case to match the existing model types and the
// JSONL export format (D4, SPEC-026).
type ManifestRoot struct {
	// Version is the Memory Manifest specification version, always "1.0" for
	// archives produced by this implementation. Consumers MUST reject archives
	// with an unknown version rather than attempting partial parsing.
	Version string `json:"version"`

	// ExportedAt is when the archive was created, formatted as RFC 3339.
	ExportedAt string `json:"exported_at"`

	// Producer identifies the tool and version that created this archive.
	Producer ManifestProducer `json:"producer"`

	// Project is the project slug all records in this archive belong to.
	Project string `json:"project"`

	// Scope is the memory scope of the exported data ("project", "global", "org").
	// Defaults to "project" when absent for backwards compatibility.
	Scope string `json:"scope,omitempty"`

	// Memories holds every exported memory record.
	Memories []*model.Memory `json:"memories"`

	// Entities holds the knowledge-graph nodes. Empty when the project has no
	// entities — callers must treat nil and empty slice as equivalent.
	Entities []*model.Entity `json:"entities,omitempty"`

	// Relations holds the knowledge-graph edges. Empty when there are no relations.
	Relations []*model.Relation `json:"relations,omitempty"`

	// Sessions holds agent working sessions. Empty when no sessions exist.
	Sessions []*model.Session `json:"sessions,omitempty"`

	// Stats provides summary counts captured at export time. Informational only;
	// consumers MUST NOT rely on these for correctness — count the arrays instead.
	Stats *ManifestStats `json:"stats,omitempty"`
}

// ManifestProducer identifies the tool that created a manifest archive.
type ManifestProducer struct {
	// Name is the tool name, e.g. "mneme".
	Name string `json:"name"`

	// Version is the semantic version of the producer, e.g. "1.0.0".
	Version string `json:"version"`
}

// ManifestStats captures aggregate counts at export time. These are
// informational only and must not be treated as authoritative by consumers.
type ManifestStats struct {
	// MemoryCount is the number of memory records in the archive.
	MemoryCount int `json:"memory_count"`

	// EntityCount is the number of entity records in the archive.
	EntityCount int `json:"entity_count"`

	// RelationCount is the number of relation records in the archive.
	RelationCount int `json:"relation_count"`

	// SessionCount is the number of session records in the archive.
	SessionCount int `json:"session_count"`
}

// ManifestExportResult summarises a completed manifest export operation.
type ManifestExportResult struct {
	// Project is the project slug that was exported.
	Project string `json:"project"`

	// MemoryCount is the number of memory records written.
	MemoryCount int `json:"memory_count"`

	// EntityCount is the number of entity records written.
	EntityCount int `json:"entity_count"`

	// RelationCount is the number of relation records written.
	RelationCount int `json:"relation_count"`

	// SessionCount is the number of session records written.
	SessionCount int `json:"session_count"`

	// ExportedAt is the RFC 3339 timestamp of the export.
	ExportedAt string `json:"exported_at"`
}

// ManifestImportResult summarises a completed manifest import operation.
type ManifestImportResult struct {
	// MemoriesCreated is the count of memory records that did not exist and were inserted.
	MemoriesCreated int `json:"memories_created"`

	// MemoriesUpdated is the count of memories that existed by TopicKey and were updated.
	MemoriesUpdated int `json:"memories_updated"`

	// MemoriesSkipped is the count of memories that already existed by ID and were left unchanged.
	MemoriesSkipped int `json:"memories_skipped"`

	// EntitiesCreated is the count of entity records inserted.
	EntitiesCreated int `json:"entities_created"`

	// EntitiesSkipped is the count of entity records that already existed.
	EntitiesSkipped int `json:"entities_skipped"`

	// RelationsCreated is the count of relation records inserted.
	RelationsCreated int `json:"relations_created"`

	// RelationsSkipped is the count of relation records that already existed or
	// had orphan references.
	RelationsSkipped int `json:"relations_skipped"`

	// SessionsCreated is the count of session records inserted.
	SessionsCreated int `json:"sessions_created"`

	// SessionsSkipped is the count of session records that already existed.
	SessionsSkipped int `json:"sessions_skipped"`
}

// manifestFilename is the path of the root document inside the tar archive.
// It MUST be the first entry in the tar stream (D7.7, SPEC-026).
const manifestFilename = "manifest.json"

// manifestTarGzExtension is the file extension used for manifest archives.
// Auto-detection on import relies on this suffix.
const manifestTarGzExtension = ".manifest.tar.gz"

// exportedAtString formats t as RFC 3339 UTC for use in manifest fields.
func exportedAtString(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
