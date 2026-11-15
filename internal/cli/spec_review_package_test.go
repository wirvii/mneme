package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// reviewPackageCLIProject is the project slug every test in this file uses.
const reviewPackageCLIProject = "review-package-cli-project"

// advanceFrontierSpecToQAWithCommit walks specID (in draft) to implementing
// like TestSpecCLI_FrontierOutput does, makes one real commit, and enters
// qa: the review range then holds that commit, unlike
// advanceFrontierSpecToQA, whose range is empty.
func advanceFrontierSpecToQAWithCommit(t *testing.T, repoDir, dataDir, workflowDir, project, specID string) {
	t.Helper()
	for i, by := range []string{"orch", "arch", "arch", "arch", "backend"} {
		if i == 1 {
			writeFrontierCriteriaCLI(t, workflowDir, project, specID)
			writeReviewReportCLI(t, workflowDir, project, specID, "review-spec")
		}
		if i == 4 {
			writeReviewReportCLI(t, workflowDir, project, specID, "review-plan")
		}
		if _, stderr, err := runSpecCmdInRepo(t, repoDir, dataDir, workflowDir, project, "spec", "advance", specID, "--by", by); err != nil {
			t.Fatalf("spec advance step %d: %v (stderr=%s)", i, err, stderr)
		}
	}
	commitFrontierFile(t, repoDir, "work.txt", "implement the feature")
	if _, stderr, err := runSpecCmdInRepo(t, repoDir, dataDir, workflowDir, project, "spec", "advance", specID, "--by", "backend"); err != nil {
		t.Fatalf("spec advance (implementing->qa): %v (stderr=%s)", err, stderr)
	}
}

// newReviewPackageSpec creates a standard spec in a fresh repository and
// returns the fixture directories and its id.
func newReviewPackageSpec(t *testing.T) (repoDir, dataDir, workflowDir, specID string) {
	t.Helper()
	repoDir = newFrontierGitRepo(t)
	dataDir = t.TempDir()
	workflowDir = t.TempDir()
	out, _, err := runSpecCmdInRepo(t, repoDir, dataDir, workflowDir, reviewPackageCLIProject,
		"spec", "new", "review package", "--lane", "standard")
	if err != nil {
		t.Fatalf("spec new: %v", err)
	}
	return repoDir, dataDir, workflowDir, specIDFromCreateOutput(t, out)
}

// TestSpecReviewPackageCmd_PrintsOnlyThePath is C8's CLI half: on success
// stdout is exactly the absolute path and a newline, and the file holds the
// range's commit.
func TestSpecReviewPackageCmd_PrintsOnlyThePath(t *testing.T) {
	repoDir, dataDir, workflowDir, specID := newReviewPackageSpec(t)
	advanceFrontierSpecToQAWithCommit(t, repoDir, dataDir, workflowDir, reviewPackageCLIProject, specID)

	stdout, stderr, err := runSpecCmdInRepo(t, repoDir, dataDir, workflowDir, reviewPackageCLIProject,
		"spec", "review-package", specID)
	if err != nil {
		t.Fatalf("spec review-package: %v", err)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	if !strings.HasSuffix(stdout, "\n") || strings.Count(stdout, "\n") != 1 {
		t.Fatalf("stdout = %q, want exactly one line", stdout)
	}
	p := strings.TrimSuffix(stdout, "\n")
	if !filepath.IsAbs(p) {
		t.Errorf("path %q is not absolute", p)
	}
	if !regexp.MustCompile(`^review-package-[0-9a-f]{8}-[0-9a-f]{8}\.md$`).MatchString(filepath.Base(p)) {
		t.Errorf("file name %q does not match review-package-<8>-<8>.md", filepath.Base(p))
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read package: %v", err)
	}
	content := string(raw)
	if !strings.Contains(content, "# Paquete de revision de "+specID) {
		t.Errorf("package lacks its title:\n%s", content)
	}
	start := strings.Index(content, "## Commits:")
	end := strings.Index(content, "## Archivos:")
	if start < 0 || end < start {
		t.Fatalf("package lacks the commits section:\n%s", content)
	}
	commits := content[start:end]
	if !strings.Contains(commits, "implement the feature") || strings.Contains(commits, "(vacio)") {
		t.Errorf("commits section = %q, want the range commit", commits)
	}
}

// TestSpecReviewPackageCmd_FailsOutsideQA is C8's CLI half: outside qa the
// command returns the sentinel error, prints nothing and writes nothing.
func TestSpecReviewPackageCmd_FailsOutsideQA(t *testing.T) {
	repoDir, dataDir, workflowDir, specID := newReviewPackageSpec(t)

	stdout, stderr, err := runSpecCmdInRepo(t, repoDir, dataDir, workflowDir, reviewPackageCLIProject,
		"spec", "review-package", specID)
	if err == nil {
		t.Fatal("want an error outside qa, got nil")
	}
	if stdout != "" || stderr != "" {
		t.Errorf("stdout = %q, stderr = %q; want both empty", stdout, stderr)
	}
	for _, want := range []string{"el paquete solo existe mientras la spec esta en qa", "review package unavailable"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to contain %q", err, want)
		}
	}
	matches, globErr := filepath.Glob(filepath.Join(workflowDir, reviewPackageCLIProject, "specs", specID, "review-package-*.md"))
	if globErr != nil {
		t.Fatalf("glob: %v", globErr)
	}
	if len(matches) != 0 {
		t.Errorf("files written: %v", matches)
	}
}

// specStatusJSONKeys runs `spec status <id> --json` and decodes its keys.
func specStatusJSONKeys(t *testing.T, repoDir, dataDir, workflowDir, specID string) map[string]json.RawMessage {
	t.Helper()
	out, _, err := runSpecCmdInRepo(t, repoDir, dataDir, workflowDir, reviewPackageCLIProject,
		"spec", "status", specID, "--json")
	if err != nil {
		t.Fatalf("spec status --json: %v", err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &keys); err != nil {
		t.Fatalf("decode spec status --json: %v\n%s", err, out)
	}
	return keys
}

// specStatusText runs `spec status <id>` and returns its stdout.
func specStatusText(t *testing.T, repoDir, dataDir, workflowDir, specID string) string {
	t.Helper()
	out, _, err := runSpecCmdInRepo(t, repoDir, dataDir, workflowDir, reviewPackageCLIProject,
		"spec", "status", specID)
	if err != nil {
		t.Fatalf("spec status: %v", err)
	}
	return out
}

// TestSpecStatusCmd_JSONCarriesQARoundsAndPackagePath is C9's CLI half:
// qa_rounds is always in the JSON, review_package_path appears once the
// package exists and only in qa, and the text lines follow the same rules.
func TestSpecStatusCmd_JSONCarriesQARoundsAndPackagePath(t *testing.T) {
	repoDir, dataDir, workflowDir, specID := newReviewPackageSpec(t)
	advanceFrontierSpecToQAWithCommit(t, repoDir, dataDir, workflowDir, reviewPackageCLIProject, specID)

	keys := specStatusJSONKeys(t, repoDir, dataDir, workflowDir, specID)
	if string(keys["qa_rounds"]) != "0" {
		t.Errorf("qa_rounds = %s, want 0", keys["qa_rounds"])
	}
	if _, ok := keys["review_package_path"]; ok {
		t.Errorf("review_package_path present before the package was written")
	}

	out, _, err := runSpecCmdInRepo(t, repoDir, dataDir, workflowDir, reviewPackageCLIProject,
		"spec", "review-package", specID)
	if err != nil {
		t.Fatalf("spec review-package: %v", err)
	}
	path := strings.TrimSuffix(out, "\n")

	keys = specStatusJSONKeys(t, repoDir, dataDir, workflowDir, specID)
	var got string
	if err := json.Unmarshal(keys["review_package_path"], &got); err != nil || got != path {
		t.Errorf("review_package_path = %s, want %q", keys["review_package_path"], path)
	}
	text := specStatusText(t, repoDir, dataDir, workflowDir, specID)
	if !strings.Contains(text, "Review package: "+path) || strings.Contains(text, "QA rounds:") {
		t.Errorf("text in qa: want Review package and no QA rounds line:\n%s", text)
	}

	if _, stderr, err := runSpecCmdInRepo(t, repoDir, dataDir, workflowDir, reviewPackageCLIProject,
		"spec", "reject", specID, "--by", "qa", "--reason", "x", "--finding", "AC1=falla"); err != nil {
		t.Fatalf("spec reject: %v (stderr=%s)", err, stderr)
	}
	keys = specStatusJSONKeys(t, repoDir, dataDir, workflowDir, specID)
	if string(keys["qa_rounds"]) != "1" {
		t.Errorf("after reject: qa_rounds = %s, want 1", keys["qa_rounds"])
	}
	if _, ok := keys["review_package_path"]; ok {
		t.Errorf("after reject: review_package_path present outside qa")
	}
	text = specStatusText(t, repoDir, dataDir, workflowDir, specID)
	if !strings.Contains(text, "QA rounds: 1") || strings.Contains(text, "Review package:") || strings.Contains(text, "Review range:") {
		t.Errorf("text after reject: want QA rounds: 1 and no review lines:\n%s", text)
	}
}
