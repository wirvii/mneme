package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// parseCodexHooks is a helper that reads and parses hooks.json from path,
// returning the top-level "hooks" map.
func parseCodexHooks(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks key missing or not an object in %s", path)
	}
	return hooks
}

// TestWriteCodexHooks_NewFile verifies that WriteCodexHooks creates hooks.json
// from scratch with the expected SessionStart and Stop entries.
func TestWriteCodexHooks_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".codex", "hooks.json")

	if err := WriteCodexHooks(path); err != nil {
		t.Fatalf("WriteCodexHooks: %v", err)
	}

	hooks := parseCodexHooks(t, path)

	for _, event := range []string{"SessionStart", "Stop"} {
		list, ok := hooks[event].([]any)
		if !ok || len(list) == 0 {
			t.Errorf("%s: event missing or empty", event)
		}
	}

	// Verify the specific commands.
	checkCommand := func(event, wantCmd string) {
		t.Helper()
		list, _ := hooks[event].([]any)
		for _, item := range list {
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
				if entry["command"] == wantCmd {
					return
				}
			}
		}
		t.Errorf("%s: command %q not found", event, wantCmd)
	}

	checkCommand("SessionStart", "mneme hook session-start")
	checkCommand("Stop", "mneme hook session-end")
}

// TestWriteCodexHooks_Idempotent verifies that running WriteCodexHooks twice
// produces a byte-identical result.
func TestWriteCodexHooks_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")

	if err := WriteCodexHooks(path); err != nil {
		t.Fatalf("first run: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after first run: %v", err)
	}

	if err := WriteCodexHooks(path); err != nil {
		t.Fatalf("second run: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after second run: %v", err)
	}

	if string(first) != string(second) {
		t.Errorf("WriteCodexHooks is not idempotent.\nFirst:\n%s\nSecond:\n%s", first, second)
	}
}

// TestWriteCodexHooks_NoDuplicateEntries verifies that running WriteCodexHooks
// twice does not add duplicate command entries within the same event.
func TestWriteCodexHooks_NoDuplicateEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")

	if err := WriteCodexHooks(path); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := WriteCodexHooks(path); err != nil {
		t.Fatalf("second run: %v", err)
	}

	hooks := parseCodexHooks(t, path)

	countCommand := func(event, cmd string) int {
		list, _ := hooks[event].([]any)
		count := 0
		for _, item := range list {
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
				if entry["command"] == cmd {
					count++
				}
			}
		}
		return count
	}

	if n := countCommand("SessionStart", "mneme hook session-start"); n != 1 {
		t.Errorf("SessionStart: expected 1 occurrence of session-start, got %d", n)
	}
	if n := countCommand("Stop", "mneme hook session-end"); n != 1 {
		t.Errorf("Stop: expected 1 occurrence of session-end, got %d", n)
	}
}

// TestWriteCodexHooks_PreservesOtherHooks verifies that pre-existing hooks for
// other events are not removed when WriteCodexHooks merges its entries.
func TestWriteCodexHooks_PreservesOtherHooks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")

	// Seed a hooks.json with an existing PreToolUse event.
	existing := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{"type": "command", "command": "other-hook"},
					},
				},
			},
		},
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if err := WriteCodexHooks(path); err != nil {
		t.Fatalf("WriteCodexHooks: %v", err)
	}

	hooks := parseCodexHooks(t, path)

	// PreToolUse must still be present.
	if _, ok := hooks["PreToolUse"]; !ok {
		t.Error("PreToolUse event was removed")
	}
	// SessionStart and Stop must have been added.
	if _, ok := hooks["SessionStart"]; !ok {
		t.Error("SessionStart event was not added")
	}
	if _, ok := hooks["Stop"]; !ok {
		t.Error("Stop event was not added")
	}
}
