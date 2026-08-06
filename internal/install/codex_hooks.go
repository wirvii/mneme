package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// WriteCodexHooks merges the mneme session hooks into ~/.codex/hooks.json
// (or whichever path is given). It uses an append-if-absent algorithm that
// mirrors PatchHooks but targets the Codex hooks.json schema.
//
// Codex hooks.json schema (S2, SPEC-049 — verified against official docs):
//
//	{
//	  "hooks": {
//	    "SessionStart": [{"hooks": [{"type":"command","command":"<string>"}]}],
//	    "UserPromptSubmit": [{"hooks": [{"type":"command","command":"<string>"}]}]
//	  }
//	}
//
// "Stop" is deliberately absent from the schema above (SPEC-106, D4): Codex's
// Stop contract rejects this hook outright (plain text on stdout with exit 0
// is invalid for that event, S1), and the registration never delivered a
// usable reminder to either agent in the first place. A pre-existing "Stop"
// registration from an older mneme version is purged by
// Codex().RetiredHooks, not re-added here.
//
// Both hooks.json and ~/.claude/settings.json nest their hook registrations
// under the SAME top-level "hooks" key — there is no root difference to
// adapt for (DD8, SPEC-106; a previous version of this comment claimed
// otherwise). removeHookCommands (delegation_project.go) operates on either
// file unmodified. Everything inside that key (event → array of
// matcher-groups → array of hook commands) is identical too.
//
// The function reuses hookCommandExists to prevent duplicate entries.
//
// Per D3b (SPEC-049): hooks are best-effort; if the user has not trusted them
// via Codex's `/hooks` command, the session-lifecycle discipline in AGENTS.md
// (§5) ensures memory hygiene without automation. The install step always
// writes the file so the hooks are ready to be trusted at any time.
func WriteCodexHooks(path string) error {
	root := map[string]any{}

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("install: codex hooks: read %s: %w", path, err)
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("install: codex hooks: parse %s: %w", path, err)
		}
	}

	// Ensure "hooks" key exists and is a map.
	hooksRaw, ok := root["hooks"]
	if !ok || hooksRaw == nil {
		hooksRaw = map[string]any{}
	}
	hooks, ok := hooksRaw.(map[string]any)
	if !ok {
		return fmt.Errorf("install: codex hooks: hooks key in %s is not an object", path)
	}

	// Patches to register: SessionStart plus opt-in speech prompt. Stop remains retired,
	// see the package-level godoc above and Codex().RetiredHooks).
	patches := []HookPatch{
		{Event: "SessionStart", Command: "mneme hook session-start"},
		{Event: "UserPromptSubmit", Command: "mneme hook speech-prompt"},
	}

	for _, patch := range patches {
		cmd := map[string]any{
			"type":    "command",
			"command": patch.Command,
		}

		// Retrieve existing event list (array of matcher-groups), or start empty.
		var eventList []any
		if raw, exists := hooks[patch.Event]; exists && raw != nil {
			if list, ok := raw.([]any); ok {
				eventList = list
			}
		}

		// Only append if not already present in any group.
		if !hookCommandExists(eventList, patch.Command) {
			group := map[string]any{
				"hooks": []any{cmd},
			}
			eventList = append(eventList, group)
		}
		hooks[patch.Event] = eventList
	}

	root["hooks"] = hooks

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("install: codex hooks: marshal: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("install: codex hooks: mkdir: %w", err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return fmt.Errorf("install: codex hooks: write: %w", err)
	}
	return nil
}
