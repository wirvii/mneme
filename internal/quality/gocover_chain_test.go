package quality

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// TestGoCoverChain_RealToolchain covers AC24/D19 point 1: the guardian that
// runs against Go's REAL toolchain, not a hand-written fixture string. It
// builds a throwaway, dependency-free module in t.TempDir(), commits it in
// two steps (a base commit without the module's source, then one that adds
// it) so ChangedLines has a real diff to compute, runs `go test
// -coverprofile -covermode=count` on it for real, and feeds the resulting
// profile through the EXACT production chain: ParseProfile("go-cover", …)
// -> NormalizeSourcePath -> ComputeDiffCoverage. It asserts the EXACT
// percentage and EXACTLY which lines are missing — never "greater than
// zero" — because a toolchain-real test that only checks "some coverage
// happened" would not catch a normalization or block-expansion regression.
//
// This is NOT the recursion R3 forbids: the nested `go test` runs against
// a two-file throwaway module in t.TempDir(), never against this
// repository's own suite.
func TestGoCoverChain_RealToolchain(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		// Never t.Skip: a skipped guardian is green forever, which is
		// exactly the antipattern this EPIC exists to eliminate (D19).
		t.Fatalf("go toolchain not found in PATH: %v — this guardian must run, never skip", err)
	}

	dir := t.TempDir()
	gitRun := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=quality-chain-test", "GIT_AUTHOR_EMAIL=quality-chain-test@example.com",
			"GIT_COMMITTER_NAME=quality-chain-test", "GIT_COMMITTER_EMAIL=quality-chain-test@example.com",
		)
		if out, runErr := cmd.CombinedOutput(); runErr != nil {
			t.Fatalf("git %v: %v\n%s", args, runErr, out)
		}
	}
	gitRun("init", "-b", "main")
	gitRun("config", "commit.gpgsign", "false")

	// go.mod declares NO `require` — the throwaway module needs no network
	// access at all (D19).
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/covtest\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	gitRun("add", ".")
	gitRun("commit", "-m", "base: go.mod only")
	baseSHA := strippedHeadSHA(t, dir)

	// lib.go: one covered function, one uncovered function — Go's own
	// coverage instrumentation marks each function's single-statement body
	// as its own BLOCK (verified empirically: block ranges are
	// "lib.go:3.20,5.2" for Covered and "lib.go:7.22,9.2" for Uncovered),
	// so the block-expansion rule (D9) marks lines 3-5 covered and 7-9
	// uninstrumented-turned-uncovered.
	libGo := `package lib

func Covered() int {
	return 1
}

func Uncovered() int {
	return 2
}
`
	libTestGo := `package lib

import "testing"

func TestCovered(t *testing.T) {
	if Covered() != 1 {
		t.Fatal("bad")
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "lib.go"), []byte(libGo), 0o644); err != nil {
		t.Fatalf("write lib.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lib_test.go"), []byte(libTestGo), 0o644); err != nil {
		t.Fatalf("write lib_test.go: %v", err)
	}
	gitRun("add", ".")
	gitRun("commit", "-m", "add lib.go with a covered and an uncovered function")

	// Run the REAL toolchain. Inherits the CURRENT process's environment
	// (GOCACHE/GOMODCACHE included) unmodified — the Makefile's `test`
	// target already passes these through explicitly (SPEC-085 R2), so
	// this nested invocation never writes into the sandboxed HOME.
	profilePath := filepath.Join(dir, "cov.out")
	cmd := exec.Command(goBin, "test", "-coverprofile="+profilePath, "-covermode=count", "./...")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test -coverprofile: %v\n%s", err, out)
	}

	raw, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}

	profile, err := ParseProfile("go-cover", raw)
	if err != nil {
		t.Fatalf("ParseProfile(go-cover): %v", err)
	}

	g := &Git{RepoDir: dir}
	changed, err := g.ChangedLines(baseSHA, "HEAD")
	if err != nil {
		t.Fatalf("ChangedLines: %v", err)
	}
	if _, ok := changed["lib.go"]; !ok {
		t.Fatalf("ChangedLines() = %v, want an entry for lib.go", changed)
	}

	repoFiles := make([]string, 0, len(changed))
	for f := range changed {
		repoFiles = append(repoFiles, f)
	}
	normalized := &Profile{Files: make(map[string]FileCoverage, len(profile.Files))}
	for rawPath, fc := range profile.Files {
		rel, ok := NormalizeSourcePath(rawPath, repoFiles)
		if !ok {
			t.Fatalf("NormalizeSourcePath(%q, %v) did not resolve", rawPath, repoFiles)
		}
		normalized.Files[rel] = fc
	}

	stats := ComputeDiffCoverage(changed, normalized, nil)

	if stats.LinesEligible != 6 {
		t.Errorf("LinesEligible = %d, want 6 (lines 3,4,5,7,8,9 of lib.go)", stats.LinesEligible)
	}
	if stats.LinesCovered != 3 {
		t.Errorf("LinesCovered = %d, want 3 (lines 3,4,5)", stats.LinesCovered)
	}
	if stats.Pct != 50.0 {
		t.Errorf("Pct = %v, want EXACTLY 50.0", stats.Pct)
	}
	wantMissing := map[string][]int{"lib.go": {7, 8, 9}}
	if !reflect.DeepEqual(stats.Missing, wantMissing) {
		t.Errorf("Missing = %v, want %v (Uncovered()'s block)", stats.Missing, wantMissing)
	}
}

// strippedHeadSHA returns the current HEAD SHA of dir, trimmed.
func strippedHeadSHA(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	sha := string(out)
	for len(sha) > 0 && (sha[len(sha)-1] == '\n' || sha[len(sha)-1] == '\r') {
		sha = sha[:len(sha)-1]
	}
	return sha
}
