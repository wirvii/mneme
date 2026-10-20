package cli

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/wirvii/mneme/internal/model"
)

// TestUpdateCmd_RepeatableRelationFlags is SPEC-166 AC8: --supersedes is
// repeatable (StringArrayVar keeps every value) and counts as an update flag
// on its own.
func TestUpdateCmd_RepeatableRelationFlags(t *testing.T) {
	curID, oldA := seedMemCmdEnv(t)

	svc, cleanup, err := initService()
	if err != nil {
		t.Fatalf("initService: %v", err)
	}
	oldB, err := svc.Save(context.Background(), model.SaveRequest{Title: "Third memory", Content: "gamma content"})
	cleanup()
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	out, err := runCmd(t, newUpdateCmd(), curID, "--supersedes", oldA, "--supersedes", oldB.ID, "--json")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	var resp model.SaveResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, out)
	}
	if len(resp.RelationsApplied) != 2 {
		t.Fatalf("relations_applied = %+v, want both supersedes", resp.RelationsApplied)
	}
	if resp.RelationsApplied[0].TargetID != oldA || resp.RelationsApplied[1].TargetID != oldB.ID {
		t.Errorf("unexpected targets: %+v", resp.RelationsApplied)
	}

	svc, cleanup, err = initService()
	if err != nil {
		t.Fatalf("initService: %v", err)
	}
	defer cleanup()
	for _, id := range []string{oldA, oldB.ID} {
		m, err := svc.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("Get %s: %v", id, err)
		}
		if m.SupersededBy != curID {
			t.Errorf("%s superseded_by = %q, want %q", id, m.SupersededBy, curID)
		}
	}

	if _, err := runCmd(t, newUpdateCmd(), curID); err == nil {
		t.Error("update with no flags must still fail")
	}
}
