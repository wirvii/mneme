package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/sddfile"
	"github.com/wirvii/mneme/internal/service"
)

// commitSDDMarker writes a valid, committed .mneme-sdd marker at repoDir —
// the exact fact (D1) SPEC-140 uses to distinguish "repository already
// activated, new machine" from a virgin repository — and commits it.
func commitSDDMarker(t *testing.T, repoDir string) {
	t.Helper()
	if err := sddfile.WriteMarker(repoDir, sddfile.Marker{
		SDDVersion: 1, Project: "wirvii/mneme",
		CreatedAt: "2026-09-01T00:00:00Z", LastExportAt: "2026-09-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}
	runGitOK(t, repoDir, "add", ".")
	runGitOK(t, repoDir, "commit", "-m", "marker")
}

// TestSDDEnablePreview_VirginRepoKeepsEveryWarning is SPEC-140 AC11: a
// repository with no committed marker keeps printing every one of the four
// publication warnings, derived by iterating service.SDDWarnings() rather
// than copying the literal strings — a fifth warning added later is
// covered automatically, a deleted one fails automatically.
func TestSDDEnablePreview_VirginRepoKeepsEveryWarning(t *testing.T) {
	repoDir, fakeHome := sddCLITestRepo(t)
	t.Setenv("HOME", fakeHome)

	stdout, _, err := runSDDCmd(t, repoDir, "enable")
	if err != nil {
		t.Fatalf("sdd enable: %v", err)
	}
	for _, w := range service.SDDWarnings() {
		if !strings.Contains(stdout, w) {
			t.Errorf("missing warning in virgin preview output: %q", w)
		}
	}
	if !strings.Contains(stdout, "--apply") {
		t.Error("virgin preview must still ask for --apply")
	}
}

// TestSDDEnablePreview_AlreadyEnabledCloneDoesNotRefuse is SPEC-140 AC13:
// the exact montage of a clone (marker committed, a record whose anchor
// this base does not know) must no longer make the preview fail — it
// exits 0, does not mention ErrSDDNotConverged, and names `mneme sdd
// import`. Its control (same files, no marker) must keep refusing: D45's
// negative is not relaxed where it still applies.
func TestSDDEnablePreview_AlreadyEnabledCloneDoesNotRefuse(t *testing.T) {
	repoDir, fakeHome := sddCLITestRepo(t)
	t.Setenv("HOME", fakeHome)

	foreign := &sddfile.BacklogRecord{Item: &model.BacklogItem{
		ID: "BL-050", Title: "de otra maquina", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: "wirvii/mneme", Lane: model.LaneStandard,
		UUID: "01a044bc-7c25-7448-87e9-febc5c5982ee",
	}}
	data, err := sddfile.MarshalBacklog(foreign)
	if err != nil {
		t.Fatalf("MarshalBacklog fixture: %v", err)
	}
	path := sddfile.BacklogPath(repoDir, "BL-050")
	if err := sddfile.WriteRecord(path, data); err != nil {
		t.Fatalf("WriteRecord fixture: %v", err)
	}
	runGitOK(t, repoDir, "add", ".")
	runGitOK(t, repoDir, "commit", "-m", "foreign record")

	commitSDDMarker(t, repoDir)

	stdout, stderr, err := runSDDCmd(t, repoDir, "enable")
	if err != nil {
		t.Fatalf("sdd enable must succeed with the marker present: err=%v stdout=%q stderr=%q", err, stdout, stderr)
	}
	if strings.Contains(stdout+stderr, "ErrSDDNotConverged") || strings.Contains(stdout+stderr, "sdd convergence") {
		t.Errorf("preview must not mention the convergence refusal here: %s", stdout+stderr)
	}
	if !strings.Contains(stdout, "mneme sdd import") {
		t.Errorf("preview must name `mneme sdd import`: %s", stdout)
	}

	// Control: the SAME foreign record, but WITHOUT the marker, must keep
	// refusing — D45's negative is reserved for exactly that case.
	controlRepo, controlHome := sddCLITestRepo(t)
	t.Setenv("HOME", controlHome)
	controlPath := sddfile.BacklogPath(controlRepo, "BL-050")
	if err := sddfile.WriteRecord(controlPath, data); err != nil {
		t.Fatalf("WriteRecord control fixture: %v", err)
	}
	runGitOK(t, controlRepo, "add", ".")
	runGitOK(t, controlRepo, "commit", "-m", "foreign record, no marker")

	_, controlStderr, controlErr := runSDDCmd(t, controlRepo, "enable")
	if controlErr == nil {
		t.Fatalf("control (no marker) must still refuse with ErrSDDNotConverged")
	}
	if !strings.Contains(controlErr.Error()+controlStderr, "sdd convergence") {
		t.Errorf("control did not fail for the expected reason: %v / %s", controlErr, controlStderr)
	}
}

// TestRenderSDDEnableResult_AlreadyEnabledVsVirgin is SPEC-140 AC12: the two
// cases over the SAME render function, in one table. The virgin case is the
// control — without it, an implementation that always prints the new
// report would also pass.
func TestRenderSDDEnableResult_AlreadyEnabledVsVirgin(t *testing.T) {
	t.Run("already enabled, hooks missing", func(t *testing.T) {
		var buf bytes.Buffer
		renderSDDEnableResult(&buf, &service.SDDEnableResult{
			RepoRoot:          "/repo",
			AlreadyEnabled:    true,
			EnabledSince:      "2026-09-01T00:00:00Z",
			HooksInstalled:    false,
			UnknownToThisBase: []string{"a", "b"},
		})
		out := buf.String()
		if !strings.Contains(out, "mneme sdd hooks install") {
			t.Errorf("missing hooks-install instruction: %s", out)
		}
		if !strings.Contains(out, "mneme sdd import") {
			t.Errorf("missing import instruction: %s", out)
		}
		for _, w := range service.SDDWarnings() {
			if strings.Contains(out, w) {
				t.Errorf("already-enabled output must not contain a publication warning: %q\n%s", w, out)
			}
		}
		if strings.HasPrefix(out, "Plan:") {
			t.Errorf("already-enabled output must not start with the plan/count header: %s", out)
		}
	})

	t.Run("virgin repo (control)", func(t *testing.T) {
		var buf bytes.Buffer
		renderSDDEnableResult(&buf, &service.SDDEnableResult{
			RepoRoot: "/repo",
			Warnings: service.SDDWarnings(),
		})
		out := buf.String()
		for _, w := range service.SDDWarnings() {
			if !strings.Contains(out, w) {
				t.Errorf("virgin output missing warning: %q\n%s", w, out)
			}
		}
		if strings.Contains(out, "already enabled for this team") {
			t.Errorf("virgin output must not claim the mechanism is already enabled: %s", out)
		}
	})
}
