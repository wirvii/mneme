package config

import (
	"os"
	"path/filepath"
	"testing"

	tomlPkg "github.com/pelletier/go-toml/v2"
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

// --- SPEC-086 D6/D7: SetSubagentContainmentMode -----------------------------

func TestSetSubagentContainmentMode_WriteAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := SetSubagentContainmentMode(path, "wirvii/wirvii360r", "block"); err != nil {
		t.Fatalf("SetSubagentContainmentMode: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.SubagentContainmentMode("wirvii/wirvii360r"); got != "block" {
		t.Errorf("SubagentContainmentMode = %q, want %q", got, "block")
	}
	// A different, un-promoted project stays on the default.
	if got := cfg.SubagentContainmentMode("wirvii/other"); got != "warn" {
		t.Errorf("SubagentContainmentMode(other) = %q, want %q", got, "warn")
	}
}

// TestSetSubagentContainmentMode_RejectsInvalidMode is the mutation-tested
// guard: an invalid mode string must be rejected before anything is written
// to disk. Deleting the validateContainmentMode call would let a typo like
// "blocked" silently persist and then silently fail Validate() on the next
// Load, or worse, resolve to "" and drift to the default.
func TestSetSubagentContainmentMode_RejectsInvalidMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := SetSubagentContainmentMode(path, "wirvii/wirvii360r", "blocked"); err == nil {
		t.Fatal("SetSubagentContainmentMode: want error for invalid mode, got nil")
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("SetSubagentContainmentMode: want no file written on validation failure")
	}
}

// TestSetSubagentContainmentMode_PreservesOtherSections mirrors
// TestSetModelsOverrides_SurvivesReload: writing a containment override must
// not disturb an unrelated pre-existing section.
func TestSetSubagentContainmentMode_PreservesOtherSections(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	initial := `[search]
default_limit = 20
`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatalf("write initial config: %v", err)
	}

	if err := SetSubagentContainmentMode(path, "wirvii/mneme", "block"); err != nil {
		t.Fatalf("SetSubagentContainmentMode: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Search.DefaultLimit != 20 {
		t.Errorf("Search.DefaultLimit = %d, want 20 (unrelated section must survive)", cfg.Search.DefaultLimit)
	}
}

// --- SPEC-093 §3.4: SetProfilesDefault ---------------------------------------

func TestSetProfilesDefault_WriteAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := SetProfilesDefault(path, "chatea-pro"); err != nil {
		t.Fatalf("SetProfilesDefault: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Profiles.Default != "chatea-pro" {
		t.Errorf("Profiles.Default = %q, want %q", cfg.Profiles.Default, "chatea-pro")
	}
}

func TestSetProfilesDefault_EmptyClears(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := SetProfilesDefault(path, "chatea-pro"); err != nil {
		t.Fatalf("first SetProfilesDefault: %v", err)
	}
	if err := SetProfilesDefault(path, ""); err != nil {
		t.Fatalf("second SetProfilesDefault (clear): %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Profiles.Default != "" {
		t.Errorf("Profiles.Default = %q, want empty after clear", cfg.Profiles.Default)
	}
}

// TestSetProfilesDefault_PreservesOtherSections mirrors
// TestSetModelsOverrides_SurvivesReload / TestSetSubagentContainmentMode_PreservesOtherSections:
// writing the default profile must not disturb an unrelated pre-existing
// section, nor a pre-existing [models.overrides] map.
func TestSetProfilesDefault_PreservesOtherSections(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := SetModelsOverrides(path, map[string]string{"bug-hunter": "opus"}); err != nil {
		t.Fatalf("SetModelsOverrides: %v", err)
	}

	seededCfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load seeded config: %v", err)
	}
	seededCfg.Search.DefaultLimit = 20
	seededOut, err := tomlPkg.Marshal(seededCfg)
	if err != nil {
		t.Fatalf("marshal seeded config: %v", err)
	}
	if err := os.WriteFile(path, seededOut, 0o644); err != nil {
		t.Fatalf("write seeded config: %v", err)
	}

	if err := SetProfilesDefault(path, "chatea-pro"); err != nil {
		t.Fatalf("SetProfilesDefault: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Profiles.Default != "chatea-pro" {
		t.Errorf("Profiles.Default = %q, want %q", cfg.Profiles.Default, "chatea-pro")
	}
	if got := cfg.Models.Overrides["bug-hunter"]; got != "opus" {
		t.Errorf("Models.Overrides[bug-hunter] = %q, want %q (unrelated section must survive)", got, "opus")
	}
	if cfg.Search.DefaultLimit != 20 {
		t.Errorf("Search.DefaultLimit = %d, want 20 (unrelated section must survive)", cfg.Search.DefaultLimit)
	}
}
