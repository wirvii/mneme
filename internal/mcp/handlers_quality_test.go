package mcp

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/quality"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// TestMapServiceError_QualitySentinels covers AC21: each of the eight new
// quality sentinels maps to its correct JSON-RPC code — CodeMemoryNotFound
// for ErrCertificateNotFound (an absence, like every other *NotFound
// sentinel), CodeInvalidParams for the other seven (validation/precondition
// failures, same bucket as ErrInvalidTransition/ErrReasonRequired).
func TestMapServiceError_QualitySentinels(t *testing.T) {
	h := &handlers{}

	tests := []struct {
		name string
		err  error
		want int
	}{
		{"ErrInvalidConstitution", model.ErrInvalidConstitution, CodeInvalidParams},
		{"ErrConstitutionAblated", model.ErrConstitutionAblated, CodeInvalidParams},
		{"ErrConstitutionChanged", model.ErrConstitutionChanged, CodeInvalidParams},
		{"ErrCertificateMissing", model.ErrCertificateMissing, CodeInvalidParams},
		{"ErrCertificateStale", model.ErrCertificateStale, CodeInvalidParams},
		{"ErrCertificateNotGreen", model.ErrCertificateNotGreen, CodeInvalidParams},
		{"ErrWorktreeDirty", model.ErrWorktreeDirty, CodeInvalidParams},
		{"ErrCertificateNotFound", model.ErrCertificateNotFound, CodeMemoryNotFound},
		{"ErrInvalidBudget", model.ErrInvalidBudget, CodeInvalidParams},
		{"ErrUnsupportedBudgetSchema", model.ErrUnsupportedBudgetSchema, CodeInvalidParams},
		{"ErrBudgetNotFound", model.ErrBudgetNotFound, CodeMemoryNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := h.mapServiceError("quality_test", tt.err)
			if got.Code != tt.want {
				t.Errorf("mapServiceError(%v).Code = %d, want %d", tt.err, got.Code, tt.want)
			}
		})
	}
}

// fakeQualityRunner is the D14 seam for the MCP-level dispatch tests below —
// never make/sh/the real suite (AC18).
type fakeQualityRunner struct{}

func (fakeQualityRunner) Run(_ context.Context, gate quality.Gate, _ string) quality.GateResult {
	return quality.GateResult{Name: gate.Name, Status: quality.GateStatusPass}
}

// initQualityTestGitRepo creates a real git repo with a valid, enabled
// constitution and one committed file — enough for quality_verify to run
// end-to-end through the real JSON-RPC dispatch.
func initQualityTestGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")

	if err := os.MkdirAll(filepath.Join(dir, ".mneme"), 0o755); err != nil {
		t.Fatalf("mkdir .mneme: %v", err)
	}
	doc := `
schema_version = 1
enabled = true
[execution]
output_tail_bytes = 4096
[[gate]]
name = "build"
command = ["true"]
timeout = "5m"
required = true
`
	if err := os.WriteFile(filepath.Join(dir, ".mneme", "quality.toml"), []byte(doc), 0o644); err != nil {
		t.Fatalf("write quality.toml: %v", err)
	}
	run("add", ".")
	run("commit", "-m", "initial")
	return dir
}

// TestDispatch_QualityVerifyStatusAck_EndToEnd exercises the real JSON-RPC
// dispatch (allTools -> handleToolCall -> QualityService) for all three
// tools, wired via Server.WithQualityService (P10).
func TestDispatch_QualityVerifyStatusAck_EndToEnd(t *testing.T) {
	projectDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open project db: %v", err)
	}
	projectDB.SetMaxOpenConns(1)
	t.Cleanup(func() { projectDB.Close() })

	globalDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open global db: %v", err)
	}
	t.Cleanup(func() { globalDB.Close() })

	projectStore := store.NewMemoryStore(projectDB)
	globalStore := store.NewMemoryStore(globalDB)
	cfg := config.Default()
	svc := service.NewMemoryService(projectStore, globalStore, cfg, "test-project", embed.NopEmbedder{})

	sddStore := store.NewSDDStore(projectDB)
	sddSvc := service.NewSDDService(sddStore, cfg, "test-project", svc)

	repoDir := initQualityTestGitRepo(t)
	qualitySvc := service.NewQualityService(sddStore, "test-project", repoDir, fakeQualityRunner{})

	srv := NewServer(svc, sddSvc, nil, nil, slog.Default(), "all", "test")
	srv.WithQualityService(qualitySvc)

	// Seed a standard-lane spec at implementing directly via the store
	// (bypassing the full lifecycle, which is not this test's concern).
	spec := &model.Spec{ID: "SPEC-1", Title: "t", Status: model.SpecStatusImplementing, Project: "test-project", Lane: model.LaneStandard}
	if err := sddStore.CreateSpec(context.Background(), spec); err != nil {
		t.Fatalf("CreateSpec: %v", err)
	}

	verifyResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "quality_verify",
		Arguments: mustMarshal(t, map[string]any{"id": "SPEC-1"}),
	})
	if verifyResp.Error != nil {
		t.Fatalf("quality_verify: unexpected error code=%d message=%s", verifyResp.Error.Code, verifyResp.Error.Message)
	}
	var cert model.QualityCertificate
	unmarshalToolText(t, verifyResp, &cert)
	if cert.Verdict != model.QualityVerdictPass {
		t.Fatalf("quality_verify verdict = %q, want pass", cert.Verdict)
	}

	statusResp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name:      "quality_status",
		Arguments: mustMarshal(t, map[string]any{"id": "SPEC-1"}),
	})
	if statusResp.Error != nil {
		t.Fatalf("quality_status: unexpected error code=%d message=%s", statusResp.Error.Code, statusResp.Error.Message)
	}
	var statusOut model.QualityStatusResponse
	unmarshalToolText(t, statusResp, &statusOut)
	if statusOut.LatestCertificate == nil || statusOut.LatestCertificate.ID != cert.ID {
		t.Fatalf("quality_status latest_certificate = %+v, want id=%s", statusOut.LatestCertificate, cert.ID)
	}

	// Manually flip the certificate's only check to a finding, so quality_ack
	// has something to acknowledge.
	if err := sddStore.AckCheck(context.Background(), cert.ID, 1, "", ""); err == nil {
		t.Fatal("expected AckCheck to fail acking a non-finding row (sanity check on fixture)")
	}

	ackResp := process(t, srv, "tools/call", 3, ToolCallParams{
		Name: "quality_ack",
		Arguments: mustMarshal(t, map[string]any{
			"cert_id": cert.ID, "seq": 9999, "by": "orchestrator", "justification": "n/a",
		}),
	})
	if ackResp.Error == nil {
		t.Fatal("quality_ack with a non-existent seq should error")
	}
	if ackResp.Error.Code != CodeMemoryNotFound {
		t.Errorf("quality_ack(missing seq) code = %d, want CodeMemoryNotFound", ackResp.Error.Code)
	}
}

// TestDispatch_Init_MaterializesQualityConstitution covers SPEC-115 D15/P12
// through the real "init" MCP tool: a repo_root with no .mneme/quality.toml
// gets one written (enabled=false) after a real dispatch call, and the
// response's quality_constitution_applied field reflects it.
func TestDispatch_Init_MaterializesQualityConstitution(t *testing.T) {
	srv := newTestServerWithSDD(t)
	repoRoot := t.TempDir()

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "init",
		Arguments: mustMarshal(t, map[string]any{"repo_root": repoRoot}),
	})
	if resp.Error != nil {
		t.Fatalf("init: unexpected error code=%d message=%s", resp.Error.Code, resp.Error.Message)
	}

	var result struct {
		QualityConstitutionApplied bool `json:"quality_constitution_applied"`
	}
	unmarshalToolText(t, resp, &result)
	if !result.QualityConstitutionApplied {
		t.Error("quality_constitution_applied = false, want true")
	}

	if _, err := os.Stat(filepath.Join(repoRoot, ".mneme", "quality.toml")); err != nil {
		t.Errorf("expected .mneme/quality.toml to be written, stat err = %v", err)
	}
}

// TestDispatch_QualityTools_UnavailableWhenNotWired covers the
// qualityUnavailable path: a server that never called WithQualityService
// reports the service unavailable, never a nil-pointer panic.
func TestDispatch_QualityTools_UnavailableWhenNotWired(t *testing.T) {
	srv := newTestServerWithSDD(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "quality_status",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if resp.Error == nil {
		t.Fatal("expected an error when quality service is not wired")
	}
	if resp.Error.Code != CodeInternalError {
		t.Errorf("code = %d, want CodeInternalError", resp.Error.Code)
	}
}

// TestDispatch_QualityStatus_ReportsBaseline covers AC29's dispatch-level
// requirement (SPEC-116 P10): quality_status's real JSON-RPC response
// carries the registered baseline's fields when the file exists, and
// omits them when it does not — over the exact SAME dispatch path (no new
// tool, no tools.go/handlers.go/server.go change).
func TestDispatch_QualityStatus_ReportsBaseline(t *testing.T) {
	projectDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open project db: %v", err)
	}
	projectDB.SetMaxOpenConns(1)
	t.Cleanup(func() { projectDB.Close() })

	globalDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open global db: %v", err)
	}
	t.Cleanup(func() { globalDB.Close() })

	projectStore := store.NewMemoryStore(projectDB)
	globalStore := store.NewMemoryStore(globalDB)
	cfg := config.Default()
	svc := service.NewMemoryService(projectStore, globalStore, cfg, "test-project", embed.NopEmbedder{})

	sddStore := store.NewSDDStore(projectDB)
	sddSvc := service.NewSDDService(sddStore, cfg, "test-project", svc)

	repoDir := initQualityTestGitRepo(t)
	qualitySvc := service.NewQualityService(sddStore, "test-project", repoDir, fakeQualityRunner{})

	srv := NewServer(svc, sddSvc, nil, nil, slog.Default(), "all", "test")
	srv.WithQualityService(qualitySvc)

	// (a) No baseline file yet: the field must be absent.
	beforeResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "quality_status",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if beforeResp.Error != nil {
		t.Fatalf("quality_status (no baseline): unexpected error %+v", beforeResp.Error)
	}
	var beforeOut model.QualityStatusResponse
	unmarshalToolText(t, beforeResp, &beforeOut)
	if beforeOut.Baseline != nil {
		t.Fatalf("quality_status (no baseline) Baseline = %+v, want nil", beforeOut.Baseline)
	}

	// (b) Register a baseline, then dispatch again: the field must appear
	// with the exact registered figures.
	baseline := &quality.Baseline{
		SchemaVersion: quality.BaselineSchemaVersion,
		MeasuredAtSHA: "abc123",
		MeasuredAt:    mustParseRFC3339(t, "2026-01-01T00:00:00Z"),
		CertificateID: "cert-x",
		LinesTotal:    100,
		LinesCovered:  70,
		GlobalLinePct: 70.0,
		ScopeHash:     "h",
	}
	if err := os.WriteFile(filepath.Join(repoDir, quality.BaselineRelPath), []byte(quality.RenderBaseline(baseline)), 0o644); err != nil {
		t.Fatalf("write baseline: %v", err)
	}

	afterResp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name:      "quality_status",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if afterResp.Error != nil {
		t.Fatalf("quality_status (with baseline): unexpected error %+v", afterResp.Error)
	}
	var afterOut model.QualityStatusResponse
	unmarshalToolText(t, afterResp, &afterOut)
	if afterOut.Baseline == nil {
		t.Fatal("quality_status (with baseline) Baseline is nil, want it populated")
	}
	if afterOut.Baseline.GlobalLinePct != 70.0 || afterOut.Baseline.MeasuredAtSHA != "abc123" {
		t.Errorf("quality_status Baseline = %+v, want global_line_pct=70 measured_at_sha=abc123", afterOut.Baseline)
	}
}

// mustParseRFC3339 is a small test helper for building baseline fixtures.
func mustParseRFC3339(t *testing.T, s string) (parsed time.Time) {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("time.Parse(%q): %v", s, err)
	}
	return parsed
}
