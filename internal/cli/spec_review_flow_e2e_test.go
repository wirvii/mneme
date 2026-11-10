package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/db"
)

// TestSpecReviewFlow_E2E covers SPEC-181 C21: the whole fresh-review flow
// through the real cobra commands, with an isolated data directory, an
// isolated workflow directory and a non-git cwd. A marked standard spec is
// stopped at each guarded transition until its report is valid and current;
// a second spec, unmarked by SQL to simulate one already in progress, walks the
// same path with no reports at all.
func TestSpecReviewFlow_E2E(t *testing.T) {
	const project = "review-flow/project"
	dataDir := t.TempDir()
	workflowDir := t.TempDir()
	t.Setenv("MNEME_WORKFLOW_DIR", workflowDir)

	run := func(args ...string) (string, error) {
		t.Helper()
		out, _, err := runBacklogCmd(t, dataDir, project, args...)
		return out, err
	}
	newSpec := func(title string) string {
		t.Helper()
		out, err := run("spec", "new", title, "--lane", "standard")
		if err != nil {
			t.Fatalf("spec new: %v", err)
		}
		return specIDFromCreateOutput(t, out)
	}
	advance := func(id string) error {
		t.Helper()
		_, err := run("spec", "advance", id, "--by", "orch")
		return err
	}
	mustAdvance := func(id, step string) {
		t.Helper()
		if err := advance(id); err != nil {
			t.Fatalf("%s: spec advance: %v", step, err)
		}
	}
	specDir := func(id string) string {
		return filepath.Join(workflowDir, strings.ReplaceAll(project, "/", "-"), "specs", id)
	}

	t.Run("a marked spec is stopped at every guarded transition", func(t *testing.T) {
		id := newSpec("marked spec")
		mustAdvance(id, "to speccing")
		writeFrontierCriteriaCLI(t, workflowDir, project, id)

		err := advance(id)
		if err == nil || !strings.Contains(err.Error(), "revision-spec.toml") {
			t.Fatalf("speccing -> specced without a report: err = %v, want it naming revision-spec.toml", err)
		}

		writeReviewReportCLI(t, workflowDir, project, id, "review-spec")
		mustAdvance(id, "specced")

		// A change asked for at the gate: tarea.md edited after the report.
		report := filepath.Join(specDir(id), "revision-spec.toml")
		info, statErr := os.Stat(report)
		if statErr != nil {
			t.Fatalf("stat report: %v", statErr)
		}
		task := filepath.Join(specDir(id), "tarea.md")
		if err := os.WriteFile(task, []byte("# tarea\n"), 0o644); err != nil {
			t.Fatalf("write tarea.md: %v", err)
		}
		later := info.ModTime().Add(5 * time.Second)
		if err := os.Chtimes(task, later, later); err != nil {
			t.Fatalf("chtimes tarea.md: %v", err)
		}
		err = advance(id)
		if err == nil || !strings.Contains(err.Error(), "caducado") {
			t.Fatalf("specced -> planning with a stale report: err = %v, want a stale-report error", err)
		}

		writeReviewReportCLI(t, workflowDir, project, id, "review-spec")
		mustAdvance(id, "planning")
		mustAdvance(id, "planned")

		err = advance(id)
		if err == nil || !strings.Contains(err.Error(), "revision-plan.toml") {
			t.Fatalf("planned -> implementing without a plan report: err = %v, want it naming revision-plan.toml", err)
		}
		writeReviewReportCLI(t, workflowDir, project, id, "review-plan")
		mustAdvance(id, "implementing")
	})

	t.Run("a spec already in progress needs no reports", func(t *testing.T) {
		id := newSpec("in-progress spec")
		mustAdvance(id, "to speccing")

		dbPath := filepath.Join(dataDir, "projects", strings.ReplaceAll(project, "/", "-")+".db")
		database, err := db.Open(dbPath)
		if err != nil {
			t.Fatalf("open project db: %v", err)
		}
		if _, err := database.Exec(`UPDATE specs SET review_required = 0 WHERE id = ?`, id); err != nil {
			database.Close()
			t.Fatalf("clear mark: %v", err)
		}
		if err := database.Close(); err != nil {
			t.Fatalf("close project db: %v", err)
		}

		writeFrontierCriteriaCLI(t, workflowDir, project, id)
		for _, step := range []string{"specced", "planning", "planned", "implementing"} {
			mustAdvance(id, step)
		}
		for _, report := range []string{"revision-spec.toml", "revision-plan.toml"} {
			if _, err := os.Stat(filepath.Join(specDir(id), report)); !os.IsNotExist(err) {
				t.Errorf("%s exists for an unmarked spec (err=%v)", report, err)
			}
		}
	})
}
