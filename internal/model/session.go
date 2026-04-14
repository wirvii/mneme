package model

import "time"

// Session represents an agent working session. Sessions group memories created
// during a continuous work period and produce a summary memory when closed.
// Tracking sessions enables "what happened last time" queries and helps the
// decay system understand recency of access patterns.
type Session struct {
	// ID is a UUIDv7 identifying this session uniquely.
	ID string `json:"id"`

	// Project is the normalised project slug this session is associated with.
	Project string `json:"project"`

	// Agent identifies which agent created this session (e.g. "claude-code").
	Agent string `json:"agent"`

	// StartedAt is when the session was opened.
	StartedAt time.Time `json:"started_at"`

	// EndedAt is when the session was closed, nil if still active.
	EndedAt *time.Time `json:"ended_at,omitempty"`

	// SummaryID is the ID of the session_summary Memory created at session end.
	// Empty until the session is closed.
	SummaryID string `json:"summary_id,omitempty"`
}

// SessionEndRequest is sent by the agent when it is closing a session.
// The agent provides a human-readable summary of what was accomplished;
// mneme stores it as a TypeSessionSummary Memory.
type SessionEndRequest struct {
	// Summary is required. It should describe what was done and any important
	// context the agent (or a future agent) should know.
	Summary string `json:"summary"`

	// SessionID identifies which session to close. When empty the service
	// closes the most recent open session for the project.
	SessionID string `json:"session_id,omitempty"`

	// Project identifies the project this session belongs to.
	Project string `json:"project,omitempty"`
}

// SessionEndResponse is returned after successfully closing a session.
// It gives the agent confirmation and references to created artefacts.
type SessionEndResponse struct {
	// SessionID echoes the closed session ID.
	SessionID string `json:"session_id"`

	// SummaryMemoryID is the ID of the TypeSessionSummary Memory that was created.
	SummaryMemoryID string `json:"summary_memory_id"`

	// MemoriesCreated is the count of new memories saved during this session
	// (excluding the summary itself). Useful for quick reporting.
	MemoriesCreated int `json:"memories_created"`

	// SessionDuration is a human-readable duration string (e.g. "1h23m").
	SessionDuration string `json:"session_duration"`
}

// CheckpointRequest is sent by the agent to save a snapshot of its current
// work state. Checkpoints are idempotent via topic_key upsert — only the
// latest checkpoint is retained per project.
type CheckpointRequest struct {
	// Summary is required. Brief description of current work state and progress.
	Summary string `json:"summary"`

	// Decisions lists key decisions made since the last checkpoint. Optional.
	Decisions string `json:"decisions,omitempty"`

	// NextSteps describes what needs to happen next if context is lost. Optional.
	NextSteps string `json:"next_steps,omitempty"`

	// Project identifies the project. Defaults to the service's detected project.
	Project string `json:"project,omitempty"`
}

// CheckpointResponse confirms the checkpoint was saved.
type CheckpointResponse struct {
	// ID is the UUIDv7 of the checkpoint memory.
	ID string `json:"id"`

	// Action is "created" when the checkpoint did not exist yet, or "updated"
	// when a previous checkpoint was overwritten (upsert semantics).
	Action string `json:"action"`
}
