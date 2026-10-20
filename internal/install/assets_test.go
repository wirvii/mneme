package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFilesFromEmbed verifies that filesFromEmbed returns a CommandFile for
// each embedded file in the subdirectory, with the correct destination path.
func TestFilesFromEmbed(t *testing.T) {
	destDir := t.TempDir()

	files, err := filesFromEmbed(builtinAgents, "assets/agents", destDir)
	if err != nil {
		t.Fatalf("filesFromEmbed returned error: %v", err)
	}

	if len(files) == 0 {
		t.Fatal("filesFromEmbed returned zero files for assets/agents")
	}

	// Every returned file should have a path under destDir and non-empty content.
	for _, f := range files {
		if !strings.HasPrefix(f.Path, destDir) {
			t.Errorf("file path %q does not start with destDir %q", f.Path, destDir)
		}
		if len(f.Content) == 0 {
			t.Errorf("file %q has empty content", f.Path)
		}
		if filepath.Ext(f.Path) != ".md" {
			t.Errorf("file %q is not a .md file", f.Path)
		}
	}
}

// TestFilesFromEmbed_Commands checks that command files are returned correctly.
func TestFilesFromEmbed_Commands(t *testing.T) {
	destDir := t.TempDir()

	files, err := filesFromEmbed(builtinCommands, "assets/commands", destDir)
	if err != nil {
		t.Fatalf("filesFromEmbed(commands) returned error: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("filesFromEmbed returned zero files for assets/commands")
	}
}

// TestMnemeInitCommandAsset_ThinWrapper verifies the shape of the embedded
// assets/commands/mneme-init.md asset (SPEC-067): it must be readable
// directly (the wiring in ClaudeCode().Commands reads this exact file, not
// the whole assets/commands dir), carry the expected frontmatter (name +
// description, matching the shape of the other command assets such as
// grill-me.md), and stay a thin wrapper that only points at the mneme-init
// skill — never reintroducing the obsolete 5-phase project-init markdown.
func TestMnemeInitCommandAsset_ThinWrapper(t *testing.T) {
	content, err := builtinCommands.ReadFile("assets/commands/mneme-init.md")
	if err != nil {
		t.Fatalf("read embedded assets/commands/mneme-init.md: %v", err)
	}

	text := string(content)

	if !strings.HasPrefix(text, "---\nname: mneme-init\n") {
		t.Errorf("mneme-init.md must start with frontmatter beginning 'name: mneme-init', got: %.80s", text)
	}
	if !strings.Contains(text, "description:") {
		t.Error("mneme-init.md frontmatter is missing 'description:'")
	}
	if !strings.Contains(text, "mneme-init") || !strings.Contains(strings.ToLower(text), "skill") {
		t.Error("mneme-init.md must reference the mneme-init skill")
	}

	// Anti-drift: must not reintroduce the obsolete 5-phase workflow markdown
	// that this wrapper replaces (SPEC-067 restores a THIN wrapper only).
	forbidden := []string{"subagent_fingerprint", "Phase 0", "Phase 1", "Phase 2", "Phase 3", "Phase 4"}
	for _, marker := range forbidden {
		if strings.Contains(text, marker) {
			t.Errorf("mneme-init.md contains obsolete 5-phase marker %q — must stay a thin wrapper", marker)
		}
	}
}

// TestFilesFromEmbed_Templates checks that template files are returned correctly.
func TestFilesFromEmbed_Templates(t *testing.T) {
	destDir := t.TempDir()

	files, err := filesFromEmbed(builtinTemplates, "assets/templates", destDir)
	if err != nil {
		t.Fatalf("filesFromEmbed(templates) returned error: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("filesFromEmbed returned zero files for assets/templates")
	}
}

// codegraphPolicy is the canonical optional code-graph policy block shared by
// every bundled agent (SPEC-165 replaced the three mandatory, toolset-specific
// variants of SPEC-045). It is the authoritative source for
// TestAgentsCodegraphPolicy.
const codegraphPolicy = `<!-- mneme:codegraph-policy:start -->
## Grafo de codigo (opcional)

Si este proyecto tiene un grafo de codigo indexado (` + "`" + `mneme codegraph status` + "`" + `), puede
ahorrarte lecturas en preguntas de estructura: ` + "`" + `codegraph_search` + "`" + ` (donde vive un
simbolo), ` + "`" + `codegraph_context` + "`" + ` (definicion + relaciones), ` + "`" + `codegraph_callers` + "`" + ` (quien lo
llama), ` + "`" + `codegraph_impact` + "`" + ` (que se ve afectado). Las demas consultas van por la CLI
(` + "`" + `mneme codegraph callees|trace|node|explore|files|status` + "`" + `) si tienes Bash. Cuando
conviene y cuando no: skill ` + "`" + `codegraph` + "`" + `.

Aviso de cobertura: ` + "`" + `codegraph_impact` + "`" + ` puede estar INCOMPLETO (method-calls ` + "`" + `x.Foo()` + "`" + `,
llamadas cross-package/stdlib). Para un impacto exhaustivo antes de un refactor,
complementa con Grep/Read.
<!-- mneme:codegraph-policy:end -->`

// TestAgentsCodegraphPolicy verifies that every embedded agent file contains the
// canonical optional code-graph policy block (SPEC-045, made optional by
// SPEC-165) and no longer names the tools that moved to the CLI. The test
// fails as soon as any asset diverges, providing anti-drift protection after
// edits to the agent assets.
func TestAgentsCodegraphPolicy(t *testing.T) {
	destDir := t.TempDir()

	files, err := filesFromEmbed(builtinAgents, "assets/agents", destDir)
	if err != nil {
		t.Fatalf("filesFromEmbed returned error: %v", err)
	}

	// All six agents carry the same block.
	canonicalPolicy := map[string]string{
		"architect.md":     codegraphPolicy,
		"qa-tester.md":     codegraphPolicy,
		"backend.md":       codegraphPolicy,
		"frontend.md":      codegraphPolicy,
		"bug-hunter.md":    codegraphPolicy,
		"diagnostician.md": codegraphPolicy,
	}

	// All 6 agents must be covered — fail if the set changes without updating this test.
	found := make(map[string]bool, len(canonicalPolicy))
	for _, f := range files {
		base := filepath.Base(f.Path)
		expected, ok := canonicalPolicy[base]
		if !ok {
			continue // skip agents not in scope (e.g. future additions)
		}
		found[base] = true
		content := string(f.Content)
		if !strings.Contains(content, expected) {
			t.Errorf("agent %q: codegraph policy block does not match canonical.\n"+
				"Expected to find:\n%s\n\nGot content (first 500 bytes):\n%.500s",
				base, expected, content)
		}
		for _, retired := range []string{"codegraph_callees", "codegraph_trace", "OBLIGATORIO: cuando este proyecto tiene un grafo"} {
			if strings.Contains(content, retired) {
				t.Errorf("agent %q still contains retired policy text %q", base, retired)
			}
		}
	}

	// Verify all 6 known agents were actually present in the embed.
	for name := range canonicalPolicy {
		if !found[name] {
			t.Errorf("agent %q not found in embedded assets — was it removed?", name)
		}
	}
}

// TestAgentsMnemeAware verifies that all embedded agent files contain the
// "Integracion con mneme" section required by the spec.
func TestAgentsMnemeAware(t *testing.T) {
	destDir := t.TempDir()

	files, err := filesFromEmbed(builtinAgents, "assets/agents", destDir)
	if err != nil {
		t.Fatalf("filesFromEmbed returned error: %v", err)
	}

	for _, f := range files {
		if !strings.Contains(string(f.Content), "Integracion con mneme") {
			t.Errorf("agent file %q is missing the 'Integracion con mneme' section", filepath.Base(f.Path))
		}
	}
}

// TestWriteAgents verifies that WriteAgents installs all embedded agent files
// and overwrites existing files.
func TestWriteAgents(t *testing.T) {
	destDir := t.TempDir()

	agent := &Agent{
		Agents: func() ([]CommandFile, error) {
			return filesFromEmbed(builtinAgents, "assets/agents", destDir)
		},
	}

	if err := WriteAgents(agent); err != nil {
		t.Fatalf("WriteAgents error: %v", err)
	}

	entries, err := os.ReadDir(destDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("WriteAgents wrote no files")
	}
}

// TestWriteTemplates verifies that WriteTemplates installs template files and
// does NOT overwrite existing ones.
func TestWriteTemplates(t *testing.T) {
	destDir := t.TempDir()

	original := []byte("original content — must not be overwritten")
	files, err := filesFromEmbed(builtinTemplates, "assets/templates", destDir)
	if err != nil {
		t.Fatalf("filesFromEmbed: %v", err)
	}

	// Pre-write the first file with custom content.
	first := files[0]
	if err := os.WriteFile(first.Path, original, 0o644); err != nil {
		t.Fatalf("pre-write: %v", err)
	}

	agent := &Agent{
		Templates: func() ([]CommandFile, error) {
			return filesFromEmbed(builtinTemplates, "assets/templates", destDir)
		},
	}

	if err := WriteTemplates(agent); err != nil {
		t.Fatalf("WriteTemplates error: %v", err)
	}

	// The pre-existing file must not have been overwritten.
	content, err := os.ReadFile(first.Path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(content) != string(original) {
		t.Errorf("WriteTemplates overwrote an existing file — got %q, want %q", content, original)
	}
}
