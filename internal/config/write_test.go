package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSetModelsOverrides_WriteAndReload verifies that SetModelsOverrides persists
// overrides to disk and that a subsequent Load() reads them back correctly.
func TestSetModelsOverrides_WriteAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	overrides := map[string]string{
		"bug-hunter": "opus",
		"backend":    "haiku",
	}

	if err := SetModelsOverrides(path, overrides); err != nil {
		t.Fatalf("SetModelsOverrides error: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load after write error: %v", err)
	}

	if got := cfg.Models.Overrides["bug-hunter"]; got != "opus" {
		t.Errorf("bug-hunter: got %q, want opus", got)
	}
	if got := cfg.Models.Overrides["backend"]; got != "haiku" {
		t.Errorf("backend: got %q, want haiku", got)
	}
}

// TestSetModelsOverrides_SurvivesReload verifies that writing overrides does
// not corrupt any other section of the config (simulated upgrade scenario).
func TestSetModelsOverrides_SurvivesReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	// Write an initial config with non-default search limit.
	initial := `[search]
default_limit = 20
`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatalf("write initial config: %v", err)
	}

	// Now write model overrides.
	overrides := map[string]string{"architect": "haiku"}
	if err := SetModelsOverrides(path, overrides); err != nil {
		t.Fatalf("SetModelsOverrides error: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	// Model override must be present.
	if got := cfg.Models.Overrides["architect"]; got != "haiku" {
		t.Errorf("architect: got %q, want haiku", got)
	}
	// Original search setting must survive.
	if cfg.Search.DefaultLimit != 20 {
		t.Errorf("search.default_limit: got %d, want 20", cfg.Search.DefaultLimit)
	}
}

// TestSetModelsOverrides_Merge verifies that a second call to SetModelsOverrides
// replaces the overrides map entirely (callers are responsible for reading first
// if they want to merge).
func TestSetModelsOverrides_Merge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := SetModelsOverrides(path, map[string]string{"bug-hunter": "opus"}); err != nil {
		t.Fatalf("first write error: %v", err)
	}
	if err := SetModelsOverrides(path, map[string]string{"frontend": "haiku"}); err != nil {
		t.Fatalf("second write error: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	// Second write replaced the entire overrides map.
	if _, ok := cfg.Models.Overrides["bug-hunter"]; ok {
		t.Error("bug-hunter should be gone after second write replaced the map")
	}
	if got := cfg.Models.Overrides["frontend"]; got != "haiku" {
		t.Errorf("frontend: got %q, want haiku", got)
	}
}

// TestSetModelsOverrides_EmptyOverrides verifies that writing an empty map
// clears all overrides without breaking the config.
func TestSetModelsOverrides_EmptyOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := SetModelsOverrides(path, map[string]string{"bug-hunter": "opus"}); err != nil {
		t.Fatalf("write error: %v", err)
	}
	if err := SetModelsOverrides(path, map[string]string{}); err != nil {
		t.Fatalf("clear error: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if len(cfg.Models.Overrides) != 0 {
		t.Errorf("expected empty overrides, got %v", cfg.Models.Overrides)
	}
}
