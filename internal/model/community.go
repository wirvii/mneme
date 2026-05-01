// Package model community.go — domain types for persisted Louvain communities.
// These types are distinct from graph.Community (the in-memory algorithm output)
// and serve as the canonical representation stored in the communities table.
package model

import "time"

// Community represents a persisted community detected by the Louvain algorithm.
// It is the domain representation stored in the communities table. The type
// differs from graph.Community, which is the in-memory algorithm output with
// integer community IDs; those IDs have no stability across runs.
//
// Community identity is determined by MembershipHash (SHA-256 of sorted member
// entity IDs). Two communities are considered the same across detection runs if
// and only if they have the same membership hash (SPEC-020 D2).
type Community struct {
	// ID is the UUIDv7 primary key assigned at insert time. Stable across runs
	// for communities whose membership is unchanged.
	ID string `json:"id"`

	// Project is the project slug this community belongs to. May be empty for
	// global-scope communities.
	Project string `json:"project,omitempty"`

	// Scope identifies the memory store (project, global, org) that was used
	// during the detection run. Communities are never cross-scope.
	Scope Scope `json:"scope"`

	// MembershipHash is the SHA-256 hex digest of the sorted member entity IDs
	// joined by commas. Used for hash-match diff between consecutive detection
	// runs (SPEC-020 D3).
	MembershipHash string `json:"membership_hash"`

	// MemberCount is the number of entity members at the time of the last
	// detection run. Denormalized for cheap listing without a COUNT(*) JOIN.
	MemberCount int `json:"member_count"`

	// Modularity is the global Q value from the Louvain run that produced this
	// community. Per-community Q contribution may be added in a future spec
	// once LouvainResult exposes it (SPEC-020 open question 1).
	Modularity float64 `json:"modularity"`

	// Label is an optional human-readable name for this community. Always NULL
	// when created by this spec. SPEC-C3 (synthesis) will populate it with an
	// AI-generated label derived from the community's members and their memories.
	Label string `json:"label,omitempty"`

	// CreatedAt is the UTC time this community was first persisted.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is the UTC time this community was last refreshed by a detection
	// run. Even if membership is unchanged, UpdatedAt advances on every run.
	UpdatedAt time.Time `json:"updated_at"`

	// EntityIDs is populated on load when the caller requests members.
	// It is not stored in the communities table itself — it is loaded from the
	// community_members join table and attached here for convenience.
	EntityIDs []string `json:"entity_ids,omitempty"`
}

// DetectionResult summarises the outcome of a single community detection and
// persistence cycle. All counter fields are non-negative. A zero-value result
// is returned when detection is disabled or the project has too few memories.
type DetectionResult struct {
	// NewCommunities is the number of communities that were inserted because
	// their membership hash was not found in the existing set.
	NewCommunities int `json:"new_communities"`

	// UpdatedCommunities is the number of communities whose membership hash
	// matched an existing community; their metadata was refreshed in-place.
	UpdatedCommunities int `json:"updated_communities"`

	// DeletedCommunities is the number of previously-persisted communities that
	// were removed because their membership hash was absent from the new partition.
	DeletedCommunities int `json:"deleted_communities"`

	// TotalCommunities is the count of communities in the final persisted state
	// (NewCommunities + UpdatedCommunities).
	TotalCommunities int `json:"total_communities"`

	// ModularityFinal is the global Q value of the detected partition, as
	// returned by Louvain. 0.0 when detection was skipped.
	ModularityFinal float64 `json:"modularity_final"`

	// Duration is the wall-clock time taken by the full detection cycle
	// (graph build + Louvain + diff + persist).
	Duration time.Duration `json:"duration"`
}
