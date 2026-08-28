// Package service — tests for the real write-through materialization
// commit 5 adds: AC10 (disabled = zero bytes), AC11 (numbering unaffected),
// AC15 (write-through reflects the DB end to end), AC16 (a materialization
// failure never fails the caller).
package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/sddfile"
	"github.com/wirvii/mneme/internal/store"
)

// gitRunSDDTest runs a git command in dir with a fixed local identity
// (SPEC-085 R-C: never resolve a real identity in a test), mirroring
// lane_audit_test.go's gitRunLaneTest.
func gitRunSDDTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=sdd-test", "GIT_AUTHOR_EMAIL=sdd-test@example.com",
		"GIT_COMMITTER_NAME=sdd-test", "GIT_COMMITTER_EMAIL=sdd-test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// newSDDGitRepo creates a fresh git repository at t.TempDir() with an
// initial commit, so `git status --porcelain` is meaningful.
func newSDDGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRunSDDTest(t, dir, "init", "-b", "main")
	gitRunSDDTest(t, dir, "config", "user.email", "sdd-test@example.com")
	gitRunSDDTest(t, dir, "config", "user.name", "sdd-test")
	gitRunSDDTest(t, dir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("test repo\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	gitRunSDDTest(t, dir, "add", ".")
	gitRunSDDTest(t, dir, "commit", "-m", "initial")
	return dir
}

// newSDDMaterializeService builds an SDDService with repoDir set to a real
// (caller-supplied, D38) git repository, and returns the repo dir alongside
// it.
func newSDDMaterializeService(t *testing.T, project string) (svc *SDDService, repoDir string) {
	t.Helper()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })

	sddStore := store.NewSDDStore(database)
	cfg := config.Default()
	repoDir = newSDDGitRepo(t)

	svc = NewSDDService(sddStore, cfg, project, nil)
	svc.WithRepoDir(repoDir)
	return svc, repoDir
}

// enableSDD writes the marker directly (this commit does not yet expose
// EnableSDDRepo — that is commit 6) so materialization tests can exercise
// the "on" branch. It also commits the marker so `git status --porcelain`
// starts clean, matching D8's "encender exporta e informa que queda
// pendiente de commitear" flow for the assertions below.
func enableSDD(t *testing.T, repoDir, project string) {
	t.Helper()
	if err := sddfile.WriteMarker(repoDir, sddfile.Marker{
		SDDVersion: 1, Project: project,
		CreatedAt: time.Now().UTC().Format(time.RFC3339), LastExportAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}
}

// runFullSDDCycle exercises add → refine ×2 → promote → advance ×N →
// pushback → resolve against svc, returning the created backlog item ID
// and spec ID. Used by AC10 (disabled) and AC15 (enabled).
func runFullSDDCycle(t *testing.T, ctx context.Context, svc *SDDService) (itemID, specID string) {
	t.Helper()

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{
		Title: "cycle item", Lane: model.LaneStandard, Project: svc.project,
	})
	if err != nil {
		t.Fatalf("BacklogAdd: %v", err)
	}
	itemID = item.ID

	if _, err := svc.BacklogRefine(ctx, model.BacklogRefineRequest{ID: itemID, Refinement: "first pass", By: "orchestrator"}); err != nil {
		t.Fatalf("BacklogRefine (1): %v", err)
	}
	if _, err := svc.BacklogRefine(ctx, model.BacklogRefineRequest{ID: itemID, Refinement: "second pass", By: "orchestrator"}); err != nil {
		t.Fatalf("BacklogRefine (2): %v", err)
	}

	spec, err := svc.BacklogPromote(ctx, itemID)
	if err != nil {
		t.Fatalf("BacklogPromote: %v", err)
	}
	specID = spec.ID

	if _, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: specID, By: "orchestrator"}); err != nil { // draft->speccing
		t.Fatalf("SpecAdvance (1): %v", err)
	}

	// A pushback is only valid from speccing (validTransitionsStandard) —
	// exercised here, then resolved back to speccing, before continuing
	// forward.
	if _, err := svc.SpecPushback(ctx, model.SpecPushbackRequest{ID: specID, FromAgent: "architect", Questions: []string{"¿ok?"}}); err != nil {
		t.Fatalf("SpecPushback: %v", err)
	}
	if _, err := svc.SpecResolve(ctx, model.SpecResolveRequest{ID: specID, Resolution: "sí"}); err != nil {
		t.Fatalf("SpecResolve: %v", err)
	}

	if _, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: specID, By: "orchestrator"}); err != nil { // speccing->specced
		t.Fatalf("SpecAdvance (2): %v", err)
	}
	if _, err := svc.SpecAdvance(ctx, model.SpecAdvanceRequest{ID: specID, By: "orchestrator"}); err != nil { // specced->planning
		t.Fatalf("SpecAdvance (3): %v", err)
	}

	return itemID, specID
}

// countFiles walks dir and returns how many regular files exist under it.
// Returns 0, nil for a dir that does not exist.
func countFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(dir, func(_ string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if !d.IsDir() {
			n++
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return n
}

// TestSDDGitNative_Disabled_WritesNothing is AC10: with NO marker, a full
// SDD cycle writes ZERO bytes into the repository — `git status
// --porcelain` is empty and `.mneme` has no files at all.
func TestSDDGitNative_Disabled_WritesNothing(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, "wirvii/mneme")
	ctx := context.Background()

	runFullSDDCycle(t, ctx, svc)

	status := gitRunSDDTest(t, repoDir, "status", "--porcelain")
	if status != "" {
		t.Errorf("git status --porcelain is not empty with the mechanism disabled:\n%s", status)
	}
	if n := countFiles(t, filepath.Join(repoDir, ".mneme")); n != 0 {
		t.Errorf(".mneme has %d file(s) with the mechanism disabled, want 0", n)
	}
}

// TestSDDNextID_UnaffectedByGitNative is REWRITTEN by SPEC-131 (D55/W11),
// on purpose and declared here so the change is visible in the diff rather
// than looking like someone softened a test that was in the way: it used
// to assert that numbering was IDENTICAL whether the SDD mechanism was on
// or off. SPEC-131 D21 makes that no longer true BY DESIGN — with the
// mechanism on, numbering becomes MAX(the database's own next id, one past
// the highest correlative a file on disk already reserves). This test now
// asserts ONLY the surviving half: with the mechanism OFF, numbering is
// EXACTLY store.NextBacklogID/NextSpecID's own answer — today's behaviour,
// byte for byte. The ON case (D55's new property) is
// TestSDDNextID_MaxOfBaseAndFiles's job (SPEC-131 AC15).
func TestSDDNextID_UnaffectedByGitNative(t *testing.T) {
	ctx := context.Background()

	svc, _ := newSDDMaterializeService(t, "wirvii/mneme")
	// The mechanism is OFF for this service: newSDDMaterializeService never
	// writes a marker.

	want, err := svc.store.NextBacklogID(ctx, "wirvii/mneme")
	if err != nil {
		t.Fatalf("store.NextBacklogID: %v", err)
	}
	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "off", Lane: model.LaneStandard, Project: "wirvii/mneme"})
	if err != nil {
		t.Fatalf("BacklogAdd: %v", err)
	}
	if item.ID != want {
		t.Errorf("BacklogAdd ID = %s, want %s (mechanism off: today's numbering, unchanged)", item.ID, want)
	}

	wantSpec, err := svc.store.NextSpecID(ctx, "wirvii/mneme")
	if err != nil {
		t.Fatalf("store.NextSpecID: %v", err)
	}
	spec, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "off spec", Lane: model.LaneStandard, Project: "wirvii/mneme"})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}
	if spec.ID != wantSpec {
		t.Errorf("SpecNew ID = %s, want %s (mechanism off: today's numbering, unchanged)", spec.ID, wantSpec)
	}
}

// TestSDDExport_WriteThroughReflectsDB is AC15: end to end, the item's and
// the spec's on-disk records — parsed back — agree field by field with
// what BacklogGet/SpecStatus return from the database.
func TestSDDExport_WriteThroughReflectsDB(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, "wirvii/mneme")
	enableSDD(t, repoDir, "wirvii/mneme")
	ctx := context.Background()

	itemID, specID := runFullSDDCycle(t, ctx, svc)

	// --- backlog item ---
	backlogResp, err := svc.BacklogGet(ctx, itemID)
	if err != nil {
		t.Fatalf("BacklogGet: %v", err)
	}

	itemData, err := sddfile.ReadRecord(sddfile.BacklogPath(repoDir, itemID))
	if err != nil {
		t.Fatalf("ReadRecord(backlog): %v", err)
	}
	itemRec, err := sddfile.UnmarshalBacklog(itemData)
	if err != nil {
		t.Fatalf("UnmarshalBacklog: %v", err)
	}

	if itemRec.Item.ID != backlogResp.Item.ID || itemRec.Item.Title != backlogResp.Item.Title ||
		itemRec.Item.Status != backlogResp.Item.Status || itemRec.Item.Project != backlogResp.Item.Project ||
		itemRec.Item.SpecID != backlogResp.Item.SpecID {
		t.Errorf("materialized item = %+v, want to match BacklogGet's %+v", itemRec.Item, backlogResp.Item)
	}
	if len(itemRec.Refinements) != len(backlogResp.Refinements) {
		t.Errorf("materialized item has %d refinements, BacklogGet has %d", len(itemRec.Refinements), len(backlogResp.Refinements))
	}
	for i := range backlogResp.Refinements {
		if i >= len(itemRec.Refinements) {
			break
		}
		if itemRec.Refinements[i].Body != backlogResp.Refinements[i].Body {
			t.Errorf("refinement %d body = %q, want %q", i, itemRec.Refinements[i].Body, backlogResp.Refinements[i].Body)
		}
	}

	// --- spec ---
	specResp, err := svc.SpecStatus(ctx, specID)
	if err != nil {
		t.Fatalf("SpecStatus: %v", err)
	}

	specData, err := sddfile.ReadRecord(sddfile.SpecRecordPath(repoDir, specID))
	if err != nil {
		t.Fatalf("ReadRecord(spec): %v", err)
	}
	specRec, err := sddfile.UnmarshalSpec(specData)
	if err != nil {
		t.Fatalf("UnmarshalSpec: %v", err)
	}

	if specRec.Spec.ID != specResp.Spec.ID || specRec.Spec.Title != specResp.Spec.Title ||
		specRec.Spec.Status != specResp.Spec.Status || specRec.Spec.BacklogID != specResp.Spec.BacklogID {
		t.Errorf("materialized spec = %+v, want to match SpecStatus's %+v", specRec.Spec, specResp.Spec)
	}
	if len(specRec.History) != len(specResp.History) {
		t.Errorf("materialized spec has %d history entries, SpecStatus has %d", len(specRec.History), len(specResp.History))
	}
	if len(specRec.Pushbacks) != len(specResp.Pushbacks) {
		t.Errorf("materialized spec has %d pushbacks, SpecStatus has %d", len(specRec.Pushbacks), len(specResp.Pushbacks))
	}
	for i := range specResp.Pushbacks {
		if i >= len(specRec.Pushbacks) {
			break
		}
		if specRec.Pushbacks[i].Resolution != specResp.Pushbacks[i].Resolution ||
			specRec.Pushbacks[i].Resolved != specResp.Pushbacks[i].Resolved {
			t.Errorf("pushback %d = %+v, want to match %+v", i, specRec.Pushbacks[i], specResp.Pushbacks[i])
		}
	}
}

// TestSDDExport_MaterializeFailureNeverFailsCaller is AC16: with
// .mneme/sdd/backlog unwritable, BacklogAdd still returns the item WITHOUT
// an error — the failure is best-effort and logged, never propagated.
func TestSDDExport_MaterializeFailureNeverFailsCaller(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission-bit simulation is POSIX-specific")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root ignores permission bits")
	}

	svc, repoDir := newSDDMaterializeService(t, "wirvii/mneme")
	enableSDD(t, repoDir, "wirvii/mneme")
	ctx := context.Background()

	backlogDir := filepath.Join(repoDir, ".mneme", "sdd", "backlog")
	if err := os.MkdirAll(backlogDir, 0o755); err != nil {
		t.Fatalf("mkdir backlog dir: %v", err)
	}
	if err := os.Chmod(backlogDir, 0o500); err != nil { // read+execute, no write
		t.Fatalf("chmod backlog dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(backlogDir, 0o755) }) // let t.TempDir() clean up

	item, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{
		Title: "should still succeed", Lane: model.LaneStandard, Project: "wirvii/mneme",
	})
	if err != nil {
		t.Fatalf("BacklogAdd must succeed despite the write-through failure, got: %v", err)
	}
	if item == nil || item.ID == "" {
		t.Fatal("BacklogAdd returned no item despite succeeding")
	}

	// Confirm the DB row really was created — the failure is on the
	// FILESYSTEM side only.
	got, err := svc.BacklogGet(ctx, item.ID)
	if err != nil {
		t.Fatalf("BacklogGet after the write-through failure: %v", err)
	}
	if got.Item.Title != "should still succeed" {
		t.Errorf("BacklogGet.Title = %q, want %q", got.Item.Title, "should still succeed")
	}
}
