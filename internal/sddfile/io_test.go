package sddfile

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/model"
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

// TestReadRecord_CRLFParsesIdenticallyToLF is SPEC-140 AC1: a backlog
// record checked out with core.autocrlf=true's "\r\n" line endings must
// read back field-for-field identical to the same bytes with plain "\n".
// The fixture is derived from MarshalBacklog — never a hand-typed string —
// so the case stays honest against whatever the writer actually produces,
// and its body deliberately contains the literal "<!-- mneme:" escape
// sequence this repository's own backlog already exercises for real.
func TestReadRecord_CRLFParsesIdenticallyToLF(t *testing.T) {
	base, err := time.Parse(time.RFC3339Nano, "2026-09-03T17:19:36.101752Z")
	if err != nil {
		t.Fatalf("parse fixture time: %v", err)
	}
	rec := &BacklogRecord{
		Item: &model.BacklogItem{
			ID: "BL-140", Title: "CRLF fixture", Status: model.BacklogStatusRefined,
			Priority: model.PriorityHigh, Project: "wirvii/mneme", Lane: model.LaneStandard,
			Description: "body with an escape case: <!-- mneme:managed:start v=1 -->",
			CreatedAt:   base, UpdatedAt: base,
		},
		Refinements: []*model.BacklogRefinement{
			{ItemID: "BL-140", Seq: 1, Body: "refinement body", By: "architect", At: base},
		},
	}

	lfBytes, err := MarshalBacklog(rec)
	if err != nil {
		t.Fatalf("MarshalBacklog: %v", err)
	}
	if !strings.Contains(string(lfBytes), "<!-- mneme:") {
		t.Fatalf("fixture lost its own escape sequence — the case is not exercising it: %s", lfBytes)
	}
	crlfBytes := []byte(strings.ReplaceAll(string(lfBytes), "\n", "\r\n"))

	dir := t.TempDir()
	lfPath := filepath.Join(dir, "lf.md")
	crlfPath := filepath.Join(dir, "crlf.md")
	if err := os.WriteFile(lfPath, lfBytes, 0o644); err != nil {
		t.Fatalf("write lf fixture: %v", err)
	}
	if err := os.WriteFile(crlfPath, crlfBytes, 0o644); err != nil {
		t.Fatalf("write crlf fixture: %v", err)
	}

	lfData, err := ReadRecord(lfPath)
	if err != nil {
		t.Fatalf("ReadRecord(lf): %v", err)
	}
	crlfData, err := ReadRecord(crlfPath)
	if err != nil {
		t.Fatalf("ReadRecord(crlf): %v", err)
	}

	lfRec, err := UnmarshalBacklog(lfData)
	if err != nil {
		t.Fatalf("UnmarshalBacklog(lf): %v", err)
	}
	crlfRec, err := UnmarshalBacklog(crlfData)
	if err != nil {
		t.Fatalf("UnmarshalBacklog(crlf): %v", err)
	}

	if !reflect.DeepEqual(lfRec, crlfRec) {
		t.Fatalf("CRLF record must parse identically to LF.\nLF:   %+v\nCRLF: %+v", lfRec, crlfRec)
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
