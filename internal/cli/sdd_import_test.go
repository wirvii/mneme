package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/gitident"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/sddfile"
	"github.com/wirvii/mneme/internal/service"
)

// TestRenderSDDImportResult_EveryBranch is a targeted addition (QA
// rejection fix): the end-to-end CLI tests above only ever exercise
// renderSDDImportResult through a real `sdd import` run, which never
// happens to hit every one of its six independent branches (Created,
// Updated, Completed, Skipped, OnlyInBaseTotal>0, and the terminal
// "nothing changed" fallback) in a single scenario — the function sat at
// 64.7%, genuinely new to this spec, and undisclosed in changes.md's own
// coverage table. Table-driven, one subtest per branch, against the
// function directly rather than through the full CLI/service/store stack.
func TestRenderSDDImportResult_EveryBranch(t *testing.T) {
	tests := []struct {
		name   string
		result *service.SDDImportResult
		want   []string
	}{
		{
			name:   "no-op reason short-circuits everything else",
			result: &service.SDDImportResult{NoOpReason: "mecanismo apagado"},
			want:   []string{"Nothing to import: mecanismo apagado."},
		},
		{
			name:   "created entries are listed",
			result: &service.SDDImportResult{Created: []string{"BL-001 (backlog/BL-001.md)"}},
			want:   []string{"Created: BL-001 (backlog/BL-001.md)"},
		},
		{
			name:   "updated entries are listed",
			result: &service.SDDImportResult{Updated: []string{"SPEC-001: draft -> speccing"}},
			want:   []string{"Updated: SPEC-001: draft -> speccing"},
		},
		{
			name: "completed entries name the id, path, and filled fields",
			result: &service.SDDImportResult{Completed: []service.SDDImportCompleted{
				{ID: "BL-002", Path: "backlog/BL-002.md", Fields: []string{"priority", "lane"}},
			}},
			want: []string{"Completed: BL-002 (backlog/BL-002.md) — filled: priority, lane"},
		},
		{
			name: "skipped entries name the id, path, and reason",
			result: &service.SDDImportResult{Skipped: []service.SDDImportSkip{
				{ID: "BL-003", Path: "backlog/BL-003.md", Reason: "roto"},
			}},
			want: []string{"Skipped: BL-003 (backlog/BL-003.md) — roto"},
		},
		{
			name: "only-in-base total is reported with the listed correlatives",
			result: &service.SDDImportResult{
				OnlyInBase: []string{"BL-050", "SPEC-070"}, OnlyInBaseTotal: 2,
			},
			want: []string{
				"2 correlative(s) exist only in the local database on this branch:",
				"  - BL-050", "  - SPEC-070",
			},
		},
		{
			name:   "everything empty falls back to the explicit no-change line",
			result: &service.SDDImportResult{},
			want:   []string{"Nothing changed — the database already matches every file on this branch."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			renderSDDImportResult(&buf, tt.result)
			got := buf.String()
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("output does not contain %q:\n%s", want, got)
				}
			}
		})
	}
}

// TestSDDImportCmd_ExitCodes is SPEC-131 AC23.
func TestSDDImportCmd_ExitCodes(t *testing.T) {
	t.Run("nothing skipped -> exit 0", func(t *testing.T) {
		repoDir, fakeHome := sddCLITestRepo(t)
		seedSDDBacklog(t, repoDir, fakeHome, "clean item")
		t.Setenv("HOME", fakeHome)

		if _, _, err := runSDDCmd(t, repoDir, "enable", "--apply"); err != nil {
			t.Fatalf("enable --apply: %v", err)
		}

		if _, stderr, err := runSDDCmd(t, repoDir, "import"); err != nil {
			t.Fatalf("sdd import must exit 0 with nothing skipped: %v (stderr=%s)", err, stderr)
		}
	})

	t.Run("disputed record -> exit 1, names the path", func(t *testing.T) {
		repoDir, fakeHome := sddCLITestRepo(t)
		itemID := seedSDDBacklog(t, repoDir, fakeHome, "disputed item")
		t.Setenv("HOME", fakeHome)

		if _, _, err := runSDDCmd(t, repoDir, "enable", "--apply"); err != nil {
			t.Fatalf("enable --apply: %v", err)
		}

		// Overwrite the just-exported file with a DIFFERENT anchor for the
		// SAME correlative — a genuine dispute (D50 CASE C), not a broken
		// or foreign-anchor file.
		foreign := &sddfile.BacklogRecord{Item: &model.BacklogItem{
			ID: itemID, Title: "de otra maquina", Status: model.BacklogStatusRaw,
			Priority: model.PriorityMedium, Project: "wirvii/mneme", Lane: model.LaneStandard,
			UUID: "01a044bc-7c25-7448-87e9-febc5c5982ee",
		}}
		data, err := sddfile.MarshalBacklog(foreign)
		if err != nil {
			t.Fatalf("MarshalBacklog fixture: %v", err)
		}
		path := sddfile.BacklogPath(repoDir, itemID)
		if err := sddfile.WriteRecord(path, data); err != nil {
			t.Fatalf("WriteRecord fixture: %v", err)
		}

		stdout, stderr, err := runSDDCmd(t, repoDir, "import")
		if err == nil {
			t.Fatalf("sdd import must exit non-zero with a disputed record; stdout=%q stderr=%q", stdout, stderr)
		}
		combined := stdout + stderr + err.Error()
		if !strings.Contains(combined, filepath.Base(path)) {
			t.Errorf("output does not name the disputed path %s: %s", path, combined)
		}
	})
}

// TestSDDHooksRunImportCmd_AlwaysExitsZero is SPEC-131 AC23's other half:
// `mneme sdd hooks run-import` exits 0 even with a broken file AND a
// disputed correlative present.
func TestSDDHooksRunImportCmd_AlwaysExitsZero(t *testing.T) {
	repoDir, fakeHome := sddCLITestRepo(t)
	itemID := seedSDDBacklog(t, repoDir, fakeHome, "will be disputed")
	t.Setenv("HOME", fakeHome)

	if _, _, err := runSDDCmd(t, repoDir, "enable", "--apply"); err != nil {
		t.Fatalf("enable --apply: %v", err)
	}

	// A disputed correlative.
	foreign := &sddfile.BacklogRecord{Item: &model.BacklogItem{
		ID: itemID, Title: "de otra maquina", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: "wirvii/mneme", Lane: model.LaneStandard,
		UUID: "01a044bc-7c25-7448-87e9-febc5c5982ee",
	}}
	data, err := sddfile.MarshalBacklog(foreign)
	if err != nil {
		t.Fatalf("MarshalBacklog fixture: %v", err)
	}
	if err := sddfile.WriteRecord(sddfile.BacklogPath(repoDir, itemID), data); err != nil {
		t.Fatalf("WriteRecord fixture: %v", err)
	}

	// A broken file.
	if err := os.WriteFile(sddfile.BacklogPath(repoDir, "BL-999"), []byte("not a record"), 0o644); err != nil {
		t.Fatalf("write broken fixture: %v", err)
	}

	resetGlobalCLIFlags(t)
	gitident.Reset()
	t.Cleanup(gitident.Reset)

	orig, wdErr := os.Getwd()
	if wdErr != nil {
		t.Fatalf("Getwd: %v", wdErr)
	}
	if chErr := os.Chdir(repoDir); chErr != nil {
		t.Fatalf("Chdir: %v", chErr)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	cmd := newSDDHooksRunImportCmd()
	if runErr := cmd.RunE(cmd, nil); runErr != nil {
		t.Errorf("run-import RunE returned %v, want nil (must always exit 0, D62)", runErr)
	}
}

// TestRunSDDHooksImport_SkipsDuringRebase is runSDDHooksImport's own
// targeted addition (QA rejection fix): D62's documented "skips silently
// during rebase/merge/cherry-pick" behavior — reindexInProgress's own
// sentinel check — was never exercised by any test. A real MERGE_HEAD
// file (exactly what git itself writes mid-merge) is enough: no
// fabrication needed. Verified functionally, not just "did not panic" —
// a record that a real import WOULD pick up is confirmed absent from the
// database afterward.
func TestRunSDDHooksImport_SkipsDuringRebase(t *testing.T) {
	repoDir, fakeHome := sddCLITestRepo(t)
	seedSDDBacklog(t, repoDir, fakeHome, "seed item")
	t.Setenv("HOME", fakeHome)

	if _, _, err := runSDDCmd(t, repoDir, "enable", "--apply"); err != nil {
		t.Fatalf("enable --apply: %v", err)
	}
	runGitOK(t, repoDir, "add", ".")
	runGitOK(t, repoDir, "commit", "-m", "sdd enable")

	// A new, importable backlog record that a real import would create.
	newItem := &sddfile.BacklogRecord{Item: &model.BacklogItem{
		ID: "BL-500", Title: "should stay unimported", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: "wirvii/mneme", Lane: model.LaneStandard,
	}}
	newItemData, mErr := sddfile.MarshalBacklog(newItem)
	if mErr != nil {
		t.Fatalf("MarshalBacklog fixture: %v", mErr)
	}
	if wErr := sddfile.WriteRecord(sddfile.BacklogPath(repoDir, "BL-500"), newItemData); wErr != nil {
		t.Fatalf("WriteRecord fixture: %v", wErr)
	}

	// A real merge-in-progress sentinel — exactly what git itself writes.
	gitDirPath := filepath.Join(repoDir, ".git")
	if err := os.WriteFile(filepath.Join(gitDirPath, "MERGE_HEAD"), []byte("deadbeef\n"), 0o644); err != nil {
		t.Fatalf("write MERGE_HEAD fixture: %v", err)
	}

	resetGlobalCLIFlags(t)
	gitident.Reset()
	t.Cleanup(gitident.Reset)
	orig, wdErr := os.Getwd()
	if wdErr != nil {
		t.Fatalf("Getwd: %v", wdErr)
	}
	if chErr := os.Chdir(repoDir); chErr != nil {
		t.Fatalf("Chdir: %v", chErr)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	runSDDHooksImport()

	svc, cleanup, err := initSDDService()
	if err != nil {
		t.Fatalf("initSDDService: %v", err)
	}
	defer cleanup()
	if _, gErr := svc.BacklogGet(context.Background(), "BL-500"); gErr == nil {
		t.Error("BL-500 must not exist — the import must have been skipped mid-merge")
	}
}

// TestRunSDDHooksImport_NotAGitRepo is runSDDHooksImport's own gitDir()
// error branch — a targeted addition (QA rejection fix): the same "real,
// ordinary condition" every other not-a-git-repo test in this package
// already establishes as legitimate, not fabricated.
func TestRunSDDHooksImport_NotAGitRepo(t *testing.T) {
	dir := t.TempDir() // deliberately NOT a git repository
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	resetGlobalCLIFlags(t)
	gitident.Reset()
	t.Cleanup(gitident.Reset)

	orig, wdErr := os.Getwd()
	if wdErr != nil {
		t.Fatalf("Getwd: %v", wdErr)
	}
	if chErr := os.Chdir(dir); chErr != nil {
		t.Fatalf("Chdir: %v", chErr)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	// Must not panic — the whole point of D62 is that a hook failure never
	// affects the git operation that triggered it.
	runSDDHooksImport()
}

// TestRunSDDHooksImport_ForeignProjectMarkerIsLogged covers
// runSDDHooksImport's own ImportSDDFromRepo-error branch: a real
// foreign-project marker (D50/W6 — exactly what a repository whose
// .mneme/sdd/.mneme-sdd was committed by a DIFFERENT mneme project would
// carry, not a fabricated condition) makes ImportSDDFromRepo itself
// return an error, which must be logged and swallowed (D62), never
// panicking or propagating.
func TestRunSDDHooksImport_ForeignProjectMarkerIsLogged(t *testing.T) {
	repoDir, fakeHome := sddCLITestRepo(t)
	seedSDDBacklog(t, repoDir, fakeHome, "seed item")
	t.Setenv("HOME", fakeHome)

	if _, _, err := runSDDCmd(t, repoDir, "enable", "--apply"); err != nil {
		t.Fatalf("enable --apply: %v", err)
	}

	if err := sddfile.WriteMarker(repoDir, sddfile.Marker{
		SDDVersion: 1, Project: "some/other-project", CreatedAt: "2024-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("WriteMarker (foreign): %v", err)
	}

	resetGlobalCLIFlags(t)
	gitident.Reset()
	t.Cleanup(gitident.Reset)
	orig, wdErr := os.Getwd()
	if wdErr != nil {
		t.Fatalf("Getwd: %v", wdErr)
	}
	if chErr := os.Chdir(repoDir); chErr != nil {
		t.Fatalf("Chdir: %v", chErr)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	// Must not panic — D62's whole point.
	runSDDHooksImport()
}

// TestRunSDDHooksImport_MalformedConfigIsLogged closes runSDDHooksImport's
// own initSDDService()-error branch (QA rejection, round 3 — the
// technique the review found, verified with an 8-point coverage jump): a
// malformed ~/.mneme/config.toml — a real condition a hand-edited or
// half-written config file could leave behind, not a fabricated one —
// makes config.Load fail, which makes initSDDService fail, landing
// exactly on the branch every other test in this file happened to never
// reach (initSDDService almost never fails through any other path).
func TestRunSDDHooksImport_MalformedConfigIsLogged(t *testing.T) {
	repoDir, fakeHome := sddCLITestRepo(t)
	t.Setenv("HOME", fakeHome)

	cfgDir := filepath.Join(fakeHome, ".mneme")
	if mkErr := os.MkdirAll(cfgDir, 0o755); mkErr != nil {
		t.Fatalf("mkdir %s: %v", cfgDir, mkErr)
	}
	// Genuinely invalid TOML — an unterminated table header.
	if wErr := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte("[storage\nbroken = true\n"), 0o644); wErr != nil {
		t.Fatalf("write malformed config.toml: %v", wErr)
	}

	resetGlobalCLIFlags(t)
	gitident.Reset()
	t.Cleanup(gitident.Reset)
	orig, wdErr := os.Getwd()
	if wdErr != nil {
		t.Fatalf("Getwd: %v", wdErr)
	}
	if chErr := os.Chdir(repoDir); chErr != nil {
		t.Fatalf("Chdir: %v", chErr)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	// Must not panic — D62's whole point, exercised here through
	// initSDDService's own failure rather than gitDir's or
	// ImportSDDFromRepo's.
	runSDDHooksImport()
}

// TestSDDStatus_ReportsCompletedFileAsPending is SPEC-131 AC25: after an
// import completes an incomplete file (D46), `mneme sdd status` names it
// under PendingGit — and keeps naming it on a SECOND call, since this is
// derived from `git status` itself, never from an intermediate file that
// could fall out of sync (D54).
func TestSDDStatus_ReportsCompletedFileAsPending(t *testing.T) {
	repoDir, fakeHome := sddCLITestRepo(t)
	seedSDDBacklog(t, repoDir, fakeHome, "seed item")
	t.Setenv("HOME", fakeHome)

	if _, _, err := runSDDCmd(t, repoDir, "enable", "--apply"); err != nil {
		t.Fatalf("enable --apply: %v", err)
	}
	// The marker/export from enable is itself pending commit — commit it
	// so only OUR incomplete-file completion shows up below.
	runGitOK(t, repoDir, "add", ".")
	runGitOK(t, repoDir, "commit", "-m", "sdd enable")

	// A hand-authored, incomplete record.
	incompletePath := sddfile.BacklogPath(repoDir, "BL-900")
	if err := os.WriteFile(incompletePath, []byte("---\ntitle: \"hand written\"\n---\n\na description\n"), 0o644); err != nil {
		t.Fatalf("write incomplete fixture: %v", err)
	}

	if _, _, err := runSDDCmd(t, repoDir, "import"); err != nil {
		t.Fatalf("sdd import: %v", err)
	}

	stdout1, _, err := runSDDCmd(t, repoDir, "status")
	if err != nil {
		t.Fatalf("sdd status (1st): %v", err)
	}
	if !strings.Contains(stdout1, "BL-900") {
		t.Errorf("status (1st call) does not mention the completed file BL-900.md:\n%s", stdout1)
	}

	stdout2, _, err := runSDDCmd(t, repoDir, "status")
	if err != nil {
		t.Fatalf("sdd status (2nd): %v", err)
	}
	if !strings.Contains(stdout2, "BL-900") {
		t.Errorf("status (2nd call) does not mention the completed file BL-900.md:\n%s", stdout2)
	}
}
