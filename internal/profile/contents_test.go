package profile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestLoadContents_MinimalProfile verifies that a profile directory with
// nothing but the manifest (no agents/skills/blocks/rules) loads to a
// zero-value Contents with no error.
func TestLoadContents_MinimalProfile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ManifestFileName), "name=\"minimal\"\nversion=\"1.0.0\"\n")

	c, err := LoadContents(dir)
	if err != nil {
		t.Fatalf("LoadContents: unexpected error: %v", err)
	}
	if len(c.Agents) != 0 || len(c.Skills) != 0 || len(c.Blocks) != 0 || len(c.Rules) != 0 {
		t.Errorf("expected empty Contents for a minimal profile, got %+v", c)
	}
	if c.ModelsPath != "" || c.PolicyPath != "" || c.TemplatesDir != "" {
		t.Errorf("expected no optional paths for a minimal profile, got %+v", c)
	}
}

// TestLoadContents_FullProfile verifies parsing of every piece: agents,
// skills, blocks, rules.jsonl, and the models/policy/templates paths.
func TestLoadContents_FullProfile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, agentsSubdir, "backend.md"), "---\nname: backend\n---\nbody")
	writeFile(t, filepath.Join(dir, agentsSubdir, "architect.md"), "---\nname: architect\n---\nbody")
	writeFile(t, filepath.Join(dir, skillsSubdir, "new-project", "SKILL.md"), "# skill")
	writeFile(t, filepath.Join(dir, blocksSubdir, "profile.md"), "## Contexto")
	writeFile(t, filepath.Join(dir, rulesFileName),
		`{"title":"No CGO","content":"pure go","applies_to":["**"],"severity":"warn"}`+"\n"+
			`{"title":"Commits","content":"conventional commits","applies_to":["**"]}`+"\n")
	writeFile(t, filepath.Join(dir, modelsFileName), "[models]\n")
	writeFile(t, filepath.Join(dir, policyFileName), "[policy]\n")
	writeFile(t, filepath.Join(dir, templatesSubdir, "PLACEHOLDER"), "x")

	c, err := LoadContents(dir)
	if err != nil {
		t.Fatalf("LoadContents: unexpected error: %v", err)
	}

	if len(c.Agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(c.Agents))
	}
	if c.Agents[0].Role != "architect" || c.Agents[1].Role != "backend" {
		t.Errorf("expected agents sorted by role, got %+v", c.Agents)
	}

	if len(c.Skills) != 1 || c.Skills[0] != "new-project" {
		t.Errorf("expected 1 skill 'new-project', got %+v", c.Skills)
	}
	if c.SkillsDir == "" {
		t.Error("expected SkillsDir to be set")
	}

	if len(c.Blocks) != 1 || c.Blocks[0].Name != "profile" {
		t.Errorf("expected 1 block 'profile', got %+v", c.Blocks)
	}

	if len(c.Rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(c.Rules))
	}
	if c.Rules[0].Title != "No CGO" || c.Rules[0].Severity != "warn" {
		t.Errorf("unexpected rule[0]: %+v", c.Rules[0])
	}
	if c.Rules[1].Severity != "" {
		t.Errorf("expected rule[1] severity to default to empty, got %q", c.Rules[1].Severity)
	}

	if c.ModelsPath == "" || c.PolicyPath == "" || c.TemplatesDir == "" {
		t.Errorf("expected models/policy/templates paths to be set, got %+v", c)
	}
}

// TestLoadRules_MalformedJSON verifies that a broken JSON line produces an
// error naming its 1-indexed line number.
func TestLoadRules_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, rulesFileName),
		`{"title":"ok","content":"c","applies_to":["**"]}`+"\n"+
			`{not valid json`+"\n")

	_, err := LoadContents(dir)
	if err == nil {
		t.Fatal("expected error for malformed rules.jsonl line")
	}
	if !contains(err.Error(), "line 2") {
		t.Errorf("expected error to name line 2, got: %v", err)
	}
}

// TestLoadRules_SeverityOutOfRange verifies that a severity outside
// info/warn/block is rejected.
func TestLoadRules_SeverityOutOfRange(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, rulesFileName),
		`{"title":"bad","content":"c","applies_to":["**"],"severity":"critical"}`+"\n")

	_, err := LoadContents(dir)
	if !errors.Is(err, ErrInvalidRuleSpec) {
		t.Errorf("expected ErrInvalidRuleSpec, got %v", err)
	}
}

// TestLoadRules_EmptyAppliesTo verifies that a rule with no applies_to
// patterns is rejected.
func TestLoadRules_EmptyAppliesTo(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, rulesFileName),
		`{"title":"bad","content":"c","applies_to":[]}`+"\n")

	_, err := LoadContents(dir)
	if !errors.Is(err, ErrInvalidRuleSpec) {
		t.Errorf("expected ErrInvalidRuleSpec, got %v", err)
	}
}

// TestLoadContents_NoRulesFile verifies that the absence of rules.jsonl is
// not an error and yields a nil Rules slice.
func TestLoadContents_NoRulesFile(t *testing.T) {
	dir := t.TempDir()
	c, err := LoadContents(dir)
	if err != nil {
		t.Fatalf("LoadContents: %v", err)
	}
	if c.Rules != nil {
		t.Errorf("expected nil Rules, got %+v", c.Rules)
	}
}

// TestLoadContents_DelegatesToLoadContentsFS verifies AC2's single-parse-path
// guarantee: LoadContents(dir) and LoadContentsFS(os.DirFS(dir)) against the
// same tree produce field-for-field identical Contents (except FS itself,
// which is a distinct-but-equivalent os.DirFS(dir) value each call).
func TestLoadContents_DelegatesToLoadContentsFS(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, agentsSubdir, "backend.md"), "---\nname: backend\n---\nbody")
	writeFile(t, filepath.Join(dir, skillsSubdir, "new-project", "SKILL.md"), "# skill")
	writeFile(t, filepath.Join(dir, blocksSubdir, "profile.md"), "## Contexto")
	writeFile(t, filepath.Join(dir, rulesFileName),
		`{"title":"No CGO","content":"pure go","applies_to":["**"],"severity":"warn"}`+"\n")
	writeFile(t, filepath.Join(dir, modelsFileName), "[models]\n")

	viaLoadContents, err := LoadContents(dir)
	if err != nil {
		t.Fatalf("LoadContents: %v", err)
	}
	viaLoadContentsFS, err := LoadContentsFS(os.DirFS(dir))
	if err != nil {
		t.Fatalf("LoadContentsFS: %v", err)
	}

	if len(viaLoadContents.Agents) != len(viaLoadContentsFS.Agents) ||
		viaLoadContents.Agents[0].Role != viaLoadContentsFS.Agents[0].Role ||
		string(viaLoadContents.Agents[0].Content) != string(viaLoadContentsFS.Agents[0].Content) {
		t.Errorf("Agents diverged: %+v vs %+v", viaLoadContents.Agents, viaLoadContentsFS.Agents)
	}
	if len(viaLoadContents.Skills) != 1 || viaLoadContents.Skills[0] != viaLoadContentsFS.Skills[0] {
		t.Errorf("Skills diverged: %v vs %v", viaLoadContents.Skills, viaLoadContentsFS.Skills)
	}
	if viaLoadContents.SkillsDir != viaLoadContentsFS.SkillsDir {
		t.Errorf("SkillsDir diverged: %q vs %q", viaLoadContents.SkillsDir, viaLoadContentsFS.SkillsDir)
	}
	if len(viaLoadContents.Blocks) != 1 || viaLoadContents.Blocks[0].Name != viaLoadContentsFS.Blocks[0].Name {
		t.Errorf("Blocks diverged: %+v vs %+v", viaLoadContents.Blocks, viaLoadContentsFS.Blocks)
	}
	if len(viaLoadContents.Rules) != len(viaLoadContentsFS.Rules) {
		t.Errorf("Rules diverged: %+v vs %+v", viaLoadContents.Rules, viaLoadContentsFS.Rules)
	}
	if viaLoadContents.ModelsPath != viaLoadContentsFS.ModelsPath {
		t.Errorf("ModelsPath diverged: %q vs %q", viaLoadContents.ModelsPath, viaLoadContentsFS.ModelsPath)
	}
	if viaLoadContents.FS == nil || viaLoadContentsFS.FS == nil {
		t.Error("expected both Contents to carry a non-nil FS")
	}
}

// TestDefaultProfileName verifies the reserved constant's exact value, since
// ProfileService.Activate compares against it by string equality.
func TestDefaultProfileName(t *testing.T) {
	if DefaultProfileName != "mneme-default" {
		t.Errorf("DefaultProfileName = %q, want %q", DefaultProfileName, "mneme-default")
	}
}

// TestLoadContentsFS_MinimalProfile verifies that an fs.FS with nothing but
// the manifest parses to a near-zero-value Contents (FS still set) and no
// error — the same guarantee LoadContents already gives a disk checkout,
// now verified directly against the fs.FS entry point the embedded default
// profile also uses.
func TestLoadContentsFS_MinimalProfile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ManifestFileName), "name=\"minimal\"\nversion=\"1.0.0\"\n")

	c, err := LoadContentsFS(os.DirFS(dir))
	if err != nil {
		t.Fatalf("LoadContentsFS: unexpected error: %v", err)
	}
	if c.FS == nil {
		t.Error("expected FS to be set even for a minimal profile")
	}
	if len(c.Agents) != 0 || len(c.Skills) != 0 || len(c.Blocks) != 0 || len(c.Rules) != 0 {
		t.Errorf("expected empty Contents for a minimal profile, got %+v", c)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
