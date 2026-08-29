// Package service — tests for nextBacklogID/nextSpecID (SPEC-131 §2b
// commit 6, D21/D55): the next correlative is MAX(the database's own next
// id, one past the highest correlative a file already reserves) when the
// SDD mechanism is on, and exactly today's answer when it is off.
package service

import (
	"context"
	"os"
	"testing"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/sddfile"
)

// seedBacklogIDs inserts backlog items with the exact IDs given, bypassing
// NextBacklogID entirely, so a test can put the database's own "next id"
// at a known value.
func seedBacklogIDs(t *testing.T, ctx context.Context, svc *SDDService, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := svc.store.CreateBacklogItem(ctx, &model.BacklogItem{
			ID: id, Title: "seed", Status: model.BacklogStatusRaw, Priority: model.PriorityMedium,
			Project: importTestProject, Lane: model.LaneStandard,
		}); err != nil {
			t.Fatalf("seed CreateBacklogItem(%s): %v", id, err)
		}
	}
}

// seedSpecIDs is seedBacklogIDs' sibling for specs.
func seedSpecIDs(t *testing.T, ctx context.Context, svc *SDDService, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := svc.store.CreateSpec(ctx, &model.Spec{
			ID: id, Title: "seed", Status: model.SpecStatusDraft,
			Project: importTestProject, Lane: model.LaneStandard,
		}); err != nil {
			t.Fatalf("seed CreateSpec(%s): %v", id, err)
		}
	}
}

// TestSDDNextID_MaxOfBaseAndFiles is AC15.
func TestSDDNextID_MaxOfBaseAndFiles(t *testing.T) {
	ctx := context.Background()

	t.Run("backlog: off, base BL-003, disk BL-205.md -> BL-004 (today's property, intact)", func(t *testing.T) {
		svc, repoDir := newSDDMaterializeService(t, importTestProject)
		seedBacklogIDs(t, ctx, svc, "BL-001", "BL-002", "BL-003")
		writeRawSDDFile(t, sddfile.BacklogPath(repoDir, "BL-205"), "irrelevant content")

		got, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "x", Lane: model.LaneStandard, Project: importTestProject})
		if err != nil {
			t.Fatalf("BacklogAdd: %v", err)
		}
		if got.ID != "BL-004" {
			t.Errorf("ID = %s, want BL-004 (mechanism off: the file must be ignored)", got.ID)
		}
	})

	t.Run("backlog: on, base BL-003, disk BL-205.md -> BL-206", func(t *testing.T) {
		svc, repoDir := newSDDMaterializeService(t, importTestProject)
		enableSDD(t, repoDir, importTestProject)
		seedBacklogIDs(t, ctx, svc, "BL-001", "BL-002", "BL-003")
		writeRawSDDFile(t, sddfile.BacklogPath(repoDir, "BL-205"), "irrelevant content")

		got, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "x", Lane: model.LaneStandard, Project: importTestProject})
		if err != nil {
			t.Fatalf("BacklogAdd: %v", err)
		}
		if got.ID != "BL-206" {
			t.Errorf("ID = %s, want BL-206 (the file's number must win)", got.ID)
		}
	})

	t.Run("backlog: on, base BL-300, disk BL-205.md -> BL-301 (base wins)", func(t *testing.T) {
		svc, repoDir := newSDDMaterializeService(t, importTestProject)
		enableSDD(t, repoDir, importTestProject)
		seedBacklogIDs(t, ctx, svc, "BL-300")
		writeRawSDDFile(t, sddfile.BacklogPath(repoDir, "BL-205"), "irrelevant content")

		got, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "x", Lane: model.LaneStandard, Project: importTestProject})
		if err != nil {
			t.Fatalf("BacklogAdd: %v", err)
		}
		if got.ID != "BL-301" {
			t.Errorf("ID = %s, want BL-301 (the database's own number must win)", got.ID)
		}
	})

	t.Run("spec: on, base SPEC-003, disk SPEC-205/ (no record.md yet) -> SPEC-206", func(t *testing.T) {
		svc, repoDir := newSDDMaterializeService(t, importTestProject)
		enableSDD(t, repoDir, importTestProject)
		seedSpecIDs(t, ctx, svc, "SPEC-001", "SPEC-002", "SPEC-003")
		if err := os.MkdirAll(sddfile.SpecDir(repoDir, "SPEC-205"), 0o755); err != nil {
			t.Fatalf("mkdir SPEC-205: %v", err)
		}

		got, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "x", Lane: model.LaneStandard, Project: importTestProject})
		if err != nil {
			t.Fatalf("SpecNew: %v", err)
		}
		if got.ID != "SPEC-206" {
			t.Errorf("ID = %s, want SPEC-206 — the directory alone reserves the number (D4), "+
				"even with no record.md inside it yet", got.ID)
		}
	})
}

// Mutacion exigida (AC15): quitar la mitad del disco (dejar
// nextBacklogID/nextSpecID devolver siempre store.NextBacklogID/NextSpecID
// sin consultar sddfile.MaxBacklogID/MaxSpecID) pone en rojo las filas "on"
// (BL-206, BL-301, SPEC-206), porque en esas filas los dos lados dan
// numeros distintos por construccion. Ejecutada y revertida durante la
// implementacion; resultado real en changes.md.
