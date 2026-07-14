package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/codegraph"
	"github.com/wirvii/mneme/internal/querylog"
)

// bashInput builds a Bash hookPreToolInput for nudge/telemetry tests.
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

// ---- W2: Bash-search nudge --------------------------------------------------

func TestNudge_FiresOnBashSearch(t *testing.T) {
	dataDir, _ := setupNudgeDB(t, time.Now().UnixMilli())
	t.Setenv("MNEME_DATA_DIR", dataDir)

	cwd := mnemeRepoRoot(t)
	inp := bashInput("grep -r foo internal/", "b1")

	var stdout bytes.Buffer
	maybeEmitCodegraphNudge(inp, cwd, &stdout, io.Discard)

	if !strings.Contains(stdout.String(), "codegraph-nudge") {
		t.Errorf("nudge should fire for Bash grep; stdout: %s", stdout.String())
	}
}

func TestNudge_FiresOnBashPipeline(t *testing.T) {
	dataDir, _ := setupNudgeDB(t, time.Now().UnixMilli())
	t.Setenv("MNEME_DATA_DIR", dataDir)

	cwd := mnemeRepoRoot(t)
	inp := bashInput("git diff | rg fooBar", "b2")

	var stdout bytes.Buffer
	maybeEmitCodegraphNudge(inp, cwd, &stdout, io.Discard)

	if !strings.Contains(stdout.String(), "codegraph-nudge") {
		t.Errorf("nudge should fire for 'git diff | rg' pipeline; stdout: %s", stdout.String())
	}
}

func TestNudge_NotFiredOnBashNonSearch(t *testing.T) {
	dataDir, _ := setupNudgeDB(t, time.Now().UnixMilli())
	t.Setenv("MNEME_DATA_DIR", dataDir)

	cwd := mnemeRepoRoot(t)
	inp := bashInput("go test ./...", "b3")

	var stdout bytes.Buffer
	maybeEmitCodegraphNudge(inp, cwd, &stdout, io.Discard)

	if strings.Contains(stdout.String(), "codegraph-nudge") {
		t.Errorf("nudge must NOT fire for 'go test'; stdout: %s", stdout.String())
	}
}

// TestNudge_SharedBudget_ReadThenBash verifies the once-per-session budget is
// shared across tools: a Read fires the nudge, a subsequent Bash-search in the
// same session is suppressed.
func TestNudge_SharedBudget_ReadThenBash(t *testing.T) {
	dataDir, _ := setupNudgeDB(t, time.Now().UnixMilli())
	t.Setenv("MNEME_DATA_DIR", dataDir)
	cleanStatefile(dataDir)

	cwd := mnemeRepoRoot(t)

	var out1, out2 bytes.Buffer
	maybeEmitCodegraphNudge(buildNudgeInput("Read", "internal/x.go", "shared-sess", ""), cwd, &out1, io.Discard)
	maybeEmitCodegraphNudge(bashInput("grep -r foo internal/", "shared-sess"), cwd, &out2, io.Discard)

	if !strings.Contains(out1.String(), "codegraph-nudge") {
		t.Errorf("first (Read) call should emit nudge; stdout: %s", out1.String())
	}
	if strings.Contains(out2.String(), "codegraph-nudge") {
		t.Errorf("second (Bash) call in same session should be suppressed; stdout: %s", out2.String())
	}
}

// TestNudge_MandatoryTone verifies the nudge copy uses mandatory language.
func TestNudge_MandatoryTone(t *testing.T) {
	dataDir, _ := setupNudgeDB(t, time.Now().UnixMilli())
	t.Setenv("MNEME_DATA_DIR", dataDir)

	cwd := mnemeRepoRoot(t)
	var stdout bytes.Buffer
	maybeEmitCodegraphNudge(buildNudgeInput("Read", "internal/x.go", "tone", ""), cwd, &stdout, io.Discard)

	out := stdout.String()
	for _, want := range []string{"MANDATORY", "FIRST", "subagents", "codegraph_search"} {
		if !strings.Contains(out, want) {
			t.Errorf("nudge should contain %q (mandatory tone); stdout: %s", want, out)
		}
	}
}

// ---- W1: opportunity telemetry ---------------------------------------------

// TestOpportunity_LoggedEveryCall verifies telemetry is recorded on every
// qualified call, not once per session (AC3).
func TestOpportunity_LoggedEveryCall(t *testing.T) {
	dataDir, _ := setupNudgeDB(t, time.Now().UnixMilli())
	t.Setenv("MNEME_DATA_DIR", dataDir)
	cleanStatefile(dataDir)

	cwd := mnemeRepoRoot(t)
	inp := buildNudgeInput("Grep", "", "opp-sess", "")

	maybeEmitCodegraphNudge(inp, cwd, io.Discard, io.Discard)
	maybeEmitCodegraphNudge(inp, cwd, io.Discard, io.Discard)
	maybeEmitCodegraphNudge(inp, cwd, io.Discard, io.Discard)

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
	dataDir, _ := setupNudgeDB(t, time.Now().UnixMilli())
	t.Setenv("MNEME_DATA_DIR", dataDir)

	cwd := mnemeRepoRoot(t)
	maybeEmitCodegraphNudge(bashInput("rg --hidden secretPattern internal/", "bl"), cwd, io.Discard, io.Discard)

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
	dataDir, _ := setupNudgeDB(t, 0)
	t.Setenv("MNEME_DATA_DIR", dataDir)

	cwd := mnemeRepoRoot(t)
	maybeEmitCodegraphNudge(buildNudgeInput("Read", "internal/x.go", "ng", ""), cwd, io.Discard, io.Discard)

	if events := readOpportunities(t, dataDir); len(events) != 0 {
		t.Fatalf("expected no opportunity events without an indexed graph, got %d", len(events))
	}
}

// TestOpportunity_OffSwitch verifies MNEME_CODEGRAPH_QUERYLOG=false suppresses
// telemetry while the nudge still fires.
func TestOpportunity_OffSwitch(t *testing.T) {
	dataDir, _ := setupNudgeDB(t, time.Now().UnixMilli())
	t.Setenv("MNEME_DATA_DIR", dataDir)
	t.Setenv("MNEME_CODEGRAPH_QUERYLOG", "false")

	cwd := mnemeRepoRoot(t)
	var stdout bytes.Buffer
	maybeEmitCodegraphNudge(buildNudgeInput("Read", "internal/x.go", "off", ""), cwd, &stdout, io.Discard)

	if !strings.Contains(stdout.String(), "codegraph-nudge") {
		t.Errorf("nudge should still fire with querylog off; stdout: %s", stdout.String())
	}
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
