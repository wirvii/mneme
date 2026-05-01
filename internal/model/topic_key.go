package model

// SuggestTopicKeyRequest parameterises a SuggestTopicKey query. Using a struct
// instead of positional parameters mirrors the pattern of GapsRequest, SaveRequest,
// and ExploreRequest and makes future extensions (content, flags) non-breaking.
type SuggestTopicKeyRequest struct {
	// Title is the title of the memory for which to suggest a topic key. Required.
	Title string `json:"title"`

	// Project restricts the search to a specific project slug. When empty, the
	// service uses its configured project.
	Project string `json:"project,omitempty"`
}

// TopicKeySuggestion is returned when an agent asks mneme to suggest a topic key
// for a new memory. The suggestion helps agents produce consistent, stable keys
// (e.g. "architecture/auth-model") without having to invent them from scratch,
// which reduces duplicate memories caused by key name variations.
type TopicKeySuggestion struct {
	// Suggestion is the recommended topic key, derived from the memory title
	// and existing key patterns in the database.
	Suggestion string `json:"suggestion"`

	// ExistingMatches lists memories that already use a similar topic key.
	// The agent can review these to decide whether to update an existing memory
	// or create a new one with a distinct key.
	ExistingMatches []TopicKeyMatch `json:"existing_matches,omitempty"`

	// GapMatches lists unresolved gaps whose topic_key is similar to the query.
	// Non-nil only when gaps are found above the Jaccard threshold (SPEC-014).
	GapMatches []TopicKeyMatch `json:"gap_matches,omitempty"`

	// IsNewTopic is true when no similar topic key exists in either existing
	// memories or unresolved gaps. False means the agent should consider updating
	// an existing memory or filling an existing gap instead.
	IsNewTopic bool `json:"is_new_topic"`
}

// TopicKeyMatch is a lightweight reference to an existing memory or an unresolved
// knowledge gap that shares a similar topic key. It avoids embedding the full
// Memory struct in TopicKeySuggestion to keep the response size small.
type TopicKeyMatch struct {
	// TopicKey is the exact key of the matching memory or gap.
	TopicKey string `json:"topic_key"`

	// Title is the title of the matching memory. For gap matches, this is the
	// target_topic_key formatted as a readable label.
	Title string `json:"title"`

	// ID is the UUIDv7 of the matching memory, for direct reference or update.
	// Empty (and omitted in JSON) for gap matches, where no memory exists yet.
	ID string `json:"id,omitempty"`

	// Score is the combined relevance score for this match. Higher is better.
	// For existing matches it equals the Jaccard similarity. For gap matches it
	// includes a configurable boost and a log-scaled pending-count adjustment.
	// Omitted (zero) in JSON for backward compatibility when callers ignore scoring.
	Score float64 `json:"score,omitempty"`

	// FromGap is true when this match came from an unresolved knowledge gap
	// rather than an existing memory. Omitted (false) for existing matches.
	FromGap bool `json:"from_gap,omitempty"`

	// PendingCount is the total_mentions from unresolved_references for gap
	// matches. Zero (and omitted) for existing matches.
	PendingCount int `json:"pending_count,omitempty"`

	// Reason explains why this match was suggested, providing context for the
	// agent to make an informed decision. Omitted when empty.
	Reason string `json:"reason,omitempty"`
}
