package model

import "errors"

// Domain-level sentinel errors for mneme. These are returned by service and store
// layers to communicate precise failure reasons to callers without leaking
// implementation details. Callers should compare with errors.Is().

// ErrNotFound is returned when a requested memory does not exist in the store.
// Distinct from a database error — it means the query succeeded but matched nothing.
var ErrNotFound = errors.New("memory not found")

// ErrInvalidType is returned when a MemoryType value is not one of the recognised
// constants. Used to reject unknown types before they reach the database.
var ErrInvalidType = errors.New("invalid memory type")

// ErrInvalidScope is returned when a Scope value is not one of the recognised
// constants. Used to reject unknown scopes before they reach the database.
var ErrInvalidScope = errors.New("invalid scope")

// ErrTitleRequired is returned when a SaveRequest arrives with an empty Title.
// The title is the primary searchable field; a memory without one is not useful.
var ErrTitleRequired = errors.New("title is required")

// ErrContentRequired is returned when a SaveRequest arrives with empty Content.
// Content is the body of knowledge; a memory without content has no value.
var ErrContentRequired = errors.New("content is required")

// ErrSummaryRequired is returned when a SessionEndRequest arrives with an empty
// Summary. Without a summary the session_summary Memory cannot be created.
var ErrSummaryRequired = errors.New("session summary is required")

// ErrQueryRequired is returned when a SearchRequest arrives with an empty Query.
// An empty query would return unfiltered results, which is almost never correct
// from an agent — callers that want a full list should use a dedicated list API.
var ErrQueryRequired = errors.New("search query is required")

// ErrEntityNotFound is returned when a requested entity does not exist in the store.
var ErrEntityNotFound = errors.New("entity not found")

// ErrRelationNotFound is returned when a requested relation does not exist in the store.
var ErrRelationNotFound = errors.New("relation not found")

// ErrInvalidEntityKind is returned when an EntityKind value is not one of the
// recognised constants. Used to reject unknown kinds before they reach the database.
var ErrInvalidEntityKind = errors.New("invalid entity kind")

// ErrInvalidRelationType is returned when a RelationType value is not one of the
// recognised constants. Used to reject unknown relation types before they reach
// the database.
var ErrInvalidRelationType = errors.New("invalid relation type")

// ErrInvalidBacklogStatus is returned when a BacklogStatus value is not recognised.
var ErrInvalidBacklogStatus = errors.New("invalid backlog status")

// ErrInvalidPriority is returned when a Priority value is not recognised.
var ErrInvalidPriority = errors.New("invalid priority")

// ErrInvalidSpecStatus is returned when a SpecStatus value is not recognised.
var ErrInvalidSpecStatus = errors.New("invalid spec status")

// ErrInvalidTransition is returned when a spec state transition is not allowed
// by the state machine.
var ErrInvalidTransition = errors.New("invalid spec status transition")

// ErrBacklogNotFound is returned when a backlog item does not exist.
var ErrBacklogNotFound = errors.New("backlog item not found")

// ErrSpecNotFound is returned when a spec does not exist.
var ErrSpecNotFound = errors.New("spec not found")

// ErrPushbackNotFound is returned when a pushback does not exist.
var ErrPushbackNotFound = errors.New("pushback not found")

// ErrBacklogNotRefined is returned when trying to promote a backlog item
// that has not been refined yet.
var ErrBacklogNotRefined = errors.New("backlog item must be refined before promotion")

// ErrQualityGateFailed is returned when a spec fails a quality gate check.
var ErrQualityGateFailed = errors.New("quality gate check failed")

// ErrAppliesToRequired is returned when saving a rule without any applies_to patterns.
// A rule without an applies_to list has no defined scope and cannot be matched
// by the rules engine (SPEC-R3). Use ["**"] to express a global rule.
var ErrAppliesToRequired = errors.New("applies_to is required for rules")

// ErrAppliesToForbidden is returned when a non-rule memory specifies applies_to.
// The applies_to field is only meaningful for TypeRule memories; setting it on
// other types is a client mistake that should be surfaced explicitly.
var ErrAppliesToForbidden = errors.New("applies_to is only valid for rules")

// ErrInvalidSeverity is returned when a severity value is not one of the
// recognised constants ("info", "warn", "block").
var ErrInvalidSeverity = errors.New("invalid severity")

// ErrEmptyPattern is returned when an applies_to entry is an empty string.
// Empty patterns are ambiguous and cannot be matched reliably by the rules engine.
var ErrEmptyPattern = errors.New("applies_to patterns must not be empty")

// ErrInvalidWeight is returned when a weight value is outside [0.0, 1.0] or is
// not a finite number (NaN, +Inf, -Inf). Validation is intentionally in the Go
// layer rather than as a SQL CHECK constraint because SQLite does not reject NaN
// in BETWEEN comparisons.
var ErrInvalidWeight = errors.New("weight must be a finite number in [0.0, 1.0]")

// ErrAmbiguousSeed is returned when a short UUID prefix supplied to mem_explore
// matches more than one memory. The caller must use the full UUID to avoid
// ambiguity.
var ErrAmbiguousSeed = errors.New("seed matches multiple memories; use the full UUID")
