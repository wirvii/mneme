package model

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

	// IsNewTopic is true when no similar topic key exists in the database.
	// False means the agent should consider updating an existing memory instead.
	IsNewTopic bool `json:"is_new_topic"`
}

// TopicKeyMatch is a lightweight reference to an existing memory that shares
// a similar topic key. It avoids embedding the full Memory struct in
// TopicKeySuggestion to keep the response size small.
type TopicKeyMatch struct {
	// TopicKey is the exact key of the matching memory.
	TopicKey string `json:"topic_key"`

	// Title is the title of the matching memory, for quick human or agent review.
	Title string `json:"title"`

	// ID is the UUIDv7 of the matching memory, for direct reference or update.
	ID string `json:"id"`
}
