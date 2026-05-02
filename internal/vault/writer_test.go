package vault

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juanftp/mneme/internal/model"
)

// newTestMemory creates a minimal Memory for use in writer tests.
func newTestMemory(id, topicKey, content string, updatedAt time.Time) *model.Memory {
	return &model.Memory{
		ID:        id,
		Type:      model.TypeDecision,
		Scope:     model.ScopeProject,
		Title:     "Test memory " + id[:8],
		TopicKey:  topicKey,
		Content:   content,
		CreatedAt: updatedAt.Add(-time.Hour),
		UpdatedAt: updatedAt,
		Project:   "test/project",
	}
}

func newTestWriter(t *testing.T, root string) *Writer {
	t.Helper()
	return NewWriter(ExportOptions{
		VaultRoot: root,
		Project:   "test/project",
		Scope:     "project",
	})
}

func TestWriter_NewFile(t *testing.T) {
	root := t.TempDir()
	w := newTestWriter(t, root)

	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m := newTestMemory("019ddc45-0000-0000-0000-000000000001", "architecture/test", "hello content", ts)

	written, relPath, err := w.WriteMemory(m)
	if err != nil {
		t.Fatalf("WriteMemory failed: %v", err)
	}
	if !written {
		t.Error("WriteMemory should have written the file")
	}

	absPath := filepath.Join(root, relPath)
	data, err := os.ReadFile(absPath)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", absPath, err)
	}
	out := string(data)

	if !strings.Contains(out, "hello content") {
		t.Error("file does not contain the expected content")
	}
	if !strings.HasPrefix(out, "---\n") {
		t.Error("file should start with frontmatter")
	}
}

func TestWriter_UpdateFile(t *testing.T) {
	root := t.TempDir()
	w := newTestWriter(t, root)

	ts1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ts2 := ts1.Add(time.Hour)

	m1 := newTestMemory("019ddc45-0000-0000-0000-000000000002", "spec/decision", "original content", ts1)
	if _, _, err := w.WriteMemory(m1); err != nil {
		t.Fatalf("first write failed: %v", err)
	}

	m2 := newTestMemory("019ddc45-0000-0000-0000-000000000002", "spec/decision", "updated content", ts2)
	written, _, err := w.WriteMemory(m2)
	if err != nil {
		t.Fatalf("second write failed: %v", err)
	}
	if !written {
		t.Error("WriteMemory should have updated the file (newer updated_at)")
	}

	absPath := filepath.Join(root, MemoryPath(m2))
	data, _ := os.ReadFile(absPath)
	if !strings.Contains(string(data), "updated content") {
		t.Error("file should contain updated content")
	}
}

func TestWriter_SkipUnchanged(t *testing.T) {
	root := t.TempDir()
	w := newTestWriter(t, root)

	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m := newTestMemory("019ddc45-0000-0000-0000-000000000003", "spec/unchanged", "content v1", ts)

	if _, _, err := w.WriteMemory(m); err != nil {
		t.Fatalf("first write failed: %v", err)
	}

	// Second write with same updated_at — should be skipped.
	// Use a fresh writer to reset usedPaths, simulating a second export run.
	w2 := newTestWriter(t, root)
	written, _, err := w2.WriteMemory(m)
	if err != nil {
		t.Fatalf("second write failed: %v", err)
	}
	if written {
		t.Error("WriteMemory should have skipped the unchanged file")
	}
}

func TestWriter_AtomicRename(t *testing.T) {
	// Verify no .tmp files exist after a successful write.
	root := t.TempDir()
	w := newTestWriter(t, root)

	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m := newTestMemory("019ddc45-0000-0000-0000-000000000004", "spec/atomic", "atomic content", ts)

	if _, _, err := w.WriteMemory(m); err != nil {
		t.Fatalf("WriteMemory failed: %v", err)
	}

	// Walk root and fail if any .tmp files remain.
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, _ error) error {
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".tmp") {
			t.Errorf("stale .tmp file found after successful write: %s", path)
		}
		return nil
	})
}

func TestWriter_MarkerFile(t *testing.T) {
	root := t.TempDir()
	w := newTestWriter(t, root)

	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	memories := []*model.Memory{
		newTestMemory("019ddc45-0000-0000-0000-000000000005", "arch/marker-test", "content", ts),
	}

	result, err := w.Export(memories)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}
	if result.Written != 1 {
		t.Errorf("expected 1 written; got %d", result.Written)
	}

	markerPath := filepath.Join(root, markerFile)
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("marker file not found: %v", err)
	}

	var m VaultMarker
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("cannot parse marker JSON: %v", err)
	}
	if m.VaultVersion != 1 {
		t.Errorf("expected vault_version=1; got %d", m.VaultVersion)
	}
	if m.Project != "test/project" {
		t.Errorf("expected project=test/project; got %q", m.Project)
	}
	if m.Scope != "project" {
		t.Errorf("expected scope=project; got %q", m.Scope)
	}
	if m.MemoryCount != 1 {
		t.Errorf("expected memory_count=1; got %d", m.MemoryCount)
	}
}

func TestWriter_ProjectMismatch(t *testing.T) {
	root := t.TempDir()

	// First export: establish vault as "project-a".
	w1 := NewWriter(ExportOptions{VaultRoot: root, Project: "project-a", Scope: "project"})
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m := newTestMemory("019ddc45-0000-0000-0000-000000000006", "arch/mismatch", "content", ts)
	if _, err := w1.Export([]*model.Memory{m}); err != nil {
		t.Fatalf("first export failed: %v", err)
	}

	// Second export: attempt to write "project-b" into the same vault.
	w2 := NewWriter(ExportOptions{VaultRoot: root, Project: "project-b", Scope: "project"})
	_, err := w2.Export([]*model.Memory{m})
	if err == nil {
		t.Error("Export should fail when project mismatches vault marker")
	}
	if !strings.Contains(err.Error(), "project-a") {
		t.Errorf("error should mention original project 'project-a', got: %v", err)
	}
}

func TestWriter_DryRun(t *testing.T) {
	root := t.TempDir()
	w := NewWriter(ExportOptions{
		VaultRoot: root,
		Project:   "test/project",
		Scope:     "project",
		DryRun:    true,
	})

	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	memories := []*model.Memory{
		newTestMemory("019ddc45-0000-0000-0000-000000000007", "arch/dry1", "content 1", ts),
		newTestMemory("019ddc45-0000-0000-0000-000000000008", "arch/dry2", "content 2", ts),
	}

	result, err := w.Export(memories)
	if err != nil {
		t.Fatalf("dry-run Export failed: %v", err)
	}

	if result.Written != 2 {
		t.Errorf("dry-run should report 2 would-write; got %d", result.Written)
	}

	// No files should actually exist.
	notesDir := filepath.Join(root, "notes")
	if _, err := os.Stat(notesDir); !os.IsNotExist(err) {
		t.Error("dry-run should not create any directories or files")
	}

	// No marker file.
	if _, err := os.Stat(filepath.Join(root, markerFile)); !os.IsNotExist(err) {
		t.Error("dry-run should not write the marker file")
	}
}

func TestWriter_PathCollision(t *testing.T) {
	root := t.TempDir()
	w := newTestWriter(t, root)

	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Two memories whose topic_keys produce the same sanitized path.
	// "arch:v2" and "arch_v2" both sanitize to "notes/arch_v2.md".
	m1 := newTestMemory("019ddc45-0000-0000-0000-000000000009", "arch:v2", "first", ts)
	m2 := newTestMemory("019ddc45-0000-0000-0000-000000000010", "arch_v2", "second", ts.Add(time.Minute))

	written1, path1, err1 := w.WriteMemory(m1)
	written2, path2, err2 := w.WriteMemory(m2)

	if err1 != nil || err2 != nil {
		t.Fatalf("WriteMemory errors: %v, %v", err1, err2)
	}
	if !written1 || !written2 {
		t.Error("both memories should be written")
	}
	if path1 == path2 {
		t.Errorf("collision: both memories got the same path %q", path1)
	}

	// Both files should exist on disk.
	if _, err := os.Stat(filepath.Join(root, path1)); err != nil {
		t.Errorf("file %s not found: %v", path1, err)
	}
	if _, err := os.Stat(filepath.Join(root, path2)); err != nil {
		t.Errorf("file %s not found: %v", path2, err)
	}
}

func TestWriter_EmptyVault(t *testing.T) {
	root := t.TempDir()
	w := newTestWriter(t, root)

	result, err := w.Export(nil)
	if err != nil {
		t.Fatalf("Export with zero memories failed: %v", err)
	}
	if result.Total != 0 || result.Written != 0 {
		t.Errorf("expected 0 total/written; got total=%d written=%d", result.Total, result.Written)
	}

	// notes/ directory should NOT be created.
	notesDir := filepath.Join(root, "notes")
	if _, err := os.Stat(notesDir); !os.IsNotExist(err) {
		t.Error("notes/ dir should not be created when there are no memories")
	}

	// Marker should still be written.
	if _, err := os.Stat(filepath.Join(root, markerFile)); err != nil {
		t.Errorf("marker file should exist even with zero memories: %v", err)
	}
}

func TestWriter_DryRun_Paths(t *testing.T) {
	root := t.TempDir()
	w := NewWriter(ExportOptions{
		VaultRoot: root,
		Project:   "test/project",
		Scope:     "project",
		DryRun:    true,
	})

	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	memories := make([]*model.Memory, 25)
	for i := range memories {
		id := "019ddc45-0000-0000-0000-0000000000" + fmt.Sprintf("%02d", i)
		memories[i] = newTestMemory(id, fmt.Sprintf("arch/note-%02d", i), "content", ts)
	}

	result, err := w.Export(memories)
	if err != nil {
		t.Fatalf("dry-run Export failed: %v", err)
	}

	if len(result.Paths) != 20 {
		t.Errorf("Paths should be capped at 20; got %d", len(result.Paths))
	}
}

