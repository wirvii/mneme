package install_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/install"
	"github.com/wirvii/mneme/internal/subagents"
)

// TestMnemeInitSkill_DocumentsEveryLayer23ForbiddenToken is the repo-dev-time
// half of SPEC-090 D4/G5's doc<->mechanism coupling: unlike
// validation/run.sh's runtime check (a static, hand-maintained token list),
// this test reads subagents.Layer23ForbiddenLifecycleTokens directly — the
// SAME slice ScanLayer23Leaks scans against — so it can never silently drift
// out of sync with the detector. Adding a token to that slice without also
// adding it to SKILL.md's prose fails THIS test, at build/CI time, before
// validation/run.sh's separately-maintained list would even be exercised.
func TestMnemeInitSkill_DocumentsEveryLayer23ForbiddenToken(t *testing.T) {
	entries, err := install.BundledSkillEntries()
	if err != nil {
		t.Fatalf("BundledSkillEntries: %v", err)
	}

	var skillContent string
	for _, e := range entries {
		if filepath.ToSlash(e.RelPath) == "mneme-init/SKILL.md" {
			skillContent = string(e.Content)
			break
		}
	}
	if skillContent == "" {
		t.Fatal("mneme-init/SKILL.md not found among bundled entries")
	}

	for _, token := range subagents.Layer23ForbiddenLifecycleTokens {
		if !strings.Contains(skillContent, token) {
			t.Errorf("mneme-init/SKILL.md does not document the layer 2/3-forbidden token %q (subagents.Layer23ForbiddenLifecycleTokens)", token)
		}
	}

	// The other class of leak ScanLayer23Leaks hunts (SPEC-090 D1): the
	// capability keys. Layer23ForbiddenLifecycleTokens only enumerates the
	// lifecycle tokens, so these two are asserted directly rather than
	// looped.
	if !strings.Contains(skillContent, "tools:") {
		t.Error("mneme-init/SKILL.md does not document the tools: capability-key prohibition")
	}
	if !strings.Contains(skillContent, "permissionMode:") {
		t.Error("mneme-init/SKILL.md does not document the permissionMode: capability-key prohibition")
	}
}
