package install

import (
	"bytes"
	"io/fs"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/wirvii/mneme/internal/profile"
)

// TestDefaultProfileFS_RootedAtManifest verifies AC1: DefaultProfileFS
// returns a non-empty fs.FS with mneme-profile.toml at its root (not nested
// under "assets/profiles/default/...") and the 6 built-in agents present.
func TestDefaultProfileFS_RootedAtManifest(t *testing.T) {
	fsys := DefaultProfileFS()

	if _, err := fs.Stat(fsys, "mneme-profile.toml"); err != nil {
		t.Fatalf("expected mneme-profile.toml at the fs.FS root: %v", err)
	}

	entries, err := fs.ReadDir(fsys, "agents")
	if err != nil {
		t.Fatalf("read agents dir: %v", err)
	}
	names, err := BundledAgentNames()
	if err != nil {
		t.Fatalf("BundledAgentNames: %v", err)
	}
	if len(entries) != len(names) {
		t.Errorf("default profile has %d agent files, want %d (matching BundledAgentNames)", len(entries), len(names))
	}
}

// TestDefaultProfile_LoadContentsFS_ParsesCleanly verifies the default
// profile is a valid profile end to end via the exact same LoadContentsFS
// entry point ProfileService.Activate uses (SPEC-096 §6 AC2) — not just that
// individual files exist.
func TestDefaultProfile_LoadContentsFS_ParsesCleanly(t *testing.T) {
	c, err := profile.LoadContentsFS(DefaultProfileFS())
	if err != nil {
		t.Fatalf("LoadContentsFS(DefaultProfileFS()): %v", err)
	}

	names, err := BundledAgentNames()
	if err != nil {
		t.Fatalf("BundledAgentNames: %v", err)
	}
	if len(c.Agents) != len(names) {
		t.Errorf("Agents: got %d, want %d", len(c.Agents), len(names))
	}

	skillNames, err := BundledSkillNames()
	if err != nil {
		t.Fatalf("BundledSkillNames: %v", err)
	}
	if len(c.Skills) != len(skillNames) {
		t.Errorf("Skills: got %v, want %v", c.Skills, skillNames)
	}

	// AC4: blocks/ is empty and rules.jsonl is absent — the operating manual
	// stays host-global infra, never a profile block.
	if len(c.Blocks) != 0 {
		t.Errorf("expected 0 blocks in the OSS default profile, got %d: %+v", len(c.Blocks), c.Blocks)
	}
	if len(c.Rules) != 0 {
		t.Errorf("expected 0 rules in the OSS default profile, got %d: %+v", len(c.Rules), c.Rules)
	}

	if c.ModelsPath == "" {
		t.Error("expected ModelsPath to be set (models.toml)")
	}
	if c.TemplatesDir == "" {
		t.Error("expected TemplatesDir to be set (templates/)")
	}
}

// TestDefaultProfile_DriftAgainstAssets is the AC3/R2 guard: the default
// profile's agents/skills must be byte-identical to the assets the global
// installer already embeds (builtinAgents/builtinSkills). A future edit to
// one copy without the other breaks this test — the drift is a caught CI
// failure, not silent skew (the same precedent internal/subagents already
// established for its own archetype copy).
func TestDefaultProfile_DriftAgainstAssets(t *testing.T) {
	fsys := DefaultProfileFS()

	// --- agents/<role>.md byte-for-byte against assets/agents/*.md ---
	names, err := BundledAgentNames()
	if err != nil {
		t.Fatalf("BundledAgentNames: %v", err)
	}
	for _, role := range names {
		want, err := builtinAgents.ReadFile("assets/agents/" + role + ".md")
		if err != nil {
			t.Fatalf("read builtin agent %s: %v", role, err)
		}
		got, err := fs.ReadFile(fsys, "agents/"+role+".md")
		if err != nil {
			t.Fatalf("read default profile agent %s: %v", role, err)
		}
		if !bytes.Equal(want, got) {
			t.Errorf("agent %s: default profile copy drifted from assets/agents/%s.md", role, role)
		}
	}

	// --- skills/ byte-for-byte, full tree, against assets/skills/ ---
	entries, err := BundledSkillEntries()
	if err != nil {
		t.Fatalf("BundledSkillEntries: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("BundledSkillEntries returned no files — cannot verify drift")
	}
	for _, e := range entries {
		got, err := fs.ReadFile(fsys, "skills/"+filepathToSlash(e.RelPath))
		if err != nil {
			t.Errorf("skills/%s missing from default profile: %v", e.RelPath, err)
			continue
		}
		if !bytes.Equal(e.Content, got) {
			t.Errorf("skills/%s: default profile copy drifted from assets/skills/", e.RelPath)
		}
	}

	// --- models.toml parsed == defaultAgentModels (map-to-map, SPEC-096 §6 3.3) ---
	modelsData, err := fs.ReadFile(fsys, "models.toml")
	if err != nil {
		t.Fatalf("read default profile models.toml: %v", err)
	}
	var parsedModels struct {
		Models map[string]string `toml:"models"`
	}
	if err := toml.Unmarshal(modelsData, &parsedModels); err != nil {
		t.Fatalf("parse models.toml: %v", err)
	}
	if len(parsedModels.Models) != len(defaultAgentModels) {
		t.Fatalf("models.toml has %d entries, defaultAgentModels has %d", len(parsedModels.Models), len(defaultAgentModels))
	}
	for agent, want := range defaultAgentModels {
		got, ok := parsedModels.Models[agent]
		if !ok {
			t.Errorf("models.toml missing entry for agent %q", agent)
			continue
		}
		if got != want {
			t.Errorf("models.toml[%q] = %q, want %q (defaultAgentModels)", agent, got, want)
		}
	}

	// --- templates/spec.md byte-for-byte against the migrated subset's origin ---
	wantSpec, err := builtinTemplates.ReadFile("assets/templates/spec-template.md")
	if err != nil {
		t.Fatalf("read builtin spec-template.md: %v", err)
	}
	gotSpec, err := fs.ReadFile(fsys, "templates/spec.md")
	if err != nil {
		t.Fatalf("read default profile templates/spec.md: %v", err)
	}
	if !bytes.Equal(wantSpec, gotSpec) {
		t.Error("templates/spec.md: default profile copy drifted from assets/templates/spec-template.md")
	}
}

// filepathToSlash normalises a SkillEntry.RelPath (which BundledSkillEntries
// already derives via filepath.Rel, so it may use OS-native separators on
// Windows) to the forward-slash form fs.FS paths always require.
func filepathToSlash(p string) string {
	out := make([]byte, len(p))
	for i := 0; i < len(p); i++ {
		if p[i] == '\\' {
			out[i] = '/'
		} else {
			out[i] = p[i]
		}
	}
	return string(out)
}
