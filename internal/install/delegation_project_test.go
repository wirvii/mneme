package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestEnableProjectDelegationHook_RegistersBothEntries verifies that
// EnableProjectDelegationHook merges both the Go rules hook and the bash
// script command into <repoRoot>/.claude/settings.json.
func TestEnableProjectDelegationHook_RegistersBothEntries(t *testing.T) {
	repoRoot := t.TempDir()

	path, err := EnableProjectDelegationHook(repoRoot)
	if err != nil {
		t.Fatalf("EnableProjectDelegationHook: %v", err)
	}
	if path != filepath.Join(repoRoot, ".claude", "settings.json") {
		t.Errorf("unexpected settings path: %s", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	hooks := settings["hooks"].(map[string]any)

	assertHookEntry(t, hooks, "PreToolUse", "mneme hook pre-tool-use")

	patches, err := ProjectDelegationHookPatches()
	if err != nil {
		t.Fatalf("ProjectDelegationHookPatches: %v", err)
	}
	assertHookEntry(t, hooks, "PreToolUse", patches[1].Command)
}

// TestEnableProjectDelegationHook_Idempotent verifies that enabling twice
// does not duplicate entries.
func TestEnableProjectDelegationHook_Idempotent(t *testing.T) {
	repoRoot := t.TempDir()

	if _, err := EnableProjectDelegationHook(repoRoot); err != nil {
		t.Fatalf("first enable: %v", err)
	}
	if _, err := EnableProjectDelegationHook(repoRoot); err != nil {
		t.Fatalf("second enable: %v", err)
	}

	settingsPath := filepath.Join(repoRoot, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	hooks := settings["hooks"].(map[string]any)

	assertHookCount(t, hooks, "PreToolUse", "mneme hook pre-tool-use", 1)
}

// TestEnableProjectDelegationHook_PreservesOtherSettings verifies that
// enabling the project hook does not disturb pre-existing settings.json
// content (other hook events, arbitrary top-level keys).
func TestEnableProjectDelegationHook_PreservesOtherSettings(t *testing.T) {
	repoRoot := t.TempDir()
	settingsDir := filepath.Join(repoRoot, ".claude")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	existing := `{
  "hooks": {
    "SessionStart": [
      {"matcher": "", "hooks": [{"type": "command", "command": "mneme hook session-start"}]}
    ]
  },
  "permissions": {"allow": ["Read"]}
}`
	settingsPath := filepath.Join(settingsDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(existing), 0o644); err != nil {
		t.Fatalf("write initial settings: %v", err)
	}

	if _, err := EnableProjectDelegationHook(repoRoot); err != nil {
		t.Fatalf("EnableProjectDelegationHook: %v", err)
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

	assertHookEntry(t, hooks, "SessionStart", "mneme hook session-start")
	assertHookEntry(t, hooks, "PreToolUse", "mneme hook pre-tool-use")

	perms, ok := settings["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("permissions key was lost")
	}
	if allow, ok := perms["allow"].([]any); !ok || len(allow) != 1 || allow[0] != "Read" {
		t.Errorf("permissions.allow was mutated: %#v", perms["allow"])
	}
}

// TestDisableProjectDelegationHook_RemovesBothEntries verifies that disable
// removes both PreToolUse entries after enable registered them.
func TestDisableProjectDelegationHook_RemovesBothEntries(t *testing.T) {
	repoRoot := t.TempDir()

	if _, err := EnableProjectDelegationHook(repoRoot); err != nil {
		t.Fatalf("enable: %v", err)
	}
	path, err := DisableProjectDelegationHook(repoRoot)
	if err != nil {
		t.Fatalf("DisableProjectDelegationHook: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}

	hooksRaw, hasHooks := settings["hooks"]
	if hasHooks {
		hooks, ok := hooksRaw.(map[string]any)
		if !ok {
			t.Fatalf("hooks is not an object: %#v", hooksRaw)
		}
		if _, exists := hooks["PreToolUse"]; exists {
			t.Errorf("PreToolUse event should have been pruned entirely, still present: %#v", hooks["PreToolUse"])
		}
	}
}

// TestDisableProjectDelegationHook_PreservesOtherEntries verifies that
// disabling only removes the delegation-hook commands, leaving unrelated
// PreToolUse entries (and other events) in place.
func TestDisableProjectDelegationHook_PreservesOtherEntries(t *testing.T) {
	repoRoot := t.TempDir()
	settingsDir := filepath.Join(repoRoot, ".claude")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	settingsPath := filepath.Join(settingsDir, "settings.json")

	existing := `{
  "hooks": {
    "PreToolUse": [
      {"matcher": "", "hooks": [{"type": "command", "command": "some-other-hook.sh"}]}
    ],
    "SessionStart": [
      {"matcher": "", "hooks": [{"type": "command", "command": "mneme hook session-start"}]}
    ]
  }
}`
	if err := os.WriteFile(settingsPath, []byte(existing), 0o644); err != nil {
		t.Fatalf("write initial settings: %v", err)
	}

	if _, err := EnableProjectDelegationHook(repoRoot); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, err := DisableProjectDelegationHook(repoRoot); err != nil {
		t.Fatalf("disable: %v", err)
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

	assertHookEntry(t, hooks, "PreToolUse", "some-other-hook.sh")
	assertHookEntry(t, hooks, "SessionStart", "mneme hook session-start")
	assertHookCount(t, hooks, "PreToolUse", "mneme hook pre-tool-use", 0)
}

// TestDisableProjectDelegationHook_NoSettingsFile verifies disable is a
// no-op success when no settings.json exists at all.
func TestDisableProjectDelegationHook_NoSettingsFile(t *testing.T) {
	repoRoot := t.TempDir()

	path, err := DisableProjectDelegationHook(repoRoot)
	if err != nil {
		t.Fatalf("DisableProjectDelegationHook on missing file: %v", err)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Errorf("disable should not have created a settings file, but %s exists", path)
	}
}

// TestProjectDelegationHookStatus reports enabled/disabled correctly across
// the enable/disable lifecycle.
func TestProjectDelegationHookStatus(t *testing.T) {
	repoRoot := t.TempDir()

	enabled, _, err := ProjectDelegationHookStatus(repoRoot)
	if err != nil {
		t.Fatalf("status (no file): %v", err)
	}
	if enabled {
		t.Error("expected disabled with no settings file")
	}

	if _, err := EnableProjectDelegationHook(repoRoot); err != nil {
		t.Fatalf("enable: %v", err)
	}
	enabled, _, err = ProjectDelegationHookStatus(repoRoot)
	if err != nil {
		t.Fatalf("status (after enable): %v", err)
	}
	if !enabled {
		t.Error("expected enabled after EnableProjectDelegationHook")
	}

	if _, err := DisableProjectDelegationHook(repoRoot); err != nil {
		t.Fatalf("disable: %v", err)
	}
	enabled, _, err = ProjectDelegationHookStatus(repoRoot)
	if err != nil {
		t.Fatalf("status (after disable): %v", err)
	}
	if enabled {
		t.Error("expected disabled after DisableProjectDelegationHook")
	}
}

// TestEnableProjectDelegationHook_StripsLegacyScriptEntry covers AC10
// (per-repo): a repo settings.json with a pre-existing legacy absolute-path
// enforce_delegation.sh entry has that entry removed and the portable
// subcommand added, with no duplicates, when EnableProjectDelegationHook
// runs (`mneme delegation-hook enable`).
func TestEnableProjectDelegationHook_StripsLegacyScriptEntry(t *testing.T) {
	repoRoot := t.TempDir()
	settingsDir := filepath.Join(repoRoot, ".claude")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	settingsPath := filepath.Join(settingsDir, "settings.json")
	legacyCommand := "/Users/alice/.claude/hooks/enforce_delegation.sh"
	existing := `{
  "hooks": {
    "PreToolUse": [
      {"matcher": "", "hooks": [{"type": "command", "command": "` + legacyCommand + `"}]}
    ],
    "SessionStart": [
      {"matcher": "", "hooks": [{"type": "command", "command": "mneme hook session-start"}]}
    ]
  }
}`
	if err := os.WriteFile(settingsPath, []byte(existing), 0o644); err != nil {
		t.Fatalf("write initial settings: %v", err)
	}

	if _, err := EnableProjectDelegationHook(repoRoot); err != nil {
		t.Fatalf("EnableProjectDelegationHook: %v", err)
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
	assertHookEntry(t, hooks, "SessionStart", "mneme hook session-start")
}

// TestRemoveHookCommands_ReturnsRemovedCommands verifies that
// removeHookCommands returns exactly the commands it actually deleted (not
// merely the ones requested), and returns an empty removed slice — with no
// error — when none of the requested commands were registered.
func TestRemoveHookCommands_ReturnsRemovedCommands(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	existing := `{
  "hooks": {
    "Stop": [
      {"hooks": [{"type": "command", "command": "mneme hook session-end"}]}
    ],
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "mneme hook session-start"}]}
    ]
  }
}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatalf("write initial settings: %v", err)
	}

	patches := []HookPatch{{Event: "Stop", Command: "mneme hook session-end"}}
	removed, err := removeHookCommands(path, patches)
	if err != nil {
		t.Fatalf("removeHookCommands: %v", err)
	}
	if len(removed) != 1 || removed[0] != "mneme hook session-end" {
		t.Errorf("removed = %v, want [\"mneme hook session-end\"]", removed)
	}

	// A second call finds nothing left to remove: empty removed, no error.
	removed, err = removeHookCommands(path, patches)
	if err != nil {
		t.Fatalf("second removeHookCommands: %v", err)
	}
	if len(removed) != 0 {
		t.Errorf("second call: removed = %v, want empty", removed)
	}
}

// TestRemoveHookCommands_NoMatchDoesNotWrite is the unit-level proof behind
// SPEC-106 AC10(b): when none of the requested commands are registered,
// removeHookCommands must not touch the file at all — not even a
// byte-identical rewrite. This is verified via mtime, which only changes on
// an actual write.
func TestRemoveHookCommands_NoMatchDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	existing := `{
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "mneme hook session-start"}]}
    ]
  }
}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatalf("write initial settings: %v", err)
	}

	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}

	patches := []HookPatch{{Event: "Stop", Command: "mneme hook session-end"}}
	removed, err := removeHookCommands(path, patches)
	if err != nil {
		t.Fatalf("removeHookCommands: %v", err)
	}
	if len(removed) != 0 {
		t.Errorf("removed = %v, want empty (nothing matched)", removed)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Errorf("file was rewritten despite no match: mtime before=%v after=%v", before.ModTime(), after.ModTime())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if string(data) != existing {
		t.Errorf("file content changed despite no match:\nbefore:\n%s\nafter:\n%s", existing, data)
	}
}

// TestRemoveHookCommands_CodexHooksJSONRoot is the unit-level proof of DD8
// (SPEC-106): removeHookCommands, unmodified, operates correctly on a file
// shaped like ~/.codex/hooks.json — same top-level "hooks" key as
// ~/.claude/settings.json — removing only the requested Stop command and
// leaving SessionStart untouched.
func TestRemoveHookCommands_CodexHooksJSONRoot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")

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
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatalf("write initial hooks.json: %v", err)
	}

	patches := []HookPatch{{Event: "Stop", Command: "mneme hook session-end"}}
	removed, err := removeHookCommands(path, patches)
	if err != nil {
		t.Fatalf("removeHookCommands: %v", err)
	}
	if len(removed) != 1 || removed[0] != "mneme hook session-end" {
		t.Errorf("removed = %v, want [\"mneme hook session-end\"]", removed)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		t.Fatal("hooks key missing or not an object")
	}
	if _, exists := hooks["Stop"]; exists {
		t.Errorf("Stop event should have been pruned entirely, still present: %#v", hooks["Stop"])
	}
	assertHookEntry(t, hooks, "SessionStart", "mneme hook session-start")
}

// TestRemoveHookCommands_PurgesCustomisedPathAndReportsActual covers AC16
// (SPEC-107): a Stop entry registered with an absolute path is purged when
// the caller asks to remove the canonical "mneme hook session-end" command,
// and removed reports the REAL string that was actually deleted — the
// customised one — not the canonical one that was requested (DD14). A
// second call is then a true no-op: empty removed and a byte-identical file
// (preserving SPEC-106 AC10b).
func TestRemoveHookCommands_PurgesCustomisedPathAndReportsActual(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	const customised = "/Users/x/bin/mneme hook session-end"

	existing := `{
  "hooks": {
    "Stop": [
      {"hooks": [{"type": "command", "command": "` + customised + `"}]}
    ]
  }
}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatalf("write initial settings: %v", err)
	}

	patches := []HookPatch{{Event: "Stop", Command: "mneme hook session-end"}}
	removed, err := removeHookCommands(path, patches)
	if err != nil {
		t.Fatalf("removeHookCommands: %v", err)
	}
	if len(removed) != 1 || removed[0] != customised {
		t.Errorf("removed = %v, want [%q] (the actual customised string, not the canonical one)", removed, customised)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after first purge: %v", err)
	}

	// A second call finds nothing left to remove: empty removed, and the
	// file must not be rewritten at all.
	removed, err = removeHookCommands(path, patches)
	if err != nil {
		t.Fatalf("second removeHookCommands: %v", err)
	}
	if len(removed) != 0 {
		t.Errorf("second call: removed = %v, want empty", removed)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after second purge: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("second removeHookCommands rewrote the file despite nothing left to remove:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestRemoveHookCommands_DetectAndFilterAgree covers AC18 (SPEC-107 DD6):
// after removeHookCommands reports a non-empty removed slice, re-reading the
// file must show ZERO entries that still match that identity. This is what
// makes the "detect but don't actually filter" defect impossible — the one
// DD6 warns a commit that widens hookCommandExists without widening
// filterOutHookCommands in lockstep would introduce.
func TestRemoveHookCommands_DetectAndFilterAgree(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	const customised = "/opt/bin/mneme.exe hook session-end"

	existing := `{
  "hooks": {
    "Stop": [
      {"hooks": [{"type": "command", "command": "` + customised + `"}]}
    ]
  }
}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatalf("write initial settings: %v", err)
	}

	patches := []HookPatch{{Event: "Stop", Command: "mneme hook session-end"}}
	removed, err := removeHookCommands(path, patches)
	if err != nil {
		t.Fatalf("removeHookCommands: %v", err)
	}
	if len(removed) == 0 {
		t.Fatal("precondition failed: removeHookCommands reported nothing removed")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after purge: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	hooksRaw, hasHooks := settings["hooks"]
	if !hasHooks {
		return // Stop event was pruned entirely: zero matching entries, trivially satisfied.
	}
	hooks, ok := hooksRaw.(map[string]any)
	if !ok {
		t.Fatalf("hooks is not an object: %#v", hooksRaw)
	}
	stopList, ok := hooks["Stop"].([]any)
	if !ok {
		return // no Stop event left at all
	}
	if got := matchedHookCommands(stopList, "mneme hook session-end"); len(got) != 0 {
		t.Errorf("after a purge that reported removed=%v, the file still contains a matching entry: %v", removed, got)
	}
}

// TestProjectDelegationHookPatches_MatchGlobal verifies the project patches
// carry the exact same commands as the global ClaudeCode().DelegationHook
// registers, so opting in at project scope gives byte-identical enforcement
// semantics to the global installation.
func TestProjectDelegationHookPatches_MatchGlobal(t *testing.T) {
	projectPatches, err := ProjectDelegationHookPatches()
	if err != nil {
		t.Fatalf("ProjectDelegationHookPatches: %v", err)
	}

	agent := ClaudeCode("/usr/local/bin/mneme")
	_, globalPatches, err := agent.DelegationHook()
	if err != nil {
		t.Fatalf("DelegationHook: %v", err)
	}

	if len(projectPatches) != len(globalPatches) {
		t.Fatalf("patch count mismatch: project=%d global=%d", len(projectPatches), len(globalPatches))
	}
	for i := range projectPatches {
		if projectPatches[i].Event != globalPatches[i].Event || projectPatches[i].Command != globalPatches[i].Command {
			t.Errorf("patch %d mismatch: project=%+v global=%+v", i, projectPatches[i], globalPatches[i])
		}
	}
}
