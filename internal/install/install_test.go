package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/subagents"
)

// TestClaudeCode_MCPConfig verifies that the MCP config function returns the
// correct target path (~/claude.json) and a valid server entry JSON with the
// expected command and args fields.
func TestClaudeCode_MCPConfig(t *testing.T) {
	agent := ClaudeCode("/usr/local/bin/mneme")

	path, content, err := agent.MCPConfig("/usr/local/bin/mneme")
	if err != nil {
		t.Fatalf("MCPConfig returned error: %v", err)
	}

	if path == "" {
		t.Error("MCPConfig path must not be empty")
	}
	// Claude Code reads User MCPs from ~/.claude.json, not from a per-server file.
	if !strings.HasSuffix(path, ".claude.json") {
		t.Errorf("MCPConfig path should end with .claude.json, got %q", path)
	}

	// content is the individual server entry (command + args), not the full file.
	var entry map[string]any
	if err := json.Unmarshal(content, &entry); err != nil {
		t.Fatalf("MCPConfig content is not valid JSON: %v", err)
	}

	if cmd, ok := entry["command"].(string); !ok || cmd != "/usr/local/bin/mneme" {
		t.Errorf("MCPConfig entry command = %v, want /usr/local/bin/mneme", entry["command"])
	}

	args, ok := entry["args"].([]any)
	if !ok || len(args) < 2 {
		t.Fatalf("MCPConfig entry args missing or too short: %v", entry["args"])
	}
	if args[0] != "mcp" {
		t.Errorf("MCPConfig entry args[0] = %v, want mcp", args[0])
	}
	if args[1] != "--tools=agent" {
		t.Errorf("MCPConfig entry args[1] = %v, want --tools=agent", args[1])
	}
}

// TestWriteMCPConfig_NewFile verifies that WriteMCPConfig creates ~/.claude.json
// from scratch with the correct mcpServers.mneme entry when the file is absent.
func TestWriteMCPConfig_NewFile(t *testing.T) {
	dir := t.TempDir()
	claudeJSON := filepath.Join(dir, ".claude.json")

	if err := writeMCPConfigFile(claudeJSON, "/usr/local/bin/mneme"); err != nil {
		t.Fatalf("writeMCPConfigFile error: %v", err)
	}

	assertClaudeJSONEntry(t, claudeJSON, "/usr/local/bin/mneme")
}

// TestWriteMCPConfig_ExistingFile verifies that WriteMCPConfig merges into an
// existing ~/.claude.json without clobbering other top-level keys.
func TestWriteMCPConfig_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	claudeJSON := filepath.Join(dir, ".claude.json")

	existing := `{
  "theme": "dark",
  "mcpServers": {
    "other-tool": {
      "command": "/usr/bin/other",
      "args": ["serve"]
    }
  }
}`
	if err := os.WriteFile(claudeJSON, []byte(existing), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}

	if err := writeMCPConfigFile(claudeJSON, "/usr/local/bin/mneme"); err != nil {
		t.Fatalf("writeMCPConfigFile error: %v", err)
	}

	data, err := os.ReadFile(claudeJSON)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}

	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Existing top-level key must be preserved.
	if root["theme"] != "dark" {
		t.Errorf("theme = %v, want dark", root["theme"])
	}

	mcpServers, ok := root["mcpServers"].(map[string]any)
	if !ok {
		t.Fatal("mcpServers is not an object")
	}

	// Pre-existing server must still be there.
	if _, exists := mcpServers["other-tool"]; !exists {
		t.Error("other-tool server entry was removed")
	}

	// mneme entry must now be present.
	assertClaudeJSONEntry(t, claudeJSON, "/usr/local/bin/mneme")
}

// TestWriteMCPConfig_Idempotent verifies that running WriteMCPConfig twice
// produces the same file with no duplicate entries.
func TestWriteMCPConfig_Idempotent(t *testing.T) {
	dir := t.TempDir()
	claudeJSON := filepath.Join(dir, ".claude.json")

	if err := writeMCPConfigFile(claudeJSON, "/usr/local/bin/mneme"); err != nil {
		t.Fatalf("first writeMCPConfigFile error: %v", err)
	}
	if err := writeMCPConfigFile(claudeJSON, "/usr/local/bin/mneme"); err != nil {
		t.Fatalf("second writeMCPConfigFile error: %v", err)
	}

	data, err := os.ReadFile(claudeJSON)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}

	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("invalid JSON after idempotent run: %v", err)
	}

	mcpServers, ok := root["mcpServers"].(map[string]any)
	if !ok {
		t.Fatal("mcpServers is not an object")
	}

	// "mneme" must appear exactly once (as a single map key, not duplicated).
	count := 0
	for k := range mcpServers {
		if k == "mneme" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("mcpServers contains mneme %d time(s), want 1", count)
	}
}

// TestClaudeCode_Manual verifies that the Manual function returns a valid path
// and content that covers all critical sections of the operating manual.
func TestClaudeCode_Manual(t *testing.T) {
	agent := ClaudeCode("")

	path, content, err := agent.Manual()
	if err != nil {
		t.Fatalf("Manual returned error: %v", err)
	}

	if path == "" {
		t.Error("Manual path must not be empty")
	}
	if !strings.HasSuffix(path, "CLAUDE.md") {
		t.Errorf("Manual path should end with CLAUDE.md, got %q", path)
	}

	manual := string(content)

	requiredSections := []string{
		"# mneme Operating Manual",
		"mem_context",
		"mem_save",
		"mem_search",
		"mem_session_end",
		"mem_checkpoint",
		"SDD",
		"trivial",
		"standard",
	}
	for _, section := range requiredSections {
		if !strings.Contains(manual, section) {
			t.Errorf("Manual missing required section/keyword: %q", section)
		}
	}
}

// TestInjectManual_NewFile verifies that InjectManual creates the target file
// with the managed block when the file does not yet exist.
func TestInjectManual_NewFile(t *testing.T) {
	dir := t.TempDir()

	agent := &Agent{
		Manual: func() (string, []byte, error) {
			return filepath.Join(dir, "CLAUDE.md"), []byte("# Manual content"), nil
		},
	}

	if err := InjectManual(agent); err != nil {
		t.Fatalf("InjectManual error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	text := string(data)

	if !strings.Contains(text, "<!-- mneme:managed:start") {
		t.Error("managed start marker missing")
	}
	if !strings.Contains(text, "<!-- mneme:managed:end -->") {
		t.Error("managed end marker missing")
	}
	if !strings.Contains(text, "Manual content") {
		t.Error("manual content missing")
	}
}

// TestInjectManual_Nil verifies that InjectManual is a no-op when Manual is nil.
func TestInjectManual_Nil(t *testing.T) {
	agent := &Agent{Manual: nil}
	if err := InjectManual(agent); err != nil {
		t.Fatalf("InjectManual with nil Manual returned error: %v", err)
	}
}

// TestClaudeCode_Commands_MnemeInitWrapper verifies that Claude Code ships
// /mneme-init as a thin wrapper that only invokes the mneme-init SKILL
// (SPEC-058 / EPIC agnostic-agents SS-5; wrapper restored by SPEC-067). The
// wrapper must reference the skill and must NOT reintroduce the obsolete
// 5-phase markdown workflow. Since SPEC-141 D7, Commands() installs every
// assets/commands/ file (mneme-init, grill-me, hunt-bug, bug-to-issue) — see
// TestCommandAssets_AllInstalledByClaudeCode (G2) for that population-level
// invariant; this test stays focused on mneme-init's own content.
func TestClaudeCode_Commands_MnemeInitWrapper(t *testing.T) {
	agent := ClaudeCode("")

	if agent.Commands == nil {
		t.Fatal("ClaudeCode agent.Commands must not be nil — /mneme-init is restored as a skill wrapper (SPEC-067)")
	}

	files, err := agent.Commands()
	if err != nil {
		t.Fatalf("agent.Commands() returned error: %v", err)
	}

	var f *CommandFile
	for i := range files {
		if strings.HasSuffix(filepath.ToSlash(files[i].Path), "/.claude/commands/mneme-init.md") {
			f = &files[i]
			break
		}
	}
	if f == nil {
		t.Fatalf("expected a mneme-init.md CommandFile among %d, found none", len(files))
	}

	content := string(f.Content)
	if !strings.Contains(content, "mneme-init") || !strings.Contains(content, "skill") {
		t.Errorf("wrapper content does not reference the mneme-init skill: %q", content)
	}

	// Anti-drift: must not contain markers of the obsolete 5-phase workflow.
	forbidden := []string{"subagent_fingerprint", "Phase 0", "Phase 1"}
	for _, marker := range forbidden {
		if strings.Contains(content, marker) {
			t.Errorf("wrapper content contains obsolete 5-phase marker %q", marker)
		}
	}
}

// TestPatchSettings_Empty verifies that patching an empty (non-existing)
// settings.json creates a valid JSON file with the expected hook entries.
func TestPatchSettings_Empty(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	patches := []HookPatch{
		{Event: "SessionStart", Command: "mneme hook session-start"},
		{Event: "Stop", Command: "mneme hook session-end"},
	}

	if err := patchSettingsFile(settingsPath, patches); err != nil {
		t.Fatalf("patchSettingsFile error: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("settings.json is not valid JSON: %v", err)
	}

	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		t.Fatal("settings.hooks is not an object")
	}

	assertHookEntry(t, hooks, "SessionStart", "mneme hook session-start")
	assertHookEntry(t, hooks, "Stop", "mneme hook session-end")
}

// TestPatchSettings_Existing verifies that patching a settings.json that
// already has hooks does not clobber the existing entries.
func TestPatchSettings_Existing(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	// Write initial settings with an existing hook (correct Claude Code format).
	existing := `{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "",
        "hooks": [
          {"type": "command", "command": "existing-hook"}
        ]
      }
    ]
  },
  "theme": "dark"
}`
	if err := os.WriteFile(settingsPath, []byte(existing), 0o644); err != nil {
		t.Fatalf("write initial settings: %v", err)
	}

	patches := []HookPatch{
		{Event: "SessionStart", Command: "mneme hook session-start"},
		{Event: "Stop", Command: "mneme hook session-end"},
	}

	if err := patchSettingsFile(settingsPath, patches); err != nil {
		t.Fatalf("patchSettingsFile error: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("settings.json is not valid JSON: %v", err)
	}

	// Existing top-level key must be preserved.
	if settings["theme"] != "dark" {
		t.Errorf("settings.theme = %v, want dark", settings["theme"])
	}

	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		t.Fatal("settings.hooks is not an object")
	}

	// Both the existing and new hooks must be present.
	assertHookEntry(t, hooks, "SessionStart", "existing-hook")
	assertHookEntry(t, hooks, "SessionStart", "mneme hook session-start")
	assertHookEntry(t, hooks, "Stop", "mneme hook session-end")
}

// TestPatchSettings_Idempotent verifies that patching the same settings file
// twice does not produce duplicate hook entries.
func TestPatchSettings_Idempotent(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	patches := []HookPatch{
		{Event: "SessionStart", Command: "mneme hook session-start"},
		{Event: "Stop", Command: "mneme hook session-end"},
	}

	// Patch twice.
	if err := patchSettingsFile(settingsPath, patches); err != nil {
		t.Fatalf("first patchSettingsFile error: %v", err)
	}
	if err := patchSettingsFile(settingsPath, patches); err != nil {
		t.Fatalf("second patchSettingsFile error: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("settings.json is not valid JSON: %v", err)
	}

	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		t.Fatal("settings.hooks is not an object")
	}

	// Each command must appear exactly once.
	assertHookCount(t, hooks, "SessionStart", "mneme hook session-start", 1)
	assertHookCount(t, hooks, "Stop", "mneme hook session-end", 1)
}

// TestInjectManual_PreservesUserProsa verifies that InjectManual appends the
// managed block to an existing file without clobbering user content.
// This is the regression test for the non-destructive injection property
// (previously covered by the now-replaced InjectProtocol tests).
func TestInjectManual_PreservesUserProsa(t *testing.T) {
	dir := t.TempDir()

	importantContent := "# Claude Code — Global Configuration\n\n" +
		"## Language\nAlways respond in Español.\n\n" +
		"## Custom Rules\nNever do X.\nAlways do Y.\n"

	target := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(target, []byte(importantContent), 0o644); err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	agent := &Agent{
		Manual: func() (string, []byte, error) {
			return target, []byte("manual content"), nil
		},
	}

	if err := InjectManual(agent); err != nil {
		t.Fatalf("InjectManual error: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	content := string(data)

	// Every line of the original user config must survive intact.
	userLines := []string{
		"# Claude Code — Global Configuration",
		"Always respond in Español.",
		"Never do X.",
		"Always do Y.",
	}
	for _, line := range userLines {
		if !strings.Contains(content, line) {
			t.Errorf("user content lost after InjectManual: %q", line)
		}
	}

	// The manual block must have been appended.
	if !strings.Contains(content, "<!-- mneme:managed:start") {
		t.Error("managed start marker missing after InjectManual")
	}
	if !strings.Contains(content, "manual content") {
		t.Error("manual content missing after InjectManual")
	}
	if !strings.Contains(content, managedBlockEnd) {
		t.Error("managed end marker missing after InjectManual")
	}

	// Running InjectManual a second time must not duplicate the block.
	if err := InjectManual(agent); err != nil {
		t.Fatalf("second InjectManual error: %v", err)
	}
	data2, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target after second InjectManual: %v", err)
	}
	count := strings.Count(string(data2), "<!-- mneme:managed:start")
	if count != 1 {
		t.Errorf("managed start marker appears %d times after second InjectManual, want 1", count)
	}
}

// --- helpers -----------------------------------------------------------------

// patchSettingsFile is the testable core of PatchHooks. It accepts an explicit
// file path so tests can use a temporary directory instead of the real home dir.
func patchSettingsFile(path string, patches []HookPatch) error {
	settings := map[string]any{}

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &settings); err != nil {
			return err
		}
	}

	hooksRaw, ok := settings["hooks"]
	if !ok || hooksRaw == nil {
		hooksRaw = map[string]any{}
	}
	hooks, ok := hooksRaw.(map[string]any)
	if !ok {
		return nil
	}

	for _, patch := range patches {
		cmd := map[string]any{
			"type":    "command",
			"command": patch.Command,
		}
		var eventList []any
		if raw, exists := hooks[patch.Event]; exists && raw != nil {
			if list, ok := raw.([]any); ok {
				eventList = list
			}
		}
		if !hookCommandExists(eventList, patch.Command) {
			group := map[string]any{
				"matcher": "",
				"hooks":   []any{cmd},
			}
			eventList = append(eventList, group)
		}
		hooks[patch.Event] = eventList
	}

	settings["hooks"] = hooks

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

// writeMCPConfigFile is the testable core of WriteMCPConfig. It accepts an
// explicit path so tests can use a temporary directory instead of ~/.claude.json.
func writeMCPConfigFile(path, binaryPath string) error {
	entry := map[string]any{
		"command": binaryPath,
		"args":    []string{"mcp", "--tools=agent"},
	}
	entryData, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("writeMCPConfigFile: marshal entry: %w", err)
	}

	root := map[string]any{}
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("writeMCPConfigFile: read %s: %w", path, err)
	}
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &root); err != nil {
			return fmt.Errorf("writeMCPConfigFile: parse %s: %w", path, err)
		}
	}

	mcpRaw, ok := root["mcpServers"]
	if !ok || mcpRaw == nil {
		mcpRaw = map[string]any{}
	}
	mcpServers, ok := mcpRaw.(map[string]any)
	if !ok {
		return fmt.Errorf("writeMCPConfigFile: mcpServers is not an object")
	}

	var decodedEntry map[string]any
	if err := json.Unmarshal(entryData, &decodedEntry); err != nil {
		return fmt.Errorf("writeMCPConfigFile: decode entry: %w", err)
	}
	mcpServers["mneme"] = decodedEntry
	root["mcpServers"] = mcpServers

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("writeMCPConfigFile: marshal: %w", err)
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

// assertClaudeJSONEntry asserts that the file at path is valid JSON with a
// mcpServers.mneme entry containing the expected binary path as "command".
func assertClaudeJSONEntry(t *testing.T, path, expectedBinary string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("assertClaudeJSONEntry: read %s: %v", path, err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("assertClaudeJSONEntry: invalid JSON in %s: %v", path, err)
	}
	mcpServers, ok := root["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("assertClaudeJSONEntry: mcpServers is missing or not an object in %s", path)
	}
	mneme, ok := mcpServers["mneme"].(map[string]any)
	if !ok {
		t.Fatalf("assertClaudeJSONEntry: mcpServers.mneme is missing or not an object in %s", path)
	}
	if cmd, ok := mneme["command"].(string); !ok || cmd != expectedBinary {
		t.Errorf("assertClaudeJSONEntry: mneme.command = %v, want %s", mneme["command"], expectedBinary)
	}
	args, ok := mneme["args"].([]any)
	if !ok || len(args) < 2 || args[0] != "mcp" || args[1] != "--tools=agent" {
		t.Errorf("assertClaudeJSONEntry: mneme.args = %v, want [mcp --tools=agent]", mneme["args"])
	}
}

// assertHookEntry asserts that hooks[event] contains at least one matcher-group
// whose inner "hooks" array has an entry with the given command.
func assertHookEntry(t *testing.T, hooks map[string]any, event, command string) {
	t.Helper()
	raw, ok := hooks[event]
	if !ok {
		t.Errorf("hooks[%q] is missing", event)
		return
	}
	list, ok := raw.([]any)
	if !ok {
		t.Errorf("hooks[%q] is not a slice", event)
		return
	}
	if !hookCommandExists(list, command) {
		t.Errorf("hooks[%q] does not contain command %q", event, command)
	}
}

// TestClaudeCode_DelegationHook_RegistersPreToolUse verifies that a fresh install
// registers "mneme hook pre-tool-use" (not the legacy enforce-delegation) as the
// PreToolUse hook.
func TestClaudeCode_DelegationHook_RegistersPreToolUse(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	agent := ClaudeCode("/usr/local/bin/mneme")

	// Patch directly via DelegationHook.
	hookPath, patches, err := agent.DelegationHook()
	if err != nil {
		t.Fatalf("DelegationHook: %v", err)
	}
	// Use a temp path instead of the real home dir.
	_ = hookPath

	if err := patchSettingsFile(settingsPath, patches); err != nil {
		t.Fatalf("patchSettingsFile: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	hooks := settings["hooks"].(map[string]any)

	// Must register pre-tool-use, not the legacy enforce-delegation.
	assertHookEntry(t, hooks, "PreToolUse", "mneme hook pre-tool-use")
}

// TestPatchHooks_CustomisedPathNotDuplicated covers AC13 (SPEC-107): a
// settings.json whose SessionStart entry was hand-customised to an absolute
// path is recognised by PatchHooks as the same registration (identity, not
// literal string) — the personalised entry survives untouched, exactly one
// entry of that identity remains, and the file is byte-identical to what was
// there before (no spurious rewrite).
func TestPatchHooks_CustomisedPathNotDuplicated(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	const customised = "/Users/x/.local/bin/mneme hook session-start"

	seed := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{
					"matcher": "",
					"hooks": []any{
						map[string]any{"type": "command", "command": customised},
					},
				},
			},
		},
	}
	data, err := json.MarshalIndent(seed, "", "  ")
	if err != nil {
		t.Fatalf("marshal seed: %v", err)
	}
	if err := os.WriteFile(settingsPath, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write initial settings: %v", err)
	}

	before, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read before: %v", err)
	}

	agent := &Agent{
		Hooks: func() (string, []HookPatch, error) {
			return settingsPath, []HookPatch{
				{Event: "SessionStart", Command: "mneme hook session-start"},
			}, nil
		},
	}
	if err := PatchHooks(agent); err != nil {
		t.Fatalf("PatchHooks: %v", err)
	}

	after, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}

	var settings map[string]any
	if err := json.Unmarshal(after, &settings); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	hooks := settings["hooks"].(map[string]any)

	assertHookEntry(t, hooks, "SessionStart", customised)
	assertHookCount(t, hooks, "SessionStart", customised, 1)

	if string(before) != string(after) {
		t.Errorf("PatchHooks rewrote the file despite the entry already being present under a different path:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestReinstallHooks_ReplacesLegacy verifies that ReinstallHooks replaces the
// existing PreToolUse entries (e.g. enforce-delegation) with the new command.
func TestReinstallHooks_ReplacesLegacy(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	// Start with a settings file that has the legacy hook.
	existing := `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "mneme hook enforce-delegation"}]
      }
    ]
  }
}`
	if err := os.WriteFile(settingsPath, []byte(existing), 0o644); err != nil {
		t.Fatalf("write initial settings: %v", err)
	}

	patches := []HookPatch{
		{Event: "PreToolUse", Command: "mneme hook pre-tool-use"},
	}

	if err := ReinstallHooks(settingsPath, patches); err != nil {
		t.Fatalf("ReinstallHooks: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	hooks := settings["hooks"].(map[string]any)

	// Legacy hook must be gone.
	assertHookCount(t, hooks, "PreToolUse", "mneme hook enforce-delegation", 0)
	// New hook must be present.
	assertHookEntry(t, hooks, "PreToolUse", "mneme hook pre-tool-use")
}

// TestReinstallHooks_PreservesOtherEvents verifies that ReinstallHooks leaves
// unaffected hook events (SessionStart, Stop) intact.
func TestReinstallHooks_PreservesOtherEvents(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	existing := `{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "mneme hook session-start"}]
      }
    ],
    "PreToolUse": [
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "mneme hook enforce-delegation"}]
      }
    ]
  }
}`
	if err := os.WriteFile(settingsPath, []byte(existing), 0o644); err != nil {
		t.Fatalf("write initial settings: %v", err)
	}

	patches := []HookPatch{
		{Event: "PreToolUse", Command: "mneme hook pre-tool-use"},
	}

	if err := ReinstallHooks(settingsPath, patches); err != nil {
		t.Fatalf("ReinstallHooks: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	hooks := settings["hooks"].(map[string]any)

	// SessionStart must be untouched.
	assertHookEntry(t, hooks, "SessionStart", "mneme hook session-start")
	// PreToolUse must have only the new hook.
	assertHookEntry(t, hooks, "PreToolUse", "mneme hook pre-tool-use")
	assertHookCount(t, hooks, "PreToolUse", "mneme hook enforce-delegation", 0)
}

// assertHookCount asserts that hooks[event] contains the given command exactly
// n times across all matcher-groups' inner "hooks" arrays.
func assertHookCount(t *testing.T, hooks map[string]any, event, command string, n int) {
	t.Helper()
	raw, ok := hooks[event]
	if !ok {
		if n == 0 {
			return
		}
		t.Errorf("hooks[%q] is missing, expected %d occurrences of %q", event, n, command)
		return
	}
	list, ok := raw.([]any)
	if !ok {
		t.Errorf("hooks[%q] is not a slice", event)
		return
	}
	count := 0
	for _, item := range list {
		group, ok := item.(map[string]any)
		if !ok {
			continue
		}
		innerRaw, ok := group["hooks"]
		if !ok {
			continue
		}
		inner, ok := innerRaw.([]any)
		if !ok {
			continue
		}
		for _, h := range inner {
			entry, ok := h.(map[string]any)
			if !ok {
				continue
			}
			if cmd, ok := entry["command"].(string); ok && cmd == command {
				count++
			}
		}
	}
	if count != n {
		t.Errorf("hooks[%q] contains %q %d time(s), want %d", event, command, count, n)
	}
}

// ---------------------------------------------------------------------------
// Delegation hook tests (SPEC-032)
// ---------------------------------------------------------------------------

// TestDelegationHookContent_ValidBash verifies that the embedded hook asset
// is now a thin compat shim (SPEC-069 D4/AC12): non-empty, starts with the
// expected shebang, and its only job is to exec the portable
// "mneme hook enforce-delegation" subcommand — all the decision logic that
// used to live here (is_allowed_path, agent_id detection,
// command_mentions_protected_path, ...) has moved to internal/enforcement +
// internal/cli, and is covered by their own test suites instead.
func TestDelegationHookContent_ValidBash(t *testing.T) {
	content, err := DelegationHookContent()
	if err != nil {
		t.Fatalf("DelegationHookContent returned error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("DelegationHookContent returned empty bytes")
	}
	text := string(content)
	if !strings.HasPrefix(text, "#!/usr/bin/env bash") {
		t.Errorf("hook script shebang mismatch: first line = %q", strings.SplitN(text, "\n", 2)[0])
	}
	if !strings.Contains(text, "exec mneme hook enforce-delegation") {
		t.Errorf("shim must exec \"mneme hook enforce-delegation\"; got:\n%s", text)
	}
	// The shim must be small — a handful of lines, not the ~640-line script it
	// replaces. A generous upper bound catches an accidental re-inflation of
	// the asset (e.g. a future edit pasting the old logic back in) without
	// being brittle about the exact line count.
	if lines := strings.Count(text, "\n"); lines > 15 {
		t.Errorf("shim has %d lines, expected a small compat shim (~6 lines)", lines)
	}
}

// TestWriteDelegationHook_NewFile verifies that WriteDelegationHook creates the
// hook file with executable permissions (0755) and returns action="created"
// when the destination does not yet exist.
func TestWriteDelegationHook_NewFile(t *testing.T) {
	dir := t.TempDir()

	action, err := WriteDelegationHook(dir, false)
	if err != nil {
		t.Fatalf("WriteDelegationHook error: %v", err)
	}
	if action != "created" {
		t.Errorf("action = %q, want created", action)
	}

	dest := filepath.Join(dir, "enforce_delegation.sh")
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat hook file: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("file permissions = %o, want 0755", info.Mode().Perm())
	}
}

// TestWriteDelegationHook_IdenticalSkip verifies that writing the hook twice
// (without force) returns action="unchanged" and does not create a backup file.
func TestWriteDelegationHook_IdenticalSkip(t *testing.T) {
	dir := t.TempDir()

	// First write.
	if _, err := WriteDelegationHook(dir, false); err != nil {
		t.Fatalf("first WriteDelegationHook error: %v", err)
	}

	// Second write — same content, force=false.
	action, err := WriteDelegationHook(dir, false)
	if err != nil {
		t.Fatalf("second WriteDelegationHook error: %v", err)
	}
	if action != "unchanged" {
		t.Errorf("action = %q, want unchanged", action)
	}

	// No backup file should exist.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak-") {
			t.Errorf("unexpected backup file created: %s", e.Name())
		}
	}
}

// TestWriteDelegationHook_DifferentBackup verifies that when the destination
// file has different content, a .bak-YYYYMMDD-HHMMSS backup is created and the
// file is overwritten with the asset, returning action="updated".
func TestWriteDelegationHook_DifferentBackup(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "enforce_delegation.sh")

	// Pre-populate with different content.
	if err := os.WriteFile(dest, []byte("#!/bin/bash\n# old version\n"), 0o755); err != nil {
		t.Fatalf("write initial hook: %v", err)
	}

	action, err := WriteDelegationHook(dir, false)
	if err != nil {
		t.Fatalf("WriteDelegationHook error: %v", err)
	}
	if action != "updated" {
		t.Errorf("action = %q, want updated", action)
	}

	// Exactly one .bak-* file must exist.
	entries, _ := os.ReadDir(dir)
	backups := 0
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak-") {
			backups++
		}
	}
	if backups != 1 {
		t.Errorf("expected 1 backup file, got %d", backups)
	}

	// The hook file must now contain the embedded asset.
	written, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read written hook: %v", err)
	}
	expected, _ := DelegationHookContent()
	if string(written) != string(expected) {
		t.Error("written hook content does not match the embedded asset")
	}
}

// TestWriteDelegationHook_ForceReinstall verifies that force=true causes the
// hook to be rewritten even when the content is already identical, returning
// action="reinstalled".
func TestWriteDelegationHook_ForceReinstall(t *testing.T) {
	dir := t.TempDir()

	// Write the hook once so it is already identical to the asset.
	if _, err := WriteDelegationHook(dir, false); err != nil {
		t.Fatalf("first WriteDelegationHook error: %v", err)
	}

	action, err := WriteDelegationHook(dir, true)
	if err != nil {
		t.Fatalf("force WriteDelegationHook error: %v", err)
	}
	if action != "reinstalled" {
		t.Errorf("action = %q, want reinstalled", action)
	}
}

// TestClaudeCode_DelegationHook_IncludesBashHook verifies that DelegationHook
// returns exactly 2 PreToolUse patches, both portable mneme subcommands
// (SPEC-069): "mneme hook pre-tool-use" and "mneme hook enforce-delegation".
// Neither carries a path to the home directory (AC9).
func TestClaudeCode_DelegationHook_IncludesBashHook(t *testing.T) {
	agent := ClaudeCode("/usr/local/bin/mneme")

	_, patches, err := agent.DelegationHook()
	if err != nil {
		t.Fatalf("DelegationHook error: %v", err)
	}
	if len(patches) != 2 {
		t.Fatalf("expected 2 patches, got %d", len(patches))
	}

	// Both must target PreToolUse.
	for i, p := range patches {
		if p.Event != "PreToolUse" {
			t.Errorf("patches[%d].Event = %q, want PreToolUse", i, p.Event)
		}
	}

	// First patch must be the Go hook.
	if patches[0].Command != "mneme hook pre-tool-use" {
		t.Errorf("patches[0].Command = %q, want mneme hook pre-tool-use", patches[0].Command)
	}

	// Second patch must be the portable enforce-delegation subcommand —
	// no absolute path to enforce_delegation.sh, and no home-directory path.
	if patches[1].Command != "mneme hook enforce-delegation" {
		t.Errorf("patches[1].Command = %q, want %q", patches[1].Command, "mneme hook enforce-delegation")
	}
	if strings.HasSuffix(patches[1].Command, ".sh") {
		t.Error("patches[1].Command must not be a script path")
	}
}

// TestClaudeCode_DelegationHook_NoHomePathInEitherPatch guards AC9 directly:
// neither PreToolUse command may embed a filesystem path at all (portable
// registration assumes only that "mneme" is on PATH).
func TestClaudeCode_DelegationHook_NoHomePathInEitherPatch(t *testing.T) {
	agent := ClaudeCode("/usr/local/bin/mneme")

	_, patches, err := agent.DelegationHook()
	if err != nil {
		t.Fatalf("DelegationHook error: %v", err)
	}
	for _, p := range patches {
		if strings.Contains(p.Command, "/") {
			t.Errorf("patch command %q contains a path separator — expected a portable subcommand string", p.Command)
		}
	}
}

// TestPatchDelegationHook_StripsLegacyScriptEntry covers AC10 (global): a
// settings.json with a pre-existing legacy absolute-path
// enforce_delegation.sh entry has that entry removed and the portable
// subcommand added, with no duplicates, when PatchDelegationHook runs (the
// default, non---reinstall-hooks install path — the one `mneme upgrade`
// actually exercises).
func TestPatchDelegationHook_StripsLegacyScriptEntry(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	settingsDir := filepath.Join(tmpHome, ".claude")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	settingsPath := filepath.Join(settingsDir, "settings.json")
	legacyCommand := filepath.Join(tmpHome, ".claude", "hooks", "enforce_delegation.sh")
	existing := fmt.Sprintf(`{
  "hooks": {
    "PreToolUse": [
      {"matcher": "", "hooks": [{"type": "command", "command": %q}]}
    ],
    "SessionStart": [
      {"matcher": "", "hooks": [{"type": "command", "command": "mneme hook session-start"}]}
    ]
  }
}`, legacyCommand)
	if err := os.WriteFile(settingsPath, []byte(existing), 0o644); err != nil {
		t.Fatalf("write initial settings: %v", err)
	}

	agent := ClaudeCode("/usr/local/bin/mneme")
	if err := PatchDelegationHook(agent); err != nil {
		t.Fatalf("PatchDelegationHook: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	hooks := settings["hooks"].(map[string]any)

	assertHookCount(t, hooks, "PreToolUse", legacyCommand, 0)
	assertHookCount(t, hooks, "PreToolUse", "mneme hook enforce-delegation", 1)
	assertHookCount(t, hooks, "PreToolUse", "mneme hook pre-tool-use", 1)
	// Untouched event.
	assertHookEntry(t, hooks, "SessionStart", "mneme hook session-start")
}

// TestPatchDelegationHook_NoLegacyEntry_IsNoOpBeyondAppend verifies that
// stripLegacyDelegationHookEntries is a no-op (no spurious write/error) when
// there is nothing legacy to strip — PatchDelegationHook must still succeed
// and register the portable entries normally.
func TestPatchDelegationHook_NoLegacyEntry_IsNoOpBeyondAppend(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	agent := ClaudeCode("/usr/local/bin/mneme")
	if err := PatchDelegationHook(agent); err != nil {
		t.Fatalf("PatchDelegationHook: %v", err)
	}

	settingsPath := filepath.Join(tmpHome, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	hooks := settings["hooks"].(map[string]any)

	assertHookCount(t, hooks, "PreToolUse", "mneme hook enforce-delegation", 1)
	assertHookCount(t, hooks, "PreToolUse", "mneme hook pre-tool-use", 1)
}

// TestIsLegacyDelegationScriptCommand covers R6: only commands ending in
// "enforce_delegation.sh" match, regardless of the home directory that wrote
// them; portable subcommands and unrelated hooks never match.
func TestIsLegacyDelegationScriptCommand(t *testing.T) {
	tests := []struct {
		command string
		want    bool
	}{
		{"/Users/alice/.claude/hooks/enforce_delegation.sh", true},
		{"/home/bob/.claude/hooks/enforce_delegation.sh", true},
		{"mneme hook enforce-delegation", false},
		{"mneme hook pre-tool-use", false},
		{"some-other-hook.sh", false},
	}
	for _, tt := range tests {
		if got := isLegacyDelegationScriptCommand(tt.command); got != tt.want {
			t.Errorf("isLegacyDelegationScriptCommand(%q) = %v, want %v", tt.command, got, tt.want)
		}
	}
}

// TestReinstallHooks_LeavesOnlyPortableSubcommands covers AC11: after
// --reinstall-hooks, PreToolUse contains exactly the two subcommand entries
// and no leftover ".sh" path, even when a legacy entry pre-existed.
func TestReinstallHooks_LeavesOnlyPortableSubcommands(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	existing := `{
  "hooks": {
    "PreToolUse": [
      {"matcher": "", "hooks": [{"type": "command", "command": "/Users/alice/.claude/hooks/enforce_delegation.sh"}]}
    ]
  }
}`
	if err := os.WriteFile(settingsPath, []byte(existing), 0o644); err != nil {
		t.Fatalf("write initial settings: %v", err)
	}

	patches := []HookPatch{
		{Event: "PreToolUse", Command: "mneme hook pre-tool-use"},
		{Event: "PreToolUse", Command: "mneme hook enforce-delegation"},
	}
	if err := ReinstallHooks(settingsPath, patches); err != nil {
		t.Fatalf("ReinstallHooks: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	hooks := settings["hooks"].(map[string]any)

	assertHookCount(t, hooks, "PreToolUse", "/Users/alice/.claude/hooks/enforce_delegation.sh", 0)
	assertHookCount(t, hooks, "PreToolUse", "mneme hook pre-tool-use", 1)
	assertHookCount(t, hooks, "PreToolUse", "mneme hook enforce-delegation", 1)

	eventList := hooks["PreToolUse"].([]any)
	for _, item := range eventList {
		group := item.(map[string]any)
		inner := group["hooks"].([]any)
		for _, h := range inner {
			entry := h.(map[string]any)
			cmd, _ := entry["command"].(string)
			if strings.HasSuffix(cmd, ".sh") {
				t.Errorf("unexpected .sh command left in PreToolUse: %q", cmd)
			}
		}
	}
}

// TestPatchSettings_DelegationBashIdempotent verifies that applying the
// delegation hook patches twice does not duplicate the bash hook entry.
func TestPatchSettings_DelegationBashIdempotent(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	patches := []HookPatch{
		{Event: "PreToolUse", Command: "mneme hook pre-tool-use"},
		{Event: "PreToolUse", Command: "/home/user/.claude/hooks/enforce_delegation.sh"},
	}

	// Apply twice.
	if err := patchSettingsFile(settingsPath, patches); err != nil {
		t.Fatalf("first patchSettingsFile error: %v", err)
	}
	if err := patchSettingsFile(settingsPath, patches); err != nil {
		t.Fatalf("second patchSettingsFile error: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	hooks := settings["hooks"].(map[string]any)

	// Each command must appear exactly once.
	assertHookCount(t, hooks, "PreToolUse", "mneme hook pre-tool-use", 1)
	assertHookCount(t, hooks, "PreToolUse", "/home/user/.claude/hooks/enforce_delegation.sh", 1)
}

// ---------------------------------------------------------------------------
// SPEC-034: permission enforcement by capability
// ---------------------------------------------------------------------------

// TestDelegationHookContent_LogsBlockedAttempts previously verified that the
// embedded bash script contained the discovery-memory logging block
// (mneme save --type discovery ...). SPEC-069 ports that logging to
// logBlockedEditDiscovery in internal/cli (see
// TestLogBlockedEditDiscovery_SuccessfulSave_ReceivesExpectedRequest and
// TestLogBlockedEditDiscovery_SaveFailure_WritesWarningOnly in
// internal/cli/hook_enforce_delegation_test.go), so this asset no longer
// carries that logic. This test now guards the inverse: the shim must NOT
// contain any of the old logging markers, i.e. the port is complete and
// nothing was left duplicated in both places.
func TestDelegationHookContent_LogsBlockedAttempts(t *testing.T) {
	content, err := DelegationHookContent()
	if err != nil {
		t.Fatalf("DelegationHookContent returned error: %v", err)
	}
	text := string(content)

	staleMarkers := []string{
		"mneme save",
		"--type discovery",
		"Blocked edit: principal",
	}
	for _, m := range staleMarkers {
		if strings.Contains(text, m) {
			t.Errorf("shim should not contain the old bash logging marker %q — logic has moved to internal/cli", m)
		}
	}
}

// TestAgentAssets_ReadOnlyAllowlists verifies that architect and qa-tester
// never carry an edit tool (Edit/Write/MultiEdit), regardless of the rest of
// their allowlist. architect additionally carries no permissionMode at all
// (a pure read-only role); qa-tester carries permissionMode: bypassPermissions
// plus Bash since SPEC-087 D2/D2b — Bash lets it run its own gates
// unattended, bypassPermissions removes the per-call prompt that would
// otherwise block that, and the capability barrier stays the tools:
// allowlist (no edit tool) rather than this mode. See
// internal/subagents.TestPermissionTable_MatchesAgentAssets for the
// byte-for-byte pin against subagents.PermissionTable.
func TestAgentAssets_ReadOnlyAllowlists(t *testing.T) {
	wantArchitectTools := "tools: Read, Grep, Glob, NotebookRead, BashOutput, WebSearch, WebFetch, mcp__mneme__*"
	wantQATesterTools := "tools: Read, Grep, Glob, NotebookRead, BashOutput, Bash, WebSearch, WebFetch, mcp__chrome-live__*, mcp__plugin_chrome-devtools-mcp_chrome-devtools__*, mcp__plugin_playwright_playwright__*, mcp__mneme__*"
	editTools := []string{"Edit", "Write", "MultiEdit"}

	destDir := t.TempDir()
	files, err := filesFromEmbed(builtinAgents, "assets/agents", destDir)
	if err != nil {
		t.Fatalf("filesFromEmbed returned error: %v", err)
	}

	tests := []struct {
		name            string
		wantTools       string
		wantBypassPerms bool
	}{
		{"architect.md", wantArchitectTools, false},
		{"qa-tester.md", wantQATesterTools, true},
	}

	for _, tt := range tests {
		var found bool
		for _, f := range files {
			if filepath.Base(f.Path) != tt.name {
				continue
			}
			found = true
			text := string(f.Content)

			if !strings.Contains(text, tt.wantTools) {
				t.Errorf("%s: missing expected tools line %q", tt.name, tt.wantTools)
			}
			hasBypass := strings.Contains(text, "permissionMode: bypassPermissions")
			if hasBypass != tt.wantBypassPerms {
				t.Errorf("%s: permissionMode: bypassPermissions present=%v, want %v", tt.name, hasBypass, tt.wantBypassPerms)
			}
			// The tools: line must never include an edit tool, regardless of
			// permissionMode — that is the actual capability barrier.
			//
			// SPEC-132 trap: this MUST stay case-sensitive (strings.Contains,
			// never a case-insensitive comparison). qa-tester's tools: line
			// carries "mcp__plugin_playwright_playwright__*", which contains
			// "write" in lower case but never "Write". Making this check
			// case-insensitive would turn the browser pattern into a false
			// positive here for a reason that has nothing to do with what
			// this test actually verifies.
			for _, line := range strings.Split(text, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "tools:") {
					for _, editTool := range editTools {
						if strings.Contains(line, editTool) {
							t.Errorf("%s: tools: line must not contain %q, got %q", tt.name, editTool, line)
						}
					}
					break
				}
			}
		}
		if !found {
			t.Errorf("agent file not found in embed: %s", tt.name)
		}
	}
}

// TestAgentAssets_ImplementerAllowlists verifies that every implementer role
// has the full edit toolset and mcp__mneme__* in its tools: line, and
// (SPEC-132 D1) that the three browser-server patterns land on frontend and
// on NO other implementer.
//
// SPEC-132 Dp7: the POPULATION is derived from subagents.PermissionTable +
// subagents.IsImplementer rather than a hand-written three-file list — today
// that is still backend/frontend/bug-hunter, but a role added or removed
// from the implementer set is picked up automatically. The EXPECTATIONS
// stay literal on purpose (frontend's full tools: line, wantFrontendTools):
// TestPermissionTable_MatchesAgentAssets compares PermissionTable against
// the asset, so it cannot catch the two drifting together — losing the
// browser block from BOTH at once. Only a hand-copied, independent literal
// catches that (the same reasoning wantQATesterTools already relies on).
func TestAgentAssets_ImplementerAllowlists(t *testing.T) {
	requiredTools := []string{"Edit", "Write", "MultiEdit", "Bash", "mcp__mneme__*"}
	visualPatterns := []string{
		"mcp__chrome-live__*",
		"mcp__plugin_chrome-devtools-mcp_chrome-devtools__*",
		"mcp__plugin_playwright_playwright__*",
	}
	wantFrontendTools := "tools: Read, Grep, Glob, NotebookRead, NotebookEdit, BashOutput, Edit, Write, MultiEdit, Bash, WebSearch, WebFetch, mcp__chrome-live__*, mcp__plugin_chrome-devtools-mcp_chrome-devtools__*, mcp__plugin_playwright_playwright__*, mcp__mneme__*"

	var implementerAgents []string
	for role := range subagents.PermissionTable {
		if subagents.IsImplementer(role) {
			implementerAgents = append(implementerAgents, string(role)+".md")
		}
	}

	destDir := t.TempDir()
	files, err := filesFromEmbed(builtinAgents, "assets/agents", destDir)
	if err != nil {
		t.Fatalf("filesFromEmbed returned error: %v", err)
	}

	for _, name := range implementerAgents {
		var found bool
		for _, f := range files {
			if filepath.Base(f.Path) != name {
				continue
			}
			found = true
			text := string(f.Content)

			// Extract the tools: line from the YAML frontmatter.
			var toolsLine string
			for _, line := range strings.Split(text, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "tools:") {
					toolsLine = line
					break
				}
			}
			if toolsLine == "" {
				t.Errorf("%s: missing tools: line in frontmatter", name)
				continue
			}
			for _, tool := range requiredTools {
				if !strings.Contains(toolsLine, tool) {
					t.Errorf("%s: tools: line missing %q, got %q", name, tool, toolsLine)
				}
			}

			isFrontend := name == "frontend.md"
			for _, pattern := range visualPatterns {
				has := strings.Contains(toolsLine, pattern)
				switch {
				case isFrontend && !has:
					t.Errorf("%s: tools: line missing browser pattern %q, got %q", name, pattern, toolsLine)
				case !isFrontend && has:
					t.Errorf("%s: tools: line must not contain browser pattern %q, got %q", name, pattern, toolsLine)
				}
			}

			if isFrontend && !strings.Contains(text, wantFrontendTools) {
				t.Errorf("frontend.md: missing expected tools line %q", wantFrontendTools)
			}
		}
		if !found {
			t.Errorf("agent file not found in embed: %s", name)
		}
	}
}

// --- SPEC-038 parity tests ---

// TestInstallSteps_DefaultSequence verifies that installSteps with default
// options returns the expected ordered step names for Claude Code post
// SPEC-073: no "Agent profiles" or "Agent models" (Agents is nil, AgentsDir
// is empty), but "Remove legacy global agents" is present (LegacyAgentsCleanupDir
// is set).
func TestInstallSteps_DefaultSequence(t *testing.T) {
	agent := ClaudeCode("/usr/local/bin/mneme")
	opts := InstallOptions{BinaryPath: "/usr/local/bin/mneme"}
	steps := agent.installSteps(opts)

	var names []string
	for _, s := range steps {
		names = append(names, s.Name)
	}

	forbidden := []string{"Agent profiles", "Agent models"}
	for _, f := range forbidden {
		for _, n := range names {
			if n == f {
				t.Errorf("step %q must not be present for Claude Code — SPEC-073 dropped global agent profiles", f)
			}
		}
	}

	// "Slash commands" is required again — /mneme-init is restored as a thin
	// wrapper around the mneme-init SKILL (SPEC-058 / EPIC agnostic-agents
	// SS-5 dropped it; SPEC-067 restored it), so Commands is no longer nil.
	required := []string{"MCP server", "Session hooks", "Retire stale hooks", "Operating manual", "Slash commands", "Remove legacy global agents", "Skills", "Workflow directories"}
	for _, req := range required {
		found := false
		for _, n := range names {
			if n == req {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing required step %q", req)
		}
	}

	// "Retire stale hooks" must sit immediately after "Session hooks"
	// (SPEC-106 AC11) — this is the single authoritative sequence both
	// Install() and the CLI RunE consume.
	sessionHooksIdx, retireIdx := -1, -1
	for i, n := range names {
		if n == "Session hooks" {
			sessionHooksIdx = i
		}
		if n == "Retire stale hooks" {
			retireIdx = i
		}
	}
	if sessionHooksIdx == -1 || retireIdx != sessionHooksIdx+1 {
		t.Errorf("\"Retire stale hooks\" must be at index \"Session hooks\"+1: got sessionHooksIdx=%d retireIdx=%d in %v", sessionHooksIdx, retireIdx, names)
	}
}

// TestClaudeCode_NoStopHookRegistered verifies that ClaudeCode().Hooks()
// registers SessionStart plus UserPromptSubmit and none for the retired "Stop"
// event (SPEC-106 AC3).
func TestClaudeCode_NoStopHookRegistered(t *testing.T) {
	agent := ClaudeCode("/usr/local/bin/mneme")

	_, patches, err := agent.Hooks()
	if err != nil {
		t.Fatalf("Hooks: %v", err)
	}
	if len(patches) != 2 {
		t.Fatalf("Hooks() returned %d patches, want exactly 2: %+v", len(patches), patches)
	}
	if patches[0].Event != "SessionStart" || patches[0].Command != "mneme hook session-start" {
		t.Errorf("Hooks()[0] = %+v, want {SessionStart, mneme hook session-start}", patches[0])
	}
	if patches[1].Event != "UserPromptSubmit" || patches[1].Command != "mneme hook speech-prompt" {
		t.Errorf("Hooks()[1] = %+v, want {UserPromptSubmit, mneme hook speech-prompt}", patches[1])
	}
	for _, p := range patches {
		if p.Event == "Stop" {
			t.Errorf("Hooks() must not register a Stop patch, found: %+v", p)
		}
	}
}

// TestRetireStaleHooks_PreservesForeignStopEntries covers AC8 (SPEC-106): a
// pre-existing "Stop" registration that mixes mneme's retired command with a
// user's own script survives the purge — only the exact "mneme hook
// session-end" command is removed, never a whole matcher-group or event that
// still has other entries in it.
func TestRetireStaleHooks_PreservesForeignStopEntries(t *testing.T) {
	const binPath = "/usr/local/bin/mneme"

	t.Run("same matcher-group", func(t *testing.T) {
		tmpHome := t.TempDir()
		t.Setenv("HOME", tmpHome)

		settingsDir := filepath.Join(tmpHome, ".claude")
		if err := os.MkdirAll(settingsDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		settingsPath := filepath.Join(settingsDir, "settings.json")
		existing := `{
  "hooks": {
    "Stop": [
      {"hooks": [
        {"type": "command", "command": "mneme hook session-end"},
        {"type": "command", "command": "/home/u/my-own-stop.sh"}
      ]}
    ]
  }
}`
		if err := os.WriteFile(settingsPath, []byte(existing), 0o644); err != nil {
			t.Fatalf("write initial settings: %v", err)
		}

		if err := Install(ClaudeCode(binPath), binPath); err != nil {
			t.Fatalf("Install: %v", err)
		}

		data, err := os.ReadFile(settingsPath)
		if err != nil {
			t.Fatalf("read settings: %v", err)
		}
		var settings map[string]any
		if err := json.Unmarshal(data, &settings); err != nil {
			t.Fatalf("unmarshal settings: %v", err)
		}
		hooks, ok := settings["hooks"].(map[string]any)
		if !ok {
			t.Fatal("settings.hooks is not an object")
		}
		assertHookEntry(t, hooks, "Stop", "/home/u/my-own-stop.sh")
		assertHookCount(t, hooks, "Stop", "mneme hook session-end", 0)
	})

	t.Run("distinct matcher-groups", func(t *testing.T) {
		tmpHome := t.TempDir()
		t.Setenv("HOME", tmpHome)

		settingsDir := filepath.Join(tmpHome, ".claude")
		if err := os.MkdirAll(settingsDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		settingsPath := filepath.Join(settingsDir, "settings.json")
		existing := `{
  "hooks": {
    "Stop": [
      {"hooks": [{"type": "command", "command": "mneme hook session-end"}]},
      {"hooks": [{"type": "command", "command": "/home/u/my-own-stop.sh"}]}
    ]
  }
}`
		if err := os.WriteFile(settingsPath, []byte(existing), 0o644); err != nil {
			t.Fatalf("write initial settings: %v", err)
		}

		if err := Install(ClaudeCode(binPath), binPath); err != nil {
			t.Fatalf("Install: %v", err)
		}

		data, err := os.ReadFile(settingsPath)
		if err != nil {
			t.Fatalf("read settings: %v", err)
		}
		var settings map[string]any
		if err := json.Unmarshal(data, &settings); err != nil {
			t.Fatalf("unmarshal settings: %v", err)
		}
		hooks, ok := settings["hooks"].(map[string]any)
		if !ok {
			t.Fatal("settings.hooks is not an object")
		}
		// mneme's own matcher-group is pruned entirely; the foreign one survives.
		assertHookEntry(t, hooks, "Stop", "/home/u/my-own-stop.sh")
		assertHookCount(t, hooks, "Stop", "mneme hook session-end", 0)
	})
}

// overlappingHookIdentities returns every (registered, retired) pair that
// identifies the SAME hook by SPEC-107 identity (sameHookCommand) — the set
// that must be empty for an install to never write and purge the same
// registration in the same run (SPEC-106 AC21). The comparison is
// deliberately command-level: it ignores which event either side belongs
// to, which is STRICTER than removeHookCommands (which groups by event) —
// and it must stay that way. Loosening it to be event-scoped would let a
// command that is registered under one event and retired under another
// (a plausible future mistake) slip through undetected, which is a worse
// failure mode than an occasional false positive here.
func overlappingHookIdentities(registered, retired []string) [][2]string {
	var overlaps [][2]string
	for _, r := range registered {
		for _, x := range retired {
			if sameHookCommand(r, x) {
				overlaps = append(overlaps, [2]string{r, x})
			}
		}
	}
	return overlaps
}

// TestRetiredHooksDisjointFromHooks covers AC20 (SPEC-107, re-expressing the
// AC21 SPEC-106 invariant by identity): the invariant that keeps the install
// from oscillating between runs. Comparing by literal string alone missed a
// failure mode the identity comparison now introduces: two commands that
// differ only by path (e.g. "mneme hook session-start" registered vs.
// "/Users/x/bin/mneme hook session-start" retired) are the SAME registration
// under sameHookCommand, and would pass a plain string-equality disjointness
// check while still oscillating the file between installs. The test also
// widens its universe of "registered" commands to include
// agent.DelegationHook() (not just Hooks()/HooksWriter) — precisely the
// command (enforce-delegation) with a history of path variants — so it is no
// longer possible for that command to fall outside the invariant entirely.
// This test intentionally lands AFTER Step 3 (see plan.md D-C): before
// RetiredHooks was populated for any real agent, the same assertion would
// have passed VACUOUSLY — green without checking anything.
func TestRetiredHooksDisjointFromHooks(t *testing.T) {
	cases := []struct {
		name  string
		agent *Agent
	}{
		{"ClaudeCode", ClaudeCode("/usr/local/bin/mneme")},
		{"Codex", Codex("/usr/local/bin/mneme")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registered, err := registeredHookCommands(t, tc.agent)
			if err != nil {
				t.Fatalf("registeredHookCommands: %v", err)
			}

			var retired []string
			if tc.agent.RetiredHooks != nil {
				_, patches, err := tc.agent.RetiredHooks()
				if err != nil {
					t.Fatalf("RetiredHooks: %v", err)
				}
				for _, p := range patches {
					retired = append(retired, p.Command)
				}
			}

			if len(registered) == 0 || len(retired) == 0 {
				return // nothing to overlap
			}

			if overlaps := overlappingHookIdentities(registered, retired); len(overlaps) != 0 {
				t.Errorf("%s: registered and retired hooks overlap by identity: %v — the install would write and purge the same registration in the same run", tc.name, overlaps)
			}
		})
	}
}

// TestOverlappingHookIdentities_DetectsPathVariant exists to prevent
// TestRetiredHooksDisjointFromHooks from passing VACUOUSLY (SPEC-106
// precedent: an invariant landing in the same commit as the field it guards
// can be green because the sets are genuinely disjoint, OR because the
// helper detects nothing at all — the two are indistinguishable without a
// separate proof of detection capability). This test supplies that proof: a
// synthetic *Agent (NOT ClaudeCode/Codex) whose Hooks() registers
// "mneme hook session-start" and whose RetiredHooks() retires
// "/usr/local/bin/mneme hook session-start" — the same registration under a
// different path — exercised through registeredHookCommands/RetiredHooks
// exactly like the real test, so this runs the identical code path rather
// than a parallel one.
func TestOverlappingHookIdentities_DetectsPathVariant(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	agent := &Agent{
		Hooks: func() (string, []HookPatch, error) {
			return settingsPath, []HookPatch{
				{Event: "SessionStart", Command: "mneme hook session-start"},
			}, nil
		},
		RetiredHooks: func() (string, []HookPatch, error) {
			return settingsPath, []HookPatch{
				{Event: "Stop", Command: "/usr/local/bin/mneme hook session-start"},
			}, nil
		},
	}

	registered, err := registeredHookCommands(t, agent)
	if err != nil {
		t.Fatalf("registeredHookCommands: %v", err)
	}
	_, retiredPatches, err := agent.RetiredHooks()
	if err != nil {
		t.Fatalf("RetiredHooks: %v", err)
	}
	var retired []string
	for _, p := range retiredPatches {
		retired = append(retired, p.Command)
	}

	// Positive: the helper detects exactly one overlapping pair.
	overlaps := overlappingHookIdentities(registered, retired)
	want := [2]string{"mneme hook session-start", "/usr/local/bin/mneme hook session-start"}
	if len(overlaps) != 1 || overlaps[0] != want {
		t.Fatalf("overlappingHookIdentities = %v, want exactly [%v]", overlaps, want)
	}

	// The heart of AC21: the two strings that DO match by identity are
	// byte-for-byte DIFFERENT — this is exactly what a plain string-equality
	// disjointness check would have missed, proving the blindness of the
	// criterion this test replaces.
	if overlaps[0][0] == overlaps[0][1] {
		t.Fatal("the overlapping pair's two strings are byte-identical — this test no longer demonstrates the blindness of literal-string comparison")
	}

	// Negative control: sets genuinely disjoint by subcommand ({session-start}
	// vs {session-end}) must report no overlap at all.
	if none := overlappingHookIdentities(
		[]string{"mneme hook session-start"},
		[]string{"mneme hook session-end"},
	); len(none) != 0 {
		t.Errorf("overlappingHookIdentities on disjoint subcommands = %v, want empty", none)
	}
}

// registeredHookCommands returns every hook Command an agent actively
// registers today, regardless of which mechanism it uses: Hooks() directly
// (Claude Code), HooksWriter (Codex), or DelegationHook() — precisely the
// command (enforce-delegation) with a history of path variants, and one that
// TestRetiredHooksDisjointFromHooks would otherwise never check at all —
// exercised against a scratch HOME so no real file is ever touched, then
// introspected by walking the scratch tree for any JSON file shaped like
// {"hooks": {...}} and collecting every nested "command" value.
func registeredHookCommands(t *testing.T, agent *Agent) ([]string, error) {
	t.Helper()
	var commands []string

	if agent.Hooks != nil {
		_, patches, err := agent.Hooks()
		if err != nil {
			return nil, err
		}
		for _, p := range patches {
			commands = append(commands, p.Command)
		}
	}

	if agent.DelegationHook != nil {
		_, patches, err := agent.DelegationHook()
		if err != nil {
			return nil, err
		}
		for _, p := range patches {
			commands = append(commands, p.Command)
		}
	}

	if agent.HooksWriter != nil {
		dir := t.TempDir()
		t.Setenv("HOME", dir)
		if err := agent.HooksWriter(); err != nil {
			return nil, err
		}

		walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || filepath.Ext(path) != ".json" {
				return err
			}
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			var root map[string]any
			if uerr := json.Unmarshal(data, &root); uerr != nil {
				return nil // not JSON we care about
			}
			hooks, ok := root["hooks"].(map[string]any)
			if !ok {
				return nil
			}
			for _, eventListRaw := range hooks {
				eventList, ok := eventListRaw.([]any)
				if !ok {
					continue
				}
				for _, item := range eventList {
					group, ok := item.(map[string]any)
					if !ok {
						continue
					}
					inner, ok := group["hooks"].([]any)
					if !ok {
						continue
					}
					for _, h := range inner {
						entry, ok := h.(map[string]any)
						if !ok {
							continue
						}
						if cmd, ok := entry["command"].(string); ok {
							commands = append(commands, cmd)
						}
					}
				}
			}
			return nil
		})
		if walkErr != nil {
			return nil, walkErr
		}
	}

	return commands, nil
}

// TestRetireStaleHooksStep_Detail verifies both branches of the "Retire
// stale hooks" step's detail string (SPEC-106 AC12), using a synthetic Agent
// whose RetiredHooks targets a temp settings file with a controllable
// pre-existing "Stop" registration.
func TestRetireStaleHooksStep_Detail(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	agent := &Agent{
		Name: "Synthetic",
		Slug: "synthetic",
		RetiredHooks: func() (string, []HookPatch, error) {
			return settingsPath, []HookPatch{
				{Event: "Stop", Command: "mneme hook session-end"},
			}, nil
		},
	}

	findStep := func(steps []installStep, name string) *installStep {
		for i := range steps {
			if steps[i].Name == name {
				return &steps[i]
			}
		}
		return nil
	}

	// Branch 1: nothing registered yet — detail must be "none".
	steps := agent.installSteps(InstallOptions{})
	step := findStep(steps, "Retire stale hooks")
	if step == nil {
		t.Fatal("expected \"Retire stale hooks\" step to be present")
	}
	detail, err := step.Run()
	if err != nil {
		t.Fatalf("Run() (nothing registered): %v", err)
	}
	if detail != "none" {
		t.Errorf("detail (nothing registered) = %q, want \"none\"", detail)
	}

	// Branch 2: seed the file with the stale registration, then re-run.
	seed := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"mneme hook session-end"}]}]}}`
	if err := os.WriteFile(settingsPath, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed settings file: %v", err)
	}
	steps = agent.installSteps(InstallOptions{})
	step = findStep(steps, "Retire stale hooks")
	if step == nil {
		t.Fatal("expected \"Retire stale hooks\" step to be present (2nd run)")
	}
	detail, err = step.Run()
	if err != nil {
		t.Fatalf("Run() (registered): %v", err)
	}
	if detail != "removed: mneme hook session-end" {
		t.Errorf("detail (registered) = %q, want \"removed: mneme hook session-end\"", detail)
	}
}

// TestRetireStaleHooksStep_SkippedWhenNil verifies that an Agent with
// RetiredHooks == nil never produces a "Retire stale hooks" step — the field
// is opt-in, not mandatory (SPEC-106).
func TestRetireStaleHooksStep_SkippedWhenNil(t *testing.T) {
	agent := &Agent{Name: "Synthetic", Slug: "synthetic"}
	steps := agent.installSteps(InstallOptions{})
	for _, s := range steps {
		if s.Name == "Retire stale hooks" {
			t.Error("\"Retire stale hooks\" step must not appear when RetiredHooks is nil")
		}
	}
}

// TestInstallSteps_ReinstallHooks verifies that with ReinstallHooks=true,
// the "Delegation hook (reinstall)" step appears instead of "Delegation hook".
func TestInstallSteps_ReinstallHooks(t *testing.T) {
	agent := ClaudeCode("/usr/local/bin/mneme")
	opts := InstallOptions{BinaryPath: "/usr/local/bin/mneme", ReinstallHooks: true}
	steps := agent.installSteps(opts)

	var names []string
	for _, s := range steps {
		names = append(names, s.Name)
	}

	hasReinstall := false
	hasRegular := false
	for _, n := range names {
		if n == "Delegation hook (reinstall)" {
			hasReinstall = true
		}
		if n == "Delegation hook" {
			hasRegular = true
		}
	}

	if !hasReinstall {
		t.Error("ReinstallHooks=true: expected 'Delegation hook (reinstall)' step")
	}
	if hasRegular {
		t.Error("ReinstallHooks=true: unexpected 'Delegation hook' (non-reinstall) step")
	}
}

// TestInstallSteps_Personal verifies that with Personal=true,
// the "Personal ecosystem" step is present.
func TestInstallSteps_Personal(t *testing.T) {
	agent := ClaudeCode("/usr/local/bin/mneme")
	opts := InstallOptions{BinaryPath: "/usr/local/bin/mneme", Personal: true, PersonalSource: "/tmp/dotfiles"}
	steps := agent.installSteps(opts)

	found := false
	for _, s := range steps {
		if s.Name == "Personal ecosystem" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Personal=true: expected 'Personal ecosystem' step")
	}
}

// TestInstallSteps_NoPersonalByDefault verifies that without Personal=true,
// the "Personal ecosystem" step is absent.
func TestInstallSteps_NoPersonalByDefault(t *testing.T) {
	agent := ClaudeCode("/usr/local/bin/mneme")
	opts := InstallOptions{BinaryPath: "/usr/local/bin/mneme"}
	steps := agent.installSteps(opts)

	for _, s := range steps {
		if s.Name == "Personal ecosystem" {
			t.Error("Personal=false: 'Personal ecosystem' step must not be present")
		}
	}
}

// TestApplyAgentModels_WritesModel verifies that ApplyAgentModels writes the
// effective model into each installed agent file using the surgical editor.
func TestApplyAgentModels_WritesModel(t *testing.T) {
	dir := t.TempDir()

	content := "---\nname: backend\ndescription: \"desc\"\nmodel: claude-sonnet-4-6\ntools: Read\n---\n\nBody.\n"
	agentPath := filepath.Join(dir, "backend.md")
	if err := os.WriteFile(agentPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write agent file: %v", err)
	}

	overrides := map[string]string{"backend": "haiku"}
	if err := ApplyAgentModels(dir, overrides); err != nil {
		t.Fatalf("ApplyAgentModels error: %v", err)
	}

	data, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatalf("read agent file: %v", err)
	}

	result := string(data)
	if !strings.Contains(result, "model: haiku") {
		t.Errorf("expected model: haiku in result\ngot:\n%s", result)
	}
	if strings.Contains(result, "claude-sonnet-4-6") {
		t.Errorf("old pinned model ID should be replaced")
	}
	if !strings.Contains(result, `description: "desc"`) {
		t.Error("description must be preserved after ApplyAgentModels")
	}
}

// TestApplyAgentModels_SkipsMissingFile verifies that ApplyAgentModels
// silently skips agents whose files do not exist.
func TestApplyAgentModels_SkipsMissingFile(t *testing.T) {
	dir := t.TempDir()
	if err := ApplyAgentModels(dir, nil); err != nil {
		t.Errorf("ApplyAgentModels with no files: unexpected error: %v", err)
	}
}

// TestRunInstallSteps_CollectAll verifies that runInstallSteps continues past
// errors and returns all of them (collect-all semantics).
func TestRunInstallSteps_CollectAll(t *testing.T) {
	errA := fmt.Errorf("step A failed")
	errC := fmt.Errorf("step C failed")

	steps := []installStep{
		{Name: "A", Run: func() (string, error) { return "", errA }},
		{Name: "B", Run: func() (string, error) { return "ok", nil }},
		{Name: "C", Run: func() (string, error) { return "", errC }},
	}

	var called []string
	errs := runInstallSteps(steps, func(name, detail string, err error) {
		called = append(called, name)
	})

	if len(errs) != 2 {
		t.Errorf("expected 2 errors, got %d: %v", len(errs), errs)
	}
	if len(called) != 3 {
		t.Errorf("expected all 3 steps called, got %d: %v", len(called), called)
	}
}

// TestDryRun_MatchesInstallSteps verifies that DryRun's output lists exactly
// the same step names as installSteps(opts), in the same order and count.
// This test closes the mini-C1 class: a future change to installSteps cannot
// leave DryRun out of sync because DryRun is derived from installSteps directly.
//
// Two variants of opts are exercised to cover conditional steps (ReinstallHooks
// adds the "Delegation hook (reinstall)" variant instead of "Delegation hook").
func TestDryRun_MatchesInstallSteps(t *testing.T) {
	cases := []struct {
		name string
		opts InstallOptions
	}{
		{
			name: "default_opts",
			opts: InstallOptions{BinaryPath: "/usr/local/bin/mneme"},
		},
		{
			name: "reinstall_hooks",
			opts: InstallOptions{
				BinaryPath:     "/usr/local/bin/mneme",
				ReinstallHooks: true,
			},
		},
	}

	agent := ClaudeCode("/usr/local/bin/mneme")

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Collect expected step names from installSteps.
			steps := agent.installSteps(tc.opts)
			var wantNames []string
			for _, s := range steps {
				wantNames = append(wantNames, s.Name)
			}

			// Parse the names from DryRun output.
			output, err := DryRun(agent, tc.opts)
			if err != nil {
				t.Fatalf("DryRun(%s): unexpected error: %v", tc.name, err)
			}

			var gotNames []string
			const prefix = "  [would run]  "
			for _, line := range strings.Split(output, "\n") {
				if strings.HasPrefix(line, prefix) {
					gotNames = append(gotNames, strings.TrimPrefix(line, prefix))
				}
			}

			if len(gotNames) != len(wantNames) {
				t.Errorf("DryRun(%s): step count mismatch\n  got  %d: %v\n  want %d: %v",
					tc.name, len(gotNames), gotNames, len(wantNames), wantNames)
				return
			}
			for i := range wantNames {
				if gotNames[i] != wantNames[i] {
					t.Errorf("DryRun(%s): step[%d] mismatch: got %q, want %q",
						tc.name, i, gotNames[i], wantNames[i])
				}
			}
		})
	}
}
