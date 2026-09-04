package install

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// manualMentions reports whether text mentions name at a word boundary — not
// as a substring of a longer token (SPEC-141 §5.1: "grill-me" must not match
// inside a hypothetical "grill-me-fuerte", nor the reverse).
func manualMentions(text, name string) bool {
	re := regexp.MustCompile(`(?:^|[^a-z0-9-])` + regexp.QuoteMeta(name) + `(?:[^a-z0-9-]|$)`)
	return re.MatchString(text)
}

// TestOperatingManuals_NameOnlyDeliveredTools is G3 (SPEC-141 §5.1, D14): for
// every name that is either a bundled skill or an embedded command asset, if
// a manual mentions that name by word boundary, the runtime that manual
// governs must actually deliver it — a skill (both runtimes) or an
// installed slash command (Claude Code only, since Codex never installs
// commands). This is the guardian BL-228 exists to make permanent: the day
// the operating manual names a tool again without the install actually
// providing it, this test fails naming exactly which manual and which name.
//
// D14: this test was first written and run RED against the tree that
// predates SPEC-141 (grill-me named in both manuals, delivered by neither) —
// see changes.md for that transcript.
func TestOperatingManuals_NameOnlyDeliveredTools(t *testing.T) {
	skillNames, err := BundledSkillNames()
	if err != nil {
		t.Fatalf("BundledSkillNames: %v", err)
	}
	assetFiles, err := builtinCommands.ReadDir("assets/commands")
	if err != nil {
		t.Fatalf("read assets/commands: %v", err)
	}

	nameSet := map[string]bool{}
	for _, n := range skillNames {
		nameSet[n] = true
	}
	for _, f := range assetFiles {
		nameSet[strings.TrimSuffix(f.Name(), filepath.Ext(f.Name()))] = true
	}

	claudeCmds, err := ClaudeCode("/usr/local/bin/mneme").Commands()
	if err != nil {
		t.Fatalf("ClaudeCode Commands: %v", err)
	}
	claudeDelivered := map[string]bool{}
	for _, n := range skillNames {
		claudeDelivered[n] = true
	}
	for _, c := range claudeCmds {
		base := filepath.Base(c.Path)
		claudeDelivered[strings.TrimSuffix(base, filepath.Ext(base))] = true
	}

	// Codex never installs slash commands (Commands is nil for Codex) — only
	// bundled skills travel there.
	codexDelivered := map[string]bool{}
	for _, n := range skillNames {
		codexDelivered[n] = true
	}

	claudeManual := operatingManual()
	codexManual := operatingManualCodex()

	for name := range nameSet {
		if manualMentions(claudeManual, name) && !claudeDelivered[name] {
			t.Errorf("claude-code operating manual mentions %q but claude-code does not deliver it", name)
		}
		if manualMentions(codexManual, name) && !codexDelivered[name] {
			t.Errorf("codex operating manual mentions %q but codex does not deliver it", name)
		}
	}
}
