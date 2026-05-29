package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUpsertManagedBlock_NewFile verifies that upsertManagedBlock creates a new
// file containing only the managed block when the target does not exist.
func TestUpsertManagedBlock_NewFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "CLAUDE.md")

	if err := upsertManagedBlock(target, "hello world"); err != nil {
		t.Fatalf("upsertManagedBlock error: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	text := string(data)

	if !strings.Contains(text, managedBlockStart(managedBlockVersion)) {
		t.Error("new file missing start marker")
	}
	if !strings.Contains(text, managedBlockEnd) {
		t.Error("new file missing end marker")
	}
	if !strings.Contains(text, "hello world") {
		t.Error("new file missing content")
	}
}

// TestUpsertManagedBlock_Idempotent verifies that running upsertManagedBlock
// twice with the same content produces a byte-identical file.
func TestUpsertManagedBlock_Idempotent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "CLAUDE.md")

	if err := upsertManagedBlock(target, "my content"); err != nil {
		t.Fatalf("first upsert error: %v", err)
	}

	data1, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read after first upsert: %v", err)
	}

	if err := upsertManagedBlock(target, "my content"); err != nil {
		t.Fatalf("second upsert error: %v", err)
	}

	data2, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read after second upsert: %v", err)
	}

	if string(data1) != string(data2) {
		t.Errorf("file not byte-identical after second upsert\nfirst:\n%s\nsecond:\n%s", data1, data2)
	}
}

// TestUpsertManagedBlock_PreservesProsa verifies that prose before and after
// the managed block is preserved when the block is replaced.
func TestUpsertManagedBlock_PreservesProsa(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "CLAUDE.md")

	initial := "# My header\n\nSome user prose here.\n"
	if err := os.WriteFile(target, []byte(initial), 0o644); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	if err := upsertManagedBlock(target, "block content"); err != nil {
		t.Fatalf("upsert error: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	text := string(data)

	if !strings.Contains(text, "My header") {
		t.Error("header was lost")
	}
	if !strings.Contains(text, "Some user prose here.") {
		t.Error("prose was lost")
	}
	if !strings.Contains(text, "block content") {
		t.Error("block content missing")
	}
}

// TestUpsertManagedBlock_PreservesProsaAfter verifies that content after the
// block is preserved across updates.
func TestUpsertManagedBlock_PreservesProsaAfter(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "CLAUDE.md")

	// Install a block first.
	if err := upsertManagedBlock(target, "initial content"); err != nil {
		t.Fatalf("initial upsert: %v", err)
	}

	// Add user content after the block.
	data, _ := os.ReadFile(target)
	augmented := string(data) + "\n## My custom section\n\nUser notes here.\n"
	if err := os.WriteFile(target, []byte(augmented), 0o644); err != nil {
		t.Fatalf("write augmented: %v", err)
	}

	// Update the block content.
	if err := upsertManagedBlock(target, "updated content"); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	result, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	text := string(result)

	if !strings.Contains(text, "updated content") {
		t.Error("updated block content missing")
	}
	if strings.Contains(text, "initial content") {
		t.Error("old block content should be replaced")
	}
	if !strings.Contains(text, "My custom section") {
		t.Error("user section after block was lost")
	}
	if !strings.Contains(text, "User notes here.") {
		t.Error("user notes after block were lost")
	}
}

// TestUpsertManagedBlock_VersionBump verifies that the version in the start
// marker is updated when content changes.
func TestUpsertManagedBlock_VersionBump(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "CLAUDE.md")

	// Plant an old-version block manually.
	oldBlock := "<!-- mneme:managed:start v=0 -->\nold stuff\n" + managedBlockEnd + "\n"
	if err := os.WriteFile(target, []byte(oldBlock), 0o644); err != nil {
		t.Fatalf("write old block: %v", err)
	}

	if err := upsertManagedBlock(target, "new content"); err != nil {
		t.Fatalf("upsert error: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	text := string(data)

	if !strings.Contains(text, managedBlockStart(managedBlockVersion)) {
		t.Errorf("start marker not updated to v=%d", managedBlockVersion)
	}
	if strings.Contains(text, "v=0") {
		t.Error("old version v=0 marker still present")
	}
	if !strings.Contains(text, "new content") {
		t.Error("new content missing")
	}
}

// TestUpsertManagedBlock_LegacyProtocolMigration verifies that a file with the
// legacy mneme:protocol block ends up with only the new managed block and the
// legacy markers are fully removed.
func TestUpsertManagedBlock_LegacyProtocolMigration(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "CLAUDE.md")

	// Create a file with the legacy protocol block.
	legacyContent := "# My CLAUDE.md\n\n" +
		legacyProtocolStart + "\n" +
		"old protocol content\n" +
		legacyProtocolEnd + "\n\n" +
		"## After section\n"
	if err := os.WriteFile(target, []byte(legacyContent), 0o644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	if err := upsertManagedBlock(target, "new manual content"); err != nil {
		t.Fatalf("upsert error: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	text := string(data)

	// Legacy markers must be gone.
	if strings.Contains(text, legacyProtocolStart) {
		t.Error("legacy start marker still present")
	}
	if strings.Contains(text, legacyProtocolEnd) {
		t.Error("legacy end marker still present")
	}
	if strings.Contains(text, "old protocol content") {
		t.Error("old protocol content still present")
	}

	// New managed block must be present.
	if !strings.Contains(text, managedBlockStart(managedBlockVersion)) {
		t.Error("new managed block start marker missing")
	}
	if !strings.Contains(text, managedBlockEnd) {
		t.Error("new managed block end marker missing")
	}
	if !strings.Contains(text, "new manual content") {
		t.Error("new content missing")
	}

	// Surrounding user prose must be preserved.
	if !strings.Contains(text, "My CLAUDE.md") {
		t.Error("header before legacy block was lost")
	}
	if !strings.Contains(text, "After section") {
		t.Error("content after legacy block was lost")
	}
}

// TestUpsertManagedBlock_LegacyProtocolIdempotent verifies that the legacy
// migration is idempotent: running upsert again on the already-migrated file
// produces a byte-identical file.
func TestUpsertManagedBlock_LegacyProtocolIdempotent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "CLAUDE.md")

	legacyContent := legacyProtocolStart + "\nold stuff\n" + legacyProtocolEnd + "\n"
	if err := os.WriteFile(target, []byte(legacyContent), 0o644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	if err := upsertManagedBlock(target, "content"); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	data1, _ := os.ReadFile(target)

	if err := upsertManagedBlock(target, "content"); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	data2, _ := os.ReadFile(target)

	if string(data1) != string(data2) {
		t.Errorf("not idempotent after legacy migration\nfirst:\n%s\nsecond:\n%s", data1, data2)
	}
}

// TestReadManagedBlock_Present verifies readManagedBlock returns the content
// and version for a file with a well-formed managed block.
func TestReadManagedBlock_Present(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "CLAUDE.md")

	if err := upsertManagedBlock(target, "section content"); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	content, version, present, err := readManagedBlock(target)
	if err != nil {
		t.Fatalf("readManagedBlock error: %v", err)
	}
	if !present {
		t.Fatal("expected block to be present")
	}
	if version != managedBlockVersion {
		t.Errorf("version = %d, want %d", version, managedBlockVersion)
	}
	if !strings.Contains(content, "section content") {
		t.Errorf("content = %q, expected to contain 'section content'", content)
	}
}

// TestReadManagedBlock_Absent verifies readManagedBlock returns present=false
// for a file with no managed block.
func TestReadManagedBlock_Absent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(target, []byte("# Just user content\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, _, present, err := readManagedBlock(target)
	if err != nil {
		t.Fatalf("readManagedBlock error: %v", err)
	}
	if present {
		t.Error("expected present=false for file without managed block")
	}
}

// TestReadManagedBlock_FileNotExist verifies readManagedBlock returns
// present=false and no error when the file does not exist.
func TestReadManagedBlock_FileNotExist(t *testing.T) {
	_, _, present, err := readManagedBlock("/tmp/mneme_test_nonexistent_file_xyz.md")
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if present {
		t.Error("expected present=false for non-existent file")
	}
}
