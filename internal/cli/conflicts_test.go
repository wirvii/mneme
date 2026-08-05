package cli

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// savedIDPattern extracts the memory ID `mneme save`'s confirmation line
// prints: "Saved: <id> (<action>) — <title>".
var savedIDPattern = regexp.MustCompile(`^Saved: (\S+) `)

// saveMemoryViaCLI saves a memory with no --project override. Every helper
// in this file deliberately omits --project (leaving it at its "" default,
// auto-detect) and relies on every command resolving the SAME default
// consistently — see the discovery below for why an explicit --project
// cannot be used here.
func saveMemoryViaCLI(t *testing.T, dataDir, title, content string) string {
	t.Helper()
	stdout, stderr, err := runBacklogCmd(t, dataDir, "",
		"save", "--title", title, "--content", content, "--type", "decision")
	if err != nil {
		t.Fatalf("save %q: %v (stderr=%s)", title, err, stderr)
	}
	m := savedIDPattern.FindStringSubmatch(stdout)
	if m == nil {
		t.Fatalf("could not extract ID from save output: %q", stdout)
	}
	return m[1]
}

// TestConflictsList_JSONIsBareArray is AC15's conflicts_list half: `mneme
// conflicts list --json` must stay a bare JSON array (never the
// {relations,total} MCP envelope) — enc.Encode(resp.Relations) is fed the
// bare slice, not the envelope (SPEC-109 D9/D16).
//
// Deliberately does not pass --project to any subcommand (see the discovery
// note on saveMemoryViaCLI): newConflictsListCmd/newConflictsScanCmd
// (pre-existing, unrelated to this spec) each redeclare a LOCAL `flagProject`
// that shadows the package-level variable initService's DB-selection reads,
// so an explicit --project on "conflicts list"/"conflicts scan" is silently
// applied to the query filter but NOT to which database file gets opened —
// the command ends up querying the wrong (global) database. Every command
// here relies on the SAME "" default instead, which sidesteps the bug
// without masking or fixing it (out of SPEC-109's scope; flagged as a
// discovery/candidate BL).
func TestConflictsList_JSONIsBareArray(t *testing.T) {
	dataDir := t.TempDir()

	aID := saveMemoryViaCLI(t, dataDir, "HMAC auth", "Use HMAC-SHA256 for tokens")
	bID := saveMemoryViaCLI(t, dataDir, "RS256 auth", "Use RS256 JWT for tokens")

	if _, stderr, err := runBacklogCmd(t, dataDir, "",
		"conflicts", "link", aID, bID, "conflicts_with"); err != nil {
		t.Fatalf("conflicts link: %v (stderr=%s)", err, stderr)
	}

	stdout, stderr, err := runBacklogCmd(t, dataDir, "", "conflicts", "list", "--json")
	if err != nil {
		t.Fatalf("conflicts list --json: %v (stderr=%s)", err, stderr)
	}

	var rels []map[string]any
	if err := json.Unmarshal([]byte(stdout), &rels); err != nil {
		t.Fatalf("conflicts list --json did not decode as a bare array: %v\nstdout=%s", err, stdout)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(rels))
	}
	if _, hasTotal := rels[0]["total"]; hasTotal {
		t.Error("individual relation object must not have a 'total' field — that belongs to the envelope")
	}
}

// TestConflictsList_TableOutputUnchanged is AC15's plain-text half: the
// table format is untouched by this spec.
func TestConflictsList_TableOutputUnchanged(t *testing.T) {
	dataDir := t.TempDir()

	aID := saveMemoryViaCLI(t, dataDir, "Table format A", "content a")
	bID := saveMemoryViaCLI(t, dataDir, "Table format B", "content b")

	if _, stderr, err := runBacklogCmd(t, dataDir, "",
		"conflicts", "link", aID, bID, "conflicts_with"); err != nil {
		t.Fatalf("conflicts link: %v (stderr=%s)", err, stderr)
	}

	stdout, stderr, err := runBacklogCmd(t, dataDir, "", "conflicts", "list")
	if err != nil {
		t.Fatalf("conflicts list: %v (stderr=%s)", err, stderr)
	}

	if !strings.Contains(stdout, "relation") || !strings.Contains(stdout, "from_id") {
		t.Errorf("unexpected table header: %q", stdout)
	}
	if !strings.Contains(stdout, "conflicts_with") {
		t.Errorf("expected conflicts_with row, got: %q", stdout)
	}
}
