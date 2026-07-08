package install_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/juanftp/mneme/internal/install"
	"github.com/juanftp/mneme/internal/skill"
)

// fakeSkillEntry builds a minimal SkillEntry for testing.
func fakeEntry(relPath string, content []byte, isExec bool) install.SkillEntry {
	return install.SkillEntry{
		RelPath:      relPath,
		Content:      content,
		IsExecutable: isExec,
	}
}

// fakeAgent builds an Agent with a Skills func returning the given entries.
func fakeAgent(entries []install.SkillEntry) *install.Agent {
	return &install.Agent{
		Name: "test",
		Slug: "test",
		Skills: func() ([]install.SkillEntry, error) {
			return entries, nil
		},
	}
}

func TestWriteSkills_SubdirsAndExecBits(t *testing.T) {
	dir := t.TempDir()

	agent := fakeAgent([]install.SkillEntry{
		fakeEntry(filepath.Join("my-skill", "SKILL.md"), []byte("---\nname: my-skill\n---\n"), false),
		fakeEntry(filepath.Join("my-skill", "validation", "run.sh"), []byte("#!/bin/sh\nexit 0\n"), true),
	})

	result, err := install.WriteSkills(agent, dir, false)
	if err != nil {
		t.Fatalf("WriteSkills: %v", err)
	}

	if len(result.Installed) != 1 || result.Installed[0] != "my-skill" {
		t.Errorf("Installed = %v, want [my-skill]", result.Installed)
	}

	// Verify subdirectory was created.
	valScript := filepath.Join(dir, "my-skill", "validation", "run.sh")
	info, err := os.Stat(valScript)
	if err != nil {
		t.Fatalf("stat run.sh: %v", err)
	}

	// Verify executable bit.
	if info.Mode()&0o100 == 0 {
		t.Errorf("run.sh should have executable bit set, mode=%o", info.Mode())
	}
}

func TestWriteSkills_IdempotentNotPinned(t *testing.T) {
	dir := t.TempDir()

	skillMD := []byte("---\nname: my-skill\ndescription: \"desc\"\nversion: 0.1.0\npinned: false\n---\n")
	agent := fakeAgent([]install.SkillEntry{
		fakeEntry(filepath.Join("my-skill", "SKILL.md"), skillMD, false),
	})

	// First install.
	if _, err := install.WriteSkills(agent, dir, false); err != nil {
		t.Fatalf("first WriteSkills: %v", err)
	}

	// Modify the installed file.
	target := filepath.Join(dir, "my-skill", "SKILL.md")
	if err := os.WriteFile(target, []byte("modified"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Second install (not pinned): bundled should overwrite.
	if _, err := install.WriteSkills(agent, dir, false); err != nil {
		t.Fatalf("second WriteSkills: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) == "modified" {
		t.Error("non-pinned skill should have been overwritten by second install")
	}
}

func TestWriteSkills_PinSkip(t *testing.T) {
	dir := t.TempDir()

	// Write a pinned SKILL.md to the destination first.
	skillDir := filepath.Join(dir, "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pinnedMD := []byte("---\nname: my-skill\ndescription: \"pinned local edit\"\nversion: 9.9.9\npinned: true\n---\n## When to Use\nlocal\n")
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), pinnedMD, 0o644); err != nil {
		t.Fatal(err)
	}

	// Try to install bundled version without force.
	bundled := []byte("---\nname: my-skill\ndescription: \"bundled\"\nversion: 1.0.0\npinned: false\n---\n")
	agent := fakeAgent([]install.SkillEntry{
		fakeEntry(filepath.Join("my-skill", "SKILL.md"), bundled, false),
	})

	result, err := install.WriteSkills(agent, dir, false)
	if err != nil {
		t.Fatalf("WriteSkills (no force, pinned): %v", err)
	}

	if len(result.Skipped) != 1 || result.Skipped[0] != "my-skill" {
		t.Errorf("Skipped = %v, want [my-skill]", result.Skipped)
	}

	// Installed file must NOT have been overwritten.
	got, _ := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	parsed, _ := skill.Parse(got)
	if parsed.Version != "9.9.9" {
		t.Errorf("pinned skill was overwritten; version = %q, want 9.9.9", parsed.Version)
	}
}

func TestWriteSkills_ForceOverridesPinned(t *testing.T) {
	dir := t.TempDir()

	skillDir := filepath.Join(dir, "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: my-skill\nversion: 9.9.9\npinned: true\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	bundled := []byte("---\nname: my-skill\ndescription: \"bundled\"\nversion: 2.0.0\npinned: false\n---\n")
	agent := fakeAgent([]install.SkillEntry{
		fakeEntry(filepath.Join("my-skill", "SKILL.md"), bundled, false),
	})

	result, err := install.WriteSkills(agent, dir, true)
	if err != nil {
		t.Fatalf("WriteSkills (force): %v", err)
	}

	if len(result.Installed) != 1 {
		t.Errorf("Installed = %v, want [my-skill]", result.Installed)
	}

	got, _ := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	parsed, _ := skill.Parse(got)
	if parsed.Version != "2.0.0" {
		t.Errorf("force should overwrite; version = %q, want 2.0.0", parsed.Version)
	}
}

func TestBundledSkillEntries(t *testing.T) {
	entries, err := install.BundledSkillEntries()
	if err != nil {
		t.Fatalf("BundledSkillEntries: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one bundled skill entry")
	}

	// example-skill/SKILL.md must be present.
	found := false
	for _, e := range entries {
		fwd := filepath.ToSlash(e.RelPath)
		if fwd == "example-skill/SKILL.md" {
			found = true
		}
		// validation/run.sh must be executable.
		if strings.Contains(fwd, "/validation/") {
			if !e.IsExecutable {
				t.Errorf("%s should be marked IsExecutable", e.RelPath)
			}
		}
	}
	if !found {
		t.Error("example-skill/SKILL.md not found in BundledSkillEntries")
	}
}

// TestBundledSkillEntries_IncludesMnemeInit verifies the mneme-init skill
// (SPEC-058 / EPIC agnostic-agents SS-5) ships as a bundled skill, replacing
// the former /mneme-init slash command.
func TestBundledSkillEntries_IncludesMnemeInit(t *testing.T) {
	entries, err := install.BundledSkillEntries()
	if err != nil {
		t.Fatalf("BundledSkillEntries: %v", err)
	}

	found := false
	for _, e := range entries {
		if filepath.ToSlash(e.RelPath) == "mneme-init/SKILL.md" {
			found = true
			break
		}
	}
	if !found {
		t.Error("mneme-init/SKILL.md not found in BundledSkillEntries")
	}
}

// TestBundledSkills_AllLintClean verifies every bundled skill's SKILL.md
// passes the structural linter directly from the embedded source, without
// requiring `mneme skills install` to run first (per constraint-no-local-install).
func TestBundledSkills_AllLintClean(t *testing.T) {
	names, err := install.BundledSkillNames()
	if err != nil {
		t.Fatalf("BundledSkillNames: %v", err)
	}
	entries, err := install.BundledSkillEntries()
	if err != nil {
		t.Fatalf("BundledSkillEntries: %v", err)
	}

	for _, name := range names {
		var content []byte
		for _, e := range entries {
			if filepath.ToSlash(e.RelPath) == name+"/SKILL.md" {
				content = e.Content
				break
			}
		}
		if content == nil {
			t.Fatalf("%s: SKILL.md not found among bundled entries", name)
		}

		parsed, err := skill.Parse(content)
		if err != nil {
			t.Fatalf("%s: parse SKILL.md: %v", name, err)
		}

		result := skill.Lint(parsed, name)
		if !result.Passed {
			for _, f := range result.Errors {
				t.Errorf("%s: lint error: %s", name, f.Message)
			}
			t.Fatalf("%s: expected lint to pass with zero errors", name)
		}
	}
}

// TestInstall_DeploysSkills verifies that Install writes skills to the skills
// directory. This is the regression test for the C1 bug where the CLI install
// command omitted the WriteSkills step (only Install() included it).
func TestInstall_DeploysSkills(t *testing.T) {
	// Install requires a real home-dir lookup for several steps; instead we
	// test the contract at the level that the CLI bug was located: that
	// WriteSkills is called for an agent that has a non-nil Skills function.
	agent := install.ClaudeCode("/usr/local/bin/mneme")
	if agent.Skills == nil {
		t.Fatal("ClaudeCode agent.Skills must not be nil — CLI install would skip skills")
	}

	// Verify that WriteSkills can actually deploy skills to a temp dir,
	// proving the path that Install() (and now the CLI) exercises works.
	dir := t.TempDir()
	result, err := install.WriteSkills(agent, dir, false)
	if err != nil {
		t.Fatalf("WriteSkills via ClaudeCode agent: %v", err)
	}
	if len(result.Installed) == 0 && len(result.Skipped) == 0 {
		t.Error("expected at least one skill to be installed or skipped")
	}
}

func TestBundledSkillNames(t *testing.T) {
	names, err := install.BundledSkillNames()
	if err != nil {
		t.Fatalf("BundledSkillNames: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("expected at least one bundled skill name")
	}
	found := false
	for _, n := range names {
		if n == "example-skill" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("example-skill not found in BundledSkillNames; got %v", names)
	}
}
