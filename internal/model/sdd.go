// Package model defines the core domain types for mneme.
// This file contains all SDD (Spec-Driven Development) engine types:
// backlog items, specs, state machine, history, and pushbacks.
// No external dependencies — this is the leaf package.
package model

import "time"

// --- BACKLOG ---

// BacklogStatus represents the lifecycle state of a backlog item.
type BacklogStatus string

const (
	// BacklogStatusRaw is the initial state for a newly added backlog item.
	BacklogStatusRaw BacklogStatus = "raw"

	// BacklogStatusRefined indicates the item has been detailed during a grill session.
	BacklogStatusRefined BacklogStatus = "refined"

	// BacklogStatusPromoted indicates the item has been converted to a spec.
	BacklogStatusPromoted BacklogStatus = "promoted"

	// BacklogStatusArchived indicates the item was intentionally discarded.
	BacklogStatusArchived BacklogStatus = "archived"
)

// validBacklogStatuses is the canonical set for validation.
var validBacklogStatuses = map[BacklogStatus]struct{}{
	BacklogStatusRaw:      {},
	BacklogStatusRefined:  {},
	BacklogStatusPromoted: {},
	BacklogStatusArchived: {},
}

// Valid reports whether the BacklogStatus is one of the recognised constants.
func (s BacklogStatus) Valid() bool {
	_, ok := validBacklogStatuses[s]
	return ok
}

// Priority represents the urgency level of a backlog item.
type Priority string

const (
	// PriorityCritical indicates a blocking or time-sensitive item.
	PriorityCritical Priority = "critical"

	// PriorityHigh indicates an important item to address soon.
	PriorityHigh Priority = "high"

	// PriorityMedium is the default priority for most items.
	PriorityMedium Priority = "medium"

	// PriorityLow indicates a nice-to-have with no urgency.
	PriorityLow Priority = "low"
)

var validPriorities = map[Priority]struct{}{
	PriorityCritical: {},
	PriorityHigh:     {},
	PriorityMedium:   {},
	PriorityLow:      {},
}

// Valid reports whether the Priority is one of the recognised constants.
func (p Priority) Valid() bool {
	_, ok := validPriorities[p]
	return ok
}

// Rank returns a numeric rank for sorting. Lower values represent higher priority,
// so lists sorted ascending by Rank display most-urgent items first.
func (p Priority) Rank() int {
	switch p {
	case PriorityCritical:
		return 0
	case PriorityHigh:
		return 1
	case PriorityMedium:
		return 2
	case PriorityLow:
		return 3
	default:
		return 99
	}
}

// BacklogItem is a unit of work in the backlog. It goes through
// raw -> refined -> promoted lifecycle before becoming a spec.
type BacklogItem struct {
	// ID is a sequential identifier like "BL-001".
	ID string `json:"id"`

	// Title is the short description of the idea.
	Title string `json:"title"`

	// Description is the detailed explanation. Optional for raw items,
	// expected after refinement.
	Description string `json:"description,omitempty"`

	// Status is the lifecycle state.
	Status BacklogStatus `json:"status"`

	// Priority indicates urgency. Defaults to medium.
	Priority Priority `json:"priority"`

	// Project is the project slug this item belongs to.
	Project string `json:"project"`

	// SpecID is set when the item is promoted to a spec.
	SpecID string `json:"spec_id,omitempty"`

	// ArchiveReason is set when the item is archived.
	ArchiveReason string `json:"archive_reason,omitempty"`

	// Position is the display order within same priority. Lower = first.
	Position int `json:"position"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// BacklogAddRequest is the input for creating a new backlog item.
type BacklogAddRequest struct {
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Priority    Priority `json:"priority,omitempty"`
	Project     string   `json:"project,omitempty"`
}

// BacklogListRequest filters backlog items for listing.
type BacklogListRequest struct {
	Status  BacklogStatus `json:"status,omitempty"`
	Project string        `json:"project,omitempty"`
}

// BacklogRefineRequest updates a backlog item during refinement.
type BacklogRefineRequest struct {
	ID         string `json:"id"`
	Refinement string `json:"refinement"`
}

// --- SPEC STATE MACHINE ---

// SpecStatus represents the lifecycle state of a spec.
type SpecStatus string

const (
	// SpecStatusDraft is the initial state when a spec is first created.
	SpecStatusDraft SpecStatus = "draft"

	// SpecStatusSpeccing indicates the spec is being written by the architect.
	SpecStatusSpeccing SpecStatus = "speccing"

	// SpecStatusNeedsGrill indicates the spec is blocked on unresolved questions.
	SpecStatusNeedsGrill SpecStatus = "needs_grill"

	// SpecStatusSpecced indicates the spec document is complete and approved.
	SpecStatusSpecced SpecStatus = "specced"

	// SpecStatusPlanning indicates the architect is planning the implementation.
	SpecStatusPlanning SpecStatus = "planning"

	// SpecStatusPlanned indicates the implementation plan is ready.
	SpecStatusPlanned SpecStatus = "planned"

	// SpecStatusImplementing indicates active development is in progress.
	SpecStatusImplementing SpecStatus = "implementing"

	// SpecStatusQA indicates the implementation is under quality assurance review.
	SpecStatusQA SpecStatus = "qa"

	// SpecStatusDone is the terminal state: the spec is fully delivered.
	SpecStatusDone SpecStatus = "done"
)

var validSpecStatuses = map[SpecStatus]struct{}{
	SpecStatusDraft:        {},
	SpecStatusSpeccing:     {},
	SpecStatusNeedsGrill:   {},
	SpecStatusSpecced:      {},
	SpecStatusPlanning:     {},
	SpecStatusPlanned:      {},
	SpecStatusImplementing: {},
	SpecStatusQA:           {},
	SpecStatusDone:         {},
}

// Valid reports whether the SpecStatus is one of the recognised constants.
func (s SpecStatus) Valid() bool {
	_, ok := validSpecStatuses[s]
	return ok
}

// IsFinal reports whether this status represents a terminal state.
// Terminal specs cannot be advanced further.
func (s SpecStatus) IsFinal() bool {
	return s == SpecStatusDone
}

// IsActive reports whether this status represents an in-progress state.
// Active means work is ongoing — neither the initial draft nor the final done.
func (s SpecStatus) IsActive() bool {
	return s != SpecStatusDone && s != SpecStatusDraft
}

// validTransitions defines the state machine. Each key maps to the set
// of valid target states. Any transition not in this map is rejected with
// ErrInvalidTransition. The machine is intentionally strict: callers must
// explicitly name the target state rather than relying on a "next" heuristic.
var validTransitions = map[SpecStatus]map[SpecStatus]struct{}{
	SpecStatusDraft: {
		SpecStatusSpeccing: {},
	},
	SpecStatusSpeccing: {
		SpecStatusSpecced:    {},
		SpecStatusNeedsGrill: {},
	},
	SpecStatusNeedsGrill: {
		SpecStatusSpeccing: {},
	},
	SpecStatusSpecced: {
		SpecStatusPlanning: {},
	},
	SpecStatusPlanning: {
		SpecStatusPlanned: {},
	},
	SpecStatusPlanned: {
		SpecStatusImplementing: {},
	},
	SpecStatusImplementing: {
		SpecStatusQA:         {},
		SpecStatusNeedsGrill: {},
	},
	SpecStatusQA: {
		SpecStatusDone:         {},
		SpecStatusImplementing: {},
		SpecStatusNeedsGrill:   {},
	},
}

// CanTransitionTo reports whether transitioning from the current status
// to target is a valid state machine move. Returns false for any unknown
// source status or when the target is not in the allowed set.
func (s SpecStatus) CanTransitionTo(target SpecStatus) bool {
	targets, ok := validTransitions[s]
	if !ok {
		return false
	}
	_, valid := targets[target]
	return valid
}

// Spec is the central entity of the SDD state machine. It tracks a feature
// through its entire lifecycle from draft to done.
type Spec struct {
	// ID is a sequential identifier like "SPEC-001".
	ID string `json:"id"`

	// Title is the human-readable name of the spec.
	Title string `json:"title"`

	// Status is the current lifecycle state.
	Status SpecStatus `json:"status"`

	// Project is the project slug this spec belongs to.
	Project string `json:"project"`

	// BacklogID links to the originating backlog item, if any.
	BacklogID string `json:"backlog_id,omitempty"`

	// AssignedAgents lists which agents are currently assigned.
	AssignedAgents []string `json:"assigned_agents,omitempty"`

	// FilesChanged tracks files modified during implementation.
	FilesChanged []string `json:"files_changed,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SpecHistory records a single state transition in a spec's lifecycle.
type SpecHistory struct {
	// ID is a UUIDv7 for this history entry.
	ID string `json:"id"`

	// SpecID references the parent spec.
	SpecID string `json:"spec_id"`

	// FromStatus is the state before the transition.
	FromStatus SpecStatus `json:"from_status"`

	// ToStatus is the state after the transition.
	ToStatus SpecStatus `json:"to_status"`

	// By identifies who triggered the transition (e.g., "orchestrator", "architect").
	By string `json:"by"`

	// Reason is an optional explanation for this transition.
	Reason string `json:"reason,omitempty"`

	// At is the timestamp of the transition.
	At time.Time `json:"at"`
}

// SpecPushback records a set of questions from an agent that block progress.
// A pushback causes the spec to enter needs_grill status until resolved.
type SpecPushback struct {
	// ID is a UUIDv7 for this pushback entry.
	ID string `json:"id"`

	// SpecID references the parent spec.
	SpecID string `json:"spec_id"`

	// FromAgent identifies the agent that raised the pushback.
	FromAgent string `json:"from_agent"`

	// Questions is the list of unresolved questions.
	Questions []string `json:"questions"`

	// Resolved is true when the pushback has been addressed.
	Resolved bool `json:"resolved"`

	// Resolution is the answer provided to resolve the pushback.
	Resolution string `json:"resolution,omitempty"`

	// CreatedAt is when the pushback was raised.
	CreatedAt time.Time `json:"created_at"`

	// ResolvedAt is when the pushback was resolved. Nil if unresolved.
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}

// SpecNewRequest is the input for creating a new spec.
type SpecNewRequest struct {
	Title     string `json:"title"`
	BacklogID string `json:"backlog_id,omitempty"`
	Project   string `json:"project,omitempty"`
}

// SpecAdvanceRequest is the input for advancing a spec to its next state.
type SpecAdvanceRequest struct {
	ID     string `json:"id"`
	By     string `json:"by"`
	Reason string `json:"reason,omitempty"`
}

// SpecPushbackRequest is the input for registering a pushback.
type SpecPushbackRequest struct {
	ID        string   `json:"id"`
	FromAgent string   `json:"from_agent"`
	Questions []string `json:"questions"`
}

// SpecResolveRequest is the input for resolving a pushback.
type SpecResolveRequest struct {
	ID         string `json:"id"`
	Resolution string `json:"resolution"`
}

// SpecListRequest filters specs for listing.
type SpecListRequest struct {
	Status  SpecStatus `json:"status,omitempty"`
	Project string     `json:"project,omitempty"`
}

// SpecStatusResponse is returned by spec_status with full context:
// the current spec state plus its complete history and all pushbacks.
type SpecStatusResponse struct {
	Spec      *Spec           `json:"spec"`
	History   []*SpecHistory  `json:"history"`
	Pushbacks []*SpecPushback `json:"pushbacks"`
}
