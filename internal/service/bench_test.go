package service_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/db"
	"github.com/juanftp/mneme/internal/embed"
	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/service"
	"github.com/juanftp/mneme/internal/store"
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
