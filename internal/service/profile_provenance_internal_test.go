package service

import (
	"context"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/store"
)

// TestListProfileRuleIDs_TruncatedFlag_HitsCap exercises the truncated=true
// path of ListProfileRuleIDs by shrinking the package-private
// profileRuleScanCap for the duration of the test, rather than seeding 5000
// real rows (SPEC-105 DD3's fail-safe).
func TestListProfileRuleIDs_TruncatedFlag_HitsCap(t *testing.T) {
	original := profileRuleScanCap
	profileRuleScanCap = 3
	t.Cleanup(func() { profileRuleScanCap = original })

	projectDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open project db: %v", err)
	}
	globalDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open global db: %v", err)
	}
	t.Cleanup(func() { projectDB.Close(); globalDB.Close() })

	svc := NewMemoryService(store.NewMemoryStore(projectDB), store.NewMemoryStore(globalDB), config.Default(), "test/project", embed.NopEmbedder{})
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if _, err := svc.SaveProfileRule(ctx, model.SaveRequest{
			Title: "Rule", Content: "content", AppliesTo: []string{"**"},
		}, "chatea-pro"); err != nil {
			t.Fatalf("SaveProfileRule %d: %v", i, err)
		}
	}

	ids, truncated, err := svc.ListProfileRuleIDs(ctx, "chatea-pro")
	if err != nil {
		t.Fatalf("ListProfileRuleIDs: %v", err)
	}
	if !truncated {
		t.Error("expected truncated=true once the scan hits the shrunk cap")
	}
	if len(ids) != 3 {
		t.Errorf("expected exactly %d ids (the shrunk cap), got %d", 3, len(ids))
	}
}
