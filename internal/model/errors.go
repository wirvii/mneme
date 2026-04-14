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
