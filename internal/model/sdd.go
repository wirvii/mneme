// Package model defines the core domain types for mneme.
// This file contains all SDD (Spec-Driven Development) engine types:
// backlog items, specs, state machine, history, and pushbacks.
// No external dependencies — this is the leaf package.
package model

import "time"

// --- LANE ---

// Lane controls the SDD workflow path for a spec. Trivial items take a
// shortened path (draft→rationale→implementing→audit→done), while standard
// items traverse the full SDD lifecycle. Classification is declared at
// creation time and never inferred automatically.
type Lane string

const (
	// LaneTrivial identifies changes that touch ≤3 files and ≤20 lines,
	// add no public symbols, and avoid SQL/migrations/cmd/** paths.
	LaneTrivial Lane = "trivial"

	// LaneStandard identifies all other changes that require the full
	// SDD cycle (speccing, planning, QA).
	LaneStandard Lane = "standard"
)

var validLanes = map[Lane]struct{}{
	LaneTrivial:  {},
	LaneStandard: {},
}

// Valid reports whether the Lane value is one of the recognised constants.
func (l Lane) Valid() bool {
	_, ok := validLanes[l]
	return ok
}

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

	// Lane determines which SDD workflow path this item follows.
	// Required at creation time; defaults to standard for legacy items.
	Lane Lane `json:"lane"`

	// Scope is a glob pattern declaring which files this item may touch.
	// Required when Lane is trivial; used by the post-implementation auditor.
	Scope string `json:"scope,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// BacklogAddRequest is the input for creating a new backlog item.
type BacklogAddRequest struct {
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Priority    Priority `json:"priority,omitempty"`
	Project     string   `json:"project,omitempty"`

	// Lane is required. Omitting it returns ErrLaneRequired.
	Lane Lane `json:"lane"`

	// Scope is required when Lane is trivial; the auditor uses it to verify
	// no files outside this glob were modified.
	Scope string `json:"scope,omitempty"`
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

	// SpecStatusRationale is the trivial-lane equivalent of speccing.
	// A 1–3 sentence justification is recorded via spec_quick before
	// the item moves directly to implementing.
	SpecStatusRationale SpecStatus = "rationale"

	// SpecStatusAudit is the trivial-lane equivalent of qa. The deterministic
	// auditor checks the actual diff against the declared scope and thresholds.
	SpecStatusAudit SpecStatus = "audit"
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
	SpecStatusRationale:    {},
	SpecStatusAudit:        {},
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

// validTransitionsStandard defines the state machine for standard-lane specs.
// Each key maps to the set of valid target states. Any transition not in this
// map is rejected with ErrInvalidTransition. The machine is intentionally
// strict: callers must explicitly name the target state.
var validTransitionsStandard = map[SpecStatus]map[SpecStatus]struct{}{
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

// validTransitionsTrivial defines the state machine for trivial-lane specs.
// The path is shortened: draft→rationale→implementing→audit→done.
// The speccing, specced, planning, planned, and qa states are not used.
var validTransitionsTrivial = map[SpecStatus]map[SpecStatus]struct{}{
	SpecStatusDraft: {
		SpecStatusRationale: {},
	},
	SpecStatusRationale: {
		SpecStatusImplementing: {},
	},
	SpecStatusImplementing: {
		SpecStatusAudit:      {},
		SpecStatusNeedsGrill: {},
	},
	SpecStatusAudit: {
		SpecStatusDone:         {},
		SpecStatusImplementing: {},
	},
	SpecStatusNeedsGrill: {
		SpecStatusRationale: {},
	},
}

// CanTransitionTo reports whether transitioning from the current status to
// target is a valid state machine move for the given lane. Returns false for
// any unknown source status or when the target is not in the allowed set.
// The lane parameter selects which transition table to consult — trivial items
// use a shortened path that skips speccing, planning, and qa.
func (s SpecStatus) CanTransitionTo(target SpecStatus, lane Lane) bool {
	transitions := validTransitionsStandard
	if lane == LaneTrivial {
		transitions = validTransitionsTrivial
	}
	targets, ok := transitions[s]
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

	// Lane determines which SDD workflow path this spec follows.
	Lane Lane `json:"lane"`

	// Scope is a glob pattern declaring which files this spec may touch.
	// Required when Lane is trivial; used by the post-implementation auditor.
	Scope string `json:"scope,omitempty"`

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

	// Lane is required. Omitting it returns ErrLaneRequired.
	Lane Lane `json:"lane"`

	// Scope is required when Lane is trivial.
	Scope string `json:"scope,omitempty"`
}

// SpecQuickRequest advances a trivial-lane spec from draft directly to
// implementing by recording a one-line rationale. Rejected for standard specs.
type SpecQuickRequest struct {
	// ID is the spec to advance (must be trivial lane, draft status).
	ID string `json:"id"`

	// Rationale is a 1–3 sentence justification for the trivial classification.
	Rationale string `json:"rationale"`

	// By identifies who triggered the advance.
	By string `json:"by"`
}

// LaneAuditRequest triggers the deterministic post-implementation auditor for a
// trivial-lane spec that is in audit status.
type LaneAuditRequest struct {
	// ID is the spec to audit.
	ID string `json:"id"`

	// BaseRef is the git ref to diff against. When empty the auditor uses
	// git merge-base HEAD <default-branch> as the base.
	BaseRef string `json:"base_ref,omitempty"`
}

// LaneReclassifyRequest reclassifies a spec's lane. Only trivial→standard is
// allowed; upgrading standard→trivial is forbidden to prevent abuse.
type LaneReclassifyRequest struct {
	// ID is the spec to reclassify.
	ID string `json:"id"`

	// Lane must be "standard" (only trivial→standard is supported).
	Lane Lane `json:"lane"`

	// Scope may be updated during reclassification.
	Scope string `json:"scope,omitempty"`

	// By identifies who triggered the reclassification.
	By string `json:"by"`
}

// LaneOverrideRequest overrides a failed audit and advances a trivial-lane spec
// from audit directly to done. Requires a reason to document the decision.
type LaneOverrideRequest struct {
	// ID is the spec to override.
	ID string `json:"id"`

	// Reason must be non-empty; it is persisted as a discovery memory.
	Reason string `json:"reason"`

	// By identifies who triggered the override.
	By string `json:"by"`
}

// LaneStatusResponse is returned by LaneStatus with lane classification details
// and the latest audit summary.
type LaneStatusResponse struct {
	Spec        *Spec         `json:"spec"`
	Lane        Lane          `json:"lane"`
	Scope       string        `json:"scope,omitempty"`
	LatestAudit *AuditSummary `json:"latest_audit,omitempty"`
}

// AuditSummary summarises the most recent auditor run for a spec.
type AuditSummary struct {
	// Passed is true when all auditor checks passed.
	Passed bool `json:"passed"`

	// Breaches lists the individual threshold violations that caused failure.
	Breaches []string `json:"breaches,omitempty"`

	// At is the timestamp of the audit run.
	At time.Time `json:"at"`
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
