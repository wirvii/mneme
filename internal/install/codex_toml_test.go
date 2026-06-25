package install

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

const testBinary = "/usr/local/bin/mneme"

// TestWriteCodexConfig_NewFile verifies that WriteCodexConfig creates
// config.toml from scratch with the correct MCP server entry and fallback list.
func TestWriteCodexConfig_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".codex", "config.toml")

	if err := WriteCodexConfig(path, testBinary); err != nil {
		t.Fatalf("WriteCodexConfig: %v", err)
	}

	var cfg map[string]any
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// Verify mcp_servers.mneme.
	mcpServers, ok := cfg["mcp_servers"].(map[string]any)
	if !ok {
		t.Fatal("mcp_servers is missing or not a table")
	}
	mneme, ok := mcpServers["mneme"].(map[string]any)
	if !ok {
		t.Fatal("mcp_servers.mneme is missing or not a table")
	}
	if mneme["command"] != testBinary {
		t.Errorf("command: got %q, want %q", mneme["command"], testBinary)
	}

	// Verify project_doc_fallback_filenames contains "CLAUDE.md".
	fallbacks := toStringSlice(cfg["project_doc_fallback_filenames"])
	found := false
	for _, f := range fallbacks {
		if f == "CLAUDE.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("project_doc_fallback_filenames does not contain CLAUDE.md: %v", fallbacks)
	}
}

// TestWriteCodexConfig_Idempotent verifies that running WriteCodexConfig twice
// on a freshly created file produces a byte-identical result.
func TestWriteCodexConfig_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".codex", "config.toml")

	if err := WriteCodexConfig(path, testBinary); err != nil {
		t.Fatalf("first run: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after first run: %v", err)
	}

	if err := WriteCodexConfig(path, testBinary); err != nil {
		t.Fatalf("second run: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after second run: %v", err)
	}

	if string(first) != string(second) {
		t.Errorf("WriteCodexConfig is not idempotent.\nFirst:\n%s\nSecond:\n%s", first, second)
	}
}

// TestWriteCodexConfig_PreservesExistingKeys verifies that WriteCodexConfig
// leaves unrelated keys in config.toml untouched.
func TestWriteCodexConfig_PreservesExistingKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	// Seed file with an unrelated key.
	existing := "model = \"gpt-5.5\"\n[other_section]\nfoo = \"bar\"\n"
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if err := WriteCodexConfig(path, testBinary); err != nil {
		t.Fatalf("WriteCodexConfig: %v", err)
	}

	var cfg map[string]any
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if cfg["model"] != "gpt-5.5" {
		t.Errorf("model key was clobbered: got %v", cfg["model"])
	}
	otherSection, ok := cfg["other_section"].(map[string]any)
	if !ok {
		t.Errorf("other_section missing or wrong type: %T %v", cfg["other_section"], cfg["other_section"])
	} else if otherSection["foo"] != "bar" {
		t.Errorf("other_section.foo was clobbered: got %v", otherSection["foo"])
	}
}

// TestWriteCodexConfig_NoDuplicateCLAUDE verifies that running WriteCodexConfig
// twice does not add "CLAUDE.md" twice to project_doc_fallback_filenames.
func TestWriteCodexConfig_NoDuplicateCLAUDE(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	// Run twice.
	if err := WriteCodexConfig(path, testBinary); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := WriteCodexConfig(path, testBinary); err != nil {
		t.Fatalf("second run: %v", err)
	}

	var cfg map[string]any
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	fallbacks := toStringSlice(cfg["project_doc_fallback_filenames"])
	count := 0
	for _, f := range fallbacks {
		if f == "CLAUDE.md" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 occurrence of CLAUDE.md, got %d: %v", count, fallbacks)
	}
}

// TestWriteCodexConfig_PreservesOtherMCPServers verifies that an existing
// MCP server entry (not "mneme") is preserved after WriteCodexConfig.
func TestWriteCodexConfig_PreservesOtherMCPServers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	seed := "[mcp_servers.other]\ncommand = \"other-server\"\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if err := WriteCodexConfig(path, testBinary); err != nil {
		t.Fatalf("WriteCodexConfig: %v", err)
	}

	var cfg map[string]any
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	mcpServers, ok := cfg["mcp_servers"].(map[string]any)
	if !ok {
		t.Fatal("mcp_servers missing")
	}
	if _, ok := mcpServers["other"]; !ok {
		t.Error("mcp_servers.other was removed")
	}
	if _, ok := mcpServers["mneme"]; !ok {
		t.Error("mcp_servers.mneme was not added")
	}
}
