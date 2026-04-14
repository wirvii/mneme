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

	if err := MigrateWorkflowDir(legacy, newDir); err != nil {
		t.Fatalf("MigrateWorkflowDir error: %v", err)
	}

	// Verify files were copied.
	checkFile(t, filepath.Join(newDir, "specs", "P001", "spec.md"), "# spec")
	checkFile(t, filepath.Join(newDir, "backlog.md"), "- [ ] Feature A")
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

	if err := MigrateWorkflowDir(legacy, newDir); err != nil {
		t.Fatalf("MigrateWorkflowDir error: %v", err)
	}

	checkFile(t, filepath.Join(newDir, "notes.md"), original)
}

// TestMigrateWorkflowDir_Idempotent verifies that running migration twice
// produces the same result without errors.
func TestMigrateWorkflowDir_Idempotent(t *testing.T) {
	legacy := t.TempDir()
	newDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(legacy, "file.md"), []byte("content"), 0o644); err != nil {
		t.Fatalf("create legacy: %v", err)
	}

	if err := MigrateWorkflowDir(legacy, newDir); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := MigrateWorkflowDir(legacy, newDir); err != nil {
		t.Fatalf("second run: %v", err)
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
