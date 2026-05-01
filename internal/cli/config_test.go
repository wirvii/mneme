package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/juanftp/mneme/internal/config"
)

// runConfigShow is a test helper that invokes the config show command with the
// given args and returns the captured stdout output.
func runConfigShow(t *testing.T, args ...string) string {
	t.Helper()
	cmd := newConfigCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	// Prepend "show" since we're calling the subcommand directly.
	cmd.SetArgs(append([]string{"show"}, args...))
	_ = cmd.Execute()
	return buf.String()
}

// TestConfigShow_AllSections verifies that the default output (no filter)
// contains all 13 expected section headers.
func TestConfigShow_AllSections(t *testing.T) {
	output := runConfigShow(t)

	for _, section := range validConfigSections {
		header := "[" + section + "]"
		if !strings.Contains(output, header) {
			t.Errorf("output missing section header %q", header)
		}
	}
}

// TestConfigShow_SingleSection verifies that filtering by section name
// produces output containing only that section header.
func TestConfigShow_SingleSection(t *testing.T) {
	output := runConfigShow(t, "graph")

	if !strings.Contains(output, "[graph]") {
		t.Error("output missing [graph] section header")
	}

	// No other section headers should appear.
	for _, section := range validConfigSections {
		if section == "graph" {
			continue
		}
		if strings.Contains(output, "["+section+"]") {
			t.Errorf("output should not contain [%s] when filtering for graph", section)
		}
	}
}

// TestConfigShow_JSON verifies that --json produces valid JSON with the
// expected top-level keys and at least the graph section.
func TestConfigShow_JSON(t *testing.T) {
	output := runConfigShow(t, "--json")

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("JSON unmarshal failed: %v\noutput: %s", err, output)
	}

	// Top-level keys required by the spec.
	if _, ok := result["config_path"]; !ok {
		t.Error("JSON missing 'config_path' key")
	}
	if _, ok := result["config_file_exists"]; !ok {
		t.Error("JSON missing 'config_file_exists' key")
	}
	if _, ok := result["sections"]; !ok {
		t.Error("JSON missing 'sections' key")
	}

	// The graph section must be present.
	sections, ok := result["sections"].(map[string]interface{})
	if !ok {
		t.Fatal("'sections' is not an object")
	}
	if _, ok := sections["graph"]; !ok {
		t.Error("sections missing 'graph' key")
	}
}

// TestConfigShow_JSON_SectionFilter verifies that --json with a section filter
// returns only that section's data.
func TestConfigShow_JSON_SectionFilter(t *testing.T) {
	output := runConfigShow(t, "suggestions", "--json")

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("JSON unmarshal failed: %v\noutput: %s", err, output)
	}

	sections, ok := result["sections"].(map[string]interface{})
	if !ok {
		t.Fatal("'sections' is not an object")
	}
	if _, ok := sections["suggestions"]; !ok {
		t.Error("sections missing 'suggestions' key")
	}
	if len(sections) != 1 {
		t.Errorf("expected exactly 1 section, got %d", len(sections))
	}
}

// TestConfigShow_InvalidSection verifies that an unknown section argument
// causes a non-zero exit and the error message lists valid sections.
func TestConfigShow_InvalidSection(t *testing.T) {
	cmd := newConfigCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"show", "nosuchsection"})

	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for unknown section, got nil")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "nosuchsection") {
		t.Errorf("error message should mention the unknown section, got: %s", errMsg)
	}
	// Check that valid sections are enumerated.
	for _, s := range []string{"storage", "graph", "suggestions"} {
		if !strings.Contains(errMsg, s) {
			t.Errorf("error message should list valid section %q, got: %s", s, errMsg)
		}
	}
}

// TestConfigShow_OriginsInOutput verifies that the table output contains the
// ORIGIN column header and the string "default" for at least some fields.
func TestConfigShow_OriginsInOutput(t *testing.T) {
	output := runConfigShow(t, "storage")

	if !strings.Contains(output, "ORIGIN") {
		t.Error("table output missing ORIGIN column header")
	}
	if !strings.Contains(output, string(config.OriginDefault)) {
		t.Error("table output should contain at least one 'default' origin")
	}
}

// TestFormatOrigin verifies the formatting helper for origin display strings.
func TestFormatOrigin(t *testing.T) {
	tests := []struct {
		origin config.FieldOrigin
		envVar string
		want   string
	}{
		{config.OriginDefault, "", "default"},
		{config.OriginFile, "", "file"},
		{config.OriginEnv, "MNEME_GRAPH_MODE", "env:MNEME_GRAPH_MODE"},
		{config.OriginEnv, "", "env"},
	}

	for _, tc := range tests {
		got := formatOrigin(tc.origin, tc.envVar)
		if got != tc.want {
			t.Errorf("formatOrigin(%q, %q) = %q, want %q", tc.origin, tc.envVar, got, tc.want)
		}
	}
}
