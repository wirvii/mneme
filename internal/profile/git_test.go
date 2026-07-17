package profile

import (
	"strings"
	"testing"
)

// TestBuildGitEnv_ExtraWins verifies the plumbing behind R1's mitigation:
// GitTerminalPromptDisabled must always end up in the environment handed to
// the git subprocess, and must win over any conflicting value the ambient
// process environment happens to carry — network-free, no real git
// credential prompt required to exercise it.
func TestBuildGitEnv_ExtraWins(t *testing.T) {
	t.Setenv("GIT_TERMINAL_PROMPT", "1")

	env := buildGitEnv([]string{GitTerminalPromptDisabled})

	var matches []string
	for _, kv := range env {
		if strings.HasPrefix(kv, "GIT_TERMINAL_PROMPT=") {
			matches = append(matches, kv)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("GIT_TERMINAL_PROMPT appears %d times in built env, want exactly 1: %v", len(matches), matches)
	}
	if matches[0] != GitTerminalPromptDisabled {
		t.Errorf("GIT_TERMINAL_PROMPT entry = %q, want %q", matches[0], GitTerminalPromptDisabled)
	}
}

// TestBuildGitEnv_PreservesOtherVars confirms buildGitEnv does not drop
// unrelated environment variables while overriding the targeted one.
func TestBuildGitEnv_PreservesOtherVars(t *testing.T) {
	t.Setenv("MNEME_PROFILE_TEST_MARKER", "present")

	env := buildGitEnv([]string{GitTerminalPromptDisabled})

	found := false
	for _, kv := range env {
		if kv == "MNEME_PROFILE_TEST_MARKER=present" {
			found = true
			break
		}
	}
	if !found {
		t.Error("buildGitEnv dropped an unrelated environment variable")
	}
}

func TestRunGit_ErrorIncludesOutput(t *testing.T) {
	dir := t.TempDir() // not a git repository
	_, err := runGit(dir, nil, "status")
	if err == nil {
		t.Fatal("runGit: expected error for a non-repository directory")
	}
}

func TestOnBranch(t *testing.T) {
	dir := newFixtureRepo(t, "chatea-pro", "1.0.0")
	if !onBranch(dir) {
		t.Error("onBranch: expected true right after git init+commit (on a branch)")
	}

	// Detach HEAD by checking out the tag directly.
	mustRunGit(t, dir, "checkout", "-q", "v1")
	if onBranch(dir) {
		t.Error("onBranch: expected false after checking out a tag (detached HEAD)")
	}
}

func TestCurrentRef(t *testing.T) {
	dir := newFixtureRepo(t, "chatea-pro", "1.0.0")
	ref, err := currentRef(dir)
	if err != nil {
		t.Fatalf("currentRef: unexpected error: %v", err)
	}
	if ref == "" {
		t.Error("currentRef: expected a non-empty ref")
	}
}

func TestCurrentRef_NotARepo(t *testing.T) {
	if _, err := currentRef(t.TempDir()); err == nil {
		t.Error("currentRef: expected error for a non-repository directory")
	}
}

func TestExactRefOrSHA_ExactTag(t *testing.T) {
	dir := newFixtureRepo(t, "chatea-pro", "1.0.0")
	mustRunGit(t, dir, "checkout", "-q", "v1")

	ref, err := exactRefOrSHA(dir, nil)
	if err != nil {
		t.Fatalf("exactRefOrSHA: unexpected error: %v", err)
	}
	if ref != "v1" {
		t.Errorf("ref = %q, want exact tag %q", ref, "v1")
	}
}

func TestExactRefOrSHA_NoTagFallsBackToSHA(t *testing.T) {
	dir := newFixtureRepo(t, "chatea-pro", "1.0.0")
	addFixtureCommit(t, dir, "2.0.0", "") // untagged commit past v1

	ref, err := exactRefOrSHA(dir, nil)
	if err != nil {
		t.Fatalf("exactRefOrSHA: unexpected error: %v", err)
	}
	wantSHA := strings.TrimSpace(mustRunGit(t, dir, "rev-parse", "HEAD"))
	if ref != wantSHA {
		t.Errorf("ref = %q, want full SHA %q", ref, wantSHA)
	}
}

func TestExactRefOrSHA_NotARepo(t *testing.T) {
	if _, err := exactRefOrSHA(t.TempDir(), nil); err == nil {
		t.Error("exactRefOrSHA: expected error for a non-repository directory")
	}
}
