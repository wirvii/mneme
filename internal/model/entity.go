// Package model — entity.go defines types for the knowledge graph.
package model

import "time"

// EntityKind classifies entities in the knowledge graph. Each kind represents
// a category of named concept that memories can reference and relate to.
type EntityKind string

const (
	// KindModule represents a code module or package.
	KindModule EntityKind = "module"

	// KindService represents a deployed service or daemon.
	KindService EntityKind = "service"

	// KindLibrary represents an external dependency or library.
	KindLibrary EntityKind = "library"

	// KindConcept represents an abstract concept or domain idea.
	KindConcept EntityKind = "concept"

	// KindPerson represents a person such as a team member or contributor.
	KindPerson EntityKind = "person"

	// KindPattern represents a design or implementation pattern.
	KindPattern EntityKind = "pattern"

	// KindFile represents a source file path.
	KindFile EntityKind = "file"
)

// validEntityKinds is the canonical set of recognised entity kinds.
var validEntityKinds = map[EntityKind]struct{}{
	KindModule:  {},
	KindService: {},
	KindLibrary: {},
	KindConcept: {},
	KindPerson:  {},
	KindPattern: {},
	KindFile:    {},
}

// Valid reports whether the EntityKind is one of the recognised constants.
func (k EntityKind) Valid() bool {
	_, ok := validEntityKinds[k]
	return ok
}

// RelationType defines the kind of directed relationship between two entities.
// Relations are stored as directed edges in the knowledge graph.
type RelationType string

const (
	// RelDependsOn indicates the source entity depends on the target.
	RelDependsOn RelationType = "depends_on"

	// RelImplements indicates the source entity implements the target (e.g. an interface).
	RelImplements RelationType = "implements"

	// RelSupersedes indicates the source entity replaces or supersedes the target.
	RelSupersedes RelationType = "supersedes"

	// RelRelatedTo indicates a general bidirectional relationship.
	RelRelatedTo RelationType = "related_to"

	// RelPartOf indicates the source entity is a component of the target.
	RelPartOf RelationType = "part_of"

	// RelUses indicates the source entity uses or calls the target.
	RelUses RelationType = "uses"

	// RelConflictsWith indicates the source entity conflicts with the target.
	RelConflictsWith RelationType = "conflicts_with"
)

// validRelationTypes is the canonical set of recognised relation types.
var validRelationTypes = map[RelationType]struct{}{
	RelDependsOn:     {},
	RelImplements:    {},
	RelSupersedes:    {},
	RelRelatedTo:     {},
	RelPartOf:        {},
	RelUses:          {},
	RelConflictsWith: {},
}

// Valid reports whether the RelationType is one of the recognised constants.
func (r RelationType) Valid() bool {
	_, ok := validRelationTypes[r]
	return ok
}

// Entity is a node in the knowledge graph. Entities are named, typed concepts
// that memories reference. They are unique within a (name, project) pair so
// the same concept is not duplicated across a project's memories.
type Entity struct {
	// ID is a UUIDv7 — time-sortable and globally unique.
	ID string `json:"id"`

	// Name is the human-readable identifier for this entity (e.g. "auth-service").
	Name string `json:"name"`

	// Kind classifies what type of concept this entity represents.
	Kind EntityKind `json:"kind"`

	// Project is the normalised project slug this entity belongs to.
	// Empty for global entities that span all projects.
	Project string `json:"project,omitempty"`

	// Metadata is an optional JSON blob for storing additional attributes.
	Metadata string `json:"metadata,omitempty"`

	// CreatedAt is the wall-clock time when the entity was first created.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is the wall-clock time of the most recent update.
	UpdatedAt time.Time `json:"updated_at"`
}

// Relation is a directed edge between two entities in the knowledge graph.
// It records a typed, weighted relationship from a source entity to a target entity.
type Relation struct {
	// ID is a UUIDv7 — time-sortable and globally unique.
	ID string `json:"id"`

	// SourceID is the UUIDv7 of the entity the edge originates from.
	SourceID string `json:"source_id"`

	// TargetID is the UUIDv7 of the entity the edge points to.
	TargetID string `json:"target_id"`

	// Type is the kind of relationship.
	Type RelationType `json:"type"`

	// Weight is a 0.0–∞ strength of the relationship. Defaults to 1.0.
	Weight float64 `json:"weight"`

	// Metadata is an optional JSON blob for storing additional edge attributes.
	Metadata string `json:"metadata,omitempty"`

	// CreatedAt is the wall-clock time when the relation was first created.
	CreatedAt time.Time `json:"created_at"`
}

// RelateRequest is the input for creating a relationship between two named entities.
// Source and target entities are resolved by name and created if they do not exist.
type RelateRequest struct {
	// Source is the name of the originating entity. Required.
	Source string `json:"source"`

	// Target is the name of the destination entity. Required.
	Target string `json:"target"`

	// Relation is the type of relationship between source and target. Required.
	Relation RelationType `json:"relation"`

	// SourceKind is the entity kind used when creating the source entity.
	// Defaults to KindConcept when omitted and the entity does not exist yet.
	SourceKind EntityKind `json:"source_kind,omitempty"`

	// TargetKind is the entity kind used when creating the target entity.
	// Defaults to KindConcept when omitted and the entity does not exist yet.
	TargetKind EntityKind `json:"target_kind,omitempty"`

	// Project scopes the entity lookup to a specific project slug.
	Project string `json:"project,omitempty"`
}

// RelateResponse is the output after creating or verifying a relation.
type RelateResponse struct {
	// RelationID is the UUIDv7 of the created (or existing) relation.
	RelationID string `json:"relation_id"`

	// SourceID is the UUIDv7 of the source entity.
	SourceID string `json:"source_id"`

	// TargetID is the UUIDv7 of the target entity.
	TargetID string `json:"target_id"`

	// Created is true when a new relation was inserted, false when one already existed.
	Created bool `json:"created"`
}

// TimelineRequest is the input for retrieving memories around a point in time.
// It enables chronological navigation of the knowledge graph for a project.
type TimelineRequest struct {
	// Around is either a memory UUID or an ISO 8601 timestamp string.
	// When it is a UUID the memory's created_at timestamp is used as the centre.
	// Required.
	Around string `json:"around"`

	// Project restricts results to a specific project slug.
	Project string `json:"project,omitempty"`

	// Window is the total time range to search expressed as a duration string
	// (e.g. "7d", "24h", "30d"). Memories within ±Window/2 of the anchor are
	// returned. Defaults to "7d" when omitted.
	Window string `json:"window,omitempty"`

	// Limit caps the number of results returned. Defaults to 20 when zero.
	Limit int `json:"limit,omitempty"`
}
