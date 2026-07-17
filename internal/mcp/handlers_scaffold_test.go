package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

// --- SPEC-100 §7c: scaffold_capture -----------------------------------------

func TestHandleScaffoldCapture_Success(t *testing.T) {
	srv := newProfileTestServer(t)

	repo := t.TempDir()
	writeMCPFixtureFile(t, repo, "go.mod", "module github.com/acme/lib\n")
	writeMCPFixtureFile(t, repo, "lib.go", "package lib // github.com/acme/lib")
	profileDir := t.TempDir()

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "scaffold_capture",
		Arguments: mustMarshal(t, map[string]any{
			"repo": repo,
			"name": "library-go",
			"into": profileDir,
		}),
	})
	if resp.Error != nil {
		t.Fatalf("scaffold_capture: unexpected error: %s", resp.Error.Message)
	}

	var result struct {
		Scaffold string   `json:"scaffold"`
		Layout   string   `json:"layout"`
		Vars     []string `json:"vars"`
	}
	unmarshalToolText(t, resp, &result)
	if result.Scaffold != "library-go" || result.Layout != "single" {
		t.Errorf("unexpected result: %+v", result)
	}

	// The parametrized skeleton is written and the module path was rewritten.
	libData, err := os.ReadFile(filepath.Join(profileDir, "scaffolds", "library-go", "skeleton", "lib.go"))
	if err != nil {
		t.Fatalf("read captured skeleton: %v", err)
	}
	if string(libData) != "package lib // {{MODULE_PATH}}" {
		t.Errorf("skeleton not parametrized: %q", libData)
	}
}

func TestHandleScaffoldCapture_MissingRepo(t *testing.T) {
	srv := newProfileTestServer(t)
	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "scaffold_capture",
		Arguments: mustMarshal(t, map[string]any{"name": "x"}),
	})
	if resp.Error == nil || resp.Error.Code != CodeInvalidParams {
		t.Fatalf("want CodeInvalidParams, got %+v", resp.Error)
	}
}

func TestHandleScaffoldCapture_NothingToCapture(t *testing.T) {
	srv := newProfileTestServer(t)
	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "scaffold_capture",
		Arguments: mustMarshal(t, map[string]any{
			"repo": t.TempDir(), // empty exemplar
			"name": "x",
			"into": t.TempDir(),
		}),
	})
	if resp.Error == nil {
		t.Fatal("want error for an empty exemplar repo")
	}
}
