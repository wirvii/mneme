package mcp

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// newSkillsTestServer creates a Server with a real SkillsService targeting a
// temp directory containing the example-skill.
func newSkillsTestServer(t *testing.T) (*Server, string) {
	t.Helper()

	skillsDir := t.TempDir()

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

	projectStore := store.NewMemoryStore(projectDB)
	globalStore := store.NewMemoryStore(globalDB)
	cfg := config.Default()
	svc := service.NewMemoryService(projectStore, globalStore, cfg, "test-project", embed.NopEmbedder{})

	skillsSvc := service.NewSkillsService(skillsDir)

	logger := slog.Default()
	srv := NewServer(svc, nil, skillsSvc, nil, logger, "all", "test")
	return srv, skillsDir
}

// installExampleSkill installs the example-skill to the skillsDir via the
// MCP server to ensure the test environment is set up correctly.
func installExampleSkill(t *testing.T, srv *Server) {
	t.Helper()
	resp := process(t, srv, "tools/call", 10, ToolCallParams{
		Name:      "skills_install",
		Arguments: mustMarshal(t, map[string]any{"name": "example-skill"}),
	})
	if resp.Error != nil {
		t.Fatalf("skills_install example-skill: %s", resp.Error.Message)
	}
}

func TestHandleSkillsList(t *testing.T) {
	srv, _ := newSkillsTestServer(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "skills_list",
		Arguments: mustMarshal(t, map[string]any{}),
	})

	if resp.Error != nil {
		t.Fatalf("skills_list: unexpected error: %s", resp.Error.Message)
	}

	var result ToolCallResult
	if err := json.Unmarshal(mustMarshal(t, resp.Result), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Should include at least the bundled example-skill.
	if len(result.Content) == 0 {
		t.Fatal("expected content in skills_list result")
	}
}

func TestHandleSkillsInstall_NotFound(t *testing.T) {
	srv, _ := newSkillsTestServer(t)

	resp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name:      "skills_install",
		Arguments: mustMarshal(t, map[string]any{"name": "nonexistent"}),
	})

	if resp.Error == nil {
		t.Fatal("expected JSON-RPC error for nonexistent skill")
	}
	if resp.Error.Code != CodeMemoryNotFound {
		t.Errorf("error code = %d, want %d (CodeMemoryNotFound)", resp.Error.Code, CodeMemoryNotFound)
	}
}

func TestHandleSkillsInstallAndPin(t *testing.T) {
	srv, skillsDir := newSkillsTestServer(t)

	// Install.
	installExampleSkill(t, srv)

	// Verify it's on disk.
	if _, err := os.Stat(filepath.Join(skillsDir, "example-skill", "SKILL.md")); err != nil {
		t.Fatalf("SKILL.md not found after install: %v", err)
	}

	// Pin.
	resp := process(t, srv, "tools/call", 3, ToolCallParams{
		Name:      "skills_pin",
		Arguments: mustMarshal(t, map[string]any{"name": "example-skill"}),
	})
	if resp.Error != nil {
		t.Fatalf("skills_pin: %s", resp.Error.Message)
	}

	// Try to reinstall without force — should be blocked.
	resp = process(t, srv, "tools/call", 4, ToolCallParams{
		Name:      "skills_install",
		Arguments: mustMarshal(t, map[string]any{"name": "example-skill"}),
	})
	if resp.Error == nil {
		t.Fatal("expected error when reinstalling pinned skill without force")
	}

	// Unpin.
	resp = process(t, srv, "tools/call", 5, ToolCallParams{
		Name:      "skills_unpin",
		Arguments: mustMarshal(t, map[string]any{"name": "example-skill"}),
	})
	if resp.Error != nil {
		t.Fatalf("skills_unpin: %s", resp.Error.Message)
	}
}

func TestHandleSkillsRemove_Pinned(t *testing.T) {
	srv, skillsDir := newSkillsTestServer(t)

	installExampleSkill(t, srv)

	// Pin it.
	resp := process(t, srv, "tools/call", 6, ToolCallParams{
		Name:      "skills_pin",
		Arguments: mustMarshal(t, map[string]any{"name": "example-skill"}),
	})
	if resp.Error != nil {
		t.Fatalf("skills_pin: %s", resp.Error.Message)
	}

	// Remove without force — should fail.
	resp = process(t, srv, "tools/call", 7, ToolCallParams{
		Name:      "skills_remove",
		Arguments: mustMarshal(t, map[string]any{"name": "example-skill"}),
	})
	if resp.Error == nil {
		t.Fatal("expected error when removing pinned skill without force")
	}

	// Remove with force — should succeed.
	resp = process(t, srv, "tools/call", 8, ToolCallParams{
		Name:      "skills_remove",
		Arguments: mustMarshal(t, map[string]any{"name": "example-skill", "force": true}),
	})
	if resp.Error != nil {
		t.Fatalf("skills_remove --force: %s", resp.Error.Message)
	}

	// Verify it's gone.
	if _, err := os.Stat(filepath.Join(skillsDir, "example-skill")); !os.IsNotExist(err) {
		t.Error("skill dir should be removed after skills_remove --force")
	}
}

func TestHandleSkillsLint_PassWithIsError(t *testing.T) {
	srv, _ := newSkillsTestServer(t)
	installExampleSkill(t, srv)

	// Lint the passing skill — should succeed without IsError.
	resp := process(t, srv, "tools/call", 9, ToolCallParams{
		Name:      "skills_lint",
		Arguments: mustMarshal(t, map[string]any{"name": "example-skill"}),
	})
	if resp.Error != nil {
		t.Fatalf("skills_lint: %s", resp.Error.Message)
	}

	var toolResult ToolCallResult
	if err := json.Unmarshal(mustMarshal(t, resp.Result), &toolResult); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if toolResult.IsError {
		t.Error("passing lint should not return IsError=true")
	}
}

func TestHandleSkillsLint_FailReturnsIsError(t *testing.T) {
	srv, skillsDir := newSkillsTestServer(t)

	// Write a malformed skill — missing required sections.
	d := filepath.Join(skillsDir, "bad-skill")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "SKILL.md"),
		[]byte("---\nname: bad-skill\nversion: 1.0.0\n---\nNo sections here.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	resp := process(t, srv, "tools/call", 10, ToolCallParams{
		Name:      "skills_lint",
		Arguments: mustMarshal(t, map[string]any{"name": "bad-skill"}),
	})

	// Should not be a JSON-RPC error — it should be IsError in the result.
	if resp.Error != nil {
		t.Fatalf("expected IsError payload, got JSON-RPC error: %s", resp.Error.Message)
	}

	var toolResult ToolCallResult
	if err := json.Unmarshal(mustMarshal(t, resp.Result), &toolResult); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !toolResult.IsError {
		t.Error("failing lint should return IsError=true")
	}
}

func TestHandleSkillsValidate_PassAndFail(t *testing.T) {
	srv, skillsDir := newSkillsTestServer(t)
	installExampleSkill(t, srv)

	// Validate example-skill — should pass.
	resp := process(t, srv, "tools/call", 11, ToolCallParams{
		Name:      "skills_validate",
		Arguments: mustMarshal(t, map[string]any{"name": "example-skill"}),
	})
	if resp.Error != nil {
		t.Fatalf("skills_validate: %s", resp.Error.Message)
	}

	var toolResult ToolCallResult
	if err := json.Unmarshal(mustMarshal(t, resp.Result), &toolResult); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if toolResult.IsError {
		t.Errorf("example-skill validation should pass, got IsError=true; content: %s", toolResult.Content[0].Text)
	}

	// Create a failing validation skill.
	d := filepath.Join(skillsDir, "fail-skill")
	if err := os.MkdirAll(filepath.Join(d, "validation"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "SKILL.md"),
		[]byte("---\nname: fail-skill\nversion: 1.0.0\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "validation", "run.sh"),
		[]byte("#!/bin/sh\necho 'fail'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	resp = process(t, srv, "tools/call", 12, ToolCallParams{
		Name:      "skills_validate",
		Arguments: mustMarshal(t, map[string]any{"name": "fail-skill"}),
	})
	if resp.Error != nil {
		t.Fatalf("skills_validate (fail case): %s", resp.Error.Message)
	}

	if err := json.Unmarshal(mustMarshal(t, resp.Result), &toolResult); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !toolResult.IsError {
		t.Error("failing validation should return IsError=true")
	}
}

func TestHandleSkillsValidate_NoScript(t *testing.T) {
	srv, skillsDir := newSkillsTestServer(t)

	// Skill without validation/run.sh.
	d := filepath.Join(skillsDir, "no-validator")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "SKILL.md"),
		[]byte("---\nname: no-validator\nversion: 1.0.0\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	resp := process(t, srv, "tools/call", 13, ToolCallParams{
		Name:      "skills_validate",
		Arguments: mustMarshal(t, map[string]any{"name": "no-validator"}),
	})
	// Should be IsError:true payload, not a JSON-RPC error.
	if resp.Error != nil {
		t.Fatalf("expected IsError payload for no-validation, got JSON-RPC error: %s", resp.Error.Message)
	}

	var toolResult ToolCallResult
	if err := json.Unmarshal(mustMarshal(t, resp.Result), &toolResult); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !toolResult.IsError {
		t.Error("no-validation skill should return IsError=true")
	}
}

func TestHandleSkillsUnavailable(t *testing.T) {
	// Server without a skillsSvc.
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

	ps := store.NewMemoryStore(projectDB)
	gs := store.NewMemoryStore(globalDB)
	cfg := config.Default()
	svc := service.NewMemoryService(ps, gs, cfg, "test-project", embed.NopEmbedder{})

	logger := slog.Default()
	srv := NewServer(svc, nil, nil, nil, logger, "all", "test")

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "skills_list",
		Arguments: mustMarshal(t, map[string]any{}),
	})

	if resp.Error == nil {
		t.Fatal("expected JSON-RPC error when skillsSvc is nil")
	}
	if resp.Error.Code != CodeInternalError {
		t.Errorf("error code = %d, want %d (CodeInternalError)", resp.Error.Code, CodeInternalError)
	}
}

