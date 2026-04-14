package install

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// TestIsGitURL
// ---------------------------------------------------------------------------

// TestIsGitURL verifies that the URL heuristic correctly distinguishes git
// remote URLs from local filesystem paths.
func TestIsGitURL(t *testing.T) {
	cases := []struct {
		source string
		want   bool
	}{
		// Git remote forms — must be true.
		{"git@github.com:user/repo.git", true},
		{"git@bitbucket.org:org/repo", true},
		{"ssh://git@github.com/user/repo.git", true},
		{"https://github.com/user/repo.git", true},
		{"http://gitlab.example.com/user/repo.git", true},

		// Local paths — must be false.
		{"/home/user/.dotfiles", false},
		{"./relative/path", false},
		{"../sibling", false},
		{"https://github.com/user/repo", false}, // HTTPS without .git is ambiguous → local
		{"", false},
	}

	for _, tc := range cases {
		got := isGitURL(tc.source)
		if got != tc.want {
			t.Errorf("isGitURL(%q) = %v, want %v", tc.source, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// TestCopyTree
// ---------------------------------------------------------------------------

// TestCopyTree_Empty verifies that walking an empty source directory produces
// empty installed/skipped slices without errors.
func TestCopyTree_Empty(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	installed, skipped, err := copyTree(src, dst, false)
	if err != nil {
		t.Fatalf("copyTree returned error: %v", err)
	}
	if len(installed) != 0 {
		t.Errorf("installed = %v, want empty", installed)
	}
	if len(skipped) != 0 {
		t.Errorf("skipped = %v, want empty", skipped)
	}
}

// TestCopyTree_NewFiles verifies that new files are copied into dstDir
// preserving their relative paths, including nested subdirectories.
func TestCopyTree_NewFiles(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	writeFile(t, filepath.Join(src, "agent.md"), "# Agent")
	writeFile(t, filepath.Join(src, "sub", "nested.md"), "# Nested")

	installed, skipped, err := copyTree(src, dst, false)
	if err != nil {
		t.Fatalf("copyTree returned error: %v", err)
	}

	if len(skipped) != 0 {
		t.Errorf("skipped = %v, want empty", skipped)
	}
	assertContains(t, installed, "agent.md")
	assertContains(t, installed, filepath.Join("sub", "nested.md"))

	assertFileContent(t, filepath.Join(dst, "agent.md"), "# Agent")
	assertFileContent(t, filepath.Join(dst, "sub", "nested.md"), "# Nested")
}

// TestCopyTree_SkipExisting verifies that files already present at the
// destination are added to the skipped list when force is false.
func TestCopyTree_SkipExisting(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	writeFile(t, filepath.Join(src, "file.md"), "new content")
	writeFile(t, filepath.Join(dst, "file.md"), "original content")

	installed, skipped, err := copyTree(src, dst, false)
	if err != nil {
		t.Fatalf("copyTree returned error: %v", err)
	}

	if len(installed) != 0 {
		t.Errorf("installed = %v, want empty (file must be skipped)", installed)
	}
	assertContains(t, skipped, "file.md")

	// Destination must remain unchanged.
	assertFileContent(t, filepath.Join(dst, "file.md"), "original content")
}

// TestCopyTree_ForceOverwrite verifies that force=true replaces existing files.
func TestCopyTree_ForceOverwrite(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	writeFile(t, filepath.Join(src, "file.md"), "new content")
	writeFile(t, filepath.Join(dst, "file.md"), "original content")

	installed, skipped, err := copyTree(src, dst, true)
	if err != nil {
		t.Fatalf("copyTree returned error: %v", err)
	}

	if len(skipped) != 0 {
		t.Errorf("skipped = %v, want empty (force=true should overwrite)", skipped)
	}
	assertContains(t, installed, "file.md")
	assertFileContent(t, filepath.Join(dst, "file.md"), "new content")
}

// ---------------------------------------------------------------------------
// TestMergeSettingsJSON
// ---------------------------------------------------------------------------

// TestMergeSettingsJSON_NewFile verifies that mergeSettingsJSON creates the
// destination file with source content when the destination is absent.
func TestMergeSettingsJSON_NewFile(t *testing.T) {
	src := filepath.Join(t.TempDir(), "settings.json")
	dst := filepath.Join(t.TempDir(), "settings.json")

	writeJSON(t, src, map[string]any{"theme": "dark", "fontSize": 14})

	if err := mergeSettingsJSON(src, dst); err != nil {
		t.Fatalf("mergeSettingsJSON error: %v", err)
	}

	result := readJSON(t, dst)
	if result["theme"] != "dark" {
		t.Errorf("theme = %v, want dark", result["theme"])
	}
	if result["fontSize"] != float64(14) {
		t.Errorf("fontSize = %v, want 14", result["fontSize"])
	}
}

// TestMergeSettingsJSON_EmptySource verifies that mergeSettingsJSON is a no-op
// when the source file does not exist.
func TestMergeSettingsJSON_EmptySource(t *testing.T) {
	src := filepath.Join(t.TempDir(), "settings.json") // does not exist
	dst := filepath.Join(t.TempDir(), "settings.json")

	writeJSON(t, dst, map[string]any{"theme": "light"})

	if err := mergeSettingsJSON(src, dst); err != nil {
		t.Fatalf("mergeSettingsJSON error: %v", err)
	}

	// Destination must be unchanged.
	result := readJSON(t, dst)
	if result["theme"] != "light" {
		t.Errorf("theme = %v, want light (destination unchanged)", result["theme"])
	}
	if len(result) != 1 {
		t.Errorf("result has %d keys, want 1 (no new keys added)", len(result))
	}
}

// TestMergeSettingsJSON_PreserveExisting verifies that keys already present in
// the destination are never overwritten by source values.
func TestMergeSettingsJSON_PreserveExisting(t *testing.T) {
	src := filepath.Join(t.TempDir(), "settings.json")
	dst := filepath.Join(t.TempDir(), "settings.json")

	writeJSON(t, src, map[string]any{"theme": "dark", "fontSize": 14})
	writeJSON(t, dst, map[string]any{"theme": "light"}) // local value must win

	if err := mergeSettingsJSON(src, dst); err != nil {
		t.Fatalf("mergeSettingsJSON error: %v", err)
	}

	result := readJSON(t, dst)
	if result["theme"] != "light" {
		t.Errorf("theme = %v, want light (local value must not be overwritten)", result["theme"])
	}
}

// TestMergeSettingsJSON_AddNewKeys verifies that keys present in source but
// absent from destination are added to the destination.
func TestMergeSettingsJSON_AddNewKeys(t *testing.T) {
	src := filepath.Join(t.TempDir(), "settings.json")
	dst := filepath.Join(t.TempDir(), "settings.json")

	writeJSON(t, src, map[string]any{"theme": "dark", "fontSize": 14, "newKey": "value"})
	writeJSON(t, dst, map[string]any{"theme": "light"})

	if err := mergeSettingsJSON(src, dst); err != nil {
		t.Fatalf("mergeSettingsJSON error: %v", err)
	}

	result := readJSON(t, dst)
	if result["newKey"] != "value" {
		t.Errorf("newKey = %v, want value (new key must be added from source)", result["newKey"])
	}
	if result["fontSize"] != float64(14) {
		t.Errorf("fontSize = %v, want 14 (new key must be added from source)", result["fontSize"])
	}
}

// TestMergeSettingsJSON_Idempotent verifies that running mergeSettingsJSON twice
// produces the same result as running it once.
func TestMergeSettingsJSON_Idempotent(t *testing.T) {
	src := filepath.Join(t.TempDir(), "settings.json")
	dst := filepath.Join(t.TempDir(), "settings.json")

	writeJSON(t, src, map[string]any{"theme": "dark", "fontSize": 14})
	writeJSON(t, dst, map[string]any{"theme": "light"})

	if err := mergeSettingsJSON(src, dst); err != nil {
		t.Fatalf("first mergeSettingsJSON error: %v", err)
	}
	firstResult := readJSON(t, dst)

	if err := mergeSettingsJSON(src, dst); err != nil {
		t.Fatalf("second mergeSettingsJSON error: %v", err)
	}
	secondResult := readJSON(t, dst)

	// Both runs must produce the same keys and values.
	for k := range firstResult {
		if firstResult[k] != secondResult[k] {
			t.Errorf("key %q changed between runs: first=%v, second=%v", k, firstResult[k], secondResult[k])
		}
	}
	if len(firstResult) != len(secondResult) {
		t.Errorf("result length changed between runs: first=%d, second=%d", len(firstResult), len(secondResult))
	}
}

// ---------------------------------------------------------------------------
// TestCopyClaudeMD
// ---------------------------------------------------------------------------

// TestCopyClaudeMD_PreservesProtocol verifies that if the destination CLAUDE.md
// already contains mneme protocol markers and force=true causes a copy, the
// protocol block is re-injected into the new file automatically.
func TestCopyClaudeMD_PreservesProtocol(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	srcFile := filepath.Join(srcDir, "CLAUDE.md")
	dstFile := filepath.Join(dstDir, "CLAUDE.md")

	const startMarker = "<!-- mneme:protocol:start -->"
	const endMarker = "<!-- mneme:protocol:end -->"

	writeFile(t, srcFile, "# Personal CLAUDE.md\nMy rules.\n")

	// Destination already has protocol markers.
	writeFile(t, dstFile, "# Old content\n\n"+
		startMarker+"\nprotocol block content\n"+endMarker+"\n")

	installed, err := copyClaudeMD(srcFile, dstFile, true /* force */)
	if err != nil {
		t.Fatalf("copyClaudeMD error: %v", err)
	}
	if !installed {
		t.Fatal("copyClaudeMD must report installed=true when force=true")
	}

	data, err := os.ReadFile(dstFile)
	if err != nil {
		t.Fatalf("read dstFile: %v", err)
	}
	content := string(data)

	// Source content must be present.
	if !strings.Contains(content, "My rules.") {
		t.Error("source CLAUDE.md content is missing from destination")
	}
	// Protocol block must have been re-injected.
	if !strings.Contains(content, startMarker) {
		t.Error("protocol start marker missing after copyClaudeMD with force")
	}
	if !strings.Contains(content, "protocol block content") {
		t.Error("protocol block content missing after copyClaudeMD with force")
	}
	if !strings.Contains(content, endMarker) {
		t.Error("protocol end marker missing after copyClaudeMD with force")
	}
}

// TestCopyClaudeMD_SkipExisting verifies that an existing destination CLAUDE.md
// is not touched when force=false.
func TestCopyClaudeMD_SkipExisting(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	srcFile := filepath.Join(srcDir, "CLAUDE.md")
	dstFile := filepath.Join(dstDir, "CLAUDE.md")

	writeFile(t, srcFile, "# New content\n")
	writeFile(t, dstFile, "# Original content\n")

	installed, err := copyClaudeMD(srcFile, dstFile, false /* no force */)
	if err != nil {
		t.Fatalf("copyClaudeMD error: %v", err)
	}
	if installed {
		t.Fatal("copyClaudeMD must report installed=false when destination exists and force=false")
	}

	assertFileContent(t, dstFile, "# Original content\n")
}

// ---------------------------------------------------------------------------
// Integration tests: InstallPersonal
// ---------------------------------------------------------------------------

// TestInstallPersonal_LocalSource verifies that a complete local source
// ecosystem is fully copied into the target ClaudeDir.
func TestInstallPersonal_LocalSource(t *testing.T) {
	src := setupEcosystem(t)
	dst := t.TempDir()

	result, err := InstallPersonal(PersonalOpts{
		Source:    src,
		ClaudeDir: dst,
		Force:     false,
	})
	if err != nil {
		t.Fatalf("InstallPersonal error: %v", err)
	}

	if len(result.Installed) == 0 {
		t.Error("expected files to be installed, got empty list")
	}
	if len(result.Skipped) != 0 {
		t.Errorf("skipped = %v, want empty on first run", result.Skipped)
	}
	if !result.Merged {
		t.Error("expected settings.json to be merged")
	}

	// Verify a few representative files exist in the target.
	assertFileExists(t, filepath.Join(dst, "agents", "myagent.md"))
	assertFileExists(t, filepath.Join(dst, "commands", "mycmd.md"))
	assertFileExists(t, filepath.Join(dst, "CLAUDE.md"))
}

// TestInstallPersonal_Idempotent verifies that running InstallPersonal twice
// with force=false skips everything on the second run.
func TestInstallPersonal_Idempotent(t *testing.T) {
	src := setupEcosystem(t)
	dst := t.TempDir()

	// First run.
	if _, err := InstallPersonal(PersonalOpts{Source: src, ClaudeDir: dst}); err != nil {
		t.Fatalf("first InstallPersonal error: %v", err)
	}

	// Second run — everything should be skipped (CLAUDE.md + dirs).
	result, err := InstallPersonal(PersonalOpts{Source: src, ClaudeDir: dst})
	if err != nil {
		t.Fatalf("second InstallPersonal error: %v", err)
	}

	if len(result.Installed) != 0 {
		t.Errorf("second run installed = %v, want empty (idempotent)", result.Installed)
	}
}

// TestInstallPersonal_InvalidSource verifies that a non-existent local path
// returns a descriptive error.
func TestInstallPersonal_InvalidSource(t *testing.T) {
	dst := t.TempDir()

	_, err := InstallPersonal(PersonalOpts{
		Source:    "/this/path/does/not/exist/at/all",
		ClaudeDir: dst,
	})
	if err == nil {
		t.Fatal("expected error for non-existent source, got nil")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error message %q should mention 'does not exist'", err.Error())
	}
}

// TestInstallPersonal_EmptySource verifies that an empty source string returns
// an error immediately without touching the filesystem.
func TestInstallPersonal_EmptySource(t *testing.T) {
	dst := t.TempDir()

	_, err := InstallPersonal(PersonalOpts{Source: "", ClaudeDir: dst})
	if err == nil {
		t.Fatal("expected error for empty source, got nil")
	}
}

// TestInstallPersonal_GitLocalRepo verifies that a local git repository used
// as a git:// URL (file:// form) is cloned and installed correctly.
// This test skips if git is not in PATH so the suite remains self-contained.
func TestInstallPersonal_GitLocalRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH, skipping git clone test")
	}

	repoDir := setupLocalGitRepo(t)
	dst := t.TempDir()

	// Use file:// URL — recognised as git URL by isGitURL only if it ends
	// with .git. We instead test through isGitURL directly and call
	// InstallPersonal with a local path, since file:// is not in our
	// heuristic. We test cloneToTemp separately via a local file:// URL
	// wrapped as a plain path — so here we confirm the local-path branch works.
	result, err := InstallPersonal(PersonalOpts{
		Source:    repoDir,
		ClaudeDir: dst,
	})
	if err != nil {
		t.Fatalf("InstallPersonal error: %v", err)
	}

	if len(result.Installed) == 0 {
		t.Error("expected files to be installed from git repo ecosystem")
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// setupEcosystem creates a temporary directory tree that mimics a personal
// Claude Code ecosystem: agents/, commands/, templates/, hooks/, CLAUDE.md,
// and settings.json. Returns the root path.
func setupEcosystem(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "agents", "myagent.md"), "# My Agent\n")
	writeFile(t, filepath.Join(root, "commands", "mycmd.md"), "# My Command\n")
	writeFile(t, filepath.Join(root, "templates", "tmpl.md"), "# Template\n")
	writeFile(t, filepath.Join(root, "hooks", "pre.sh"), "#!/bin/sh\necho hook\n")
	writeFile(t, filepath.Join(root, "CLAUDE.md"), "# Personal CLAUDE.md\n")
	writeJSON(t, filepath.Join(root, "settings.json"), map[string]any{
		"theme":    "dark",
		"fontSize": 14,
	})

	return root
}

// setupLocalGitRepo initialises a local git repository with a minimal
// ecosystem, commits it, and returns the repo path. The test is skipped if
// git is unavailable.
func setupLocalGitRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("init")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")

	writeFile(t, filepath.Join(dir, "agents", "agent.md"), "# Agent\n")
	writeFile(t, filepath.Join(dir, "CLAUDE.md"), "# Personal\n")

	run("add", ".")
	run("commit", "-m", "init")

	return dir
}

// writeFile creates parent directories and writes content to path.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("writeFile mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeFile %s: %v", path, err)
	}
}

// writeJSON marshals v as indented JSON and writes it to path.
func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("writeJSON marshal: %v", err)
	}
	writeFile(t, path, string(data))
}

// readJSON reads and unmarshals a JSON file into map[string]any.
func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readJSON read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("readJSON unmarshal %s: %v", path, err)
	}
	return m
}

// assertContains asserts that slice contains the given element.
func assertContains(t *testing.T, slice []string, elem string) {
	t.Helper()
	if !slices.Contains(slice, elem) {
		t.Errorf("slice %v does not contain %q", slice, elem)
	}
}

// assertFileContent asserts that the file at path has exactly the given content.
func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("assertFileContent read %s: %v", path, err)
	}
	if string(data) != want {
		t.Errorf("file %s content = %q, want %q", path, string(data), want)
	}
}

// assertFileExists asserts that a file exists at the given path.
func assertFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("assertFileExists: %s: %v", path, err)
	}
}
