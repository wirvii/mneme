package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/gitattrs"
)

// runGitCombined runs git with args in dir, returning combined output and
// failing the test on a non-zero exit.
func runGitCombined(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// TestGitattributes_CheckAttrAnswersForNonexistentPath is SPEC-140 AC6: the
// assumption D10's entire policy rests on — that `git check-attr` answers
// for a path that does not exist on disk — is verified here, not assumed.
// If this ever stopped being true, D10 would need to be redesigned rather
// than patched.
func TestGitattributes_CheckAttrAnswersForNonexistentPath(t *testing.T) {
	dir := newTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte(gitattrs.Block()), 0o644); err != nil {
		t.Fatalf("write .gitattributes: %v", err)
	}

	out := runGitCombined(t, dir, "check-attr", "eol", "--", ".mneme/sdd/no-existe-jamas.md")
	if !strings.Contains(out, "eol: lf") {
		t.Fatalf("git check-attr on a nonexistent path did not answer as expected: %q", out)
	}
}

// TestGitattributes_CheckoutKeepsLFAndControlKeepsCR is SPEC-140 AC5: the
// end-to-end effect of the written .gitattributes block, measured with git
// itself, with its mandatory control. Runs on macOS with no artifice —
// core.autocrlf is git configuration, not an OS-specific behaviour (D13).
func TestGitattributes_CheckoutKeepsLFAndControlKeepsCR(t *testing.T) {
	dir := newTestGitRepo(t)

	covered := map[string]string{
		filepath.Join(".mneme", "sdd", "backlog", "BL-001.md"): "backlog\n",
		filepath.Join(".claude", "agents", "backend.md"):       "agent\n",
		filepath.Join(".codex", "agents", "backend.toml"):      "agent = true\n",
	}
	controlPath := "README-control.md"

	for rel, content := range covered {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, controlPath), []byte("control\n"), 0o644); err != nil {
		t.Fatalf("write control file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte(gitattrs.Block()), 0o644); err != nil {
		t.Fatalf("write .gitattributes: %v", err)
	}

	runGitCombined(t, dir, "add", ".")
	runGitCombined(t, dir, "commit", "-m", "lf fixtures + .gitattributes")
	runGitCombined(t, dir, "config", "core.autocrlf", "true")

	for rel := range covered {
		if err := os.Remove(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("remove %s: %v", rel, err)
		}
	}
	if err := os.Remove(filepath.Join(dir, controlPath)); err != nil {
		t.Fatalf("remove control file: %v", err)
	}
	runGitCombined(t, dir, "checkout", "--", ".")

	hasCR := func(rel string) bool {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return strings.Contains(string(data), "\r")
	}

	for rel := range covered {
		if hasCR(rel) {
			t.Errorf("%s still has CR after checkout with .gitattributes — the block did not take effect", rel)
		}
	}
	if !hasCR(controlPath) {
		t.Error("control file lost its CR — the montage does not prove core.autocrlf actually applied here, so the covered-files result above is worthless")
	}
}

// TestEnsureGitattributes_DeliberateCRLFRuleIsNeverTouched is SPEC-140 AC7:
// a pre-existing, deliberate rule that conflicts with D9 is left byte-for-
// byte untouched, and the operation names the conflicting pattern. Both
// halves are required: bytes-identical alone would also pass an
// implementation that silently does nothing at all, which is why the
// finding's presence is asserted too.
func TestEnsureGitattributes_DeliberateCRLFRuleIsNeverTouched(t *testing.T) {
	dir := newTestGitRepo(t)
	before := ".mneme/** text eol=crlf\n"
	path := filepath.Join(dir, ".gitattributes")
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatalf("write .gitattributes: %v", err)
	}
	runGitCombined(t, dir, "add", ".")
	runGitCombined(t, dir, "commit", "-m", "deliberate crlf rule")

	findings, err := EnsureGitattributes(dir, false)
	if err != nil {
		t.Fatalf("EnsureGitattributes: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read .gitattributes: %v", err)
	}
	if string(after) != before {
		t.Fatalf("bytes changed:\nbefore: %q\nafter:  %q", before, after)
	}

	found := false
	for _, f := range findings {
		if strings.Contains(f.Message, ".mneme/**") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a finding naming the pattern .mneme/**, got: %v", findings)
	}
}

// TestEnsureGitattributes_UserRuleAlreadyLFAddsNothing is SPEC-140 AC8: a
// user's own rule that already resolves every pattern to "lf" is left
// untouched and no mneme marker is added.
func TestEnsureGitattributes_UserRuleAlreadyLFAddsNothing(t *testing.T) {
	dir := newTestGitRepo(t)
	before := "* text eol=lf\n"
	path := filepath.Join(dir, ".gitattributes")
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatalf("write .gitattributes: %v", err)
	}
	runGitCombined(t, dir, "add", ".")
	runGitCombined(t, dir, "commit", "-m", "user lf rule")

	findings, err := EnsureGitattributes(dir, false)
	if err != nil {
		t.Fatalf("EnsureGitattributes: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings, got: %v", findings)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read .gitattributes: %v", err)
	}
	if string(after) != before {
		t.Fatalf("bytes changed:\nbefore: %q\nafter:  %q", before, after)
	}
	if strings.Contains(string(after), "mneme (SPEC-140)") {
		t.Error("mneme's marker was added even though the user's rule already resolved to lf")
	}
}

// TestEnsureGitattributes_Idempotent is SPEC-140 AC9: running the writer
// twice in a row on a repository with no .gitattributes produces
// byte-identical results, with exactly one start marker.
func TestEnsureGitattributes_Idempotent(t *testing.T) {
	dir := newTestGitRepo(t)

	if _, err := EnsureGitattributes(dir, false); err != nil {
		t.Fatalf("EnsureGitattributes (first): %v", err)
	}
	path := filepath.Join(dir, ".gitattributes")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after first run: %v", err)
	}

	if _, err := EnsureGitattributes(dir, false); err != nil {
		t.Fatalf("EnsureGitattributes (second): %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after second run: %v", err)
	}

	if string(first) != string(second) {
		t.Fatalf("not idempotent:\nfirst:  %q\nsecond: %q", first, second)
	}
	if strings.Count(string(second), "mneme (SPEC-140)") != 2 { // start + end comment both carry it
		t.Errorf("expected exactly one block (2 occurrences of the SPEC-140 tag: start+end), got %d in %q",
			strings.Count(string(second), "mneme (SPEC-140)"), second)
	}
}

// TestEnsureGitattributes_CheckModeNeverWrites is part of SPEC-140 AC10:
// checkMode=true must never touch the filesystem or run git.
func TestEnsureGitattributes_CheckModeNeverWrites(t *testing.T) {
	dir := newTestGitRepo(t)
	findings, err := EnsureGitattributes(dir, true)
	if err != nil {
		t.Fatalf("EnsureGitattributes(checkMode): %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings in check mode, got: %v", findings)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".gitattributes")); !os.IsNotExist(statErr) {
		t.Error(".gitattributes was written despite checkMode=true")
	}
}
