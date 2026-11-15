package mcp

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// initReviewPackageRepo creates a git repository with one commit, a local
// identity and signing off, so the SDD service can read real SHAs.
func initReviewPackageRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "review-package-mcp@example.com")
	run("config", "user.name", "review-package-mcp")
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	run("add", ".")
	run("commit", "-m", "initial")
	return dir
}

// newTestServerWithRepoAndWorkflowDir is newTestServerWithSDDAndWorkflowDir
// with the SDD service pointed at repoDir, so the review range can be
// computed over a real repository.
func newTestServerWithRepoAndWorkflowDir(t *testing.T, repoDir string) (*Server, string) {
	t.Helper()
	projectDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open project db: %v", err)
	}
	projectDB.SetMaxOpenConns(1)
	globalDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open global db: %v", err)
	}
	globalDB.SetMaxOpenConns(1)
	t.Cleanup(func() { projectDB.Close(); globalDB.Close() })

	cfg := config.Default()
	workflowDir := t.TempDir()
	cfg.Workflow.Dir = workflowDir
	svc := service.NewMemoryService(store.NewMemoryStore(projectDB), store.NewMemoryStore(globalDB), cfg, "test-project", embed.NopEmbedder{})
	sddSvc := service.NewSDDService(store.NewSDDStore(projectDB), cfg, "test-project", svc)
	sddSvc.WithRepoDir(repoDir)
	return NewServer(svc, sddSvc, nil, nil, slog.Default(), "all", "test"), workflowDir
}

// TestSpecStatus_MCPCarriesQARoundsAndPackagePath is C9's MCP half:
// qa_rounds is always in spec_status, and review_package_path appears only
// while the spec is in qa and the package file of its range exists.
func TestSpecStatus_MCPCarriesQARoundsAndPackagePath(t *testing.T) {
	srv, workflowDir := newTestServerWithRepoAndWorkflowDir(t, initReviewPackageRepo(t))

	newResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "spec_new",
		Arguments: mustMarshal(t, map[string]any{"title": "review package over mcp", "lane": "standard"}),
	})
	if newResp.Error != nil {
		t.Fatalf("spec_new: %v", newResp.Error.Message)
	}
	var spec struct{ ID string }
	unmarshalToolText(t, newResp, &spec)

	statusRaw := func() map[string]any {
		t.Helper()
		resp := process(t, srv, "tools/call", 2, ToolCallParams{
			Name:      "spec_status",
			Arguments: mustMarshal(t, map[string]any{"id": spec.ID}),
		})
		if resp.Error != nil {
			t.Fatalf("spec_status: %v", resp.Error.Message)
		}
		var raw map[string]any
		unmarshalToolText(t, resp, &raw)
		return raw
	}

	raw := statusRaw()
	if got, ok := raw["qa_rounds"]; !ok || got != 0.0 {
		t.Errorf("draft: qa_rounds = %v (present=%v), want 0", got, ok)
	}
	if _, ok := raw["review_package_path"]; ok {
		t.Error("draft: review_package_path present, want absent")
	}

	advanceStandardSpecToQAOverMCP(t, srv, spec.ID)
	raw = statusRaw()
	rr, ok := raw["review_range"].(map[string]any)
	if !ok || rr["available"] != true {
		t.Fatalf("qa: review_range = %v, want an available range", raw["review_range"])
	}
	if _, ok := raw["review_package_path"]; ok {
		t.Error("qa before writing: review_package_path present, want absent")
	}

	from, _ := rr["from"].(string)
	to, _ := rr["to"].(string)
	if len(from) < 8 || len(to) < 8 {
		t.Fatalf("review_range from/to = %q/%q, want SHAs", from, to)
	}
	specDir := filepath.Join(workflowDir, "test-project", "specs", spec.ID)
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(specDir, "review-package-"+from[:8]+"-"+to[:8]+".md")
	if err := os.WriteFile(path, []byte("paquete\n"), 0o644); err != nil {
		t.Fatalf("write package: %v", err)
	}
	if got := statusRaw()["review_package_path"]; got != path {
		t.Errorf("qa after writing: review_package_path = %v, want %q", got, path)
	}

	rejResp := process(t, srv, "tools/call", 3, ToolCallParams{
		Name: "spec_reject",
		Arguments: mustMarshal(t, map[string]any{
			"id": spec.ID, "reason": "falla", "by": "qa-agent",
			"findings": []map[string]any{{"criterion_id": "AC1", "detail": "does not hold"}},
		}),
	})
	if rejResp.Error != nil {
		t.Fatalf("spec_reject: %v", rejResp.Error.Message)
	}
	raw = statusRaw()
	if got := raw["qa_rounds"]; got != 1.0 {
		t.Errorf("after reject: qa_rounds = %v, want 1", got)
	}
	if _, ok := raw["review_package_path"]; ok {
		t.Error("after reject: review_package_path present, want absent")
	}
}
