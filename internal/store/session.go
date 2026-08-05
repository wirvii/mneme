package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wirvii/mneme/internal/model"
)

// CreateSession inserts a new session record. The caller is responsible for
// setting s.ID before calling; the store does not generate the ID.
func (s *MemoryStore) CreateSession(ctx context.Context, sess *model.Session) (*model.Session, error) {
	const q = `
		INSERT INTO sessions (id, project, agent, started_at, ended_at, summary_id)
		VALUES (?, ?, ?, ?, ?, ?)`

	var endedAt sql.NullString
	var summaryID sql.NullString

	if sess.EndedAt != nil {
		endedAt = sql.NullString{String: sess.EndedAt.UTC().Format(time.RFC3339Nano), Valid: true}
	}
	summaryID = toNullString(sess.SummaryID)

	_, err := s.db.ExecContext(ctx, q,
		sess.ID,
		toNullString(sess.Project),
		toNullString(sess.Agent),
		sess.StartedAt.UTC().Format(time.RFC3339Nano),
		endedAt,
		summaryID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: create session: %w", err)
	}

	return sess, nil
}

// EndSession marks a session as ended by setting ended_at to the current UTC
// time and recording the summaryID of the session_summary Memory. Returns
// model.ErrNotFound if the session does not exist.
func (s *MemoryStore) EndSession(ctx context.Context, id string, summaryID string) error {
	const q = `
		UPDATE sessions
		SET ended_at = datetime('now'), summary_id = ?
		WHERE id = ?`

	res, err := s.db.ExecContext(ctx, q, toNullString(summaryID), id)
	if err != nil {
		return fmt.Errorf("store: end session: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: end session: rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("store: end session: %w", model.ErrNotFound)
	}

	return nil
}

// GetLastSession returns the most recently started session for the given project.
// Returns nil, nil when no session exists for that project.
func (s *MemoryStore) GetLastSession(ctx context.Context, project string) (*model.Session, error) {
	const q = `
		SELECT id, project, agent, started_at, ended_at, summary_id
		FROM sessions
		WHERE project = ?
		ORDER BY started_at DESC
		LIMIT 1`

	row := s.db.QueryRowContext(ctx, q, project)
	sess, err := scanSession(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: get last session: %w", err)
	}

	return sess, nil
}

// ListSessionsByProject returns all sessions for the given project ordered by
// started_at ascending. An empty project returns sessions across all projects.
// The result is unbounded and suitable for manifest export of complete project state.
func (s *MemoryStore) ListSessionsByProject(ctx context.Context, project string) ([]*model.Session, error) {
	const qWithProject = `
		SELECT id, project, agent, started_at, ended_at, summary_id
		FROM sessions
		WHERE project = ?
		ORDER BY started_at ASC`

	const qAll = `
		SELECT id, project, agent, started_at, ended_at, summary_id
		FROM sessions
		ORDER BY started_at ASC`

	var rows *sql.Rows
	var err error
	if project != "" {
		rows, err = s.db.QueryContext(ctx, qWithProject, project)
	} else {
		rows, err = s.db.QueryContext(ctx, qAll)
	}
	if err != nil {
		return nil, fmt.Errorf("store: list sessions by project: %w", err)
	}
	defer rows.Close()

	var sessions []*model.Session
	for rows.Next() {
		sess, err := scanSessionRow(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list sessions by project: scan: %w", err)
		}
		sessions = append(sessions, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list sessions by project: iterate: %w", err)
	}

	return sessions, nil
}

// sessionWorkWhere define qué cuenta como "trabajo" de una sesión y es la
// ÚNICA definición del predicado: los dos consumidores lo embeben por
// concatenación, así que no pueden divergir. Precedente y misma exigencia que
// HardDeleteBySource (memory.go:443-446, "MUST stay byte-identical"): la
// asimetría entre dos rutas sobre las mismas filas es exactamente lo que en
// SPEC-105 dejó un guard divergente para siempre.
//
// Filtra deleted_at Y superseded_by, los mismos dos filtros de mem_search /
// mem_stats / store.List, para que el número del aviso siempre cuadre con lo
// que el usuario ve (SPEC-108 D7). El único `?` es el project.
const sessionWorkWhere = `m.session_id IS NOT NULL
	  AND m.session_id != ''
	  AND m.type != 'session_summary'
	  AND m.deleted_at IS NULL
	  AND m.superseded_by IS NULL
	  AND m.project IS ?`

// listSessionsWithoutSummaryQuery — args: (project, model.SessionSummaryTopicKeyPrefix)
//
// El NOT EXISTS filtra SOLO deleted_at, a propósito y en asimetría con el
// conteo de trabajo (SPEC-108 D8): un resumen *superseded* sí prueba que la
// sesión se cerró, y filtrarlo dejaría el trabajo contando para siempre =
// aviso perpetuo sin forma de converger. La asimetría apunta al SILENCIO,
// nunca a la divergencia. Reproduce además el predicado con que store.Upsert
// localiza su propia fila (memory.go:254-256), que es quien escribe el resumen.
// Sin LIMIT: devuelve una fila por session_id distinto (21 en un año de uso
// real) y un LIMIT haría INCORRECTO el conteo de "otras más antiguas" (D21).
var listSessionsWithoutSummaryQuery = `
	SELECT m.session_id, COUNT(*), MIN(m.created_at), MAX(m.created_at)
	FROM memories m
	WHERE ` + sessionWorkWhere + `
	  AND NOT EXISTS (
	      SELECT 1 FROM memories s
	      WHERE s.topic_key = ? || m.session_id
	        AND s.project IS m.project
	        AND s.deleted_at IS NULL)
	GROUP BY m.session_id
	ORDER BY MAX(m.created_at) DESC`

// getSessionActivityQuery — args: (project, sessionID)
var getSessionActivityQuery = `
	SELECT COUNT(*), MIN(m.created_at), MAX(m.created_at)
	FROM memories m
	WHERE ` + sessionWorkWhere + `
	  AND m.session_id = ?`

// GetSessionActivity devuelve el trabajo registrado por UNA sesión.
// MemoryCount 0 con FirstAt/LastAt cero cuando no hay filas (nunca error).
// Solo mira el project store: una sesión que únicamente guardó memorias
// global/org no se ve aquí (SPEC-108 D10).
//
// Caveat sub-segundo (D20): created_at se escribe siempre como RFC3339Nano
// (memory.go:124), que recorta ceros de la fracción, así que dentro del MISMO
// segundo el orden lexicográfico de MIN/MAX puede equivocarse ("…:27Z" ordena
// después de "…:27.6Z"). Error máximo sub-segundo sobre una duración que se
// reporta en segundos: aceptado, no mitigado.
func (s *MemoryStore) GetSessionActivity(ctx context.Context, project, sessionID string) (*model.SessionActivity, error) {
	row := s.db.QueryRowContext(ctx, getSessionActivityQuery, toNullString(project), sessionID)

	var count int
	var firstAt, lastAt sql.NullString
	if err := row.Scan(&count, &firstAt, &lastAt); err != nil {
		return nil, fmt.Errorf("store: get session activity: %w", err)
	}

	activity := &model.SessionActivity{
		SessionID:   sessionID,
		MemoryCount: count,
	}
	if firstAt.Valid {
		if t, err := parseTime(firstAt.String); err == nil {
			activity.FirstAt = t
		}
	}
	if lastAt.Valid {
		if t, err := parseTime(lastAt.String); err == nil {
			activity.LastAt = t
		}
	}

	return activity, nil
}

// ListSessionsWithoutSummary devuelve una entrada por sesión con trabajo
// registrado y SIN resumen, ordenadas por última actividad DESC.
func (s *MemoryStore) ListSessionsWithoutSummary(ctx context.Context, project string) ([]model.SessionActivity, error) {
	rows, err := s.db.QueryContext(ctx, listSessionsWithoutSummaryQuery,
		toNullString(project), model.SessionSummaryTopicKeyPrefix)
	if err != nil {
		return nil, fmt.Errorf("store: list sessions without summary: %w", err)
	}
	defer rows.Close()

	activities := make([]model.SessionActivity, 0)
	for rows.Next() {
		var sessionID string
		var count int
		var firstAt, lastAt sql.NullString
		if err := rows.Scan(&sessionID, &count, &firstAt, &lastAt); err != nil {
			return nil, fmt.Errorf("store: list sessions without summary: scan: %w", err)
		}

		activity := model.SessionActivity{
			SessionID:   sessionID,
			MemoryCount: count,
		}
		if firstAt.Valid {
			if t, err := parseTime(firstAt.String); err == nil {
				activity.FirstAt = t
			}
		}
		if lastAt.Valid {
			if t, err := parseTime(lastAt.String); err == nil {
				activity.LastAt = t
			}
		}
		activities = append(activities, activity)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list sessions without summary: iterate: %w", err)
	}

	return activities, nil
}

// sessionRowScanner is satisfied by both *sql.Row and *sql.Rows.
type sessionRowScanner interface {
	Scan(dest ...any) error
}

// scanSession scans a *sql.Row into a *model.Session.
func scanSession(row *sql.Row) (*model.Session, error) {
	return scanSessionRow(row)
}

// scanSessionRow scans either a *sql.Row or *sql.Rows into a *model.Session.
func scanSessionRow(row sessionRowScanner) (*model.Session, error) {
	var (
		sess      model.Session
		project   sql.NullString
		agent     sql.NullString
		endedAt   sql.NullString
		summaryID sql.NullString
		startedAt string
	)

	err := row.Scan(
		&sess.ID, &project, &agent,
		&startedAt, &endedAt, &summaryID,
	)
	if err != nil {
		return nil, err
	}

	sess.Project = project.String
	sess.Agent = agent.String
	sess.SummaryID = summaryID.String

	if t, err := parseTime(startedAt); err == nil {
		sess.StartedAt = t
	}
	if endedAt.Valid {
		if t, err := parseTime(endedAt.String); err == nil {
			sess.EndedAt = &t
		}
	}

	return &sess, nil
}
