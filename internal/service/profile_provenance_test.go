package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/wirvii/mneme/internal/model"
)

// TestSaveProfileRule_StampsProvenance verifies that SaveProfileRule always
// stamps source="profile:<name>", forces project scope and Shared=0
// regardless of what the caller's request carried on those fields, and that
// the resulting rule is picked up by Context's rules phase like any other
// active rule (SPEC-092 AC4).
func TestSaveProfileRule_StampsProvenance(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	global := model.ScopeGlobal
	shared := 2
	resp, err := svc.SaveProfileRule(ctx, model.SaveRequest{
		Title:     "No CGO",
		Content:   "This repo builds pure-Go, no CGO, no build tags.",
		AppliesTo: []string{"**"},
		Severity:  model.SeverityWarn,
		Scope:     global, // should be overridden to project
		Shared:    &shared,
	}, "chatea-pro")
	if err != nil {
		t.Fatalf("SaveProfileRule: %v", err)
	}

	mem, err := svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if mem.Source != "profile:chatea-pro" {
		t.Errorf("Source: got %q, want %q", mem.Source, "profile:chatea-pro")
	}
	if mem.Scope != model.ScopeProject {
		t.Errorf("Scope: got %q, want %q (SaveProfileRule must force project scope)", mem.Scope, model.ScopeProject)
	}
	if mem.Type != model.TypeRule {
		t.Errorf("Type: got %q, want %q", mem.Type, model.TypeRule)
	}
	if mem.Shared != 0 {
		t.Errorf("Shared: got %d, want 0 (SaveProfileRule must force Shared=0)", mem.Shared)
	}

	// The rule must be visible through the normal rules phase like any other
	// active rule.
	ctxResp, err := svc.Context(ctx, model.ContextRequest{Project: svc.ProjectSlug()})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}
	found := false
	for _, r := range ctxResp.Rules {
		if r.ID == resp.ID {
			found = true
		}
	}
	if !found {
		t.Error("expected profile-stamped rule to appear in Context().Rules")
	}
}

// TestMemSave_CannotForgeSource verifies the security boundary AC4 requires:
// a SaveRequest built the way the public mem_save path builds it (from
// caller-controlled JSON) can never carry a non-empty Source, because the
// field is json:"-" and therefore never populated by json.Unmarshal —
// regardless of what key the raw payload contains.
func TestMemSave_CannotForgeSource(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	raw := []byte(`{"title":"t","content":"c","source":"profile:evil"}`)
	var req model.SaveRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Source != "" {
		t.Fatalf("expected Source to stay empty after unmarshaling attacker JSON, got %q", req.Source)
	}

	resp, err := svc.Save(ctx, req)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	mem, err := svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if mem.Source != "" {
		t.Errorf("Source: got %q, want empty — mem_save must never be able to set provenance", mem.Source)
	}
}

// TestPurgeProfileRules_HardDeletes verifies PurgeProfileRules physically
// removes every rule stamped with the given profile's provenance — the row
// no longer exists at all, not merely soft-deleted — while leaving
// hand-authored rules (Source == "") untouched (SPEC-092 AC5).
func TestPurgeProfileRules_HardDeletes(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	var stampedIDs []string
	for i := 0; i < 2; i++ {
		resp, err := svc.SaveProfileRule(ctx, model.SaveRequest{
			Title:     "Profile rule",
			Content:   "content",
			AppliesTo: []string{"**"},
		}, "chatea-pro")
		if err != nil {
			t.Fatalf("SaveProfileRule: %v", err)
		}
		stampedIDs = append(stampedIDs, resp.ID)
	}

	handSevr := model.SeverityWarn
	handResp, err := svc.Save(ctx, model.SaveRequest{
		Title:     "Hand-authored rule",
		Content:   "content",
		Type:      model.TypeRule,
		AppliesTo: []string{"**"},
		Severity:  handSevr,
	})
	if err != nil {
		t.Fatalf("Save hand-authored: %v", err)
	}

	removed, err := svc.PurgeProfileRules(ctx, svc.ProjectSlug(), "chatea-pro")
	if err != nil {
		t.Fatalf("PurgeProfileRules: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("expected 2 removed ids, got %d (%v)", len(removed), removed)
	}

	for _, id := range stampedIDs {
		_, err := svc.Get(ctx, id)
		if !errors.Is(err, model.ErrNotFound) {
			t.Errorf("expected ErrNotFound for purged rule %s, got %v", id, err)
		}
	}

	// (a) Forget (decay) does NOT achieve this — it never touches deleted_at,
	// so a "forgotten" rule still satisfies deleted_at IS NULL and keeps
	// showing up in loadActiveRules. Demonstrated here against a *third*
	// profile-stamped rule so the assertion is about Forget specifically.
	forgottenResp, err := svc.SaveProfileRule(ctx, model.SaveRequest{
		Title:     "Forgettable",
		Content:   "content",
		AppliesTo: []string{"**"},
	}, "chatea-pro")
	if err != nil {
		t.Fatalf("SaveProfileRule (forgettable): %v", err)
	}
	if err := svc.Forget(ctx, forgottenResp.ID, "test"); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	stillThere, err := svc.Get(ctx, forgottenResp.ID)
	if err != nil {
		t.Fatalf("Get after Forget: %v", err)
	}
	if stillThere == nil {
		t.Fatal("expected Forget to leave the row retrievable (it only decays importance, never deletes)")
	}
	// This is the regression this test exists to pin: Forget only decays
	// importance (SetDecayRate), it never touches deleted_at, so
	// loadActiveRules (which filters on deleted_at IS NULL, not importance)
	// still returns the "forgotten" rule. That insufficiency is exactly why
	// PurgeProfileRules/HardDeleteBySource exist.
	ctxResp, err := svc.Context(ctx, model.ContextRequest{Project: svc.ProjectSlug()})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}
	foundAfterForget := false
	for _, r := range ctxResp.Rules {
		if r.ID == forgottenResp.ID {
			foundAfterForget = true
		}
	}
	if !foundAfterForget {
		t.Error("expected Forget to be insufficient: the forgotten rule should still appear in Context().Rules")
	}

	// Hand-authored rule survives the purge untouched.
	got, err := svc.Get(ctx, handResp.ID)
	if err != nil {
		t.Fatalf("Get hand-authored: %v", err)
	}
	if got == nil {
		t.Fatal("expected hand-authored rule to survive PurgeProfileRules")
	}

	// (b) Idempotent — nothing left to purge.
	removedAgain, err := svc.PurgeProfileRules(ctx, svc.ProjectSlug(), "chatea-pro")
	if err != nil {
		t.Fatalf("PurgeProfileRules (second call, only forgottenResp left): %v", err)
	}
	if len(removedAgain) != 1 {
		t.Fatalf("expected 1 remaining profile rule (forgottenResp) to be purged, got %d", len(removedAgain))
	}

	final, err := svc.PurgeProfileRules(ctx, svc.ProjectSlug(), "chatea-pro")
	if err != nil {
		t.Fatalf("PurgeProfileRules (third call): %v", err)
	}
	if len(final) != 0 {
		t.Errorf("expected 0 removed on third call, got %d", len(final))
	}
}
