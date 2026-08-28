package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/gitident"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/sddfile"
)

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
