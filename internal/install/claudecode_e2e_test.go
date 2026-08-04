package install

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestClaudeCodeInstall_Idempotency is an end-to-end test that runs
// Install(ClaudeCode(...)) twice against a temporary HOME and verifies every
// artefact is byte-identical the second time — mirrors
// TestCodexInstall_Idempotency's pattern for the other supported agent.
func TestClaudeCodeInstall_Idempotency(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	const binPath = "/usr/local/bin/mneme"
	agent := ClaudeCode(binPath)

	if err := Install(agent, binPath); err != nil {
		t.Fatalf("first Install: %v", err)
	}

	paths := claudeCodeGoldenArtifactPaths(tmpHome)
	first := make(map[string][]byte, len(paths))
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("after first install, read %s: %v", p, err)
		}
		first[p] = data
	}

	if err := Install(agent, binPath); err != nil {
		t.Fatalf("second Install: %v", err)
	}

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("after second install, read %s: %v", p, err)
		}
		if !bytes.Equal(first[p], data) {
			t.Errorf("%s not idempotent across two Install() calls", p)
		}
	}
}

// TestClaudeCodeInstall_RetiresStopHook covers AC7 (SPEC-106): starting from a
// pre-existing ~/.claude/settings.json that already registers the retired
// Stop -> mneme hook session-end command (and nothing else in Stop), plus a
// foreign PreToolUse entry and an unrelated top-level key, Install(ClaudeCode(...))
// removes the Stop key entirely while leaving everything else untouched.
func TestClaudeCodeInstall_RetiresStopHook(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	settingsDir := filepath.Join(tmpHome, ".claude")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	settingsPath := filepath.Join(settingsDir, "settings.json")
	existing := `{
  "theme": "dark",
  "hooks": {
    "Stop": [
      {"hooks": [{"type": "command", "command": "mneme hook session-end"}]}
    ],
    "PreToolUse": [
      {"hooks": [{"type": "command", "command": "some-other-hook.sh"}]}
    ]
  }
}`
	if err := os.WriteFile(settingsPath, []byte(existing), 0o644); err != nil {
		t.Fatalf("write initial settings: %v", err)
	}

	const binPath = "/usr/local/bin/mneme"
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
	if _, exists := hooks["Stop"]; exists {
		t.Errorf("Stop key must not exist after install, got %#v", hooks["Stop"])
	}
	assertHookEntry(t, hooks, "SessionStart", "mneme hook session-start")
	assertHookEntry(t, hooks, "PreToolUse", "some-other-hook.sh")
	if settings["theme"] != "dark" {
		t.Errorf("theme = %v, want \"dark\"", settings["theme"])
	}
}

// TestClaudeCodeInstall_IdempotencyWithPreexistingStopHook covers AC10(b) for
// Claude Code: a HOME that already carries the retired Stop registration has
// it purged on the first install; a second install finds nothing left to
// remove for the Stop event and settings.json stays byte-identical from that
// point on. This test asserts byte-identical content rather than mtime: the
// "Session hooks" and "Delegation hook" steps (PatchHooks) unconditionally
// rewrite settings.json on every install regardless of the "Retire stale
// hooks" step's own no-write behaviour, so mtime is not a meaningful signal
// at this full end-to-end level — the no-write proof for removeHookCommands
// itself lives at the unit level (TestRemoveHookCommands_NoMatchDoesNotWrite)
// and the "none" detail is covered by TestRetireStaleHooksStep_Detail.
func TestClaudeCodeInstall_IdempotencyWithPreexistingStopHook(t *testing.T) {
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
      {"hooks": [{"type": "command", "command": "mneme hook session-end"}]}
    ]
  }
}`
	if err := os.WriteFile(settingsPath, []byte(existing), 0o644); err != nil {
		t.Fatalf("write initial settings: %v", err)
	}

	const binPath = "/usr/local/bin/mneme"

	if err := Install(ClaudeCode(binPath), binPath); err != nil {
		t.Fatalf("first Install: %v", err)
	}
	afterFirst, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings after first install: %v", err)
	}
	var firstSettings map[string]any
	if err := json.Unmarshal(afterFirst, &firstSettings); err != nil {
		t.Fatalf("unmarshal after first install: %v", err)
	}
	if hooks, ok := firstSettings["hooks"].(map[string]any); ok {
		if _, exists := hooks["Stop"]; exists {
			t.Fatalf("precondition failed: Stop key still present after first install: %#v", hooks["Stop"])
		}
	}

	if err := Install(ClaudeCode(binPath), binPath); err != nil {
		t.Fatalf("second Install: %v", err)
	}
	afterSecond, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings after second install: %v", err)
	}

	if !bytes.Equal(afterFirst, afterSecond) {
		t.Errorf("settings.json not byte-identical between 1st and 2nd install:\n1st:\n%s\n2nd:\n%s", afterFirst, afterSecond)
	}
}

// claudeCodeGoldenArtifactPaths returns the absolute paths of every artefact
// a vanilla `mneme install claude-code` is expected to produce under tmpHome.
func claudeCodeGoldenArtifactPaths(tmpHome string) []string {
	return []string{
		filepath.Join(tmpHome, ".claude.json"),
		filepath.Join(tmpHome, ".claude", "CLAUDE.md"),
		filepath.Join(tmpHome, ".claude", "settings.json"),
		filepath.Join(tmpHome, ".claude", "commands", "mneme-init.md"),
		filepath.Join(tmpHome, ".claude", "skills", "example-skill", "SKILL.md"),
		filepath.Join(tmpHome, ".claude", "skills", "mneme-init", "SKILL.md"),
		filepath.Join(tmpHome, ".claude", "skills", "mneme-profile-author", "SKILL.md"),
		filepath.Join(tmpHome, ".mneme", "templates", "spec-template.md"),
	}
}

// TestClaudeCodeInstall_VanillaGolden is the SPEC-096 §6 AC7 no-regression
// guard: `mneme install claude-code` against a project with no profile pin
// (SourceVanilla, §6 3.5) produces EXACTLY the same artefacts it did before
// §6 landed — §6 adds internal/install/assets/profiles/default/ and
// DefaultProfileFS() in parallel, but never touches installSteps/
// ClaudeCode()/the existing assets/{agents,skills,templates,operating-manual}
// tree. This test freezes that contract: every expected artefact exists with
// the right shape, AND — the regression this guards against — NO
// .claude/agents/ directory is created (SPEC-073 already dropped global
// agent profiles; §6's default-profile agents must never leak into a
// vanilla, non-profile install) and no profile-lock/profile-block artefact
// appears (Install() never activates any profile — that is exclusively a
// SessionStart/`profile use` runtime path, §6 §3.5).
func TestClaudeCodeInstall_VanillaGolden(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	const binPath = "/usr/local/bin/mneme"
	if err := Install(ClaudeCode(binPath), binPath); err != nil {
		t.Fatalf("Install: %v", err)
	}

	for _, p := range claudeCodeGoldenArtifactPaths(tmpHome) {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected artefact %s to exist: %v", p, err)
		}
	}

	// --- MCP config (.claude.json) ---
	mcpData, err := os.ReadFile(filepath.Join(tmpHome, ".claude.json"))
	if err != nil {
		t.Fatalf("read .claude.json: %v", err)
	}
	var mcpRoot map[string]any
	if err := json.Unmarshal(mcpData, &mcpRoot); err != nil {
		t.Fatalf("parse .claude.json: %v", err)
	}
	mcpServers, ok := mcpRoot["mcpServers"].(map[string]any)
	if !ok {
		t.Fatal(".claude.json: mcpServers missing or not an object")
	}
	if _, ok := mcpServers["mneme"]; !ok {
		t.Error(".claude.json: mcpServers.mneme entry missing")
	}

	// --- Operating manual managed block ---
	manualData, err := os.ReadFile(filepath.Join(tmpHome, ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	manual := string(manualData)
	if !strings.Contains(manual, "<!-- mneme:managed:start") || !strings.Contains(manual, "<!-- mneme:managed:end -->") {
		t.Error("CLAUDE.md: managed block markers missing")
	}

	// --- Session hooks ---
	settingsData, err := os.ReadFile(filepath.Join(tmpHome, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	settings := string(settingsData)
	for _, want := range []string{"mneme hook session-start", "mneme hook pre-tool-use", "mneme hook enforce-delegation"} {
		if !strings.Contains(settings, want) {
			t.Errorf("settings.json: expected hook command %q not found", want)
		}
	}
	// AC5 (SPEC-106): "session-end" is retired from the Stop event — a
	// vanilla install must never register it, nor the "Stop" key at all.
	for _, mustNot := range []string{"mneme hook session-end", "\"Stop\""} {
		if strings.Contains(settings, mustNot) {
			t.Errorf("settings.json: must not contain %q (SPEC-106 D4), got:\n%s", mustNot, settings)
		}
	}

	// --- The regression this test exists to catch: no global agent
	//     profiles, no profile activation artefacts, in a vanilla install. ---
	if _, err := os.Stat(filepath.Join(tmpHome, ".claude", "agents")); !os.IsNotExist(err) {
		t.Errorf(".claude/agents must not exist after a vanilla install (SPEC-073); stat err = %v", err)
	}
	if strings.Contains(manual, "<!-- mneme:profile:start -->") {
		t.Error("CLAUDE.md must not contain a profile block after a vanilla install — Install() never activates a profile")
	}
	if _, err := os.Stat(filepath.Join(tmpHome, ".mneme", "profile.lock")); !os.IsNotExist(err) {
		t.Errorf("no .mneme/profile.lock must exist after a vanilla install; stat err = %v", err)
	}
}

// TestClaudeCodeOperatingManual_AntiDrift verifies that the embedded
// operating-manual.md (claude-code) contains the grill-me-over-brainstorming
// refinement doctrine (SPEC-103 D10/AC10). It is the first content-level
// anti-drift test for the claude-code manual's body — mirroring
// TestCodexOperatingManual_AntiDrift, which already guards the Codex variant
// — so that a future edit cannot silently drop the clause the way nothing
// previously protected this file's prose.
func TestClaudeCodeOperatingManual_AntiDrift(t *testing.T) {
	content := operatingManual()

	if len(content) == 0 {
		t.Fatal("operatingManual() returned empty string")
	}

	if !strings.Contains(content, "grill-me") {
		t.Error("operating-manual.md: expected keyword \"grill-me\" not found")
	}
	if !strings.Contains(content, "Do NOT use `superpowers:brainstorming`") {
		t.Error("operating-manual.md: expected negation of superpowers:brainstorming not found")
	}
	// SPEC-106 DD19: the manual must say there is no automatic net for
	// session-end — mneme hook session-end is a retired no-op, not a
	// reminder mechanism.
	if !strings.Contains(content, "no hook that reminds you") {
		t.Error("operating-manual.md: expected the no-automatic-net phrase for session end not found")
	}
}
