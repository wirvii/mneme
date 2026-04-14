package install

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMigrateWorkflowDir_Basic verifies that files from legacyDir are copied
// to newDir when they do not already exist there.
func TestMigrateWorkflowDir_Basic(t *testing.T) {
	legacy := t.TempDir()
	newDir := t.TempDir()

	// Create a nested structure in the legacy dir.
	_ = os.MkdirAll(filepath.Join(legacy, "specs", "P001"), 0o755)
	if err := os.WriteFile(filepath.Join(legacy, "specs", "P001", "spec.md"), []byte("# spec"), 0o644); err != nil {
		t.Fatalf("create legacy file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "backlog.md"), []byte("- [ ] Feature A"), 0o644); err != nil {
		t.Fatalf("create legacy backlog: %v", err)
	}

	result, err := MigrateWorkflowDir(legacy, newDir)
	if err != nil {
		t.Fatalf("MigrateWorkflowDir error: %v", err)
	}

	// Verify files were copied.
	checkFile(t, filepath.Join(newDir, "specs", "P001", "spec.md"), "# spec")
	checkFile(t, filepath.Join(newDir, "backlog.md"), "- [ ] Feature A")

	if len(result.Copied) != 2 {
		t.Errorf("Copied: got %d files, want 2; files: %v", len(result.Copied), result.Copied)
	}
	if len(result.Skipped) != 0 {
		t.Errorf("Skipped: got %d files, want 0", len(result.Skipped))
	}
}

// TestMigrateWorkflowDir_MultipleProjects verifies that multiple project
// sub-directories under legacyDir are each migrated with their full structure
// intact under the corresponding sub-directory in newDir.
func TestMigrateWorkflowDir_MultipleProjects(t *testing.T) {
	// Simulate ~/.workflows/ containing two project dirs.
	legacy := t.TempDir()
	newDir := t.TempDir()

	projects := map[string]map[string]string{
		"mneme": {
			"specs/P001/spec.md":    "# mneme spec",
			"plans/backlog.md":      "- [ ] Item A",
		},
		"platform": {
			"bugs/B001/bug.md":      "# platform bug",
			"specs/P002/spec.md":    "# platform spec",
		},
	}

	for project, files := range projects {
		for relPath, content := range files {
			full := filepath.Join(legacy, project, relPath)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
			}
			if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
				t.Fatalf("write %s: %v", full, err)
			}
		}
	}

	result, err := MigrateWorkflowDir(legacy, newDir)
	if err != nil {
		t.Fatalf("MigrateWorkflowDir error: %v", err)
	}

	// Verify all files landed under their project slug in newDir.
	for project, files := range projects {
		for relPath, content := range files {
			dst := filepath.Join(newDir, project, relPath)
			checkFile(t, dst, content)
		}
	}

	if len(result.Copied) != 4 {
		t.Errorf("Copied: got %d files, want 4; files: %v", len(result.Copied), result.Copied)
	}
	if len(result.Skipped) != 0 {
		t.Errorf("Skipped: got %d files, want 0", len(result.Skipped))
	}
}

// TestMigrateWorkflowDir_NoOverwrite verifies that pre-existing files in newDir
// are preserved (not overwritten) during migration.
func TestMigrateWorkflowDir_NoOverwrite(t *testing.T) {
	legacy := t.TempDir()
	newDir := t.TempDir()

	original := "pre-existing content"
	if err := os.WriteFile(filepath.Join(legacy, "notes.md"), []byte("legacy content"), 0o644); err != nil {
		t.Fatalf("create legacy: %v", err)
	}
	// Pre-write the file in the new dir.
	if err := os.WriteFile(filepath.Join(newDir, "notes.md"), []byte(original), 0o644); err != nil {
		t.Fatalf("create new: %v", err)
	}

	result, err := MigrateWorkflowDir(legacy, newDir)
	if err != nil {
		t.Fatalf("MigrateWorkflowDir error: %v", err)
	}

	checkFile(t, filepath.Join(newDir, "notes.md"), original)

	if len(result.Copied) != 0 {
		t.Errorf("Copied: got %d files, want 0", len(result.Copied))
	}
	if len(result.Skipped) != 1 {
		t.Errorf("Skipped: got %d files, want 1", len(result.Skipped))
	}
}

// TestMigrateWorkflowDir_Idempotent verifies that running migration twice
// produces the same result without errors and that the second run reports all
// files as skipped.
func TestMigrateWorkflowDir_Idempotent(t *testing.T) {
	legacy := t.TempDir()
	newDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(legacy, "file.md"), []byte("content"), 0o644); err != nil {
		t.Fatalf("create legacy: %v", err)
	}

	first, err := MigrateWorkflowDir(legacy, newDir)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if len(first.Copied) != 1 {
		t.Errorf("first run Copied: got %d, want 1", len(first.Copied))
	}

	second, err := MigrateWorkflowDir(legacy, newDir)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(second.Copied) != 0 {
		t.Errorf("second run Copied: got %d, want 0", len(second.Copied))
	}
	if len(second.Skipped) != 1 {
		t.Errorf("second run Skipped: got %d, want 1", len(second.Skipped))
	}

	checkFile(t, filepath.Join(newDir, "file.md"), "content")
}

// TestParseBacklogMD verifies that raw items are parsed and completed items
// are ignored.
func TestParseBacklogMD(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantLen int
		titles  []string
	}{
		{
			name:    "raw items only",
			input:   "- [ ] Feature A\n- [ ] Feature B\n",
			wantLen: 2,
			titles:  []string{"Feature A", "Feature B"},
		},
		{
			name:    "mixed — completed items ignored",
			input:   "- [ ] Keep me\n- [x] Done already\n- [ ] Also keep\n",
			wantLen: 2,
			titles:  []string{"Keep me", "Also keep"},
		},
		{
			name:    "headers and prose ignored",
			input:   "# Backlog\n\n- [ ] Item one\n\nSome prose\n",
			wantLen: 1,
			titles:  []string{"Item one"},
		},
		{
			name:    "empty input",
			input:   "",
			wantLen: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			items := ParseBacklogMD(tc.input)
			if len(items) != tc.wantLen {
				t.Errorf("got %d items, want %d", len(items), tc.wantLen)
			}
			for i, title := range tc.titles {
				if i >= len(items) {
					break
				}
				if items[i].Title != title {
					t.Errorf("item[%d].Title = %q, want %q", i, items[i].Title, title)
				}
			}
		})
	}
}

// checkFile is a test helper that asserts the file at path has the expected content.
func checkFile(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("checkFile: ReadFile %s: %v", path, err)
		return
	}
	if string(data) != want {
		t.Errorf("checkFile %s: got %q, want %q", path, data, want)
	}
}
