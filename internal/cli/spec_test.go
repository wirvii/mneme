package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/model"
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

// TestSpecList_MarksFrozenSpecs is SPEC-126 AC16: a frozen row gets a
// "— frozen" suffix, a spec whose linked backlog item is missing gets
// "— frozen (link missing)" instead, and the summary note prints exactly
// once, saying what each mark means and pointing to a concrete command.
func TestSpecList_MarksFrozenSpecs(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-spec-list-frozen"

	// SPEC-001: linked to an item that gets archived.
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
	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "archive", "BL-001", "--reason", "abandoned"); err != nil {
		t.Fatalf("archive: %v (stderr=%s)", err, stderr)
	}

	// SPEC-002: a BacklogID that never existed.
	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"spec", "new", "Dangling link spec", "--lane", "standard", "--from-backlog", "BL-999"); err != nil {
		t.Fatalf("spec new (dangling): %v (stderr=%s)", err, stderr)
	}

	stdout, stderr, err := runBacklogCmd(t, dataDir, project, "spec", "list")
	if err != nil {
		t.Fatalf("spec list: %v (stderr=%s)", err, stderr)
	}

	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	var archivedLine, danglingLine string
	for _, line := range lines {
		// Match the TABLE ROW prefix specifically ("  SPEC-001    ["), not
		// any line mentioning the ID — the summary note below also names
		// SPEC-001 in its "Run \"mneme spec status SPEC-001\"" sentence.
		if strings.HasPrefix(line, "  SPEC-001 ") {
			archivedLine = line
		}
		if strings.HasPrefix(line, "  SPEC-002 ") {
			danglingLine = line
		}
	}
	if !strings.Contains(archivedLine, "— frozen") || strings.Contains(archivedLine, "link missing") {
		t.Errorf("SPEC-001 row: got %q, want a bare '— frozen' suffix", archivedLine)
	}
	if !strings.Contains(danglingLine, "— frozen (link missing)") {
		t.Errorf("SPEC-002 row: got %q, want '— frozen (link missing)'", danglingLine)
	}

	if !strings.Contains(stdout, "2 specs are marked") {
		t.Errorf("expected the summary note to say 2 specs are marked, got %q", stdout)
	}
	if !strings.Contains(stdout, "still be read") {
		t.Errorf("expected the note to say the spec can still be read, got %q", stdout)
	}
	if !strings.Contains(stdout, "can no longer change") {
		t.Errorf("expected the note to say the status can no longer change, got %q", stdout)
	}
	if !strings.Contains(stdout, "mneme spec status SPEC-001") {
		t.Errorf("expected the note to point to the first marked spec with a runnable command, got %q", stdout)
	}
	if n := strings.Count(stdout, "specs are marked"); n != 1 {
		t.Errorf("expected the note exactly once, found %d occurrences in %q", n, stdout)
	}
}

// TestSpecStatus_FrozenBlock_ArchivedItem is SPEC-126 AC17: the Frozen:
// block names the archived item, its reason, the irreversibility, and the
// agreed way back — and appears before the timeline, never with a date.
func TestSpecStatus_FrozenBlock_ArchivedItem(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-spec-status-frozen-archived"

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
	// Advance once so the timeline is non-empty — needed to check the
	// Frozen: block's position relative to it.
	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"spec", "advance", "SPEC-001", "--by", "orchestrator"); err != nil {
		t.Fatalf("spec advance: %v (stderr=%s)", err, stderr)
	}
	const reason = "Superseded by BL-207"
	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "archive", "BL-001", "--reason", reason); err != nil {
		t.Fatalf("archive: %v (stderr=%s)", err, stderr)
	}

	stdout, stderr, err := runBacklogCmd(t, dataDir, project, "spec", "status", "SPEC-001")
	if err != nil {
		t.Fatalf("spec status: %v (stderr=%s)", err, stderr)
	}

	if !strings.Contains(stdout, "Frozen: backlog item BL-001 was archived") {
		t.Errorf("expected the Frozen: header line naming BL-001, got %q", stdout)
	}
	if !strings.Contains(stdout, reason) {
		t.Errorf("expected the reason %q in the output, got %q", reason, stdout)
	}
	if !strings.Contains(stdout, "can never change again") || !strings.Contains(stdout, "cannot be undone") {
		t.Errorf("expected the irreversibility warning, got %q", stdout)
	}
	if !strings.Contains(stdout, "create a new backlog item") || !strings.Contains(stdout, "BL-001") {
		t.Errorf("expected the agreed way back naming BL-001, got %q", stdout)
	}
	if strings.Contains(stdout, "updated_at") {
		t.Errorf("must never print updated_at as if it were an archive date, got %q", stdout)
	}

	frozenIdx := strings.Index(stdout, "Frozen:")
	timelineIdx := strings.Index(stdout, "Timeline:")
	if frozenIdx == -1 || timelineIdx == -1 || frozenIdx > timelineIdx {
		t.Errorf("expected Frozen: before Timeline:, got frozenIdx=%d timelineIdx=%d in %q", frozenIdx, timelineIdx, stdout)
	}
}

// TestSpecStatus_FrozenBlock_MissingItem is SPEC-126 AC18: the "missing"
// state names the absent item, never claims it was "archived" (it was never
// actually read), and says every future status change will fail.
func TestSpecStatus_FrozenBlock_MissingItem(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-spec-status-frozen-missing"

	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"spec", "new", "Dangling link spec", "--lane", "standard", "--from-backlog", "BL-999"); err != nil {
		t.Fatalf("spec new: %v (stderr=%s)", err, stderr)
	}

	stdout, stderr, err := runBacklogCmd(t, dataDir, project, "spec", "status", "SPEC-001")
	if err != nil {
		t.Fatalf("spec status: %v (stderr=%s)", err, stderr)
	}

	if !strings.Contains(stdout, "Frozen: backlog item BL-999 is not in this database") {
		t.Errorf("expected the Frozen: header naming BL-999 as missing, got %q", stdout)
	}
	if strings.Contains(stdout, "was archived") {
		t.Errorf("must not claim the missing item was archived, got %q", stdout)
	}
	if !strings.Contains(stdout, "will fail") {
		t.Errorf("expected a statement that status changes will fail, got %q", stdout)
	}
}

// TestSpecStatus_LiveSpec_NoFrozenBlock is AC17's negative half: a live
// spec's "spec status" output has no Frozen: block at all.
func TestSpecStatus_LiveSpec_NoFrozenBlock(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-spec-status-live"

	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"spec", "new", "Live spec", "--lane", "standard"); err != nil {
		t.Fatalf("spec new: %v (stderr=%s)", err, stderr)
	}

	stdout, stderr, err := runBacklogCmd(t, dataDir, project, "spec", "status", "SPEC-001")
	if err != nil {
		t.Fatalf("spec status: %v (stderr=%s)", err, stderr)
	}
	if strings.Contains(stdout, "Frozen:") {
		t.Errorf("expected no Frozen: block for a live spec, got %q", stdout)
	}
}

// TestSpecReject_FindingFlag_Repeatable is SPEC-156 AC9/P5: two --finding
// flags must produce TWO findings, not one — the exact defect SPEC-132 V8
// documented for a StringVar used where StringArrayVar was required (a
// repeated flag silently keeps only the last value). Exercised against a
// trivial-lane spec rejected from audit (D5 exclusion 1: the guardian never
// validates the findings' content there), so this test isolates the CLI
// flag-collection behaviour from the criteria.toml plumbing P3 already
// covers at the service layer.
func TestSpecReject_FindingFlag_Repeatable(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-spec-reject-finding-repeatable"

	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"spec", "new", "Trivial reject", "--lane", "trivial", "--scope", "internal/**/*.go"); err != nil {
		t.Fatalf("spec new: %v (stderr=%s)", err, stderr)
	}
	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"spec", "quick", "SPEC-001", "one-liner", "--by", "orchestrator"); err != nil {
		t.Fatalf("spec quick: %v (stderr=%s)", err, stderr)
	}
	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"spec", "advance", "SPEC-001", "--by", "backend"); err != nil { // implementing -> audit
		t.Fatalf("spec advance (implementing->audit): %v (stderr=%s)", err, stderr)
	}

	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"spec", "reject", "SPEC-001",
		"--reason", "manual audit found issues", "--by", "orchestrator",
		"--finding", "AC1=first issue", "--finding", "AC2=second issue"); err != nil {
		t.Fatalf("spec reject: %v (stderr=%s)", err, stderr)
	}

	stdout, stderr, err := runBacklogCmd(t, dataDir, project, "spec", "history", "SPEC-001", "--json")
	if err != nil {
		t.Fatalf("spec history --json: %v (stderr=%s)", err, stderr)
	}
	var history []model.SpecHistory
	if err := json.Unmarshal([]byte(stdout), &history); err != nil {
		t.Fatalf("unmarshal spec history: %v\nstdout=%s", err, stdout)
	}
	last := history[len(history)-1]
	if !strings.Contains(last.Reason, "[AC1] first issue") {
		t.Errorf("persisted reason missing the first --finding: %q", last.Reason)
	}
	if !strings.Contains(last.Reason, "[AC2] second issue") {
		t.Errorf("persisted reason missing the second --finding: %q — a StringVar bug would silently drop it", last.Reason)
	}
}

// TestSpecReject_FindingFlag_MalformedValue_Fails covers a --finding value
// with no "=" separator: the command must fail immediately, naming the
// offending value, before ever reaching the service layer (no real spec is
// needed for this assertion to hold).
func TestSpecReject_FindingFlag_MalformedValue_Fails(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-spec-reject-finding-malformed"

	_, stderr, err := runBacklogCmd(t, dataDir, project,
		"spec", "reject", "SPEC-999",
		"--reason", "r", "--by", "qa-agent", "--finding", "no-equals-sign")
	if err == nil {
		t.Fatal("expected an error for a --finding value with no '=' separator, got nil")
	}
	if !strings.Contains(err.Error(), "no-equals-sign") && !strings.Contains(stderr, "no-equals-sign") {
		t.Errorf("expected the offending value named in the error, got err=%v stderr=%q", err, stderr)
	}
}
