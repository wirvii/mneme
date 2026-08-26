package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/store"
)

// runBacklogCmd executes "mneme backlog <argv...>" against an isolated
// --data-dir/--project so tests never touch the real ~/.mneme instance, and
// returns stdout/stderr separately.
//
// It also chdirs into an isolated, non-git temp directory for the duration
// of the call — same pattern as runSubagentsCmd (internal/cli/subagents_test.go)
// — because --data-dir alone does nothing to isolate service.DetectTeamMemory,
// which resolves relative to the REAL process cwd via `git rev-parse
// --show-toplevel` (SPEC-085).
//
// newBacklogAddCmd writes its "Created ..." line and refinement advisory via
// fmt.Fprintf(os.Stdout, ...) directly (matching every other backlog
// subcommand in this file), not cmd.OutOrStdout() — so capturing output
// requires redirecting the process-wide os.Stdout for the call's duration,
// rather than cobra's SetOut.
func runBacklogCmd(t *testing.T, dataDir, project string, argv ...string) (stdout, stderr string, err error) {
	t.Helper()

	isolatedCwd := t.TempDir()
	orig, wdErr := os.Getwd()
	if wdErr != nil {
		t.Fatalf("getwd: %v", wdErr)
	}
	if chErr := os.Chdir(isolatedCwd); chErr != nil {
		t.Fatalf("chdir into isolated cwd: %v", chErr)
	}
	t.Cleanup(func() {
		if restoreErr := os.Chdir(orig); restoreErr != nil {
			t.Fatalf("restore cwd: %v", restoreErr)
		}
	})

	r, w, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatalf("os.Pipe: %v", pipeErr)
	}
	origStdout := os.Stdout
	os.Stdout = w

	root := NewRootCmd()
	errBuf := new(bytes.Buffer)
	root.SetErr(errBuf)

	args := append([]string{"--data-dir", dataDir, "--project", project}, argv...)
	root.SetArgs(args)
	err = root.Execute()

	os.Stdout = origStdout
	if closeErr := w.Close(); closeErr != nil {
		t.Fatalf("close stdout pipe writer: %v", closeErr)
	}
	outBytes, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatalf("read stdout pipe: %v", readErr)
	}

	return string(outBytes), errBuf.String(), err
}

// TestBacklogAdd_PrintsAdvisoryOnStandardLane verifies CLI parity with the
// MCP envelope (SPEC-103 AC5): a standard-lane "mneme backlog add" prints the
// grill-me refinement advisory to stdout.
func TestBacklogAdd_PrintsAdvisoryOnStandardLane(t *testing.T) {
	dataDir := t.TempDir()

	stdout, stderr, err := runBacklogCmd(t, dataDir, "test-backlog-add-standard",
		"backlog", "add", "Standard-lane item", "--lane", "standard")
	if err != nil {
		t.Fatalf("backlog add: %v (stderr=%s)", err, stderr)
	}
	if !strings.Contains(stdout, "Created BL-") {
		t.Errorf("stdout missing creation line: %s", stdout)
	}
	if !strings.Contains(stdout, "grill-me") {
		t.Errorf("stdout missing refinement advisory (grill-me): %s", stdout)
	}
}

// TestBacklogAdd_NoAdvisoryOnTrivialLane verifies that a trivial-lane
// "mneme backlog add" prints no refinement advisory (SPEC-103 AC6).
func TestBacklogAdd_NoAdvisoryOnTrivialLane(t *testing.T) {
	dataDir := t.TempDir()

	stdout, stderr, err := runBacklogCmd(t, dataDir, "test-backlog-add-trivial",
		"backlog", "add", "Trivial-lane item", "--lane", "trivial", "--scope", "internal/model/*.go")
	if err != nil {
		t.Fatalf("backlog add: %v (stderr=%s)", err, stderr)
	}
	if !strings.Contains(stdout, "Created BL-") {
		t.Errorf("stdout missing creation line: %s", stdout)
	}
	if strings.Contains(stdout, "grill-me") {
		t.Errorf("stdout unexpectedly contains refinement advisory for a trivial-lane item: %s", stdout)
	}
}

// TestBacklogList_JSONIsBareArrayWithFullDescription is AC15: `mneme backlog
// list --json` must stay a bare JSON array (never the {items,total} MCP
// envelope) and must carry the FULL description — never the excerpt+truncated
// pair the MCP view uses (D9: the CLI is the only path left to full
// fidelity, so the service layer never truncates and printJSON is fed the
// bare item slice, not the envelope).
func TestBacklogList_JSONIsBareArrayWithFullDescription(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-backlog-list-json"

	longDescription := strings.Repeat("grill ledger content ", 500) // > 200 runes
	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "add", "Ledger item", "--lane", "standard", "--description", longDescription); err != nil {
		t.Fatalf("backlog add: %v (stderr=%s)", err, stderr)
	}

	stdout, stderr, err := runBacklogCmd(t, dataDir, project, "backlog", "list", "--json")
	if err != nil {
		t.Fatalf("backlog list --json: %v (stderr=%s)", err, stderr)
	}

	var items []map[string]any
	if err := json.Unmarshal([]byte(stdout), &items); err != nil {
		t.Fatalf("backlog list --json did not decode as a bare array: %v\nstdout=%s", err, stdout)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	item := items[0]
	if _, hasExcerpt := item["excerpt"]; hasExcerpt {
		t.Error("CLI --json output must not have an 'excerpt' field — that is the MCP view's field")
	}
	if _, hasTruncated := item["truncated"]; hasTruncated {
		t.Error("CLI --json output must not have a 'truncated' field — that is the MCP view's field")
	}
	gotDesc, _ := item["description"].(string)
	if gotDesc != longDescription {
		t.Errorf("description was not returned in full: got %d runes, want %d",
			len([]rune(gotDesc)), len([]rune(longDescription)))
	}
}

// TestBacklogList_TableOutputFormatUnchanged is AC15's plain-text half: the
// table format (`  %-8s  [%-8s]  %-40s  %s%s\n`) is unchanged, and rows are
// ordered by priority RANK (critical, high, medium, low — SPEC-109 D7/D20),
// not the priority column's lexicographic order.
func TestBacklogList_TableOutputFormatUnchanged(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-backlog-list-table"

	for _, tc := range []struct {
		title    string
		priority string
	}{
		{"Low item", "low"},
		{"Medium item", "medium"},
		{"Critical item", "critical"},
		{"High item", "high"},
	} {
		if _, stderr, err := runBacklogCmd(t, dataDir, project,
			"backlog", "add", tc.title, "--lane", "standard", "--priority", tc.priority); err != nil {
			t.Fatalf("backlog add %q: %v (stderr=%s)", tc.title, err, stderr)
		}
	}

	stdout, stderr, err := runBacklogCmd(t, dataDir, project, "backlog", "list")
	if err != nil {
		t.Fatalf("backlog list: %v (stderr=%s)", err, stderr)
	}

	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d: %q", len(lines), stdout)
	}

	wantTitles := []string{"Critical item", "High item", "Medium item", "Low item"}
	for i, want := range wantTitles {
		if !strings.Contains(lines[i], want) {
			t.Errorf("line %d = %q, want it to contain %q (priority-rank order)", i, lines[i], want)
		}
	}
	// The table format itself (column widths, brackets around status) is
	// untouched by this spec — only the ordering above is new behaviour.
	// Spot-check its shape without hardcoding exact padding.
	if !strings.Contains(lines[0], "[raw") || !strings.Contains(lines[0], "]") {
		t.Errorf("expected bracketed status column, got: %q", lines[0])
	}
}

// TestBacklogListCmd_ShowsRefinementCountOnlyWhenNonZero is AC24/AC26: the
// "refs:N" suffix appears only for items with at least one refinement, and
// is absent for items with zero.
func TestBacklogListCmd_ShowsRefinementCountOnlyWhenNonZero(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-backlog-list-refcount"

	addOut, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "add", "Refined item", "--lane", "standard")
	if err != nil {
		t.Fatalf("backlog add refined item: %v (stderr=%s)", err, stderr)
	}
	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "add", "Untouched item", "--lane", "standard"); err != nil {
		t.Fatalf("backlog add untouched item: %v (stderr=%s)", err, stderr)
	}

	if !strings.Contains(addOut, "Created BL-001") {
		t.Fatalf("expected first item to be BL-001, got stdout=%s", addOut)
	}
	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "refine", "BL-001", "--refinement", "r1"); err != nil {
		t.Fatalf("backlog refine BL-001: %v (stderr=%s)", err, stderr)
	}
	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "refine", "BL-001", "--refinement", "r2"); err != nil {
		t.Fatalf("backlog refine BL-001 (2nd): %v (stderr=%s)", err, stderr)
	}

	stdout, stderr, err := runBacklogCmd(t, dataDir, project, "backlog", "list")
	if err != nil {
		t.Fatalf("backlog list: %v (stderr=%s)", err, stderr)
	}

	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	var refinedLine, untouchedLine string
	for _, line := range lines {
		if strings.Contains(line, "BL-001") {
			refinedLine = line
		}
		if strings.Contains(line, "BL-002") {
			untouchedLine = line
		}
	}
	if refinedLine == "" || untouchedLine == "" {
		t.Fatalf("expected both BL-001 and BL-002 in output: %q", stdout)
	}
	if !strings.Contains(refinedLine, "refs:2") {
		t.Errorf("BL-001 line missing refs:2 suffix: %q", refinedLine)
	}
	if strings.Contains(untouchedLine, "refs:") {
		t.Errorf("BL-002 line unexpectedly contains a refs: suffix: %q", untouchedLine)
	}
}

// TestBacklogRefineCmd_SecondRefinementSucceeds is AC24: a second refinement
// on the same item succeeds (no longer rejected), reports the refinement
// number, and keeps the item in refined status.
func TestBacklogRefineCmd_SecondRefinementSucceeds(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-backlog-refine-second"

	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "add", "X", "--lane", "standard"); err != nil {
		t.Fatalf("backlog add: %v (stderr=%s)", err, stderr)
	}

	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "refine", "BL-001", "--refinement", "r1"); err != nil {
		t.Fatalf("first refine: %v (stderr=%s)", err, stderr)
	}

	stdout, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "refine", "BL-001", "--refinement", "r2", "--by", "architect")
	if err != nil {
		t.Fatalf("second refine: %v (stderr=%s)", err, stderr)
	}
	if !strings.Contains(stdout, "refinement #2") {
		t.Errorf("expected stdout to report refinement #2, got %q", stdout)
	}
	if !strings.Contains(stdout, "refined") {
		t.Errorf("expected stdout to report status refined, got %q", stdout)
	}
}

// TestBacklogGetCmd_PrintsAllRefinements is AC25: "mneme backlog get" prints
// the item header plus every refinement body in full.
func TestBacklogGetCmd_PrintsAllRefinements(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-backlog-get-text"

	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "add", "X", "--lane", "standard"); err != nil {
		t.Fatalf("backlog add: %v (stderr=%s)", err, stderr)
	}
	for _, body := range []string{"first refinement", "second refinement"} {
		if _, stderr, err := runBacklogCmd(t, dataDir, project,
			"backlog", "refine", "BL-001", "--refinement", body); err != nil {
			t.Fatalf("refine %q: %v (stderr=%s)", body, err, stderr)
		}
	}

	stdout, stderr, err := runBacklogCmd(t, dataDir, project, "backlog", "get", "BL-001")
	if err != nil {
		t.Fatalf("backlog get: %v (stderr=%s)", err, stderr)
	}
	if !strings.Contains(stdout, "BL-001") {
		t.Errorf("expected item header in output: %q", stdout)
	}
	if !strings.Contains(stdout, "first refinement") || !strings.Contains(stdout, "second refinement") {
		t.Errorf("expected both refinement bodies in full, got %q", stdout)
	}
	if !strings.Contains(stdout, "#1") || !strings.Contains(stdout, "#2") {
		t.Errorf("expected refinement sequence numbers #1 and #2, got %q", stdout)
	}
}

// TestBacklogGetCmd_JSONIsEnvelope is AC25: "mneme backlog get --json" prints
// the full {item, refinements} envelope.
func TestBacklogGetCmd_JSONIsEnvelope(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-backlog-get-json"

	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "add", "X", "--lane", "standard"); err != nil {
		t.Fatalf("backlog add: %v (stderr=%s)", err, stderr)
	}
	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "refine", "BL-001", "--refinement", "r1", "--by", "backend"); err != nil {
		t.Fatalf("refine: %v (stderr=%s)", err, stderr)
	}

	stdout, stderr, err := runBacklogCmd(t, dataDir, project, "backlog", "get", "BL-001", "--json")
	if err != nil {
		t.Fatalf("backlog get --json: %v (stderr=%s)", err, stderr)
	}

	var envelope struct {
		Item struct {
			ID              string `json:"id"`
			RefinementCount int    `json:"refinement_count"`
		} `json:"item"`
		Refinements []struct {
			Seq  int    `json:"seq"`
			Body string `json:"body"`
			By   string `json:"by"`
		} `json:"refinements"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("backlog get --json did not decode as the envelope: %v\nstdout=%s", err, stdout)
	}
	if envelope.Item.ID != "BL-001" {
		t.Errorf("Item.ID = %q, want BL-001", envelope.Item.ID)
	}
	if envelope.Item.RefinementCount != 1 {
		t.Errorf("Item.RefinementCount = %d, want 1", envelope.Item.RefinementCount)
	}
	if len(envelope.Refinements) != 1 {
		t.Fatalf("expected 1 refinement, got %d", len(envelope.Refinements))
	}
	if envelope.Refinements[0].Body != "r1" || envelope.Refinements[0].By != "backend" {
		t.Errorf("refinement = %+v, want body=r1 by=backend", envelope.Refinements[0])
	}
}

// TestBacklogArchiveCmd_RequiresReasonWithoutTouchingTheStore is SPEC-125
// AC3: the CLI keeps its own --reason precondition, unchanged in behaviour
// and message, so a caller without a reason never opens the database.
func TestBacklogArchiveCmd_RequiresReasonWithoutTouchingTheStore(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-backlog-archive-no-reason"

	stdout, _, err := runBacklogCmd(t, dataDir, project, "backlog", "archive", "BL-001")
	if err == nil {
		t.Fatalf("expected an error when --reason is omitted, got none (stdout=%s)", stdout)
	}
	if !strings.Contains(err.Error(), "--reason is required") {
		t.Errorf("expected the existing --reason message, got %q", err.Error())
	}

	entries, readErr := os.ReadDir(dataDir)
	if readErr != nil {
		t.Fatalf("read data dir: %v", readErr)
	}
	if len(entries) != 0 {
		t.Errorf("expected the data dir to stay empty (no DB opened), found %v", entries)
	}
}

// TestBacklogArchiveCmd_PrintsFreezeMessageWhenSpecIsAlive is SPEC-125 AC33:
// the first line stays byte-identical to the pre-SPEC-125 output, and — only
// when the archived item froze a live spec — the CLI additionally names the
// spec, its status, that the freeze cannot be undone, and the agreed way
// back (a new backlog item referencing the archived one).
func TestBacklogArchiveCmd_PrintsFreezeMessageWhenSpecIsAlive(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-backlog-archive-freeze"

	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "add", "Frozen work", "--lane", "standard"); err != nil {
		t.Fatalf("backlog add: %v (stderr=%s)", err, stderr)
	}
	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "refine", "BL-001", "--refinement", "r1"); err != nil {
		t.Fatalf("refine: %v (stderr=%s)", err, stderr)
	}
	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "promote", "BL-001"); err != nil {
		t.Fatalf("promote: %v (stderr=%s)", err, stderr)
	}
	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"spec", "advance", "SPEC-001", "--by", "orchestrator"); err != nil {
		t.Fatalf("spec advance: %v (stderr=%s)", err, stderr)
	}

	stdout, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "archive", "BL-001", "--reason", "abandoned mid-flight")
	if err != nil {
		t.Fatalf("backlog archive: %v (stderr=%s)", err, stderr)
	}

	lines := strings.SplitN(stdout, "\n", 2)
	if lines[0] != "Archived BL-001: abandoned mid-flight" {
		t.Errorf("first line = %q, want the byte-identical pre-SPEC-125 line", lines[0])
	}
	if !strings.Contains(stdout, "SPEC-001") {
		t.Errorf("expected the frozen spec's ID in the output, got %q", stdout)
	}
	if !strings.Contains(stdout, "speccing") {
		t.Errorf("expected the frozen spec's status in the output, got %q", stdout)
	}
	if !strings.Contains(stdout, "cannot be undone") {
		t.Errorf("expected the irreversibility warning in the output, got %q", stdout)
	}
	if !strings.Contains(stdout, "new backlog item") || !strings.Contains(stdout, "BL-001") {
		t.Errorf("expected the agreed way back (new item referencing BL-001) in the output, got %q", stdout)
	}
}

// TestBacklogGetCmd_ArchiveReasonLine is SPEC-126 AC1/AC2: "backlog get"
// prints "archived: <reason>" verbatim right after the header, and ONLY for
// an archived item — a non-archived item's output is untouched.
func TestBacklogGetCmd_ArchiveReasonLine(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-backlog-get-archive-reason"

	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "add", "Archived item", "--lane", "standard"); err != nil {
		t.Fatalf("backlog add: %v (stderr=%s)", err, stderr)
	}
	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "add", "Live item", "--lane", "standard"); err != nil {
		t.Fatalf("backlog add (live): %v (stderr=%s)", err, stderr)
	}

	const reason = "Superseded by BL-207"
	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "archive", "BL-001", "--reason", reason); err != nil {
		t.Fatalf("backlog archive: %v (stderr=%s)", err, stderr)
	}

	archivedOut, stderr, err := runBacklogCmd(t, dataDir, project, "backlog", "get", "BL-001")
	if err != nil {
		t.Fatalf("backlog get BL-001: %v (stderr=%s)", err, stderr)
	}
	if !strings.Contains(archivedOut, "archived: "+reason) {
		t.Errorf("expected the archive reason line, got %q", archivedOut)
	}

	liveOut, stderr, err := runBacklogCmd(t, dataDir, project, "backlog", "get", "BL-002")
	if err != nil {
		t.Fatalf("backlog get BL-002: %v (stderr=%s)", err, stderr)
	}
	if strings.Contains(liveOut, "archived:") {
		t.Errorf("expected NO archive reason line for a non-archived item, got %q", liveOut)
	}
}

// TestBacklogListCmd_ArchiveReasonSuffix is SPEC-126 AC3: "backlog list"
// prints the archive reason as a suffix on the SAME row — no extra line, one
// line per item still, and non-archived rows unaffected.
// TestBacklogList_TableOutputFormatUnchanged already pins the non-archived
// case; this test adds the archived one.
func TestBacklogListCmd_ArchiveReasonSuffix(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-backlog-list-archive-reason"

	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "add", "Archived item", "--lane", "standard"); err != nil {
		t.Fatalf("backlog add: %v (stderr=%s)", err, stderr)
	}
	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "add", "Live item", "--lane", "standard"); err != nil {
		t.Fatalf("backlog add (live): %v (stderr=%s)", err, stderr)
	}

	const reason = "Superseded by BL-207"
	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "archive", "BL-001", "--reason", reason); err != nil {
		t.Fatalf("backlog archive: %v (stderr=%s)", err, stderr)
	}

	stdout, stderr, err := runBacklogCmd(t, dataDir, project, "backlog", "list")
	if err != nil {
		t.Fatalf("backlog list: %v (stderr=%s)", err, stderr)
	}

	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (one per item), got %d: %q", len(lines), stdout)
	}

	var archivedLine, liveLine string
	for _, line := range lines {
		if strings.Contains(line, "BL-001") {
			archivedLine = line
		}
		if strings.Contains(line, "BL-002") {
			liveLine = line
		}
	}
	if !strings.Contains(archivedLine, "archived: "+reason) {
		t.Errorf("expected the archive reason suffix on BL-001's row, got %q", archivedLine)
	}
	if strings.Contains(liveLine, "archived:") {
		t.Errorf("expected NO archive reason suffix on the live item's row, got %q", liveLine)
	}
}

// TestBacklogArchive_EmptyReason_PlaceholderInBothCommands is SPEC-126 AC4:
// an archived item whose archive_reason is EMPTY prints the placeholder
// "(no reason recorded)" in both "backlog get" and "backlog list" — never a
// bare "archived:" with nothing after it. The empty reason is inserted via
// the STORE directly (bypassing the service, which has required a non-empty
// reason since SPEC-125 D1) since that is the only way to reach this state.
func TestBacklogArchive_EmptyReason_PlaceholderInBothCommands(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-backlog-archive-empty-reason"

	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "add", "Legacy archived item", "--lane", "standard"); err != nil {
		t.Fatalf("backlog add: %v (stderr=%s)", err, stderr)
	}
	// A real reason first (the service requires one), then cleared directly
	// via the store — mirroring an item archived before SPEC-125 made the
	// reason mandatory.
	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "archive", "BL-001", "--reason", "temporary, cleared below"); err != nil {
		t.Fatalf("backlog archive: %v (stderr=%s)", err, stderr)
	}

	dbPath := filepath.Join(dataDir, "projects", project+".db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	sddStore := store.NewSDDStore(database)
	ctx := context.Background()
	item, err := sddStore.GetBacklogItem(ctx, "BL-001")
	if err != nil {
		t.Fatalf("GetBacklogItem: %v", err)
	}
	item.ArchiveReason = ""
	if err := sddStore.UpdateBacklogItem(ctx, item); err != nil {
		t.Fatalf("UpdateBacklogItem: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	getOut, stderr, err := runBacklogCmd(t, dataDir, project, "backlog", "get", "BL-001")
	if err != nil {
		t.Fatalf("backlog get: %v (stderr=%s)", err, stderr)
	}
	if !strings.Contains(getOut, "archived: (no reason recorded)") {
		t.Errorf("backlog get: expected the placeholder, got %q", getOut)
	}

	listOut, stderr, err := runBacklogCmd(t, dataDir, project, "backlog", "list")
	if err != nil {
		t.Fatalf("backlog list: %v (stderr=%s)", err, stderr)
	}
	if !strings.Contains(listOut, "archived: (no reason recorded)") {
		t.Errorf("backlog list: expected the placeholder, got %q", listOut)
	}
}

// TestBacklogGetAndList_JSONKeysUnchanged is SPEC-126 AC5: neither
// "backlog get --json" nor "backlog list --json" gains or loses a key —
// the archive reason already travelled through the JSON encoding of
// model.BacklogItem.ArchiveReason before this spec; the fix is exclusively
// to the readable output.
func TestBacklogGetAndList_JSONKeysUnchanged(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-backlog-json-keys-unchanged"

	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "add", "Archived item", "--lane", "standard"); err != nil {
		t.Fatalf("backlog add: %v (stderr=%s)", err, stderr)
	}
	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "archive", "BL-001", "--reason", "for the JSON key check"); err != nil {
		t.Fatalf("backlog archive: %v (stderr=%s)", err, stderr)
	}

	listOut, stderr, err := runBacklogCmd(t, dataDir, project, "backlog", "list", "--json")
	if err != nil {
		t.Fatalf("backlog list --json: %v (stderr=%s)", err, stderr)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(listOut), &items); err != nil {
		t.Fatalf("decode backlog list --json: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	wantKeys := []string{
		"id", "title", "status", "priority", "project", "archive_reason",
		"position", "lane", "refinement_count", "created_at", "updated_at",
	}
	for _, k := range wantKeys {
		if _, ok := items[0][k]; !ok {
			t.Errorf("backlog list --json: missing expected key %q in %v", k, items[0])
		}
	}
	if _, ok := items[0]["frozen"]; ok {
		t.Error("backlog list --json: unexpected 'frozen' key — this spec adds no field to BacklogItem")
	}
	// Exact count, not just presence/absence (mirrors
	// TestFreezeJSON_AdditiveContract in internal/model/sdd_test.go): a key
	// added for an unrelated reason in the future — one this loop never
	// thought to check for by name — must still turn this red instead of
	// passing silently.
	if len(items[0]) != len(wantKeys) {
		t.Errorf("backlog list --json: key count: got %d (%v), want %d (%v)",
			len(items[0]), keysOfAny(items[0]), len(wantKeys), wantKeys)
	}

	getOut, stderr, err := runBacklogCmd(t, dataDir, project, "backlog", "get", "BL-001", "--json")
	if err != nil {
		t.Fatalf("backlog get --json: %v (stderr=%s)", err, stderr)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(getOut), &envelope); err != nil {
		t.Fatalf("decode backlog get --json: %v", err)
	}
	item, ok := envelope["item"].(map[string]any)
	if !ok {
		t.Fatalf("expected an 'item' object, got %#v", envelope["item"])
	}
	for _, k := range wantKeys {
		if _, ok := item[k]; !ok {
			t.Errorf("backlog get --json: missing expected key %q in %v", k, item)
		}
	}
	if len(item) != len(wantKeys) {
		t.Errorf("backlog get --json: key count: got %d (%v), want %d (%v)",
			len(item), keysOfAny(item), len(wantKeys), wantKeys)
	}
}

// keysOfAny returns the keys of a decoded JSON object (map[string]any), for
// readable failure messages in TestBacklogGetAndList_JSONKeysUnchanged.
func keysOfAny(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
