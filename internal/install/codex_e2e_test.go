package install

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// TestCodexInstall_Idempotency is an end-to-end test that runs Install(Codex(...))
// twice against a temporary HOME directory and verifies:
//   - All three artefacts are created: config.toml, hooks.json, AGENTS.md.
//   - The bundled example-skill is installed to $tmpHome/.agents/skills/.
//   - A second Install() call produces byte-identical artefacts (idempotency, AC-2, AC-3).
func TestCodexInstall_Idempotency(t *testing.T) {
	tmpHome := t.TempDir()

	// Override HOME so all os.UserHomeDir() calls inside the agent point to tmpHome.
	t.Setenv("HOME", tmpHome)

	const binPath = "/usr/local/bin/mneme"
	agent := Codex(binPath)

	// First install.
	if err := Install(agent, binPath); err != nil {
		t.Fatalf("first Install: %v", err)
	}

	configPath := filepath.Join(tmpHome, ".codex", "config.toml")
	hooksPath := filepath.Join(tmpHome, ".codex", "hooks.json")
	agentsPath := filepath.Join(tmpHome, ".codex", "AGENTS.md")
	skillDir := filepath.Join(tmpHome, ".agents", "skills", "example-skill", "SKILL.md")

	// Verify artefacts exist after first run.
	for _, path := range []string{configPath, hooksPath, agentsPath} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("after first install, %s: %v", path, err)
		}
	}
	if _, err := os.Stat(skillDir); err != nil {
		t.Errorf("after first install, skill SKILL.md missing at %s: %v", skillDir, err)
	}

	// Read all artefacts.
	readFile := func(path string) []byte {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", path, err)
		}
		return data
	}
	firstConfig := readFile(configPath)
	firstHooks := readFile(hooksPath)
	firstAgents := readFile(agentsPath)

	// Second install.
	if err := Install(agent, binPath); err != nil {
		t.Fatalf("second Install: %v", err)
	}

	secondConfig := readFile(configPath)
	secondHooks := readFile(hooksPath)
	secondAgents := readFile(agentsPath)

	// Verify idempotency (byte-identical).
	if !bytes.Equal(firstConfig, secondConfig) {
		t.Errorf("config.toml not idempotent.\nFirst:\n%s\nSecond:\n%s", firstConfig, secondConfig)
	}
	if !bytes.Equal(firstHooks, secondHooks) {
		t.Errorf("hooks.json not idempotent.\nFirst:\n%s\nSecond:\n%s", firstHooks, secondHooks)
	}
	if !bytes.Equal(firstAgents, secondAgents) {
		t.Errorf("AGENTS.md not idempotent.\nFirst:\n%s\nSecond:\n%s", firstAgents, secondAgents)
	}
}

// TestCodexInstall_Artefacts verifies the content of each artefact created by
// Codex install: config.toml has mcp_servers.mneme + CLAUDE.md fallback;
// hooks.json has SessionStart and Stop entries; AGENTS.md has the managed block.
func TestCodexInstall_Artefacts(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	const binPath = "/usr/local/bin/mneme"
	if err := Install(Codex(binPath), binPath); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// --- config.toml (AC-1) ---
	configPath := filepath.Join(tmpHome, ".codex", "config.toml")
	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	var cfg map[string]any
	if err := toml.Unmarshal(configData, &cfg); err != nil {
		t.Fatalf("parse config.toml: %v", err)
	}
	mcpServers, ok := cfg["mcp_servers"].(map[string]any)
	if !ok {
		t.Fatal("config.toml: mcp_servers missing or not a table")
	}
	mneme, ok := mcpServers["mneme"].(map[string]any)
	if !ok {
		t.Fatal("config.toml: mcp_servers.mneme missing or not a table")
	}
	if mneme["command"] != binPath {
		t.Errorf("config.toml: mcp_servers.mneme.command = %q, want %q", mneme["command"], binPath)
	}
	// Verify args match exactly ["mcp", "--tools=agent"] (AC-1, mirrors install_test.go:598-600).
	args := toStringSlice(mneme["args"])
	if len(args) != 2 || args[0] != "mcp" || args[1] != "--tools=agent" {
		t.Errorf("config.toml: mcp_servers.mneme.args = %v, want [mcp --tools=agent]", args)
	}
	fallbacks := toStringSlice(cfg["project_doc_fallback_filenames"])
	foundClaude := false
	for _, f := range fallbacks {
		if f == "CLAUDE.md" {
			foundClaude = true
		}
	}
	if !foundClaude {
		t.Errorf("config.toml: project_doc_fallback_filenames missing CLAUDE.md: %v", fallbacks)
	}

	// --- hooks.json (AC-3) ---
	hooksPath := filepath.Join(tmpHome, ".codex", "hooks.json")
	hooksData, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("read hooks.json: %v", err)
	}
	var hooksRoot map[string]any
	if err := json.Unmarshal(hooksData, &hooksRoot); err != nil {
		t.Fatalf("parse hooks.json: %v", err)
	}
	hooks, ok := hooksRoot["hooks"].(map[string]any)
	if !ok {
		t.Fatal("hooks.json: root hooks key missing or not an object")
	}
	for _, event := range []string{"SessionStart", "Stop"} {
		if _, ok := hooks[event]; !ok {
			t.Errorf("hooks.json: event %q missing", event)
		}
	}

	// --- AGENTS.md (AC-4) ---
	agentsPath := filepath.Join(tmpHome, ".codex", "AGENTS.md")
	agentsData, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	agentsContent := string(agentsData)
	if !strings.Contains(agentsContent, "<!-- mneme:managed:start") {
		t.Error("AGENTS.md: managed block start marker missing")
	}
	if !strings.Contains(agentsContent, "<!-- mneme:managed:end -->") {
		t.Error("AGENTS.md: managed block end marker missing")
	}
	// Verify block size < 32 KiB (AC-4).
	if len(agentsData) >= 32*1024 {
		t.Errorf("AGENTS.md: size %d bytes >= 32 KiB limit", len(agentsData))
	}
}

// TestCodexInstall_NoDryRunSteps verifies that the Codex agent's step list
// does not include steps that are Claude-specific and must be absent (AC-5).
func TestCodexInstall_NoDryRunSteps(t *testing.T) {
	agent := Codex("/usr/local/bin/mneme")
	opts := InstallOptions{BinaryPath: "/usr/local/bin/mneme"}
	steps := agent.installSteps(opts)

	var names []string
	for _, s := range steps {
		names = append(names, s.Name)
	}

	// Steps that must be present.
	required := []string{"MCP server", "Session hooks", "Operating manual", "Workflow templates", "Skills", "Workflow directories"}
	for _, req := range required {
		found := false
		for _, n := range names {
			if n == req {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Codex steps: required step %q is missing; got %v", req, names)
		}
	}

	// Steps that must NOT be present for Codex.
	forbidden := []string{"Agent profiles", "Agent models", "Delegation hook", "Delegation hook (reinstall)", "Slash commands"}
	for _, forb := range forbidden {
		for _, n := range names {
			if n == forb {
				t.Errorf("Codex steps: step %q must not be present for single-agent Codex; got %v", forb, names)
			}
		}
	}
}

// TestCodexOperatingManual_AntiDrift verifies that the embedded
// operating-manual-codex.md contains all 5 canonical sections. This mirrors
// TestAgentsCodegraphPolicy as an anti-drift gate: if the asset loses a
// required section the test fails immediately.
func TestCodexOperatingManual_AntiDrift(t *testing.T) {
	content := operatingManualCodex()

	if len(content) == 0 {
		t.Fatal("operatingManualCodex() returned empty string")
	}

	// Verify the asset is compact enough for Codex's 32 KiB limit (S3, AC-4).
	if len(content) >= 32*1024 {
		t.Errorf("operating-manual-codex.md size %d bytes exceeds 32 KiB Codex limit", len(content))
	}

	// Canonical sections that must be present (§1–§5, SPEC-049 D4).
	sections := []string{
		"## §1 How to launch",
		"## §2 Single-agent model",
		"## §3 SDD + lanes",
		"## §4 Skills",
		"## §5 Memory & conflicts",
	}
	for _, sec := range sections {
		if !strings.Contains(content, sec) {
			t.Errorf("operating-manual-codex.md: canonical section %q is missing", sec)
		}
	}

	// Key concepts that must be present.
	keywords := []string{
		"single agent",
		"mem_context",
		"mem_search",
		"mem_save",
		"mem_session_end",
		"backlog_add",
		"spec_advance",
		"AGENTS.md",
		"grill-me",
		"Do NOT use `superpowers:brainstorming`",
	}
	for _, kw := range keywords {
		if !strings.Contains(content, kw) {
			t.Errorf("operating-manual-codex.md: expected keyword %q not found", kw)
		}
	}
}

// TestCodexBuilder_Fields verifies that Codex() returns an Agent with the
// expected field values as declared in SPEC-049 D1–D4.
func TestCodexBuilder_Fields(t *testing.T) {
	const binPath = "/usr/local/bin/mneme"
	agent := Codex(binPath)

	if agent.Name != "Codex" {
		t.Errorf("Name = %q, want \"Codex\"", agent.Name)
	}
	if agent.Slug != "codex" {
		t.Errorf("Slug = %q, want \"codex\"", agent.Slug)
	}
	if agent.MCPConfig != nil {
		t.Error("MCPConfig must be nil for Codex (TOML writer is in MCPConfigWriter)")
	}
	if agent.MCPConfigWriter == nil {
		t.Error("MCPConfigWriter must not be nil")
	}
	if agent.Hooks != nil {
		t.Error("Hooks must be nil for Codex (hooks writer is in HooksWriter)")
	}
	if agent.HooksWriter == nil {
		t.Error("HooksWriter must not be nil")
	}
	if agent.Manual == nil {
		t.Error("Manual must not be nil (injects operating manual into AGENTS.md)")
	}
	if agent.Commands != nil {
		t.Error("Commands must be nil for Codex (single-agent, no slash commands)")
	}
	if agent.Agents != nil {
		t.Error("Agents must be nil for Codex (no subagent profiles)")
	}
	if agent.DelegationHook != nil {
		t.Error("DelegationHook must be nil for Codex (no role enforcement)")
	}
	if agent.Skills == nil {
		t.Error("Skills must not be nil (bundled skills are installed)")
	}
	if agent.AgentsDir != "" {
		t.Errorf("AgentsDir must be empty for Codex (skip Agent models step), got %q", agent.AgentsDir)
	}
	if agent.LegacyAgentsCleanupDir != "" {
		t.Errorf("LegacyAgentsCleanupDir must be empty for Codex (never wrote global agent profiles, SPEC-073), got %q", agent.LegacyAgentsCleanupDir)
	}
	if agent.SkillsDir == "" {
		t.Error("SkillsDir must not be empty (Codex discovery path $HOME/.agents/skills)")
	}
	if !strings.Contains(agent.SkillsDir, ".agents") || !strings.Contains(agent.SkillsDir, "skills") {
		t.Errorf("SkillsDir should contain .agents/skills, got %q", agent.SkillsDir)
	}
}
