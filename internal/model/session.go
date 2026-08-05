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

// SessionSummaryTopicKeyPrefix es el ÚNICO lugar donde vive el prefijo del
// topic_key de un resumen de sesión (SPEC-108 D9). El store lo recibe LIGADO
// como parámetro SQL, nunca concatenado dentro de la sentencia.
const SessionSummaryTopicKeyPrefix = "session/"

// SessionSummaryTopicKey devuelve la clave de idempotencia con la que
// SessionEnd hace upsert del resumen de una sesión.
func SessionSummaryTopicKey(sessionID string) string {
	return SessionSummaryTopicKeyPrefix + sessionID
}

// SessionActivity resume el trabajo que una sesión dejó registrado.
// MemoryCount cuenta SOLO memorias activas del project store que no son
// session_summary; FirstAt/LastAt son sus created_at extremos.
type SessionActivity struct {
	SessionID   string    `json:"session_id"`
	MemoryCount int       `json:"memory_count"`
	FirstAt     time.Time `json:"first_at"`
	LastAt      time.Time `json:"last_at"`
}

// PendingSessionsRequest identifica el proyecto y la sesión actual al
// consultar sesiones anteriores que dejaron trabajo sin resumen.
type PendingSessionsRequest struct {
	Project          string `json:"project,omitempty"`
	CurrentSessionID string `json:"current_session_id,omitempty"`
}

// PendingSessionsResponse reporta la sesión huérfana más reciente (si la hay)
// y cuántas otras más antiguas están en el mismo estado.
type PendingSessionsResponse struct {
	Pending    *SessionActivity `json:"pending,omitempty"`
	OlderCount int              `json:"older_count"`
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
	// (excluding the summary itself). AUSENTE cuando el caller no envió
	// session_id: mneme generó el id y no puede atribuir trabajo a un UUID que
	// acaba de inventar — un 0 ahí sería literalmente cierto y prácticamente
	// una mentira (SPEC-108 D13).
	MemoriesCreated *int `json:"memories_created,omitempty"`

	// SessionDuration es el lapso observable del trabajo registrado
	// (now - primer created_at), redondeado a segundos: una cota INFERIOR de la
	// duración real. No se deriva de sessions.started_at, que se fabrica en el
	// instante del cierre (SPEC-108 D11/§2.2). Ausente cuando no hay nada que
	// medir.
	SessionDuration string `json:"session_duration,omitempty"`
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
