package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ProjectDelegationHookPatches returns the two PreToolUse hook entries used
// for delegation enforcement (the Go rules hook + the enforce-delegation
// orchestrator-guard subcommand) — identical to what
// ClaudeCode().DelegationHook registers globally.
//
// SPEC-052 §5.2/§8.2 (EPIC agnostic-agents SS-6) adds a PROJECT-scoped,
// opt-in path to register these SAME entries into
// <repoRoot>/.claude/settings.json instead of (or in addition to, during the
// transition) the global ~/.claude/settings.json: a project with no
// implementer subagents stays single-agent (precedent: Codex/SPEC-049,
// which never installs this hook at all) and never enables it, while a
// project that DOES generate implementer subagents (backend/frontend/
// bug-hunter) can opt in without touching every other project's
// configuration.
//
// Both entries are portable mneme subcommands (SPEC-069) — neither carries a
// path to the home directory, so a committed .claude/settings.json works on
// any machine with `mneme` on PATH, without requiring `mneme install` to have
// run there first. Claude Code reads PreToolUse hooks from
// `.claude/settings.json` at project scope exactly like it does from
// `~/.claude/settings.json` at user scope (see docs/enforcement-model.md for
// the verification evidence) — no self-disabling global-hook fallback is
// needed.
func ProjectDelegationHookPatches() ([]HookPatch, error) {
	return []HookPatch{
		{Event: "PreToolUse", Command: "mneme hook pre-tool-use"},
		{Event: "PreToolUse", Command: "mneme hook enforce-delegation"},
	}, nil
}

// EnableProjectDelegationHook merges the delegation-enforcement PreToolUse
// entries into <repoRoot>/.claude/settings.json. It reuses PatchHooks'
// append-if-absent merge via a temporary proxy Agent — the same pattern
// PatchDelegationHook already uses for the global settings path — so the
// result is idempotent and never duplicates entries.
//
// SPEC-069 D3: before appending, it strips any legacy
// enforce_delegation.sh absolute-path registration
// (stripLegacyDelegationHookEntries) that may already be present in this
// repo's settings.json from an install performed before the migration.
func EnableProjectDelegationHook(repoRoot string) (string, error) {
	patches, err := ProjectDelegationHookPatches()
	if err != nil {
		return "", err
	}
	settingsPath := filepath.Join(repoRoot, ".claude", "settings.json")

	if err := stripLegacyDelegationHookEntries(settingsPath); err != nil {
		return "", fmt.Errorf("install: enable project delegation hook: strip legacy: %w", err)
	}

	proxy := &Agent{
		Hooks: func() (string, []HookPatch, error) {
			return settingsPath, patches, nil
		},
	}
	if err := PatchHooks(proxy); err != nil {
		return "", fmt.Errorf("install: enable project delegation hook: %w", err)
	}
	return settingsPath, nil
}

// DisableProjectDelegationHook removes the delegation-enforcement PreToolUse
// entries from <repoRoot>/.claude/settings.json, leaving every other hook
// entry (and every other setting) untouched. A missing settings file, or one
// that never had the hook registered, is a no-op success.
func DisableProjectDelegationHook(repoRoot string) (string, error) {
	patches, err := ProjectDelegationHookPatches()
	if err != nil {
		return "", err
	}
	settingsPath := filepath.Join(repoRoot, ".claude", "settings.json")

	if _, err := removeHookCommands(settingsPath, patches); err != nil {
		return "", fmt.Errorf("install: disable project delegation hook: %w", err)
	}
	return settingsPath, nil
}

// ProjectDelegationHookStatus reports whether repoRoot's
// .claude/settings.json currently registers BOTH delegation-enforcement
// PreToolUse commands.
func ProjectDelegationHookStatus(repoRoot string) (enabled bool, settingsPath string, err error) {
	patches, perr := ProjectDelegationHookPatches()
	if perr != nil {
		return false, "", perr
	}
	settingsPath = filepath.Join(repoRoot, ".claude", "settings.json")

	data, readErr := os.ReadFile(settingsPath)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return false, settingsPath, nil
		}
		return false, settingsPath, fmt.Errorf("install: project delegation hook status: read settings: %w", readErr)
	}

	settings := map[string]any{}
	if len(data) > 0 {
		if jerr := json.Unmarshal(data, &settings); jerr != nil {
			return false, settingsPath, fmt.Errorf("install: project delegation hook status: parse settings: %w", jerr)
		}
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		return false, settingsPath, nil
	}

	for _, patch := range patches {
		eventList, _ := hooks[patch.Event].([]any)
		if !hookCommandExists(eventList, patch.Command) {
			return false, settingsPath, nil
		}
	}
	return true, settingsPath, nil
}

// removeHookCommands deletes every command entry matching one of patches
// from path's hooks map, pruning now-empty matcher-groups and now-empty
// event arrays. Every other entry — other hook events, every other
// top-level setting — is left untouched. It works on ANY JSON file that
// nests its hook registrations under a top-level "hooks" key — both
// ~/.claude/settings.json (Claude Code) and ~/.codex/hooks.json (Codex)
// share that exact shape (DD8, SPEC-106: there is no root difference to
// adapt for), so this single implementation serves both agents unmodified.
// A missing file, or one with no matching command registered, is a no-op
// success: removed is nil and no write is performed — that no-write is what
// keeps a repeated purge byte-identical (SPEC-106 AC10b).
func removeHookCommands(path string, patches []HookPatch) (removed []string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read settings: %w", err)
	}

	settings := map[string]any{}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &settings); err != nil {
			return nil, fmt.Errorf("parse settings: %w", err)
		}
	}

	hooksRaw, ok := settings["hooks"]
	if !ok || hooksRaw == nil {
		return nil, nil
	}
	hooks, ok := hooksRaw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("settings.hooks is not an object")
	}

	// Group commands by event while preserving the order events first appear
	// in patches, so the returned removed slice is deterministic regardless
	// of Go's randomised map iteration order.
	var events []string
	byEvent := make(map[string][]string)
	for _, p := range patches {
		if _, seen := byEvent[p.Event]; !seen {
			events = append(events, p.Event)
		}
		byEvent[p.Event] = append(byEvent[p.Event], p.Command)
	}

	for _, event := range events {
		commands := byEvent[event]
		eventListRaw, ok := hooks[event]
		if !ok {
			continue
		}
		eventList, ok := eventListRaw.([]any)
		if !ok {
			continue
		}

		var removedHere []string
		for _, cmd := range commands {
			if hookCommandExists(eventList, cmd) {
				removedHere = append(removedHere, cmd)
			}
		}
		if len(removedHere) == 0 {
			continue
		}
		removed = append(removed, removedHere...)

		filtered := filterOutHookCommands(eventList, commands)
		if len(filtered) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = filtered
		}
	}

	if len(removed) == 0 {
		return nil, nil
	}

	settings["hooks"] = hooks
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return removed, fmt.Errorf("marshal: %w", err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return removed, fmt.Errorf("write: %w", err)
	}
	return removed, nil
}

// filterOutHookCommands returns eventList (an array of matcher-groups) with
// every command entry whose "command" field is in commands removed.
// Matcher-groups whose inner "hooks" array becomes empty are dropped
// entirely; every other matcher-group (and every entry within it that
// doesn't match) is preserved unchanged.
func filterOutHookCommands(eventList []any, commands []string) []any {
	toRemove := make(map[string]bool, len(commands))
	for _, c := range commands {
		toRemove[c] = true
	}

	filtered := make([]any, 0, len(eventList))
	for _, item := range eventList {
		group, ok := item.(map[string]any)
		if !ok {
			filtered = append(filtered, item)
			continue
		}
		innerRaw, ok := group["hooks"]
		if !ok {
			filtered = append(filtered, item)
			continue
		}
		inner, ok := innerRaw.([]any)
		if !ok {
			filtered = append(filtered, item)
			continue
		}

		keptInner := make([]any, 0, len(inner))
		for _, h := range inner {
			entry, ok := h.(map[string]any)
			if !ok {
				keptInner = append(keptInner, h)
				continue
			}
			cmd, _ := entry["command"].(string)
			if toRemove[cmd] {
				continue
			}
			keptInner = append(keptInner, h)
		}

		if len(keptInner) == 0 {
			continue // drop the whole now-empty matcher-group
		}
		group["hooks"] = keptInner
		filtered = append(filtered, group)
	}
	return filtered
}
