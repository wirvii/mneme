package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/store"
)

// firstWordAfter returns the token immediately following prefix in s,
// trimming a trailing "(" if present — used to pull an id out of the
// "Saved: <id> (<action>) — <title>" / "Created <id>: ..." style lines the
// CLI's write commands print.
func firstWordAfter(t *testing.T, s, prefix string) string {
	t.Helper()
	idx := strings.Index(s, prefix)
	if idx < 0 {
		t.Fatalf("output does not contain %q: %s", prefix, s)
	}
	rest := strings.TrimSpace(s[idx+len(prefix):])
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		t.Fatalf("nothing after %q in: %s", prefix, s)
	}
	return strings.TrimSuffix(strings.TrimSuffix(fields[0], ":"), "(")
}

// TestGetCmd_ForeignRefWarning is AC14: "mneme get" prints the foreign-
// reference warning line when at least one reference is foreign, and prints
// nothing extra when every reference is local or unanchored.
func TestGetCmd_ForeignRefWarning(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-get-foreign-ref"

	saveOut, stderr, err := runBacklogCmd(t, dataDir, project,
		"save", "--title", "Cites work", "--content", "See SPEC-125.")
	if err != nil {
		t.Fatalf("save: %v (stderr=%s)", err, stderr)
	}
	id := firstWordAfter(t, saveOut, "Saved:")

	// Force a FOREIGN anchor directly at the store layer: no local SPEC-125
	// exists in this project, so this anchor can never resolve "local" —
	// exactly the state a note imported from a peer's machine leaves behind.
	dbPath := filepath.Join(dataDir, "projects", project+".db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	memStore := store.NewMemoryStore(database)
	ctx := context.Background()
	foreignAnchor := "0198f2c1-4a7b-7c3d-9e10-3f4a5b6c7d8e"
	if err := memStore.SetSDDRefs(ctx, id, []model.SDDRef{{RefID: "SPEC-125", TargetUUID: foreignAnchor}}); err != nil {
		t.Fatalf("SetSDDRefs: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	getOut, stderr, err := runBacklogCmd(t, dataDir, project, "get", id)
	if err != nil {
		t.Fatalf("get: %v (stderr=%s)", err, stderr)
	}
	if !strings.Contains(getOut, "Referencias a trabajo que no está en esta máquina: SPEC-125") {
		t.Errorf("expected the foreign-reference warning line, got:\n%s", getOut)
	}

	// A second memory whose only reference is unanchored must print nothing.
	saveOut2, stderr, err := runBacklogCmd(t, dataDir, project,
		"save", "--title", "Cites nothing real", "--content", "See SPEC-999.")
	if err != nil {
		t.Fatalf("save (unanchored): %v (stderr=%s)", err, stderr)
	}
	id2 := firstWordAfter(t, saveOut2, "Saved:")

	getOut2, stderr, err := runBacklogCmd(t, dataDir, project, "get", id2)
	if err != nil {
		t.Fatalf("get (unanchored): %v (stderr=%s)", err, stderr)
	}
	if strings.Contains(getOut2, "Referencias a trabajo") {
		t.Errorf("an unanchored-only memory must print no warning, got:\n%s", getOut2)
	}
}

// TestUUIDNotInHumanOutput is AC16: the readable output of "mneme spec
// status", "mneme backlog get", and "mneme status" never contains a spec's
// or backlog item's anchor, while the --json output of the first two does
// (D9's visibility rule).
func TestUUIDNotInHumanOutput(t *testing.T) {
	dataDir := t.TempDir()
	project := "test-uuid-not-in-human-output"

	addOut, stderr, err := runBacklogCmd(t, dataDir, project,
		"backlog", "add", "Test item", "--lane", "standard")
	if err != nil {
		t.Fatalf("backlog add: %v (stderr=%s)", err, stderr)
	}
	backlogID := firstWordAfter(t, addOut, "Created")

	specOut, stderr, err := runBacklogCmd(t, dataDir, project,
		"spec", "new", "Test spec", "--lane", "standard")
	if err != nil {
		t.Fatalf("spec new: %v (stderr=%s)", err, stderr)
	}
	specID := firstWordAfter(t, specOut, "Created")

	backlogJSON, stderr, err := runBacklogCmd(t, dataDir, project, "backlog", "get", backlogID, "--json")
	if err != nil {
		t.Fatalf("backlog get --json: %v (stderr=%s)", err, stderr)
	}
	var backlogEnvelope map[string]any
	if err := json.Unmarshal([]byte(backlogJSON), &backlogEnvelope); err != nil {
		t.Fatalf("decode backlog get --json: %v", err)
	}
	backlogItem, ok := backlogEnvelope["item"].(map[string]any)
	if !ok {
		t.Fatalf("expected an 'item' object, got %#v", backlogEnvelope["item"])
	}
	backlogUUID, _ := backlogItem["uuid"].(string)
	if backlogUUID == "" {
		t.Fatalf("backlog get --json: expected a non-empty uuid, got %#v", backlogItem)
	}

	specJSON, stderr, err := runBacklogCmd(t, dataDir, project, "spec", "status", specID, "--json")
	if err != nil {
		t.Fatalf("spec status --json: %v (stderr=%s)", err, stderr)
	}
	var specEnvelope map[string]any
	if err := json.Unmarshal([]byte(specJSON), &specEnvelope); err != nil {
		t.Fatalf("decode spec status --json: %v", err)
	}
	specObj, ok := specEnvelope["spec"].(map[string]any)
	if !ok {
		t.Fatalf("expected a 'spec' object, got %#v", specEnvelope["spec"])
	}
	specUUID, _ := specObj["uuid"].(string)
	if specUUID == "" {
		t.Fatalf("spec status --json: expected a non-empty uuid, got %#v", specObj)
	}

	// Readable outputs: neither anchor may appear anywhere.
	backlogReadable, stderr, err := runBacklogCmd(t, dataDir, project, "backlog", "get", backlogID)
	if err != nil {
		t.Fatalf("backlog get: %v (stderr=%s)", err, stderr)
	}
	if strings.Contains(backlogReadable, backlogUUID) {
		t.Errorf("backlog get readable output leaked the anchor:\n%s", backlogReadable)
	}

	specReadable, stderr, err := runBacklogCmd(t, dataDir, project, "spec", "status", specID)
	if err != nil {
		t.Fatalf("spec status: %v (stderr=%s)", err, stderr)
	}
	if strings.Contains(specReadable, specUUID) {
		t.Errorf("spec status readable output leaked the anchor:\n%s", specReadable)
	}

	statusOut, stderr, err := runBacklogCmd(t, dataDir, project, "status")
	if err != nil {
		t.Fatalf("status: %v (stderr=%s)", err, stderr)
	}
	if strings.Contains(statusOut, backlogUUID) || strings.Contains(statusOut, specUUID) {
		t.Errorf("mneme status leaked an anchor:\n%s", statusOut)
	}
}
