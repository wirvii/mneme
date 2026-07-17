package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

// newMCPMonorepoFixtureRepo creates a local git profile repo (no network)
// carrying one monorepo/turborepo scaffold plus a composable blueprint, tagged
// v1 — the source an app_add MCP test installs.
func newMCPMonorepoFixtureRepo(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()

	mustRunGitForMCPProfile(t, dir, "init", "-q")
	mustRunGitForMCPProfile(t, dir, "config", "user.name", "mneme-test")
	mustRunGitForMCPProfile(t, dir, "config", "user.email", "mneme-test@example.com")

	writeMCPFixtureFile(t, dir, "mneme-profile.toml", "name = \""+name+"\"\nversion = \"1.0.0\"\n")
	writeMCPFixtureFile(t, dir, "scaffolds/saas/scaffold.toml",
		"layout = \"monorepo\"\ntoolchain = \"turborepo\"\nbootstrap = \"create-turbo@2.3.1\"\nblueprints = [\"go-core-srv\"]\n")
	writeMCPFixtureFile(t, dir, "_blueprints/go-core-srv/main.go", "package main\n")

	mustRunGitForMCPProfile(t, dir, "add", ".")
	mustRunGitForMCPProfile(t, dir, "commit", "-q", "-m", "initial")
	mustRunGitForMCPProfile(t, dir, "tag", "v1")
	return dir
}

// --- SPEC-099 §7b: app_add --------------------------------------------------

func TestHandleAppAdd_Success(t *testing.T) {
	srv := newProfileTestServer(t)
	source := newMCPMonorepoFixtureRepo(t, "saas-profile")

	if resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "profile_add",
		Arguments: mustMarshal(t, map[string]any{"source": source}),
	}); resp.Error != nil {
		t.Fatalf("profile_add: %s", resp.Error.Message)
	}

	// A monorepo root pinned to the installed profile, recording scaffold=saas.
	monorepo := t.TempDir()
	pin := "name = \"saas-profile\"\nsource = \"" + source + "\"\nscaffold = \"saas\"\n"
	if err := os.WriteFile(filepath.Join(monorepo, ".mneme-profile"), []byte(pin), 0o644); err != nil {
		t.Fatalf("write pin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(monorepo, "pnpm-workspace.yaml"), []byte("packages:\n  - \"apps/*\"\n"), 0o644); err != nil {
		t.Fatalf("write workspace: %v", err)
	}

	resp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name: "app_add",
		Arguments: mustMarshal(t, map[string]any{
			"blueprint": "go-core-srv",
			"name":      "billing",
			"dir":       monorepo,
		}),
	})
	if resp.Error != nil {
		t.Fatalf("app_add: unexpected error: %s", resp.Error.Message)
	}

	var result struct {
		Blueprint string `json:"blueprint"`
		App       string `json:"app"`
		Scaffold  string `json:"scaffold"`
	}
	unmarshalToolText(t, resp, &result)
	if result.App != "billing" || result.Blueprint != "go-core-srv" || result.Scaffold != "saas" {
		t.Errorf("unexpected result: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(monorepo, "apps", "billing", "main.go")); err != nil {
		t.Errorf("app not created: %v", err)
	}
}

func TestHandleAppAdd_MissingArgs(t *testing.T) {
	srv := newProfileTestServer(t)
	for _, args := range []map[string]any{
		{"name": "x"},           // missing blueprint
		{"blueprint": "go-svc"}, // missing name
	} {
		resp := process(t, srv, "tools/call", 1, ToolCallParams{
			Name:      "app_add",
			Arguments: mustMarshal(t, args),
		})
		if resp.Error == nil || resp.Error.Code != CodeInvalidParams {
			t.Errorf("args %v: want CodeInvalidParams, got %+v", args, resp.Error)
		}
	}
}

func TestHandleAppAdd_NoScaffoldInPin(t *testing.T) {
	srv := newProfileTestServer(t)
	// A vanilla monorepo root (no pin/scaffold) → ErrScaffoldNotFound →
	// CodeMemoryNotFound.
	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "app_add",
		Arguments: mustMarshal(t, map[string]any{
			"blueprint": "go-core-srv",
			"name":      "billing",
			"dir":       t.TempDir(),
		}),
	})
	if resp.Error == nil || resp.Error.Code != CodeMemoryNotFound {
		t.Fatalf("want CodeMemoryNotFound, got %+v", resp.Error)
	}
}
