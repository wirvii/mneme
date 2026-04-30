package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
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
// generated and CreatedAt is set to the current UTC time. When Weight is zero,
// the type-specific default from model.DefaultRelationWeights is used so that
// every relation carries a meaningful strength from the start.
//
// The caller is responsible for ensuring source and target entity IDs exist;
// the database foreign key constraint will reject invalid IDs.
func (s *MemoryStore) CreateRelation(ctx context.Context, r *model.Relation) (*model.Relation, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("store: create relation: generate id: %w", err)
	}

	now := time.Now().UTC()
	r.ID = id.String()
	r.CreatedAt = now

	if r.Weight == 0 {
		r.Weight = model.DefaultWeight(r.Type)
	}

	const q = `
		INSERT INTO relations (id, source_id, target_id, type, weight, metadata, created_at, last_traversed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

	var lastTraversed *string
	if !r.LastTraversedAt.IsZero() {
		s := r.LastTraversedAt.UTC().Format(time.RFC3339Nano)
		lastTraversed = &s
	}

	_, err = s.db.ExecContext(ctx, q,
		r.ID, r.SourceID, r.TargetID, string(r.Type),
		r.Weight, toNullString(r.Metadata),
		r.CreatedAt.Format(time.RFC3339Nano),
		lastTraversed,
	)
	if err != nil {
		return nil, fmt.Errorf("store: create relation: insert: %w", err)
	}

	return r, nil
}

// UpdateRelationWeight atomically adjusts the weight of a relation by delta,
// clamping the result to [0.0, 1.0]. The last_traversed_at timestamp is updated
// to now in the same SQL statement to eliminate any read-modify-write race
// condition. Returns the relation after the update, or ErrRelationNotFound if
// the ID does not exist. This is the primary API for Hebbian auto-strengthening
// (SPEC-G2).
func (s *MemoryStore) UpdateRelationWeight(ctx context.Context, relationID string, delta float64, now time.Time) (*model.Relation, error) {
	const q = `
		UPDATE relations
		SET weight           = MAX(0.0, MIN(1.0, weight + ?)),
		    last_traversed_at = ?
		WHERE id = ?`

	result, err := s.db.ExecContext(ctx, q, delta, now.UTC().Format(time.RFC3339Nano), relationID)
	if err != nil {
		return nil, fmt.Errorf("store: update relation weight: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("store: update relation weight: rows affected: %w", err)
	}
	if rows == 0 {
		return nil, fmt.Errorf("store: update relation weight: %w", model.ErrRelationNotFound)
	}

	return s.getRelationByID(ctx, relationID)
}

// TouchRelation updates only the last_traversed_at timestamp of a relation
// without changing its weight. Use this when a relation is traversed during
// graph navigation (e.g. 1-hop expansion) without intent to strengthen or
// weaken it.
func (s *MemoryStore) TouchRelation(ctx context.Context, relationID string, now time.Time) error {
	const q = `UPDATE relations SET last_traversed_at = ? WHERE id = ?`

	result, err := s.db.ExecContext(ctx, q, now.UTC().Format(time.RFC3339Nano), relationID)
	if err != nil {
		return fmt.Errorf("store: touch relation: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: touch relation: rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("store: touch relation: %w", model.ErrRelationNotFound)
	}
	return nil
}

// getRelationByID retrieves a single relation by primary key.
func (s *MemoryStore) getRelationByID(ctx context.Context, id string) (*model.Relation, error) {
	const q = `
		SELECT id, source_id, target_id, type, weight, metadata, created_at, last_traversed_at
		FROM relations
		WHERE id = ?`

	row := s.db.QueryRowContext(ctx, q, id)
	r, err := scanRelation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("store: get relation: %w", model.ErrRelationNotFound)
		}
		return nil, fmt.Errorf("store: get relation: %w", err)
	}
	return r, nil
}

// GetRelationsFrom returns all outgoing (source → *) relations for the entity
// identified by entityID. An empty slice is returned when no relations exist.
func (s *MemoryStore) GetRelationsFrom(ctx context.Context, entityID string) ([]*model.Relation, error) {
	const q = `
		SELECT id, source_id, target_id, type, weight, metadata, created_at, last_traversed_at
		FROM relations
		WHERE source_id = ?
		ORDER BY created_at`

	return s.queryRelations(ctx, q, entityID)
}

// GetRelationsTo returns all incoming (* → target) relations for the entity
// identified by entityID. An empty slice is returned when no relations exist.
func (s *MemoryStore) GetRelationsTo(ctx context.Context, entityID string) ([]*model.Relation, error) {
	const q = `
		SELECT id, source_id, target_id, type, weight, metadata, created_at, last_traversed_at
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

// GetStrongRelations returns all relations connected to entityID (in either
// direction) whose weight exceeds threshold, ordered by weight descending and
// capped at limit. The bidirectional lookup is implemented with two separate
// indexed queries merged in Go so that SQLite can use idx_relations_source and
// idx_relations_target independently — which is faster than a single OR query
// on large tables (D7, SPEC-007).
//
// An empty slice is returned when no relations exceed the threshold.
func (s *MemoryStore) GetStrongRelations(ctx context.Context, entityID string, threshold float64, limit int) ([]*model.Relation, error) {
	const qFrom = `
		SELECT id, source_id, target_id, type, weight, metadata, created_at, last_traversed_at
		FROM relations
		WHERE source_id = ? AND weight > ?
		ORDER BY weight DESC
		LIMIT ?`

	const qTo = `
		SELECT id, source_id, target_id, type, weight, metadata, created_at, last_traversed_at
		FROM relations
		WHERE target_id = ? AND weight > ?
		ORDER BY weight DESC
		LIMIT ?`

	fromRels, err := s.queryRelationsMultiArg(ctx, qFrom, entityID, threshold, limit)
	if err != nil {
		return nil, fmt.Errorf("store: get strong relations: outgoing: %w", err)
	}

	toRels, err := s.queryRelationsMultiArg(ctx, qTo, entityID, threshold, limit)
	if err != nil {
		return nil, fmt.Errorf("store: get strong relations: incoming: %w", err)
	}

	// Merge and deduplicate by relation ID, then re-sort by weight descending
	// and cap at limit.
	seen := make(map[string]bool, len(fromRels)+len(toRels))
	merged := make([]*model.Relation, 0, len(fromRels)+len(toRels))
	for _, r := range append(fromRels, toRels...) {
		if seen[r.ID] {
			continue
		}
		seen[r.ID] = true
		merged = append(merged, r)
	}

	// Insertion-sort by weight descending (stable, small slices).
	for i := 1; i < len(merged); i++ {
		for j := i; j > 0 && merged[j].Weight > merged[j-1].Weight; j-- {
			merged[j], merged[j-1] = merged[j-1], merged[j]
		}
	}

	if len(merged) > limit {
		merged = merged[:limit]
	}

	return merged, nil
}

// GetEntityMemoryIDs returns the IDs of all memories linked to entityID. This
// is the inverse of GetMemoryEntities (which returns full Entity objects given
// a memory). The result is used by the graph expansion algorithm to map
// neighbor entities back to their memories without loading full entities.
//
// An empty slice is returned when no memories are linked; this is not an error.
func (s *MemoryStore) GetEntityMemoryIDs(ctx context.Context, entityID string) ([]string, error) {
	const q = `SELECT memory_id FROM memory_entities WHERE entity_id = ?`

	rows, err := s.db.QueryContext(ctx, q, entityID)
	if err != nil {
		return nil, fmt.Errorf("store: get entity memory ids: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: get entity memory ids: scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: get entity memory ids: iterate: %w", err)
	}

	return ids, nil
}

// BatchTouchRelations updates last_traversed_at for multiple relations in a
// single statement. This is the bulk version of TouchRelation used after graph
// expansion to mark traversed edges for future decay eligibility (D3, SPEC-007).
// Best-effort: the caller should log failures and continue — search results are
// not affected by a failed touch.
func (s *MemoryStore) BatchTouchRelations(ctx context.Context, ids []string, now time.Time) error {
	if len(ids) == 0 {
		return nil
	}

	// Build a parameterised query: UPDATE ... WHERE id IN (?, ?, ...)
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+1)
	args = append(args, now.UTC().Format(time.RFC3339Nano))
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}

	q := "UPDATE relations SET last_traversed_at = ? WHERE id IN (" +
		strings.Join(placeholders, ",") + ")"

	_, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("store: batch touch relations: %w", err)
	}
	return nil
}

// FindRelationBidirectional looks up an existing relation between two entities
// with a given type in either direction: (source→target) or (target→source).
// It first tries sourceID→targetID; if not found it tries targetID→sourceID.
// Returns (nil, nil) when no matching relation exists in either direction.
// This is used by the Hebbian worker to avoid creating duplicate edges when
// the existing relation was stored in the opposite direction (Q3, SPEC-006).
func (s *MemoryStore) FindRelationBidirectional(ctx context.Context, sourceID, targetID string, relType model.RelationType) (*model.Relation, error) {
	rel, err := s.FindRelation(ctx, sourceID, targetID, relType)
	if err != nil {
		return nil, fmt.Errorf("store: find relation bidirectional: forward: %w", err)
	}
	if rel != nil {
		return rel, nil
	}
	// Try the reverse direction.
	rel, err = s.FindRelation(ctx, targetID, sourceID, relType)
	if err != nil {
		return nil, fmt.Errorf("store: find relation bidirectional: reverse: %w", err)
	}
	return rel, nil
}

// DecayRelationWeights applies exponential weight decay to all relations whose
// last_traversed_at is not NULL and whose last traversal was more than
// graceDays ago. The decay formula is:
//
//	newWeight = MAX(0.0, weight × e^(-decayRate × daysSinceTraversal))
//
// Relations with last_traversed_at IS NULL are excluded — they represent
// intentionally created structural edges that should not decay (D6, SPEC-006).
// Relations traversed within graceDays are also excluded.
//
// The decay is computed in Go (not SQL) to avoid requiring the SQLite math
// extension (SQLITE_ENABLE_MATH_FUNCTIONS). Eligible relations are fetched,
// their new weights are computed, and each is updated individually.
//
// Returns the number of relations whose weight was updated. When decayRate is
// zero the function returns (0, nil) immediately — decay is disabled.
func (s *MemoryStore) DecayRelationWeights(ctx context.Context, decayRate float64, graceDays int) (int, error) {
	if decayRate <= 0 {
		return 0, nil
	}

	now := time.Now().UTC()
	graceCutoff := now.AddDate(0, 0, -graceDays)

	// Fetch relations with a non-NULL last_traversed_at that is older than the grace period.
	const q = `
		SELECT id, source_id, target_id, type, weight, metadata, created_at, last_traversed_at
		FROM relations
		WHERE last_traversed_at IS NOT NULL
		  AND last_traversed_at < ?`

	rows, err := s.db.QueryContext(ctx, q, graceCutoff.Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("store: decay relation weights: query: %w", err)
	}
	defer rows.Close()

	type candidate struct {
		id      string
		weight  float64
		daysDue float64
	}
	var candidates []candidate

	for rows.Next() {
		r, err := scanRelationRow(rows)
		if err != nil {
			return 0, fmt.Errorf("store: decay relation weights: scan: %w", err)
		}
		days := now.Sub(r.LastTraversedAt).Hours() / 24.0
		candidates = append(candidates, candidate{id: r.ID, weight: r.Weight, daysDue: days})
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("store: decay relation weights: iterate: %w", err)
	}
	rows.Close()

	const upd = `UPDATE relations SET weight = ? WHERE id = ?`
	updated := 0
	for _, c := range candidates {
		if ctx.Err() != nil {
			return updated, ctx.Err()
		}
		newWeight := c.weight * math.Exp(-decayRate*c.daysDue)
		if newWeight < 0 {
			newWeight = 0
		}
		if _, err := s.db.ExecContext(ctx, upd, newWeight, c.id); err != nil {
			return updated, fmt.Errorf("store: decay relation weights: update %s: %w", c.id, err)
		}
		updated++
	}

	return updated, nil
}

// FindRelation looks up an existing relation between two entities with a given
// type. Returns (nil, nil) when no matching relation exists.
func (s *MemoryStore) FindRelation(ctx context.Context, sourceID, targetID string, relType model.RelationType) (*model.Relation, error) {
	const q = `
		SELECT id, source_id, target_id, type, weight, metadata, created_at, last_traversed_at
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
		       decay_rate, revision_count, superseded_by, deleted_at,
		       applies_to, severity
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

// queryRelationsMultiArg executes a relation SELECT query with multiple
// positional arguments and returns the scanned results. Used by
// GetStrongRelations which requires three arguments (entityID, threshold, limit).
func (s *MemoryStore) queryRelationsMultiArg(ctx context.Context, q string, args ...any) ([]*model.Relation, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: query relations multi-arg: %w", err)
	}
	defer rows.Close()

	var relations []*model.Relation
	for rows.Next() {
		r, err := scanRelationRow(rows)
		if err != nil {
			return nil, fmt.Errorf("store: query relations multi-arg: scan: %w", err)
		}
		relations = append(relations, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: query relations multi-arg: iterate: %w", err)
	}

	return relations, nil
}

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
// Expects 8 columns in order: id, source_id, target_id, type, weight, metadata,
// created_at, last_traversed_at. All relation SELECT queries must include
// last_traversed_at after migration 007.
func scanRelationRow(row relationScanner) (*model.Relation, error) {
	var (
		r              model.Relation
		metadata       sql.NullString
		createdAt      string
		lastTraversed  sql.NullString
	)

	err := row.Scan(
		&r.ID, &r.SourceID, &r.TargetID,
		&r.Type, &r.Weight,
		&metadata, &createdAt,
		&lastTraversed,
	)
	if err != nil {
		return nil, err
	}

	r.Metadata = metadata.String

	if t, err := parseTime(createdAt); err == nil {
		r.CreatedAt = t
	}
	if lastTraversed.Valid && lastTraversed.String != "" {
		if t, err := parseTime(lastTraversed.String); err == nil {
			r.LastTraversedAt = t
		}
	}

	return &r, nil
}
