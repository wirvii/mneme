package install

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/managedblock"
)

// TestCommandAssets_AreThinWrappersOverBundledSkills is G1 (SPEC-141 §5.1):
// every file under assets/commands/ must be a thin wrapper over a bundled
// skill of the same name — its name (sans extension) is a real
// BundledSkillNames() entry, its body names that skill, its frontmatter
// `name:` equals the file's own basename, it carries commandBlockMarker
// (SPEC-141 §4-bis D16), and it stays at or under 25 non-empty lines. This
// closes the whole class BL-228 reported: a command asset can never again
// carry doctrine the second runtime cannot receive, because every command
// is now provably a wrapper around a skill that DOES travel to both.
func TestCommandAssets_AreThinWrappersOverBundledSkills(t *testing.T) {
	skillNames, err := BundledSkillNames()
	if err != nil {
		t.Fatalf("BundledSkillNames: %v", err)
	}
	skillSet := make(map[string]bool, len(skillNames))
	for _, n := range skillNames {
		skillSet[n] = true
	}

	entries, err := builtinCommands.ReadDir("assets/commands")
	if err != nil {
		t.Fatalf("read assets/commands: %v", err)
	}

	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))

		if !skillSet[name] {
			t.Errorf("command asset %q has no matching bundled skill %q", e.Name(), name)
			continue
		}

		data, err := builtinCommands.ReadFile("assets/commands/" + e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		text := string(data)

		if !strings.HasPrefix(text, "---\nname: "+name+"\n") {
			t.Errorf("%s: frontmatter must start with 'name: %s'", e.Name(), name)
		}
		if !strings.Contains(text, "**"+name+"**") {
			t.Errorf("%s: body does not name the %q skill it wraps", e.Name(), name)
		}
		if _, _, present := managedblock.ReadText(text, commandBlockMarker); !present {
			t.Errorf("%s: missing the managed-block marker for %q", e.Name(), commandBlockMarker)
		}

		nonEmpty := 0
		for _, line := range strings.Split(text, "\n") {
			if strings.TrimSpace(line) != "" {
				nonEmpty++
			}
		}
		if nonEmpty > 25 {
			t.Errorf("%s: %d non-empty lines, want <= 25 for a thin wrapper", e.Name(), nonEmpty)
		}
	}
}

// TestCommandAssets_AllInstalledByClaudeCode is G2 (SPEC-141 §5.1) — the
// exact regression BL-228 reported: the set of basenames
// ClaudeCode(bin).Commands() installs must equal, in BOTH directions, the
// set of files embedded under assets/commands/. An asset that exists but is
// not installed (BL-228's own defect) fails here, and so does the opposite
// mistake (installing something that is not a real asset).
func TestCommandAssets_AllInstalledByClaudeCode(t *testing.T) {
	entries, err := builtinCommands.ReadDir("assets/commands")
	if err != nil {
		t.Fatalf("read assets/commands: %v", err)
	}
	assetNames := make(map[string]bool, len(entries))
	for _, e := range entries {
		assetNames[e.Name()] = true
	}

	cmds, err := ClaudeCode("/usr/local/bin/mneme").Commands()
	if err != nil {
		t.Fatalf("ClaudeCode Commands: %v", err)
	}
	installedNames := make(map[string]bool, len(cmds))
	for _, c := range cmds {
		installedNames[filepath.Base(c.Path)] = true
	}

	for name := range assetNames {
		if !installedNames[name] {
			t.Errorf("asset %q is embedded under assets/commands/ but not installed by ClaudeCode().Commands()", name)
		}
	}
	for name := range installedNames {
		if !assetNames[name] {
			t.Errorf("ClaudeCode().Commands() installs %q, which is not an embedded asset", name)
		}
	}
}
