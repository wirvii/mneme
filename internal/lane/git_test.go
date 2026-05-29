package lane

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestParseNumStatLine verifies numstat line parsing for text and binary files.
func TestParseNumStatLine(t *testing.T) {
	tests := []struct {
		line    string
		want    FileStat
		wantErr bool
	}{
		{
			line: "10\t5\tinternal/store/sdd.go",
			want: FileStat{Added: 10, Removed: 5, Path: "internal/store/sdd.go"},
		},
		{
			line: "0\t0\tinternal/store/sdd_test.go",
			want: FileStat{Added: 0, Removed: 0, Path: "internal/store/sdd_test.go"},
		},
		{
			// Binary file: numstat outputs "-\t-\t<path>"
			line: "-\t-\tdocs/image.png",
			want: FileStat{Added: 0, Removed: 0, Path: "docs/image.png"},
		},
		{
			line:    "not-a-number\t0\tfile.go",
			wantErr: true,
		},
		{
			line:    "only-one-field",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.line, func(t *testing.T) {
			got, err := parseNumStatLine(tc.line)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil (result: %+v)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestDiffStats_Totals verifies TotalFiles and TotalLines aggregate correctly.
func TestDiffStats_Totals(t *testing.T) {
	stats := &DiffStats{
		Files: []FileStat{
			{Added: 10, Removed: 5, Path: "a.go"},
			{Added: 0, Removed: 0, Path: "image.png"}, // binary
			{Added: 3, Removed: 2, Path: "b.go"},
		},
	}
	if got := stats.TotalFiles(); got != 3 {
		t.Errorf("TotalFiles() = %d, want 3", got)
	}
	if got := stats.TotalLines(); got != 20 {
		t.Errorf("TotalLines() = %d, want 20", got)
	}
}

// TestGitDiffer_NumStat performs an integration test against a temporary git
// repository. It creates a commit, modifies a file, and verifies NumStat output.
func TestGitDiffer_NumStat(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}

	dir := t.TempDir()

	// Initialise a minimal git repo.
	gitInit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	gitInit("init", "-b", "main")
	gitInit("config", "user.email", "test@test.com")
	gitInit("config", "user.name", "Test")

	// Create an initial commit.
	fPath := filepath.Join(dir, "hello.go")
	if err := os.WriteFile(fPath, []byte("package main\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatalf("write hello.go: %v", err)
	}
	gitInit("add", ".")
	gitInit("commit", "-m", "initial")

	// Record the initial commit SHA as our base.
	baseCmd := exec.Command("git", "rev-parse", "HEAD")
	baseCmd.Dir = dir
	baseOut, err := baseCmd.Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	baseRef := string(baseOut[:len(baseOut)-1]) // trim newline

	// Modify the file.
	if err := os.WriteFile(fPath, []byte("package main\n\nfunc Hello() {}\nfunc World() {}\n"), 0o644); err != nil {
		t.Fatalf("modify hello.go: %v", err)
	}
	gitInit("add", ".")
	gitInit("commit", "-m", "add World")

	differ := &GitDiffer{RepoDir: dir}
	stats, err := differ.NumStat(baseRef)
	if err != nil {
		t.Fatalf("NumStat: %v", err)
	}

	if stats.TotalFiles() != 1 {
		t.Errorf("TotalFiles() = %d, want 1", stats.TotalFiles())
	}
	if stats.TotalLines() == 0 {
		t.Error("TotalLines() = 0, expected > 0")
	}
}
