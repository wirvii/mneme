// Package model defines the core domain types for mneme.
// This file contains all SDD (Spec-Driven Development) engine types:
// backlog items, specs, state machine, history, and pushbacks.
// No external dependencies — this is the leaf package.
package model

import "time"

// LaneAuditRecord is a structured record of a single lane auditor run.
// One row is inserted per run — both passes and failures — so lane_status
// can read the latest outcome without parsing spec_history text.
type LaneAuditRecord struct {
	// ID is the auto-incremented row identifier assigned by SQLite.
	ID int64 `json:"id"`

	// SpecID references the audited spec.
	SpecID string `json:"spec_id"`

	// Passed is true when all auditor checks passed for this run.
	Passed bool `json:"passed"`

	// FileCount is the number of files changed according to git diff --numstat.
	FileCount int `json:"file_count"`

	// LinesChanged is the sum of added and removed lines across all files.
	LinesChanged int `json:"lines_changed"`

	// Breaches is a newline-joined string of individual threshold violations.
	// Empty string when Passed is true.
	Breaches string `json:"breaches,omitempty"`

	// BaseSHA is the git ref used as the base for diffing (the actual SHA or
	// "(default)" when the auditor's merge-base logic was used).
	BaseSHA string `json:"base_sha,omitempty"`

	// CreatedAt is when this audit run was recorded.
	CreatedAt time.Time `json:"created_at"`
}

// LaneStatsResponse summarises lane compliance metrics for a project.
// All counts are scoped to the project passed to LaneStats.
type LaneStatsResponse struct {
	// TrivialCount is the total number of trivial-lane specs.
	TrivialCount int `json:"trivial_count"`

	// AuditFailCount is the number of trivial specs whose latest audit failed.
	AuditFailCount int `json:"audit_fail_count"`

	// AuditFailRate is AuditFailCount / TrivialCount when TrivialCount > 0.
	// Zero when there are no trivial specs.
	AuditFailRate float64 `json:"audit_fail_rate"`

	// OverrideCount is the number of trivial specs that were completed via
	// lane_override (detected from history reasons starting with "lane override:").
	OverrideCount int `json:"override_count"`

	// ReclassifyCount is the number of trivial specs reclassified to standard
	// (detected from history reasons containing "reclassified from trivial to standard").
	ReclassifyCount int `json:"reclassify_count"`
}

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

// Convención de acotado de los listados del SDD (SPEC-109 D4/D13). Los tres
// valores son los que YA existían en el repo, no inventados. Definidos aquí
// una sola vez porque tres números repartidos entre service y mcp no son una
// convención, son tres coincidencias.
const (
	// ListDefaultLimit es el default que aplica el frontend MCP cuando el
	// llamador omite limit. Es el default de mem_timeline (service/graph.go).
	ListDefaultLimit = 20

	// ListMaxLimit es el techo duro que aplica el service. Un limit mayor se
	// capa EN SILENCIO (D5), lo cual es no-lesivo SÓLO porque Total informa
	// del número real de coincidencias: sin ese dato, capar callado sería
	// otro dato falso que parece real. Las dos decisiones son inseparables.
	ListMaxLimit = 50

	// ListExcerptRunes es el largo del excerpt en RUNAS (nunca bytes).
	// Es el de makeTimelinePreview.
	ListExcerptRunes = 200
)

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

	// RefinementCount is how many refinements the item has accumulated
	// (SPEC-110 D12).
	//
	// EVERY read path that returns a BacklogItem MUST populate it. A path that
	// leaves it at zero while rows exist would be exactly the "false datum that
	// looks real" pathology SPEC-109 fixed. There are only two such paths
	// (GetBacklogItem, ListBacklogItems) and both derive their projection from
	// the same const, so the symmetry is structural rather than conventional.
	//
	// json name differs from the MCP list view's `refinements` (D18): inside
	// BacklogGetResponse the array is already called `refinements`, and a
	// nested integer with the same name would be confusing.
	RefinementCount int `json:"refinement_count"`

	// UUID is this item's own identity: a UUIDv7 minted once at creation
	// (SPEC-128 D1), immutable, never surfaced in human-readable output
	// (D9) — it exists so a memory that mentions "BL-194" can be resolved
	// correctly even on a machine whose own BL-194 names different work.
	// Never empty in practice (self-healing backfill, D7); omitempty only
	// covers the narrow window between a legacy row's migration and its
	// backfill.
	UUID string `json:"uuid,omitempty"`

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

	// Limit caps the number of items returned (SPEC-109 D5/D9).
	// <= 0 means no window (the CLI's path — full fidelity, never truncated).
	// > ListMaxLimit is silently capped to ListMaxLimit by the service: this
	// is safe only because Total always reports the true match count.
	Limit int `json:"limit,omitempty"`
}

// BacklogListResponse wraps a page of items with the REAL number of matches.
//
// Returned BY VALUE, not by pointer (D10): renderFullStatus
// (cli/status.go:117) discards BacklogList's error and then ranges over the
// result — a pattern that would become a nil-deref with a pointer. The zero
// value is a safe empty list.
type BacklogListResponse struct {
	Items []*BacklogItem `json:"items"`

	// Total is the number of matches BEFORE Limit was applied — the same
	// contract as SearchResponse.Total (model/search.go), which mem_timeline
	// used to violate (SPEC-109 D3/D18).
	Total int `json:"total"`
}

// BacklogRefineRequest updates a backlog item during refinement.
type BacklogRefineRequest struct {
	ID         string `json:"id"`
	Refinement string `json:"refinement"`

	// By is who appends the refinement. OPTIONAL, unlike the required `by` of
	// spec_advance/spec_reject/lane_override: backlog_refine is already in
	// production and called by every installed project's agents, so making it
	// mandatory would hard-fail, on the first call, the very flow this change
	// unblocks (D5).
	By string `json:"by,omitempty"`
}

// BacklogRefinement is one appended refinement of a backlog item (SPEC-110 D2).
//
// Refinements are rows, not text concatenated into BacklogItem.Description:
// allowing N refinements while still appending to a single column would CREATE
// the size problem the item's title wrongly claimed already existed (the largest
// description measured is 9,438 bytes because the old code accepted exactly one
// refinement; five would be ~45 KB).
//
// Ordering is by Seq, never by At (D21): time.RFC3339Nano is NOT
// lexicographically chronological — Format trims trailing zeros from the
// fractional second, so "...52.770018Z" < "...52.77Z" byte-for-byte even though
// the first denotes the LATER instant. QA rejected exactly that mistake in
// SPEC-109. At is informational only.
type BacklogRefinement struct {
	// ItemID is the backlog item this refinement belongs to.
	ItemID string `json:"item_id"`

	// Seq is the 1-based position within the item's refinements. Assigned by
	// the store inside a transaction (MAX(seq)+1), so it is gapless and
	// monotonic per item.
	Seq int `json:"seq"`

	// Body is the refinement text, verbatim.
	Body string `json:"body"`

	// By is who appended it (e.g. "orchestrator", "architect"). Optional:
	// empty means UNATTRIBUTED, which is an honest absence rather than an
	// invented value (D5). Same semantics and same column name as
	// SpecHistory.By — the sibling table of the same SDD engine.
	By string `json:"by,omitempty"`

	// At is the append timestamp. Informational — never a sort key (see above).
	At time.Time `json:"at"`
}

// BacklogGetResponse is what BacklogGet returns: the item plus ALL of its
// refinements (SPEC-110 D6/D7).
//
// No Total field, deliberately: with no window, len(Refinements) and
// Item.RefinementCount cannot differ, and a third number that always agrees is
// a future lie waiting for someone to add a window and forget to update it.
// Their agreement is asserted in tests instead of exposed on the wire.
//
// Returned BY VALUE, like BacklogListResponse (SPEC-109 D10): the zero value is
// a safe empty response instead of a nil-deref.
type BacklogGetResponse struct {
	Item        *BacklogItem         `json:"item"`
	Refinements []*BacklogRefinement `json:"refinements"`
}

// BacklogArchiveRequest is the input for archiving a backlog item. Reason is
// mandatory (SPEC-125 D1) — the service, not the CLI, is what enforces it.
type BacklogArchiveRequest struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// FrozenSpec names the spec an archive froze, and the state it was left in
// (SPEC-125 D5). Deliberately NOT a *Spec: what the caller gets back is a
// spec it can no longer act on, and handing over the full live-looking
// object would invite treating it as one.
//
// FrozenSpec is the INVERSE direction of SPEC-126's SpecFreeze: FrozenSpec
// answers "which spec did this archive just freeze" (from backlog_archive's
// response); SpecFreeze answers "what is freezing this spec" (from
// spec_status/spec_list). The two never appear in the same response — never
// confuse them because their names are similar.
type FrozenSpec struct {
	ID     string     `json:"id"`
	Title  string     `json:"title"`
	Status SpecStatus `json:"status"`
}

// BacklogArchiveResult is what an archive returns. FrozenSpec == nil means
// nothing was frozen — the item had no spec, and that is the common case.
//
// Returned BY VALUE, like BacklogListResponse/BacklogGetResponse (SPEC-109
// D10): the zero value is a safe empty result instead of a nil dereference
// for a caller that discards the error.
type BacklogArchiveResult struct {
	Item       *BacklogItem `json:"item"`
	FrozenSpec *FrozenSpec  `json:"frozen_spec,omitempty"`
}

// BacklogIndexEntry is the narrow projection of a backlog item that the
// freeze decision (SpecFreeze, below) needs: just enough to tell whether the
// item is archived and, if so, why. It exists so a spec listing can be
// decorated with the freeze without loading every item's full description —
// a grill ledger that can run to tens of KB per item (SPEC-126 DD4).
type BacklogIndexEntry struct {
	Status        BacklogStatus `json:"status"`
	ArchiveReason string        `json:"archive_reason,omitempty"`
}

// SpecFreezeState is the CLOSED vocabulary of why a spec can no longer
// change status (SPEC-126 DD2). There is deliberately NO "live" value: a
// spec that can still move carries no SpecFreeze at all, so the reading rule
// is the single unambiguous one SPEC-125 DD7 already established for
// BacklogArchiveResult.FrozenSpec — presence, not a value, means frozen.
type SpecFreezeState string

const (
	// SpecFreezeArchived means the originating backlog item was read and is
	// archived — SPEC-125 D4's freeze, computed by the single predicate
	// service.specFreeze from the same comparison loadMutableSpec makes.
	SpecFreezeArchived SpecFreezeState = "archived"

	// SpecFreezeMissing means the originating backlog item is not in the
	// database at all. The spec cannot move either — loadMutableSpec fails
	// closed with ErrBacklogNotFound (SPEC-125 DD5/AC25) — but for a
	// different reason and with a different remedy than an archived item, so
	// it is NOT collapsed into SpecFreezeArchived.
	SpecFreezeMissing SpecFreezeState = "missing"
)

// SpecFreeze reports that a spec can no longer change status, and why
// (SPEC-126 DD2). It is the INVERSE direction of FrozenSpec (see that type's
// godoc): FrozenSpec answers "which spec did this archive just freeze",
// SpecFreeze answers "what is freezing this spec". The two appear in
// different responses and must never be confused.
//
// Reading rule, single and without exception: presence of a non-nil
// SpecFreeze means this spec can no longer change status. It holds for both
// values — SpecFreezeArchived produces ErrSpecFrozen and SpecFreezeMissing
// produces ErrBacklogNotFound, both from loadMutableSpec, in all eight
// spec-mutating verbs.
type SpecFreeze struct {
	State     SpecFreezeState `json:"state"`
	BacklogID string          `json:"backlog_id"`
	// Reason is the archive reason recorded for that item. Empty is possible
	// and legitimate: archive_reason defaults to '' at the schema level and
	// only became mandatory in the service with SPEC-125 D1, so an older row
	// can carry none.
	Reason string `json:"reason,omitempty"`
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

	// SpecStatusDone is terminal by default — but, since SPEC-087 D6,
	// reversible: an explicit, reasoned spec_reject (never spec_advance,
	// which stays a one-way forward-only operation — see
	// nextForwardStatusForLane, which has no "done" key and so keeps
	// rejecting an advance attempt from done) sends it back to
	// implementing when a review after the fact uncovers a defect.
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
// Terminal specs cannot be advanced further via spec_advance (see
// nextForwardStatusForLane) — the state machine has no forward move out of
// done. Since SPEC-087 D6 this is "terminal by default", not absolute:
// spec_reject can still send a done spec back to implementing when a
// post-hoc review finds a defect. That is a deliberate, reasoned exception
// distinct from spec_advance, which remains permanently rejected from done.
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
	// SPEC-087 D6: spec_reject can send a done spec back to implementing —
	// the only way out of done. spec_advance stays impossible from done:
	// nextForwardStatusForLane has no "done" key, so SpecAdvance still fails
	// with ErrInvalidTransition before CanTransitionTo is ever consulted.
	SpecStatusDone: {
		SpecStatusImplementing: {},
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
	// SPEC-087 D6: mirrors the standard-lane row above — spec_reject can
	// send a trivial spec back to implementing from done too.
	SpecStatusDone: {
		SpecStatusImplementing: {},
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

	// BaseSHA is the full git commit SHA captured when the spec first entered
	// implementing status. Used by the lane auditor to produce a per-spec diff
	// (rather than a diff against the whole branch) so audits are accurate when
	// multiple specs are in flight on the same branch.
	BaseSHA string `json:"base_sha,omitempty"`

	// AssignedAgents lists which agents are currently assigned.
	AssignedAgents []string `json:"assigned_agents,omitempty"`

	// FilesChanged tracks files modified during implementation.
	FilesChanged []string `json:"files_changed,omitempty"`

	// UUID is this spec's own identity: a UUIDv7 minted once at creation
	// (SPEC-128 D1), immutable, never surfaced in human-readable output
	// (D9) — it exists so a memory that mentions "SPEC-125" can be
	// resolved correctly even on a machine whose own SPEC-125 names
	// different work. Never empty in practice (self-healing backfill, D7);
	// omitempty only covers the narrow window between a legacy row's
	// migration and its backfill.
	UUID string `json:"uuid,omitempty"`

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

// SpecRejectRequest sends a spec backward to implementing, recording a
// rejection reason. This is the canonical way to model a review that
// uncovers defects requiring further implementation work — whether caught
// during the normal review gate or after the fact, once a spec already
// reached done (SPEC-087 D6).
//
// Standard lane: qa → implementing, or done → implementing.
// Trivial lane:  audit → implementing, or done → implementing.
//
// Distinct from SpecPushback (which models ambiguity → needs_grill).
type SpecRejectRequest struct {
	// ID is the spec to reject.
	ID string `json:"id"`

	// Reason documents why the spec was rejected. Must be non-empty
	// (ErrReasonRequired) — rejections are auditable decisions.
	Reason string `json:"reason"`

	// By identifies who triggered the rejection (e.g. "qa-agent", "orchestrator").
	By string `json:"by"`
}

// LaneStatusResponse is returned by LaneStatus with lane classification details
// and the latest audit summary.
type LaneStatusResponse struct {
	Spec        *Spec         `json:"spec"`
	Lane        Lane          `json:"lane"`
	Scope       string        `json:"scope,omitempty"`
	LatestAudit *AuditSummary `json:"latest_audit,omitempty"`

	// RejectionCount is the number of times this spec was rejected back to
	// implementing from qa (standard) or audit (trivial). Derived from
	// spec_history entries where to_status=implementing and from_status is
	// qa or audit. No additional column — history is the source of truth.
	RejectionCount int `json:"rejection_count"`
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

	// Limit caps the number of specs returned (SPEC-109 D5/D9). Same
	// two-mode semantics as BacklogListRequest.Limit: <= 0 means no window,
	// > ListMaxLimit is silently capped by the service.
	Limit int `json:"limit,omitempty"`
}

// SpecListResponse is the SpecList equivalent of BacklogListResponse.
// Returned BY VALUE for the same nil-deref reason (D10). No excerpt field:
// model.Spec has no Description (D15/CF1) — there is no long text to excerpt.
type SpecListResponse struct {
	Specs []*Spec `json:"specs"`
	Total int     `json:"total"`

	// Frozen holds a SpecFreeze entry for each spec in Specs that can no
	// longer change status, keyed by spec ID (SPEC-126 DD6). Absent from the
	// JSON (nil map, omitempty) when none is frozen — a listing where
	// nothing is marked looks byte-identical to the response shape before
	// this spec. A separate map instead of a field inside each Spec: the
	// freeze is a derived fact, and burying it inside the entity would
	// invite caching it as part of the entity (DD1).
	Frozen map[string]SpecFreeze `json:"frozen,omitempty"`
}

// SpecStatusResponse is returned by spec_status with full context:
// the current spec state plus its complete history and all pushbacks.
type SpecStatusResponse struct {
	Spec      *Spec           `json:"spec"`
	History   []*SpecHistory  `json:"history"`
	Pushbacks []*SpecPushback `json:"pushbacks"`

	// Frozen is nil — and its JSON key absent (omitempty) — when the spec
	// can still change status. Non-nil names why it cannot (SPEC-126 DD6).
	Frozen *SpecFreeze `json:"frozen,omitempty"`
}

// --- SPEC DOCUMENTS (SPEC-087 D3) ---

// SpecDocKind identifies which entregable document a caller is writing via
// spec_doc_write. This is a closed, Go-authored enum: the caller never
// supplies a filename, only a kind — SpecDocKind.Filename maps it to the
// exact filename SpecDocWrite writes, so a subagent can hand mneme its
// deliverable (spec.md, plan.md, qa-report.md, changes.md) instead of
// copying it by hand into the workflow directory, without ever choosing its
// own filename.
type SpecDocKind string

const (
	// SpecDocKindSpec writes spec.md — the architect's specification.
	SpecDocKindSpec SpecDocKind = "spec"

	// SpecDocKindPlan writes plan.md — the architect's implementation plan.
	SpecDocKindPlan SpecDocKind = "plan"

	// SpecDocKindQAReport writes qa-report.md — the qa-tester's review report.
	SpecDocKindQAReport SpecDocKind = "qa-report"

	// SpecDocKindChanges writes changes.md — an implementer's record of
	// where it diverged from the spec.
	SpecDocKindChanges SpecDocKind = "changes"

	// SpecDocKindCriteria writes criteria.toml — the spec's executable
	// acceptance criteria (SPEC-117 S3 D1). The architect is read-only on
	// the repository (internal/subagents/permissions.go), so this is its
	// ONLY write channel for the closed-vocabulary criteria document,
	// exactly like spec.md/plan.md above. A fifth kind inherits
	// specDocPath's two defenses (id pattern, no directory escape)
	// unmodified — see internal/service/sdd.go.
	SpecDocKindCriteria SpecDocKind = "criteria"

	// SpecDocKindBudget writes budget.toml — the spec's presupuesto contra
	// el grafo (SPEC-118 S4 D2). The sixth kind, and the same reason as
	// SpecDocKindCriteria: the architect is read-only on the repository, so
	// spec_doc_write is its only write channel. Restricted to the
	// architect role specifically for a subagent caller (SPEC-118 D10,
	// internal/cli/hook.go's roleScopedDocKinds) — D10's cerrojo 2, closing
	// the hole cerrojo 1 (quality_ack denied to every subagent) alone does
	// not: an implementer could otherwise write its OWN original budget
	// with no [revision] to sign, and examine itself.
	SpecDocKindBudget SpecDocKind = "budget"
)

// specDocFilenames maps each closed SpecDocKind to the exact filename it
// writes within a spec's workflow directory. Mirrors the filenames
// inferSpecStatus (internal/service/init.go) already recognises.
var specDocFilenames = map[SpecDocKind]string{
	SpecDocKindSpec:     "spec.md",
	SpecDocKindPlan:     "plan.md",
	SpecDocKindQAReport: "qa-report.md",
	SpecDocKindChanges:  "changes.md",
	SpecDocKindCriteria: "criteria.toml",
	SpecDocKindBudget:   "budget.toml",
}

// Filename returns the filename k maps to, and whether k is a recognised
// SpecDocKind. An unrecognised kind must never reach a filesystem path —
// callers use this to reject it up front (ErrUnknownSpecDocKind).
func (k SpecDocKind) Filename() (string, bool) {
	name, ok := specDocFilenames[k]
	return name, ok
}

// SpecDocWriteRequest is the input for spec_doc_write: write content to the
// file kind maps to, inside id's workflow directory.
type SpecDocWriteRequest struct {
	// ID is the spec whose workflow directory the document is written under.
	ID string `json:"id"`

	// Kind selects the destination filename via SpecDocKind.Filename.
	Kind SpecDocKind `json:"kind"`

	// Content is the full document content, written verbatim.
	Content string `json:"content"`
}

// SpecDocWriteResponse is returned by SpecDocWrite.
type SpecDocWriteResponse struct {
	// Path is the absolute path written.
	Path string `json:"path"`

	// Bytes is len(content) as written.
	Bytes int `json:"bytes"`

	// Created is true when the file did not exist before this call.
	Created bool `json:"created"`
}
