// Package service community.go — orchestrates Louvain community detection and
// atomic persistence of detected communities (SPEC-020).
//
// DetectAndPersistCommunities is the single entry point for the consolidation
// pipeline (and future callers). It:
//   1. Gathers all active memory IDs for the scope/project.
//   2. Builds the entity-level knowledge graph via BuildGraphForSeeds.
//   3. Runs Louvain community detection.
//   4. Diffs the result against existing persisted communities.
//   5. Applies inserts, updates, and deletes atomically via SaveCommunitiesTx.
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/juanftp/mneme/internal/graph"
	"github.com/juanftp/mneme/internal/model"
)

// communityMembershipHash computes a stable SHA-256 digest of a community's
// member entity IDs. The IDs are sorted before hashing so that the hash is
// independent of the order in which Louvain assigns members.
//
// SHA-256 is collision-resistant for the set sizes encountered in mneme
// (typically <1000 members per community) and is part of the Go stdlib
// (crypto/sha256), adding zero external dependencies.
func communityMembershipHash(entityIDs []string) string {
	sorted := make([]string, len(entityIDs))
	copy(sorted, entityIDs)
	sort.Strings(sorted)
	h := sha256.Sum256([]byte(strings.Join(sorted, ",")))
	return hex.EncodeToString(h[:])
}

// DetectAndPersistCommunities runs the full community detection and persistence
// cycle for the given scope and project:
//
//  1. If detection is disabled in config (CommunityDetectionEnabled=false), the
//     function returns a zero-value DetectionResult immediately.
//  2. All active (non-deleted, non-superseded) memory IDs are gathered as seeds.
//  3. A SparseGraph is built with MaxNodes=10000 to capture the full topology.
//  4. Louvain community detection runs on the graph.
//  5. Communities below CommunityMinSize are discarded (noise reduction).
//  6. The new partition is diffed against the existing persisted communities using
//     SHA-256 membership hashes:
//     - Hash match  → UPDATE (refresh updated_at, modularity, member_count).
//     - No match    → INSERT (new UUIDv7, new members).
//     - Unmatched existing → DELETE (stale community).
//  7. All changes are applied in a single atomic transaction.
//
// Edge cases:
//   - Zero seeds or fewer nodes than CommunityMinSize → early return, zero result.
//   - Graph build error → lenient (BuildGraphForSeeds never errors; lenient contract).
//   - Louvain error → wrapped and returned.
func (svc *MemoryService) DetectAndPersistCommunities(
	ctx context.Context,
	scope model.Scope,
	project string,
) (*model.DetectionResult, error) {
	start := time.Now()
	result := &model.DetectionResult{}

	if !svc.config.Graph.CommunityDetectionEnabled {
		return result, nil
	}

	minSize := svc.config.Graph.CommunityMinSize
	if minSize <= 0 {
		// Treat 0 as "use default 3" per spec D8 and config validation rules.
		minSize = 3
	}

	st := svc.storeFor(scope)

	// Step 1: gather all active memory IDs as Louvain seeds.
	memoryIDs, err := st.ListActiveMemoryIDs(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("service: detect communities: list memories: %w", err)
	}
	if len(memoryIDs) < minSize {
		// Not enough memories for meaningful detection.
		result.Duration = time.Since(start)
		return result, nil
	}

	// Step 2: build entity-level graph with expanded cap for full-project detection.
	opts := DefaultGraphBuildOptions()
	opts.MaxNodes = 10000
	sparseGraph, _ := svc.BuildGraphForSeeds(ctx, memoryIDs, opts)
	if len(sparseGraph.Nodes) < minSize {
		// Fewer graph nodes than threshold — no meaningful communities.
		result.Duration = time.Since(start)
		return result, nil
	}

	// Step 3: run Louvain.
	louvainResult, err := graph.Louvain(*sparseGraph, graph.DefaultLouvainOptions())
	if err != nil {
		return nil, fmt.Errorf("service: detect communities: louvain: %w", err)
	}
	result.ModularityFinal = louvainResult.Modularity

	// Step 4: filter communities below minimum size.
	type newComm struct {
		hash      string
		entityIDs []string
		modularity float64
	}
	var significant []newComm
	for _, c := range louvainResult.Communities {
		members := make([]string, len(c.Members))
		for i, m := range c.Members {
			members[i] = string(m)
		}
		if len(members) < minSize {
			continue
		}
		h := communityMembershipHash(members)
		significant = append(significant, newComm{
			hash:      h,
			entityIDs: members,
			modularity: louvainResult.Modularity,
		})
	}

	// Step 5: load existing communities for diff.
	existing, err := st.ListCommunities(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("service: detect communities: list existing: %w", err)
	}
	existingByHash := make(map[string]*model.Community, len(existing))
	for _, c := range existing {
		existingByHash[c.MembershipHash] = c
	}

	// Step 6: diff — three categories.
	now := time.Now().UTC()
	var toInsert []*model.Community
	var toUpdate []*model.Community
	matched := make(map[string]bool, len(significant))

	for _, nc := range significant {
		if ec, ok := existingByHash[nc.hash]; ok {
			// UPDATE: same membership, refresh metadata.
			ec.UpdatedAt = now
			ec.Modularity = nc.modularity
			ec.MemberCount = len(nc.entityIDs)
			toUpdate = append(toUpdate, ec)
			matched[nc.hash] = true
		} else {
			// INSERT: new community.
			id, idErr := uuid.NewV7()
			if idErr != nil {
				return nil, fmt.Errorf("service: detect communities: generate id: %w", idErr)
			}
			toInsert = append(toInsert, &model.Community{
				ID:             id.String(),
				Project:        project,
				Scope:          scope,
				MembershipHash: nc.hash,
				MemberCount:    len(nc.entityIDs),
				Modularity:     nc.modularity,
				CreatedAt:      now,
				UpdatedAt:      now,
				EntityIDs:      nc.entityIDs,
			})
		}
	}

	var toDelete []string
	for hash, ec := range existingByHash {
		if !matched[hash] {
			toDelete = append(toDelete, ec.ID)
		}
	}

	// Step 7: persist atomically.
	if err := st.SaveCommunitiesTx(ctx, toInsert, toUpdate, toDelete); err != nil {
		return nil, fmt.Errorf("service: detect communities: save: %w", err)
	}

	result.NewCommunities = len(toInsert)
	result.UpdatedCommunities = len(toUpdate)
	result.DeletedCommunities = len(toDelete)
	result.TotalCommunities = len(toInsert) + len(toUpdate)
	result.Duration = time.Since(start)

	return result, nil
}
