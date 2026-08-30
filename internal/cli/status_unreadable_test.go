// Package cli — SPEC-133 step 3's own CLI-level verification for AC6, AC7,
// and AC8: the basic (non-discriminating) half of each. The discriminating
// half — byte-for-byte comparison of a healthy database's output against
// the base branch, and the exact total/name checks of AC10/AC13 — is
// step 5's own job (spec.md §4), written by someone who already has the
// real behaviour in front of them rather than a prediction.
package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/model"
)

// insertRawUnreadableBacklogItemCLI inserts a backlog_items row directly
// via SQL with an unparseable created_at, bypassing every mneme write
// path — the CLI-level sibling of internal/store's own
// insertRawUnreadableBacklogItem, needed here because these tests exercise
// real cobra commands against a real on-disk database (dataDir), not an
// in-memory store handle.
func insertRawUnreadableBacklogItemCLI(t *testing.T, dataDir, project, id string) {
	t.Helper()
	dbPath := filepath.Join(dataDir, "projects", project+".db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("close db: %v", closeErr)
		}
	}()
	if _, err := database.ExecContext(context.Background(),
		`INSERT INTO backlog_items (id, title, description, status, priority, project, position, lane, scope, uuid, previous_ids, created_at, updated_at)
		 VALUES (?, ?, '', ?, ?, ?, 0, ?, '', '', '', ?, ?)`,
		id, "pre-existing malformed row", string(model.BacklogStatusRaw), string(model.PriorityMedium),
		project, string(model.LaneStandard), "not-a-timestamp", "not-a-timestamp",
	); err != nil {
		t.Fatalf("insert malformed fixture row %s: %v", id, err)
	}
}

// TestStatus_AnnouncesUnreadableRowInsteadOfVanishingSection is SPEC-133
// AC6's basic half: with one legible and one illegible backlog item, and a
// spec in progress, "mneme status" exits 0, still shows the BACKLOG
// section with the legible item, and names the illegible one exactly once.
func TestStatus_AnnouncesUnreadableRowInsteadOfVanishingSection(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-status-unreadable"

	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "add", "healthy item", "--lane", "standard"); err != nil {
		t.Fatalf("backlog add: %v (stderr=%s)", err, stderr)
	}
	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"spec", "new", "healthy spec", "--lane", "standard"); err != nil {
		t.Fatalf("spec new: %v (stderr=%s)", err, stderr)
	}
	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"spec", "advance", "SPEC-001", "--by", "orchestrator"); err != nil {
		t.Fatalf("spec advance: %v (stderr=%s)", err, stderr)
	}

	insertRawUnreadableBacklogItemCLI(t, dataDir, project, "BL-901")

	stdout, stderr, err := runBacklogCmd(t, dataDir, project, "status")
	if err != nil {
		t.Fatalf("status: %v (stderr=%s)", err, stderr)
	}
	if !strings.Contains(stdout, "BACKLOG") {
		t.Errorf("stdout is missing the BACKLOG section entirely — the exact defect SPEC-133 fixes: %s", stdout)
	}
	if !strings.Contains(stdout, "BL-001") {
		t.Errorf("stdout is missing the legible item BL-001: %s", stdout)
	}
	if !strings.Contains(stdout, "SPECS IN PROGRESS") || !strings.Contains(stdout, "SPEC-001") {
		t.Errorf("stdout is missing the healthy in-progress spec: %s", stdout)
	}
	count := strings.Count(stdout, "Row BL-901 (backlog) could not be fully read")
	if count != 1 {
		t.Errorf("expected the announcement naming BL-901 exactly once, got %d occurrence(s): %s", count, stdout)
	}
}

// TestStatus_JSONUnreadableKeyPresentOnlyWhenSomethingIsUnreadable is
// SPEC-133 AC7's basic half: the "unreadable" key appears — naming BL-901 —
// only when there is something to declare, and existing keys keep their
// established shape.
func TestStatus_JSONUnreadableKeyPresentOnlyWhenSomethingIsUnreadable(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-status-unreadable-json"

	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "add", "healthy item", "--lane", "standard"); err != nil {
		t.Fatalf("backlog add: %v (stderr=%s)", err, stderr)
	}

	healthyOut, stderr, err := runBacklogCmd(t, dataDir, project, "status", "--json")
	if err != nil {
		t.Fatalf("status --json (healthy): %v (stderr=%s)", err, stderr)
	}
	var healthy map[string]any
	if err := json.Unmarshal([]byte(healthyOut), &healthy); err != nil {
		t.Fatalf("status --json (healthy) did not decode: %v\nstdout=%s", err, healthyOut)
	}
	if _, present := healthy["unreadable"]; present {
		t.Errorf("status --json on a healthy database must not carry an 'unreadable' key: %s", healthyOut)
	}

	insertRawUnreadableBacklogItemCLI(t, dataDir, project, "BL-901")

	stdout, stderr, err := runBacklogCmd(t, dataDir, project, "status", "--json")
	if err != nil {
		t.Fatalf("status --json: %v (stderr=%s)", err, stderr)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("status --json did not decode: %v\nstdout=%s", err, stdout)
	}
	unreadable, ok := out["unreadable"].([]any)
	if !ok {
		t.Fatalf("status --json 'unreadable' field is not a bare array: %T (stdout=%s)", out["unreadable"], stdout)
	}
	if len(unreadable) != 1 {
		t.Fatalf("expected exactly one unreadable row, got %d: %s", len(unreadable), stdout)
	}
	row, ok := unreadable[0].(map[string]any)
	if !ok {
		t.Fatalf("unreadable[0] is not an object: %T", unreadable[0])
	}
	if row["id"] != "BL-901" {
		t.Errorf("unreadable[0].id = %v, want BL-901", row["id"])
	}
}

// TestBacklogList_AnnouncesUnreadableRowOnStderrWithoutChangingStdout is
// SPEC-133 AC8's basic half for "mneme backlog list": stdout (--json form)
// stays identical to the healthy-only run, and the aviso lands on stderr
// exactly once, naming BL-901 — with an empty stderr on the healthy run.
func TestBacklogList_AnnouncesUnreadableRowOnStderrWithoutChangingStdout(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-backloglist-unreadable"

	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "add", "healthy item", "--lane", "standard"); err != nil {
		t.Fatalf("backlog add: %v (stderr=%s)", err, stderr)
	}

	healthyStdout, healthyStderr, err := runBacklogCmd(t, dataDir, project, "backlog", "list", "--json")
	if err != nil {
		t.Fatalf("backlog list --json (healthy): %v (stderr=%s)", err, healthyStderr)
	}
	if healthyStderr != "" {
		t.Errorf("backlog list --json (healthy) stderr must be empty, got: %q", healthyStderr)
	}

	insertRawUnreadableBacklogItemCLI(t, dataDir, project, "BL-901")

	stdout, stderr, err := runBacklogCmd(t, dataDir, project, "backlog", "list", "--json")
	if err != nil {
		t.Fatalf("backlog list --json: %v (stderr=%s)", err, stderr)
	}
	if stdout != healthyStdout {
		t.Errorf("backlog list --json stdout changed after an unrelated row became unreadable:\ngot:  %q\nwant: %q", stdout, healthyStdout)
	}
	count := strings.Count(stderr, "Row BL-901 (backlog) could not be fully read")
	if count != 1 {
		t.Errorf("expected the stderr announcement naming BL-901 exactly once, got %d: %q", count, stderr)
	}
}

// TestSpecList_AnnouncesUnreadableRowOnStderrWithoutChangingStdout mirrors
// the backlog test above for "mneme spec list" (AC8).
func TestSpecList_AnnouncesUnreadableRowOnStderrWithoutChangingStdout(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-speclist-unreadable"

	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"spec", "new", "healthy spec", "--lane", "standard"); err != nil {
		t.Fatalf("spec new: %v (stderr=%s)", err, stderr)
	}

	healthyStdout, healthyStderr, err := runBacklogCmd(t, dataDir, project, "spec", "list", "--json")
	if err != nil {
		t.Fatalf("spec list --json (healthy): %v (stderr=%s)", err, healthyStderr)
	}
	if healthyStderr != "" {
		t.Errorf("spec list --json (healthy) stderr must be empty, got: %q", healthyStderr)
	}

	dbPath := filepath.Join(dataDir, "projects", project+".db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if _, err := database.ExecContext(context.Background(),
		`INSERT INTO specs (id, title, status, project, backlog_id, assigned_agents, files_changed, created_at, updated_at, lane, scope, base_sha, uuid, previous_ids)
		 VALUES (?, ?, ?, ?, NULL, '[]', '[]', ?, ?, ?, '', '', '', '')`,
		"SPEC-901", "pre-existing malformed spec", string(model.SpecStatusDraft), project,
		"2026-01-01T00:00:00Z", "not-a-timestamp", string(model.LaneStandard),
	); err != nil {
		t.Fatalf("insert malformed spec: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	stdout, stderr, err := runBacklogCmd(t, dataDir, project, "spec", "list", "--json")
	if err != nil {
		t.Fatalf("spec list --json: %v (stderr=%s)", err, stderr)
	}
	if stdout != healthyStdout {
		t.Errorf("spec list --json stdout changed after an unrelated row became unreadable:\ngot:  %q\nwant: %q", stdout, healthyStdout)
	}
	count := strings.Count(stderr, "Row SPEC-901 (spec) could not be fully read")
	if count != 1 {
		t.Errorf("expected the stderr announcement naming SPEC-901 exactly once, got %d: %q", count, stderr)
	}
}

// TestLaneStats_AnnouncesUnreadableRowInNormalOutput is SPEC-133 D13's
// "informe" posture for "mneme lane stats": the aviso lands in the NORMAL
// (stdout) output, not stderr, because lane stats' own output is already a
// report with sections rather than a bare list. A healthy database's --json
// output must not carry the key at all (AC2's basic half).
func TestLaneStats_AnnouncesUnreadableRowInNormalOutput(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-lanestats-unreadable"

	if _, stderr, err := runBacklogCmd(t, dataDir, project,
		"spec", "new", "healthy trivial spec", "--lane", "trivial", "--scope", "internal/model/*.go"); err != nil {
		t.Fatalf("spec new: %v (stderr=%s)", err, stderr)
	}

	healthyJSON, _, err := runBacklogCmd(t, dataDir, project, "lane", "stats", "--json")
	if err != nil {
		t.Fatalf("lane stats --json (healthy): %v", err)
	}
	if strings.Contains(healthyJSON, "unreadable") {
		t.Errorf("lane stats --json on a healthy database must not carry the 'unreadable' key: %s", healthyJSON)
	}

	dbPath := filepath.Join(dataDir, "projects", project+".db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if _, err := database.ExecContext(context.Background(),
		`INSERT INTO specs (id, title, status, project, backlog_id, assigned_agents, files_changed, created_at, updated_at, lane, scope, base_sha, uuid, previous_ids)
		 VALUES (?, ?, ?, ?, NULL, '[]', '[]', ?, ?, ?, '', '', '', '')`,
		"SPEC-902", "pre-existing malformed trivial spec", string(model.SpecStatusDraft), project,
		"2026-01-01T00:00:00Z", "not-a-timestamp", string(model.LaneTrivial),
	); err != nil {
		t.Fatalf("insert malformed spec: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	stdout, stderr, err := runBacklogCmd(t, dataDir, project, "lane", "stats")
	if err != nil {
		t.Fatalf("lane stats: %v (stderr=%s)", err, stderr)
	}
	if !strings.Contains(stdout, "Trivial specs:") {
		t.Errorf("lane stats output is missing its usual report: %s", stdout)
	}
	count := strings.Count(stdout, "Row SPEC-902 (spec) could not be fully read")
	if count != 1 {
		t.Errorf("expected the announcement naming SPEC-902 exactly once in stdout, got %d: %s", count, stdout)
	}
}
