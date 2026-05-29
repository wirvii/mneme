package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDetectDrift_DuplicatedSection verifies that a heading matching a canonical
// section produces a drift finding in category (a).
func TestDetectDrift_DuplicatedSection(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "CLAUDE.md")

	content := "# My Project\n\n" +
		"## Session lifecycle\n\n" +
		"- Always do mem_search first.\n"
	if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	findings, err := DetectDrift(f)
	if err != nil {
		t.Fatalf("DetectDrift error: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("expected at least one finding for duplicated section")
	}

	var found bool
	for _, fi := range findings {
		if strings.Contains(fi.Message, "duplicates global manual section") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'duplicates global manual section' finding, got: %v", findings)
	}
}

// TestDetectDrift_Contradiction verifies that an enforcement-contradicting phrase
// produces a finding in category (b).
func TestDetectDrift_Contradiction(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "CLAUDE.md")

	content := "# My Project\n\n" +
		"The orchestrator can edit code directly when needed.\n"
	if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	findings, err := DetectDrift(f)
	if err != nil {
		t.Fatalf("DetectDrift error: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("expected at least one finding for contradiction")
	}

	var found bool
	for _, fi := range findings {
		if strings.Contains(fi.Message, "contradicts enforcement") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'contradicts enforcement' finding, got: %v", findings)
	}
}

// TestDetectDrift_CleanRepo verifies that a clean project CLAUDE.md with no
// duplicated sections and no contradictions produces zero findings.
func TestDetectDrift_CleanRepo(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "CLAUDE.md")

	content := "# My Project\n\n" +
		"## Stack\n\nGo 1.24, SQLite.\n\n" +
		"## Conventions\n\nConventional Commits.\n"
	if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	findings, err := DetectDrift(f)
	if err != nil {
		t.Fatalf("DetectDrift error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected zero findings for clean file, got: %v", findings)
	}
}

// TestDetectDrift_NeverEditsFile verifies that DetectDrift never modifies the
// file it is scanning (read-only guarantee).
func TestDetectDrift_NeverEditsFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "CLAUDE.md")

	original := "## Session lifecycle\n\nOrchestrator can edit code here.\n"
	if err := os.WriteFile(f, []byte(original), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := DetectDrift(f)
	if err != nil {
		t.Fatalf("DetectDrift error: %v", err)
	}

	data, err := os.ReadFile(f)
	if err != nil {
		t.Fatalf("read after drift: %v", err)
	}
	if string(data) != original {
		t.Error("DetectDrift modified the file (must never write)")
	}
}

// TestDetectDrift_SkipsManagedBlock verifies that content inside the managed
// block is not flagged even when it contains canonical section headings.
func TestDetectDrift_SkipsManagedBlock(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "CLAUDE.md")

	// The managed block itself contains "Session lifecycle" — should not flag.
	content := "# My Project\n\n" +
		"<!-- mneme:managed:start v=1 -->\n" +
		"## Session lifecycle\n\n" +
		"Orchestrator can edit code.\n" +
		"<!-- mneme:managed:end -->\n\n" +
		"## My Stack\n\nGo.\n"
	if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	findings, err := DetectDrift(f)
	if err != nil {
		t.Fatalf("DetectDrift error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected zero findings (content inside managed block should be ignored), got: %v", findings)
	}
}

// TestDetectDrift_FileNotExist verifies that DetectDrift returns nil findings
// and nil error when the file does not exist.
func TestDetectDrift_FileNotExist(t *testing.T) {
	findings, err := DetectDrift("/tmp/mneme_drift_test_nonexistent_xyz.md")
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected zero findings for missing file, got: %v", findings)
	}
}

// TestDriftFinding_String verifies the formatted output of DriftFinding.String().
func TestDriftFinding_String(t *testing.T) {
	fi := DriftFinding{
		File:    "/path/to/CLAUDE.md",
		Line:    42,
		Message: "some advisory message",
	}
	got := fi.String()
	if !strings.Contains(got, "/path/to/CLAUDE.md") {
		t.Errorf("String() missing file path: %q", got)
	}
	if !strings.Contains(got, "42") {
		t.Errorf("String() missing line number: %q", got)
	}
	if !strings.Contains(got, "some advisory message") {
		t.Errorf("String() missing message: %q", got)
	}
}
