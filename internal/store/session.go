package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/juanftp/mneme/internal/model"
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

// scanSession scans a *sql.Row into a *model.Session.
func scanSession(row *sql.Row) (*model.Session, error) {
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
