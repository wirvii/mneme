package gitrepo

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// writeTestFile writes content to name inside dir, failing the test on error.
func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// numberedLines renders "L1\n".."Ln\n", replacing line `changed` (1-based)
// with replacement when changed > 0, and inserting an empty line before
// line `blankBefore` when blankBefore > 0.
func numberedLines(n, changed int, replacement string, blankBefore int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		if i == blankBefore {
			b.WriteString("\n")
		}
		if i == changed {
			b.WriteString(replacement + "\n")
			continue
		}
		fmt.Fprintf(&b, "L%d\n", i)
	}
	return b.String()
}

// requireGit skips the test when git is not available, like its neighbours.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
}

// TestGit_LogOneline proves --abbrev=8 wins over a repository's own
// core.abbrev=12 and that only the commits of the range are listed.
func TestGit_LogOneline(t *testing.T) {
	requireGit(t)
	dir := initTestGitRepo(t)
	gitRunTest(t, dir, "config", "core.abbrev", "12")
	g := &Git{RepoDir: dir}
	base, err := g.HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	writeTestFile(t, dir, "uno.txt", "uno\n")
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "dentro uno")
	writeTestFile(t, dir, "dos.txt", "dos\n")
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "dentro dos")

	out, err := g.LogOneline(base, "HEAD")
	if err != nil {
		t.Fatalf("LogOneline: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("LogOneline lines = %d, want 2:\n%s", len(lines), out)
	}
	abbrev8 := regexp.MustCompile(`^[0-9a-f]{8} `)
	for _, l := range lines {
		if !abbrev8.MatchString(l) {
			t.Errorf("line %q does not start with an 8-hex SHA and a space", l)
		}
	}
	for _, want := range []string{"dentro uno", "dentro dos"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("LogOneline missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(string(out), "initial") {
		t.Errorf("LogOneline lists the commit before the range:\n%s", out)
	}
}

// TestGit_DiffStat proves the stat summary is produced and fits 80 columns.
func TestGit_DiffStat(t *testing.T) {
	requireGit(t)
	dir := initTestGitRepo(t)
	g := &Git{RepoDir: dir}
	base, err := g.HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	writeTestFile(t, dir, "committed.txt", "v2\n")
	gitRunTest(t, dir, "commit", "-am", "change")

	out, err := g.DiffStat(base, "HEAD")
	if err != nil {
		t.Fatalf("DiffStat: %v", err)
	}
	if !strings.Contains(string(out), " changed") {
		t.Errorf("DiffStat lacks the summary line:\n%s", out)
	}
	for _, l := range strings.Split(string(out), "\n") {
		if len(l) > 80 {
			t.Errorf("DiffStat line wider than 80 columns (%d): %q", len(l), l)
		}
	}
}

// TestGit_DiffWithContext proves -U10 shows exactly ten lines of context on
// each side of a one-line change, with full-index index lines.
func TestGit_DiffWithContext(t *testing.T) {
	requireGit(t)
	dir := initTestGitRepo(t)
	g := &Git{RepoDir: dir}
	writeTestFile(t, dir, "lines.txt", numberedLines(30, 0, "", 0))
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "lines")
	base, err := g.HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	writeTestFile(t, dir, "lines.txt", numberedLines(30, 15, "L15-cambiada", 0))
	gitRunTest(t, dir, "commit", "-am", "cambia L15")

	out, err := g.DiffWithContext(base, "HEAD", 10)
	if err != nil {
		t.Fatalf("DiffWithContext: %v", err)
	}
	present := []string{
		`(?m)^ L5$`, `(?m)^ L25$`, `(?m)^-L15$`, `(?m)^\+L15-cambiada$`,
		`(?m)^--- a/lines\.txt$`, `(?m)^\+\+\+ b/lines\.txt$`,
		`(?m)^index [0-9a-f]{40}\.\.[0-9a-f]{40}`,
	}
	for _, p := range present {
		if !regexp.MustCompile(p).Match(out) {
			t.Errorf("DiffWithContext lacks %s:\n%s", p, out)
		}
	}
	for _, p := range []string{`(?m)^[ +-]L4$`, `(?m)^[ +-]L26$`} {
		if regexp.MustCompile(p).Match(out) {
			t.Errorf("DiffWithContext has %s, beyond ten lines of context:\n%s", p, out)
		}
	}
}

// TestGit_ReviewPrimitives_ErrorsNameThePrimitive is the sibling of
// TestGit_ErrorsNameThePrimitive for the review-package primitives: over a
// directory that is not a repository each one fails with the gitrepo:
// prefix and names its own command.
func TestGit_ReviewPrimitives_ErrorsNameThePrimitive(t *testing.T) {
	requireGit(t)
	g := &Git{RepoDir: t.TempDir()}

	tests := []struct {
		name    string
		command string
		run     func() error
	}{
		{"LogOneline", "git log --oneline", func() error { _, err := g.LogOneline("a", "b"); return err }},
		{"DiffStat", "git diff --stat", func() error { _, err := g.DiffStat("a", "b"); return err }},
		{"DiffWithContext", "git diff -U10", func() error { _, err := g.DiffWithContext("a", "b", 10); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil {
				t.Fatalf("%s: want error over a non-git directory, got nil", tt.name)
			}
			if !strings.HasPrefix(err.Error(), "gitrepo:") {
				t.Errorf("%s error = %q, want the gitrepo: prefix", tt.name, err.Error())
			}
			if !strings.Contains(err.Error(), tt.command) {
				t.Errorf("%s error = %q, want it to name %q", tt.name, err.Error(), tt.command)
			}
		})
	}
}

// hostileFixture builds a repository whose range modifies a.txt, z.txt and
// lines.txt (L15 changed, an empty context line between L14 and L15) and
// returns the directory and the base SHA.
func hostileFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := initTestGitRepo(t)
	writeTestFile(t, dir, "a.txt", "a1\n")
	writeTestFile(t, dir, "z.txt", "z1\n")
	writeTestFile(t, dir, "lines.txt", numberedLines(30, 0, "", 15))
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "fixture")
	base, err := (&Git{RepoDir: dir}).HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	writeTestFile(t, dir, "a.txt", "a2\n")
	writeTestFile(t, dir, "z.txt", "z2\n")
	writeTestFile(t, dir, "lines.txt", numberedLines(30, 15, "L15-cambiada", 15))
	gitRunTest(t, dir, "commit", "-am", "change")
	return dir, base
}

// applyHostileConfig sets every user setting and environment variable that
// would change the text of a diff if rawDiff and gitEnv did not pin it.
func applyHostileConfig(t *testing.T, dir string) {
	t.Helper()
	orderDir := t.TempDir()
	orderFile := filepath.Join(orderDir, "order.txt")
	if err := os.WriteFile(orderFile, []byte("z.txt\n"), 0o644); err != nil {
		t.Fatalf("write order.txt: %v", err)
	}
	settings := [][2]string{
		{"diff.orderFile", orderFile},
		{"diff.indentHeuristic", "false"},
		{"diff.statGraphWidth", "3"},
		{"diff.statNameWidth", "4"},
		{"diff.algorithm", "patience"},
		{"diff.interHunkContext", "20"},
		{"diff.context", "1"},
		{"diff.noprefix", "true"},
		{"diff.mnemonicPrefix", "true"},
		{"core.quotePath", "true"},
		{"core.abbrev", "12"},
		{"diff.suppressBlankEmpty", "true"},
		{"color.diff", "always"},
	}
	for _, s := range settings {
		gitRunTest(t, dir, "config", s[0], s[1])
	}
	t.Setenv("GIT_DIFF_OPTS", "-u1")
}

// TestGit_DiffWithContext_HostileConfigDoesNotChangeResult proves the same
// range gives the same bytes under a hostile configuration and environment.
// Manual guardian: removing "-O"+os.DevNull from rawDiff turns this red
// (z.txt moves first), and removing gitEnv() turns it red through
// GIT_DIFF_OPTS; each was verified once and reverted byte for byte.
func TestGit_DiffWithContext_HostileConfigDoesNotChangeResult(t *testing.T) {
	requireGit(t)
	dir, base := hostileFixture(t)
	g := &Git{RepoDir: dir}

	before, err := g.DiffWithContext(base, "HEAD", 10)
	if err != nil {
		t.Fatalf("DiffWithContext (default): %v", err)
	}
	s := string(before)
	if ia, iz := strings.Index(s, "a/a.txt"), strings.Index(s, "a/z.txt"); ia < 0 || iz < 0 || ia > iz {
		t.Fatalf("default order: want a.txt before z.txt:\n%s", s)
	}
	if !regexp.MustCompile(`(?m)^ $`).Match(before) {
		t.Fatalf("default: the empty context line is not a single space:\n%s", s)
	}

	applyHostileConfig(t, dir)
	after, err := g.DiffWithContext(base, "HEAD", 10)
	if err != nil {
		t.Fatalf("DiffWithContext (hostile): %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("DiffWithContext differs under a hostile config:\n--- default ---\n%s\n--- hostile ---\n%s", before, after)
	}
}

// TestGit_DiffStat_HostileConfigDoesNotChangeResult is the DiffStat sibling
// of the case above.
func TestGit_DiffStat_HostileConfigDoesNotChangeResult(t *testing.T) {
	requireGit(t)
	dir, base := hostileFixture(t)
	g := &Git{RepoDir: dir}

	before, err := g.DiffStat(base, "HEAD")
	if err != nil {
		t.Fatalf("DiffStat (default): %v", err)
	}
	s := string(before)
	if ia, iz := strings.Index(s, "a.txt"), strings.Index(s, "z.txt"); ia < 0 || iz < 0 || ia > iz {
		t.Fatalf("default order: want a.txt before z.txt:\n%s", s)
	}

	applyHostileConfig(t, dir)
	after, err := g.DiffStat(base, "HEAD")
	if err != nil {
		t.Fatalf("DiffStat (hostile): %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("DiffStat differs under a hostile config:\n--- default ---\n%s\n--- hostile ---\n%s", before, after)
	}
}

// TestGitEnv_DropsDiffVariables proves gitEnv removes the two variables
// that would change a diff, whatever their case, adds GIT_ATTR_NOSYSTEM=1
// and keeps everything else.
func TestGitEnv_DropsDiffVariables(t *testing.T) {
	t.Setenv("GIT_DIFF_OPTS", "-u1")
	t.Setenv("GIT_EXTERNAL_DIFF", "x")
	t.Setenv("MNEME_GITENV_PROBE", "1")

	env := gitEnv()
	var hasNoSystem, hasProbe bool
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if strings.EqualFold(name, "GIT_DIFF_OPTS") || strings.EqualFold(name, "GIT_EXTERNAL_DIFF") {
			t.Errorf("gitEnv kept %q", kv)
		}
		if kv == "GIT_ATTR_NOSYSTEM=1" {
			hasNoSystem = true
		}
		if kv == "MNEME_GITENV_PROBE=1" {
			hasProbe = true
		}
	}
	if !hasNoSystem {
		t.Error("gitEnv lacks GIT_ATTR_NOSYSTEM=1")
	}
	if !hasProbe {
		t.Error("gitEnv dropped an unrelated variable")
	}
}
