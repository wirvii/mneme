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
// from scratch with the expected SessionStart entry, and — the SPEC-106 AC4
// regression this test now also guards — that the "Stop" key is ABSENT, not
// merely unasserted: registering it never delivered a usable reminder (Codex
// rejects plain-text stdout for that event) and it is retired in this asset
// (see Codex().RetiredHooks for the purge of any pre-existing copy).
func TestWriteCodexHooks_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".codex", "hooks.json")

	if err := WriteCodexHooks(path); err != nil {
		t.Fatalf("WriteCodexHooks: %v", err)
	}

	hooks := parseCodexHooks(t, path)

	list, ok := hooks["SessionStart"].([]any)
	if !ok || len(list) == 0 {
		t.Error("SessionStart: event missing or empty")
	}
	if _, exists := hooks["Stop"]; exists {
		t.Errorf("Stop: event must be absent (SPEC-106 D4), got %#v", hooks["Stop"])
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
}

// TestWriteCodexHooks_CustomisedPathNotDuplicated is the regression test for
// the defect confirmed live 2026-08-04 (SPEC-107, BL-135): a hooks.json whose
// SessionStart entry was hand-edited to an absolute path used to be
// unrecognised by WriteCodexHooks, so re-running `mneme install codex` added
// a SECOND SessionStart entry — double context injection every session.
// Under the identity comparison (sameHookCommand), the customised entry is
// recognised as the same registration, so WriteCodexHooks must add nothing
// and the file must come out byte-identical.
func TestWriteCodexHooks_CustomisedPathNotDuplicated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")

	const customised = "/Users/x/.local/bin/mneme hook session-start"

	// Seed the file via the exact same MarshalIndent format WriteCodexHooks
	// itself produces, so a byte-identical comparison after WriteCodexHooks
	// proves "no rewrite happened" rather than merely "the format changed".
	seed := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{
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
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write initial hooks.json: %v", err)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read before: %v", err)
	}

	if err := WriteCodexHooks(path); err != nil {
		t.Fatalf("WriteCodexHooks: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}

	hooks := parseCodexHooks(t, path)
	list, ok := hooks["SessionStart"].([]any)
	if !ok {
		t.Fatal("SessionStart event missing")
	}
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
			if cmd, _ := entry["command"].(string); cmd == customised {
				count++
			}
		}
	}
	if count != 1 {
		t.Errorf("SessionStart has %d entries for the customised command, want exactly 1 (no duplicate)", count)
	}

	if string(before) != string(after) {
		t.Errorf("WriteCodexHooks rewrote the file despite no change needed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestWriteCodexHooks_PreservesOtherHooks verifies that pre-existing hooks for
// other events are not removed when WriteCodexHooks merges its entries, and
// that WriteCodexHooks does not itself (re-)add the retired "Stop" event
// (SPEC-106 D4) even when merging into a file that already has other hooks.
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
	// SessionStart must have been added.
	if _, ok := hooks["SessionStart"]; !ok {
		t.Error("SessionStart event was not added")
	}
	// Stop is retired (SPEC-106 D4) and must not be (re-)introduced.
	if _, ok := hooks["Stop"]; ok {
		t.Error("Stop event must not be added — it is retired (SPEC-106 D4)")
	}
}
