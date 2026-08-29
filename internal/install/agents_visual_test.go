package install

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/subagents"
)

// visualCertificationCoveredRoles is SPEC-132 D1's closed set: exactly the
// two roles whose static asset must carry the visual-certification text.
var visualCertificationCoveredRoles = map[string]bool{
	"qa-tester": true,
	"frontend":  true,
}

// TestAgentAssets_CarryVisualSection pins SPEC-132 AC7: the static agent
// assets carry the same visual-certification text as the composed layer-1
// block (Dp3), derived from subagents.LayerOneAsset() rather than retyped
// here, and only on qa-tester/frontend.
//
// Population is the DIRECTORY, not a hand-written "the other four" list
// (SPEC-132 Dp7) — every *.md file present is checked, in both copies (V6):
// the global installer's assets/agents/*.md AND the OSS default profile's
// assets/profiles/default/agents/*.md (a separate embed, SPEC-096 §6).
//
// Same two mandatory closures as TestCompose_VisualSectionByRole (AC5):
// fails on a CutSection error, and fails if the cut section is under 200
// bytes — otherwise strings.Contains(text, "") would trivially pass.
func TestAgentAssets_CarryVisualSection(t *testing.T) {
	visualText, err := subagents.CutSection(subagents.LayerOneAsset(), "visual-certification")
	if err != nil {
		t.Fatalf("CutSection(visual-certification): %v", err)
	}
	if len(visualText) < 200 {
		t.Fatalf("visual-certification section is only %d bytes, want >= 200 — guardian would be blind", len(visualText))
	}

	t.Run("assets/agents", func(t *testing.T) {
		destDir := t.TempDir()
		files, err := filesFromEmbed(builtinAgents, "assets/agents", destDir)
		if err != nil {
			t.Fatalf("filesFromEmbed returned error: %v", err)
		}
		for _, f := range files {
			name := filepath.Base(f.Path)
			role := strings.TrimSuffix(name, ".md")
			assertVisualSectionPresence(t, name, role, string(f.Content), visualText)
		}
	})

	t.Run("assets/profiles/default/agents", func(t *testing.T) {
		fsys := DefaultProfileFS()
		entries, err := fs.ReadDir(fsys, "agents")
		if err != nil {
			t.Fatalf("read default profile agents dir: %v", err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			content, err := fs.ReadFile(fsys, "agents/"+entry.Name())
			if err != nil {
				t.Fatalf("read default profile agent %s: %v", entry.Name(), err)
			}
			role := strings.TrimSuffix(entry.Name(), ".md")
			assertVisualSectionPresence(t, entry.Name(), role, string(content), visualText)
		}
	})
}

// assertVisualSectionPresence requires visualText to appear in text if and
// only if role is in visualCertificationCoveredRoles.
func assertVisualSectionPresence(t *testing.T, fileName, role, text, visualText string) {
	t.Helper()
	has := strings.Contains(text, visualText)
	want := visualCertificationCoveredRoles[role]
	if want && !has {
		t.Errorf("%s: role %q must carry the visual-certification text, but it is absent", fileName, role)
	}
	if !want && has {
		t.Errorf("%s: role %q must NOT carry the visual-certification text, but it is present", fileName, role)
	}
}
