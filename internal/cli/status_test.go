package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// TestStatus_JSONBacklogAndSpecsAreBareArrays is AC15's status half:
// renderFullStatus discards BacklogList/SpecList's error and ranges over
// the result directly (cli/status.go) — safe with the BacklogListResponse/
// SpecListResponse envelopes returned BY VALUE (D10). The dashboard's own
// JSON fields "backlog" and "specs_in_progress" stay bare arrays of full
// items, unaffected by the envelope change.
func TestStatus_JSONBacklogAndSpecsAreBareArrays(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-status-json"

	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "add", "Dashboard item", "--lane", "standard"); err != nil {
		t.Fatalf("backlog add: %v (stderr=%s)", err, stderr)
	}

	stdout, stderr, err := runBacklogCmd(t, dataDir, project, "status", "--json")
	if err != nil {
		t.Fatalf("status --json: %v (stderr=%s)", err, stderr)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("status --json did not decode: %v\nstdout=%s", err, stdout)
	}

	backlog, ok := out["backlog"].([]any)
	if !ok {
		t.Fatalf("status --json 'backlog' field is not a bare array: %T", out["backlog"])
	}
	if len(backlog) != 1 {
		t.Fatalf("expected 1 backlog item, got %d", len(backlog))
	}
	item, ok := backlog[0].(map[string]any)
	if !ok {
		t.Fatalf("backlog[0] is not an object: %T", backlog[0])
	}
	if item["title"] != "Dashboard item" {
		t.Errorf("backlog[0].title = %v, want %q", item["title"], "Dashboard item")
	}
	if _, hasExcerpt := item["excerpt"]; hasExcerpt {
		t.Error("status --json backlog items must not carry an 'excerpt' field — that is the MCP view's projection")
	}

	// specs_in_progress is nil (JSON null) when there are no in-progress
	// specs — pre-existing behaviour, unrelated to SPEC-109. Only reject the
	// shape this spec could have broken: an object envelope like
	// {"specs":[...],"total":N} instead of a bare array/null.
	if specs, present := out["specs_in_progress"]; present && specs != nil {
		if _, ok := specs.([]any); !ok {
			t.Fatalf("status --json 'specs_in_progress' field is not a bare array: %T", specs)
		}
	}
}

// TestStatus_MarksFrozenSpecInProgress is SPEC-126 AC20: a frozen spec
// stays IN SPECS IN PROGRESS (never pulled out — that would hide it, the
// very defect this spec fixes, from the other side), with a mark on its
// second line, and "specs_in_progress" in --json keeps containing it too.
func TestStatus_MarksFrozenSpecInProgress(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-status-frozen-spec"

	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "add", "To archive", "--lane", "standard"); err != nil {
		t.Fatalf("backlog add: %v (stderr=%s)", err, stderr)
	}
	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "refine", "BL-001", "--refinement", "details"); err != nil {
		t.Fatalf("refine: %v (stderr=%s)", err, stderr)
	}
	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "promote", "BL-001"); err != nil {
		t.Fatalf("promote: %v (stderr=%s)", err, stderr)
	}
	// Advance so the spec lands in SPECS IN PROGRESS (draft is excluded).
	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"spec", "advance", "SPEC-001", "--by", "orchestrator"); err != nil {
		t.Fatalf("spec advance: %v (stderr=%s)", err, stderr)
	}
	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "archive", "BL-001", "--reason", "abandoned"); err != nil {
		t.Fatalf("archive: %v (stderr=%s)", err, stderr)
	}

	stdout, stderr, err := runBacklogCmd(t, dataDir, project, "status")
	if err != nil {
		t.Fatalf("status: %v (stderr=%s)", err, stderr)
	}
	if !strings.Contains(stdout, "SPECS IN PROGRESS") {
		t.Fatalf("expected a SPECS IN PROGRESS section, got %q", stdout)
	}
	if !strings.Contains(stdout, "SPEC-001") {
		t.Errorf("expected the frozen spec to still appear in the dashboard, got %q", stdout)
	}
	if !strings.Contains(stdout, "frozen: BL-001 was archived") {
		t.Errorf("expected the frozen mark naming BL-001, got %q", stdout)
	}

	jsonOut, stderr, err := runBacklogCmd(t, dataDir, project, "status", "--json")
	if err != nil {
		t.Fatalf("status --json: %v (stderr=%s)", err, stderr)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &out); err != nil {
		t.Fatalf("status --json did not decode: %v\nstdout=%s", err, jsonOut)
	}
	inProgress, ok := out["specs_in_progress"].([]any)
	if !ok || len(inProgress) != 1 {
		t.Fatalf("expected 1 spec in specs_in_progress, got %#v", out["specs_in_progress"])
	}
	frozenSpecs, ok := out["frozen_specs"].(map[string]any)
	if !ok {
		t.Fatalf("expected a 'frozen_specs' object, got %#v", out["frozen_specs"])
	}
	if _, ok := frozenSpecs["SPEC-001"]; !ok {
		t.Errorf("expected SPEC-001 in frozen_specs, got %#v", frozenSpecs)
	}
}

// TestStatus_JSON_NoFrozenSpecs_KeyAbsent is SPEC-126 AC21's negative half:
// with nothing frozen, "frozen_specs" is absent (not an empty object), and
// "backlog"/"specs_in_progress" remain the bare arrays
// TestStatus_JSONBacklogAndSpecsAreBareArrays already pins.
func TestStatus_JSON_NoFrozenSpecs_KeyAbsent(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-status-no-frozen"

	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"spec", "new", "Live spec", "--lane", "standard"); err != nil {
		t.Fatalf("spec new: %v (stderr=%s)", err, stderr)
	}
	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"spec", "advance", "SPEC-001", "--by", "orchestrator"); err != nil {
		t.Fatalf("spec advance: %v (stderr=%s)", err, stderr)
	}

	jsonOut, stderr, err := runBacklogCmd(t, dataDir, project, "status", "--json")
	if err != nil {
		t.Fatalf("status --json: %v (stderr=%s)", err, stderr)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &out); err != nil {
		t.Fatalf("status --json did not decode: %v\nstdout=%s", err, jsonOut)
	}
	if _, ok := out["frozen_specs"]; ok {
		t.Errorf("expected 'frozen_specs' ABSENT when nothing is frozen, got %#v", out["frozen_specs"])
	}
}

// synthesisNoticeLine is the SPEC-187 status warning for n syntheses.
func synthesisNoticeLine(n int) string {
	return "Active syntheses: " + strconv.Itoa(n) + " (generation is off; run mneme mem synthesis-sweep --apply to retire them)"
}

// seedStatusSyntheses writes n active syntheses for project into the
// project database under dataDir, the database `mneme status` opens.
func seedStatusSyntheses(t *testing.T, dataDir, project string, n int) {
	t.Helper()
	cfg := config.Default()
	cfg.Storage.DataDir = dataDir
	d, err := db.Open(cfg.ProjectDBPath(project))
	if err != nil {
		t.Fatalf("open project db: %v", err)
	}
	defer d.Close()
	s := store.NewMemoryStore(d)
	for i := 0; i < n; i++ {
		if _, err := s.Create(context.Background(), &model.Memory{
			Type: model.TypeSynthesis, Scope: model.ScopeProject, Project: project,
			Title: "status synthesis " + strconv.Itoa(i), Content: "body", Importance: 0.85,
		}); err != nil {
			t.Fatalf("seed synthesis: %v", err)
		}
	}
}

// lineAfter reports whether want is the line immediately after the first
// line that contains anchor.
func lineAfter(out, anchor, want string) bool {
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if strings.Contains(l, anchor) && i+1 < len(lines) {
			return lines[i+1] == want
		}
	}
	return false
}

// TestStatus_SynthesisNotice_ShownWhenActive verifies the SPEC-187 warning
// in both panels: right after "Memories: N active" in the basic one, right
// under "  N project - M global" in the full one, and active_syntheses in
// --json.
func TestStatus_SynthesisNotice_ShownWhenActive(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		projectDB, err := db.OpenMemory()
		if err != nil {
			t.Fatalf("open project db: %v", err)
		}
		globalDB, err := db.OpenMemory()
		if err != nil {
			t.Fatalf("open global db: %v", err)
		}
		t.Cleanup(func() { projectDB.Close(); globalDB.Close() })
		ps := store.NewMemoryStore(projectDB)
		for i := 0; i < 2; i++ {
			if _, err := ps.Create(context.Background(), &model.Memory{
				Type: model.TypeSynthesis, Scope: model.ScopeProject, Project: "org/basic",
				Title: "s" + strconv.Itoa(i), Content: "c", Importance: 0.85,
			}); err != nil {
				t.Fatalf("seed: %v", err)
			}
		}
		svc := service.NewMemoryService(ps, store.NewMemoryStore(globalDB), config.Default(), "org/basic", embed.NopEmbedder{})

		render := func(asJSON bool) string {
			return captureStdout(t, func() {
				if err := renderBasicStatus(context.Background(), svc, asJSON); err != nil {
					t.Errorf("renderBasicStatus: %v", err)
				}
			})
		}
		out := render(false)
		if !lineAfter(out, "Memories: ", synthesisNoticeLine(2)) {
			t.Errorf("warning not right after Memories:\n%s", out)
		}
		js := render(true)
		var m map[string]any
		if err := json.Unmarshal([]byte(js), &m); err != nil {
			t.Fatalf("decode: %v\n%s", err, js)
		}
		if m["active_syntheses"] != float64(2) {
			t.Errorf("active_syntheses = %v, want 2", m["active_syntheses"])
		}
	})

	t.Run("full", func(t *testing.T) {
		dataDir := t.TempDir()
		project := "test-status-synth"
		seedStatusSyntheses(t, dataDir, project, 3)

		stdout, stderr, err := runBacklogCmd(t, dataDir, project, "status")
		if err != nil {
			t.Fatalf("status: %v (stderr=%s)", err, stderr)
		}
		if !lineAfter(stdout, " project - ", "  "+synthesisNoticeLine(3)) {
			t.Errorf("warning not right under the MEMORIES counts:\n%s", stdout)
		}
		js, stderr, err := runBacklogCmd(t, dataDir, project, "status", "--json")
		if err != nil {
			t.Fatalf("status --json: %v (stderr=%s)", err, stderr)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(js), &m); err != nil {
			t.Fatalf("decode: %v\n%s", err, js)
		}
		if m["active_syntheses"] != float64(3) {
			t.Errorf("active_syntheses = %v, want 3", m["active_syntheses"])
		}
	})
}

// TestStatus_SynthesisNotice_AbsentWhenNone verifies the line and the JSON
// key are absent without syntheses, and also when generation is on (the
// warning is about leftovers of a generator that is off).
func TestStatus_SynthesisNotice_AbsentWhenNone(t *testing.T) {
	assertAbsent := func(t *testing.T, dataDir, project string) {
		t.Helper()
		stdout, stderr, err := runBacklogCmd(t, dataDir, project, "status")
		if err != nil {
			t.Fatalf("status: %v (stderr=%s)", err, stderr)
		}
		if strings.Contains(stdout, "Active syntheses") {
			t.Errorf("warning printed:\n%s", stdout)
		}
		js, stderr, err := runBacklogCmd(t, dataDir, project, "status", "--json")
		if err != nil {
			t.Fatalf("status --json: %v (stderr=%s)", err, stderr)
		}
		if strings.Contains(js, "active_syntheses") {
			t.Errorf("active_syntheses key present:\n%s", js)
		}
	}

	t.Run("no syntheses", func(t *testing.T) {
		dataDir := t.TempDir()
		project := "test-status-nosynth"
		seedStatusSyntheses(t, dataDir, project, 0)
		assertAbsent(t, dataDir, project)
	})

	t.Run("generation on", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		if err := os.MkdirAll(filepath.Join(home, ".mneme"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(home, ".mneme", "config.toml"), []byte("[graph]\nsynthesis_enabled = true\n"), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
		dataDir := t.TempDir()
		project := "test-status-synth-on"
		seedStatusSyntheses(t, dataDir, project, 2)
		assertAbsent(t, dataDir, project)
	})
}
