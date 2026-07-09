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

	if err := removeHookCommands(settingsPath, patches); err != nil {
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
// from settingsPath's hooks map, pruning now-empty matcher-groups and
// now-empty event arrays. Every other entry — other hook events, every
// other top-level setting — is left untouched. A missing file, or one with
// no matching command registered, is a no-op success (no write performed).
func removeHookCommands(settingsPath string, patches []HookPatch) error {
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read settings: %w", err)
	}

	settings := map[string]any{}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("parse settings: %w", err)
		}
	}

	hooksRaw, ok := settings["hooks"]
	if !ok || hooksRaw == nil {
		return nil
	}
	hooks, ok := hooksRaw.(map[string]any)
	if !ok {
		return fmt.Errorf("settings.hooks is not an object")
	}

	byEvent := make(map[string][]string)
	for _, p := range patches {
		byEvent[p.Event] = append(byEvent[p.Event], p.Command)
	}

	changed := false
	for event, commands := range byEvent {
		eventListRaw, ok := hooks[event]
		if !ok {
			continue
		}
		eventList, ok := eventListRaw.([]any)
		if !ok {
			continue
		}

		anyExisted := false
		for _, cmd := range commands {
			if hookCommandExists(eventList, cmd) {
				anyExisted = true
				break
			}
		}
		if !anyExisted {
			continue
		}
		changed = true

		filtered := filterOutHookCommands(eventList, commands)
		if len(filtered) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = filtered
		}
	}

	if !changed {
		return nil
	}

	settings["hooks"] = hooks
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := os.WriteFile(settingsPath, append(out, '\n'), 0o644); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
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
