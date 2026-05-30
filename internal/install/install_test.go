package install

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

// TestClaudeCode_Commands verifies that the commands list contains the
// mneme-init command with non-empty content at the expected path.
func TestClaudeCode_Commands(t *testing.T) {
	agent := ClaudeCode("")

	cmds, err := agent.Commands()
	if err != nil {
		t.Fatalf("Commands returned error: %v", err)
	}
	if len(cmds) == 0 {
		t.Fatal("Commands must return at least one command file")
	}

	var found bool
	for _, cmd := range cmds {
		if strings.HasSuffix(cmd.Path, "mneme-init.md") {
			found = true
			if len(cmd.Content) == 0 {
				t.Error("mneme-init.md content must not be empty")
			}
			content := string(cmd.Content)
			if !strings.Contains(content, "mem_save") {
				t.Error("mneme-init.md should reference mem_save")
			}
			if !strings.Contains(content, "mem_context") {
				t.Error("mneme-init.md should reference mem_context")
			}
		}
	}
	if !found {
		t.Error("Commands did not return a mneme-init.md file")
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

// TestDelegationHookContent_ValidBash verifies that the embedded hook asset is
// non-empty, starts with the expected shebang, and contains the key functions
// that make the hook work: is_allowed_path and agent_id detection.
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
	// Base markers (SPEC-032) + SPEC-042 hardening markers.
	for _, marker := range []string{
		"is_allowed_path",
		"agent_id",
		// D1: multi-key subagent detection
		"session.agent_id",
		// D2: explicit jq guard
		"command -v jq",
		// D3: protected-path helper for python/node
		"command_mentions_protected_path",
	} {
		if !strings.Contains(text, marker) {
			t.Errorf("hook script is missing expected marker: %q", marker)
		}
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
// returns exactly 2 PreToolUse patches: the mneme Go hook and the bash script.
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

	// Second patch must be the bash hook path.
	if !strings.HasSuffix(patches[1].Command, "enforce_delegation.sh") {
		t.Errorf("patches[1].Command = %q, expected path ending with enforce_delegation.sh", patches[1].Command)
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

// TestDelegationHookContent_LogsBlockedAttempts verifies that the embedded hook
// script contains the mneme discovery-memory logging block and that the exit 2
// guarantee is preserved.
func TestDelegationHookContent_LogsBlockedAttempts(t *testing.T) {
	content, err := DelegationHookContent()
	if err != nil {
		t.Fatalf("DelegationHookContent returned error: %v", err)
	}
	text := string(content)

	markers := []string{
		"mneme save",
		"--type discovery",
		"Blocked edit: principal",
		"command -v mneme",
		"|| ",
		"exit 2",
	}
	for _, m := range markers {
		if !strings.Contains(text, m) {
			t.Errorf("hook script missing expected marker: %q", m)
		}
	}
}

// TestAgentAssets_ReadOnlyAllowlists verifies that architect and qa-tester have
// the correct read-only tools allowlist and do not include edit tools or
// permissionMode: bypassPermissions.
func TestAgentAssets_ReadOnlyAllowlists(t *testing.T) {
	readOnlyAgents := []string{"architect.md", "qa-tester.md"}
	wantTools := "tools: Read, Grep, Glob, NotebookRead, BashOutput, mcp__mneme__*"
	editTools := []string{"Edit", "Write", "MultiEdit"}

	destDir := t.TempDir()
	files, err := filesFromEmbed(builtinAgents, "assets/agents", destDir)
	if err != nil {
		t.Fatalf("filesFromEmbed returned error: %v", err)
	}

	for _, name := range readOnlyAgents {
		var found bool
		for _, f := range files {
			if filepath.Base(f.Path) != name {
				continue
			}
			found = true
			text := string(f.Content)

			if !strings.Contains(text, wantTools) {
				t.Errorf("%s: missing expected tools line %q", name, wantTools)
			}
			if strings.Contains(text, "permissionMode: bypassPermissions") {
				t.Errorf("%s: must not contain permissionMode: bypassPermissions", name)
			}
			// The tools: line must not include edit tools. Extract just the tools line.
			for _, line := range strings.Split(text, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "tools:") {
					for _, editTool := range editTools {
						if strings.Contains(line, editTool) {
							t.Errorf("%s: tools: line must not contain %q, got %q", name, editTool, line)
						}
					}
					break
				}
			}
		}
		if !found {
			t.Errorf("agent file not found in embed: %s", name)
		}
	}
}

// TestAgentAssets_ImplementerAllowlists verifies that backend, frontend, and
// bug-hunter have the full edit toolset and mcp__mneme__* in their tools: line.
func TestAgentAssets_ImplementerAllowlists(t *testing.T) {
	implementerAgents := []string{"backend.md", "frontend.md", "bug-hunter.md"}
	requiredTools := []string{"Edit", "Write", "MultiEdit", "Bash", "mcp__mneme__*"}

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
		}
		if !found {
			t.Errorf("agent file not found in embed: %s", name)
		}
	}
}

// --- SPEC-038 parity tests ---

// TestInstallSteps_DefaultSequence verifies that installSteps with default
// options returns the expected ordered step names, including "Agent models"
// immediately after "Agent profiles".
func TestInstallSteps_DefaultSequence(t *testing.T) {
	agent := ClaudeCode("/usr/local/bin/mneme")
	opts := InstallOptions{BinaryPath: "/usr/local/bin/mneme"}
	steps := agent.installSteps(opts)

	var names []string
	for _, s := range steps {
		names = append(names, s.Name)
	}

	agentProfilesIdx := -1
	agentModelsIdx := -1
	for i, n := range names {
		switch n {
		case "Agent profiles":
			agentProfilesIdx = i
		case "Agent models":
			agentModelsIdx = i
		}
	}

	if agentProfilesIdx == -1 {
		t.Error("missing step 'Agent profiles'")
	}
	if agentModelsIdx == -1 {
		t.Fatal("missing step 'Agent models' — required by SPEC-038")
	}
	if agentProfilesIdx != -1 && agentModelsIdx != agentProfilesIdx+1 {
		t.Errorf("'Agent models' must immediately follow 'Agent profiles'; got indices %d and %d", agentProfilesIdx, agentModelsIdx)
	}

	required := []string{"MCP server", "Session hooks", "Operating manual", "Slash commands", "Workflow directories"}
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

// ---------------------------------------------------------------------------
// SPEC-042: enforce_delegation.sh smoke tests (D6)
// ---------------------------------------------------------------------------

// hookSmokeCase describes a single PreToolUse JSON payload and its expected
// exit code when passed to enforce_delegation.sh.
type hookSmokeCase struct {
	name     string
	payload  string
	wantExit int
}

// TestDelegationHook_SmokeTests runs table-driven bash invocations of the
// embedded enforce_delegation.sh hook against synthetic PreToolUse JSON
// payloads and verifies the correct exit code for each case.
//
// The test is skipped (not failed) when bash or jq are absent from PATH so
// it does not break CI environments that lack these tools.
func TestDelegationHook_SmokeTests(t *testing.T) {
	// Require bash.
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found in PATH — skipping smoke test")
	}
	// Require jq (hook uses jq for all JSON parsing).
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not found in PATH — skipping smoke test")
	}

	// Write the embedded hook to a temp file so we invoke the asset directly.
	hookContent, err := DelegationHookContent()
	if err != nil {
		t.Fatalf("DelegationHookContent: %v", err)
	}
	hookFile := filepath.Join(t.TempDir(), "enforce_delegation.sh")
	if err := os.WriteFile(hookFile, hookContent, 0o755); err != nil {
		t.Fatalf("write hook to temp: %v", err)
	}

	cases := []hookSmokeCase{
		// --- B-cases: subagent detection (AC1) ---

		// B1: agent_id at root (non-empty) → subagent → allow.
		{
			name:     "B1_agent_id_root_nonempty",
			payload:  `{"agent_id":"abc-123","tool_name":"Edit","tool_input":{"file_path":"internal/foo.go"}}`,
			wantExit: 0,
		},
		// B2: agent_id empty string at root → treated as orchestrator → block.
		{
			name:     "B2_agent_id_root_empty",
			payload:  `{"agent_id":"","tool_name":"Edit","tool_input":{"file_path":"internal/foo.go"}}`,
			wantExit: 2,
		},
		// B3: agent_id null at root → treated as orchestrator → block.
		{
			name:     "B3_agent_id_root_null",
			payload:  `{"agent_id":null,"tool_name":"Edit","tool_input":{"file_path":"internal/foo.go"}}`,
			wantExit: 2,
		},
		// B4: agent_id nested in session → D1 multi-key reads it → allow.
		{
			name:     "B4_agent_id_session_nested",
			payload:  `{"session":{"agent_id":"abc-456"},"tool_name":"Edit","tool_input":{"file_path":"internal/foo.go"}}`,
			wantExit: 0,
		},
		// B5: agent_id nested in context → D1 multi-key reads it → allow.
		{
			name:     "B5_agent_id_context_nested",
			payload:  `{"context":{"agent_id":"abc-789"},"tool_name":"Edit","tool_input":{"file_path":"internal/foo.go"}}`,
			wantExit: 0,
		},

		// --- A-cases: orchestrator bypass hardening (AC2) ---

		// A1: python shutil.copy to protected path → block.
		{
			name:     "A1_python_shutil_copy",
			payload:  `{"tool_name":"Bash","tool_input":{"command":"python3 -c \"import shutil; shutil.copy('src.go', 'internal/dst.go')\""}}`,
			wantExit: 2,
		},
		// A2: python subprocess.run(['cp', ...]) to protected path → block.
		{
			name:     "A2_python_subprocess_cp",
			payload:  `{"tool_name":"Bash","tool_input":{"command":"python3 -c \"import subprocess; subprocess.run(['cp', 'src', 'internal/dst.go'])\""}}`,
			wantExit: 2,
		},
		// A3: python os.rename to protected path → block.
		{
			name:     "A3_python_os_rename",
			payload:  `{"tool_name":"Bash","tool_input":{"command":"python3 -c \"import os; os.rename('src.go', 'internal/dst.go')\""}}`,
			wantExit: 2,
		},

		// --- Non-regression (AC3) ---

		// NR1: python print(2+2) — no paths → allow.
		{
			name:     "NR1_python_print_only",
			payload:  `{"tool_name":"Bash","tool_input":{"command":"python3 -c \"print(2+2)\""}}`,
			wantExit: 0,
		},
		// NR2: Edit to .claude/ path → allow (in whitelist).
		{
			name:     "NR2_edit_dotclaude",
			payload:  `{"tool_name":"Edit","tool_input":{"file_path":".claude/settings.json"}}`,
			wantExit: 0,
		},
		// NR3: Non-mutating tool (Read) → allow.
		{
			name:     "NR3_non_mutating_tool",
			payload:  `{"tool_name":"Read","tool_input":{"file_path":"internal/foo.go"}}`,
			wantExit: 0,
		},
		// NR4: Edit to docs/*.md → allow (in whitelist).
		{
			name:     "NR4_edit_docs_md",
			payload:  `{"tool_name":"Edit","tool_input":{"file_path":"docs/ARCHITECTURE.md"}}`,
			wantExit: 0,
		},
		// NR5: SPEC-033/040 non-regression: redirect to internal/ → block.
		{
			name:     "NR5_redirect_to_internal",
			payload:  `{"tool_name":"Bash","tool_input":{"command":"echo hello > internal/foo.go"}}`,
			wantExit: 2,
		},
		// NR6: SPEC-033/040 non-regression: redirect to .claude/ → allow.
		{
			name:     "NR6_redirect_to_dotclaude",
			payload:  `{"tool_name":"Bash","tool_input":{"command":"echo hello > .claude/settings.json"}}`,
			wantExit: 0,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("bash", hookFile)
			cmd.Stdin = bytes.NewBufferString(tc.payload)
			// Discard hook stdout/stderr — we only care about exit code.
			cmd.Stdout = nil
			cmd.Stderr = nil

			runErr := cmd.Run()

			var gotExit int
			if runErr != nil {
				if exitErr, ok := runErr.(*exec.ExitError); ok {
					gotExit = exitErr.ExitCode()
				} else {
					t.Fatalf("unexpected exec error: %v", runErr)
				}
			}

			if gotExit != tc.wantExit {
				t.Errorf("exit code = %d, want %d (payload: %s)", gotExit, tc.wantExit, tc.payload)
			}
		})
	}
}
