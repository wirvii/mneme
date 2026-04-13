// Package store implements the data access layer for mneme using SQLite.
// It provides repository-pattern interfaces for CRUD operations on memories,
// full-text search via FTS5, and session tracking. All operations use real SQL
// against SQLite — no ORM, no mocks.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/juanftp/mneme/internal/db"
	"github.com/juanftp/mneme/internal/model"
)

// MemoryStore provides CRUD, list, search, and session operations against the
// SQLite database. It embeds SearchStore and SessionStore so callers can use a
// single handle for all store operations.
type MemoryStore struct {
	db *db.DB
}

// NewMemoryStore constructs a MemoryStore backed by the given database.
// The caller retains ownership of database and is responsible for closing it.
func NewMemoryStore(database *db.DB) *MemoryStore {
	return &MemoryStore{db: database}
}

// Create persists a new memory, assigns a UUIDv7 ID, and sets CreatedAt and
// UpdatedAt to the current UTC time. Associated file paths are inserted into
// memory_files in the same implicit transaction.
// The returned *Memory reflects the stored state including the generated ID.
func (s *MemoryStore) Create(ctx context.Context, m *model.Memory) (*model.Memory, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("store: create memory: generate id: %w", err)
	}

	now := time.Now().UTC()
	m.ID = id.String()
	m.CreatedAt = now
	m.UpdatedAt = now

	const q = `
		INSERT INTO memories (
			id, type, scope, title, content, topic_key, project,
			session_id, created_by, created_at, updated_at,
			importance, confidence, access_count, last_accessed,
			decay_rate, revision_count, superseded_by, deleted_at
		) VALUES (
			?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?, ?
		)`

	var topicKey, project, sessionID, createdBy, supersededBy sql.NullString
	var lastAccessed sql.NullString
	var deletedAt sql.NullString

	topicKey = toNullString(m.TopicKey)
	project = toNullString(m.Project)
	sessionID = toNullString(m.SessionID)
	createdBy = toNullString(m.CreatedBy)
	supersededBy = toNullString(m.SupersededBy)

	if m.LastAccessed != nil {
		lastAccessed = sql.NullString{String: m.LastAccessed.UTC().Format(time.RFC3339Nano), Valid: true}
	}
	if m.DeletedAt != nil {
		deletedAt = sql.NullString{String: m.DeletedAt.UTC().Format(time.RFC3339Nano), Valid: true}
	}

	_, err = s.db.ExecContext(ctx, q,
		m.ID, string(m.Type), string(m.Scope), m.Title, m.Content,
		topicKey, project,
		sessionID, createdBy,
		m.CreatedAt.Format(time.RFC3339Nano),
		m.UpdatedAt.Format(time.RFC3339Nano),
		m.Importance, m.Confidence, m.AccessCount, lastAccessed,
		m.DecayRate, m.RevisionCount, supersededBy, deletedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("store: create memory: insert: %w", err)
	}

	if err := s.insertFiles(ctx, m.ID, m.Files); err != nil {
		return nil, err
	}

	return m, nil
}

// Get retrieves a non-deleted memory by its UUIDv7 id. Returns nil, nil when no
// memory with that id exists (or it has been soft-deleted). Associated file paths
// are loaded and populated in the returned Memory.Files slice.
func (s *MemoryStore) Get(ctx context.Context, id string) (*model.Memory, error) {
	const q = `
		SELECT id, type, scope, title, content, topic_key, project,
		       session_id, created_by, created_at, updated_at,
		       importance, confidence, access_count, last_accessed,
		       decay_rate, revision_count, superseded_by, deleted_at
		FROM memories
		WHERE id = ? AND deleted_at IS NULL`

	row := s.db.QueryRowContext(ctx, q, id)
	m, err := scanMemory(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: get memory: %w", err)
	}

	if err := s.loadFiles(ctx, m); err != nil {
		return nil, err
	}

	return m, nil
}

// Update applies a partial update to an existing memory identified by id. Only
// non-nil fields in req are written. updated_at is always refreshed and
// revision_count is always incremented. When Files is non-nil in req, the
// memory_files rows are replaced atomically. Returns model.ErrNotFound if no
// active memory with that id exists.
func (s *MemoryStore) Update(ctx context.Context, id string, req *model.UpdateRequest) error {
	setClauses := []string{"updated_at = ?", "revision_count = revision_count + 1"}
	args := []any{time.Now().UTC().Format(time.RFC3339Nano)}

	if req.Title != nil {
		setClauses = append(setClauses, "title = ?")
		args = append(args, *req.Title)
	}
	if req.Content != nil {
		setClauses = append(setClauses, "content = ?")
		args = append(args, *req.Content)
	}
	if req.Type != nil {
		setClauses = append(setClauses, "type = ?")
		args = append(args, string(*req.Type))
	}
	if req.Importance != nil {
		setClauses = append(setClauses, "importance = ?")
		args = append(args, *req.Importance)
	}
	if req.Confidence != nil {
		setClauses = append(setClauses, "confidence = ?")
		args = append(args, *req.Confidence)
	}

	args = append(args, id)
	q := fmt.Sprintf(
		"UPDATE memories SET %s WHERE id = ? AND deleted_at IS NULL",
		strings.Join(setClauses, ", "),
	)

	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("store: update memory: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update memory: rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("store: update memory: %w", model.ErrNotFound)
	}

	if req.Files != nil {
		if err := s.replaceFiles(ctx, id, *req.Files); err != nil {
			return err
		}
	}

	return nil
}

// Upsert inserts or updates a memory based on its TopicKey, Project, and Scope.
// When TopicKey is empty a new memory is always created. Otherwise it looks for
// an active record with the same (topic_key, project, scope) triple and updates
// it if found. Returns the resulting memory, a boolean created flag (true=new,
// false=updated), and any error.
func (s *MemoryStore) Upsert(ctx context.Context, m *model.Memory) (*model.Memory, bool, error) {
	if m.TopicKey == "" {
		created, err := s.Create(ctx, m)
		if err != nil {
			return nil, false, err
		}
		return created, true, nil
	}

	const sel = `
		SELECT id FROM memories
		WHERE topic_key = ? AND project IS ? AND scope = ? AND deleted_at IS NULL
		LIMIT 1`

	var existingID string
	err := s.db.QueryRowContext(ctx, sel, m.TopicKey, toNullString(m.Project), string(m.Scope)).
		Scan(&existingID)

	if errors.Is(err, sql.ErrNoRows) {
		// No existing record — create fresh.
		created, createErr := s.Create(ctx, m)
		if createErr != nil {
			return nil, false, createErr
		}
		return created, true, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("store: upsert memory: lookup: %w", err)
	}

	// Existing record — update the mutable fields.
	const upd = `
		UPDATE memories
		SET title = ?, content = ?, importance = ?, type = ?,
		    updated_at = ?, revision_count = revision_count + 1
		WHERE id = ? AND deleted_at IS NULL`

	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, upd,
		m.Title, m.Content, m.Importance, string(m.Type),
		now.Format(time.RFC3339Nano),
		existingID,
	)
	if err != nil {
		return nil, false, fmt.Errorf("store: upsert memory: update: %w", err)
	}

	if len(m.Files) > 0 {
		if err := s.replaceFiles(ctx, existingID, m.Files); err != nil {
			return nil, false, err
		}
	}

	updated, err := s.Get(ctx, existingID)
	if err != nil {
		return nil, false, fmt.Errorf("store: upsert memory: reload: %w", err)
	}

	return updated, false, nil
}

// SetDecayRate updates the decay_rate column for the memory identified by id.
// It is used by the service layer to accelerate expiry (e.g. Forget sets the
// rate to 1.0 so the memory loses importance on the next scoring pass).
// Returns model.ErrNotFound when no active memory with that id exists.
func (s *MemoryStore) SetDecayRate(ctx context.Context, id string, rate float64) error {
	const q = `
		UPDATE memories SET decay_rate = ?, updated_at = datetime('now')
		WHERE id = ? AND deleted_at IS NULL`

	res, err := s.db.ExecContext(ctx, q, rate, id)
	if err != nil {
		return fmt.Errorf("store: set decay rate: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: set decay rate: rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("store: set decay rate: %w", model.ErrNotFound)
	}

	return nil
}

// SoftDelete marks a memory as deleted by setting deleted_at to the current
// UTC time. The record remains in the database for audit and decay purposes.
// Returns model.ErrNotFound when no active memory with that id exists.
func (s *MemoryStore) SoftDelete(ctx context.Context, id string) error {
	const q = `
		UPDATE memories SET deleted_at = datetime('now')
		WHERE id = ? AND deleted_at IS NULL`

	res, err := s.db.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("store: soft delete memory: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: soft delete memory: rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("store: soft delete memory: %w", model.ErrNotFound)
	}

	return nil
}

// HardDelete permanently removes all soft-deleted memories whose deleted_at
// timestamp is older than olderThan. Returns the number of rows deleted.
// Use this as part of a periodic cleanup job after the retention window expires.
func (s *MemoryStore) HardDelete(ctx context.Context, olderThan time.Time) (int, error) {
	const q = `
		DELETE FROM memories
		WHERE deleted_at IS NOT NULL AND deleted_at < ?`

	res, err := s.db.ExecContext(ctx, q, olderThan.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("store: hard delete memories: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: hard delete memories: rows affected: %w", err)
	}

	return int(n), nil
}

// ListOptions parameterises a List query. Zero values are ignored (no filter).
type ListOptions struct {
	// Project restricts results to a specific project slug. Empty means no filter.
	Project string

	// Scope restricts results to a specific scope. Zero value means no filter.
	Scope model.Scope

	// Type restricts results to a specific memory type. Zero value means no filter.
	Type model.MemoryType

	// IncludeSuperseded includes memories whose superseded_by is set when true.
	// Defaults to false (superseded memories are hidden).
	IncludeSuperseded bool

	// OrderBy is the column used for sorting, e.g. "importance", "created_at",
	// "updated_at". Defaults to "importance DESC" when empty.
	OrderBy string

	// Limit caps the returned rows. Defaults to 50 when zero.
	Limit int
}

// List returns memories matching the given options. Soft-deleted memories are
// always excluded. Files are populated for each returned memory.
func (s *MemoryStore) List(ctx context.Context, opts ListOptions) ([]*model.Memory, error) {
	where := []string{"deleted_at IS NULL"}
	args := []any{}

	if opts.Project != "" {
		where = append(where, "project = ?")
		args = append(args, opts.Project)
	}
	if opts.Scope != "" {
		where = append(where, "scope = ?")
		args = append(args, string(opts.Scope))
	}
	if opts.Type != "" {
		where = append(where, "type = ?")
		args = append(args, string(opts.Type))
	}
	if !opts.IncludeSuperseded {
		where = append(where, "superseded_by IS NULL")
	}

	orderBy := opts.OrderBy
	if orderBy == "" {
		orderBy = "importance DESC"
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}

	q := fmt.Sprintf(`
		SELECT id, type, scope, title, content, topic_key, project,
		       session_id, created_by, created_at, updated_at,
		       importance, confidence, access_count, last_accessed,
		       decay_rate, revision_count, superseded_by, deleted_at
		FROM memories
		WHERE %s
		ORDER BY %s
		LIMIT ?`,
		strings.Join(where, " AND "),
		orderBy,
	)

	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list memories: %w", err)
	}
	defer rows.Close()

	var memories []*model.Memory
	for rows.Next() {
		m, err := scanMemoryRow(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list memories: scan: %w", err)
		}
		memories = append(memories, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list memories: iterate: %w", err)
	}
	// Close rows before issuing file queries to avoid a deadlock when the
	// connection pool is limited to a single connection (e.g. SQLite in-memory
	// databases used in tests). rows.Close() is idempotent so defer still works.
	rows.Close()

	for _, m := range memories {
		if err := s.loadFiles(ctx, m); err != nil {
			return nil, err
		}
	}

	return memories, nil
}

// IncrementAccess increments access_count by one and sets last_accessed to the
// current UTC time for the memory identified by id. No error is returned if the
// id does not exist — a best-effort update is acceptable for access tracking.
func (s *MemoryStore) IncrementAccess(ctx context.Context, id string) error {
	const q = `
		UPDATE memories
		SET access_count = access_count + 1, last_accessed = datetime('now')
		WHERE id = ?`

	_, err := s.db.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("store: increment access: %w", err)
	}

	return nil
}

// Count returns the number of active (non-deleted) memories for the given project.
func (s *MemoryStore) Count(ctx context.Context, project string) (int, error) {
	const q = `
		SELECT COUNT(*) FROM memories
		WHERE project = ? AND deleted_at IS NULL`

	var n int
	if err := s.db.QueryRowContext(ctx, q, project).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count memories: %w", err)
	}

	return n, nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// insertFiles bulk-inserts file paths for a memory. It is a no-op when files is empty.
func (s *MemoryStore) insertFiles(ctx context.Context, memoryID string, files []string) error {
	if len(files) == 0 {
		return nil
	}

	const q = `INSERT OR IGNORE INTO memory_files (memory_id, file_path) VALUES (?, ?)`
	for _, f := range files {
		if _, err := s.db.ExecContext(ctx, q, memoryID, f); err != nil {
			return fmt.Errorf("store: insert file %q: %w", f, err)
		}
	}

	return nil
}

// replaceFiles removes all existing file entries for memoryID and inserts the
// provided slice in their place.
func (s *MemoryStore) replaceFiles(ctx context.Context, memoryID string, files []string) error {
	if _, err := s.db.ExecContext(ctx,
		"DELETE FROM memory_files WHERE memory_id = ?", memoryID,
	); err != nil {
		return fmt.Errorf("store: replace files: delete: %w", err)
	}

	return s.insertFiles(ctx, memoryID, files)
}

// loadFiles queries memory_files and populates m.Files.
func (s *MemoryStore) loadFiles(ctx context.Context, m *model.Memory) error {
	const q = `SELECT file_path FROM memory_files WHERE memory_id = ? ORDER BY file_path`

	rows, err := s.db.QueryContext(ctx, q, m.ID)
	if err != nil {
		return fmt.Errorf("store: load files for %s: %w", m.ID, err)
	}
	defer rows.Close()

	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return fmt.Errorf("store: load files for %s: scan: %w", m.ID, err)
		}
		m.Files = append(m.Files, path)
	}

	return rows.Err()
}

// toNullString converts an empty string to a SQL NULL; non-empty strings are valid.
func toNullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// scannerRow is satisfied by both *sql.Row and *sql.Rows to allow a shared
// scan helper.
type scannerRow interface {
	Scan(dest ...any) error
}

// scanMemory scans a single *sql.Row into a *model.Memory.
func scanMemory(row *sql.Row) (*model.Memory, error) {
	return scanMemoryRow(row)
}

// scanMemoryRow scans either a *sql.Row or *sql.Rows into a *model.Memory.
func scanMemoryRow(row scannerRow) (*model.Memory, error) {
	var (
		m            model.Memory
		topicKey     sql.NullString
		project      sql.NullString
		sessionID    sql.NullString
		createdBy    sql.NullString
		supersededBy sql.NullString
		lastAccessed sql.NullString
		deletedAt    sql.NullString
		createdAt    string
		updatedAt    string
	)

	err := row.Scan(
		&m.ID, &m.Type, &m.Scope, &m.Title, &m.Content,
		&topicKey, &project,
		&sessionID, &createdBy,
		&createdAt, &updatedAt,
		&m.Importance, &m.Confidence, &m.AccessCount, &lastAccessed,
		&m.DecayRate, &m.RevisionCount, &supersededBy, &deletedAt,
	)
	if err != nil {
		return nil, err
	}

	m.TopicKey = topicKey.String
	m.Project = project.String
	m.SessionID = sessionID.String
	m.CreatedBy = createdBy.String
	m.SupersededBy = supersededBy.String

	if t, err := parseTime(createdAt); err == nil {
		m.CreatedAt = t
	}
	if t, err := parseTime(updatedAt); err == nil {
		m.UpdatedAt = t
	}
	if lastAccessed.Valid {
		if t, err := parseTime(lastAccessed.String); err == nil {
			m.LastAccessed = &t
		}
	}
	if deletedAt.Valid {
		if t, err := parseTime(deletedAt.String); err == nil {
			m.DeletedAt = &t
		}
	}

	return &m, nil
}

// parseTime attempts multiple SQLite datetime formats, returning the first that parses.
func parseTime(s string) (time.Time, error) {
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("store: parse time %q: no matching format", s)
}
