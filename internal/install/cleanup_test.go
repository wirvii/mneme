package install

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRemoveInstalledBuiltinAgents_RemovesIdentical verifies that a profile
// file that is a byte-identical copy of the built-in asset is removed.
func TestRemoveInstalledBuiltinAgents_RemovesIdentical(t *testing.T) {
	dir := t.TempDir()

	asset, err := builtinAgents.ReadFile("assets/agents/backend.md")
	if err != nil {
		t.Fatalf("read asset: %v", err)
	}
	path := filepath.Join(dir, "backend.md")
	if err := os.WriteFile(path, asset, 0o644); err != nil {
		t.Fatalf("write installed copy: %v", err)
	}

	removed, err := RemoveInstalledBuiltinAgents(dir)
	if err != nil {
		t.Fatalf("RemoveInstalledBuiltinAgents error: %v", err)
	}

	if !containsName(removed, "backend") {
		t.Errorf("expected \"backend\" in removed, got %v", removed)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("expected %s to be removed, stat err = %v", path, statErr)
	}
}

// TestRemoveInstalledBuiltinAgents_ModelOverrideNormalized verifies that a
// profile whose only difference from the asset is a `model:` line changed by
// `mneme model set` (i.e. produced via SetModelInFrontmatter) is still
// treated as unmodified and removed — the comparison normalises the model
// line on both sides before comparing (D2).
func TestRemoveInstalledBuiltinAgents_ModelOverrideNormalized(t *testing.T) {
	dir := t.TempDir()

	asset, err := builtinAgents.ReadFile("assets/agents/backend.md")
	if err != nil {
		t.Fatalf("read asset: %v", err)
	}
	overridden, err := SetModelInFrontmatter(asset, "haiku")
	if err != nil {
		t.Fatalf("SetModelInFrontmatter: %v", err)
	}
	path := filepath.Join(dir, "backend.md")
	if err := os.WriteFile(path, overridden, 0o644); err != nil {
		t.Fatalf("write overridden copy: %v", err)
	}

	removed, err := RemoveInstalledBuiltinAgents(dir)
	if err != nil {
		t.Fatalf("RemoveInstalledBuiltinAgents error: %v", err)
	}

	if !containsName(removed, "backend") {
		t.Errorf("expected \"backend\" in removed (model-only diff), got %v", removed)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("expected %s to be removed, stat err = %v", path, statErr)
	}
}

// TestRemoveInstalledBuiltinAgents_KeepsCustomized verifies that a profile
// whose body was hand-edited (not just the model line) is left intact — the
// fail-safe rule never deletes a customisation.
func TestRemoveInstalledBuiltinAgents_KeepsCustomized(t *testing.T) {
	dir := t.TempDir()

	asset, err := builtinAgents.ReadFile("assets/agents/backend.md")
	if err != nil {
		t.Fatalf("read asset: %v", err)
	}
	customized := append([]byte{}, asset...)
	customized = append(customized, []byte("\n\n## Custom section added by the user\n")...)
	path := filepath.Join(dir, "backend.md")
	if err := os.WriteFile(path, customized, 0o644); err != nil {
		t.Fatalf("write customized copy: %v", err)
	}

	removed, err := RemoveInstalledBuiltinAgents(dir)
	if err != nil {
		t.Fatalf("RemoveInstalledBuiltinAgents error: %v", err)
	}

	if containsName(removed, "backend") {
		t.Errorf("customized profile must not be removed, got removed=%v", removed)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("expected %s to still exist, stat err = %v", path, statErr)
	}
}

// TestRemoveInstalledBuiltinAgents_IdempotentMissing verifies that a missing
// agentsDir is a no-op (removed empty, err nil), and that running twice in a
// row over an already-cleaned directory stays a no-op.
func TestRemoveInstalledBuiltinAgents_IdempotentMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")

	removed, err := RemoveInstalledBuiltinAgents(dir)
	if err != nil {
		t.Fatalf("first run: unexpected error: %v", err)
	}
	if len(removed) != 0 {
		t.Errorf("first run: expected no removed entries, got %v", removed)
	}

	removed, err = RemoveInstalledBuiltinAgents(dir)
	if err != nil {
		t.Fatalf("second run: unexpected error: %v", err)
	}
	if len(removed) != 0 {
		t.Errorf("second run: expected no removed entries, got %v", removed)
	}
}

// TestRemoveInstalledBuiltinAgents_ReportsRemoved verifies that removed lists
// exactly the bundled agent names whose installed profile was an unmodified
// copy, and that a second run over the now-cleaned directory is a no-op.
func TestRemoveInstalledBuiltinAgents_ReportsRemoved(t *testing.T) {
	dir := t.TempDir()

	names, err := BundledAgentNames()
	if err != nil {
		t.Fatalf("BundledAgentNames: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("BundledAgentNames returned no names")
	}

	for _, name := range names {
		asset, err := builtinAgents.ReadFile("assets/agents/" + name + ".md")
		if err != nil {
			t.Fatalf("read asset %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".md"), asset, 0o644); err != nil {
			t.Fatalf("write installed copy %s: %v", name, err)
		}
	}

	removed, err := RemoveInstalledBuiltinAgents(dir)
	if err != nil {
		t.Fatalf("RemoveInstalledBuiltinAgents error: %v", err)
	}
	if len(removed) != len(names) {
		t.Fatalf("expected %d removed entries, got %d: %v", len(names), len(removed), removed)
	}
	for _, name := range names {
		if !containsName(removed, name) {
			t.Errorf("expected %q in removed, got %v", name, removed)
		}
	}

	// Idempotent: nothing left to remove on a second pass.
	removedAgain, err := RemoveInstalledBuiltinAgents(dir)
	if err != nil {
		t.Fatalf("second run: unexpected error: %v", err)
	}
	if len(removedAgain) != 0 {
		t.Errorf("second run: expected no removed entries, got %v", removedAgain)
	}
}

// containsName reports whether names contains target.
func containsName(names []string, target string) bool {
	for _, n := range names {
		if n == target {
			return true
		}
	}
	return false
}
