package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/quality"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

func newWorkTestHandlers(t *testing.T) (*handlers, *service.SDDService, *store.SDDStore) {
	t.Helper()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })
	cfg := config.Default()
	cfg.Workflow.Engine = config.WorkflowEngineDeliveryV2
	sddStore := store.NewSDDStore(database)
	sdd := service.NewSDDService(sddStore, cfg, "p", nil)
	sdd.WithRepoDir(initQualityTestGitRepo(t))
	sdd.WithDeliveryVerifier(func(int) quality.Runner { return fakeQualityRunner{} }, "test-version")
	return &handlers{sdd: sdd, logger: slog.Default()}, sdd, sddStore
}

func workToolNames() map[string]bool {
	out := map[string]bool{}
	for _, tool := range allTools() {
		if strings.HasPrefix(tool.Name, "work_") {
			out[tool.Name] = true
		}
	}
	return out
}

func toolResultJSON(t *testing.T, result *ToolCallResult, dst any) string {
	t.Helper()
	if result == nil || len(result.Content) != 1 {
		t.Fatalf("unexpected tool result: %#v", result)
	}
	if err := json.Unmarshal([]byte(result.Content[0].Text), dst); err != nil {
		t.Fatalf("unmarshal tool result: %v: %s", err, result.Content[0].Text)
	}
	return result.Content[0].Text
}

func TestWorkToolsRegisteredAndDispatched(t *testing.T) {
	want := map[string]bool{
		"work_begin": true, "work_get": true, "work_lock": true, "work_amend": true,
		"work_review": true, "work_verify": true, "work_complete": true,
	}
	if got := workToolNames(); !mapsEqual(got, want) {
		t.Fatalf("work tools = %v, want %v", got, want)
	}
	if len(allTools()) != 94 {
		t.Fatalf("tool count = %d, want 94", len(allTools()))
	}
	h, _, _ := newWorkTestHandlers(t)
	for name := range want {
		_, rpcErr := h.handleToolCall(context.Background(), ToolCallParams{Name: name, Arguments: json.RawMessage(`{}`)})
		if rpcErr != nil && rpcErr.Code == CodeMethodNotFound {
			t.Errorf("%s fell through to MethodNotFound", name)
		}
	}
}

func mapsEqual(left, right map[string]bool) bool {
	if len(left) != len(right) {
		return false
	}
	for key := range left {
		if !right[key] {
			return false
		}
	}
	return true
}

func TestHandleWorkGetOmitsContractUUID(t *testing.T) {
	h, sdd, sddStore := newWorkTestHandlers(t)
	created, err := sdd.WorkBegin(context.Background(), model.WorkBeginRequest{Goal: "g", Scope: []string{"internal/**"}, Verification: []model.VerificationKind{model.VerificationBuild}})
	if err != nil {
		t.Fatal(err)
	}
	aggregate, err := sddStore.GetWorkAggregate(context.Background(), created.Contract.ID)
	if err != nil {
		t.Fatal(err)
	}
	result, rpcErr := h.handleToolCall(context.Background(), ToolCallParams{Name: "work_get", Arguments: mustMarshal(t, model.WorkGetRequest{ID: created.Contract.ID})})
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	var response model.WorkGetResponse
	raw := toolResultJSON(t, result, &response)
	if response.Contract.ID != created.Contract.ID || strings.Contains(raw, aggregate.Contract.UUID) {
		t.Fatalf("work_get response leaks or loses identity: %s", raw)
	}
}

func TestHandleWorkReviewAndCompleteRemainUnavailable(t *testing.T) {
	h, sdd, _ := newWorkTestHandlers(t)
	created, err := sdd.WorkBegin(context.Background(), model.WorkBeginRequest{Goal: "g", Scope: []string{"internal/**"}, Verification: []model.VerificationKind{model.VerificationBuild}})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"work_review", "work_complete"} {
		result, rpcErr := h.handleToolCall(context.Background(), ToolCallParams{Name: name, Arguments: mustMarshal(t, model.WorkActionRequest{ID: created.Contract.ID})})
		if rpcErr != nil {
			t.Fatalf("%s: %v", name, rpcErr)
		}
		var response model.WorkCapabilityResult
		raw := strings.ToLower(toolResultJSON(t, result, &response))
		if response.Available || response.Performed || response.ReasonCode != "phase_not_available" {
			t.Fatalf("%s fabricated capability: %#v", name, response)
		}
		for _, forbidden := range []string{`"verdict"`, `"success"`, `"passed"`, `"certificate"`} {
			if strings.Contains(raw, forbidden) {
				t.Fatalf("%s contains %s: %s", name, forbidden, raw)
			}
		}
	}
}

func TestHandleWorkVerifyReturnsSharedCertificateAndChecks(t *testing.T) {
	h, sdd, sddStore := newWorkTestHandlers(t)
	created, err := sdd.WorkBegin(context.Background(), model.WorkBeginRequest{Goal: "g", Scope: []string{"internal/**"}, Verification: []model.VerificationKind{model.VerificationBuild}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sdd.WorkLock(context.Background(), model.WorkLockRequest{ID: created.Contract.ID, By: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := sddStore.TransitionWork(context.Background(), created.Contract.ID, model.WorkStatusImplementing, model.WorkStatusVerifying, "test", ""); err != nil {
		t.Fatal(err)
	}
	result, rpcErr := h.handleToolCall(context.Background(), ToolCallParams{Name: "work_verify", Arguments: mustMarshal(t, model.WorkActionRequest{ID: created.Contract.ID})})
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	var response model.WorkCapabilityResult
	raw := toolResultJSON(t, result, &response)
	if !response.Available || !response.Performed || response.Certificate == nil || len(response.Checks) == 0 || response.Checks[0].CertificateID != response.Certificate.ID {
		t.Fatalf("response=%#v raw=%s", response, raw)
	}
	if !strings.Contains(raw, `"certificate"`) || !strings.Contains(raw, `"checks"`) {
		t.Fatalf("missing shared fields: %s", raw)
	}
}

func TestWorkVerifyToolDescriptionIsCurrent(t *testing.T) {
	for _, tool := range allTools() {
		if tool.Name != "work_verify" {
			continue
		}
		lower := strings.ToLower(tool.Description)
		if strings.Contains(lower, "unavailable") || !strings.Contains(lower, "certificate") || !strings.Contains(lower, "criteria") {
			t.Fatalf("description=%q", tool.Description)
		}
		return
	}
	t.Fatal("work_verify tool missing")
}

func TestMapServiceErrorWorkSentinels(t *testing.T) {
	h := &handlers{}
	for _, err := range []error{model.ErrInvalidContract, model.ErrInvalidCriteria, model.ErrInvalidWorkTransition, model.ErrReasonRequired, model.ErrWorkflowEngineDisabled} {
		if got := h.mapServiceError("work_test", err); got.Code != CodeInvalidParams {
			t.Errorf("%v mapped to %d, want %d", err, got.Code, CodeInvalidParams)
		}
	}
	if got := h.mapServiceError("work_test", model.ErrWorkNotFound); got.Code != CodeMemoryNotFound {
		t.Errorf("ErrWorkNotFound mapped to %d", got.Code)
	}
	if !errors.Is(model.ErrWorkflowEngineDisabled, model.ErrWorkflowEngineDisabled) {
		t.Fatal("sentinel identity broken")
	}
}
