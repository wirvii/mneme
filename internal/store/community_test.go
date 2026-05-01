package store

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/juanftp/mneme/internal/model"
)

var entityCounter atomic.Int64

// ─── helpers ──────────────────────────────────────────────────────────────────

// newCommunity builds a minimal *model.Community for test use. The caller
// should set EntityIDs before passing to SaveCommunitiesTx if members are needed.
func newCommunity(project string, hash string, entityIDs []string) *model.Community {
	id, _ := uuid.NewV7()
	now := time.Now().UTC()
	return &model.Community{
		ID:             id.String(),
		Project:        project,
		Scope:          model.ScopeProject,
		MembershipHash: hash,
		MemberCount:    len(entityIDs),
		Modularity:     0.42,
		CreatedAt:      now,
		UpdatedAt:      now,
		EntityIDs:      entityIDs,
	}
}

// insertEntity inserts a bare entity row and returns its ID, so community_members
// FK constraints are satisfied. Each call generates a unique entity name.
func insertEntity(t *testing.T, s *MemoryStore, project string) string {
	t.Helper()
	ctx := context.Background()
	n := entityCounter.Add(1)
	e := &model.Entity{
		Name:    fmt.Sprintf("entity-%d", n),
		Kind:    model.KindModule,
		Project: project,
	}
	created, err := s.CreateEntity(ctx, e)
	if err != nil {
		t.Fatalf("insertEntity: %v", err)
	}
	return created.ID
}

// insertMemory inserts a minimal active memory and returns its ID.
func insertCommunityTestMemory(t *testing.T, s *MemoryStore, project string, deleted bool) string {
	t.Helper()
	ctx := context.Background()
	m := &model.Memory{
		Type:      model.TypeDiscovery,
		Scope:     model.ScopeProject,
		Title:     "test memory",
		Content:   "content",
		Project:   project,
		Importance: 0.5,
	}
	created, err := s.Create(ctx, m)
	if err != nil {
		t.Fatalf("insertCommunityTestMemory: %v", err)
	}
	if deleted {
		if err := s.SoftDelete(ctx, created.ID); err != nil {
			t.Fatalf("insertCommunityTestMemory soft-delete: %v", err)
		}
	}
	return created.ID
}

// ─── ListActiveMemoryIDs ──────────────────────────────────────────────────────

// TestStore_ListActiveMemoryIDs verifies that soft-deleted and superseded
// memories are excluded and only active ones are returned.
func TestStore_ListActiveMemoryIDs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	proj := "proj-list-active"

	// 3 active memories.
	id1 := insertCommunityTestMemory(t, s, proj, false)
	id2 := insertCommunityTestMemory(t, s, proj, false)
	id3 := insertCommunityTestMemory(t, s, proj, false)

	// 1 soft-deleted.
	_ = insertCommunityTestMemory(t, s, proj, true)

	// 1 superseded: create two and supersede the second.
	winner := insertCommunityTestMemory(t, s, proj, false)
	loser := insertCommunityTestMemory(t, s, proj, false)
	if err := s.SetSupersededBy(ctx, loser, winner); err != nil {
		t.Fatalf("SetSupersededBy: %v", err)
	}

	ids, err := s.ListActiveMemoryIDs(ctx, proj)
	if err != nil {
		t.Fatalf("ListActiveMemoryIDs: %v", err)
	}

	// Expect: id1, id2, id3, winner (4 active).
	want := map[string]bool{id1: true, id2: true, id3: true, winner: true}
	if len(ids) != len(want) {
		t.Fatalf("got %d ids, want %d: %v", len(ids), len(want), ids)
	}
	for _, id := range ids {
		if !want[id] {
			t.Errorf("unexpected id in result: %s", id)
		}
	}
}

// TestStore_ListActiveMemoryIDs_Empty verifies that an empty project returns
// an empty (non-nil) slice.
func TestStore_ListActiveMemoryIDs_Empty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	ids, err := s.ListActiveMemoryIDs(ctx, "no-such-project")
	if err != nil {
		t.Fatalf("ListActiveMemoryIDs: %v", err)
	}
	if ids == nil {
		t.Error("expected non-nil slice for empty project")
	}
	if len(ids) != 0 {
		t.Errorf("expected 0 ids, got %d", len(ids))
	}
}

// ─── SaveCommunitiesTx ────────────────────────────────────────────────────────

// TestStore_SaveCommunitiesTx_Insert verifies that new communities and their
// members are persisted.
func TestStore_SaveCommunitiesTx_Insert(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	proj := "proj-insert"
	e1 := insertEntity(t, s, proj)
	e2 := insertEntity(t, s, proj)
	e3 := insertEntity(t, s, proj)

	c := newCommunity(proj, "hash-abc", []string{e1, e2, e3})

	if err := s.SaveCommunitiesTx(ctx, []*model.Community{c}, nil, nil); err != nil {
		t.Fatalf("SaveCommunitiesTx insert: %v", err)
	}

	// Verify the community row is present.
	communities, err := s.ListCommunities(ctx, proj)
	if err != nil {
		t.Fatalf("ListCommunities: %v", err)
	}
	if len(communities) != 1 {
		t.Fatalf("got %d communities, want 1", len(communities))
	}
	got := communities[0]
	if got.ID != c.ID {
		t.Errorf("ID = %q, want %q", got.ID, c.ID)
	}
	if got.MembershipHash != "hash-abc" {
		t.Errorf("MembershipHash = %q, want %q", got.MembershipHash, "hash-abc")
	}
	if got.MemberCount != 3 {
		t.Errorf("MemberCount = %d, want 3", got.MemberCount)
	}

	// Verify community members.
	members, err := s.GetCommunityEntityIDs(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCommunityEntityIDs: %v", err)
	}
	if len(members) != 3 {
		t.Fatalf("got %d members, want 3: %v", len(members), members)
	}
}

// TestStore_SaveCommunitiesTx_Update verifies that updating a community
// advances updated_at and adjusts modularity and member_count in-place
// without touching community_members.
func TestStore_SaveCommunitiesTx_Update(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	proj := "proj-update"
	e1 := insertEntity(t, s, proj)

	c := newCommunity(proj, "hash-update", []string{e1})

	// First insert.
	if err := s.SaveCommunitiesTx(ctx, []*model.Community{c}, nil, nil); err != nil {
		t.Fatalf("SaveCommunitiesTx insert: %v", err)
	}

	// Modify and update.
	original := c.UpdatedAt
	time.Sleep(time.Millisecond) // ensure time difference
	c.UpdatedAt = time.Now().UTC()
	c.Modularity = 0.55
	c.MemberCount = 1

	if err := s.SaveCommunitiesTx(ctx, nil, []*model.Community{c}, nil); err != nil {
		t.Fatalf("SaveCommunitiesTx update: %v", err)
	}

	communities, err := s.ListCommunities(ctx, proj)
	if err != nil {
		t.Fatalf("ListCommunities: %v", err)
	}
	if len(communities) != 1 {
		t.Fatalf("got %d communities, want 1", len(communities))
	}
	got := communities[0]
	if got.Modularity != 0.55 {
		t.Errorf("Modularity = %f, want 0.55", got.Modularity)
	}
	if !got.UpdatedAt.After(original) {
		t.Errorf("UpdatedAt should have advanced: got %v, original %v", got.UpdatedAt, original)
	}
}

// TestStore_SaveCommunitiesTx_Delete verifies that deleting a community removes
// the community row and its members via ON DELETE CASCADE.
func TestStore_SaveCommunitiesTx_Delete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	proj := "proj-delete"
	e1 := insertEntity(t, s, proj)

	c := newCommunity(proj, "hash-delete", []string{e1})

	if err := s.SaveCommunitiesTx(ctx, []*model.Community{c}, nil, nil); err != nil {
		t.Fatalf("SaveCommunitiesTx insert: %v", err)
	}

	// Verify member exists.
	members, err := s.GetCommunityEntityIDs(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCommunityEntityIDs before delete: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("expected 1 member before delete, got %d", len(members))
	}

	// Delete the community.
	if err := s.SaveCommunitiesTx(ctx, nil, nil, []string{c.ID}); err != nil {
		t.Fatalf("SaveCommunitiesTx delete: %v", err)
	}

	communities, err := s.ListCommunities(ctx, proj)
	if err != nil {
		t.Fatalf("ListCommunities after delete: %v", err)
	}
	if len(communities) != 0 {
		t.Errorf("expected 0 communities after delete, got %d", len(communities))
	}

	// Members should be gone via cascade.
	members, err = s.GetCommunityEntityIDs(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCommunityEntityIDs after delete: %v", err)
	}
	if len(members) != 0 {
		t.Errorf("expected 0 members after cascade delete, got %d", len(members))
	}
}

// TestStore_SaveCommunitiesTx_Atomic verifies that insert + update + delete all
// happen in one call and the final state is consistent.
func TestStore_SaveCommunitiesTx_Atomic(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	proj := "proj-atomic"
	e1 := insertEntity(t, s, proj)
	e2 := insertEntity(t, s, proj)

	// Seed: one community to update, one to delete.
	toUpdate := newCommunity(proj, "hash-update-atomic", []string{e1})
	toDelete := newCommunity(proj, "hash-delete-atomic", []string{e2})

	if err := s.SaveCommunitiesTx(ctx, []*model.Community{toUpdate, toDelete}, nil, nil); err != nil {
		t.Fatalf("SaveCommunitiesTx seed: %v", err)
	}

	// New community to insert.
	e3 := insertEntity(t, s, proj)
	toInsert := newCommunity(proj, "hash-new-atomic", []string{e3})

	// Apply combined diff.
	toUpdate.Modularity = 0.77
	toUpdate.UpdatedAt = time.Now().UTC()

	if err := s.SaveCommunitiesTx(ctx,
		[]*model.Community{toInsert},
		[]*model.Community{toUpdate},
		[]string{toDelete.ID},
	); err != nil {
		t.Fatalf("SaveCommunitiesTx combined: %v", err)
	}

	communities, err := s.ListCommunities(ctx, proj)
	if err != nil {
		t.Fatalf("ListCommunities: %v", err)
	}
	// Should have 2: toInsert + toUpdate.
	if len(communities) != 2 {
		t.Fatalf("got %d communities, want 2", len(communities))
	}
}

// TestStore_ListCommunities_ByProject verifies that communities from different
// projects are kept isolated and only the requested project's communities
// are returned.
func TestStore_ListCommunities_ByProject(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	projA := "proj-a"
	projB := "proj-b"

	eA := insertEntity(t, s, projA)
	eB := insertEntity(t, s, projB)

	cA := newCommunity(projA, "hash-a", []string{eA})
	cB := newCommunity(projB, "hash-b", []string{eB})

	if err := s.SaveCommunitiesTx(ctx, []*model.Community{cA, cB}, nil, nil); err != nil {
		t.Fatalf("SaveCommunitiesTx: %v", err)
	}

	// Query only projA.
	communities, err := s.ListCommunities(ctx, projA)
	if err != nil {
		t.Fatalf("ListCommunities projA: %v", err)
	}
	if len(communities) != 1 {
		t.Fatalf("got %d communities for projA, want 1", len(communities))
	}
	if communities[0].Project != projA {
		t.Errorf("community project = %q, want %q", communities[0].Project, projA)
	}

	// Query only projB.
	communities, err = s.ListCommunities(ctx, projB)
	if err != nil {
		t.Fatalf("ListCommunities projB: %v", err)
	}
	if len(communities) != 1 {
		t.Fatalf("got %d communities for projB, want 1", len(communities))
	}
}

// TestStore_ListCommunities_Empty verifies that an empty slice (not nil) is
// returned when no communities exist for a project.
func TestStore_ListCommunities_Empty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	communities, err := s.ListCommunities(ctx, "no-communities")
	if err != nil {
		t.Fatalf("ListCommunities: %v", err)
	}
	if communities == nil {
		t.Error("expected non-nil slice")
	}
	if len(communities) != 0 {
		t.Errorf("expected 0 communities, got %d", len(communities))
	}
}

// TestStore_CommunityMembers_CascadeOnEntityDelete verifies that when an entity
// is deleted directly (edge case — entities are rarely deleted), the
// community_members row is removed via ON DELETE CASCADE.
func TestStore_CommunityMembers_CascadeOnEntityDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	proj := "proj-cascade-entity"
	e1 := insertEntity(t, s, proj)
	e2 := insertEntity(t, s, proj)

	c := newCommunity(proj, "hash-cascade-entity", []string{e1, e2})
	if err := s.SaveCommunitiesTx(ctx, []*model.Community{c}, nil, nil); err != nil {
		t.Fatalf("SaveCommunitiesTx: %v", err)
	}

	// Delete entity e1 directly.
	const qDel = `DELETE FROM entities WHERE id = ?`
	if _, err := s.db.ExecContext(ctx, qDel, e1); err != nil {
		t.Fatalf("delete entity: %v", err)
	}

	// community_members for e1 should be gone.
	members, err := s.GetCommunityEntityIDs(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCommunityEntityIDs: %v", err)
	}
	for _, m := range members {
		if m == e1 {
			t.Errorf("entity e1 should have been cascade-deleted from community_members")
		}
	}
	if len(members) != 1 {
		t.Errorf("expected 1 member remaining, got %d", len(members))
	}
}
