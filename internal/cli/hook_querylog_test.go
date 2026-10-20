package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/codegraph"
	"github.com/wirvii/mneme/internal/querylog"
)

// bashInput builds a Bash hookPreToolInput for telemetry tests.
func bashInput(command, sessionID string) hookPreToolInput {
	var inp hookPreToolInput
	inp.ToolName = "Bash"
	inp.ToolInput.Command = command
	inp.SessionID = sessionID
	return inp
}

// readOpportunities returns the querylog events recorded for the mneme repo slug
// under dataDir.
func readOpportunities(t *testing.T, dataDir string) []querylog.Event {
	t.Helper()
	path := codegraph.QuerylogPath(filepath.Join(dataDir, "projects"), repoSlug)
	events, err := querylog.Read(path)
	if err != nil {
		t.Fatalf("querylog.Read: %v", err)
	}
	return events
}

// ---- W2: Bash-search opportunities -----------------------------------------

// TestOpportunity_BashSearchCommands verifies which Bash commands count as
// code exploration: searches (also inside a pipeline) do, other commands do not.
func TestOpportunity_BashSearchCommands(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    int
	}{
		{"grep", "grep -r foo internal/", 1},
		{"pipeline", "git diff | rg fooBar", 1},
		{"non-search", "go test ./...", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dataDir, _ := setupOpportunityDB(t, time.Now().UnixMilli())
			t.Setenv("MNEME_DATA_DIR", dataDir)

			recordCodegraphOpportunity(bashInput(tt.command, "b"), mnemeRepoRoot(t))

			if got := len(readOpportunities(t, dataDir)); got != tt.want {
				t.Errorf("opportunity events = %d, want %d", got, tt.want)
			}
		})
	}
}

// ---- W1: opportunity telemetry ---------------------------------------------

// TestOpportunity_LoggedEveryCall verifies telemetry is recorded on every
// qualified call, not once per session (AC3).
func TestOpportunity_LoggedEveryCall(t *testing.T) {
	dataDir, _ := setupOpportunityDB(t, time.Now().UnixMilli())
	t.Setenv("MNEME_DATA_DIR", dataDir)

	cwd := mnemeRepoRoot(t)
	inp := buildToolInput("Grep", "", "opp-sess", "")

	recordCodegraphOpportunity(inp, cwd)
	recordCodegraphOpportunity(inp, cwd)
	recordCodegraphOpportunity(inp, cwd)

	events := readOpportunities(t, dataDir)
	if len(events) != 3 {
		t.Fatalf("expected 3 opportunity events (every call), got %d", len(events))
	}
	for _, ev := range events {
		if ev.Kind != querylog.KindOpportunity || ev.Tool != "Grep" || ev.Source != "hook" {
			t.Errorf("unexpected opportunity event: %+v", ev)
		}
	}
}

// TestOpportunity_BashToolLabel verifies the Bash executable head is recorded as
// "bash:<cmd>" and no command text is stored.
func TestOpportunity_BashToolLabel(t *testing.T) {
	dataDir, _ := setupOpportunityDB(t, time.Now().UnixMilli())
	t.Setenv("MNEME_DATA_DIR", dataDir)

	cwd := mnemeRepoRoot(t)
	recordCodegraphOpportunity(bashInput("rg --hidden secretPattern internal/", "bl"), cwd)

	events := readOpportunities(t, dataDir)
	if len(events) != 1 {
		t.Fatalf("expected 1 opportunity event, got %d", len(events))
	}
	if events[0].Tool != "bash:rg" {
		t.Errorf("tool label = %q, want bash:rg", events[0].Tool)
	}
	// Privacy: the raw command / query must never be persisted.
	path := codegraph.QuerylogPath(filepath.Join(dataDir, "projects"), repoSlug)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if s := string(data); strings.Contains(s, "secretPattern") || strings.Contains(s, "--hidden") {
		t.Errorf("querylog leaked command content: %s", s)
	}
}

// TestOpportunity_NoGraph_NoLog verifies an unindexed project logs nothing.
func TestOpportunity_NoGraph_NoLog(t *testing.T) {
	// updatedAtMs=0 → DB exists but has 0 nodes; ProbeGraph reports no nodes.
	dataDir, _ := setupOpportunityDB(t, 0)
	t.Setenv("MNEME_DATA_DIR", dataDir)

	cwd := mnemeRepoRoot(t)
	recordCodegraphOpportunity(buildToolInput("Read", "internal/x.go", "ng", ""), cwd)

	if events := readOpportunities(t, dataDir); len(events) != 0 {
		t.Fatalf("expected no opportunity events without an indexed graph, got %d", len(events))
	}
}

// TestOpportunity_OffSwitch verifies MNEME_CODEGRAPH_QUERYLOG=false suppresses
// telemetry.
func TestOpportunity_OffSwitch(t *testing.T) {
	dataDir, _ := setupOpportunityDB(t, time.Now().UnixMilli())
	t.Setenv("MNEME_DATA_DIR", dataDir)
	t.Setenv("MNEME_CODEGRAPH_QUERYLOG", "false")

	recordCodegraphOpportunity(buildToolInput("Read", "internal/x.go", "off", ""), mnemeRepoRoot(t))

	if events := readOpportunities(t, dataDir); len(events) != 0 {
		t.Fatalf("expected no telemetry when querylog disabled, got %d", len(events))
	}
}

// TestBashSearchHead verifies command classification.
func TestBashSearchHead(t *testing.T) {
	cases := []struct {
		cmd      string
		wantHead string
		wantOK   bool
	}{
		{"grep -r foo .", "grep", true},
		{"rg pattern", "rg", true},
		{"git diff | grep foo", "grep", true},
		{"/usr/bin/find . -name '*.go'", "find", true},
		{"cat internal/x.go", "cat", true},
		{"go test ./...", "", false},
		{"ls -la", "", false},
		{"make build && grep foo x", "grep", true},
		{"", "", false},
	}
	for _, tc := range cases {
		gotHead, gotOK := bashSearchHead(tc.cmd)
		if gotOK != tc.wantOK || gotHead != tc.wantHead {
			t.Errorf("bashSearchHead(%q) = (%q, %v), want (%q, %v)", tc.cmd, gotHead, gotOK, tc.wantHead, tc.wantOK)
		}
	}
}
