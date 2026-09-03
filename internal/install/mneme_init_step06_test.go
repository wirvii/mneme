package install_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// findMnemeInitSkillCopies walks internal/install/assets (this test file's
// own directory's assets subtree — located via runtime.Caller, the same
// technique internal/sddfile/leaf_test.go and internal/quality/leaf_test.go
// already use for their own perimeter guardians) and returns every path
// whose suffix is "skills/mneme-init/SKILL.md" — SPEC-140 AC16's literal
// "internal/install/assets/**/skills/mneme-init/SKILL.md" pattern, DERIVED
// by walking the real tree rather than listing the two known paths by
// hand (Forma 4 of the dead-criteria catalog): the day a third copy
// appears under a new profile, this test covers it with no edit.
func findMnemeInitSkillCopies(t *testing.T) []string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not resolve this test file's own source path")
	}
	assetsDir := filepath.Join(filepath.Dir(thisFile), "assets")

	var found []string
	err := filepath.WalkDir(assetsDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(filepath.ToSlash(path), "skills/mneme-init/SKILL.md") {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", assetsDir, err)
	}
	if len(found) < 2 {
		t.Fatalf("expected at least 2 copies of mneme-init/SKILL.md under %s, found %d: %v", assetsDir, len(found), found)
	}
	return found
}

// TestMnemeInitSkill_BothCopiesCarryStep06 is SPEC-140 AC16: every copy the
// walk above finds must (a) contain the Step 0.6 anchor and (b) declare the
// SAME version — never listing the two known paths by hand.
func TestMnemeInitSkill_BothCopiesCarryStep06(t *testing.T) {
	copies := findMnemeInitSkillCopies(t)

	var firstVersion, firstPath string
	for _, path := range copies {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		content := string(data)

		if !strings.Contains(content, "Step 0.6") {
			t.Errorf("%s: missing the Step 0.6 (diagnose before offering) anchor", path)
		}

		version := extractSkillVersion(t, path, content)
		if firstVersion == "" {
			firstVersion, firstPath = version, path
			continue
		}
		if version != firstVersion {
			t.Errorf("%s declares version %q, but %s declares %q — the two copies have drifted apart",
				path, version, firstPath, firstVersion)
		}
	}
}

// extractSkillVersion pulls the "version: X.Y.Z" frontmatter line out of a
// SKILL.md's content.
func extractSkillVersion(t *testing.T, path, content string) string {
	t.Helper()
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "version:") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "version:"))
		}
	}
	t.Fatalf("%s: no version: line found in frontmatter", path)
	return ""
}
