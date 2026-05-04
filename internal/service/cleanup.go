package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/store"
)

// CleanupOrphanRelationsRequest configures a cleanup pass over the entity
// graph (SPEC-031). DryRun defaults to true so the dangerous path is opt-in:
// callers must set DryRun=false explicitly to delete rows. Scope chooses
// which store(s) to scan: "project" (default), "global", or "all".
type CleanupOrphanRelationsRequest struct {
	Project            string
	Scope              string
	DryRun             bool
	AlsoDeleteEntities bool
}

// OrphanExample is a presentable summary of one orphan relation, included in
// CleanupOrphanRelationsResult so callers can review before applying the
// destructive run.
type OrphanExample struct {
	RelationID string             `json:"relation_id"`
	SourceID   string             `json:"source_id"`
	SourceName string             `json:"source_name"`
	TargetID   string             `json:"target_id"`
	TargetName string             `json:"target_name"`
	Type       model.RelationType `json:"type"`
}

// CleanupOrphanRelationsResult reports the outcome of a cleanup pass. Counts
// reflect what actually happened: in dry-run mode RelationsDeleted and
// EntitiesDeleted are zero and OrphanRelationsFound is the candidate count.
type CleanupOrphanRelationsResult struct {
	OrphanRelationsFound int             `json:"orphan_relations_found"`
	RelationsDeleted     int             `json:"relations_deleted"`
	EntitiesDeleted      int             `json:"entities_deleted"`
	DryRun               bool            `json:"dry_run"`
	Examples             []OrphanExample `json:"examples"`
}

// maxCleanupExamples caps the number of examples returned regardless of
// orphan count, so the response stays bounded for very large projects.
const maxCleanupExamples = 20

// CleanupOrphanRelations finds and optionally deletes relations whose
// endpoints are not connected to any memory through memory_entities. Such
// relations are unreachable from mem_explore and were created by the legacy
// mem_relate path before SPEC-031.
//
// Scope semantics:
//   - "project" (default): scan only the project store for the request's
//     project (or the service's default project when empty).
//   - "global": scan only the global store.
//   - "all": scan both stores in sequence; results are summed.
//
// When AlsoDeleteEntities is true, after each relation deletion the cleanup
// checks whether the source/target entity is still referenced anywhere
// (memory_entities or other relations). Entities that became fully orphan are
// deleted; entities still in use are left alone. EntitiesDeleted reflects how
// many were actually removed.
func (svc *MemoryService) CleanupOrphanRelations(ctx context.Context, req CleanupOrphanRelationsRequest) (*CleanupOrphanRelationsResult, error) {
	scope := req.Scope
	if scope == "" {
		scope = "project"
	}

	project := req.Project
	if project == "" {
		project = svc.project
	}

	result := &CleanupOrphanRelationsResult{
		DryRun:   req.DryRun,
		Examples: make([]OrphanExample, 0, maxCleanupExamples),
	}

	switch scope {
	case "project":
		if err := svc.cleanupOrphansInStore(ctx, svc.projectStore, project, req, result); err != nil {
			return nil, fmt.Errorf("service: cleanup orphans (project): %w", err)
		}
	case "global":
		if err := svc.cleanupOrphansInStore(ctx, svc.globalStore, "", req, result); err != nil {
			return nil, fmt.Errorf("service: cleanup orphans (global): %w", err)
		}
	case "all":
		if err := svc.cleanupOrphansInStore(ctx, svc.projectStore, project, req, result); err != nil {
			return nil, fmt.Errorf("service: cleanup orphans (project): %w", err)
		}
		if err := svc.cleanupOrphansInStore(ctx, svc.globalStore, "", req, result); err != nil {
			return nil, fmt.Errorf("service: cleanup orphans (global): %w", err)
		}
	default:
		return nil, fmt.Errorf("service: cleanup orphans: invalid scope %q (expected project|global|all)", scope)
	}

	return result, nil
}

// cleanupOrphansInStore runs the cleanup pass against a single MemoryStore and
// accumulates counts and examples into result. It is called once per requested
// store (project, global, or both for scope=all).
func (svc *MemoryService) cleanupOrphansInStore(ctx context.Context, st *store.MemoryStore, project string, req CleanupOrphanRelationsRequest, result *CleanupOrphanRelationsResult) error {
	orphans, err := st.FindOrphanRelations(ctx, project)
	if err != nil {
		return fmt.Errorf("find orphan relations: %w", err)
	}
	result.OrphanRelationsFound += len(orphans)

	for _, o := range orphans {
		if len(result.Examples) < maxCleanupExamples {
			result.Examples = append(result.Examples, OrphanExample{
				RelationID: o.RelationID,
				SourceID:   o.SourceID,
				SourceName: o.SourceName,
				TargetID:   o.TargetID,
				TargetName: o.TargetName,
				Type:       o.Type,
			})
		}

		if req.DryRun {
			continue
		}

		if delErr := st.DeleteRelation(ctx, o.RelationID); delErr != nil {
			if errors.Is(delErr, model.ErrRelationNotFound) {
				continue // already gone, idempotent
			}
			return fmt.Errorf("delete relation %s: %w", o.RelationID, delErr)
		}
		result.RelationsDeleted++

		if !req.AlsoDeleteEntities {
			continue
		}

		for _, entID := range []string{o.SourceID, o.TargetID} {
			referenced, refErr := st.EntityIsLinkedOrReferenced(ctx, entID)
			if refErr != nil {
				return fmt.Errorf("check entity references %s: %w", entID, refErr)
			}
			if referenced {
				continue
			}
			if delErr := st.DeleteEntity(ctx, entID); delErr != nil {
				if errors.Is(delErr, model.ErrEntityNotFound) {
					continue
				}
				return fmt.Errorf("delete entity %s: %w", entID, delErr)
			}
			result.EntitiesDeleted++
		}
	}

	return nil
}
