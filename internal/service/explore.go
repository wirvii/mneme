package service

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"unicode/utf8"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/store"
)

// seedKind classifies the format of a seed parameter supplied to Explore.
type seedKind int

const (
	seedUUIDFull   seedKind = iota // 36-char UUID with dashes
	seedUUIDPrefix                  // 8+ hex chars without dashes
	seedTopicKey                    // topic_key (contains "/" or ".")
)

// exploreItem is a candidate node queued during the BFS traversal. The priority
// queue orders items by accumulatedWeight descending so the strongest-path nodes
// are explored first.
type exploreItem struct {
	memoryID          string
	accumulatedWeight float64
	distance          int
	relationType      model.RelationType
	relationID        string // used for BatchTouchRelations
	parentMemoryID    string
}

// explorePQ implements heap.Interface for a max-heap of *exploreItem ordered by
// accumulatedWeight descending.
type explorePQ []*exploreItem

func (pq explorePQ) Len() int            { return len(pq) }
func (pq explorePQ) Less(i, j int) bool  { return pq[i].accumulatedWeight > pq[j].accumulatedWeight }
func (pq explorePQ) Swap(i, j int)       { pq[i], pq[j] = pq[j], pq[i] }
func (pq *explorePQ) Push(x any)         { *pq = append(*pq, x.(*exploreItem)) }
func (pq *explorePQ) Pop() any {
	old := *pq
	n := len(old)
	item := old[n-1]
	*pq = old[:n-1]
	return item
}

// Explore performs a prioritised BFS traversal of the knowledge graph starting
// from a seed memory. The seed can be a full UUID, a short UUID prefix (8+ hex
// chars), or a topic_key. The traversal respects the Depth, Budget, and
// Threshold parameters in req, applying config defaults when values are zero.
//
// Nodes are returned sorted by (distance ASC, accumulated_weight DESC) so the
// most directly and strongly connected memories appear first. The seed itself is
// excluded from the returned Nodes slice.
//
// Relations traversed during exploration have their last_traversed_at updated
// asynchronously via BatchTouchRelations so they are not subject to premature
// decay (D6, SPEC-008).
func (svc *MemoryService) Explore(ctx context.Context, req model.ExploreRequest) (*model.ExploreResponse, error) {
	// Apply defaults.
	cfg := svc.config.Graph
	// nil depth means "use config default"; 0 is valid (returns seed only).
	depth := cfg.ExploreDefaultDepth
	if req.Depth != nil {
		depth = *req.Depth
	}
	budget := req.Budget
	if budget <= 0 {
		budget = cfg.ExploreDefaultBudget
		if budget <= 0 {
			budget = 4000
		}
	}
	threshold := req.Threshold
	if threshold == 0 {
		threshold = cfg.ExpansionThreshold
	}
	fanOutCap := cfg.ExpansionFanOutCap
	if fanOutCap <= 0 {
		fanOutCap = 50
	}
	maxNodes := cfg.ExploreMaxNodes
	if maxNodes <= 0 {
		maxNodes = 200
	}

	// Validate params.
	if depth < 0 || depth > 5 {
		return nil, fmt.Errorf("service: explore: depth must be between 0 and 5")
	}
	if threshold < 0 || threshold > 1.0 {
		return nil, fmt.Errorf("service: explore: threshold must be between 0.0 and 1.0")
	}

	// Resolve the seed memory.
	seed, seedStore, err := svc.resolveSeed(ctx, req.Seed, req.Project)
	if err != nil {
		return nil, fmt.Errorf("service: explore: resolve seed: %w", err)
	}
	if seed == nil {
		return nil, fmt.Errorf("service: explore: %w", model.ErrNotFound)
	}

	// Estimate seed tokens and start budget tracking.
	seedTokens := estimateTokens(seed.Title + seed.Content)
	tokensUsed := seedTokens

	slog.DebugContext(ctx, "explore start",
		"event", "mem_explore",
		"seed_id", seed.ID,
		"depth", depth,
		"budget", budget,
		"threshold", threshold,
	)

	// When depth is 0 return empty nodes immediately (seed is not counted as a discovery).
	if depth == 0 {
		return &model.ExploreResponse{
			SeedID:          seed.ID,
			SeedTitle:       seed.Title,
			Nodes:           []model.ExploreNode{},
			TotalNodes:      0,
			TokensUsed:      tokensUsed,
			MaxDepthReached: 0,
		}, nil
	}

	// visited maps memoryID → ExploreNode (also acts as the visited set).
	visited := make(map[string]model.ExploreNode, 64)
	visited[seed.ID] = model.ExploreNode{MemoryID: seed.ID, AccumulatedWeight: 1.0}

	// touchIDs collects relation IDs for the async BatchTouchRelations call.
	touchIDs := make([]string, 0, 64)

	// Initialise the priority queue and seed it with depth-1 neighbors.
	pq := make(explorePQ, 0, 64)
	heap.Init(&pq)

	seedEntities, err := seedStore.GetMemoryEntities(ctx, seed.ID)
	if err != nil {
		return nil, fmt.Errorf("service: explore: get seed entities: %w", err)
	}

	for _, entity := range seedEntities {
		rels, relErr := seedStore.GetStrongRelations(ctx, entity.ID, threshold, fanOutCap)
		if relErr != nil {
			return nil, fmt.Errorf("service: explore: get seed relations: %w", relErr)
		}
		for _, rel := range rels {
			neighborEntityID := otherEnd(rel, entity.ID)
			memIDs, memErr := seedStore.GetEntityMemoryIDs(ctx, neighborEntityID)
			if memErr != nil {
				return nil, fmt.Errorf("service: explore: get seed neighbor ids: %w", memErr)
			}
			for _, memID := range memIDs {
				if memID == seed.ID {
					continue
				}
				heap.Push(&pq, &exploreItem{
					memoryID:          memID,
					accumulatedWeight: rel.Weight,
					distance:          1,
					relationType:      rel.Type,
					relationID:        rel.ID,
					parentMemoryID:    seed.ID,
				})
			}
			touchIDs = append(touchIDs, rel.ID)
		}
	}

	// BFS loop.
	for pq.Len() > 0 && len(visited) < maxNodes+1 { // +1 for the seed itself
		item := heap.Pop(&pq).(*exploreItem)

		if item.distance > depth {
			continue
		}

		if existing, already := visited[item.memoryID]; already {
			// Update if this path is stronger than the previously recorded one.
			if item.accumulatedWeight > existing.AccumulatedWeight {
				existing.AccumulatedWeight = item.accumulatedWeight
				existing.RelationType = item.relationType
				existing.ParentMemoryID = item.parentMemoryID
				visited[item.memoryID] = existing
			}
			continue
		}

		// Load lightweight metadata (avoids reading full content).
		meta, metaErr := seedStore.GetMemoryMetadata(ctx, item.memoryID)
		if metaErr != nil {
			return nil, fmt.Errorf("service: explore: get metadata: %w", metaErr)
		}
		if meta == nil {
			continue // memory was deleted between enqueue and pop
		}

		// Token budget check.
		tokenCost := (utf8.RuneCountInString(meta.Title) + meta.ContentLen) / 3
		if tokenCost < 1 {
			tokenCost = 1
		}
		if tokensUsed+tokenCost > budget {
			slog.DebugContext(ctx, "explore budget exceeded",
				"event", "mem_explore_budget_exceeded",
				"memory_id", item.memoryID,
				"tokens_used", tokensUsed,
				"token_cost", tokenCost,
				"budget", budget,
			)
			continue // skip this node; try the next one in the queue
		}
		tokensUsed += tokenCost

		node := model.ExploreNode{
			MemoryID:          meta.ID,
			ParentMemoryID:    item.parentMemoryID,
			Title:             meta.Title,
			TopicKey:          meta.TopicKey,
			Type:              meta.Type,
			Distance:          item.distance,
			AccumulatedWeight: item.accumulatedWeight,
			RelationType:      item.relationType,
			TokenEstimate:     tokenCost,
		}
		visited[meta.ID] = node

		// Expand neighbors if we have remaining depth.
		if item.distance < depth {
			entities, entErr := seedStore.GetMemoryEntities(ctx, meta.ID)
			if entErr != nil {
				return nil, fmt.Errorf("service: explore: expand entities: %w", entErr)
			}
			for _, entity := range entities {
				rels, relErr := seedStore.GetStrongRelations(ctx, entity.ID, threshold, fanOutCap)
				if relErr != nil {
					return nil, fmt.Errorf("service: explore: expand relations: %w", relErr)
				}
				for _, rel := range rels {
					neighborEntityID := otherEnd(rel, entity.ID)
					neighborMemIDs, memErr := seedStore.GetEntityMemoryIDs(ctx, neighborEntityID)
					if memErr != nil {
						return nil, fmt.Errorf("service: explore: expand neighbor ids: %w", memErr)
					}
					for _, nMemID := range neighborMemIDs {
						if _, alreadyVisited := visited[nMemID]; alreadyVisited {
							continue
						}
						heap.Push(&pq, &exploreItem{
							memoryID:          nMemID,
							accumulatedWeight: item.accumulatedWeight * rel.Weight,
							distance:          item.distance + 1,
							relationType:      rel.Type,
							relationID:        rel.ID,
							parentMemoryID:    meta.ID,
						})
					}
					touchIDs = append(touchIDs, rel.ID)
				}
			}
		}
	}

	// Build result: exclude seed, sort by (distance ASC, accumulated_weight DESC).
	nodes := make([]model.ExploreNode, 0, len(visited)-1)
	for id, n := range visited {
		if id == seed.ID {
			continue
		}
		nodes = append(nodes, n)
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Distance != nodes[j].Distance {
			return nodes[i].Distance < nodes[j].Distance
		}
		return nodes[i].AccumulatedWeight > nodes[j].AccumulatedWeight
	})

	// Compute max depth reached.
	maxDepth := 0
	for _, n := range nodes {
		if n.Distance > maxDepth {
			maxDepth = n.Distance
		}
	}

	// Fire-and-forget batch touch for traversed relations (D6, SPEC-008).
	if len(touchIDs) > 0 {
		go svc.batchTouchRelations(ctx, touchIDs)
	}

	slog.DebugContext(ctx, "explore done",
		"event", "mem_explore",
		"seed_id", seed.ID,
		"nodes", len(nodes),
		"tokens_used", tokensUsed,
		"max_depth", maxDepth,
	)

	return &model.ExploreResponse{
		SeedID:          seed.ID,
		SeedTitle:       seed.Title,
		Nodes:           nodes,
		TotalNodes:      len(nodes),
		TokensUsed:      tokensUsed,
		MaxDepthReached: maxDepth,
	}, nil
}

// resolveSeed resolves the seed parameter to a *model.Memory and the store it
// lives in. Accepts full UUID, 8+ hex prefix, or topic_key. Returns (nil, nil,
// nil) when the seed is not found in either store.
func (svc *MemoryService) resolveSeed(ctx context.Context, seed, project string) (*model.Memory, *store.MemoryStore, error) {
	kind := classifySeed(seed)

	switch kind {
	case seedUUIDFull:
		m, s, err := svc.getFromEitherStore(ctx, seed)
		if err != nil {
			return nil, nil, err
		}
		return m, s, nil

	case seedUUIDPrefix:
		// Try project store first.
		m, err := svc.projectStore.GetByIDPrefix(ctx, seed)
		if err != nil {
			if errors.Is(err, model.ErrAmbiguousSeed) {
				return nil, nil, fmt.Errorf("resolve seed: %w", model.ErrAmbiguousSeed)
			}
			return nil, nil, fmt.Errorf("resolve seed: project store: %w", err)
		}
		if m != nil {
			return m, svc.projectStore, nil
		}
		// Try global store.
		m, err = svc.globalStore.GetByIDPrefix(ctx, seed)
		if err != nil {
			if errors.Is(err, model.ErrAmbiguousSeed) {
				return nil, nil, fmt.Errorf("resolve seed: %w", model.ErrAmbiguousSeed)
			}
			return nil, nil, fmt.Errorf("resolve seed: global store: %w", err)
		}
		if m != nil {
			return m, svc.globalStore, nil
		}
		return nil, nil, nil

	default: // seedTopicKey
		// Determine project slug to use for lookup.
		proj := project
		if proj == "" {
			proj = svc.project
		}
		m, err := svc.projectStore.GetByTopicKey(ctx, seed, proj)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve seed: project store: %w", err)
		}
		if m != nil {
			return m, svc.projectStore, nil
		}
		// Fallback to global store (global memories have project="" in GetByTopicKey).
		m, err = svc.globalStore.GetByTopicKey(ctx, seed, "")
		if err != nil {
			return nil, nil, fmt.Errorf("resolve seed: global store: %w", err)
		}
		if m != nil {
			return m, svc.globalStore, nil
		}
		return nil, nil, nil
	}
}

// classifySeed returns the seedKind for the given seed string.
func classifySeed(seed string) seedKind {
	if looksLikeUUID(seed) {
		return seedUUIDFull
	}
	if len(seed) >= 8 && isAllHex(seed) {
		return seedUUIDPrefix
	}
	return seedTopicKey
}

// isAllHex reports whether every character in s is a valid hexadecimal digit.
// Used to distinguish UUID short prefixes from topic_key strings.
func isAllHex(s string) bool {
	for _, ch := range s {
		if !isHexRune(ch) {
			return false
		}
	}
	return len(s) > 0
}

// otherEnd returns the entity ID on the opposite end of rel from entityID.
// Relations are directed (source_id → target_id). When entityID is the source,
// the other end is the target; when it is the target, the other end is the source.
func otherEnd(rel *model.Relation, entityID string) string {
	if rel.SourceID == entityID {
		return rel.TargetID
	}
	return rel.SourceID
}
