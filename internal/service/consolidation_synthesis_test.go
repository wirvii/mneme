package service_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// consolidationSynthesisProject is the slug seedSynthesisCluster writes its
// entities under, so the service of these tests uses the same one.
const consolidationSynthesisProject = "test/synthesis"

// newConsolidationSynthesisService builds a service over two in-memory
// databases with config.Default(). Only when enabled is true does it turn
// synthesis generation on, so the default path is exercised exactly as a
// person without a config.toml gets it (SPEC-187).
func newConsolidationSynthesisService(t *testing.T, enabled bool) (*service.MemoryService, *store.MemoryStore, *db.DB, *config.Config) {
	t.Helper()
	projectDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open project db: %v", err)
	}
	globalDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open global db: %v", err)
	}
	t.Cleanup(func() { projectDB.Close(); globalDB.Close() })

	cfg := config.Default()
	cfg.Graph.CommunityMinSize = 2
	if enabled {
		cfg.Graph.SynthesisEnabled = true
	}
	ps := store.NewMemoryStore(projectDB)
	gs := store.NewMemoryStore(globalDB)
	svc := service.NewMemoryService(ps, gs, cfg, consolidationSynthesisProject, embed.NopEmbedder{})
	return svc, ps, projectDB, cfg
}

// countSynthesisRows counts every synthesis row, active or not, so a row the
// generator created and something else retired would still be seen.
func countSynthesisRows(t *testing.T, d *db.DB) int {
	t.Helper()
	var n int
	if err := d.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM memories WHERE type='synthesis'`).Scan(&n); err != nil {
		t.Fatalf("count synthesis: %v", err)
	}
	return n
}

// TestRunConsolidation_SynthesisOff_CreatesNoRows verifies that with the new
// default a consolidation over a base with detectable communities creates no
// synthesis row at all.
func TestRunConsolidation_SynthesisOff_CreatesNoRows(t *testing.T) {
	svc, ps, projectDB, _ := newConsolidationSynthesisService(t, false)
	ctx := context.Background()
	seedSynthesisCluster(t, ctx, svc, ps, "off", 5)

	res, err := svc.RunConsolidation(ctx)
	if err != nil {
		t.Fatalf("RunConsolidation: %v", err)
	}
	if res.CommunitiesDetected == 0 {
		t.Fatal("CommunitiesDetected = 0; the fixture must produce a community for the test to mean anything")
	}
	if res.SynthesisCreated != 0 {
		t.Errorf("SynthesisCreated = %d, want 0", res.SynthesisCreated)
	}
	if n := countSynthesisRows(t, projectDB); n != 0 {
		t.Errorf("synthesis rows = %d, want 0", n)
	}
}

// TestRunConsolidation_SynthesisOn_CreatesRows verifies that turning the key
// back on restores the generator over the same scenario.
func TestRunConsolidation_SynthesisOn_CreatesRows(t *testing.T) {
	svc, ps, projectDB, _ := newConsolidationSynthesisService(t, true)
	ctx := context.Background()
	seedSynthesisCluster(t, ctx, svc, ps, "on", 5)

	res, err := svc.RunConsolidation(ctx)
	if err != nil {
		t.Fatalf("RunConsolidation: %v", err)
	}
	if res.SynthesisCreated == 0 {
		t.Error("SynthesisCreated = 0, want > 0")
	}
	if n := countSynthesisRows(t, projectDB); n == 0 {
		t.Error("synthesis rows = 0, want > 0")
	}
}

// TestRunConsolidation_SynthesisOff_StillSweepsDedupsAndEvicts verifies that
// turning syntheses off leaves the rest of the pipeline intact: the decay
// sweep, the exact-title dedup and the budget eviction all still run.
func TestRunConsolidation_SynthesisOff_StillSweepsDedupsAndEvicts(t *testing.T) {
	svc, ps, projectDB, cfg := newConsolidationSynthesisService(t, false)
	ctx := context.Background()
	clusterIDs, _ := seedSynthesisCluster(t, ctx, svc, ps, "keep", 5)

	upsert := func(title string, importance float64) string {
		t.Helper()
		m, _, err := ps.Upsert(ctx, &model.Memory{
			Type:       model.TypeDiscovery,
			Scope:      model.ScopeProject,
			Project:    consolidationSynthesisProject,
			Title:      title,
			Content:    "content of " + title,
			Importance: importance,
		})
		if err != nil {
			t.Fatalf("Upsert %q: %v", title, err)
		}
		return m.ID
	}
	faded := upsert("fixture-faded", 0.01)
	winner := upsert("fixture-dup", 0.25)
	loser := upsert("fixture-dup", 0.20)

	for _, id := range clusterIDs {
		m, err := ps.Get(ctx, id)
		if err != nil || m == nil {
			t.Fatalf("Get cluster memory %s: %v", id, err)
		}
		if m.Importance <= 0.25 {
			t.Fatalf("cluster memory importance %.3f <= 0.25; the eviction assertion would be meaningless", m.Importance)
		}
	}

	cfg.Storage.ProjectBudget = 5
	res, err := svc.RunConsolidation(ctx)
	if err != nil {
		t.Fatalf("RunConsolidation: %v", err)
	}
	if res.Swept != 1 || res.Duplicates != 1 || res.Evicted != 1 || res.SynthesisCreated != 0 {
		t.Errorf("result: swept=%d duplicates=%d evicted=%d synthesis_created=%d, want 1/1/1/0",
			res.Swept, res.Duplicates, res.Evicted, res.SynthesisCreated)
	}

	state := func(id string) (deleted bool, supersededBy string) {
		t.Helper()
		var del, sup sql.NullString
		if err := projectDB.QueryRowContext(ctx,
			`SELECT deleted_at, superseded_by FROM memories WHERE id = ?`, id).Scan(&del, &sup); err != nil {
			t.Fatalf("read state of %s: %v", id, err)
		}
		return del.Valid, sup.String
	}
	if del, _ := state(faded); !del {
		t.Error("the 0.01 memory is still active; the decay sweep did not run")
	}
	if _, sup := state(loser); sup != winner {
		t.Errorf("the 0.20 duplicate superseded_by = %q, want %q", sup, winner)
	}
	if del, _ := state(winner); !del {
		t.Error("the 0.25 memory is still active; it should be the one evicted by the budget")
	}
	for _, id := range clusterIDs {
		if del, _ := state(id); del {
			t.Errorf("cluster memory %s was retired, want active", id)
		}
	}
	if n := countSynthesisRows(t, projectDB); n != 0 {
		t.Errorf("synthesis rows = %d, want 0", n)
	}
}
