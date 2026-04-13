package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestClaudeCode_MCPConfig verifies that the generated MCP JSON is valid and
// contains the expected fields.
func TestClaudeCode_MCPConfig(t *testing.T) {
	agent := ClaudeCode("/usr/local/bin/mneme")

	path, content, err := agent.MCPConfig("/usr/local/bin/mneme")
	if err != nil {
		t.Fatalf("MCPConfig returned error: %v", err)
	}

	if path == "" {
		t.Error("MCPConfig path must not be empty")
	}
	if !strings.HasSuffix(path, "mneme.json") {
		t.Errorf("MCPConfig path should end with mneme.json, got %q", path)
	}

	var cfg map[string]any
	if err := json.Unmarshal(content, &cfg); err != nil {
		t.Fatalf("MCPConfig content is not valid JSON: %v", err)
	}

	if cmd, ok := cfg["command"].(string); !ok || cmd != "/usr/local/bin/mneme" {
		t.Errorf("MCPConfig command = %v, want /usr/local/bin/mneme", cfg["command"])
	}

	args, ok := cfg["args"].([]any)
	if !ok || len(args) < 2 {
		t.Fatalf("MCPConfig args missing or too short: %v", cfg["args"])
	}
	if args[0] != "mcp" {
		t.Errorf("MCPConfig args[0] = %v, want mcp", args[0])
	}
	if args[1] != "--tools=agent" {
		t.Errorf("MCPConfig args[1] = %v, want --tools=agent", args[1])
	}
}

// TestClaudeCode_Protocol verifies that the protocol markdown contains all
// critical sections the agent needs to operate mneme autonomously.
func TestClaudeCode_Protocol(t *testing.T) {
	agent := ClaudeCode("")

	_, content, markers, err := agent.Protocol()
	if err != nil {
		t.Fatalf("Protocol returned error: %v", err)
	}

	proto := string(content)

	requiredSections := []string{
		"# mneme — Persistent Memory Protocol",
		"## At Session Start",
		"## When to Save",
		"## When to Search",
		"## Session End (Mandatory)",
		"## Post-Compaction Recovery",
		"## Principles",
		"mem_context",
		"mem_save",
		"mem_search",
		"mem_session_end",
	}
	for _, section := range requiredSections {
		if !strings.Contains(proto, section) {
			t.Errorf("Protocol missing required section/keyword: %q", section)
		}
	}

	if markers[0] == "" || markers[1] == "" {
		t.Error("Protocol markers must not be empty")
	}
	if !strings.HasPrefix(proto, markers[0]) {
		t.Errorf("Protocol content should start with start marker %q", markers[0])
	}
	if !strings.HasSuffix(strings.TrimSpace(proto), markers[1]) {
		t.Errorf("Protocol content should end with end marker %q", markers[1])
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

// TestInjectProtocol_NewFile verifies that InjectProtocol creates the target
// file containing the protocol block when the file does not yet exist.
func TestInjectProtocol_NewFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "CLAUDE.md")

	startMarker := "<!-- mneme:protocol:start -->"
	endMarker := "<!-- mneme:protocol:end -->"
	block := []byte(startMarker + "\nprotocol content\n" + endMarker)

	if err := injectProtocolFile(target, block, startMarker, endMarker); err != nil {
		t.Fatalf("injectProtocolFile error: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, startMarker) {
		t.Error("injected file missing start marker")
	}
	if !strings.Contains(content, "protocol content") {
		t.Error("injected file missing protocol content")
	}
	if !strings.Contains(content, endMarker) {
		t.Error("injected file missing end marker")
	}
}

// TestInjectProtocol_ExistingFile verifies that InjectProtocol appends the
// protocol block to an existing file that has no markers, without overwriting
// the existing content.
func TestInjectProtocol_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "CLAUDE.md")

	existingContent := "# My existing CLAUDE.md\n\nSome existing rules here.\n"
	if err := os.WriteFile(target, []byte(existingContent), 0o644); err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	startMarker := "<!-- mneme:protocol:start -->"
	endMarker := "<!-- mneme:protocol:end -->"
	block := []byte(startMarker + "\nprotocol content\n" + endMarker)

	if err := injectProtocolFile(target, block, startMarker, endMarker); err != nil {
		t.Fatalf("injectProtocolFile error: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "My existing CLAUDE.md") {
		t.Error("existing content was clobbered")
	}
	if !strings.Contains(content, "protocol content") {
		t.Error("protocol content was not appended")
	}
}

// TestInjectProtocol_Replace verifies that InjectProtocol replaces the existing
// protocol block between markers with the new content.
func TestInjectProtocol_Replace(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "CLAUDE.md")

	startMarker := "<!-- mneme:protocol:start -->"
	endMarker := "<!-- mneme:protocol:end -->"

	existingContent := "# My CLAUDE.md\n\n" +
		startMarker + "\nOLD protocol content\n" + endMarker + "\n\n" +
		"# After section\n"
	if err := os.WriteFile(target, []byte(existingContent), 0o644); err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	block := []byte(startMarker + "\nNEW protocol content\n" + endMarker)

	if err := injectProtocolFile(target, block, startMarker, endMarker); err != nil {
		t.Fatalf("injectProtocolFile error: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}

	content := string(data)
	if strings.Contains(content, "OLD protocol content") {
		t.Error("old protocol content should have been replaced")
	}
	if !strings.Contains(content, "NEW protocol content") {
		t.Error("new protocol content is missing")
	}
	if !strings.Contains(content, "My CLAUDE.md") {
		t.Error("content before markers was clobbered")
	}
	if !strings.Contains(content, "After section") {
		t.Error("content after markers was clobbered")
	}
}

// TestInjectProtocol_NoOverwrite verifies that important user content is never
// destroyed by InjectProtocol, regardless of whether markers are present.
// This is the regression test for the destructive overwrite bug.
func TestInjectProtocol_NoOverwrite(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "CLAUDE.md")

	importantContent := "# Claude Code — Global Configuration\n\n" +
		"## Language\nAlways respond in Español.\n\n" +
		"## Custom Rules\nNever do X.\nAlways do Y.\n"

	if err := os.WriteFile(target, []byte(importantContent), 0o644); err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	startMarker := "<!-- mneme:protocol:start -->"
	endMarker := "<!-- mneme:protocol:end -->"
	block := []byte(startMarker + "\nprotocol content\n" + endMarker)

	if err := injectProtocolFile(target, block, startMarker, endMarker); err != nil {
		t.Fatalf("injectProtocolFile error: %v", err)
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
			t.Errorf("user content lost after inject: %q", line)
		}
	}

	// The protocol must have been appended.
	if !strings.Contains(content, startMarker) {
		t.Error("start marker missing after inject")
	}
	if !strings.Contains(content, "protocol content") {
		t.Error("protocol content missing after inject")
	}
	if !strings.Contains(content, endMarker) {
		t.Error("end marker missing after inject")
	}

	// Running inject a second time must not duplicate the block.
	if err := injectProtocolFile(target, block, startMarker, endMarker); err != nil {
		t.Fatalf("second injectProtocolFile error: %v", err)
	}
	data2, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target after second inject: %v", err)
	}
	count := strings.Count(string(data2), startMarker)
	if count != 1 {
		t.Errorf("start marker appears %d times after second inject, want 1", count)
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

// injectProtocolFile is the testable core of InjectProtocol. It accepts an
// explicit file path so tests can use a temporary directory.
func injectProtocolFile(path string, block []byte, startMarker, endMarker string) error {
	existing, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return os.WriteFile(path, append(block, '\n'), 0o644)
	}
	if err != nil {
		return err
	}
	merged := mergeProtocol(existing, block, startMarker, endMarker)
	return os.WriteFile(path, merged, 0o644)
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
