package sddfile

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSDDFile_MaxIDs is AC2: MaxBacklogID/MaxSpecID read only names, never
// content — proven by including a file whose content is illegible but whose
// name is a valid correlative, and asserting it still counts.
func TestSDDFile_MaxIDs(t *testing.T) {
	t.Run("backlog: absent directory is zero", func(t *testing.T) {
		root := t.TempDir()
		got, err := MaxBacklogID(root)
		if err != nil {
			t.Fatalf("MaxBacklogID: %v", err)
		}
		if got != 0 {
			t.Errorf("MaxBacklogID = %d, want 0", got)
		}
	})

	t.Run("backlog: max of well-formed names, ignoring malformed and unreadable content", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(RootDir(root), "backlog")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, "BL-001.md"), "ok")
		writeFile(t, filepath.Join(dir, "BL-205.md"), "\x00\x01illegible-but-name-is-valid")
		writeFile(t, filepath.Join(dir, "BL-abc.md"), "ignored: not a number")
		writeFile(t, filepath.Join(dir, "notas.md"), "ignored: no BL- prefix")

		got, err := MaxBacklogID(root)
		if err != nil {
			t.Fatalf("MaxBacklogID: %v", err)
		}
		if got != 205 {
			t.Errorf("MaxBacklogID = %d, want 205 (must never open a file to decide)", got)
		}
	})

	t.Run("spec: directory without record.md still counts (name reserves the number, D4)", func(t *testing.T) {
		root := t.TempDir()
		specsDir := filepath.Join(RootDir(root), "specs")
		if err := os.MkdirAll(filepath.Join(specsDir, "SPEC-207"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(specsDir, "SPEC-abc"), 0o755); err != nil {
			t.Fatal(err)
		}

		got, err := MaxSpecID(root)
		if err != nil {
			t.Fatalf("MaxSpecID: %v", err)
		}
		if got != 207 {
			t.Errorf("MaxSpecID = %d, want 207", got)
		}
	})

	t.Run("spec: absent directory is zero", func(t *testing.T) {
		root := t.TempDir()
		got, err := MaxSpecID(root)
		if err != nil {
			t.Fatalf("MaxSpecID: %v", err)
		}
		if got != 0 {
			t.Errorf("MaxSpecID = %d, want 0", got)
		}
	})
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
