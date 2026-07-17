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

// ErrCrossScopeRelation is returned by mem_relate when the resolved source and
// target memories form a forbidden scope pair: a global or org source memory
// cannot create a relation that targets a project-scoped memory. This mirrors
// the SPEC-006 D1 invariant enforced for wikilinks and Hebbian co-access.
var ErrCrossScopeRelation = errors.New("cross-scope relation not allowed (global/org → project)")

// --- Lane sentinel errors (SPEC-035) ---

// ErrLaneRequired is returned when backlog_add or spec_new is called without
// a lane. Classification is always explicit — there is no default.
var ErrLaneRequired = errors.New("lane is required (trivial or standard)")

// ErrInvalidLane is returned when the lane value is not one of the recognised
// constants. Valid values are "trivial" and "standard".
var ErrInvalidLane = errors.New("invalid lane: must be trivial or standard")

// ErrScopeRequired is returned when lane=trivial is declared but no scope glob
// is provided. The auditor cannot verify boundary compliance without a scope.
var ErrScopeRequired = errors.New("scope is required for trivial lane")

// ErrLaneImmutable is returned when a caller attempts to change the lane after
// the spec has entered implementing status. Lane is immutable at that point.
var ErrLaneImmutable = errors.New("lane cannot be changed after implementing has started")

// ErrLaneMismatch is returned when a lane-specific operation is called on a
// spec whose lane does not match. For example, spec_quick requires trivial.
var ErrLaneMismatch = errors.New("operation is only valid for the other lane")

// ErrAuditFailed is returned when the deterministic post-implementation auditor
// detects one or more threshold violations. The caller should inspect the
// AuditResult breaches field for the list of individual failures.
var ErrAuditFailed = errors.New("lane audit failed: threshold violations detected")

// ErrReasonRequired is returned when an operation requires a non-empty reason
// and none was supplied. Used by lane_override and spec_reject, both of which
// are auditable decisions that must be documented.
var ErrReasonRequired = errors.New("reason is required")

// --- Skill sentinel errors (SPEC-037) ---

// ErrSkillNotFound is returned when a requested skill directory does not exist
// under the skills install directory. Distinct from a filesystem error — it
// means the lookup succeeded but matched nothing.
var ErrSkillNotFound = errors.New("skill not found")

// ErrSkillMalformed is returned when a SKILL.md file cannot be parsed or
// contains structurally invalid content. The caller should inspect the wrapped
// error for details about the specific failure.
var ErrSkillMalformed = errors.New("skill malformed")

// ErrSkillPinned is returned when a remove or overwrite operation is attempted
// on a skill whose SKILL.md has pinned:true. Pass --force to override.
// pinned solely protects overwrite/remove — it has no hook or capability coupling.
var ErrSkillPinned = errors.New("skill is pinned")

// ErrSkillNoValidation is returned when skills_validate is called for a skill
// that does not have a validation/run.sh script. This is informational — the
// absence of a validator is not treated as a failure.
var ErrSkillNoValidation = errors.New("skill has no validation script")

// --- Model sentinel errors (SPEC-038) ---

// ErrUnknownAgent is returned when a model set/reset operation references an
// agent name that is not in the set of bundled agents. Agents are derived from
// the embedded assets/agents/*.md files — the canonical source of truth.
var ErrUnknownAgent = errors.New("unknown agent")

// ErrInvalidModel is returned when a model string is empty. Any non-empty
// string is accepted as a valid model alias (unknown aliases produce a warning,
// not an error, so the field intentionally stays open-ended).
var ErrInvalidModel = errors.New("model must not be empty")

// --- Conflict sentinel errors (SPEC-039) ---

// ErrCLIUnavailable is returned when the Claude CLI binary is not found on
// PATH during a conflicts scan. The scan must not fall back to any metered API;
// callers should report the condition and skip judgment gracefully.
var ErrCLIUnavailable = errors.New("claude CLI not available")

// ErrInvalidRelation is returned when a conflict link operation specifies a
// relation type that is not one of the accepted values: supersedes,
// conflicts_with, or unrelated.
var ErrInvalidRelation = errors.New("invalid conflict relation: must be supersedes, conflicts_with, or unrelated")

// ErrCrossStoreRelation is returned when ConflictLink, ConflictUnlink, or
// persistVerdict are asked to relate a global-scoped memory and a
// project-scoped memory. The two memories live in separate SQLite databases
// and no single store can atomically own the relation row. Callers must
// resolve both IDs to the same store before creating a relation.
var ErrCrossStoreRelation = errors.New("cannot relate a global and a project memory; they live in separate stores")

// ErrUnknownSpecDocKind is returned when spec_doc_write is called with a
// SpecDocKind that SpecDocKind.Filename does not recognise. The kind enum is
// closed by design (SPEC-087 D3) — a caller can never invent a new
// filename.
var ErrUnknownSpecDocKind = errors.New("unknown spec doc kind")

// --- Profile sentinel errors (SPEC-091 §1) ---
//
// These mirror the internal/profile leaf's own sentinels (profile.ErrProfileExists,
// profile.ErrProfileNotFound, profile.ErrProfileNameMismatch,
// profile.ErrInvalidManifest, profile.ErrInvalidPin) — ProfileService translates
// between the two so cli/mcp only ever need to import internal/model for
// errors.Is comparisons, never internal/profile directly (the same posture
// SkillsService/ConflictsService already establish for their own leaves).

// ErrProfileExists is returned when `profile add` targets a name already
// present in the host-level store and --force was not passed.
var ErrProfileExists = errors.New("profile already installed")

// ErrProfileNotFound is returned when `profile update` targets a name that is
// not present in the host-level store.
var ErrProfileNotFound = errors.New("profile not installed")

// ErrProfileNameMismatch is returned when `profile add --name X` is given but
// the cloned repository's manifest declares a different name — the store
// directory must never disagree with the profile's own declared identity.
var ErrProfileNameMismatch = errors.New("requested profile name does not match manifest name")

// ErrInvalidProfile is returned when a profile manifest or pin fails
// required-field or safe-slug validation.
var ErrInvalidProfile = errors.New("invalid profile manifest or pin")

