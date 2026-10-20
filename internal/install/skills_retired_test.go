package install_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/wirvii/mneme/internal/install"
)

// seedSkillDir writes <root>/<dir>/SKILL.md declaring the given name and pin
// state, the way a previous mneme install (or a user) would have left it.
func seedSkillDir(t *testing.T, root, dir, name string, pinned bool) {
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

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// TestWriteSkills_RemovesRetiredSkills covers SPEC-162 C8: retired skills are
// deleted from the discovery directory unless pinned (force overrides), a
// foreign skill is never touched, and a directory that only shares a retired
// name is kept.
func TestWriteSkills_RemovesRetiredSkills(t *testing.T) {
	tests := []struct {
		name        string
		force       bool
		wantGone    []string
		wantKept    []string
		wantRemoved []string
	}{
		{
			name:        "without force keeps the pinned retired skill",
			wantGone:    []string{"new-app", "mneme-profile-author"},
			wantKept:    []string{"new-project", "my-skill", "new-project-lookalike"},
			wantRemoved: []string{"mneme-profile-author", "new-app"},
		},
		{
			name:        "force removes the pinned retired skill too",
			force:       true,
			wantGone:    []string{"new-app", "mneme-profile-author", "new-project"},
			wantKept:    []string{"my-skill", "new-project-lookalike"},
			wantRemoved: []string{"mneme-profile-author", "new-app", "new-project"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			seedSkillDir(t, dir, "new-app", "new-app", false)
			seedSkillDir(t, dir, "mneme-profile-author", "mneme-profile-author", false)
			seedSkillDir(t, dir, "new-project", "new-project", true)
			seedSkillDir(t, dir, "my-skill", "my-skill", false)
			// A directory whose SKILL.md declares a different name is not
			// mneme's retired skill, whatever the folder is called.
			seedSkillDir(t, dir, "new-project-lookalike", "something-else", false)

			result, err := install.WriteSkills(fakeAgent(nil), dir, tt.force)
			if err != nil {
				t.Fatalf("WriteSkills: %v", err)
			}

			got := slices.Clone(result.Removed)
			slices.Sort(got)
			if !slices.Equal(got, tt.wantRemoved) {
				t.Errorf("Removed = %v, want %v", got, tt.wantRemoved)
			}
			for _, n := range tt.wantGone {
				if exists(filepath.Join(dir, n)) {
					t.Errorf("%s should have been removed", n)
				}
			}
			for _, n := range tt.wantKept {
				if !exists(filepath.Join(dir, n, "SKILL.md")) {
					t.Errorf("%s should have been kept", n)
				}
			}
		})
	}
}

// TestInstall_RemovesRetiredSkillsPerRuntime covers the "applies to the Claude
// Code path and to the Codex path" half of SPEC-162 C8 through the real agents.
func TestInstall_RemovesRetiredSkillsPerRuntime(t *testing.T) {
	const binPath = "/usr/local/bin/mneme"
	tests := []struct {
		name      string
		agent     func() *install.Agent
		skillsRel string
	}{
		{"claude-code", func() *install.Agent { return install.ClaudeCode(binPath) }, filepath.Join(".claude", "skills")},
		{"codex", func() *install.Agent { return install.Codex(binPath) }, filepath.Join(".agents", "skills")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			skillsDir := filepath.Join(home, tt.skillsRel)
			seedSkillDir(t, skillsDir, "new-app", "new-app", false)
			seedSkillDir(t, skillsDir, "my-skill", "my-skill", false)

			if err := install.Install(tt.agent(), binPath); err != nil {
				t.Fatalf("Install: %v", err)
			}

			if exists(filepath.Join(skillsDir, "new-app")) {
				t.Error("new-app should have been removed by the install")
			}
			if !exists(filepath.Join(skillsDir, "my-skill", "SKILL.md")) {
				t.Error("my-skill should have been kept")
			}
		})
	}
}
