package cli

import (
	"encoding/json"
	"strings"
	"testing"
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
