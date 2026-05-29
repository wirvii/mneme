package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/juanftp/mneme/internal/model"
)

// newTestModelsService creates a ModelsService with a config file in a temp dir.
func newTestModelsService(t *testing.T) (*ModelsService, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	return NewModelsService(cfgPath), cfgPath
}

// TestModelsService_List_Defaults verifies that List returns all bundled agents
// with default origins when no overrides exist.
func TestModelsService_List_Defaults(t *testing.T) {
	svc, _ := newTestModelsService(t)
	resp, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List error: %v", err)
	}

	if len(resp.Agents) == 0 {
		t.Fatal("List returned no agents")
	}

	// All should be "default" with no overrides file.
	for _, ai := range resp.Agents {
		if ai.Origin != "default" {
			t.Errorf("agent %s: origin = %q, want default", ai.Agent, ai.Origin)
		}
		if ai.Model == "" {
			t.Errorf("agent %s: model must not be empty", ai.Agent)
		}
	}

	// Verify expected defaults.
	agentMap := make(map[string]string)
	for _, ai := range resp.Agents {
		agentMap[ai.Agent] = ai.Model
	}
	if got := agentMap["architect"]; got != "opus" {
		t.Errorf("architect default: got %q, want opus", got)
	}
	if got := agentMap["backend"]; got != "sonnet" {
		t.Errorf("backend default: got %q, want sonnet", got)
	}
}

// TestModelsService_List_WithOverride verifies that after Set, List shows
// origin="override" for the affected agent.
func TestModelsService_List_WithOverride(t *testing.T) {
	svc, _ := newTestModelsService(t)
	ctx := context.Background()

	if _, err := svc.Set(ctx, ModelSetRequest{Agent: "bug-hunter", Model: "opus"}); err != nil {
		t.Fatalf("Set error: %v", err)
	}

	resp, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}

	for _, ai := range resp.Agents {
		if ai.Agent == "bug-hunter" {
			if ai.Origin != "override" {
				t.Errorf("bug-hunter: origin = %q, want override", ai.Origin)
			}
			if ai.Model != "opus" {
				t.Errorf("bug-hunter: model = %q, want opus", ai.Model)
			}
			return
		}
	}
	t.Error("bug-hunter not found in List response")
}

// TestModelsService_Set_UnknownAgent verifies ErrUnknownAgent is returned.
func TestModelsService_Set_UnknownAgent(t *testing.T) {
	svc, _ := newTestModelsService(t)
	_, err := svc.Set(context.Background(), ModelSetRequest{Agent: "nosuchagent", Model: "opus"})
	if !errors.Is(err, model.ErrUnknownAgent) {
		t.Errorf("expected ErrUnknownAgent, got %v", err)
	}
}

// TestModelsService_Set_EmptyModel verifies ErrInvalidModel is returned.
func TestModelsService_Set_EmptyModel(t *testing.T) {
	svc, _ := newTestModelsService(t)
	_, err := svc.Set(context.Background(), ModelSetRequest{Agent: "backend", Model: ""})
	if !errors.Is(err, model.ErrInvalidModel) {
		t.Errorf("expected ErrInvalidModel, got %v", err)
	}
}

// TestModelsService_Set_KnownAlias verifies no warning for known aliases.
func TestModelsService_Set_KnownAlias(t *testing.T) {
	svc, _ := newTestModelsService(t)
	resp, err := svc.Set(context.Background(), ModelSetRequest{Agent: "backend", Model: "sonnet"})
	if err != nil {
		t.Fatalf("Set error: %v", err)
	}
	if resp.Warning != "" {
		t.Errorf("unexpected warning for known alias: %q", resp.Warning)
	}
}

// TestModelsService_Set_UnknownAlias verifies warning (not error) for unknown alias.
func TestModelsService_Set_UnknownAlias(t *testing.T) {
	svc, _ := newTestModelsService(t)
	resp, err := svc.Set(context.Background(), ModelSetRequest{Agent: "backend", Model: "banana"})
	if err != nil {
		t.Fatalf("Set should not error for unknown alias, got: %v", err)
	}
	if resp.Warning == "" {
		t.Error("expected warning for unknown alias banana")
	}
}

// TestModelsService_Set_Hint verifies the hint is returned on success.
func TestModelsService_Set_Hint(t *testing.T) {
	svc, _ := newTestModelsService(t)
	resp, err := svc.Set(context.Background(), ModelSetRequest{Agent: "architect", Model: "opus"})
	if err != nil {
		t.Fatalf("Set error: %v", err)
	}
	if resp.Hint == "" {
		t.Error("Set must return a hint to reinstall")
	}
}

// TestModelsService_Reset_Single verifies that Reset with an agent removes
// only that agent's override.
func TestModelsService_Reset_Single(t *testing.T) {
	svc, _ := newTestModelsService(t)
	ctx := context.Background()

	if _, err := svc.Set(ctx, ModelSetRequest{Agent: "bug-hunter", Model: "opus"}); err != nil {
		t.Fatalf("Set error: %v", err)
	}
	if _, err := svc.Set(ctx, ModelSetRequest{Agent: "backend", Model: "haiku"}); err != nil {
		t.Fatalf("Set error: %v", err)
	}

	resetResp, err := svc.Reset(ctx, ModelResetRequest{Agent: "bug-hunter"})
	if err != nil {
		t.Fatalf("Reset error: %v", err)
	}
	if len(resetResp.Reset) != 1 || resetResp.Reset[0] != "bug-hunter" {
		t.Errorf("Reset returned %v, want [bug-hunter]", resetResp.Reset)
	}

	// bug-hunter should now show as default, backend still override.
	listResp, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	for _, ai := range listResp.Agents {
		switch ai.Agent {
		case "bug-hunter":
			if ai.Origin != "default" {
				t.Errorf("bug-hunter after reset: origin = %q, want default", ai.Origin)
			}
		case "backend":
			if ai.Origin != "override" {
				t.Errorf("backend: origin = %q, want override", ai.Origin)
			}
		}
	}
}

// TestModelsService_Reset_All verifies that Reset with empty agent clears all.
func TestModelsService_Reset_All(t *testing.T) {
	svc, _ := newTestModelsService(t)
	ctx := context.Background()

	if _, err := svc.Set(ctx, ModelSetRequest{Agent: "bug-hunter", Model: "opus"}); err != nil {
		t.Fatalf("Set error: %v", err)
	}
	if _, err := svc.Set(ctx, ModelSetRequest{Agent: "backend", Model: "haiku"}); err != nil {
		t.Fatalf("Set error: %v", err)
	}

	resetResp, err := svc.Reset(ctx, ModelResetRequest{})
	if err != nil {
		t.Fatalf("Reset all error: %v", err)
	}
	if len(resetResp.Reset) != 2 {
		t.Errorf("Reset all returned %d items, want 2: %v", len(resetResp.Reset), resetResp.Reset)
	}

	listResp, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List after reset: %v", err)
	}
	for _, ai := range listResp.Agents {
		if ai.Origin != "default" {
			t.Errorf("agent %s: origin = %q after reset all, want default", ai.Agent, ai.Origin)
		}
	}
}

// TestModelsService_Reset_UnknownAgent verifies ErrUnknownAgent.
func TestModelsService_Reset_UnknownAgent(t *testing.T) {
	svc, _ := newTestModelsService(t)
	_, err := svc.Reset(context.Background(), ModelResetRequest{Agent: "nosuchagent"})
	if !errors.Is(err, model.ErrUnknownAgent) {
		t.Errorf("expected ErrUnknownAgent, got %v", err)
	}
}

// TestModelsService_Reset_Hint verifies hint is non-empty when items are reset.
func TestModelsService_Reset_Hint(t *testing.T) {
	svc, _ := newTestModelsService(t)
	ctx := context.Background()

	if _, err := svc.Set(ctx, ModelSetRequest{Agent: "backend", Model: "haiku"}); err != nil {
		t.Fatalf("Set error: %v", err)
	}

	resp, err := svc.Reset(ctx, ModelResetRequest{Agent: "backend"})
	if err != nil {
		t.Fatalf("Reset error: %v", err)
	}
	if resp.Hint == "" {
		t.Error("Reset must return a hint when items are reset")
	}
}
