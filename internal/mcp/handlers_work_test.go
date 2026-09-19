package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

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
		"work_review": true, "work_verify": true, "work_complete": true, "work_resume": true, "work_metrics": true,
	}
	if got := workToolNames(); !mapsEqual(got, want) {
		t.Fatalf("work tools = %v, want %v", got, want)
	}
	if len(allTools()) != 97 {
		t.Fatalf("tool count = %d, want 97", len(allTools()))
	}
	h, _, _ := newWorkTestHandlers(t)
	for name := range want {
		_, rpcErr := h.handleToolCall(context.Background(), ToolCallParams{Name: name, Arguments: json.RawMessage(`{}`)})
		if rpcErr != nil && rpcErr.Code == CodeMethodNotFound {
			t.Errorf("%s fell through to MethodNotFound", name)
		}
	}
}

func TestWorkMetricsSchemaIsClosed(t *testing.T) {
	tool := findTool(allTools(), "work_metrics")
	if tool == nil {
		t.Fatal("work_metrics tool missing")
	}
	schema := tool.InputSchema.(map[string]any)
	if schema["additionalProperties"] != false {
		t.Fatalf("additionalProperties = %v, want false", schema["additionalProperties"])
	}
	if required, ok := schema["required"]; ok && required != nil {
		t.Fatalf("required = %v, want absent", required)
	}
	properties := schema["properties"].(map[string]any)
	if len(properties) != 3 {
		t.Fatalf("properties = %v", properties)
	}
	ids := properties["ids"].(map[string]any)
	if ids["type"] != "array" || ids["maxItems"] != 50 || ids["uniqueItems"] != true {
		t.Fatalf("ids schema = %v", ids)
	}
	items := ids["items"].(map[string]any)
	if items["type"] != "string" || items["pattern"] != `^WORK-[0-9]+$` {
		t.Fatalf("ids items = %v", items)
	}
	limit := properties["limit"].(map[string]any)
	if limit["type"] != "integer" || limit["minimum"] != 1 || limit["maximum"] != 50 {
		t.Fatalf("limit schema = %v", limit)
	}
	if properties["project"].(map[string]any)["type"] != "string" {
		t.Fatalf("project schema = %v", properties["project"])
	}
}

func TestWorkMetricsAuthorityRemainsReadOnly(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "cli", "hook.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, mapName := range []string{"lifecycleTools", "roleScopedTools"} {
		if authorityMapContains(text, mapName, "mcp__mneme__work_metrics") {
			t.Fatalf("work_metrics unexpectedly appears in %s", mapName)
		}
	}
	for _, tool := range []string{"work_begin", "work_lock", "work_amend", "work_complete", "work_resume"} {
		if !authorityMapContains(text, "lifecycleTools", "mcp__mneme__"+tool) {
			t.Errorf("lifecycleTools lost %s", tool)
		}
	}
	if !authorityMapContains(text, "roleScopedTools", "mcp__mneme__work_review") {
		t.Error("roleScopedTools lost work_review")
	}
	mutated := `var lifecycleTools = map[string]bool{"mcp__mneme__work_metrics": true}`
	if !authorityMapContains(mutated, "lifecycleTools", "mcp__mneme__work_metrics") {
		t.Fatal("authority detector does not catch a forbidden test-local entry")
	}
}

func authorityMapContains(source, mapName, tool string) bool {
	start := strings.Index(source, "var "+mapName+" = map[")
	if start < 0 {
		return false
	}
	body := source[start:]
	end := strings.Index(body, "\n}")
	if end < 0 {
		end = len(body)
	}
	return strings.Contains(body[:end], `"`+tool+`"`)
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

func TestHandleWorkCompleteReturnsPersistedEvidence(t *testing.T) {
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
	work, _ := sddStore.GetWork(context.Background(), created.Contract.ID)
	head, _ := (&quality.Git{RepoDir: sdd.RepoDir()}).HeadSHA()
	now := time.Now().UTC()
	cert := &model.DeliveryCertificate{Project: work.Project, WorkID: work.ID, ContractRevision: work.ContractRevision, ContractHash: work.ContractHash, HeadSHA: head, BaseSHA: work.BaseSHA, Verdict: model.DeliveryVerdictPass, StartedAt: now, FinishedAt: now}
	checks := []*model.DeliveryCheck{{Kind: "gate", Name: "build", Status: model.DeliveryCheckPass, Effect: model.DeliveryEffectBlocks}}
	if err := sddStore.InsertDeliveryCertificate(context.Background(), cert, checks); err != nil {
		t.Fatal(err)
	}
	result, rpcErr := h.handleToolCall(context.Background(), ToolCallParams{Name: "work_complete", Arguments: mustMarshal(t, model.WorkCompleteRequest{ID: created.Contract.ID, By: "orchestrator"})})
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	var response model.WorkCapabilityResult
	toolResultJSON(t, result, &response)
	if !response.Available || !response.Performed || response.Work.Contract.Status != model.WorkStatusDone || response.Certificate == nil || response.Certificate.ID != cert.ID || len(response.Checks) != 1 {
		t.Fatalf("response=%#v", response)
	}
}

func TestHandleWorkResumeReturnsAggregate(t *testing.T) {
	h, sdd, sddStore := newWorkTestHandlers(t)
	created, err := sdd.WorkBegin(context.Background(), model.WorkBeginRequest{Goal: "g", Scope: []string{"internal/**"}, Verification: []model.VerificationKind{model.VerificationBuild}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sdd.WorkLock(context.Background(), model.WorkLockRequest{ID: created.Contract.ID, By: "test"}); err != nil {
		t.Fatal(err)
	}
	for _, edge := range [][2]model.WorkStatus{{model.WorkStatusImplementing, model.WorkStatusVerifying}, {model.WorkStatusVerifying, model.WorkStatusEscalated}} {
		if err := sddStore.TransitionWork(context.Background(), created.Contract.ID, edge[0], edge[1], "test", ""); err != nil {
			t.Fatal(err)
		}
	}
	result, rpcErr := h.handleToolCall(context.Background(), ToolCallParams{Name: "work_resume", Arguments: mustMarshal(t, model.WorkResumeRequest{ID: created.Contract.ID, By: "orchestrator", Reason: "again"})})
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	var response model.WorkGetResponse
	toolResultJSON(t, result, &response)
	if response.Contract.Status != model.WorkStatusImplementing || response.History[len(response.History)-1].Reason != "again" {
		t.Fatalf("response=%#v", response)
	}
}

func TestHandleWorkLifecycleRejectsMalformedJSON(t *testing.T) {
	h, _, _ := newWorkTestHandlers(t)
	for _, name := range []string{"work_complete", "work_resume"} {
		_, rpcErr := h.handleToolCall(context.Background(), ToolCallParams{Name: name, Arguments: json.RawMessage(`{"id":`)})
		if rpcErr == nil || rpcErr.Code != CodeInvalidParams {
			t.Fatalf("%s error=%#v", name, rpcErr)
		}
	}
}

func TestHandleWorkMetrics_MalformedAndServiceParity(t *testing.T) {
	h, sdd, sddStore := newWorkTestHandlers(t)
	if _, rpcErr := h.handleWorkMetrics(context.Background(), json.RawMessage(`{"ids":`)); rpcErr == nil || rpcErr.Code != CodeInvalidParams {
		t.Fatalf("malformed error = %#v, want CodeInvalidParams", rpcErr)
	}
	created, err := sdd.WorkBegin(context.Background(), model.WorkBeginRequest{Goal: "g", Scope: []string{"internal/**"}, Verification: []model.VerificationKind{model.VerificationBuild}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sdd.WorkLock(context.Background(), model.WorkLockRequest{ID: created.Contract.ID, By: "test"}); err != nil {
		t.Fatal(err)
	}
	for _, edge := range [][2]model.WorkStatus{{model.WorkStatusImplementing, model.WorkStatusVerifying}, {model.WorkStatusVerifying, model.WorkStatusCorrecting}, {model.WorkStatusCorrecting, model.WorkStatusTargetedVerifying}} {
		if err := sddStore.TransitionWork(context.Background(), created.Contract.ID, edge[0], edge[1], "test", ""); err != nil {
			t.Fatal(err)
		}
	}
	work, err := sddStore.GetWork(context.Background(), created.Contract.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	cert := &model.DeliveryCertificate{Project: work.Project, WorkID: work.ID, ContractRevision: work.ContractRevision, ContractHash: work.ContractHash, HeadSHA: "head", BaseSHA: work.BaseSHA, Verdict: model.DeliveryVerdictPass, StartedAt: now, FinishedAt: now.Add(50 * time.Millisecond), DurationMs: 50}
	checks := []*model.DeliveryCheck{{Kind: "gate", Name: "build", Status: model.DeliveryCheckPass, Effect: model.DeliveryEffectBlocks}}
	if err := sddStore.InsertDeliveryCertificate(context.Background(), cert, checks); err != nil {
		t.Fatal(err)
	}
	req := model.WorkMetricsRequest{IDs: []string{created.Contract.ID}}
	want, err := sdd.WorkMetrics(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	result, rpcErr := h.handleWorkMetrics(context.Background(), mustMarshal(t, req))
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	var got model.WorkMetricsResponse
	raw := toolResultJSON(t, result, &got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MCP response = %#v, want %#v", got, want)
	}
	if got.Details[0].VerificationEvidence != model.WorkMetricEvidencePartial {
		t.Fatalf("evidence = %q, want partial", got.Details[0].VerificationEvidence)
	}
	if strings.Contains(raw, work.UUID) || strings.Contains(raw, cert.ID) {
		t.Fatalf("response leaks internal identity: %s", raw)
	}
}

func TestHandleWorkReviewReturnsSharedCertificateAndFindings(t *testing.T) {
	h, sdd, _ := newWorkTestHandlers(t)
	created, err := sdd.WorkBegin(context.Background(), model.WorkBeginRequest{
		Goal: "g", Scope: []string{"internal/**"}, Verification: []model.VerificationKind{model.VerificationBuild},
		Constraints: []model.WorkConstraintInput{{Key: "layers", Text: "dependencies point inward"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sdd.WorkLock(context.Background(), model.WorkLockRequest{ID: created.Contract.ID, By: "test"}); err != nil {
		t.Fatal(err)
	}
	head, err := (&quality.Git{RepoDir: sdd.RepoDir()}).HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	req := model.WorkReviewRequest{
		ID: created.Contract.ID, By: "qa-tester", HeadSHA: head,
		Findings:             []model.WorkReviewFindingInput{{Category: model.FindingDiscovery, Severity: model.PriorityLow, Description: "follow-up", Evidence: "evidence"}},
		ArchitectureVerdicts: []model.WorkArchitectureVerdictInput{{ConstraintKey: "layers", Status: model.DeliveryCheckPass, EvidenceKind: model.ReviewEvidenceFile, Evidence: "internal/service/work_review.go"}},
	}
	result, rpcErr := h.handleToolCall(context.Background(), ToolCallParams{Name: "work_review", Arguments: mustMarshal(t, req)})
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	var response model.WorkCapabilityResult
	raw := toolResultJSON(t, result, &response)
	if !response.Available || !response.Performed || response.Operation != "review" || response.Certificate == nil || len(response.Checks) == 0 || len(response.Work.Findings) != 1 {
		t.Fatalf("response=%#v raw=%s", response, raw)
	}
}

func TestHandleWorkReview_TargetedParity(t *testing.T) {
	h, sdd, _ := newWorkTestHandlers(t)
	created, err := sdd.WorkBegin(context.Background(), model.WorkBeginRequest{Goal: "g", Scope: []string{"internal/**"}, Verification: []model.VerificationKind{model.VerificationBuild}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sdd.WorkLock(context.Background(), model.WorkLockRequest{ID: created.Contract.ID, By: "test"}); err != nil {
		t.Fatal(err)
	}
	head, _ := (&quality.Git{RepoDir: sdd.RepoDir()}).HeadSHA()
	initialReq := model.WorkReviewRequest{ID: created.Contract.ID, By: "qa-tester", HeadSHA: head, Findings: []model.WorkReviewFindingInput{{Category: model.FindingRegression, Severity: model.PriorityHigh, Description: "regression", Evidence: "red"}}}
	if _, rpcErr := h.handleWorkReview(context.Background(), mustMarshal(t, initialReq)); rpcErr != nil {
		t.Fatal(rpcErr)
	}
	path := filepath.Join(sdd.RepoDir(), "corrected.txt")
	if err := os.WriteFile(path, []byte("corrected\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "corrected.txt"}, {"commit", "-m", "correct"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = sdd.RepoDir()
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	head, _ = (&quality.Git{RepoDir: sdd.RepoDir()}).HeadSHA()
	targetedReq := model.WorkReviewRequest{ID: created.Contract.ID, By: "qa-tester", HeadSHA: head, Resolutions: []model.WorkFindingResolutionInput{{FindingSeq: 1, Status: model.FindingFixed, Evidence: "green"}}}
	result, rpcErr := h.handleWorkReview(context.Background(), mustMarshal(t, targetedReq))
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	var response model.WorkCapabilityResult
	raw := toolResultJSON(t, result, &response)
	if response.ReviewPhase != model.ReviewPhaseTargeted || response.NextStatus != model.WorkStatusTargetedVerifying || response.Work.Findings[0].Status != model.FindingFixed || !strings.Contains(raw, `"review_phase":"targeted"`) {
		t.Fatalf("response=%#v raw=%s", response, raw)
	}
}

func TestWorkReviewToolSchemaIsClosedAndCurrent(t *testing.T) {
	for _, tool := range allTools() {
		if tool.Name != "work_review" {
			continue
		}
		lower := strings.ToLower(tool.Description)
		if strings.Contains(lower, "unavailable") || !strings.Contains(lower, "certificate") || !strings.Contains(lower, "initial") || !strings.Contains(lower, "targeted") {
			t.Fatalf("description=%q", tool.Description)
		}
		schema := tool.InputSchema.(map[string]any)
		required, _ := schema["required"].([]string)
		if !slices.Equal(required, []string{"id", "by", "head_sha"}) {
			t.Fatalf("required=%v", required)
		}
		properties := schema["properties"].(map[string]any)
		verdictItems := properties["architecture_verdicts"].(map[string]any)["items"].(map[string]any)
		verdictProps := verdictItems["properties"].(map[string]any)
		if got := verdictProps["status"].(map[string]any)["enum"]; !reflect.DeepEqual(got, []string{"pass", "fail"}) {
			t.Fatalf("status enum=%v", got)
		}
		if got := verdictProps["evidence_kind"].(map[string]any)["enum"]; !reflect.DeepEqual(got, []string{"file", "symbol", "codegraph_query"}) {
			t.Fatalf("evidence enum=%v", got)
		}
		return
	}
	t.Fatal("work_review tool missing")
}

func TestWorkReviewToolSchema_TargetedParity(t *testing.T) {
	for _, tool := range allTools() {
		if tool.Name != "work_review" {
			continue
		}
		schema := tool.InputSchema.(map[string]any)
		properties := schema["properties"].(map[string]any)
		if _, exists := properties["phase"]; exists {
			t.Fatal("work_review schema exposes caller-selected phase")
		}
		resolution := properties["resolutions"].(map[string]any)
		item := resolution["items"].(map[string]any)
		if !slices.Equal(item["required"].([]string), []string{"finding_seq", "status", "evidence"}) {
			t.Fatalf("resolution required = %v", item["required"])
		}
		props := item["properties"].(map[string]any)
		if got := props["status"].(map[string]any)["enum"]; !reflect.DeepEqual(got, []string{"fixed", "invalid"}) {
			t.Fatalf("resolution status enum = %v", got)
		}
		if _, ok := props["reason"]; !ok {
			t.Fatal("resolution reason missing")
		}
		if len(workToolNames()) != 9 {
			t.Fatalf("work tool count = %d", len(workToolNames()))
		}
		return
	}
	t.Fatal("work_review tool missing")
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
