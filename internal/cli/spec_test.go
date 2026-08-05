package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSpecList_JSONIsBareArray is AC15's spec_list half: `mneme spec list
// --json` must stay a bare JSON array (never the {specs,total} MCP envelope)
// — printJSON is fed resp.Specs, not the envelope.
func TestSpecList_JSONIsBareArray(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-spec-list-json"

	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"spec", "new", "First spec", "--lane", "standard"); err != nil {
		t.Fatalf("spec new: %v (stderr=%s)", err, stderr)
	}

	stdout, stderr, err := runBacklogCmd(t, dataDir, project, "spec", "list", "--json")
	if err != nil {
		t.Fatalf("spec list --json: %v (stderr=%s)", err, stderr)
	}

	var specs []map[string]any
	if err := json.Unmarshal([]byte(stdout), &specs); err != nil {
		t.Fatalf("spec list --json did not decode as a bare array: %v\nstdout=%s", err, stdout)
	}
	if len(specs) != 1 {
		t.Fatalf("expected 1 spec, got %d", len(specs))
	}
	if _, hasTotal := specs[0]["total"]; hasTotal {
		t.Error("individual spec object must not have a 'total' field — that belongs to the envelope, not the item")
	}
}

// TestSpecList_TableOutputUnchanged is AC15's plain-text half for spec list:
// the table format is untouched by this spec.
func TestSpecList_TableOutputUnchanged(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-spec-list-table"

	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"spec", "new", "Table format spec", "--lane", "standard"); err != nil {
		t.Fatalf("spec new: %v (stderr=%s)", err, stderr)
	}

	stdout, stderr, err := runBacklogCmd(t, dataDir, project, "spec", "list")
	if err != nil {
		t.Fatalf("spec list: %v (stderr=%s)", err, stderr)
	}

	if !strings.Contains(stdout, "SPEC-001") || !strings.Contains(stdout, "Table format spec") {
		t.Errorf("unexpected table output: %q", stdout)
	}
	if !strings.Contains(stdout, "[draft") {
		t.Errorf("expected bracketed status column, got: %q", stdout)
	}
}
