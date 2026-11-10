package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/model"
)

// TestSpecReviewStatsCmd covers SPEC-181 C15's CLI half: the Spanish text
// table and --json, through the real cobra command, with an isolated data
// directory, workflow directory and non-git cwd.
func TestSpecReviewStatsCmd(t *testing.T) {
	const project = "review-stats-project"
	dataDir := t.TempDir()
	workflowDir := t.TempDir()
	t.Setenv("MNEME_WORKFLOW_DIR", workflowDir)

	var ids []string
	for _, title := range []string{"first spec", "second spec"} {
		out, _, err := runBacklogCmd(t, dataDir, project, "spec", "new", title, "--lane", "standard")
		if err != nil {
			t.Fatalf("spec new: %v", err)
		}
		ids = append(ids, specIDFromCreateOutput(t, out))
	}
	if _, _, err := runBacklogCmd(t, dataDir, project, "spec", "new", "tiny", "--lane", "trivial", "--scope", "internal/**"); err != nil {
		t.Fatalf("spec new trivial: %v", err)
	}
	writeReviewReportCLI(t, workflowDir, project, ids[0], "review-spec")

	t.Run("text table", func(t *testing.T) {
		out, _, err := runBacklogCmd(t, dataDir, project, "spec", "review-stats", "--limit", "0")
		if err != nil {
			t.Fatalf("review-stats: %v", err)
		}
		for _, want := range []string{"SPEC", "ESTADO", "REV", "M1", "M2", "B/M/N", "CORR/REF/ESC/DESC/RIESGO", "RECH.QA", "REAP.", "FICHAS", "DONE"} {
			if !strings.Contains(out, want) {
				t.Errorf("header lacks %q:\n%s", want, out)
			}
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) != 3 {
			t.Fatalf("want header + 2 standard specs, got %d lines:\n%s", len(lines), out)
		}
		// Newest first: the second spec, without reports, comes before the first.
		second := strings.Fields(lines[1])
		first := strings.Fields(lines[2])
		if second[0] != ids[1] || first[0] != ids[0] {
			t.Fatalf("order = %s, %s; want %s, %s", second[0], first[0], ids[1], ids[0])
		}
		// second: REV no, M1 —, M2 —; first: REV sí, M1 3/3, M2 —.
		if second[2] != "no" || second[3] != "—" || second[4] != "—" {
			t.Errorf("second spec row = %v", second)
		}
		if first[2] != "sí" || first[3] != "3/3" || first[4] != "—" {
			t.Errorf("first spec row = %v", first)
		}
	})

	t.Run("limit", func(t *testing.T) {
		out, _, err := runBacklogCmd(t, dataDir, project, "spec", "review-stats", "--limit", "1")
		if err != nil {
			t.Fatalf("review-stats: %v", err)
		}
		if lines := strings.Split(strings.TrimSpace(out), "\n"); len(lines) != 2 || !strings.Contains(lines[1], ids[1]) {
			t.Errorf("--limit 1 output:\n%s", out)
		}
	})

	t.Run("json", func(t *testing.T) {
		out, _, err := runBacklogCmd(t, dataDir, project, "spec", "review-stats", "--limit", "0", "--json")
		if err != nil {
			t.Fatalf("review-stats --json: %v", err)
		}
		var rows []model.SpecReviewStats
		if err := json.Unmarshal([]byte(out), &rows); err != nil {
			t.Fatalf("unmarshal: %v\n%s", err, out)
		}
		if len(rows) != 2 || rows[0].SpecID != ids[1] || rows[1].SpecID != ids[0] {
			t.Fatalf("rows = %+v", rows)
		}
		if !rows[1].SpecReview.Present || rows[1].SpecReview.Completed != 3 || !rows[1].Reviewed {
			t.Errorf("first spec SpecReview = %+v reviewed=%v", rows[1].SpecReview, rows[1].Reviewed)
		}
		if rows[0].FollowUpItems == nil {
			t.Error("follow_up_items must be [] not null")
		}
		if strings.Contains(out, `"follow_up_items": null`) {
			t.Errorf("follow_up_items serialised as null:\n%s", out)
		}
	})

	t.Run("empty project", func(t *testing.T) {
		out, _, err := runBacklogCmd(t, t.TempDir(), "empty-project", "spec", "review-stats")
		if err != nil {
			t.Fatalf("review-stats: %v", err)
		}
		if !strings.Contains(out, "No hay specs standard") {
			t.Errorf("output = %q", out)
		}
	})

	t.Run("help says experimental", func(t *testing.T) {
		out, _, err := runBacklogCmd(t, t.TempDir(), project, "spec", "review-stats", "--help")
		if err != nil {
			t.Fatalf("--help: %v", err)
		}
		if !strings.Contains(strings.ToLower(out), "experimental") {
			t.Errorf("help lacks the word experimental:\n%s", out)
		}
	})
}
