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
	for _, want := range []string{"mneme hook session-start", "mneme hook session-end", "mneme hook pre-tool-use", "mneme hook enforce-delegation"} {
		if !strings.Contains(settings, want) {
			t.Errorf("settings.json: expected hook command %q not found", want)
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
}
