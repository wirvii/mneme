package service_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// TestBackfillSDDRefs is AC8: over a database with memories that predate
// the anchor mechanism (created directly at the store layer, bypassing
// Save's bakeSDDRefs — exactly how a real pre-SPEC-128 memory looks),
// BackfillSDDRefs creates the missing rows, marks the completion marker,
// and a second call creates zero rows.
func TestBackfillSDDRefs(t *testing.T) {
	fx := newSDDTestFixture(t, false, "Team Member", "team@example.com")
	ctx := context.Background()

	spec := &model.Spec{ID: "SPEC-001", Title: "s", Status: model.SpecStatusDraft, Project: "test/project", Lane: model.LaneStandard}
	if err := fx.sddStore.CreateSpec(ctx, spec); err != nil {
		t.Fatalf("CreateSpec: %v", err)
	}
	item := &model.BacklogItem{ID: "BL-001", Title: "b", Status: model.BacklogStatusRaw, Priority: model.PriorityMedium, Project: "test/project", Lane: model.LaneStandard}
	if err := fx.sddStore.CreateBacklogItem(ctx, item); err != nil {
		t.Fatalf("CreateBacklogItem: %v", err)
	}

	// Pre-existing memories: created directly at the store layer, exactly
	// how memories saved before this mechanism existed look — SDDRefs is
	// nil even though the text mentions real work.
	pre1, err := fx.projectStore.Create(ctx, &model.Memory{
		Type: model.TypeDecision, Scope: model.ScopeProject,
		Title: "Old note citing a spec", Content: "See SPEC-001.", Project: "test/project",
	})
	if err != nil {
		t.Fatalf("Create pre1: %v", err)
	}
	pre2, err := fx.projectStore.Create(ctx, &model.Memory{
		Type: model.TypeDiscovery, Scope: model.ScopeProject,
		Title: "Old note citing a backlog item", Content: "See BL-001.", Project: "test/project",
	})
	if err != nil {
		t.Fatalf("Create pre2: %v", err)
	}
	// A memory that mentions nothing anchorable — must be scanned but
	// contribute zero created refs.
	if _, err := fx.projectStore.Create(ctx, &model.Memory{
		Type: model.TypeDiscovery, Scope: model.ScopeProject,
		Title: "Nothing to anchor", Content: "No SDD mentions here.", Project: "test/project",
	}); err != nil {
		t.Fatalf("Create pre3: %v", err)
	}

	scanned, created, err := fx.svc.BackfillSDDRefs(ctx)
	if err != nil {
		t.Fatalf("BackfillSDDRefs: %v", err)
	}
	if scanned != 3 {
		t.Errorf("scanned = %d, want 3", scanned)
	}
	if created != 2 {
		t.Errorf("created = %d, want 2 (one ref each for pre1 and pre2)", created)
	}

	complete, err := fx.sddStore.IsSDDReferenceBackfillComplete(ctx)
	if err != nil {
		t.Fatalf("IsSDDReferenceBackfillComplete: %v", err)
	}
	if !complete {
		t.Fatal("expected the backfill marker to be complete after BackfillSDDRefs")
	}

	got1, err := fx.svc.Get(ctx, pre1.ID)
	if err != nil {
		t.Fatalf("Get pre1: %v", err)
	}
	if len(got1.SDDRefs) != 1 || got1.SDDRefs[0].Status != model.SDDRefLocal || got1.SDDRefs[0].LocalID != "SPEC-001" {
		t.Errorf("pre1 refs: %+v", got1.SDDRefs)
	}

	got2, err := fx.svc.Get(ctx, pre2.ID)
	if err != nil {
		t.Fatalf("Get pre2: %v", err)
	}
	if len(got2.SDDRefs) != 1 || got2.SDDRefs[0].Status != model.SDDRefLocal || got2.SDDRefs[0].LocalID != "BL-001" {
		t.Errorf("pre2 refs: %+v", got2.SDDRefs)
	}

	// Second call: the marker is already complete, so it must be a cheap
	// no-op — zero scanned, zero created.
	scanned2, created2, err := fx.svc.BackfillSDDRefs(ctx)
	if err != nil {
		t.Fatalf("BackfillSDDRefs (second call): %v", err)
	}
	if scanned2 != 0 || created2 != 0 {
		t.Errorf("second call: scanned=%d created=%d, want 0/0", scanned2, created2)
	}
}

// TestBackfillSDDRefs_ForeignAuthorSkipped reinforces AC9 from the backfill
// side: a memory authored by someone else is scanned but never anchored,
// and its mention keeps resolving unanchored.
func TestBackfillSDDRefs_ForeignAuthorSkipped(t *testing.T) {
	fx := newSDDTestFixture(t, false, "Team Member", "team@example.com")
	ctx := context.Background()

	spec := &model.Spec{ID: "SPEC-001", Title: "s", Status: model.SpecStatusDraft, Project: "test/project", Lane: model.LaneStandard}
	if err := fx.sddStore.CreateSpec(ctx, spec); err != nil {
		t.Fatalf("CreateSpec: %v", err)
	}

	foreign, err := fx.projectStore.Create(ctx, &model.Memory{
		Type: model.TypeDecision, Scope: model.ScopeProject,
		Title: "Someone else's note", Content: "See SPEC-001.", Project: "test/project",
		Author: "Someone Else <else@example.com>",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	scanned, created, err := fx.svc.BackfillSDDRefs(ctx)
	if err != nil {
		t.Fatalf("BackfillSDDRefs: %v", err)
	}
	if scanned != 1 {
		t.Errorf("scanned = %d, want 1", scanned)
	}
	if created != 0 {
		t.Errorf("created = %d, want 0 (foreign-authored memory must never anchor)", created)
	}

	got, err := fx.svc.Get(ctx, foreign.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.SDDRefs) != 1 || got.SDDRefs[0].Status != model.SDDRefUnanchored {
		t.Errorf("expected the mention to resolve unanchored, got %+v", got.SDDRefs)
	}
}

// TestBackfillSDDRefs_Measure turns R3 ("por debajo de un segundo", the
// spec's own estimate) into an actual measurement against a REAL copy of
// this repository's project database. It is SKIPPED unless
// MNEME_R3_DB_COPY names an existing sqlite file — never the real
// ~/.mneme/projects/wirvii-mneme.db (opening that applies migration 019;
// see the plan's paso 7 for the sqlite3 .backup procedure) — so `make test`
// on a clean machine stays green and this never runs by accident.
func TestBackfillSDDRefs_Measure(t *testing.T) {
	copyPath := os.Getenv("MNEME_R3_DB_COPY")
	if copyPath == "" {
		t.Skip("MNEME_R3_DB_COPY not set; skipping the R3 measurement")
	}

	svc, sddStore, cleanup := openMeasureFixture(t, copyPath)
	defer cleanup()

	start := time.Now()
	scanned, created, err := svc.BackfillSDDRefs(context.Background())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("BackfillSDDRefs: %v", err)
	}

	complete, err := sddStore.IsSDDReferenceBackfillComplete(context.Background())
	if err != nil {
		t.Fatalf("IsSDDReferenceBackfillComplete: %v", err)
	}

	t.Logf("R3 measurement: memories_scanned=%d refs_created=%d elapsed=%s complete=%v",
		scanned, created, elapsed, complete)
}

// openMeasureFixture opens dbPath (a real, already-migrated sqlite copy —
// see TestBackfillSDDRefs_Measure's own doc comment on how it must be
// produced) and builds a MemoryService/SDDStore pair over it, exactly the
// shape internal/cli.initService wires in production minus team-memory
// (irrelevant to this measurement). The global store is a throwaway
// in-memory database since BackfillSDDRefs only ever touches the project
// store (D12).
func openMeasureFixture(t *testing.T, dbPath string) (*service.MemoryService, *store.SDDStore, func()) {
	t.Helper()

	projectDB, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open project db copy %q: %v", dbPath, err)
	}
	globalDB, err := db.OpenMemory()
	if err != nil {
		projectDB.Close()
		t.Fatalf("open global db: %v", err)
	}

	projectStore := store.NewMemoryStore(projectDB)
	globalStore := store.NewMemoryStore(globalDB)
	sddStore := store.NewSDDStore(projectDB)

	svc := service.NewMemoryService(projectStore, globalStore, config.Default(), "wirvii/mneme", embed.NopEmbedder{},
		service.WithSDDStore(sddStore))

	cleanup := func() {
		projectDB.Close()
		globalDB.Close()
	}
	return svc, sddStore, cleanup
}
