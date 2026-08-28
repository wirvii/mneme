package sddfile

import (
	"path/filepath"
	"testing"
)

func TestRootDir(t *testing.T) {
	got := RootDir("/repo")
	want := filepath.Join("/repo", ".mneme", "sdd")
	if got != want {
		t.Errorf("RootDir = %q, want %q", got, want)
	}
}

func TestBacklogPath(t *testing.T) {
	got := BacklogPath("/repo", "BL-194")
	want := filepath.Join("/repo", ".mneme", "sdd", "backlog", "BL-194.md")
	if got != want {
		t.Errorf("BacklogPath = %q, want %q", got, want)
	}
}

func TestSpecDir(t *testing.T) {
	got := SpecDir("/repo", "SPEC-130")
	want := filepath.Join("/repo", ".mneme", "sdd", "specs", "SPEC-130")
	if got != want {
		t.Errorf("SpecDir = %q, want %q", got, want)
	}
}

// TestSpecRecordPath_IsAlwaysRecordMD is D39 (firm): the spec record file
// is NEVER named spec.md — that name belongs to model.SpecDocKind's closed
// vocabulary, which BL-196 deposits in the same directory.
func TestSpecRecordPath_IsAlwaysRecordMD(t *testing.T) {
	got := SpecRecordPath("/repo", "SPEC-130")
	want := filepath.Join("/repo", ".mneme", "sdd", "specs", "SPEC-130", "record.md")
	if got != want {
		t.Errorf("SpecRecordPath = %q, want %q", got, want)
	}
	if filepath.Base(got) == "spec.md" {
		t.Fatal("SpecRecordPath must never resolve to spec.md (D39)")
	}
}
