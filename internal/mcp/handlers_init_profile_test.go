package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitTool_ReportsLegacyProfileTraces covers SPEC-162 C7's MCP half: the
// init tool names the pin and the lock the retired team profiles left, and
// never deletes either of them.
func TestInitTool_ReportsLegacyProfileTraces(t *testing.T) {
	srv := newTestServerWithSDD(t)

	dir := t.TempDir()
	pin := filepath.Join(dir, ".mneme-profile")
	lock := filepath.Join(dir, ".mneme", "profile.lock")
	if err := os.MkdirAll(filepath.Dir(lock), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{pin, lock} {
		if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "init",
		Arguments: mustMarshal(t, map[string]any{"repo_root": dir, "check": true}),
	})

	var out struct {
		DriftFindings []string `json:"drift_findings"`
	}
	unmarshalToolText(t, resp, &out)

	joined := strings.Join(out.DriftFindings, "\n")
	for _, p := range []string{pin, lock} {
		if !strings.Contains(joined, p) {
			t.Errorf("drift_findings does not mention %s:\n%s", p, joined)
		}
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s must survive the init tool (err=%v)", p, err)
		}
	}
}
