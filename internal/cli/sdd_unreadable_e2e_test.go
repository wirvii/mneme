// Package cli — SPEC-133 step 5's own end-to-end verification: the
// discriminating half of AC2 (a real base-branch build, not a hand-typed
// guess of what its output "should" look like — the sixth known form of
// dead criterion) plus AC6/AC7/AC8's full two-table fixture, AC10, and
// AC13. Every test here shares spec.md §8's "two-table fixture": BL-900
// (healthy) / BL-901 (created_at corrupt) in backlog_items, SPEC-900
// (healthy, in progress) / SPEC-901 (in progress, updated_at corrupt) in
// specs — paso 0 found that a single-table fixture only ever exercises one
// of the two independent code paths, which is not what these criteria, as
// finally written, require.
package cli

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/gitident"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/project"
)

// --- shared two-table fixture helpers, --data-dir/--project style ---

// insertHealthyBacklogItemCLI inserts a fully valid backlog_items row
// directly via SQL with a caller-chosen literal ID — the healthy half of
// the two-table fixture, sibling to insertRawUnreadableBacklogItemCLI
// (status_unreadable_test.go).
func insertHealthyBacklogItemCLI(t *testing.T, dataDir, project, id string) {
	t.Helper()
	dbPath := filepath.Join(dataDir, "projects", project+".db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("close db: %v", closeErr)
		}
	}()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := database.ExecContext(context.Background(),
		`INSERT INTO backlog_items (id, title, description, status, priority, project, position, lane, scope, uuid, previous_ids, created_at, updated_at)
		 VALUES (?, ?, '', ?, ?, ?, 0, ?, '', '', '', ?, ?)`,
		id, "healthy fixture row", string(model.BacklogStatusRaw), string(model.PriorityMedium),
		project, string(model.LaneStandard), now, now,
	); err != nil {
		t.Fatalf("insert healthy fixture row %s: %v", id, err)
	}
}

// insertHealthySpecInProgressCLI inserts a fully valid, in-progress specs
// row directly via SQL with a caller-chosen literal ID.
func insertHealthySpecInProgressCLI(t *testing.T, dataDir, project, id string) {
	t.Helper()
	dbPath := filepath.Join(dataDir, "projects", project+".db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("close db: %v", closeErr)
		}
	}()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := database.ExecContext(context.Background(),
		`INSERT INTO specs (id, title, status, project, backlog_id, assigned_agents, files_changed, created_at, updated_at, lane, scope, base_sha, uuid, previous_ids)
		 VALUES (?, ?, ?, ?, NULL, '[]', '[]', ?, ?, ?, '', '', '', '')`,
		id, "healthy fixture spec", string(model.SpecStatusSpeccing), project, now, now, string(model.LaneStandard),
	); err != nil {
		t.Fatalf("insert healthy fixture spec %s: %v", id, err)
	}
}

// insertRawUnreadableSpecInProgressCLI is insertRawUnreadableSpecMCP's CLI
// sibling with status "speccing" (in progress) rather than "draft" — the
// two-table fixture's SPEC-901 is documented as "en curso" (spec.md §8).
func insertRawUnreadableSpecInProgressCLI(t *testing.T, dataDir, project, id string) {
	t.Helper()
	dbPath := filepath.Join(dataDir, "projects", project+".db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("close db: %v", closeErr)
		}
	}()
	if _, err := database.ExecContext(context.Background(),
		`INSERT INTO specs (id, title, status, project, backlog_id, assigned_agents, files_changed, created_at, updated_at, lane, scope, base_sha, uuid, previous_ids)
		 VALUES (?, ?, ?, ?, NULL, '[]', '[]', ?, ?, ?, '', '', '', '')`,
		id, "unreadable fixture spec", string(model.SpecStatusSpeccing), project,
		"2026-01-01T00:00:00Z", "not-a-timestamp", string(model.LaneStandard),
	); err != nil {
		t.Fatalf("insert unreadable fixture spec %s: %v", id, err)
	}
}

// --- AC2's discriminating half: a REAL base-branch build ---

var (
	baseBranchBinaryOnce sync.Once
	baseBranchBinaryPath string
	baseBranchBinaryErr  error
)

// buildBaseBranchBinary builds mneme from this branch's merge-base against
// main into a disposable directory (via "git archive" decoded through
// archive/tar — no dependency on an external tar binary, no worktree, no
// mutation of this repository's own checkout), once per test binary run,
// and returns the resulting executable's path.
//
// This exists because AC2's "byte a byte idéntica a la rama base" is only
// a real check when compared against a REAL base-branch execution — a
// hand-typed "this is what it looked like before" string is exactly the
// sixth known form of dead criterion (a declared comparison value nobody
// re-derives from the thing it claims to describe).
func buildBaseBranchBinary(t *testing.T) string {
	t.Helper()
	baseBranchBinaryOnce.Do(func() {
		// go test's own working directory is the PACKAGE directory
		// (internal/cli), not the repo root. git archive's implicit
		// pathspec is scoped to cwd — run from here it would silently
		// archive only internal/cli's own subtree, with no go.mod at the
		// top of the result. repoRoot pins both git calls to the actual
		// repository root so the archive is the WHOLE tree.
		repoRootOut, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
		if err != nil {
			baseBranchBinaryErr = fmt.Errorf("git rev-parse --show-toplevel: %w", err)
			return
		}
		repoRoot := strings.TrimSpace(string(repoRootOut))

		mergeBaseCmd := exec.Command("git", "merge-base", "HEAD", "main")
		mergeBaseCmd.Dir = repoRoot
		out, err := mergeBaseCmd.Output()
		if err != nil {
			baseBranchBinaryErr = fmt.Errorf("git merge-base HEAD main: %w", err)
			return
		}
		sha := strings.TrimSpace(string(out))

		scratch, err := os.MkdirTemp("", "mneme-basebranch-src-")
		if err != nil {
			baseBranchBinaryErr = fmt.Errorf("mkdir scratch dir: %w", err)
			return
		}

		archiveCmd := exec.Command("git", "archive", sha)
		archiveCmd.Dir = repoRoot
		archiveOut, err := archiveCmd.StdoutPipe()
		if err != nil {
			baseBranchBinaryErr = fmt.Errorf("git archive stdout pipe: %w", err)
			return
		}
		var archiveStderr bytes.Buffer
		archiveCmd.Stderr = &archiveStderr
		if err := archiveCmd.Start(); err != nil {
			baseBranchBinaryErr = fmt.Errorf("git archive start: %w", err)
			return
		}
		if err := extractTarInto(archiveOut, scratch); err != nil {
			baseBranchBinaryErr = fmt.Errorf("extract git archive %s: %w", sha, err)
			return
		}
		if err := archiveCmd.Wait(); err != nil {
			baseBranchBinaryErr = fmt.Errorf("git archive %s: %w\n%s", sha, err, archiveStderr.String())
			return
		}

		binPath := filepath.Join(scratch, "mneme-basebranch-bin")
		buildCmd := exec.Command("go", "build", "-o", binPath, "./cmd/mneme")
		buildCmd.Dir = scratch
		if out, err := buildCmd.CombinedOutput(); err != nil {
			baseBranchBinaryErr = fmt.Errorf("go build base-branch binary: %w\n%s", err, out)
			return
		}
		baseBranchBinaryPath = binPath
	})
	if baseBranchBinaryErr != nil {
		t.Fatalf("buildBaseBranchBinary: %v", baseBranchBinaryErr)
	}
	return baseBranchBinaryPath
}

// extractTarInto decodes a tar stream (as produced by "git archive") into
// dest, using only the standard library.
func extractTarInto(r io.Reader, dest string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dest, hdr.Name) //nolint:gosec // hdr.Name comes from this repo's own git archive, not an untrusted source
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)) //nolint:gosec // same trusted source as above
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil { //nolint:gosec // bounded by this repo's own tree size
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		}
	}
}

// runExternalMneme runs binPath as a subprocess with the same
// --data-dir/--project convention runBacklogCmd uses, from an isolated
// non-git cwd (SPEC-085 rule 3's posture, applied to an external process
// too), and returns stdout, stderr, and the process exit code.
func runExternalMneme(t *testing.T, binPath, dataDir, project string, argv ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	isolatedCwd := t.TempDir()
	args := append([]string{"--data-dir", dataDir, "--project", project}, argv...)
	cmd := exec.Command(binPath, args...)
	cmd.Dir = isolatedCwd
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if ok := asExitError(err, &exitErr); ok {
			return outBuf.String(), errBuf.String(), exitErr.ExitCode()
		}
		t.Fatalf("run base-branch binary %v: %v", args, err)
	}
	return outBuf.String(), errBuf.String(), 0
}

// TestSDDUnreadable_HealthyTwoTableDatabaseByteForByteAgainstBaseBranch is
// SPEC-133 AC2's discriminating half: with the two-table fixture's healthy
// rows only (BL-900, SPEC-900 — no corruption in either table), all seven
// surfaces produce EXACTLY the same stdout, stderr, and exit code as a
// binary built from this branch's own merge-base against main. This is
// what stops a field that is always present from silently satisfying
// AC6/AC7/AC8's "the healthy shape never changes" — without ever actually
// comparing against the real base branch.
func TestSDDUnreadable_HealthyTwoTableDatabaseByteForByteAgainstBaseBranch(t *testing.T) {
	baseBinary := buildBaseBranchBinary(t)

	dataDir := t.TempDir()
	proj := "spec133-ac2-healthy"
	insertHealthyBacklogItemCLI(t, dataDir, proj, "BL-900")
	insertHealthySpecInProgressCLI(t, dataDir, proj, "SPEC-900")

	commands := [][]string{
		{"backlog", "list"},
		{"backlog", "list", "--json"},
		{"spec", "list"},
		{"spec", "list", "--json"},
		{"status"},
		{"status", "--json"},
		{"lane", "stats", "--json"},
	}

	for _, argv := range commands {
		name := strings.Join(argv, " ")
		t.Run(name, func(t *testing.T) {
			headOut, headErr, headRunErr := runBacklogCmd(t, dataDir, proj, argv...)
			headExit := 0
			if headRunErr != nil {
				headExit = 1
			}

			baseOut, baseErr, baseExit := runExternalMneme(t, baseBinary, dataDir, proj, argv...)

			if headOut != baseOut {
				t.Errorf("stdout for %q differs from the base branch:\nhead: %q\nbase: %q", name, headOut, baseOut)
			}
			if headErr != baseErr {
				t.Errorf("stderr for %q differs from the base branch:\nhead: %q\nbase: %q", name, headErr, baseErr)
			}
			if headExit != baseExit {
				t.Errorf("exit code for %q differs from the base branch: head=%d base=%d", name, headExit, baseExit)
			}
		})
	}
}

// --- AC6/AC7/AC8 with the full two-table fixture ---

// TestStatus_TwoTableFixture_BothSectionsSurviveAndBothRowsAreNamed is
// SPEC-133 AC6 with the two-table fixture spec.md §8 requires: with one
// corrupt row per table, BOTH the BACKLOG and SPECS IN PROGRESS sections
// survive (each keeps its own healthy row), and the single UNREADABLE
// announcement names both BL-901 and SPEC-901 exactly once each. A
// single-table fixture only ever loses ONE section — paso 0's own finding
// — so this is the fixture that actually exercises D9's "the panel omits
// SECTIONS, not just one" repair.
func TestStatus_TwoTableFixture_BothSectionsSurviveAndBothRowsAreNamed(t *testing.T) {
	dataDir := t.TempDir()
	proj := "test-status-two-table"

	insertHealthyBacklogItemCLI(t, dataDir, proj, "BL-900")
	insertRawUnreadableBacklogItemCLI(t, dataDir, proj, "BL-901")
	insertHealthySpecInProgressCLI(t, dataDir, proj, "SPEC-900")
	insertRawUnreadableSpecInProgressCLI(t, dataDir, proj, "SPEC-901")

	stdout, stderr, err := runBacklogCmd(t, dataDir, proj, "status")
	if err != nil {
		t.Fatalf("status: %v (stderr=%s)", err, stderr)
	}
	if !strings.Contains(stdout, "BACKLOG") || !strings.Contains(stdout, "BL-900") {
		t.Errorf("BACKLOG section with BL-900 is missing: %s", stdout)
	}
	if !strings.Contains(stdout, "SPECS IN PROGRESS") || !strings.Contains(stdout, "SPEC-900") {
		t.Errorf("SPECS IN PROGRESS section with SPEC-900 is missing: %s", stdout)
	}
	if c := strings.Count(stdout, "Row BL-901 (backlog) could not be fully read"); c != 1 {
		t.Errorf("expected BL-901's announcement exactly once, got %d: %s", c, stdout)
	}
	if c := strings.Count(stdout, "Row SPEC-901 (spec) could not be fully read"); c != 1 {
		t.Errorf("expected SPEC-901's announcement exactly once, got %d: %s", c, stdout)
	}
}

// TestStatus_TwoTableFixture_JSONNamesBothRowsWithKind is AC7 with the
// two-table fixture: the "unreadable" key carries exactly two elements,
// one per table, each with its own kind.
func TestStatus_TwoTableFixture_JSONNamesBothRowsWithKind(t *testing.T) {
	dataDir := t.TempDir()
	proj := "test-status-two-table-json"

	insertHealthyBacklogItemCLI(t, dataDir, proj, "BL-900")
	insertRawUnreadableBacklogItemCLI(t, dataDir, proj, "BL-901")
	insertHealthySpecInProgressCLI(t, dataDir, proj, "SPEC-900")
	insertRawUnreadableSpecInProgressCLI(t, dataDir, proj, "SPEC-901")

	stdout, stderr, err := runBacklogCmd(t, dataDir, proj, "status", "--json")
	if err != nil {
		t.Fatalf("status --json: %v (stderr=%s)", err, stderr)
	}
	var out struct {
		Unreadable []model.UnreadableRow `json:"unreadable"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("decode status --json: %v\n%s", err, stdout)
	}
	if len(out.Unreadable) != 2 {
		t.Fatalf("unreadable = %+v, want exactly 2 rows", out.Unreadable)
	}
	byID := make(map[string]model.UnreadableRow, len(out.Unreadable))
	for _, r := range out.Unreadable {
		byID[r.ID] = r
	}
	if bl, ok := byID["BL-901"]; !ok || bl.Kind != model.UnreadableKindBacklog {
		t.Errorf("BL-901 missing or wrong kind: %+v", byID)
	}
	if sp, ok := byID["SPEC-901"]; !ok || sp.Kind != model.UnreadableKindSpec {
		t.Errorf("SPEC-901 missing or wrong kind: %+v", byID)
	}
}

// TestBacklogList_TwoTableFixture_NeverMentionsTheOtherTablesRow is AC8's
// negative half: "backlog list"'s stderr announcement names ONLY its own
// table's row, never the spec's — the check that catches the two tables
// getting mixed where D13 says they must not.
func TestBacklogList_TwoTableFixture_NeverMentionsTheOtherTablesRow(t *testing.T) {
	dataDir := t.TempDir()
	proj := "test-backloglist-two-table"

	insertHealthyBacklogItemCLI(t, dataDir, proj, "BL-900")
	insertRawUnreadableBacklogItemCLI(t, dataDir, proj, "BL-901")
	insertHealthySpecInProgressCLI(t, dataDir, proj, "SPEC-900")
	insertRawUnreadableSpecInProgressCLI(t, dataDir, proj, "SPEC-901")

	_, stderr, err := runBacklogCmd(t, dataDir, proj, "backlog", "list", "--json")
	if err != nil {
		t.Fatalf("backlog list --json: %v (stderr=%s)", err, stderr)
	}
	if !strings.Contains(stderr, "BL-901") {
		t.Errorf("stderr must announce BL-901: %q", stderr)
	}
	if strings.Contains(stderr, "SPEC-901") {
		t.Errorf("backlog list must never mention a spec row (D13): %q", stderr)
	}
}

// TestSpecList_TwoTableFixture_NeverMentionsTheOtherTablesRow mirrors the
// test above for "spec list" (AC8's negative half, symmetric direction).
func TestSpecList_TwoTableFixture_NeverMentionsTheOtherTablesRow(t *testing.T) {
	dataDir := t.TempDir()
	proj := "test-speclist-two-table"

	insertHealthyBacklogItemCLI(t, dataDir, proj, "BL-900")
	insertRawUnreadableBacklogItemCLI(t, dataDir, proj, "BL-901")
	insertHealthySpecInProgressCLI(t, dataDir, proj, "SPEC-900")
	insertRawUnreadableSpecInProgressCLI(t, dataDir, proj, "SPEC-901")

	_, stderr, err := runBacklogCmd(t, dataDir, proj, "spec", "list", "--json")
	if err != nil {
		t.Fatalf("spec list --json: %v (stderr=%s)", err, stderr)
	}
	if !strings.Contains(stderr, "SPEC-901") {
		t.Errorf("stderr must announce SPEC-901: %q", stderr)
	}
	if strings.Contains(stderr, "BL-901") {
		t.Errorf("spec list must never mention a backlog row (D13): %q", stderr)
	}
}

// --- AC10 / AC13: the SDD git-native mechanism, end to end ---

// seedSDDSpecInProgress mirrors seedSDDBacklog (sdd_test.go) for specs:
// creates one spec via the normal SpecNew path and advances it to
// speccing (in progress), returning its real assigned ID.
func seedSDDSpecInProgress(t *testing.T, repoDir, fakeHome, title string) string {
	t.Helper()
	resetGlobalCLIFlags(t)
	gitident.Reset()
	t.Cleanup(gitident.Reset)
	t.Setenv("HOME", fakeHome)

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(orig) }()

	svc, cleanup, err := initSDDService()
	if err != nil {
		t.Fatalf("initSDDService (seed spec): %v", err)
	}
	defer cleanup()

	spec, err := svc.SpecNew(context.Background(), model.SpecNewRequest{Title: title, Lane: model.LaneStandard})
	if err != nil {
		t.Fatalf("SpecNew (seed): %v", err)
	}
	if _, err := svc.SpecAdvance(context.Background(), model.SpecAdvanceRequest{ID: spec.ID, By: "fixture"}); err != nil {
		t.Fatalf("SpecAdvance (seed): %v", err)
	}
	return spec.ID
}

// sddFixtureDBHandle opens the SAME project database "mneme ..." commands
// will use for the given repoDir/fakeHome pair — the identical
// slug-resolution initSDDService performs (config.Load + project detection
// off repoDir, no git remote set here) — and returns it along with the
// resolved slug, so a row inserted through it lands exactly where those
// commands will read it.
func sddFixtureDBHandle(t *testing.T, repoDir, fakeHome string) (*db.DB, string) {
	t.Helper()
	cfgPath := filepath.Join(fakeHome, ".mneme", "config.toml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	det := project.NewDetector(repoDir)
	slug, _ := det.DetectProject()
	database, err := db.Open(cfg.ProjectDBPath(slug))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	return database, slug
}

// runRootCmdInRepo executes "mneme <argv...>" via the FULL root command
// (NewRootCmd()), chdir'd into repoDir with HOME=fakeHome for the
// duration of the call — deliberately NOT the --data-dir/--project
// override runBacklogCmd uses, because AC10/AC13 exercise the real
// project/repo-root detection the SDD git-native mechanism itself depends
// on. Captures os.Stdout via a pipe (some commands write straight to
// os.Stdout rather than through cmd.OutOrStdout(), same rationale as
// runBacklogCmd) and stderr via cobra's SetErr.
func runRootCmdInRepo(t *testing.T, repoDir, fakeHome string, argv ...string) (stdout, stderr string, err error) {
	t.Helper()
	resetGlobalCLIFlags(t)
	gitident.Reset()
	t.Cleanup(gitident.Reset)
	t.Setenv("HOME", fakeHome)

	orig, wdErr := os.Getwd()
	if wdErr != nil {
		t.Fatalf("Getwd: %v", wdErr)
	}
	if chErr := os.Chdir(repoDir); chErr != nil {
		t.Fatalf("Chdir %s: %v", repoDir, chErr)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	r, w, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatalf("os.Pipe: %v", pipeErr)
	}
	origStdout := os.Stdout
	os.Stdout = w

	root := NewRootCmd()
	errBuf := new(bytes.Buffer)
	root.SetErr(errBuf)
	root.SetArgs(argv)
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

// setupSDDUnreadableTwoTableFixture builds the two-table fixture inside a
// throwaway git repository with the SDD mechanism already enabled: one
// healthy backlog item and one healthy in-progress spec (created and
// exported through the normal path, so their files already exist under
// .mneme/sdd/), then BL-901/SPEC-901 inserted directly via SQL AFTER that
// export — so neither corrupt row ever had a file materialized for it,
// which is exactly what AC13 checks.
func setupSDDUnreadableTwoTableFixture(t *testing.T) (repoDir, fakeHome, healthyBacklogID, healthySpecID string) {
	t.Helper()
	repoDir, fakeHome = sddCLITestRepo(t)
	healthyBacklogID = seedSDDBacklog(t, repoDir, fakeHome, "healthy item")
	healthySpecID = seedSDDSpecInProgress(t, repoDir, fakeHome, "healthy spec")

	if _, stderr, err := runRootCmdInRepo(t, repoDir, fakeHome, "sdd", "enable", "--apply"); err != nil {
		t.Fatalf("sdd enable --apply (setup): %v (stderr=%s)", err, stderr)
	}

	database, slug := sddFixtureDBHandle(t, repoDir, fakeHome)
	if _, err := database.ExecContext(context.Background(),
		`INSERT INTO backlog_items (id, title, description, status, priority, project, position, lane, scope, uuid, previous_ids, created_at, updated_at)
		 VALUES (?, ?, '', ?, ?, ?, 1, ?, '', '', '', ?, ?)`,
		"BL-901", "unreadable fixture row", string(model.BacklogStatusRaw), string(model.PriorityMedium),
		slug, string(model.LaneStandard), "not-a-timestamp", "not-a-timestamp",
	); err != nil {
		t.Fatalf("insert BL-901: %v", err)
	}
	if _, err := database.ExecContext(context.Background(),
		`INSERT INTO specs (id, title, status, project, backlog_id, assigned_agents, files_changed, created_at, updated_at, lane, scope, base_sha, uuid, previous_ids)
		 VALUES (?, ?, ?, ?, NULL, '[]', '[]', ?, ?, ?, '', '', '', '')`,
		"SPEC-901", "unreadable fixture spec", string(model.SpecStatusSpeccing), slug,
		"2026-01-01T00:00:00Z", "not-a-timestamp", string(model.LaneStandard),
	); err != nil {
		t.Fatalf("insert SPEC-901: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close fixture db: %v", err)
	}
	return repoDir, fakeHome, healthyBacklogID, healthySpecID
}

// TestSDDUnreadable_SixCommandsSucceedAndCountsStayExact is SPEC-133 AC10:
// end to end, with the two-table fixture and the SDD git-native mechanism
// already enabled, the six commands that used to fail all exit 0, and
// "sdd status" still reports the true SQL totals for BOTH tables — 2
// backlog items, 2 specs, never degraded to 1 — while naming both
// corrupted rows.
func TestSDDUnreadable_SixCommandsSucceedAndCountsStayExact(t *testing.T) {
	repoDir, fakeHome, _, _ := setupSDDUnreadableTwoTableFixture(t)

	for _, argv := range [][]string{
		{"backlog", "list"},
		{"spec", "list"},
		{"lane", "stats"},
		{"sdd", "status"},
		{"sdd", "enable"},
		{"sdd", "export"},
	} {
		name := strings.Join(argv, " ")
		t.Run(name, func(t *testing.T) {
			_, stderr, err := runRootCmdInRepo(t, repoDir, fakeHome, argv...)
			if err != nil {
				t.Fatalf("%s: %v (stderr=%s)", name, err, stderr)
			}
		})
	}

	stdout, stderr, err := runRootCmdInRepo(t, repoDir, fakeHome, "sdd", "status")
	if err != nil {
		t.Fatalf("sdd status: %v (stderr=%s)", err, stderr)
	}
	if !strings.Contains(stdout, "Database has 2 backlog item(s), 2 spec(s).") {
		t.Errorf("sdd status must still report the true SQL totals for BOTH tables (2 and 2, D6/D11): %s", stdout)
	}
	if c := strings.Count(stdout, "Row BL-901 (backlog) could not be fully read"); c != 1 {
		t.Errorf("expected BL-901 named exactly once, got %d: %s", c, stdout)
	}
	if c := strings.Count(stdout, "Row SPEC-901 (spec) could not be fully read"); c != 1 {
		t.Errorf("expected SPEC-901 named exactly once, got %d: %s", c, stdout)
	}
}

// TestSDDExport_TwoTableFixture_SkipsBothUnreadableFilesAndNamesThem is
// SPEC-133 AC13: after "mneme sdd export" over the two-table fixture, the
// healthy backlog item's and the healthy spec's files exist at their exact
// paths under .mneme/sdd/, the corrupt BL-901's and SPEC-901's files do
// NOT exist, and the export's own result names both (D12).
func TestSDDExport_TwoTableFixture_SkipsBothUnreadableFilesAndNamesThem(t *testing.T) {
	repoDir, fakeHome, healthyBacklogID, healthySpecID := setupSDDUnreadableTwoTableFixture(t)

	stdout, stderr, err := runRootCmdInRepo(t, repoDir, fakeHome, "sdd", "export")
	if err != nil {
		t.Fatalf("sdd export: %v (stderr=%s)", err, stderr)
	}

	healthyBacklogPath := filepath.Join(repoDir, ".mneme", "sdd", "backlog", healthyBacklogID+".md")
	if _, statErr := os.Stat(healthyBacklogPath); statErr != nil {
		t.Errorf("healthy backlog item's file is missing at %s: %v", healthyBacklogPath, statErr)
	}
	corruptBacklogPath := filepath.Join(repoDir, ".mneme", "sdd", "backlog", "BL-901.md")
	if _, statErr := os.Stat(corruptBacklogPath); statErr == nil {
		t.Errorf("BL-901's file must NOT exist, found at %s", corruptBacklogPath)
	} else if !os.IsNotExist(statErr) {
		t.Errorf("unexpected error stat-ing %s: %v", corruptBacklogPath, statErr)
	}

	healthySpecPath := filepath.Join(repoDir, ".mneme", "sdd", "specs", healthySpecID, "record.md")
	if _, statErr := os.Stat(healthySpecPath); statErr != nil {
		t.Errorf("healthy spec's file is missing at %s: %v", healthySpecPath, statErr)
	}
	corruptSpecPath := filepath.Join(repoDir, ".mneme", "sdd", "specs", "SPEC-901", "record.md")
	if _, statErr := os.Stat(corruptSpecPath); statErr == nil {
		t.Errorf("SPEC-901's file must NOT exist, found at %s", corruptSpecPath)
	} else if !os.IsNotExist(statErr) {
		t.Errorf("unexpected error stat-ing %s: %v", corruptSpecPath, statErr)
	}

	if c := strings.Count(stdout, "Row BL-901 (backlog) could not be fully read"); c != 1 {
		t.Errorf("expected BL-901 named exactly once, got %d: %s", c, stdout)
	}
	if c := strings.Count(stdout, "Row SPEC-901 (spec) could not be fully read"); c != 1 {
		t.Errorf("expected SPEC-901 named exactly once, got %d: %s", c, stdout)
	}
}
