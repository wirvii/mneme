package sddfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteRecord_AtomicAndReadBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "backlog", "BL-001.md")

	if err := WriteRecord(path, []byte("hello")); err != nil {
		t.Fatalf("WriteRecord: %v", err)
	}

	got, err := ReadRecord(path)
	if err != nil {
		t.Fatalf("ReadRecord: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("ReadRecord = %q, want %q", got, "hello")
	}

	// No leftover tmp file after a successful write.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("leftover tmp file after WriteRecord: %s", e.Name())
		}
	}

	// A second write overwrites cleanly.
	if err := WriteRecord(path, []byte("updated")); err != nil {
		t.Fatalf("WriteRecord (second): %v", err)
	}
	got, err = ReadRecord(path)
	if err != nil {
		t.Fatalf("ReadRecord (second): %v", err)
	}
	if string(got) != "updated" {
		t.Errorf("ReadRecord (second) = %q, want %q", got, "updated")
	}
}

func TestCleanStaleTmp_RemovesOrphans(t *testing.T) {
	dir := t.TempDir()
	orphan := filepath.Join(dir, ".BL-001.md.tmp")
	if err := os.WriteFile(orphan, []byte("stale"), 0o644); err != nil {
		t.Fatalf("write orphan: %v", err)
	}
	real := filepath.Join(dir, "BL-002.md")
	if err := os.WriteFile(real, []byte("real"), 0o644); err != nil {
		t.Fatalf("write real: %v", err)
	}

	if err := CleanStaleTmp(dir); err != nil {
		t.Fatalf("CleanStaleTmp: %v", err)
	}

	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("orphan tmp file still exists after CleanStaleTmp")
	}
	if _, err := os.Stat(real); err != nil {
		t.Errorf("CleanStaleTmp removed a real file: %v", err)
	}
}

func TestListRecords_FindsOnlyMarkdown(t *testing.T) {
	dir := t.TempDir()
	mustWrite := func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustWrite("backlog/BL-001.md", "a")
	mustWrite("specs/SPEC-001/record.md", "b")
	mustWrite(".mneme-sdd", `{"sdd_version":1}`)

	got, err := ListRecords(dir)
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListRecords found %d files, want 2 (.md only): %v", len(got), got)
	}
}

func TestListRecords_NonExistentDirReturnsEmpty(t *testing.T) {
	got, err := ListRecords(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("ListRecords on missing dir: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no records, got %v", got)
	}
}
