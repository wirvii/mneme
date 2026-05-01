// Package store community.go — repository operations for persisted Louvain
// communities (SPEC-020). All reads and writes use real SQL; no ORM.
//
// The primary operations are:
//   - ListActiveMemoryIDs: gathers all non-deleted, non-superseded memory IDs
//     for a project, used as Louvain seeds during community detection.
//   - ListCommunities: loads all communities for a project, used for hash-match
//     diff in the service layer.
//   - SaveCommunitiesTx: atomically applies a diff (inserts, updates, deletes)
//     in a single BEGIN/COMMIT transaction.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/juanftp/mneme/internal/model"
)

// ListActiveMemoryIDs returns the IDs of all active (non-deleted,
// non-superseded) memories for the given project. An empty project string
// returns all active memories in the store regardless of project. The
// returned slice is never nil; an empty project returns an empty slice.
//
// This is used by the community detection service to gather seeds for
// BuildGraphForSeeds (SPEC-020 D6).
func (s *MemoryStore) ListActiveMemoryIDs(ctx context.Context, project string) ([]string, error) {
	const qAll = `
		SELECT id FROM memories
		WHERE deleted_at IS NULL AND superseded_by IS NULL`
	const qProject = `
		SELECT id FROM memories
		WHERE deleted_at IS NULL AND superseded_by IS NULL AND project = ?`

	var rows *sql.Rows
	var err error
	if project == "" {
		rows, err = s.db.QueryContext(ctx, qAll)
	} else {
		rows, err = s.db.QueryContext(ctx, qProject, project)
	}
	if err != nil {
		return nil, fmt.Errorf("store: list active memory ids: %w", err)
	}
	defer rows.Close()

	ids := make([]string, 0, 64)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: list active memory ids: scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list active memory ids: rows: %w", err)
	}
	return ids, nil
}

// ListCommunities returns all communities for the given project. An empty
// project returns communities for all projects in the store. EntityIDs is
// not populated by this call — use GetCommunityEntityIDs for that.
// The returned slice is never nil.
func (s *MemoryStore) ListCommunities(ctx context.Context, project string) ([]*model.Community, error) {
	const qAll = `
		SELECT id, project, scope, membership_hash, member_count, modularity,
		       COALESCE(label, ''), created_at, updated_at
		FROM communities`
	const qProject = `
		SELECT id, project, scope, membership_hash, member_count, modularity,
		       COALESCE(label, ''), created_at, updated_at
		FROM communities
		WHERE project = ?`

	var rows *sql.Rows
	var err error
	if project == "" {
		rows, err = s.db.QueryContext(ctx, qAll)
	} else {
		rows, err = s.db.QueryContext(ctx, qProject, project)
	}
	if err != nil {
		return nil, fmt.Errorf("store: list communities: %w", err)
	}
	defer rows.Close()

	out := make([]*model.Community, 0)
	for rows.Next() {
		c, err := scanCommunity(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list communities: scan: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list communities: rows: %w", err)
	}
	return out, nil
}

// GetCommunityEntityIDs returns the entity IDs that are members of the given
// community. The slice is never nil; an unknown community returns an empty slice.
func (s *MemoryStore) GetCommunityEntityIDs(ctx context.Context, communityID string) ([]string, error) {
	const q = `
		SELECT entity_id FROM community_members
		WHERE community_id = ?
		ORDER BY entity_id`

	rows, err := s.db.QueryContext(ctx, q, communityID)
	if err != nil {
		return nil, fmt.Errorf("store: get community entity ids: %w", err)
	}
	defer rows.Close()

	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: get community entity ids: scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: get community entity ids: rows: %w", err)
	}
	return ids, nil
}

// SaveCommunitiesTx atomically applies a community diff in a single
// BEGIN/COMMIT transaction:
//
//   - toInsert: new communities not previously in the DB. Members are
//     inserted into community_members using community.EntityIDs.
//   - toUpdate: existing communities whose membership hash matched; only
//     updated_at, modularity, and member_count are refreshed (no member
//     re-insertion because the membership is identical).
//   - toDelete: community IDs whose membership hash had no match in the
//     new partition. ON DELETE CASCADE removes the community_members rows.
//
// All three slices may be nil or empty; the function is a no-op in that case.
// If any operation fails the transaction is rolled back and the previous
// state is fully preserved.
func (s *MemoryStore) SaveCommunitiesTx(
	ctx context.Context,
	toInsert []*model.Community,
	toUpdate []*model.Community,
	toDelete []string,
) error {
	if len(toInsert) == 0 && len(toUpdate) == 0 && len(toDelete) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: save communities tx: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const insertComm = `
		INSERT INTO communities
			(id, project, scope, membership_hash, member_count, modularity, label, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	const insertMember = `
		INSERT INTO community_members (community_id, entity_id) VALUES (?, ?)`

	for _, c := range toInsert {
		projectVal := sql.NullString{String: c.Project, Valid: c.Project != ""}
		labelVal := sql.NullString{String: c.Label, Valid: c.Label != ""}

		_, err := tx.ExecContext(ctx, insertComm,
			c.ID,
			projectVal,
			string(c.Scope),
			c.MembershipHash,
			c.MemberCount,
			c.Modularity,
			labelVal,
			c.CreatedAt.UTC().Format(time.RFC3339Nano),
			c.UpdatedAt.UTC().Format(time.RFC3339Nano),
		)
		if err != nil {
			return fmt.Errorf("store: save communities tx: insert community %s: %w", c.ID, err)
		}

		for _, entityID := range c.EntityIDs {
			if _, err := tx.ExecContext(ctx, insertMember, c.ID, entityID); err != nil {
				return fmt.Errorf("store: save communities tx: insert member %s -> %s: %w", c.ID, entityID, err)
			}
		}
	}

	const updateComm = `
		UPDATE communities
		SET updated_at = ?, modularity = ?, member_count = ?
		WHERE id = ?`

	for _, c := range toUpdate {
		_, err := tx.ExecContext(ctx, updateComm,
			c.UpdatedAt.UTC().Format(time.RFC3339Nano),
			c.Modularity,
			c.MemberCount,
			c.ID,
		)
		if err != nil {
			return fmt.Errorf("store: save communities tx: update community %s: %w", c.ID, err)
		}
	}

	const deleteComm = `DELETE FROM communities WHERE id = ?`
	for _, id := range toDelete {
		if _, err := tx.ExecContext(ctx, deleteComm, id); err != nil {
			return fmt.Errorf("store: save communities tx: delete community %s: %w", id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: save communities tx: commit: %w", err)
	}
	return nil
}

// scanCommunity reads one row from a *sql.Rows cursor into a *model.Community.
// It expects the columns in the exact order selected by ListCommunities:
// id, project, scope, membership_hash, member_count, modularity, label,
// created_at, updated_at.
func scanCommunity(rows *sql.Rows) (*model.Community, error) {
	c := &model.Community{}
	var projectRaw sql.NullString
	var createdAtStr, updatedAtStr string

	err := rows.Scan(
		&c.ID,
		&projectRaw,
		&c.Scope,
		&c.MembershipHash,
		&c.MemberCount,
		&c.Modularity,
		&c.Label,
		&createdAtStr,
		&updatedAtStr,
	)
	if err != nil {
		return nil, err
	}

	if projectRaw.Valid {
		c.Project = projectRaw.String
	}

	c.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAtStr)
	if err != nil {
		// Fallback for rows stored without nanoseconds.
		c.CreatedAt, err = time.Parse(time.RFC3339, createdAtStr)
		if err != nil {
			return nil, fmt.Errorf("parse created_at %q: %w", createdAtStr, err)
		}
	}

	c.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAtStr)
	if err != nil {
		c.UpdatedAt, err = time.Parse(time.RFC3339, updatedAtStr)
		if err != nil {
			return nil, fmt.Errorf("parse updated_at %q: %w", updatedAtStr, err)
		}
	}

	return c, nil
}
