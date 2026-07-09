package service_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// BenchmarkSearch_GraphExpansion_5K measures the overhead of 1-hop graph
// expansion against a corpus of 5K memories, 10K relations, and ~20K
// memory-entity links. Per SPEC-007 acceptance criterion 3, the graph channel
// should add <50ms to search latency relative to include_graph=false.
//
// Run with: go test -tags fts5 -bench=BenchmarkSearch_GraphExpansion_5K -benchtime=5s ./internal/service/
func BenchmarkSearch_GraphExpansion_5K(b *testing.B) {
	projectDB, err := db.OpenMemory()
	if err != nil {
		b.Fatalf("open project db: %v", err)
	}
	globalDB, err := db.OpenMemory()
	if err != nil {
		b.Fatalf("open global db: %v", err)
	}
	b.Cleanup(func() { projectDB.Close(); globalDB.Close() })

	ps := store.NewMemoryStore(projectDB)
	gs := store.NewMemoryStore(globalDB)
	cfg := config.Default()
	cfg.Graph.ExpansionEnabled = true
	cfg.Graph.ExpansionThreshold = 0.3
	cfg.Graph.ExpansionFanOutCap = 50
	cfg.Graph.ExpansionSeedTopK = 10

	svc := service.NewMemoryService(ps, gs, cfg, "bench/project", embed.NopEmbedder{})
	ctx := context.Background()

	const (
		numMemories  = 500  // scaled down from 5K for in-memory test speed
		numEntities  = 100  // entities shared across memories
		relPerEntity = 5    // relations per entity (~10K total at 5K scale)
	)

	// Create entities.
	entities := make([]*model.Entity, numEntities)
	for i := 0; i < numEntities; i++ {
		e, err := ps.CreateEntity(ctx, &model.Entity{
			Name:    fmt.Sprintf("entity-%d", i),
			Kind:    model.KindModule,
			Project: "bench/project",
		})
		if err != nil {
			b.Fatalf("CreateEntity %d: %v", i, err)
		}
		entities[i] = e
	}

	// Create relations between entities.
	for i := 0; i < numEntities; i++ {
		for j := 1; j <= relPerEntity && i+j < numEntities; j++ {
			_, err := ps.CreateRelation(ctx, &model.Relation{
				SourceID: entities[i].ID,
				TargetID: entities[i+j].ID,
				Type:     model.RelRelatedTo,
				Weight:   0.5 + float64(j)*0.05,
			})
			if err != nil {
				b.Fatalf("CreateRelation: %v", err)
			}
		}
	}

	// Create memories and link them to entities.
	for i := 0; i < numMemories; i++ {
		resp, err := svc.Save(ctx, model.SaveRequest{
			Title:   fmt.Sprintf("memory-%d benchmark test content", i),
			Content: fmt.Sprintf("benchmark memory content %d for testing graph expansion performance", i),
		})
		if err != nil {
			b.Fatalf("Save memory %d: %v", i, err)
		}
		// Link each memory to a couple of entities.
		eIdx := i % numEntities
		if err := ps.LinkMemoryEntity(ctx, resp.ID, entities[eIdx].ID, "mention"); err != nil {
			b.Fatalf("LinkMemoryEntity: %v", err)
		}
	}

	b.ResetTimer()

	b.Run("with_graph", func(b *testing.B) {
		graphOn := true
		for i := 0; i < b.N; i++ {
			_, err := svc.Search(ctx, model.SearchRequest{
				Query:        "benchmark memory content",
				IncludeGraph: &graphOn,
				Limit:        10,
			})
			if err != nil {
				b.Fatalf("Search: %v", err)
			}
		}
	})

	b.Run("without_graph", func(b *testing.B) {
		graphOff := false
		for i := 0; i < b.N; i++ {
			_, err := svc.Search(ctx, model.SearchRequest{
				Query:        "benchmark memory content",
				IncludeGraph: &graphOff,
				Limit:        10,
			})
			if err != nil {
				b.Fatalf("Search: %v", err)
			}
		}
	})
}

// BenchmarkRebuildGraph_5K measures the performance of RebuildGraph against a
// corpus of 500 memories (scaled from the 5K target) with overlapping file-path
// entities. Per SPEC-009 AC6 the full rebuild should complete in < 5s for 5K
// memories / 20K memory_entities. At 500 memories the benchmark serves as a
// baseline; the 5K target can be validated by scaling numMemories to 5000.
//
// Run with: go test -tags fts5 -bench=BenchmarkRebuildGraph_5K -benchtime=3x ./internal/service/
func BenchmarkRebuildGraph_5K(b *testing.B) {
	projectDB, err := db.OpenMemory()
	if err != nil {
		b.Fatalf("open project db: %v", err)
	}
	globalDB, err := db.OpenMemory()
	if err != nil {
		b.Fatalf("open global db: %v", err)
	}
	b.Cleanup(func() { projectDB.Close(); globalDB.Close() })

	ps := store.NewMemoryStore(projectDB)
	gs := store.NewMemoryStore(globalDB)
	cfg := config.Default()

	svc := service.NewMemoryService(ps, gs, cfg, "bench/rebuild", embed.NopEmbedder{})
	ctx := context.Background()

	// Create 500 memories (scaled from 5K) with overlapping file-path content
	// so the SQL JOIN produces a meaningful number of candidate pairs.
	const numMemories = 500
	// Each memory mentions ~5 file paths sampled from a pool of 50.
	// This gives ~50 memories per entity → lots of pairs.
	paths := make([]string, 50)
	for i := range paths {
		paths[i] = fmt.Sprintf("internal/store/entity%d.go", i)
	}

	for i := 0; i < numMemories; i++ {
		// Pick 5 consecutive paths from the pool (wrapping around).
		var content string
		for j := 0; j < 5; j++ {
			content += paths[(i+j)%len(paths)] + " "
		}
		if _, err := svc.Save(ctx, model.SaveRequest{
			Title:   fmt.Sprintf("bench-rebuild-memory-%d", i),
			Content: content,
		}); err != nil {
			b.Fatalf("Save memory %d: %v", i, err)
		}
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Use Force=true so each benchmark iteration starts fresh.
		result, err := svc.RebuildGraph(ctx, model.RebuildRequest{
			Scope:     "project",
			MinShared: 2,
			Force:     true,
		})
		if err != nil {
			b.Fatalf("RebuildGraph: %v", err)
		}
		b.ReportMetric(float64(result.MemoriesScanned), "memories_scanned/op")
		b.ReportMetric(float64(result.RelationsCreated+result.RelationsExisting), "relations/op")
	}
}

// BenchmarkExplore_Depth3_5K measures the performance of mem_explore with
// depth=3 against a corpus of 500 memories and 100 entities with 5 relations
// per entity. Per SPEC-008 acceptance criterion 6, the traversal should complete
// in < 200ms for depth=3 on 5K memories / 10K relations.
//
// Run with: go test -tags fts5 -bench=BenchmarkExplore_Depth3_5K -benchtime=5s ./internal/service/
func BenchmarkExplore_Depth3_5K(b *testing.B) {
	projectDB, err := db.OpenMemory()
	if err != nil {
		b.Fatalf("open project db: %v", err)
	}
	globalDB, err := db.OpenMemory()
	if err != nil {
		b.Fatalf("open global db: %v", err)
	}
	b.Cleanup(func() { projectDB.Close(); globalDB.Close() })

	ps := store.NewMemoryStore(projectDB)
	gs := store.NewMemoryStore(globalDB)
	cfg := config.Default()
	cfg.Graph.ExpansionEnabled = true
	cfg.Graph.ExpansionThreshold = 0.3
	cfg.Graph.ExpansionFanOutCap = 50
	cfg.Graph.ExploreMaxNodes = 200

	svc := service.NewMemoryService(ps, gs, cfg, "bench/explore", embed.NopEmbedder{})
	ctx := context.Background()

	const (
		numMemories  = 500
		numEntities  = 100
		relPerEntity = 5
	)

	// Create entities.
	entities := make([]*model.Entity, numEntities)
	for i := 0; i < numEntities; i++ {
		e, err := ps.CreateEntity(ctx, &model.Entity{
			Name:    fmt.Sprintf("explore-entity-%d", i),
			Kind:    model.KindModule,
			Project: "bench/explore",
		})
		if err != nil {
			b.Fatalf("CreateEntity %d: %v", i, err)
		}
		entities[i] = e
	}

	// Create relations between entities.
	for i := 0; i < numEntities; i++ {
		for j := 1; j <= relPerEntity && i+j < numEntities; j++ {
			_, err := ps.CreateRelation(ctx, &model.Relation{
				SourceID: entities[i].ID,
				TargetID: entities[i+j].ID,
				Type:     model.RelRelatedTo,
				Weight:   0.5 + float64(j)*0.05,
			})
			if err != nil {
				b.Fatalf("CreateRelation: %v", err)
			}
		}
	}

	// Create memories and link them to entities. Track first memory as seed.
	var seedID string
	for i := 0; i < numMemories; i++ {
		resp, err := svc.Save(ctx, model.SaveRequest{
			Title:   fmt.Sprintf("explore-memory-%d", i),
			Content: fmt.Sprintf("benchmark explore memory content %d for testing graph traversal performance", i),
		})
		if err != nil {
			b.Fatalf("Save memory %d: %v", i, err)
		}
		if i == 0 {
			seedID = resp.ID
		}
		eIdx := i % numEntities
		if err := ps.LinkMemoryEntity(ctx, resp.ID, entities[eIdx].ID, "mention"); err != nil {
			b.Fatalf("LinkMemoryEntity: %v", err)
		}
	}

	depth := 3
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := svc.Explore(ctx, model.ExploreRequest{
			Seed:   seedID,
			Depth:  &depth,
			Budget: 100_000,
		})
		if err != nil {
			b.Fatalf("Explore: %v", err)
		}
	}
}

// BenchmarkSearch_PPRMode_5K measures the overhead of the PPR graph channel
// (BuildGraphForSeeds + PPR + entity-to-memory mapping) against a corpus of
// 500 memories and 100 entities with 5 relations per entity. Per SPEC-017 AC4,
// the full search (FTS5 + vector + PPR graph) must complete in <100ms total.
//
// Run with: go test -tags fts5 -bench=BenchmarkSearch_PPRMode_5K -benchtime=5s ./internal/service/
func BenchmarkSearch_PPRMode_5K(b *testing.B) {
	projectDB, err := db.OpenMemory()
	if err != nil {
		b.Fatalf("open project db: %v", err)
	}
	globalDB, err := db.OpenMemory()
	if err != nil {
		b.Fatalf("open global db: %v", err)
	}
	b.Cleanup(func() { projectDB.Close(); globalDB.Close() })

	ps := store.NewMemoryStore(projectDB)
	gs := store.NewMemoryStore(globalDB)
	cfg := config.Default()
	cfg.Graph.ExpansionEnabled = true
	cfg.Graph.ExpansionThreshold = 0.3
	cfg.Graph.ExpansionFanOutCap = 50
	cfg.Graph.ExpansionSeedTopK = 10
	cfg.Graph.GraphMode = "ppr"

	svc := service.NewMemoryService(ps, gs, cfg, "bench/ppr", embed.NopEmbedder{})
	ctx := context.Background()

	const (
		numMemories  = 500
		numEntities  = 100
		relPerEntity = 5
	)

	entities := make([]*model.Entity, numEntities)
	for i := 0; i < numEntities; i++ {
		e, err := ps.CreateEntity(ctx, &model.Entity{
			Name:    fmt.Sprintf("ppr-entity-%d", i),
			Kind:    model.KindModule,
			Project: "bench/ppr",
		})
		if err != nil {
			b.Fatalf("CreateEntity %d: %v", i, err)
		}
		entities[i] = e
	}

	for i := 0; i < numEntities; i++ {
		for j := 1; j <= relPerEntity && i+j < numEntities; j++ {
			_, err := ps.CreateRelation(ctx, &model.Relation{
				SourceID: entities[i].ID,
				TargetID: entities[i+j].ID,
				Type:     model.RelRelatedTo,
				Weight:   0.5 + float64(j)*0.05,
			})
			if err != nil {
				b.Fatalf("CreateRelation: %v", err)
			}
		}
	}

	for i := 0; i < numMemories; i++ {
		resp, err := svc.Save(ctx, model.SaveRequest{
			Title:   fmt.Sprintf("ppr-memory-%d benchmark ppr mode content", i),
			Content: fmt.Sprintf("ppr benchmark memory content %d for testing ppr graph channel performance", i),
		})
		if err != nil {
			b.Fatalf("Save memory %d: %v", i, err)
		}
		eIdx := i % numEntities
		if err := ps.LinkMemoryEntity(ctx, resp.ID, entities[eIdx].ID, "mention"); err != nil {
			b.Fatalf("LinkMemoryEntity: %v", err)
		}
	}

	b.ResetTimer()

	b.Run("ppr_mode", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, err := svc.Search(ctx, model.SearchRequest{
				Query: "ppr benchmark memory content",
				Limit: 10,
			})
			if err != nil {
				b.Fatalf("Search PPR: %v", err)
			}
		}
	})

	b.Run("1hop_mode", func(b *testing.B) {
		cfg1Hop := config.Default()
		cfg1Hop.Graph.ExpansionEnabled = true
		cfg1Hop.Graph.ExpansionThreshold = 0.3
		cfg1Hop.Graph.ExpansionFanOutCap = 50
		cfg1Hop.Graph.ExpansionSeedTopK = 10
		cfg1Hop.Graph.GraphMode = "1hop"
		svc1Hop := service.NewMemoryService(ps, gs, cfg1Hop, "bench/ppr", embed.NopEmbedder{})

		for i := 0; i < b.N; i++ {
			_, err := svc1Hop.Search(ctx, model.SearchRequest{
				Query: "ppr benchmark memory content",
				Limit: 10,
			})
			if err != nil {
				b.Fatalf("Search 1hop: %v", err)
			}
		}
	})
}

// BenchmarkContext_GraphFocus_5K measures the overhead of context assembly
// with graph-focused expansion (PPR mode). Per SPEC-017 AC4, the total
// context call should complete in <150ms for 5K memories.
//
// Run with: go test -tags fts5 -bench=BenchmarkContext_GraphFocus_5K -benchtime=5s ./internal/service/
func BenchmarkContext_GraphFocus_5K(b *testing.B) {
	projectDB, err := db.OpenMemory()
	if err != nil {
		b.Fatalf("open project db: %v", err)
	}
	globalDB, err := db.OpenMemory()
	if err != nil {
		b.Fatalf("open global db: %v", err)
	}
	b.Cleanup(func() { projectDB.Close(); globalDB.Close() })

	ps := store.NewMemoryStore(projectDB)
	gs := store.NewMemoryStore(globalDB)
	cfg := config.Default()
	cfg.Graph.ExpansionEnabled = true
	cfg.Graph.ExpansionThreshold = 0.3
	cfg.Graph.ExpansionFanOutCap = 50
	cfg.Graph.ExpansionSeedTopK = 10
	cfg.Graph.GraphMode = "ppr"

	svc := service.NewMemoryService(ps, gs, cfg, "bench/ctx-ppr", embed.NopEmbedder{})
	ctx := context.Background()

	const (
		numMemories  = 500
		numEntities  = 100
		relPerEntity = 5
	)

	entities := make([]*model.Entity, numEntities)
	for i := 0; i < numEntities; i++ {
		e, err := ps.CreateEntity(ctx, &model.Entity{
			Name:    fmt.Sprintf("ctx-ppr-entity-%d", i),
			Kind:    model.KindModule,
			Project: "bench/ctx-ppr",
		})
		if err != nil {
			b.Fatalf("CreateEntity %d: %v", i, err)
		}
		entities[i] = e
	}

	for i := 0; i < numEntities; i++ {
		for j := 1; j <= relPerEntity && i+j < numEntities; j++ {
			_, err := ps.CreateRelation(ctx, &model.Relation{
				SourceID: entities[i].ID,
				TargetID: entities[i+j].ID,
				Type:     model.RelRelatedTo,
				Weight:   0.5 + float64(j)*0.05,
			})
			if err != nil {
				b.Fatalf("CreateRelation: %v", err)
			}
		}
	}

	for i := 0; i < numMemories; i++ {
		resp, err := svc.Save(ctx, model.SaveRequest{
			Title:   fmt.Sprintf("ctx-ppr-memory-%d architecture benchmark", i),
			Content: fmt.Sprintf("context ppr benchmark content %d for architecture focus testing", i),
		})
		if err != nil {
			b.Fatalf("Save memory %d: %v", i, err)
		}
		eIdx := i % numEntities
		if err := ps.LinkMemoryEntity(ctx, resp.ID, entities[eIdx].ID, "mention"); err != nil {
			b.Fatalf("LinkMemoryEntity: %v", err)
		}
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := svc.Context(ctx, model.ContextRequest{
			Focus: "architecture benchmark",
		})
		if err != nil {
			b.Fatalf("Context PPR: %v", err)
		}
	}
}

// BenchmarkContext_CommunityPacking measures context assembly with community
// packing enabled against a corpus of ~500 memories across 10 communities.
// Target: <150ms total per call (SPEC-022 AC-6). Run with:
//
//	CGO_ENABLED=1 go test -tags fts5 -bench=BenchmarkContext_CommunityPacking -benchtime=5s ./internal/service/
func BenchmarkContext_CommunityPacking(b *testing.B) {
	projectDB, err := db.OpenMemory()
	if err != nil {
		b.Fatalf("open project db: %v", err)
	}
	globalDB, err := db.OpenMemory()
	if err != nil {
		b.Fatalf("open global db: %v", err)
	}
	b.Cleanup(func() { projectDB.Close(); globalDB.Close() })

	ps := store.NewMemoryStore(projectDB)
	gs := store.NewMemoryStore(globalDB)
	cfg := config.Default()
	cfg.Context.ContextPackingMode = "communities"
	cfg.Context.ClusterOverviewsBudget = 1500
	cfg.Context.TopClusterMaxMembers = 10
	cfg.Graph.CommunityMinSize = 1

	svc := service.NewMemoryService(ps, gs, cfg, "bench/commpack", embed.NopEmbedder{})
	ctx := context.Background()

	const (
		numMemories    = 500
		numCommunities = 10
		entitiesPerC   = 5
	)

	// Create entities and communities.
	allEntityIDs := make([]string, 0, numCommunities*entitiesPerC)
	communities := make([]*model.Community, 0, numCommunities)
	for c := 0; c < numCommunities; c++ {
		entityIDs := make([]string, 0, entitiesPerC)
		for e := 0; e < entitiesPerC; e++ {
			ent, entErr := ps.FindOrCreateEntity(ctx,
				fmt.Sprintf("bench-c%d-e%d", c, e),
				model.KindConcept,
				"bench/commpack",
			)
			if entErr != nil {
				b.Fatalf("FindOrCreateEntity: %v", entErr)
			}
			entityIDs = append(entityIDs, ent.ID)
			allEntityIDs = append(allEntityIDs, ent.ID)
		}
		communities = append(communities, &model.Community{
			ID:             fmt.Sprintf("bench-comm-%d", c),
			Project:        "bench/commpack",
			Scope:          model.ScopeProject,
			MembershipHash: fmt.Sprintf("hash-bench-%d", c),
			MemberCount:    entitiesPerC,
			Modularity:     0.4,
			Label:          fmt.Sprintf("Bench Community %d", c),
			EntityIDs:      entityIDs,
		})
	}
	if saveErr := ps.SaveCommunitiesTx(ctx, communities, nil, nil); saveErr != nil {
		b.Fatalf("SaveCommunitiesTx: %v", saveErr)
	}

	// Create synthesis memories for each community.
	for _, comm := range communities {
		imp := 0.85
		synthTopicKey := "synthesis/community-" + comm.ID
		_, _, uErr := ps.Upsert(ctx, &model.Memory{
			Type:       model.TypeSynthesis,
			Scope:      model.ScopeProject,
			Project:    "bench/commpack",
			Title:      "Cluster: " + comm.Label,
			Content:    fmt.Sprintf("## Cluster Overview\n%s\n## Top Members\n- member 1\n## Aggregate Metadata\ncount: %d", comm.Label, comm.MemberCount),
			TopicKey:   synthTopicKey,
			Importance: imp,
		})
		if uErr != nil {
			b.Fatalf("Upsert synthesis: %v", uErr)
		}
	}

	// Create memories linked to entities.
	for i := 0; i < numMemories; i++ {
		resp, saveErr := svc.Save(ctx, model.SaveRequest{
			Title:   fmt.Sprintf("bench-commpack-memory-%d", i),
			Content: fmt.Sprintf("community packing benchmark content %d architecture focus", i),
		})
		if saveErr != nil {
			b.Fatalf("Save memory %d: %v", i, saveErr)
		}
		eIdx := i % len(allEntityIDs)
		if linkErr := ps.LinkMemoryEntity(ctx, resp.ID, allEntityIDs[eIdx], "mention"); linkErr != nil {
			b.Fatalf("LinkMemoryEntity: %v", linkErr)
		}
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, benchErr := svc.Context(ctx, model.ContextRequest{
			Focus: "architecture benchmark",
		})
		if benchErr != nil {
			b.Fatalf("Context: %v", benchErr)
		}
	}
}

// BenchmarkContext_FlatMode_Baseline measures context assembly in flat mode
// against the same ~500-memory corpus as BenchmarkContext_CommunityPacking,
// providing a baseline for the community packing overhead measurement.
//
// Run with:
//
//	CGO_ENABLED=1 go test -tags fts5 -bench=BenchmarkContext_FlatMode_Baseline -benchtime=5s ./internal/service/
func BenchmarkContext_FlatMode_Baseline(b *testing.B) {
	projectDB, err := db.OpenMemory()
	if err != nil {
		b.Fatalf("open project db: %v", err)
	}
	globalDB, err := db.OpenMemory()
	if err != nil {
		b.Fatalf("open global db: %v", err)
	}
	b.Cleanup(func() { projectDB.Close(); globalDB.Close() })

	ps := store.NewMemoryStore(projectDB)
	gs := store.NewMemoryStore(globalDB)
	cfg := config.Default()
	cfg.Context.ContextPackingMode = "flat"

	svc := service.NewMemoryService(ps, gs, cfg, "bench/flatbase", embed.NopEmbedder{})
	ctx := context.Background()

	const numMemories = 500
	for i := 0; i < numMemories; i++ {
		_, saveErr := svc.Save(ctx, model.SaveRequest{
			Title:   fmt.Sprintf("bench-flatbase-memory-%d", i),
			Content: fmt.Sprintf("flat mode baseline benchmark content %d architecture focus", i),
		})
		if saveErr != nil {
			b.Fatalf("Save memory %d: %v", i, saveErr)
		}
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, benchErr := svc.Context(ctx, model.ContextRequest{
			Focus: "architecture benchmark",
		})
		if benchErr != nil {
			b.Fatalf("Context: %v", benchErr)
		}
	}
}
