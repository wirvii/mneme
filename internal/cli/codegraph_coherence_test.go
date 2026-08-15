package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/install"
	"github.com/wirvii/mneme/internal/subagents"
)

// TestCodegraphVocabularyCoherence verifies the "code graph first" policy speaks
// the same mandatory vocabulary across all three surfaces (SPEC-083 AC7): the
// pre-tool-use nudge, the operating manuals (orchestrator + Codex), and the
// subagent policy asset. All three must reference the code graph tools and use
// imperative, mandatory-tone language — so an agent hears one consistent message.
func TestCodegraphVocabularyCoherence(t *testing.T) {
	// Nudge (English): mandatory tone + tool reference.
	var nudge bytes.Buffer
	renderCodegraphNudge(&nudge, false, 0)
	assertContainsAll(t, "nudge", nudge.String(), "MANDATORY", "FIRST", "codegraph_search")

	// Orchestrator operating manual (English).
	assertContainsAll(t, "operating-manual", install.OperatingManual(), "MANDATORY", "codegraph_search", "codegraph adoption")

	// Codex operating manual (English, project-role runtime).
	assertContainsAll(t, "operating-manual-codex", install.OperatingManualCodex(), "MANDATORY", "codegraph_search")

	// Subagent policy asset (Spanish): mandatory tone + tool reference.
	assertContainsAll(t, "agent-fixed", subagents.LayerOneAsset(), "OBLIGATORIO", "codegraph_search")
}

func assertContainsAll(t *testing.T, surface, content string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(content, w) {
			t.Errorf("%s: missing shared vocabulary %q", surface, w)
		}
	}
}
