package vault

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/model"
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
	if strings.Contains(out, "sdd_refs:") {
		t.Errorf("a memory with no SDDRefs must never write sdd_refs: (AC12)\nGot:\n%s", out)
	}
}

// TestWriter_SDDRefs writes a memory with an anchored SDD reference through
// the real Writer end to end (not just FromMemory/WriteTo in isolation) and
// confirms the anchor lands in the file exactly as "REF=UUID".
func TestWriter_SDDRefs(t *testing.T) {
	root := t.TempDir()
	w := newTestWriter(t, root)

	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m := newTestMemory("019ddc45-0000-0000-0000-000000000010", "architecture/sdd-refs", "cites SPEC-125", ts)
	m.SDDRefs = []model.SDDRef{
		{RefID: "SPEC-125", TargetUUID: "0198f2c1-4a7b-7c3d-9e10-3f4a5b6c7d8e"},
	}

	_, relPath, err := w.WriteMemory(m)
	if err != nil {
		t.Fatalf("WriteMemory failed: %v", err)
	}

	absPath := filepath.Join(root, relPath)
	data, err := os.ReadFile(absPath)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", absPath, err)
	}
	out := string(data)

	if !strings.Contains(out, "sdd_refs:\n  - SPEC-125=0198f2c1-4a7b-7c3d-9e10-3f4a5b6c7d8e\n") {
		t.Errorf("expected the anchored reference in the written file\nGot:\n%s", out)
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

// TestWriter_SkipUnchanged_SubSecondPrecision verifies that idempotency holds
// when the memory's UpdatedAt carries sub-second precision (nanoseconds). This
// was the root cause of SPEC-023 CRIT-1: RFC3339 truncates to the second, so
// the on-disk timestamp was always slightly older than the DB timestamp, causing
// every export run to rewrite all files.
func TestWriter_SkipUnchanged_SubSecondPrecision(t *testing.T) {
	root := t.TempDir()
	w := newTestWriter(t, root)

	// Use a timestamp that contains sub-second precision.
	ts := time.Date(2026, 5, 2, 1, 34, 34, 729792000, time.UTC) // 729792 µs
	m := newTestMemory("019ddc45-0000-0000-0000-000000000042", "spec/subsecond", "content v1", ts)

	// First export — must write the file.
	written, _, err := w.WriteMemory(m)
	if err != nil {
		t.Fatalf("first WriteMemory failed: %v", err)
	}
	if !written {
		t.Fatal("first WriteMemory should have written the file")
	}

	// Second export with the same memory — must skip (idempotent round-trip).
	w2 := newTestWriter(t, root)
	written2, _, err := w2.WriteMemory(m)
	if err != nil {
		t.Fatalf("second WriteMemory failed: %v", err)
	}
	if written2 {
		t.Error("second WriteMemory should have skipped the unchanged file (sub-second idempotency broken)")
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

// TestWriter_PathModeUUID_FlatByID verifies that a Writer configured with
// PathModeUUID writes to notes/<uuid>.md regardless of topic_key, and that
// two distinct memories (even with the same topic_key) land in two distinct
// files — the git-native team-memory shared vault layout (SPEC-053 D1).
func TestWriter_PathModeUUID_FlatByID(t *testing.T) {
	root := t.TempDir()
	w := NewWriter(ExportOptions{
		VaultRoot: root,
		Project:   "test/project",
		Scope:     "shared",
		PathMode:  PathModeUUID,
	})

	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m1 := newTestMemory("019ddc45-0000-0000-0000-000000000101", "same/topic-key", "first memory", ts)
	m2 := newTestMemory("019ddc45-0000-0000-0000-000000000102", "same/topic-key", "second memory", ts)

	written1, path1, err1 := w.WriteMemory(m1)
	written2, path2, err2 := w.WriteMemory(m2)
	if err1 != nil || err2 != nil {
		t.Fatalf("WriteMemory errors: %v, %v", err1, err2)
	}
	if !written1 || !written2 {
		t.Error("both memories should be written")
	}

	wantPath1 := "notes/019ddc45-0000-0000-0000-000000000101.md"
	wantPath2 := "notes/019ddc45-0000-0000-0000-000000000102.md"
	if path1 != wantPath1 {
		t.Errorf("path1 = %q, want %q", path1, wantPath1)
	}
	if path2 != wantPath2 {
		t.Errorf("path2 = %q, want %q", path2, wantPath2)
	}
	if path1 == path2 {
		t.Fatal("distinct memories must never share a PathModeUUID path")
	}

	for _, p := range []string{path1, path2} {
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			t.Errorf("file %s not found: %v", p, err)
		}
	}
}

// TestWriter_PathModeUUID_ResaveRewritesSameFile verifies that writing the
// same memory ID twice (e.g. a re-save after edits) targets the same file
// path both times, and the second write updates its content.
func TestWriter_PathModeUUID_ResaveRewritesSameFile(t *testing.T) {
	root := t.TempDir()

	id := "019ddc45-0000-0000-0000-000000000201"
	ts1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ts2 := ts1.Add(time.Hour)

	w1 := NewWriter(ExportOptions{VaultRoot: root, Project: "test/project", Scope: "shared", PathMode: PathModeUUID})
	m1 := newTestMemory(id, "spec/decision", "original content", ts1)
	if _, path1, err := w1.WriteMemory(m1); err != nil {
		t.Fatalf("first write failed: %v", err)
	} else if path1 != "notes/"+id+".md" {
		t.Fatalf("unexpected first path: %q", path1)
	}

	// Simulate a fresh Writer per Save call (mirrors how MemoryService
	// constructs a new vault.Writer for each write-through materialization).
	w2 := NewWriter(ExportOptions{VaultRoot: root, Project: "test/project", Scope: "shared", PathMode: PathModeUUID})
	m2 := newTestMemory(id, "spec/decision", "updated content", ts2)
	written, path2, err := w2.WriteMemory(m2)
	if err != nil {
		t.Fatalf("second write failed: %v", err)
	}
	if !written {
		t.Error("second write should have updated the file (newer updated_at)")
	}
	if path2 != "notes/"+id+".md" {
		t.Fatalf("resave must target the same path; got %q", path2)
	}

	data, err := os.ReadFile(filepath.Join(root, path2))
	if err != nil {
		t.Fatalf("read rewritten file: %v", err)
	}
	if !strings.Contains(string(data), "updated content") {
		t.Error("rewritten file should contain updated content")
	}
}
