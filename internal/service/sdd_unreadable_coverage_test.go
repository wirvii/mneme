package service

import (
	"context"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/store"
)

// TestSDDService_RecentlyCompletedSpecs_HealthyAndUnreadable exercises
// SDDService.RecentlyCompletedSpecs (SPEC-133 D2's new signature) directly
// at the SERVICE layer — closing an AC15 coverage gap paso 6 found: this
// method is only ever called cross-package, from internal/cli's
// renderFullStatus, and Go's default per-package coverage instrumentation
// (go test ./..., no -coverpkg) never attributes a caller's exercise of a
// callee in a DIFFERENT package back to that callee, so it read 0.0% in
// internal/service's own report despite being genuinely exercised via
// "mneme status" in internal/cli's test suite.
//
// One DONE spec stays healthy; a second DONE spec is corrupted in
// updated_at directly via SQL after reaching done (the only way to produce
// a row no mneme write path can create) — this is what the store layer's
// own tests already prove for ListSpecs/ListBacklogItems, pinned here
// again for the service-layer wrapper specifically, since that is the
// method status.go actually calls.
func TestSDDService_RecentlyCompletedSpecs_HealthyAndUnreadable(t *testing.T) {
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })

	sddStore := store.NewSDDStore(database)
	cfg := config.Default()
	svc := NewSDDService(sddStore, cfg, "project", nil)
	ctx := context.Background()

	_, healthyDone := promoteAndAdvance(t, svc, ctx, 7)
	if healthyDone.Status != model.SpecStatusDone {
		t.Fatalf("expected healthyDone spec done, got %s", healthyDone.Status)
	}

	_, corruptDone := promoteAndAdvance(t, svc, ctx, 7)
	if corruptDone.Status != model.SpecStatusDone {
		t.Fatalf("expected corruptDone spec done, got %s", corruptDone.Status)
	}
	if _, err := database.ExecContext(ctx,
		`UPDATE specs SET updated_at = 'not-a-timestamp' WHERE id = ?`, corruptDone.ID,
	); err != nil {
		t.Fatalf("corrupt %s: %v", corruptDone.ID, err)
	}

	specs, unreadable, err := svc.RecentlyCompletedSpecs(ctx, "project", 5)
	if err != nil {
		t.Fatalf("RecentlyCompletedSpecs: %v", err)
	}
	if len(specs) != 1 || specs[0].ID != healthyDone.ID {
		t.Errorf("specs = %+v, want exactly [%s]", specs, healthyDone.ID)
	}
	if len(unreadable) != 1 || unreadable[0].ID != corruptDone.ID || unreadable[0].Kind != model.UnreadableKindSpec {
		t.Errorf("unreadable = %+v, want exactly one row naming %s with kind %s",
			unreadable, corruptDone.ID, model.UnreadableKindSpec)
	}
}
