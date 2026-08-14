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

// TestCodexInstall_IdempotencyWithPreexistingStopHook covers AC10(b) for
// Codex: a HOME whose hooks.json already carries the retired Stop
// registration has it purged on the first install; a second install finds
// nothing left to remove and hooks.json stays byte-identical from that point
// on. Content-only, not mtime: WriteCodexHooks (the HooksWriter step)
// unconditionally rewrites hooks.json on every install regardless of the
// "Retire stale hooks" step's own no-write behaviour — same reasoning as
// TestClaudeCodeInstall_IdempotencyWithPreexistingStopHook.
func TestCodexInstall_IdempotencyWithPreexistingStopHook(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	codexDir := filepath.Join(tmpHome, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	hooksPath := filepath.Join(codexDir, "hooks.json")
	existing := `{
  "hooks": {
    "Stop": [
      {"hooks": [{"type": "command", "command": "mneme hook session-end"}]}
    ]
  }
}`
	if err := os.WriteFile(hooksPath, []byte(existing), 0o644); err != nil {
		t.Fatalf("write initial hooks.json: %v", err)
	}

	const binPath = "/usr/local/bin/mneme"

	if err := Install(Codex(binPath), binPath); err != nil {
		t.Fatalf("first Install: %v", err)
	}
	afterFirst, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("read hooks.json after first install: %v", err)
	}
	firstHooks := parseCodexHooks(t, hooksPath)
	if _, exists := firstHooks["Stop"]; exists {
		t.Fatalf("precondition failed: Stop key still present after first install: %#v", firstHooks["Stop"])
	}

	if err := Install(Codex(binPath), binPath); err != nil {
		t.Fatalf("second Install: %v", err)
	}
	afterSecond, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("read hooks.json after second install: %v", err)
	}

	if !bytes.Equal(afterFirst, afterSecond) {
		t.Errorf("hooks.json not byte-identical between 1st and 2nd install:\n1st:\n%s\n2nd:\n%s", afterFirst, afterSecond)
	}
}

// TestCodexInstall_RetiresStopHook covers AC9 (SPEC-106) — the E2E proof of
// DD8: removeHookCommands, unmodified, purges Codex's hooks.json exactly the
// same way it purges Claude Code's settings.json. Two cases: a foreign Stop
// entry survives alongside mneme's (which is removed); with only mneme's
// entry present, the Stop key disappears entirely.
func TestCodexInstall_RetiresStopHook(t *testing.T) {
	const binPath = "/usr/local/bin/mneme"

	t.Run("foreign Stop entry survives", func(t *testing.T) {
		tmpHome := t.TempDir()
		t.Setenv("HOME", tmpHome)

		codexDir := filepath.Join(tmpHome, ".codex")
		if err := os.MkdirAll(codexDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		hooksPath := filepath.Join(codexDir, "hooks.json")
		existing := `{
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "mneme hook session-start"}]}
    ],
    "Stop": [
      {"hooks": [{"type": "command", "command": "mneme hook session-end"}]},
      {"hooks": [{"type": "command", "command": "/home/u/codex-stop.sh"}]}
    ]
  }
}`
		if err := os.WriteFile(hooksPath, []byte(existing), 0o644); err != nil {
			t.Fatalf("write initial hooks.json: %v", err)
		}

		if err := Install(Codex(binPath), binPath); err != nil {
			t.Fatalf("Install: %v", err)
		}

		hooks := parseCodexHooks(t, hooksPath)
		stopList, ok := hooks["Stop"].([]any)
		if !ok {
			t.Fatal("Stop key removed entirely; expected the foreign entry to survive")
		}
		foundForeign, foundMneme := false, false
		for _, item := range stopList {
			group, ok := item.(map[string]any)
			if !ok {
				continue
			}
			inner, _ := group["hooks"].([]any)
			for _, h := range inner {
				entry, ok := h.(map[string]any)
				if !ok {
					continue
				}
				switch entry["command"] {
				case "mneme hook session-end":
					foundMneme = true
				case "/home/u/codex-stop.sh":
					foundForeign = true
				}
			}
		}
		if foundMneme {
			t.Error("mneme's Stop entry should have been purged, still present")
		}
		if !foundForeign {
			t.Error("foreign Stop entry /home/u/codex-stop.sh was removed")
		}
	})

	t.Run("only mneme's entry: Stop key removed entirely", func(t *testing.T) {
		tmpHome := t.TempDir()
		t.Setenv("HOME", tmpHome)

		codexDir := filepath.Join(tmpHome, ".codex")
		if err := os.MkdirAll(codexDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		hooksPath := filepath.Join(codexDir, "hooks.json")
		existing := `{
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "mneme hook session-start"}]}
    ],
    "Stop": [
      {"hooks": [{"type": "command", "command": "mneme hook session-end"}]}
    ]
  }
}`
		if err := os.WriteFile(hooksPath, []byte(existing), 0o644); err != nil {
			t.Fatalf("write initial hooks.json: %v", err)
		}

		if err := Install(Codex(binPath), binPath); err != nil {
			t.Fatalf("Install: %v", err)
		}

		hooks := parseCodexHooks(t, hooksPath)
		if _, exists := hooks["Stop"]; exists {
			t.Errorf("Stop key must not exist, got %#v", hooks["Stop"])
		}
	})
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
	if _, ok := hooks["SessionStart"]; !ok {
		t.Error("hooks.json: event \"SessionStart\" missing")
	}
	// AC6 (SPEC-106): "Stop" is retired — it must be absent, not merely
	// unasserted.
	if _, ok := hooks["Stop"]; ok {
		t.Errorf("hooks.json: event \"Stop\" must be absent (SPEC-106 D4), got %#v", hooks["Stop"])
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
// does not include steps that are Claude-specific and must be absent (AC-5),
// and — SPEC-106 AC11 — that "Retire stale hooks" is present at exactly
// "Session hooks" + 1.
func TestCodexInstall_NoDryRunSteps(t *testing.T) {
	agent := Codex("/usr/local/bin/mneme")
	opts := InstallOptions{BinaryPath: "/usr/local/bin/mneme"}
	steps := agent.installSteps(opts)

	var names []string
	for _, s := range steps {
		names = append(names, s.Name)
	}

	// Steps that must be present.
	required := []string{"MCP server", "Session hooks", "Retire stale hooks", "Operating manual", "Workflow templates", "Skills", "Workflow directories"}
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

	sessionHooksIdx := indexOfStepName(names, "Session hooks")
	retireIdx := indexOfStepName(names, "Retire stale hooks")
	if sessionHooksIdx == -1 || retireIdx != sessionHooksIdx+1 {
		t.Errorf("\"Retire stale hooks\" must sit immediately after \"Session hooks\": got indices %d and %d in %v", sessionHooksIdx, retireIdx, names)
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

// indexOfStepName returns the index of name in names, or -1 if absent.
func indexOfStepName(names []string, name string) int {
	for i, n := range names {
		if n == name {
			return i
		}
	}
	return -1
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
		// SPEC-121: la sección de lenguaje llano, añadida al final sin
		// renumerar las anteriores.
		"## §7 Plain language: everything a person reads",
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
		// SPEC-121: los dos anclajes compartidos que cierran la frontera de
		// «lo que lee una persona» y su cláusula de reenvío.
		"Channels that reach a person",
		"The exemption never travels with the text",
	}
	for _, kw := range keywords {
		if !strings.Contains(content, kw) {
			t.Errorf("operating-manual-codex.md: expected keyword %q not found", kw)
		}
	}

	// SPEC-106 DD19/DD20: the manual must say there is no automatic net for
	// session-end (mneme hook session-end is a retired no-op), and must no
	// longer claim the Stop event as part of the session hooks pair.
	if !strings.Contains(content, "no hook that reminds you") {
		t.Error("operating-manual-codex.md: expected the no-automatic-net phrase for session end not found")
	}
	if strings.Contains(content, "SessionStart/Stop") {
		t.Error("operating-manual-codex.md: must not contain \"SessionStart/Stop\" — Stop is retired (SPEC-106 D4)")
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
