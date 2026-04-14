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
