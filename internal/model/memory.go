// Package model defines the core domain types for mneme.
// This is the leaf package — it MUST NOT import any internal or external packages
// beyond the Go standard library. Every other package in mneme depends on model,
// so keeping it dependency-free ensures clean architecture boundaries.
package model

import (
	"strings"
	"time"
)

// MemoryType classifies the nature of a memory so retrieval, scoring, and decay
// can be tuned per category. The type is stored as a string in SQLite so it is
// human-readable in direct DB queries.
type MemoryType string

const (
	// TypeDecision records an architectural or technical decision and its rationale.
	// High importance — decisions are expensive to rediscover.
	TypeDecision MemoryType = "decision"

	// TypeDiscovery captures something learned about a codebase, API, or tool
	// that wasn't obvious. Useful for avoiding repeated research.
	TypeDiscovery MemoryType = "discovery"

	// TypeBugfix documents a bug and its fix so the same mistake isn't made twice.
	TypeBugfix MemoryType = "bugfix"

	// TypePattern records a recurring design or implementation pattern used in the project.
	TypePattern MemoryType = "pattern"

	// TypePreference stores personal or team preferences that guide style choices.
	TypePreference MemoryType = "preference"

	// TypeConvention documents naming, formatting, or structural conventions enforced
	// in the codebase or team.
	TypeConvention MemoryType = "convention"

	// TypeArchitecture describes the high-level structure, component relationships,
	// and infrastructure decisions of a project.
	TypeArchitecture MemoryType = "architecture"

	// TypeConfig records configuration values, environment-specific settings, or
	// service endpoints that the agent frequently needs.
	TypeConfig MemoryType = "config"

	// TypeSessionSummary is a synthetic memory created at session end to capture
	// what was accomplished. It decays faster than other types.
	TypeSessionSummary MemoryType = "session_summary"

	// TypeRule marks a memory as a project or global rule — a binding constraint
	// that agents must respect. Rules differ from conventions in two ways:
	// (1) they carry applies_to patterns and a severity level, and (2) they
	// are immune to decay (decay_rate=0) so they remain active until
	// explicitly revoked via mem_forget or mem_update.
	TypeRule MemoryType = "rule"

	// TypeSynthesis marks a memory as an auto-generated community summary.
	// Synthesis memories are produced by the consolidation pipeline after
	// community detection (SPEC-021). They are excluded from community detection
	// seeds (to prevent synthesis-of-synthesis loops) and from Hebbian co-access
	// tracking (to prevent graph inflation). Decay rate is 0 — synthesis memories
	// are regenerated each consolidation cycle and never stale.
	TypeSynthesis MemoryType = "synthesis"
)

// validMemoryTypes is the canonical set of known types, used by Valid() and
// AllMemoryTypes(). Adding a new type here is the single point of change.
var validMemoryTypes = map[MemoryType]struct{}{
	TypeDecision:       {},
	TypeDiscovery:      {},
	TypeBugfix:         {},
	TypePattern:        {},
	TypePreference:     {},
	TypeConvention:     {},
	TypeArchitecture:   {},
	TypeConfig:         {},
	TypeSessionSummary: {},
	TypeRule:           {},
	TypeSynthesis:      {},
}

// Valid reports whether the MemoryType is one of the recognised constants.
// Use this to validate user or agent input before persisting.
func (t MemoryType) Valid() bool {
	_, ok := validMemoryTypes[t]
	return ok
}

// AllMemoryTypes returns every defined MemoryType in a stable slice.
// Useful for building selection UIs, generating documentation, or iterating
// over scoring tables without hard-coding the list elsewhere.
func AllMemoryTypes() []MemoryType {
	return []MemoryType{
		TypeDecision,
		TypeDiscovery,
		TypeBugfix,
		TypePattern,
		TypePreference,
		TypeConvention,
		TypeArchitecture,
		TypeConfig,
		TypeSessionSummary,
		TypeRule,
		TypeSynthesis,
	}
}

// Severity controls how strictly a rule is enforced. It is stored as a
// string in SQLite and validated before persistence.
type Severity string

const (
	// SeverityInfo means the rule is advisory — the agent should consider
	// it but is not prevented from acting.
	SeverityInfo Severity = "info"

	// SeverityWarn means the agent receives an explicit warning before
	// the action proceeds. The hook emits the warning but does not block.
	SeverityWarn Severity = "warn"

	// SeverityBlock means the hook rejects the action entirely. The agent
	// must find an alternative approach or request an exception.
	SeverityBlock Severity = "block"
)

var validSeverities = map[Severity]struct{}{
	SeverityInfo:  {},
	SeverityWarn:  {},
	SeverityBlock: {},
}

// Valid reports whether the Severity is one of the recognised constants.
// An empty string is not a valid severity — callers should set a default
// before calling Valid when processing rule SaveRequests.
func (s Severity) Valid() bool {
	_, ok := validSeverities[s]
	return ok
}

// Scope controls which database and query context a memory belongs to.
// Scope determines both storage location (global.db vs project.db) and
// retrieval visibility (a project query includes project + global memories).
type Scope string

const (
	// ScopeGlobal memories apply across all projects and organisations.
	// Stored in ~/.mneme/global.db. Examples: personal coding preferences,
	// language conventions, universal patterns.
	ScopeGlobal Scope = "global"

	// ScopeOrg memories apply across all projects within an organisation.
	// Stored in ~/.mneme/global.db under the org namespace. Examples: team
	// conventions, shared tooling preferences.
	ScopeOrg Scope = "org"

	// ScopeProject memories are specific to a single project slug.
	// Stored in ~/.mneme/projects/{slug}.db. Examples: architecture decisions,
	// project-specific bugfixes, local config values.
	ScopeProject Scope = "project"
)

// validScopes mirrors the pattern of validMemoryTypes — single authoritative set.
var validScopes = map[Scope]struct{}{
	ScopeGlobal:  {},
	ScopeOrg:     {},
	ScopeProject: {},
}

// Valid reports whether the Scope is one of the recognised constants.
func (s Scope) Valid() bool {
	_, ok := validScopes[s]
	return ok
}

// Memory is the central domain entity. It represents a single unit of knowledge
// stored by an agent. Fields mirror the SQLite schema exactly so that mapping
// between the two is transparent and error-free.
type Memory struct {
	// ID is a UUIDv7 — time-sortable and globally unique without coordination.
	ID string `json:"id"`

	// Type classifies the memory for scoring and retrieval tuning.
	Type MemoryType `json:"type"`

	// Scope determines storage location and query visibility.
	Scope Scope `json:"scope"`

	// Title is a short, human-readable summary. It is indexed in FTS5.
	Title string `json:"title"`

	// Content holds the full structured knowledge, typically Markdown.
	Content string `json:"content"`

	// TopicKey is a stable, dot-delimited identifier for deterministic upserts
	// (e.g. "architecture/auth-model"). When provided, saving a memory with the
	// same topic_key+project+scope updates the existing record instead of
	// creating a duplicate.
	TopicKey string `json:"topic_key,omitempty"`

	// Project is the normalised project slug. NULL/empty for global and org memories.
	Project string `json:"project,omitempty"`

	// SessionID links the memory to the agent session that created it.
	SessionID string `json:"session_id,omitempty"`

	// CreatedBy identifies the agent or actor that saved this memory
	// (e.g. "claude-code", "user").
	CreatedBy string `json:"created_by,omitempty"`

	// CreatedAt is the wall-clock time when the memory was first saved.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is the wall-clock time of the most recent revision.
	UpdatedAt time.Time `json:"updated_at"`

	// Importance is a 0.0–1.0 score. Higher means the memory is surfaced more
	// aggressively in context building. Decays over time at DecayRate.
	Importance float64 `json:"importance"`

	// Confidence is a 0.0–1.0 score representing how certain the agent is that
	// this memory is correct and current.
	Confidence float64 `json:"confidence"`

	// AccessCount tracks how many times this memory has been retrieved.
	// High-access memories resist decay — they are clearly useful.
	AccessCount int `json:"access_count"`

	// LastAccessed is the timestamp of the most recent retrieval, nil if never accessed.
	LastAccessed *time.Time `json:"last_accessed,omitempty"`

	// DecayRate is the per-day multiplier applied to Importance.
	// A rate of 0.01 means Importance loses ~1% per day when not accessed.
	DecayRate float64 `json:"decay_rate"`

	// RevisionCount is incremented on each update, providing a lightweight
	// version history signal.
	RevisionCount int `json:"revision_count"`

	// SupersededBy holds the ID of a newer memory that replaces this one.
	// Non-nil means this memory is outdated but kept for audit purposes.
	SupersededBy string `json:"superseded_by,omitempty"`

	// DeletedAt marks soft-deleted memories. Soft deletion preserves history
	// and allows the decay/consolidation subsystem to reason about past knowledge.
	DeletedAt *time.Time `json:"deleted_at,omitempty"`

	// Files is the list of source file paths this memory is related to.
	// Enables "show me memories about this file" queries.
	Files []string `json:"files,omitempty"`

	// AppliesTo holds the list of patterns that determine when this rule is
	// relevant. Patterns can be file path globs (e.g. "internal/**/*.go"),
	// tool selectors (e.g. "tool:Edit"), combined selectors
	// ("tool:Edit+internal/**/*.go"), negations ("!docs/**"), or the global
	// wildcard "**". Only meaningful when Type is TypeRule; for all other
	// types this slice must be nil or empty.
	// Stored in SQLite as a JSON-encoded TEXT column.
	AppliesTo []string `json:"applies_to,omitempty"`

	// Severity indicates how strictly this rule should be enforced.
	// Valid values are "info" (advisory), "warn" (explicit warning), and
	// "block" (reject the action). Only meaningful when Type is TypeRule.
	// Stored as a TEXT column in SQLite with a CHECK constraint.
	Severity Severity `json:"severity,omitempty"`

	// Shared is the team-memory sharing level (SPEC-053/SPEC-061), a 2-level
	// flag layered on top of ScopeProject — it does not introduce a new scope:
	//   0 = local — never materialized to the shared git vault (default).
	//   1 = auto-shared — durable memory types, when team-memory is active.
	//   2 = team-curated — explicitly promoted (mneme promote).
	// Stored as an INTEGER column with a CHECK constraint restricting it to
	// {0,1,2}. Defaults to 0 so memory saved before team-memory is enabled (or
	// with team-memory never enabled) behaves exactly as before this field
	// existed.
	Shared int `json:"shared,omitempty"`

	// Author is the human git identity ("Name <email>") that authored this
	// memory, distinct from CreatedBy (the saving agent, e.g. "claude-code").
	// Empty until the write-through materialisation path (SS-B) populates it
	// from the local git config. Round-trips through the vault frontmatter so
	// peers preserve the original author on import.
	Author string `json:"author,omitempty"`

	// Source records the provenance of a memory materialized by a profile
	// activation (SPEC-092), formatted "profile:<name>". Empty means
	// hand-authored — the default for every memory saved before this field
	// existed and for every memory the public mem_save path creates. Source
	// is the switch's deletion key (MemoryStore.HardDeleteBySource) and, in a
	// later spec, the exclusion key for the team-memory vault: a profile's
	// rules are derived/regenerable (re-materialized by re-activating the
	// profile) and must never be confused with a human's durable knowledge.
	Source string `json:"source,omitempty"`
}

// ProfileSourcePrefix marks the provenance of a memory materialized by a
// profile activation (SPEC-092 §2's SaveProfileRule stamps the full value
// "profile:<name>"). It is the single string every layer that stamps or
// reads profile provenance must reference — see IsProfileSource, the one
// predicate the team-memory frontier (SPEC-094 §4) uses to exclude
// profile-injected memories from the shared vault, both on write (never
// materialize) and on read (never resurrect an orphaned note).
const ProfileSourcePrefix = "profile:"

// IsProfileSource reports whether source was stamped by a profile
// activation, i.e. it starts with ProfileSourcePrefix. "" — the default for
// every hand-authored memory and for every memory the public mem_save path
// creates — always reports false, so hand-authored knowledge is never
// affected by the guards built on top of this predicate (SPEC-071's
// share-by-default stays intact). Having a single predicate, rather than an
// inline strings.HasPrefix at each call site, is what keeps the switch's
// deletion key (MemoryStore.HardDeleteBySource) and the vault-exclusion
// guards from drifting apart if the prefix ever changes.
func IsProfileSource(source string) bool {
	return strings.HasPrefix(source, ProfileSourcePrefix)
}

// SaveRequest carries the agent's intent to persist a memory. Title and Content
// are required; all other fields are optional and receive sensible defaults in
// the service layer. Using a dedicated request type decouples the transport
// format from the domain entity.
type SaveRequest struct {
	// Title is required. It is the searchable summary of the memory.
	Title string `json:"title"`

	// Content is required. Full knowledge body, typically structured Markdown.
	Content string `json:"content"`

	// Type defaults to TypeDiscovery when omitted.
	Type MemoryType `json:"type,omitempty"`

	// Scope defaults to ScopeProject when omitted.
	Scope Scope `json:"scope,omitempty"`

	// TopicKey enables deterministic upserts. When set and a memory with the
	// same key exists in the same scope+project, the service updates rather than inserts.
	TopicKey string `json:"topic_key,omitempty"`

	// Project identifies the target project. The service resolves this from the
	// detected project when omitted.
	Project string `json:"project,omitempty"`

	// SessionID links this memory to the current agent session.
	SessionID string `json:"session_id,omitempty"`

	// CreatedBy identifies the saving agent.
	CreatedBy string `json:"created_by,omitempty"`

	// Files are source file paths associated with this memory.
	Files []string `json:"files,omitempty"`

	// Importance is a pointer so the service can distinguish "not provided"
	// (use type-based default) from "explicitly set to 0.0".
	Importance *float64 `json:"importance,omitempty"`

	// AppliesTo specifies the patterns this rule applies to. Required when
	// Type is TypeRule; ignored for all other types.
	AppliesTo []string `json:"applies_to,omitempty"`

	// Severity specifies the enforcement level. Required when Type is
	// TypeRule; ignored for all other types. Defaults to SeverityWarn
	// when omitted for a rule.
	Severity Severity `json:"severity,omitempty"`

	// Shared is a pointer so the service can distinguish "not provided" (bake
	// a type-based default when team-memory is active, resolved in SS-B) from
	// "explicitly set". A nil value together with team-memory inactive always
	// resolves to 0 (local, not shared) — the behaviour before this field
	// existed.
	Shared *int `json:"shared,omitempty"`

	// Source is the profile provenance stamp (SPEC-092), formatted
	// "profile:<name>". It is deliberately json:"-": the public mem_save tool
	// unmarshals its raw JSON arguments directly into a SaveRequest, so any
	// field with a JSON name could be spoofed by a caller that includes it in
	// the request payload — json:"-" makes that structurally impossible
	// rather than relying on the tool schema (documentation only, never
	// enforced) to keep an agent from setting it. The only writer is
	// MemoryService.SaveProfileRule, which sets this field directly in Go
	// code before calling Save.
	Source string `json:"-"`
}

// UpdateRequest carries the fields an agent wants to change on an existing memory.
// All fields are pointers so callers can perform partial updates — only non-nil
// fields are applied. This avoids accidental overwrites with zero values.
type UpdateRequest struct {
	// Title replaces the existing title when non-nil.
	Title *string `json:"title,omitempty"`

	// Content replaces the existing content when non-nil.
	Content *string `json:"content,omitempty"`

	// Type updates the memory classification when non-nil.
	Type *MemoryType `json:"type,omitempty"`

	// Importance overrides the current importance score when non-nil.
	Importance *float64 `json:"importance,omitempty"`

	// Confidence overrides the current confidence score when non-nil.
	Confidence *float64 `json:"confidence,omitempty"`

	// Files replaces the associated file list when non-nil.
	Files *[]string `json:"files,omitempty"`

	// AppliesTo replaces the applies_to list when non-nil.
	// Only valid when the memory is of TypeRule.
	AppliesTo *[]string `json:"applies_to,omitempty"`

	// Severity replaces the severity when non-nil.
	// Only valid when the memory is of TypeRule.
	Severity *Severity `json:"severity,omitempty"`
}

// Embedding represents a vector representation of a memory's content.
// It is stored as a flat float32 slice serialised to a BLOB in SQLite.
// The vector is not included in JSON responses because it is a raw numeric
// array with no human-readable value — callers that need it should use the
// store layer directly.
type Embedding struct {
	// MemoryID is the UUIDv7 of the memory this embedding belongs to.
	MemoryID string `json:"memory_id"`

	// Vector holds the raw float32 coefficients. Its length equals Dimensions.
	// Omitted from JSON to avoid bloating API responses.
	Vector []float32 `json:"-"`

	// Model is a stable identifier for the embedding strategy used to produce
	// this vector (e.g. "tfidf-v1", "minilm-v2"). Used to detect when
	// re-embedding is needed after an upgrade.
	Model string `json:"model"`

	// Dimensions is the length of Vector. Stored explicitly so the reader can
	// pre-allocate the slice without re-computing it from the BLOB size.
	Dimensions int `json:"dimensions"`

	// CreatedAt is the wall-clock time when the embedding was generated.
	CreatedAt time.Time `json:"created_at"`
}

// SaveResponse is returned after a successful mem_save call. It gives the agent
// enough information to reference the memory later and understand whether a new
// record was created or an existing one was updated.
type SaveResponse struct {
	// ID is the UUIDv7 of the created or updated memory.
	ID string `json:"id"`

	// Action is either "created" or "updated".
	Action string `json:"action"`

	// RevisionCount is the new revision number after the save.
	RevisionCount int `json:"revision_count"`

	// Title echoes the stored title back to the caller.
	Title string `json:"title"`

	// TopicKey echoes the topic key (empty if none was provided or derived).
	TopicKey string `json:"topic_key,omitempty"`
}
