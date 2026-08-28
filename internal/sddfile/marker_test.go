package sddfile

import "testing"

func TestReadMarker_AbsentReturnsNilNoError(t *testing.T) {
	m, err := ReadMarker(t.TempDir())
	if err != nil {
		t.Fatalf("ReadMarker on a repo with no marker: %v", err)
	}
	if m != nil {
		t.Errorf("ReadMarker = %+v, want nil for an absent marker", m)
	}
}

func TestWriteMarker_ThenReadMarker_RoundTrips(t *testing.T) {
	repoRoot := t.TempDir()
	want := Marker{
		SDDVersion: 1, Project: "wirvii/mneme",
		CreatedAt: "2026-08-28T10:00:00Z", LastExportAt: "2026-08-28T10:00:00Z",
		BacklogCount: 194, SpecCount: 127,
	}
	if err := WriteMarker(repoRoot, want); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}
	got, err := ReadMarker(repoRoot)
	if err != nil {
		t.Fatalf("ReadMarker: %v", err)
	}
	if got == nil {
		t.Fatal("ReadMarker returned nil after WriteMarker")
	}
	if *got != want {
		t.Errorf("ReadMarker = %+v, want %+v", *got, want)
	}
}
