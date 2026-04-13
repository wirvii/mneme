package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/juanftp/mneme/internal/model"
)

// CreateEntity persists a new entity in the knowledge graph. A UUIDv7 ID is
// generated and CreatedAt / UpdatedAt are set to the current UTC time.
// The returned *Entity reflects the stored state including the generated ID.
func (s *MemoryStore) CreateEntity(ctx context.Context, e *model.Entity) (*model.Entity, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("store: create entity: generate id: %w", err)
	}

	now := time.Now().UTC()
	e.ID = id.String()
	e.CreatedAt = now
	e.UpdatedAt = now

	const q = `
		INSERT INTO entities (id, name, kind, project, metadata, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`

	_, err = s.db.ExecContext(ctx, q,
		e.ID, e.Name, string(e.Kind),
		toNullString(e.Project), toNullString(e.Metadata),
		e.CreatedAt.Format(time.RFC3339Nano),
		e.UpdatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("store: create entity: insert: %w", err)
	}

	return e, nil
}

// GetEntity retrieves an entity by its UUIDv7 id.
// Returns ErrEntityNotFound when no entity with that id exists.
func (s *MemoryStore) GetEntity(ctx context.Context, id string) (*model.Entity, error) {
	const q = `
		SELECT id, name, kind, project, metadata, created_at, updated_at
		FROM entities
		WHERE id = ?`

	row := s.db.QueryRowContext(ctx, q, id)
	e, err := scanEntity(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("store: get entity: %w", model.ErrEntityNotFound)
		}
		return nil, fmt.Errorf("store: get entity: %w", err)
	}

	return e, nil
}

// GetEntityByName retrieves an entity by its (name, project) unique pair.
// Pass an empty project string to look up entities that are not project-scoped.
// Returns ErrEntityNotFound when no matching entity exists.
func (s *MemoryStore) GetEntityByName(ctx context.Context, name, project string) (*model.Entity, error) {
	const q = `
		SELECT id, name, kind, project, metadata, created_at, updated_at
		FROM entities
		WHERE name = ? AND project IS ?`

	row := s.db.QueryRowContext(ctx, q, name, toNullString(project))
	e, err := scanEntity(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("store: get entity by name: %w", model.ErrEntityNotFound)
		}
		return nil, fmt.Errorf("store: get entity by name: %w", err)
	}

	return e, nil
}

// FindOrCreateEntity retrieves the entity with the given name and project, or
// creates it with the supplied kind when it does not exist. This is the primary
// way callers resolve entity references without causing duplicate rows.
func (s *MemoryStore) FindOrCreateEntity(ctx context.Context, name string, kind model.EntityKind, project string) (*model.Entity, error) {
	e, err := s.GetEntityByName(ctx, name, project)
	if err == nil {
		return e, nil
	}
	if !errors.Is(err, model.ErrEntityNotFound) {
		return nil, fmt.Errorf("store: find or create entity: %w", err)
	}

	// Entity does not exist — create it.
	newEntity := &model.Entity{
		Name:    name,
		Kind:    kind,
		Project: project,
	}
	created, err := s.CreateEntity(ctx, newEntity)
	if err != nil {
		return nil, fmt.Errorf("store: find or create entity: %w", err)
	}

	return created, nil
}

// ListEntities returns entities filtered by project and optionally by kind.
// Pass an empty project to list across all projects; pass an empty kind to
// skip kind filtering. Results are ordered by name and capped by limit (defaults
// to 50 when zero).
func (s *MemoryStore) ListEntities(ctx context.Context, project string, kind model.EntityKind, limit int) ([]*model.Entity, error) {
	if limit <= 0 {
		limit = 50
	}

	args := []any{}
	where := []string{}

	if project != "" {
		where = append(where, "project = ?")
		args = append(args, project)
	}
	if kind != "" {
		where = append(where, "kind = ?")
		args = append(args, string(kind))
	}

	q := `SELECT id, name, kind, project, metadata, created_at, updated_at FROM entities`
	if len(where) > 0 {
		q += " WHERE "
		for i, w := range where {
			if i > 0 {
				q += " AND "
			}
			q += w
		}
	}
	q += " ORDER BY name LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list entities: %w", err)
	}
	defer rows.Close()

	var entities []*model.Entity
	for rows.Next() {
		e, err := scanEntityRow(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list entities: scan: %w", err)
		}
		entities = append(entities, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list entities: iterate: %w", err)
	}

	return entities, nil
}

// CreateRelation inserts a directed edge between two entities. A UUIDv7 ID is
// generated and CreatedAt is set to the current UTC time. The caller is
// responsible for ensuring source and target entity IDs exist; the database
// foreign key constraint will reject invalid IDs.
func (s *MemoryStore) CreateRelation(ctx context.Context, r *model.Relation) (*model.Relation, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("store: create relation: generate id: %w", err)
	}

	now := time.Now().UTC()
	r.ID = id.String()
	r.CreatedAt = now

	if r.Weight == 0 {
		r.Weight = 1.0
	}

	const q = `
		INSERT INTO relations (id, source_id, target_id, type, weight, metadata, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`

	_, err = s.db.ExecContext(ctx, q,
		r.ID, r.SourceID, r.TargetID, string(r.Type),
		r.Weight, toNullString(r.Metadata),
		r.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("store: create relation: insert: %w", err)
	}

	return r, nil
}

// GetRelationsFrom returns all outgoing (source → *) relations for the entity
// identified by entityID. An empty slice is returned when no relations exist.
func (s *MemoryStore) GetRelationsFrom(ctx context.Context, entityID string) ([]*model.Relation, error) {
	const q = `
		SELECT id, source_id, target_id, type, weight, metadata, created_at
		FROM relations
		WHERE source_id = ?
		ORDER BY created_at`

	return s.queryRelations(ctx, q, entityID)
}

// GetRelationsTo returns all incoming (* → target) relations for the entity
// identified by entityID. An empty slice is returned when no relations exist.
func (s *MemoryStore) GetRelationsTo(ctx context.Context, entityID string) ([]*model.Relation, error) {
	const q = `
		SELECT id, source_id, target_id, type, weight, metadata, created_at
		FROM relations
		WHERE target_id = ?
		ORDER BY created_at`

	return s.queryRelations(ctx, q, entityID)
}

// LinkMemoryEntity inserts a row into memory_entities associating a memory with
// an entity under a given role (e.g. "mention", "subject"). The role defaults
// to "mention" when empty. The operation is idempotent due to the PRIMARY KEY
// constraint — calling it multiple times is safe.
func (s *MemoryStore) LinkMemoryEntity(ctx context.Context, memoryID, entityID, role string) error {
	if role == "" {
		role = "mention"
	}

	const q = `
		INSERT OR IGNORE INTO memory_entities (memory_id, entity_id, role)
		VALUES (?, ?, ?)`

	_, err := s.db.ExecContext(ctx, q, memoryID, entityID, role)
	if err != nil {
		return fmt.Errorf("store: link memory entity: %w", err)
	}

	return nil
}

// GetMemoryEntities returns all entities linked to the memory identified by
// memoryID. An empty slice is returned when no entities are linked.
func (s *MemoryStore) GetMemoryEntities(ctx context.Context, memoryID string) ([]*model.Entity, error) {
	const q = `
		SELECT e.id, e.name, e.kind, e.project, e.metadata, e.created_at, e.updated_at
		FROM entities e
		JOIN memory_entities me ON e.id = me.entity_id
		WHERE me.memory_id = ?
		ORDER BY e.name`

	rows, err := s.db.QueryContext(ctx, q, memoryID)
	if err != nil {
		return nil, fmt.Errorf("store: get memory entities: %w", err)
	}
	defer rows.Close()

	var entities []*model.Entity
	for rows.Next() {
		e, err := scanEntityRow(rows)
		if err != nil {
			return nil, fmt.Errorf("store: get memory entities: scan: %w", err)
		}
		entities = append(entities, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: get memory entities: iterate: %w", err)
	}

	return entities, nil
}

// FindRelation looks up an existing relation between two entities with a given
// type. Returns (nil, nil) when no matching relation exists.
func (s *MemoryStore) FindRelation(ctx context.Context, sourceID, targetID string, relType model.RelationType) (*model.Relation, error) {
	const q = `
		SELECT id, source_id, target_id, type, weight, metadata, created_at
		FROM relations
		WHERE source_id = ? AND target_id = ? AND type = ?
		LIMIT 1`

	row := s.db.QueryRowContext(ctx, q, sourceID, targetID, string(relType))
	r, err := scanRelation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: find relation: %w", err)
	}

	return r, nil
}

// ListMemoriesInRange returns all non-deleted memories whose created_at falls
// within [from, to]. Results are ordered by created_at ascending. The limit
// defaults to 20 when zero.
func (s *MemoryStore) ListMemoriesInRange(ctx context.Context, from, to time.Time, project string, limit int) ([]*model.Memory, error) {
	if limit <= 0 {
		limit = 20
	}

	args := []any{
		from.UTC().Format(time.RFC3339Nano),
		to.UTC().Format(time.RFC3339Nano),
	}
	where := "deleted_at IS NULL AND created_at >= ? AND created_at <= ?"

	if project != "" {
		where += " AND project = ?"
		args = append(args, project)
	}

	q := fmt.Sprintf(`
		SELECT id, type, scope, title, content, topic_key, project,
		       session_id, created_by, created_at, updated_at,
		       importance, confidence, access_count, last_accessed,
		       decay_rate, revision_count, superseded_by, deleted_at
		FROM memories
		WHERE %s
		ORDER BY created_at ASC
		LIMIT ?`, where)

	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list memories in range: %w", err)
	}
	defer rows.Close()

	var memories []*model.Memory
	for rows.Next() {
		m, err := scanMemoryRow(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list memories in range: scan: %w", err)
		}
		memories = append(memories, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list memories in range: iterate: %w", err)
	}
	rows.Close()

	for _, m := range memories {
		if err := s.loadFiles(ctx, m); err != nil {
			return nil, err
		}
	}

	return memories, nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// queryRelations is a shared helper that executes a relation SELECT query with
// a single argument and returns the scanned results.
func (s *MemoryStore) queryRelations(ctx context.Context, q, arg string) ([]*model.Relation, error) {
	rows, err := s.db.QueryContext(ctx, q, arg)
	if err != nil {
		return nil, fmt.Errorf("store: query relations: %w", err)
	}
	defer rows.Close()

	var relations []*model.Relation
	for rows.Next() {
		r, err := scanRelationRow(rows)
		if err != nil {
			return nil, fmt.Errorf("store: query relations: scan: %w", err)
		}
		relations = append(relations, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: query relations: iterate: %w", err)
	}

	return relations, nil
}

// entityScanner is satisfied by both *sql.Row and *sql.Rows.
type entityScanner interface {
	Scan(dest ...any) error
}

// scanEntity scans a *sql.Row into a *model.Entity.
func scanEntity(row *sql.Row) (*model.Entity, error) {
	return scanEntityRow(row)
}

// scanEntityRow scans either a *sql.Row or *sql.Rows into a *model.Entity.
func scanEntityRow(row entityScanner) (*model.Entity, error) {
	var (
		e         model.Entity
		project   sql.NullString
		metadata  sql.NullString
		createdAt string
		updatedAt string
	)

	err := row.Scan(
		&e.ID, &e.Name, &e.Kind,
		&project, &metadata,
		&createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}

	e.Project = project.String
	e.Metadata = metadata.String

	if t, err := parseTime(createdAt); err == nil {
		e.CreatedAt = t
	}
	if t, err := parseTime(updatedAt); err == nil {
		e.UpdatedAt = t
	}

	return &e, nil
}

// relationScanner is satisfied by both *sql.Row and *sql.Rows.
type relationScanner interface {
	Scan(dest ...any) error
}

// scanRelation scans a *sql.Row into a *model.Relation.
func scanRelation(row *sql.Row) (*model.Relation, error) {
	return scanRelationRow(row)
}

// scanRelationRow scans either a *sql.Row or *sql.Rows into a *model.Relation.
func scanRelationRow(row relationScanner) (*model.Relation, error) {
	var (
		r         model.Relation
		metadata  sql.NullString
		createdAt string
	)

	err := row.Scan(
		&r.ID, &r.SourceID, &r.TargetID,
		&r.Type, &r.Weight,
		&metadata, &createdAt,
	)
	if err != nil {
		return nil, err
	}

	r.Metadata = metadata.String

	if t, err := parseTime(createdAt); err == nil {
		r.CreatedAt = t
	}

	return &r, nil
}
