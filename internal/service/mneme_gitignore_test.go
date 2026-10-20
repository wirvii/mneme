package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testFirstEntry  = "first.entry"
	testSecondEntry = "second/"
)

// TestEnsureMnemeGitignore_CreatesFileWithEntriesInOrder verifies that
// ensureMnemeGitignore creates <repoRoot>/.mneme/.gitignore containing every
// entry given, in order — first entry before second.
func TestEnsureMnemeGitignore_CreatesFileWithEntriesInOrder(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, ".mneme"), 0o755); err != nil {
		t.Fatalf("mkdir .mneme: %v", err)
	}

	if err := ensureMnemeGitignore(repoRoot, testFirstEntry, testSecondEntry); err != nil {
		t.Fatalf("ensureMnemeGitignore: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(repoRoot, ".mneme", ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	got := string(data)
	lockIdx := strings.Index(got, testFirstEntry)
	backupsIdx := strings.Index(got, testSecondEntry)
	if lockIdx == -1 || backupsIdx == -1 {
		t.Fatalf("expected both entries present, got %q", got)
	}
	if lockIdx > backupsIdx {
		t.Errorf("expected %q before %q, got %q", testFirstEntry, testSecondEntry, got)
	}
}

// TestEnsureMnemeGitignore_IdempotentAndPreservesExistingContent verifies
// that calling ensureMnemeGitignore twice against a .gitignore with
// pre-existing, hand-authored content never duplicates any entry and never
// disturbs what was already there.
func TestEnsureMnemeGitignore_IdempotentAndPreservesExistingContent(t *testing.T) {
	repoRoot := t.TempDir()
	mnemeDir := filepath.Join(repoRoot, ".mneme")
	if err := os.MkdirAll(mnemeDir, 0o755); err != nil {
		t.Fatalf("mkdir .mneme: %v", err)
	}
	gitignorePath := filepath.Join(mnemeDir, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte("# hand-authored note\nsome-other-entry\n"), 0o644); err != nil {
		t.Fatalf("seed .gitignore: %v", err)
	}

	if err := ensureMnemeGitignore(repoRoot, testFirstEntry, testSecondEntry); err != nil {
		t.Fatalf("ensureMnemeGitignore (first call): %v", err)
	}
	if err := ensureMnemeGitignore(repoRoot, testFirstEntry, testSecondEntry); err != nil {
		t.Fatalf("ensureMnemeGitignore (second call): %v", err)
	}

	data, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "some-other-entry") {
		t.Errorf("expected pre-existing entry preserved, got %q", got)
	}
	if strings.Count(got, testFirstEntry) != 1 {
		t.Errorf("expected exactly one %q entry after two calls, got %q", testFirstEntry, got)
	}
	if strings.Count(got, testSecondEntry) != 1 {
		t.Errorf("expected exactly one %q entry after two calls, got %q", testSecondEntry, got)
	}
}

// TestEnsureMnemeGitignore_OneEntryAlreadyPresentOnlyAddsMissing verifies
// that when one of the entries already exists, only the missing one is
// appended — not both re-written.
func TestEnsureMnemeGitignore_OneEntryAlreadyPresentOnlyAddsMissing(t *testing.T) {
	repoRoot := t.TempDir()
	mnemeDir := filepath.Join(repoRoot, ".mneme")
	if err := os.MkdirAll(mnemeDir, 0o755); err != nil {
		t.Fatalf("mkdir .mneme: %v", err)
	}
	gitignorePath := filepath.Join(mnemeDir, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte(testFirstEntry+"\n"), 0o644); err != nil {
		t.Fatalf("seed .gitignore: %v", err)
	}

	if err := ensureMnemeGitignore(repoRoot, testFirstEntry, testSecondEntry); err != nil {
		t.Fatalf("ensureMnemeGitignore: %v", err)
	}

	data, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	got := string(data)
	if strings.Count(got, testFirstEntry) != 1 {
		t.Errorf("expected exactly one %q entry, got %q", testFirstEntry, got)
	}
	if !strings.Contains(got, testSecondEntry) {
		t.Errorf("expected the missing %q entry to be appended, got %q", testSecondEntry, got)
	}
}

// TestEnsureMnemeGitignore_ReadErrorPropagates verifies that a read failure
// (here: the .gitignore path is itself a directory) is surfaced, not
// silently swallowed.
func TestEnsureMnemeGitignore_ReadErrorPropagates(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, ".mneme", ".gitignore"), 0o755); err != nil {
		t.Fatalf("mkdir .gitignore as directory: %v", err)
	}

	if err := ensureMnemeGitignore(repoRoot, testFirstEntry, testSecondEntry); err == nil {
		t.Fatal("expected an error when the .gitignore path is a directory")
	}
}

// TestEnsureMnemeGitignore_WriteErrorPropagates verifies that a write
// failure (here: a read-only .mneme directory) is surfaced. Skipped when
// running as root, since permission checks are bypassed for root and the
// injected failure would never occur.
func TestEnsureMnemeGitignore_WriteErrorPropagates(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission-based write-failure injection does not apply")
	}

	repoRoot := t.TempDir()
	mnemeDir := filepath.Join(repoRoot, ".mneme")
	if err := os.MkdirAll(mnemeDir, 0o755); err != nil {
		t.Fatalf("mkdir .mneme: %v", err)
	}
	if err := os.Chmod(mnemeDir, 0o555); err != nil {
		t.Fatalf("chmod .mneme read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(mnemeDir, 0o755) })

	if err := ensureMnemeGitignore(repoRoot, testFirstEntry, testSecondEntry); err == nil {
		t.Fatal("expected an error writing .gitignore into a read-only .mneme directory")
	}
}
