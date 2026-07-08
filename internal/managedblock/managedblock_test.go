package managedblock

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUpsertText_NewFile verifies that upserting into empty text produces a
// file containing only the block, table-driven over a couple of marker/
// version/content combinations.
func TestUpsertText_NewFile(t *testing.T) {
	tests := []struct {
		name    string
		marker  string
		version int
		content string
	}{
		{"managed block v1", "managed", 1, "hello world"},
		{"agent-fixed block v3", "agent-fixed", 3, "capa-1 content"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := UpsertText("", tt.marker, tt.version, tt.content)

			if !strings.Contains(got, StartMarker(tt.marker, tt.version)) {
				t.Errorf("missing start marker in %q", got)
			}
			if !strings.Contains(got, EndMarker(tt.marker)) {
				t.Errorf("missing end marker in %q", got)
			}
			if !strings.Contains(got, tt.content) {
				t.Errorf("missing content in %q", got)
			}
			if !strings.HasSuffix(got, "\n") {
				t.Errorf("expected trailing newline, got %q", got)
			}
		})
	}
}

// TestUpsertText_Idempotent verifies that applying UpsertText twice with
// identical arguments produces byte-identical output, across a table of
// starting texts (empty, plain prose, already-managed).
func TestUpsertText_Idempotent(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{"empty", ""},
		{"plain prose", "# Header\n\nSome prose.\n"},
		{"already managed", UpsertText("# Header\n", "managed", 1, "old content")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first := UpsertText(tt.text, "managed", 1, "stable content")
			second := UpsertText(first, "managed", 1, "stable content")
			if first != second {
				t.Errorf("not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
			}
		})
	}
}

// TestUpsertText_PreservesOutsideMarkers verifies that prose before and after
// the managed block survives an upsert, table-driven over prose placement.
func TestUpsertText_PreservesOutsideMarkers(t *testing.T) {
	tests := []struct {
		name        string
		initial     string
		wantContain []string
	}{
		{
			name:        "prose before only",
			initial:     "# My header\n\nSome user prose here.\n",
			wantContain: []string{"My header", "Some user prose here."},
		},
		{
			name: "prose before and after existing block",
			initial: UpsertText("# Header\n", "managed", 1, "initial content") +
				"\n## Custom section\n\nUser notes here.\n",
			wantContain: []string{"Header", "Custom section", "User notes here."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := UpsertText(tt.initial, "managed", 1, "block content")
			for _, want := range tt.wantContain {
				if !strings.Contains(got, want) {
					t.Errorf("expected %q to survive upsert, got:\n%s", want, got)
				}
			}
			if !strings.Contains(got, "block content") {
				t.Error("new block content missing")
			}
		})
	}
}

// TestUpsertText_ReplacesOldBlock verifies that a pre-existing block (any
// version) is fully replaced, not duplicated, and the version is refreshed.
func TestUpsertText_ReplacesOldBlock(t *testing.T) {
	oldBlock := "<!-- mneme:managed:start v=0 -->\nold stuff\n" + EndMarker("managed") + "\n"

	got := UpsertText(oldBlock, "managed", 1, "new content")

	if !strings.Contains(got, StartMarker("managed", 1)) {
		t.Error("start marker not refreshed to new version")
	}
	if strings.Contains(got, "v=0") {
		t.Error("old version marker still present")
	}
	if strings.Contains(got, "old stuff") {
		t.Error("old content should be replaced, not preserved")
	}
	if !strings.Contains(got, "new content") {
		t.Error("new content missing")
	}
	if strings.Count(got, StartMarker("managed", 1)) != 1 {
		t.Error("block duplicated instead of replaced")
	}
}

// TestReadText roundtrips content written by UpsertText, table-driven over
// present/absent scenarios.
func TestReadText(t *testing.T) {
	tests := []struct {
		name        string
		text        string
		marker      string
		wantContent string
		wantVersion int
		wantPresent bool
	}{
		{
			name:        "present",
			text:        UpsertText("", "managed", 1, "section content"),
			marker:      "managed",
			wantContent: "section content",
			wantVersion: 1,
			wantPresent: true,
		},
		{
			name:        "absent",
			text:        "# Just user content\n",
			marker:      "managed",
			wantContent: "",
			wantVersion: 0,
			wantPresent: false,
		},
		{
			name:        "different marker not matched",
			text:        UpsertText("", "agent-fixed", 2, "capa-1"),
			marker:      "managed",
			wantContent: "",
			wantVersion: 0,
			wantPresent: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, version, present := ReadText(tt.text, tt.marker)
			if present != tt.wantPresent {
				t.Fatalf("present = %v, want %v", present, tt.wantPresent)
			}
			if !present {
				return
			}
			if version != tt.wantVersion {
				t.Errorf("version = %d, want %d", version, tt.wantVersion)
			}
			if !strings.Contains(content, tt.wantContent) {
				t.Errorf("content = %q, want to contain %q", content, tt.wantContent)
			}
		})
	}
}

// TestUpsert_FileBacked verifies the file I/O wrapper: new file creation,
// idempotent re-upsert, and reading it back via Read.
func TestUpsert_FileBacked(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "nested", "AGENTS.md")

	if err := Upsert(target, "managed", 1, "first content"); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	data1, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read after first upsert: %v", err)
	}

	if err := Upsert(target, "managed", 1, "first content"); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	data2, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read after second upsert: %v", err)
	}

	if string(data1) != string(data2) {
		t.Errorf("Upsert not idempotent on disk:\nfirst:\n%s\nsecond:\n%s", data1, data2)
	}

	content, version, present, err := Read(target, "managed")
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if !present {
		t.Fatal("expected block to be present")
	}
	if version != 1 {
		t.Errorf("version = %d, want 1", version)
	}
	if !strings.Contains(content, "first content") {
		t.Errorf("content = %q, expected to contain 'first content'", content)
	}
}

// TestRead_FileNotExist verifies Read returns present=false and no error for
// a non-existent file.
func TestRead_FileNotExist(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist.md")

	_, _, present, err := Read(missing, "managed")
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if present {
		t.Error("expected present=false for non-existent file")
	}
}
