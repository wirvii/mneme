package install

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// writeRetiredSkill leaves <root>/<dir>/SKILL.md declaring name and pin
// state, the way an earlier mneme install (or a person) would have.
func writeRetiredSkill(t *testing.T, root, dir, name string, pinned bool) {
	t.Helper()
	pin := "false"
	if pinned {
		pin = "true"
	}
	body := "---\nname: " + name + "\ndescription: \"d\"\nversion: 1.0.0\npinned: " + pin + "\n---\n"
	if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// markedCommandContent returns a real mneme-written command (it carries the
// managed-block marker) by reading a command mneme still ships.
func markedCommandContent(t *testing.T) []byte {
	t.Helper()
	data, err := builtinCommands.ReadFile("assets/commands/grill-me.md")
	if err != nil {
		t.Fatalf("read embedded grill-me command: %v", err)
	}
	return data
}

// TestInstall_RetiresMnemeOwnedFlowLeftovers is SPEC-169 C4: what mneme itself
// installed for hunt-bug, bug-to-issue and example-skill is deleted from both
// skill discovery directories and from the Claude Code commands directory.
func TestInstall_RetiresMnemeOwnedFlowLeftovers(t *testing.T) {
	legacyHunt, err := os.ReadFile("testdata/legacy-commands/hunt-bug.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, runtime := range []struct{ name, skillsRel string }{
		{"claude-code skills", filepath.Join(".claude", "skills")},
		{"codex skills", filepath.Join(".agents", "skills")},
	} {
		t.Run(runtime.name, func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, runtime.skillsRel)
			for _, n := range []string{"hunt-bug", "bug-to-issue", "example-skill"} {
				writeRetiredSkill(t, dir, n, n, false)
			}
			result, err := WriteSkills(&Agent{Skills: BundledSkillEntries}, dir, false)
			if err != nil {
				t.Fatalf("WriteSkills: %v", err)
			}
			got := slices.Clone(result.Removed)
			slices.Sort(got)
			if want := []string{"bug-to-issue", "example-skill", "hunt-bug"}; !slices.Equal(got, want) {
				t.Errorf("Removed = %v, want %v", got, want)
			}
			for _, n := range []string{"hunt-bug", "bug-to-issue", "example-skill"} {
				if fileExists(filepath.Join(dir, n)) {
					t.Errorf("%s should have been removed", n)
				}
			}
		})
	}

	t.Run("claude-code commands", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), ".claude", "commands")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		// One carries the managed-block marker, the other matches a known
		// legacy digest: both are recognised as mneme's own.
		if err := os.WriteFile(filepath.Join(dir, "hunt-bug.md"), legacyHunt, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "bug-to-issue.md"), markedCommandContent(t), 0o644); err != nil {
			t.Fatal(err)
		}
		agent := &Agent{Commands: func() ([]CommandFile, error) {
			return []CommandFile{{Path: filepath.Join(dir, "grill-me.md"), Content: markedCommandContent(t)}}, nil
		}}
		result, err := WriteCommands(agent, false)
		if err != nil {
			t.Fatalf("WriteCommands: %v", err)
		}
		got := slices.Clone(result.Removed)
		slices.Sort(got)
		if want := []string{"bug-to-issue.md", "hunt-bug.md"}; !slices.Equal(got, want) {
			t.Errorf("Removed = %v, want %v", got, want)
		}
		for _, n := range []string{"hunt-bug.md", "bug-to-issue.md"} {
			if fileExists(filepath.Join(dir, n)) {
				t.Errorf("%s should have been removed", n)
			}
		}
	})
}

// TestInstall_KeepsForeignOrPinnedRetiredArtifacts is SPEC-169 C4's other
// half: a retired name that a person owns (foreign command, pinned skill, a
// skill whose SKILL.md declares another name) survives and is reported.
func TestInstall_KeepsForeignOrPinnedRetiredArtifacts(t *testing.T) {
	t.Run("skills", func(t *testing.T) {
		dir := t.TempDir()
		writeRetiredSkill(t, dir, "hunt-bug", "hunt-bug", true)            // pinned: keep
		writeRetiredSkill(t, dir, "bug-to-issue", "my-own-thing", false)   // other name: keep
		writeRetiredSkill(t, dir, "example-skill", "example-skill", false) // plain: remove

		result, err := WriteSkills(&Agent{Skills: BundledSkillEntries}, dir, false)
		if err != nil {
			t.Fatalf("WriteSkills: %v", err)
		}
		if !fileExists(filepath.Join(dir, "hunt-bug", "SKILL.md")) {
			t.Error("pinned retired skill must be kept")
		}
		if !fileExists(filepath.Join(dir, "bug-to-issue", "SKILL.md")) {
			t.Error("skill declaring another name must be kept")
		}
		if fileExists(filepath.Join(dir, "example-skill")) {
			t.Error("unpinned skill with its own name must be removed")
		}
		if want := []string{"example-skill"}; !slices.Equal(result.Removed, want) {
			t.Errorf("Removed = %v, want %v", result.Removed, want)
		}
	})

	t.Run("commands", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "commands")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		own := []byte("# my own /hunt-bug shortcut, written by hand\n")
		if err := os.WriteFile(filepath.Join(dir, "hunt-bug.md"), own, 0o644); err != nil {
			t.Fatal(err)
		}
		agent := &Agent{Commands: func() ([]CommandFile, error) {
			return []CommandFile{{Path: filepath.Join(dir, "grill-me.md"), Content: markedCommandContent(t)}}, nil
		}}
		result, err := WriteCommands(agent, false)
		if err != nil {
			t.Fatalf("WriteCommands: %v", err)
		}
		got, err := os.ReadFile(filepath.Join(dir, "hunt-bug.md"))
		if err != nil || string(got) != string(own) {
			t.Errorf("foreign retired command was touched: %q, %v", got, err)
		}
		if want := []string{"hunt-bug.md"}; !slices.Equal(result.KeptRetired, want) {
			t.Errorf("KeptRetired = %v, want %v", result.KeptRetired, want)
		}
		if len(result.Removed) != 0 {
			t.Errorf("Removed = %v, want none", result.Removed)
		}
	})
}
