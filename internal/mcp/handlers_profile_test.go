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

// newProfileTestServer builds a Server whose underlying config.DataDir is a
// fresh t.TempDir() — unlike newTestServer's config.Default(), which resolves
// DataDir under the single sandbox HOME shared by every test in this binary
// (SPEC-085's whole-binary HOME sandbox). ProfileService is the first
// feature in this test suite whose filesystem side effects (the host-level
// profile store) are NOT reset by db.OpenMemory()'s in-memory SQLite, so
// reusing newTestServer here would let one test's `profile add` leak into
// every other test that also calls newTestServer in the same run — mirrors
// the dedicated-dataDir pattern already established by
// newQuerylogTestHandlers in handlers_querylog_test.go.
func newProfileTestServer(t *testing.T) *Server {
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
	cfg.Storage.DataDir = t.TempDir()

	svc := service.NewMemoryService(store.NewMemoryStore(projectDB), store.NewMemoryStore(globalDB), cfg, "test-project", embed.NopEmbedder{})
	return NewServer(svc, nil, nil, nil, slog.Default(), "all", "test")
}

// mustRunGitForMCPProfile runs git with args in dir, failing the test on error.
func mustRunGitForMCPProfile(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// newMCPProfileFixtureRepo creates a local git repository (entirely inside
// t.TempDir(), no network) with a valid mneme-profile.toml, tagged "v1".
func newMCPProfileFixtureRepo(t *testing.T, name, version string) string {
	t.Helper()
	dir := t.TempDir()

	mustRunGitForMCPProfile(t, dir, "init", "-q")
	mustRunGitForMCPProfile(t, dir, "config", "user.name", "mneme-test")
	mustRunGitForMCPProfile(t, dir, "config", "user.email", "mneme-test@example.com")

	manifest := "name = \"" + name + "\"\nversion = \"" + version + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "mneme-profile.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	mustRunGitForMCPProfile(t, dir, "add", ".")
	mustRunGitForMCPProfile(t, dir, "commit", "-q", "-m", "initial commit")
	mustRunGitForMCPProfile(t, dir, "tag", "v1")

	return dir
}

// --- SPEC-095 §5: profile_new ------------------------------------------------

func TestHandleProfileNew_Success(t *testing.T) {
	srv := newProfileTestServer(t)
	dest := filepath.Join(t.TempDir(), "chatea-pro")

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "profile_new",
		Arguments: mustMarshal(t, map[string]any{"name": "chatea-pro", "dir": dest}),
	})
	if resp.Error != nil {
		t.Fatalf("profile_new: unexpected error: %s", resp.Error.Message)
	}

	var result struct {
		Name         string `json:"name"`
		Path         string `json:"path"`
		ManifestPath string `json:"manifest_path"`
	}
	unmarshalToolText(t, resp, &result)
	if result.Name != "chatea-pro" || result.Path != dest {
		t.Errorf("unexpected NewProfileResult: %+v", result)
	}
	if _, err := os.Stat(result.ManifestPath); err != nil {
		t.Errorf("expected manifest at %s: %v", result.ManifestPath, err)
	}
	if info, err := os.Stat(filepath.Join(dest, ".git")); err != nil || !info.IsDir() {
		t.Errorf("expected %s/.git to exist: %v", dest, err)
	}
}

func TestHandleProfileNew_MissingName(t *testing.T) {
	srv := newProfileTestServer(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "profile_new",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if resp.Error == nil {
		t.Fatal("expected CodeInvalidParams, got nil error")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want %d", resp.Error.Code, CodeInvalidParams)
	}
}

func TestHandleProfileNew_DestinationNotEmpty(t *testing.T) {
	srv := newProfileTestServer(t)
	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "keep.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "profile_new",
		Arguments: mustMarshal(t, map[string]any{"name": "chatea-pro", "dir": dest}),
	})
	if resp.Error == nil {
		t.Fatal("profile_new: expected an error for non-empty destination")
	}
}

func TestHandleProfileAdd_MissingSource(t *testing.T) {
	srv := newProfileTestServer(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "profile_add",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if resp.Error == nil {
		t.Fatal("expected CodeInvalidParams, got nil error")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want %d", resp.Error.Code, CodeInvalidParams)
	}
}

func TestHandleProfileAdd_Success(t *testing.T) {
	srv := newProfileTestServer(t)
	source := newMCPProfileFixtureRepo(t, "chatea-pro", "1.0.0")

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "profile_add",
		Arguments: mustMarshal(t, map[string]any{"source": source}),
	})
	if resp.Error != nil {
		t.Fatalf("profile_add: unexpected error: %s", resp.Error.Message)
	}

	var result struct {
		Name    string `json:"Name"`
		Version string `json:"Version"`
	}
	unmarshalToolText(t, resp, &result)
	if result.Name != "chatea-pro" || result.Version != "1.0.0" {
		t.Errorf("unexpected AddResult: %+v", result)
	}
}

func TestHandleProfileAdd_AlreadyExists(t *testing.T) {
	srv := newProfileTestServer(t)
	source := newMCPProfileFixtureRepo(t, "chatea-pro", "1.0.0")

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "profile_add",
		Arguments: mustMarshal(t, map[string]any{"source": source}),
	})
	if resp.Error != nil {
		t.Fatalf("first profile_add: unexpected error: %s", resp.Error.Message)
	}

	resp = process(t, srv, "tools/call", 2, ToolCallParams{
		Name:      "profile_add",
		Arguments: mustMarshal(t, map[string]any{"source": source}),
	})
	if resp.Error == nil {
		t.Fatal("second profile_add: expected an error")
	}
}

func TestHandleProfileList_Empty(t *testing.T) {
	srv := newProfileTestServer(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "profile_list",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if resp.Error != nil {
		t.Fatalf("profile_list: unexpected error: %s", resp.Error.Message)
	}

	var result []any
	unmarshalToolText(t, resp, &result)
	if len(result) != 0 {
		t.Errorf("len(result) = %d, want 0", len(result))
	}
}

func TestHandleProfileStatus_Absent(t *testing.T) {
	srv := newProfileTestServer(t)
	projectRoot := t.TempDir()

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "profile_status",
		Arguments: mustMarshal(t, map[string]any{"project_root": projectRoot}),
	})
	if resp.Error != nil {
		t.Fatalf("profile_status: unexpected error: %s", resp.Error.Message)
	}

	var result struct {
		State int `json:"State"`
	}
	unmarshalToolText(t, resp, &result)
	if result.State != 0 { // profile.PinAbsent == 0
		t.Errorf("State = %d, want 0 (PinAbsent)", result.State)
	}
}

func TestHandleProfileUpdate_NoNameNoPin(t *testing.T) {
	srv := newProfileTestServer(t)
	projectRoot := t.TempDir()

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(projectRoot); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "profile_update",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if resp.Error == nil {
		t.Fatal("expected an error when there is no pin and no name")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want %d", resp.Error.Code, CodeInvalidParams)
	}
}

func TestHandleProfileUpdate_NotFound(t *testing.T) {
	srv := newProfileTestServer(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "profile_update",
		Arguments: mustMarshal(t, map[string]any{"name": "nonexistent"}),
	})
	if resp.Error == nil {
		t.Fatal("expected an error for a nonexistent profile")
	}
	if resp.Error.Code != CodeMemoryNotFound {
		t.Errorf("error code = %d, want %d", resp.Error.Code, CodeMemoryNotFound)
	}
}

// --- SPEC-093 §3: profile_use / profile_default ------------------------------

func TestHandleProfileUse_ActivatesAndMaterializes(t *testing.T) {
	srv := newProfileTestServer(t)
	source := newMCPProfileFixtureRepo(t, "chatea-pro", "1.0.0")

	addResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "profile_add",
		Arguments: mustMarshal(t, map[string]any{"source": source}),
	})
	if addResp.Error != nil {
		t.Fatalf("profile_add: unexpected error: %s", addResp.Error.Message)
	}

	repoRoot := t.TempDir()
	resp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name:      "profile_use",
		Arguments: mustMarshal(t, map[string]any{"name": "chatea-pro", "project_root": repoRoot}),
	})
	if resp.Error != nil {
		t.Fatalf("profile_use: unexpected error: %s", resp.Error.Message)
	}

	var result struct {
		Name         string `json:"name"`
		Ref          string `json:"ref"`
		Materialized bool   `json:"materialized"`
	}
	unmarshalToolText(t, resp, &result)
	if result.Name != "chatea-pro" || !result.Materialized {
		t.Errorf("unexpected UseResult: %+v", result)
	}

	if _, err := os.Stat(filepath.Join(repoRoot, ".mneme-profile")); err != nil {
		t.Errorf("expected .mneme-profile to be written: %v", err)
	}
}

func TestHandleProfileUse_MissingName(t *testing.T) {
	srv := newProfileTestServer(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "profile_use",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if resp.Error == nil {
		t.Fatal("expected CodeInvalidParams, got nil error")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want %d", resp.Error.Code, CodeInvalidParams)
	}
}

func TestHandleProfileUse_NotInstalled(t *testing.T) {
	srv := newProfileTestServer(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "profile_use",
		Arguments: mustMarshal(t, map[string]any{"name": "nonexistent", "project_root": t.TempDir()}),
	})
	if resp.Error == nil {
		t.Fatal("expected an error for a nonexistent profile")
	}
	if resp.Error.Code != CodeMemoryNotFound {
		t.Errorf("error code = %d, want %d", resp.Error.Code, CodeMemoryNotFound)
	}
}

func TestHandleProfileDefault_SetPrintClear(t *testing.T) {
	srv := newProfileTestServer(t)
	source := newMCPProfileFixtureRepo(t, "chatea-pro", "1.0.0")
	t.Cleanup(func() {
		_ = config.SetProfilesDefault(config.DefaultPath(), "")
	})

	addResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "profile_add",
		Arguments: mustMarshal(t, map[string]any{"source": source}),
	})
	if addResp.Error != nil {
		t.Fatalf("profile_add: unexpected error: %s", addResp.Error.Message)
	}

	setResp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name:      "profile_default",
		Arguments: mustMarshal(t, map[string]any{"name": "chatea-pro"}),
	})
	if setResp.Error != nil {
		t.Fatalf("profile_default (set): unexpected error: %s", setResp.Error.Message)
	}
	var setResult struct {
		Default string `json:"default"`
	}
	unmarshalToolText(t, setResp, &setResult)
	if setResult.Default != "chatea-pro" {
		t.Errorf("set result = %+v, want Default=chatea-pro", setResult)
	}

	printResp := process(t, srv, "tools/call", 3, ToolCallParams{
		Name:      "profile_default",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if printResp.Error != nil {
		t.Fatalf("profile_default (print): unexpected error: %s", printResp.Error.Message)
	}
	var printResult struct {
		Default string `json:"default"`
	}
	unmarshalToolText(t, printResp, &printResult)
	if printResult.Default != "chatea-pro" {
		t.Errorf("print result = %+v, want Default=chatea-pro", printResult)
	}

	clearResp := process(t, srv, "tools/call", 4, ToolCallParams{
		Name:      "profile_default",
		Arguments: mustMarshal(t, map[string]any{"clear": true}),
	})
	if clearResp.Error != nil {
		t.Fatalf("profile_default (clear): unexpected error: %s", clearResp.Error.Message)
	}
	var clearResult struct {
		Default string `json:"default"`
	}
	unmarshalToolText(t, clearResp, &clearResult)
	if clearResult.Default != "" {
		t.Errorf("clear result = %+v, want empty Default", clearResult)
	}
}

func TestHandleProfileDefault_NotInstalled(t *testing.T) {
	srv := newProfileTestServer(t)
	t.Cleanup(func() {
		_ = config.SetProfilesDefault(config.DefaultPath(), "")
	})

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "profile_default",
		Arguments: mustMarshal(t, map[string]any{"name": "nonexistent"}),
	})
	if resp.Error == nil {
		t.Fatal("expected an error for a nonexistent profile")
	}
	if resp.Error.Code != CodeMemoryNotFound {
		t.Errorf("error code = %d, want %d", resp.Error.Code, CodeMemoryNotFound)
	}
}
