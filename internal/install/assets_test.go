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

// codegraphPolicyNoBash is the canonical codegraph-first exploration policy block
// for agents that do NOT have Bash in their toolset (architect, qa-tester).
// It is the authoritative source of truth for TestAgentsCodegraphPolicy.
const codegraphPolicyNoBash = `<!-- mneme:codegraph-policy:start -->
## Exploracion de codigo: grafo primero

Este proyecto puede tener un grafo de codigo indexado (mneme codegraph). Antes de
usar Read o Grep para ENTENDER el codigo —su estructura, quien llama a que, el
impacto de un cambio, o donde vive un simbolo— usa PRIMERO las tools del grafo:

- ` + "`" + `codegraph_search` + "`" + `   — encontrar simbolos por nombre o concepto
- ` + "`" + `codegraph_context` + "`" + `  — vecindario de un simbolo (definicion + relaciones)
- ` + "`" + `codegraph_callers` + "`" + `  — quien llama a un simbolo
- ` + "`" + `codegraph_callees` + "`" + `  — a quien llama un simbolo
- ` + "`" + `codegraph_impact` + "`" + `   — que se ve afectado si cambias un simbolo
- ` + "`" + `codegraph_trace` + "`" + `    — caminos entre dos simbolos

Cae a Read/Grep SOLO si: el grafo no cubre la pregunta, esta desactualizado
(stale), o el repo no esta indexado. Para leer el contenido literal de un archivo
que YA localizaste, Read es lo correcto.
<!-- mneme:codegraph-policy:end -->`

// codegraphPolicyWithBash is the canonical codegraph-first exploration policy block
// for agents that have Bash in their toolset (backend, frontend, bug-hunter).
// It extends codegraphPolicyNoBash with a clause forbidding Bash for code navigation.
const codegraphPolicyWithBash = `<!-- mneme:codegraph-policy:start -->
## Exploracion de codigo: grafo primero

Este proyecto puede tener un grafo de codigo indexado (mneme codegraph). Antes de
usar Read o Grep para ENTENDER el codigo —su estructura, quien llama a que, el
impacto de un cambio, o donde vive un simbolo— usa PRIMERO las tools del grafo:

- ` + "`" + `codegraph_search` + "`" + `   — encontrar simbolos por nombre o concepto
- ` + "`" + `codegraph_context` + "`" + `  — vecindario de un simbolo (definicion + relaciones)
- ` + "`" + `codegraph_callers` + "`" + `  — quien llama a un simbolo
- ` + "`" + `codegraph_callees` + "`" + `  — a quien llama un simbolo
- ` + "`" + `codegraph_impact` + "`" + `   — que se ve afectado si cambias un simbolo
- ` + "`" + `codegraph_trace` + "`" + `    — caminos entre dos simbolos

Cae a Read/Grep SOLO si: el grafo no cubre la pregunta, esta desactualizado
(stale), o el repo no esta indexado. Para leer el contenido literal de un archivo
que YA localizaste, Read es lo correcto.

NO uses ` + "`" + `Bash` + "`" + ` (grep/cat/find/rg/head/tail) para navegar o entender la estructura
del codigo: eso lo resuelven las tools del grafo y Read/Grep nativos. Reserva Bash
para build, test, git y operaciones —no para explorar codigo.
<!-- mneme:codegraph-policy:end -->`

// codegraphPolicyDiagnostician is the canonical codegraph-first exploration policy
// block for the diagnostician agent. It has Bash but distinguishes code exploration
// (use codegraph) from log/infra reading (Bash remains valid).
const codegraphPolicyDiagnostician = `<!-- mneme:codegraph-policy:start -->
## Exploracion de codigo: grafo primero

Este proyecto puede tener un grafo de codigo indexado (mneme codegraph). Antes de
usar Read o Grep para ENTENDER el codigo —su estructura, quien llama a que, el
impacto de un cambio, o donde vive un simbolo— usa PRIMERO las tools del grafo:

- ` + "`" + `codegraph_search` + "`" + `   — encontrar simbolos por nombre o concepto
- ` + "`" + `codegraph_context` + "`" + `  — vecindario de un simbolo (definicion + relaciones)
- ` + "`" + `codegraph_callers` + "`" + `  — quien llama a un simbolo
- ` + "`" + `codegraph_callees` + "`" + `  — a quien llama un simbolo
- ` + "`" + `codegraph_impact` + "`" + `   — que se ve afectado si cambias un simbolo
- ` + "`" + `codegraph_trace` + "`" + `    — caminos entre dos simbolos

Cae a Read/Grep SOLO si: el grafo no cubre la pregunta, esta desactualizado
(stale), o el repo no esta indexado. Para leer el contenido literal de un archivo
que YA localizaste, Read es lo correcto.

NO uses ` + "`" + `Bash` + "`" + ` (grep/cat/find/rg) para navegar o entender la estructura del CODIGO
—usa las tools del grafo. Bash sigue siendo tu herramienta para leer LOGS, infra y
diagnostico operacional (ver ` + "`" + `## Permisos de Bash` + "`" + `): esa exploracion no cambia.
<!-- mneme:codegraph-policy:end -->`

// TestAgentsCodegraphPolicy verifies that every embedded agent file contains the
// canonical codegraph-first exploration policy block (SPEC-045). The test fails
// as soon as any asset diverges from its expected canonical variant, providing
// anti-drift protection after edits to the agent assets.
func TestAgentsCodegraphPolicy(t *testing.T) {
	destDir := t.TempDir()

	files, err := filesFromEmbed(builtinAgents, "assets/agents", destDir)
	if err != nil {
		t.Fatalf("filesFromEmbed returned error: %v", err)
	}

	// Map each agent basename to its expected canonical policy block.
	canonicalPolicy := map[string]string{
		"architect.md":     codegraphPolicyNoBash,
		"qa-tester.md":     codegraphPolicyNoBash,
		"backend.md":       codegraphPolicyWithBash,
		"frontend.md":      codegraphPolicyWithBash,
		"bug-hunter.md":    codegraphPolicyWithBash,
		"diagnostician.md": codegraphPolicyDiagnostician,
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
