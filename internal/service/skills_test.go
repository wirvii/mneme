package service_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/service"
)

// writeMinimalSkill creates a minimal skill directory in skillsDir for testing.
// If pinned is true, sets pinned:true in the frontmatter.
func writeMinimalSkill(t *testing.T, skillsDir, name string, pinned bool) {
	t.Helper()
	d := filepath.Join(skillsDir, name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	pinnedStr := "false"
	if pinned {
		pinnedStr = "true"
	}
	md := "---\n" +
		"name: " + name + "\n" +
		"description: \"Valid description longer than 20 characters.\"\n" +
		"version: 0.1.0\n" +
		"pinned: " + pinnedStr + "\n" +
		"---\n\n" +
		conformantBody(name)
	if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
}

// conformantBody returns a minimal 5-section body for a skill.
func conformantBody(name string) string {
	return "## When to Use\n\nUse " + name + " when testing.\n\n" +
		"## Critical Rules\n\n1. Rule one.\n\n" +
		"## Automated Checks\n\n| Check | What it verifies | How to fix |\n|---|---|---|\n| a | b | c |\n\n" +
		"## Verification\n\nRun the script.\n\n" +
		"## Workflow\n\n1. Step one.\n"
}

func TestSkillsService_List(t *testing.T) {
	dir := t.TempDir()
	svc := service.NewSkillsService(dir)

	// Install the bundled example-skill first so we have something installed.
	if err := svc.Install("example-skill", false); err != nil {
		t.Fatalf("Install example-skill: %v", err)
	}

	infos, err := svc.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) == 0 {
		t.Fatal("expected at least one skill in list")
	}

	found := false
	for _, i := range infos {
		if i.Name == "example-skill" {
			found = true
			if !i.Installed {
				t.Error("example-skill should be Installed=true after install")
			}
			if !i.Bundled {
				t.Error("example-skill should be Bundled=true")
			}
			break
		}
	}
	if !found {
		t.Error("example-skill not found in List output")
	}
}

func TestSkillsService_Install_NotFound(t *testing.T) {
	dir := t.TempDir()
	svc := service.NewSkillsService(dir)

	err := svc.Install("nonexistent-skill", false)
	if !errors.Is(err, model.ErrSkillNotFound) {
		t.Errorf("expected ErrSkillNotFound, got %v", err)
	}
}

func TestSkillsService_Install_Pinned(t *testing.T) {
	dir := t.TempDir()
	svc := service.NewSkillsService(dir)

	// Install first.
	if err := svc.Install("example-skill", false); err != nil {
		t.Fatalf("first Install: %v", err)
	}
	// Pin it.
	if err := svc.Pin("example-skill"); err != nil {
		t.Fatalf("Pin: %v", err)
	}

	// Try to reinstall without force — should get ErrSkillPinned.
	err := svc.Install("example-skill", false)
	if !errors.Is(err, model.ErrSkillPinned) {
		t.Errorf("expected ErrSkillPinned, got %v", err)
	}

	// With force — should succeed.
	if err := svc.Install("example-skill", true); err != nil {
		t.Errorf("Install with force should succeed: %v", err)
	}
}

func TestSkillsService_PinUnpin(t *testing.T) {
	dir := t.TempDir()
	svc := service.NewSkillsService(dir)

	if err := svc.Install("example-skill", false); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if err := svc.Pin("example-skill"); err != nil {
		t.Fatalf("Pin: %v", err)
	}

	infos, _ := svc.List()
	for _, i := range infos {
		if i.Name == "example-skill" && !i.Pinned {
			t.Error("expected example-skill to be pinned")
		}
	}

	if err := svc.Unpin("example-skill"); err != nil {
		t.Fatalf("Unpin: %v", err)
	}

	infos, _ = svc.List()
	for _, i := range infos {
		if i.Name == "example-skill" && i.Pinned {
			t.Error("expected example-skill to be unpinned")
		}
	}
}

func TestSkillsService_Pin_NotFound(t *testing.T) {
	dir := t.TempDir()
	svc := service.NewSkillsService(dir)

	err := svc.Pin("ghost")
	if !errors.Is(err, model.ErrSkillNotFound) {
		t.Errorf("expected ErrSkillNotFound, got %v", err)
	}
}

func TestSkillsService_Remove(t *testing.T) {
	dir := t.TempDir()
	svc := service.NewSkillsService(dir)

	writeMinimalSkill(t, dir, "my-skill", false)

	if err := svc.Remove("my-skill", false); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	// Should be gone.
	if _, err := os.Stat(filepath.Join(dir, "my-skill")); !os.IsNotExist(err) {
		t.Error("expected skill dir to be removed")
	}
}

func TestSkillsService_Remove_Pinned(t *testing.T) {
	dir := t.TempDir()
	svc := service.NewSkillsService(dir)

	writeMinimalSkill(t, dir, "pinned-skill", true)

	err := svc.Remove("pinned-skill", false)
	if !errors.Is(err, model.ErrSkillPinned) {
		t.Errorf("expected ErrSkillPinned, got %v", err)
	}

	// With force: should succeed.
	if err := svc.Remove("pinned-skill", true); err != nil {
		t.Errorf("Remove with force should succeed: %v", err)
	}
}

func TestSkillsService_Remove_NotFound(t *testing.T) {
	dir := t.TempDir()
	svc := service.NewSkillsService(dir)

	err := svc.Remove("ghost", false)
	if !errors.Is(err, model.ErrSkillNotFound) {
		t.Errorf("expected ErrSkillNotFound, got %v", err)
	}
}

func TestSkillsService_Lint(t *testing.T) {
	dir := t.TempDir()
	svc := service.NewSkillsService(dir)

	if err := svc.Install("example-skill", false); err != nil {
		t.Fatalf("Install: %v", err)
	}

	results, err := svc.Lint("example-skill")
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Passed {
		t.Errorf("expected example-skill to pass lint, errors: %v", results[0].Errors)
	}
}

func TestSkillsService_Lint_NotFound(t *testing.T) {
	dir := t.TempDir()
	svc := service.NewSkillsService(dir)

	_, err := svc.Lint("ghost")
	if !errors.Is(err, model.ErrSkillNotFound) {
		t.Errorf("expected ErrSkillNotFound, got %v", err)
	}
}

func TestSkillsService_Validate(t *testing.T) {
	dir := t.TempDir()
	svc := service.NewSkillsService(dir)

	if err := svc.Install("example-skill", false); err != nil {
		t.Fatalf("Install: %v", err)
	}

	result, err := svc.Validate(context.Background(), "example-skill")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !result.Passed {
		t.Errorf("expected example-skill validation to pass; output: %s", result.Output)
	}
}

func TestSkillsService_Validate_NotFound(t *testing.T) {
	dir := t.TempDir()
	svc := service.NewSkillsService(dir)

	_, err := svc.Validate(context.Background(), "ghost")
	if !errors.Is(err, model.ErrSkillNotFound) {
		t.Errorf("expected ErrSkillNotFound, got %v", err)
	}
}

func TestSkillsService_Validate_NoValidation(t *testing.T) {
	dir := t.TempDir()
	svc := service.NewSkillsService(dir)

	// Create a skill without validation/run.sh.
	writeMinimalSkill(t, dir, "no-validator", false)

	_, err := svc.Validate(context.Background(), "no-validator")
	if !errors.Is(err, model.ErrSkillNoValidation) {
		t.Errorf("expected ErrSkillNoValidation, got %v", err)
	}
}
