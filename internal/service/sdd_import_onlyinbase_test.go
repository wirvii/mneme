// Package service — a targeted addition (QA rejection, round 3): D22 says
// a broken record is skipped, the rest of the batch enters, and the
// import NEVER aborts. computeOnlyInBase's own failure was found to
// violate that guarantee — a pre-existing row this batch never even
// touches, with a timestamp the store cannot parse, aborted the ENTIRE
// import and discarded every Created/Updated/Skipped entry the batch had
// already recorded, silently, for the rest of the project too. Fixed in
// ImportSDDFromRepo by logging and swallowing computeOnlyInBase's own
// error instead of propagating it.
package service

import (
	"context"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/store"
)

// TestSDDImport_MalformedUnrelatedRowNeverAbortsTheBatch reproduces the
// exact scenario found during review: a backlog row with a timestamp the
// store's own parseTime cannot read, inserted directly via SQL (never
// through any mneme code path, which always writes a parseable format) —
// simulating a row a much older mneme version, or a hand-edited database,
// could have left behind. That row is NOT part of this import's own
// files at all; computeOnlyInBase only touches it while listing every
// item in the project to compute the "only in base" summary.
func TestSDDImport_MalformedUnrelatedRowNeverAbortsTheBatch(t *testing.T) {
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	database.SetMaxOpenConns(1)

	sddStore := store.NewSDDStore(database)
	cfg := config.Default()
	repoDir := newSDDGitRepo(t)
	svc := NewSDDService(sddStore, cfg, importTestProject, nil)
	svc.WithRepoDir(repoDir)
	enableSDD(t, repoDir, importTestProject)
	ctx := context.Background()

	// A row NOT touched by any file in this batch, with a created_at the
	// store's own parseTime rejects outright (no format in its list ever
	// matches "not-a-timestamp") — inserted directly via SQL because no
	// mneme write path can ever produce this.
	if _, err := database.ExecContext(ctx,
		`INSERT INTO backlog_items (id, title, description, status, priority, project, position, lane, scope, uuid, previous_ids, created_at, updated_at)
		 VALUES (?, ?, '', ?, ?, ?, 0, ?, '', ?, '', ?, ?)`,
		"BL-999", "pre-existing malformed row", string(model.BacklogStatusRaw), string(model.PriorityMedium),
		importTestProject, string(model.LaneStandard), "0198f000-0000-7000-8000-0000000009999", "not-a-timestamp", "not-a-timestamp",
	); err != nil {
		t.Fatalf("insert malformed fixture row: %v", err)
	}

	// A perfectly healthy file this batch SHOULD import.
	healthy := &model.BacklogItem{
		ID: "BL-998", Title: "healthy, must still import", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: importTestProject, Lane: model.LaneStandard,
	}
	writeBacklogFixture(t, repoDir, healthy, nil)

	result, err := svc.ImportSDDFromRepo(ctx, repoDir, true)
	if err != nil {
		t.Fatalf("ImportSDDFromRepo must never abort the batch over an unrelated malformed row (D22): %v", err)
	}
	if len(result.Created) != 1 || result.Created[0][:6] != "BL-998" {
		t.Errorf("Created = %v, want exactly one entry for BL-998 — the healthy file must still have imported", result.Created)
	}
	if _, gErr := svc.store.GetBacklogItem(ctx, "BL-998"); gErr != nil {
		t.Errorf("BL-998 was not created despite the batch succeeding: %v", gErr)
	}

	// SPEC-133 D10 supersedes SPEC-131 round 4 here: computeOnlyInBase no
	// longer aborts over BL-999's unparseable created_at — it names the row
	// (result.Unreadable) and still counts its id in the "only in base"
	// summary, exactly like a healthy row would. OnlyInBaseError, which
	// used to be the only way to tell "the calculation failed" apart from
	// "genuinely nothing is only in the base", stays empty because the
	// calculation now genuinely succeeds.
	if result.OnlyInBaseError != "" {
		t.Errorf("OnlyInBaseError = %q, want empty — SPEC-133 tolerates BL-999's malformed row instead of aborting the calculation", result.OnlyInBaseError)
	}
	if result.OnlyInBaseTotal != 1 || len(result.OnlyInBase) != 1 || result.OnlyInBase[0] != "BL-999" {
		t.Errorf("OnlyInBaseTotal=%d OnlyInBase=%v, want 1 and [\"BL-999\"] — BL-999 is only in the base and still countable despite being unreadable",
			result.OnlyInBaseTotal, result.OnlyInBase)
	}
	if len(result.Unreadable) != 1 || result.Unreadable[0].ID != "BL-999" {
		t.Errorf("Unreadable = %+v, want exactly one row naming BL-999", result.Unreadable)
	}
}

// TestSDDImport_OnlyInBaseErrorEmptyOnHealthyImport is the DISCRIMINATING
// other half of the assertion above: OnlyInBaseError must stay empty when
// the only-in-base calculation actually succeeds — proving the field is
// wired to the real failure, not merely always set or always empty.
func TestSDDImport_OnlyInBaseErrorEmptyOnHealthyImport(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, importTestProject)
	enableSDD(t, repoDir, importTestProject)
	ctx := context.Background()

	// A pre-existing item NOT covered by any file in this batch — this is
	// what makes OnlyInBaseTotal genuinely > 0 here, not just present.
	existing := &model.BacklogItem{
		ID: "BL-997", Title: "only in the base", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: importTestProject, Lane: model.LaneStandard,
	}
	if err := svc.store.CreateBacklogItem(ctx, existing); err != nil {
		t.Fatalf("CreateBacklogItem: %v", err)
	}

	healthy := &model.BacklogItem{
		ID: "BL-996", Title: "healthy", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: importTestProject, Lane: model.LaneStandard,
	}
	writeBacklogFixture(t, repoDir, healthy, nil)

	result, err := svc.ImportSDDFromRepo(ctx, repoDir, true)
	if err != nil {
		t.Fatalf("ImportSDDFromRepo: %v", err)
	}
	if result.OnlyInBaseError != "" {
		t.Errorf("OnlyInBaseError = %q, want empty on a healthy import", result.OnlyInBaseError)
	}
	if result.OnlyInBaseTotal != 1 {
		t.Errorf("OnlyInBaseTotal = %d, want 1 (BL-997)", result.OnlyInBaseTotal)
	}
}

// TestSDDImport_MalformedExistingRowIsSkippedAsRoto closes
// importBacklogRecord's own "read-existing" roto branch (QA rejection,
// round 3 — the technique the review found and I had wrongly called
// unreachable): a row whose OWN id matches an incoming file's
// correlative, but whose timestamp the store cannot parse, makes
// GetBacklogItem fail with something other than ErrBacklogNotFound —
// landing in the exact branch that reports "roto" instead of updating.
// Corrupted via a real UPDATE after a normal CreateBacklogItem, not a
// hand-built INSERT — the row is otherwise entirely legitimate.
func TestSDDImport_MalformedExistingRowIsSkippedAsRoto(t *testing.T) {
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	database.SetMaxOpenConns(1)

	sddStore := store.NewSDDStore(database)
	cfg := config.Default()
	repoDir := newSDDGitRepo(t)
	svc := NewSDDService(sddStore, cfg, importTestProject, nil)
	svc.WithRepoDir(repoDir)
	enableSDD(t, repoDir, importTestProject)
	ctx := context.Background()

	existing := &model.BacklogItem{
		ID: "BL-700", Title: "will be corrupted", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: importTestProject, Lane: model.LaneStandard,
	}
	if err := svc.store.CreateBacklogItem(ctx, existing); err != nil {
		t.Fatalf("CreateBacklogItem: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE backlog_items SET created_at = 'not-a-timestamp' WHERE id = ?`, "BL-700"); err != nil {
		t.Fatalf("corrupt fixture row: %v", err)
	}

	// A file that would otherwise legitimately update BL-700.
	writeBacklogFixture(t, repoDir, &model.BacklogItem{
		ID: "BL-700", UUID: existing.UUID, Title: "from the file", Status: model.BacklogStatusRaw,
		Priority: model.PriorityMedium, Project: importTestProject, Lane: model.LaneStandard,
	}, nil)

	result, err := svc.ImportSDDFromRepo(ctx, repoDir, true)
	if err != nil {
		t.Fatalf("ImportSDDFromRepo must never abort over a single malformed row (D22): %v", err)
	}
	byID := map[string]string{}
	for _, s := range result.Skipped {
		byID[s.ID] = s.Reason
	}
	if byID["BL-700"] != "roto" {
		t.Errorf("BL-700 reason = %q, want roto", byID["BL-700"])
	}
}

// TestSDDImport_MalformedExistingSpecIsSkippedAsRoto is
// TestSDDImport_MalformedExistingRowIsSkippedAsRoto's spec-side sibling,
// closing importSpecRecord's own equivalent branch.
func TestSDDImport_MalformedExistingSpecIsSkippedAsRoto(t *testing.T) {
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	database.SetMaxOpenConns(1)

	sddStore := store.NewSDDStore(database)
	cfg := config.Default()
	repoDir := newSDDGitRepo(t)
	svc := NewSDDService(sddStore, cfg, importTestProject, nil)
	svc.WithRepoDir(repoDir)
	enableSDD(t, repoDir, importTestProject)
	ctx := context.Background()

	existing := &model.Spec{
		ID: "SPEC-700", Title: "will be corrupted", Status: model.SpecStatusDraft,
		Project: importTestProject, Lane: model.LaneStandard,
	}
	if err := svc.store.CreateSpec(ctx, existing); err != nil {
		t.Fatalf("CreateSpec: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE specs SET updated_at = 'not-a-timestamp' WHERE id = ?`, "SPEC-700"); err != nil {
		t.Fatalf("corrupt fixture row: %v", err)
	}

	writeSpecFixture(t, repoDir, &model.Spec{
		ID: "SPEC-700", UUID: existing.UUID, Title: "from the file", Status: model.SpecStatusDraft,
		Project: importTestProject, Lane: model.LaneStandard,
	}, nil, nil)

	result, err := svc.ImportSDDFromRepo(ctx, repoDir, true)
	if err != nil {
		t.Fatalf("ImportSDDFromRepo must never abort over a single malformed row (D22): %v", err)
	}
	byID := map[string]string{}
	for _, s := range result.Skipped {
		byID[s.ID] = s.Reason
	}
	if byID["SPEC-700"] != "roto" {
		t.Errorf("SPEC-700 reason = %q, want roto", byID["SPEC-700"])
	}
}
