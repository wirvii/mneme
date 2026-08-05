package cli

import (
	"encoding/json"
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
