package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

// newMCPScaffoldFixtureRepo creates a local git profile repo (no network)
// carrying one single-layout scaffold, tagged v1.
func newMCPScaffoldFixtureRepo(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()

	mustRunGitForMCPProfile(t, dir, "init", "-q")
	mustRunGitForMCPProfile(t, dir, "config", "user.name", "mneme-test")
	mustRunGitForMCPProfile(t, dir, "config", "user.email", "mneme-test@example.com")

	writeMCPFixtureFile(t, dir, "mneme-profile.toml", "name = \""+name+"\"\nversion = \"1.0.0\"\n")
	writeMCPFixtureFile(t, dir, "scaffolds/library-go/scaffold.toml",
		"layout = \"single\"\n[vars]\nmodule_path = { prompt = \"Go module\", default = \"github.com/acme/lib\" }\n")
	writeMCPFixtureFile(t, dir, "scaffolds/library-go/skeleton/go.mod", "module {{module_path}}\n")

	mustRunGitForMCPProfile(t, dir, "add", ".")
	mustRunGitForMCPProfile(t, dir, "commit", "-q", "-m", "initial")
	mustRunGitForMCPProfile(t, dir, "tag", "v1")
	return dir
}

func writeMCPFixtureFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// --- SPEC-098 §7a: project_new ----------------------------------------------

func TestHandleProjectNew_Success(t *testing.T) {
	srv := newProfileTestServer(t)
	source := newMCPScaffoldFixtureRepo(t, "chatea-pro")

	// Install the profile, then pin a project_root to it so the active profile
	// (and its scaffold catalog) resolves to that checkout.
	if resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "profile_add",
		Arguments: mustMarshal(t, map[string]any{"source": source}),
	}); resp.Error != nil {
		t.Fatalf("profile_add: %s", resp.Error.Message)
	}

	projectRoot := t.TempDir()
	pin := "name = \"chatea-pro\"\nsource = \"" + source + "\"\n"
	if err := os.WriteFile(filepath.Join(projectRoot, ".mneme-profile"), []byte(pin), 0o644); err != nil {
		t.Fatalf("write pin: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "newlib")
	resp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name: "project_new",
		Arguments: mustMarshal(t, map[string]any{
			"scaffold":     "library-go",
			"dir":          dest,
			"vars":         map[string]any{"module_path": "github.com/wirvii/newlib"},
			"project_root": projectRoot,
		}),
	})
	if resp.Error != nil {
		t.Fatalf("project_new: unexpected error: %s", resp.Error.Message)
	}

	var result struct {
		Scaffold string `json:"scaffold"`
		Layout   string `json:"layout"`
		Path     string `json:"path"`
	}
	unmarshalToolText(t, resp, &result)
	if result.Scaffold != "library-go" || result.Layout != "single" {
		t.Errorf("unexpected result: %+v", result)
	}
	gomod, err := os.ReadFile(filepath.Join(dest, "go.mod"))
	if err != nil || string(gomod) != "module github.com/wirvii/newlib\n" {
		t.Errorf("go.mod = %q, err %v", string(gomod), err)
	}
}

func TestHandleProjectNew_MissingArgs(t *testing.T) {
	srv := newProfileTestServer(t)

	for _, args := range []map[string]any{
		{"dir": "/tmp/x"},          // missing scaffold
		{"scaffold": "library-go"}, // missing dir
	} {
		resp := process(t, srv, "tools/call", 1, ToolCallParams{
			Name:      "project_new",
			Arguments: mustMarshal(t, args),
		})
		if resp.Error == nil || resp.Error.Code != CodeInvalidParams {
			t.Errorf("args %v: want CodeInvalidParams, got %+v", args, resp.Error)
		}
	}
}

func TestHandleProjectNew_ScaffoldNotFound(t *testing.T) {
	srv := newProfileTestServer(t)
	// Vanilla project_root → embedded OSS default (no scaffolds) → not found,
	// mapped to CodeMemoryNotFound.
	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "project_new",
		Arguments: mustMarshal(t, map[string]any{
			"scaffold":     "nope",
			"dir":          filepath.Join(t.TempDir(), "x"),
			"project_root": t.TempDir(),
		}),
	})
	if resp.Error == nil || resp.Error.Code != CodeMemoryNotFound {
		t.Fatalf("want CodeMemoryNotFound, got %+v", resp.Error)
	}
}
