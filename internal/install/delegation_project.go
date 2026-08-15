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
// implementer subagents does not need delegation containment and never
// enables it, while a
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
// entries into both runtime project hook files. It reuses PatchHooks'
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
	codexPath := filepath.Join(repoRoot, ".codex", "hooks.json")
	codexProxy := &Agent{Hooks: func() (string, []HookPatch, error) {
		return codexPath, patches, nil
	}}
	if err := PatchHooks(codexProxy); err != nil {
		_, _ = removeHookCommands(settingsPath, patches)
		return "", fmt.Errorf("install: enable project delegation hook for Codex: %w", err)
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
	if _, err := removeHookCommands(filepath.Join(repoRoot, ".codex", "hooks.json"), patches); err != nil {
		return "", fmt.Errorf("install: disable project delegation hook for Codex: %w", err)
	}
	return settingsPath, nil
}

// ProjectDelegationHookStatus reports whether repoRoot's
// .claude/settings.json currently registers BOTH delegation-enforcement
// PreToolUse commands. "Registers" is by hook identity (SPEC-107,
// sameHookCommand), so a registration customised to an absolute path (or
// with extra arguments) still reports enabled=true — the status only goes
// stale for the literal canonical string, which the identity check no
// longer requires.
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
	if !containsAllHookPatches(settings, patches) {
		return false, settingsPath, nil
	}
	codexData, codexErr := os.ReadFile(filepath.Join(repoRoot, ".codex", "hooks.json"))
	if codexErr != nil {
		if os.IsNotExist(codexErr) {
			return false, settingsPath, nil
		}
		return false, settingsPath, fmt.Errorf("install: project delegation hook status: read Codex hooks: %w", codexErr)
	}
	var codexSettings map[string]any
	if err := json.Unmarshal(codexData, &codexSettings); err != nil {
		return false, settingsPath, fmt.Errorf("install: project delegation hook status: parse Codex hooks: %w", err)
	}
	return containsAllHookPatches(codexSettings, patches), settingsPath, nil
}

func containsAllHookPatches(settings map[string]any, patches []HookPatch) bool {
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		return false
	}
	for _, patch := range patches {
		eventList, _ := hooks[patch.Event].([]any)
		if !hookCommandExists(eventList, patch.Command) {
			return false
		}
	}
	return true
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
// keeps a repeated purge byte-identical (SPEC-106 AC10b). Matching is by hook
// identity (SPEC-107, sameHookCommand): removed reports the REAL command
// string(s) found in the file — which may be a customised path, not
// necessarily the canonical patches.Command — via matchedHookCommands
// (DD14), so a caller's log output never falsely claims to have purged the
// canonical form when it purged a user's customisation instead.
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
			removedHere = append(removedHere, matchedHookCommands(eventList, cmd)...)
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
// every command entry that is the SAME hook registration (SPEC-107,
// sameHookCommand — executable + subcommand identity, not necessarily the
// same literal string) as one of commands removed. Matcher-groups whose
// inner "hooks" array becomes empty are dropped entirely; every other
// matcher-group (and every entry within it that doesn't match) is preserved
// unchanged.
//
// This MUST share the exact same predicate hookCommandExists uses to detect
// what removeHookCommands is about to purge (SPEC-107 DD6): before this
// change, detection and filtering held two independent notions of "same
// command" (hookCommandExists's identity-aware comparison vs. this
// function's literal map[string]bool set). Had only detection been widened,
// removeHookCommands would have reported a customised entry as removed
// (`removed` non-empty), rewritten the file, and left that same entry in
// place — a worse defect than the literal-equality status quo: the purge
// step would misreport success and never converge to a stable state. AC18
// verifies the two stay in agreement.
func filterOutHookCommands(eventList []any, commands []string) []any {
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
			if matchesAny(cmd, commands) {
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

// matchesAny reports whether cmd is the same hook registration (SPEC-107,
// sameHookCommand) as any entry in candidates.
func matchesAny(cmd string, candidates []string) bool {
	for _, c := range candidates {
		if sameHookCommand(cmd, c) {
			return true
		}
	}
	return false
}
