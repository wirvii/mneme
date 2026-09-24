package cli

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newFrontierGitRepo initialises a git repository with one commit and
// returns its path — deliberately WITHOUT .mneme/shared/.mneme-vault or
// .mneme/sdd/.mneme-sdd (SPEC-085 rule 3), so neither team-memory nor the
// SDD git-native mechanism activates for this fixture: initSDDService
// resolves repoDir to this real repository (so SPEC-157's git primitives
// — HeadSHA/IsAncestor — actually run), but DetectTeamMemory/
// ResolveSDDState both stay inert because the marker files are absent.
func newFrontierGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGitOK(t, dir, "init", "-b", "main")
	runGitOK(t, dir, "config", "user.email", "frontier-cli-test@example.com")
	runGitOK(t, dir, "config", "user.name", "frontier-cli-test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	runGitOK(t, dir, "add", ".")
	runGitOK(t, dir, "commit", "-m", "initial")
	return dir
}

// commitFrontierFile adds one more commit to repoDir — used to move HEAD
// so a frontier note/rebase can be exercised for real.
func commitFrontierFile(t *testing.T, repoDir, name, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repoDir, name), []byte("content\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	runGitOK(t, repoDir, "add", ".")
	runGitOK(t, repoDir, "commit", "-m", message)
}

// runSpecCmdInRepo executes "mneme spec <argv...>" chdir'd into a FIXED
// real git repository (repoDir) — unlike runBacklogCmd's isolated non-git
// temp dir, SPEC-157's CLI output needs `git rev-parse HEAD`/`git
// merge-base --is-ancestor` to actually run against something. workflowDir
// isolates Config.Workflow.Dir via MNEME_WORKFLOW_DIR (SPEC-085: the
// process-wide sandboxed HOME is shared across the whole test binary run,
// so relying on the real default would collide across tests/subtests).
func runSpecCmdInRepo(t *testing.T, repoDir, dataDir, workflowDir, project string, argv ...string) (stdout, stderr string, err error) {
	t.Helper()

	orig, wdErr := os.Getwd()
	if wdErr != nil {
		t.Fatalf("getwd: %v", wdErr)
	}
	if chErr := os.Chdir(repoDir); chErr != nil {
		t.Fatalf("chdir into fixture repo: %v", chErr)
	}
	t.Cleanup(func() {
		if restoreErr := os.Chdir(orig); restoreErr != nil {
			t.Fatalf("restore cwd: %v", restoreErr)
		}
	})
	t.Setenv("MNEME_WORKFLOW_DIR", workflowDir)

	r, w, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatalf("os.Pipe: %v", pipeErr)
	}
	origStdout := os.Stdout
	os.Stdout = w

	root := NewRootCmd()
	errBuf := new(bytes.Buffer)
	root.SetErr(errBuf)

	args := append([]string{"--data-dir", dataDir, "--project", project}, argv...)
	root.SetArgs(args)
	err = root.Execute()

	os.Stdout = origStdout
	if closeErr := w.Close(); closeErr != nil {
		t.Fatalf("close stdout pipe writer: %v", closeErr)
	}
	outBytes, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatalf("read stdout pipe: %v", readErr)
	}

	return string(outBytes), errBuf.String(), err
}

// writeFrontierCriteriaCLI writes a minimal valid criteria.toml directly to
// disk at the path specDocPath (internal/service/sdd.go) would resolve —
// there is no CLI wrapper for spec_doc_write (it is an MCP/agent-only
// channel), so this mirrors internal/service's own writeSpecCriteria test
// helper at the filesystem level instead.
func writeFrontierCriteriaCLI(t *testing.T, workflowDir, project, specID string) {
	t.Helper()
	safeProject := strings.ReplaceAll(project, "/", "-")
	dir := filepath.Join(workflowDir, safeProject, "specs", specID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir criteria dir: %v", err)
	}
	const doc = `schema_version = 1

[[criterion]]
id = "AC1"
mode = "manual"
text = "fixture criterion"
evidence_required = "fixture"
`
	if err := os.WriteFile(filepath.Join(dir, "criteria.toml"), []byte(doc), 0o644); err != nil {
		t.Fatalf("write criteria.toml: %v", err)
	}
}

// headSHACLI returns repoDir's current HEAD SHA.
func headSHACLI(t *testing.T, repoDir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// specIDFromCreateOutput extracts "SPEC-NNN" from "spec new"'s
// "Created SPEC-NNN: ..." stdout line.
func specIDFromCreateOutput(t *testing.T, stdout string) string {
	t.Helper()
	fields := strings.Fields(stdout)
	for _, f := range fields {
		if strings.HasPrefix(f, "SPEC-") {
			return strings.TrimSuffix(f, ":")
		}
	}
	t.Fatalf("could not find a SPEC-### id in output: %q", stdout)
	return ""
}

// advanceFrontierSpecToQA walks a freshly created standard-lane spec
// (specID, in draft) all the way to qa via real "mneme spec advance"
// invocations against repoDir/dataDir/workflowDir/project, writing the
// criteria.toml SPEC-156 requires along the way.
func advanceFrontierSpecToQA(t *testing.T, repoDir, dataDir, workflowDir, project, specID string) {
	t.Helper()
	for i, by := range []string{"orch", "arch", "arch", "arch", "backend", "backend"} {
		if by == "arch" && i == 1 {
			writeFrontierCriteriaCLI(t, workflowDir, project, specID)
		}
		_, stderr, err := runSpecCmdInRepo(t, repoDir, dataDir, workflowDir, project, "spec", "advance", specID, "--by", by)
		if err != nil {
			t.Fatalf("spec advance step %d (%s): %v (stderr=%s)", i, by, err, stderr)
		}
	}
}

// TestSpecCLI_FrontierOutput is SPEC-157 criteria.toml AC18 / spec.md
// AC19: what a person sees on the command line for the review range and
// the frontier note, across "spec advance", "spec reject", and
// "spec history".
func TestSpecCLI_FrontierOutput(t *testing.T) {
	const project = "frontier-cli-project"

	t.Run("spec advance prints the review range notice on entering qa", func(t *testing.T) {
		repoDir := newFrontierGitRepo(t)
		dataDir := t.TempDir()
		workflowDir := t.TempDir()

		createOut, _, err := runSpecCmdInRepo(t, repoDir, dataDir, workflowDir, project,
			"spec", "new", "advance notice", "--lane", "standard")
		if err != nil {
			t.Fatalf("spec new: %v", err)
		}
		specID := specIDFromCreateOutput(t, createOut)

		// draft -> speccing -> specced -> planning -> planned -> implementing.
		for i, by := range []string{"orch", "arch", "arch", "arch", "backend"} {
			if by == "arch" && i == 1 {
				writeFrontierCriteriaCLI(t, workflowDir, project, specID)
			}
			if _, stderr, err := runSpecCmdInRepo(t, repoDir, dataDir, workflowDir, project, "spec", "advance", specID, "--by", by); err != nil {
				t.Fatalf("spec advance step %d: %v (stderr=%s)", i, err, stderr)
			}
		}

		// A real commit between entering implementing (base) and entering
		// qa (delivered) — otherwise From==To and the notice would say
		// "nada nuevo" instead of exercising the "primera pasada" text.
		commitFrontierFile(t, repoDir, "work.txt", "implement the feature")

		// implementing -> qa: THE transition that prints the Notice.
		stdout, stderr, err := runSpecCmdInRepo(t, repoDir, dataDir, workflowDir, project, "spec", "advance", specID, "--by", "backend")
		if err != nil {
			t.Fatalf("spec advance (implementing->qa): %v (stderr=%s)", err, stderr)
		}
		if !strings.Contains(stdout, "primera pasada") {
			t.Errorf("stdout does not print the review range notice:\n%s", stdout)
		}
	})

	t.Run("spec advance prints the frontier note on leaving qa with HEAD moved", func(t *testing.T) {
		repoDir := newFrontierGitRepo(t)
		dataDir := t.TempDir()
		workflowDir := t.TempDir()

		createOut, _, err := runSpecCmdInRepo(t, repoDir, dataDir, workflowDir, project,
			"spec", "new", "exit notice", "--lane", "standard")
		if err != nil {
			t.Fatalf("spec new: %v", err)
		}
		specID := specIDFromCreateOutput(t, createOut)
		advanceFrontierSpecToQA(t, repoDir, dataDir, workflowDir, project, specID)

		delivered := headSHACLI(t, repoDir)
		commitFrontierFile(t, repoDir, "after-entry.txt", "commit while QA reviews")
		moved := headSHACLI(t, repoDir)
		if delivered == moved {
			t.Fatalf("setup: HEAD did not move")
		}

		stdout, stderr, err := runSpecCmdInRepo(t, repoDir, dataDir, workflowDir, project, "spec", "advance", specID, "--by", "qa-agent")
		if err != nil {
			t.Fatalf("spec advance (qa->done): %v (stderr=%s)", err, stderr)
		}
		if !strings.Contains(stdout, "frontera: la frontera avanza solo hasta el extremo entregado") {
			t.Errorf("stdout does not print the frontier note:\n%s", stdout)
		}
		if !strings.Contains(stdout, delivered[:8]) || !strings.Contains(stdout, moved[:8]) {
			t.Errorf("stdout does not name both SHAs (%s / %s):\n%s", delivered[:8], moved[:8], stdout)
		}
	})

	t.Run("spec reject prints the frontier note", func(t *testing.T) {
		repoDir := newFrontierGitRepo(t)
		dataDir := t.TempDir()
		workflowDir := t.TempDir()

		createOut, _, err := runSpecCmdInRepo(t, repoDir, dataDir, workflowDir, project,
			"spec", "new", "reject notice", "--lane", "standard")
		if err != nil {
			t.Fatalf("spec new: %v", err)
		}
		specID := specIDFromCreateOutput(t, createOut)
		advanceFrontierSpecToQA(t, repoDir, dataDir, workflowDir, project, specID)
		commitFrontierFile(t, repoDir, "after-entry-reject.txt", "commit while QA reviews (reject case)")

		stdout, stderr, err := runSpecCmdInRepo(t, repoDir, dataDir, workflowDir, project,
			"spec", "reject", specID, "--reason", "found issues", "--by", "qa-agent",
			"--finding", "AC1=does not hold")
		if err != nil {
			t.Fatalf("spec reject: %v (stderr=%s)", err, stderr)
		}
		if !strings.Contains(stdout, "frontera: la frontera avanza solo hasta el extremo entregado") {
			t.Errorf("stdout does not print the frontier note:\n%s", stdout)
		}
	})

	t.Run("spec history names which end reviewed_sha is", func(t *testing.T) {
		repoDir := newFrontierGitRepo(t)
		dataDir := t.TempDir()
		workflowDir := t.TempDir()

		createOut, _, err := runSpecCmdInRepo(t, repoDir, dataDir, workflowDir, project,
			"spec", "new", "history labels", "--lane", "standard")
		if err != nil {
			t.Fatalf("spec new: %v", err)
		}
		specID := specIDFromCreateOutput(t, createOut)
		advanceFrontierSpecToQA(t, repoDir, dataDir, workflowDir, project, specID)

		if _, stderr, err := runSpecCmdInRepo(t, repoDir, dataDir, workflowDir, project,
			"spec", "advance", specID, "--by", "qa-agent"); err != nil {
			t.Fatalf("spec advance (qa->done): %v (stderr=%s)", err, stderr)
		}

		stdout, _, err := runSpecCmdInRepo(t, repoDir, dataDir, workflowDir, project, "spec", "history", specID)
		if err != nil {
			t.Fatalf("spec history: %v", err)
		}
		if !strings.Contains(stdout, "extremo entregado:") {
			t.Errorf("spec history does not label the delivered end:\n%s", stdout)
		}
		if !strings.Contains(stdout, "frontera revisada:") {
			t.Errorf("spec history does not label the reviewed frontier:\n%s", stdout)
		}
	})

	t.Run("spec status prints the review range notice while in qa", func(t *testing.T) {
		repoDir := newFrontierGitRepo(t)
		dataDir := t.TempDir()
		workflowDir := t.TempDir()

		createOut, _, err := runSpecCmdInRepo(t, repoDir, dataDir, workflowDir, project,
			"spec", "new", "status notice", "--lane", "standard")
		if err != nil {
			t.Fatalf("spec new: %v", err)
		}
		specID := specIDFromCreateOutput(t, createOut)
		advanceFrontierSpecToQA(t, repoDir, dataDir, workflowDir, project, specID)

		stdout, _, err := runSpecCmdInRepo(t, repoDir, dataDir, workflowDir, project, "spec", "status", specID)
		if err != nil {
			t.Fatalf("spec status: %v", err)
		}
		if !strings.Contains(stdout, "Review range:") {
			t.Errorf("spec status does not print the review range:\n%s", stdout)
		}
	})
}
