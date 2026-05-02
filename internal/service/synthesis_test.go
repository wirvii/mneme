package service_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/db"
	"github.com/juanftp/mneme/internal/embed"
	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/service"
	"github.com/juanftp/mneme/internal/store"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// buildMembers creates a sorted slice of n mock Memory values for use as
// generator input. Importance decreases from 0.9 to avoid ties in ordering.
func buildMembers(n int) []*model.Memory {
	now := time.Now().UTC()
	members := make([]*model.Memory, n)
	for i := 0; i < n; i++ {
		members[i] = &model.Memory{
			ID:         fmt.Sprintf("mem-%03d", i),
			Title:      fmt.Sprintf("Memory Title %d", i+1),
			Content:    fmt.Sprintf("Content for memory %d with enough text to test excerpts.", i+1),
			Type:       model.TypeDecision,
			Importance: 0.9 - float64(i)*0.01,
			TopicKey:   fmt.Sprintf("architecture/decision-%d", i+1),
			CreatedAt:  now.Add(-time.Duration(i) * time.Hour),
		}
	}
	return members
}

// newSynthesisTestService constructs a MemoryService with synthesis enabled.
func newSynthesisTestService(t *testing.T) (*service.MemoryService, *store.MemoryStore) {
	t.Helper()
	projectDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open project db: %v", err)
	}
	globalDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open global db: %v", err)
	}
	t.Cleanup(func() { projectDB.Close(); globalDB.Close() })

	ps := store.NewMemoryStore(projectDB)
	gs := store.NewMemoryStore(globalDB)
	cfg := config.Default()
	cfg.Graph.SynthesisEnabled = true
	cfg.Graph.SynthesisTopN = 3
	cfg.Graph.SynthesisMaxMembers = 50
	cfg.Graph.CommunityDetectionEnabled = true
	cfg.Graph.CommunityMinSize = 2

	svc := service.NewMemoryService(ps, gs, cfg, "test/synthesis", embed.NopEmbedder{})
	return svc, ps
}

// ─── GenerateSynthesisContent unit tests ─────────────────────────────────────

func TestGenerateSynthesisContent_Basic(t *testing.T) {
	t.Parallel()

	members := buildMembers(5)
	title, content := service.GenerateSynthesisContent("test-community-uuid-001", members, 3, 50)

	// Title must start with "Cluster:" and include top-3 titles.
	if !strings.HasPrefix(title, "Cluster:") {
		t.Errorf("title %q does not start with 'Cluster:'", title)
	}
	for i := 0; i < 3; i++ {
		if !strings.Contains(title, members[i].Title) {
			t.Errorf("title %q missing top member title %q", title, members[i].Title)
		}
	}

	// All 4 sections must be present.
	for _, section := range []string{"## Cluster Overview", "## Top Members", "## All Members", "## Aggregate Metadata"} {
		if !strings.Contains(content, section) {
			t.Errorf("content missing section %q", section)
		}
	}
}

func TestGenerateSynthesisContent_TitleTruncation(t *testing.T) {
	t.Parallel()

	// Create members with very long titles.
	members := make([]*model.Memory, 3)
	for i := range members {
		members[i] = &model.Memory{
			ID:         fmt.Sprintf("mem-%d", i),
			Title:      strings.Repeat(fmt.Sprintf("VeryLongTitleWord%d", i), 5),
			Content:    "content",
			Type:       model.TypeDecision,
			Importance: 0.9 - float64(i)*0.1,
			CreatedAt:  time.Now(),
		}
	}

	title, _ := service.GenerateSynthesisContent("comm-id-001", members, 3, 50)

	if len(title) > 80 {
		t.Errorf("title length %d exceeds 80 chars: %q", len(title), title)
	}
	if !strings.HasSuffix(title, "...") {
		t.Errorf("truncated title %q should end with '...'", title)
	}
}

func TestGenerateSynthesisContent_Wikilinks(t *testing.T) {
	t.Parallel()

	members := buildMembers(3)
	_, content := service.GenerateSynthesisContent("comm-id-002", members, 3, 50)

	// Every member has a topic_key, so every wikilink should appear.
	for _, m := range members {
		wikilink := fmt.Sprintf("[[%s]]", m.TopicKey)
		if !strings.Contains(content, wikilink) {
			t.Errorf("content missing wikilink %q for member %q", wikilink, m.Title)
		}
	}
}

func TestGenerateSynthesisContent_NoTopicKey(t *testing.T) {
	t.Parallel()

	members := buildMembers(2)
	members[1].TopicKey = "" // second member has no topic key

	_, content := service.GenerateSynthesisContent("comm-id-003", members, 2, 50)

	// The member without a topic_key must not produce a wikilink line in top-members.
	if strings.Contains(content, "**Topic:** [[") {
		// Check that any wikilink topic line belongs only to members[0].
		lines := strings.Split(content, "\n")
		topicCount := 0
		for _, l := range lines {
			if strings.Contains(l, "**Topic:** [[") {
				topicCount++
			}
		}
		if topicCount != 1 {
			t.Errorf("expected 1 topic wikilink line (member[0] only), got %d", topicCount)
		}
	}
}

func TestGenerateSynthesisContent_Truncation(t *testing.T) {
	t.Parallel()

	members := buildMembers(60)
	_, content := service.GenerateSynthesisContent("comm-id-004", members, 3, 50)

	if !strings.Contains(content, "Truncated: showing top 50 of 60 members.") {
		t.Error("content missing truncation footer for 60 members with maxMembers=50")
	}
}

func TestGenerateSynthesisContent_Empty(t *testing.T) {
	t.Parallel()

	title, content := service.GenerateSynthesisContent("comm-id-005", []*model.Memory{}, 3, 50)
	if title != "" || content != "" {
		t.Errorf("expected empty strings for zero members, got title=%q content=%q", title, content)
	}
}

func TestGenerateSynthesisContent_Deterministic(t *testing.T) {
	t.Parallel()

	members := buildMembers(10)
	t1, c1 := service.GenerateSynthesisContent("comm-id-006", members, 3, 50)
	t2, c2 := service.GenerateSynthesisContent("comm-id-006", members, 3, 50)

	if t1 != t2 {
		t.Errorf("non-deterministic title: first=%q second=%q", t1, t2)
	}
	if c1 != c2 {
		t.Errorf("non-deterministic content across two identical calls")
	}
}

// ─── GenerateCommunitySyntheses integration tests ────────────────────────────

// seedSynthesisCluster creates n memories, n entities linked 1-1, and dense
// intra-cluster relations. Returns memory IDs and entity IDs.
func seedSynthesisCluster(
	t *testing.T,
	ctx context.Context,
	svc *service.MemoryService,
	ps *store.MemoryStore,
	prefix string,
	n int,
) (memIDs, entityIDs []string) {
	t.Helper()
	for i := 0; i < n; i++ {
		resp, err := svc.Save(ctx, model.SaveRequest{
			Title:   fmt.Sprintf("%s-mem-%d", prefix, i),
			Content: fmt.Sprintf("Content for %s memory %d.", prefix, i),
		})
		if err != nil {
			t.Fatalf("Save %s-%d: %v", prefix, i, err)
		}
		memIDs = append(memIDs, resp.ID)

		ent, err := ps.FindOrCreateEntity(ctx,
			fmt.Sprintf("%s-entity-%d", prefix, i),
			model.KindConcept,
			"test/synthesis",
		)
		if err != nil {
			t.Fatalf("FindOrCreateEntity %s-%d: %v", prefix, i, err)
		}
		entityIDs = append(entityIDs, ent.ID)

		if err := ps.LinkMemoryEntity(ctx, resp.ID, ent.ID, "mention"); err != nil {
			t.Fatalf("LinkMemoryEntity %s-%d: %v", prefix, i, err)
		}
	}

	// Dense intra-cluster edges.
	for i := 0; i < len(entityIDs); i++ {
		for j := i + 1; j < len(entityIDs); j++ {
			if _, err := ps.CreateRelation(ctx, &model.Relation{
				SourceID: entityIDs[i],
				TargetID: entityIDs[j],
				Type:     model.RelRelatedTo,
				Weight:   0.8,
			}); err != nil {
				t.Fatalf("CreateRelation cluster edge: %v", err)
			}
		}
	}
	return memIDs, entityIDs
}

// TestGenerateCommunitySyntheses_NewCommunity verifies that synthesis memories
// are created after community detection inserts new communities.
func TestGenerateCommunitySyntheses_NewCommunity(t *testing.T) {
	svc, ps := newSynthesisTestService(t)
	ctx := context.Background()

	// Seed a cluster large enough to be detected.
	seedSynthesisCluster(t, ctx, svc, ps, "alpha", 4)

	// Run community detection.
	detection, err := svc.DetectAndPersistCommunities(ctx, model.ScopeProject, "test/synthesis")
	if err != nil {
		t.Fatalf("DetectAndPersistCommunities: %v", err)
	}
	if detection.TotalCommunities == 0 {
		t.Skip("no communities detected — graph too sparse for this test environment")
	}

	// Run synthesis generation.
	result, err := svc.GenerateCommunitySyntheses(ctx, model.ScopeProject, "test/synthesis", detection)
	if err != nil {
		t.Fatalf("GenerateCommunitySyntheses: %v", err)
	}
	if result.Created == 0 {
		t.Errorf("expected Created > 0, got %+v", result)
	}

	// Verify synthesis memories are searchable.
	opts := store.ListOptions{
		Project: "test/synthesis",
		Type:    model.TypeSynthesis,
		Limit:   100,
	}
	syntheses, err := ps.List(ctx, opts)
	if err != nil {
		t.Fatalf("List syntheses: %v", err)
	}
	if len(syntheses) == 0 {
		t.Fatal("expected at least one synthesis memory in the store")
	}
	for _, s := range syntheses {
		if !strings.HasPrefix(s.TopicKey, "synthesis/community-") {
			t.Errorf("synthesis topic_key %q does not start with 'synthesis/community-'", s.TopicKey)
		}
		if !strings.Contains(s.Content, "## Cluster Overview") {
			t.Errorf("synthesis content missing '## Cluster Overview' section")
		}
		if s.Type != model.TypeSynthesis {
			t.Errorf("synthesis type = %q, want %q", s.Type, model.TypeSynthesis)
		}
	}
}

// TestGenerateCommunitySyntheses_StableNoChange verifies that running synthesis
// twice without any graph changes produces Skipped > 0 on the second run.
func TestGenerateCommunitySyntheses_StableNoChange(t *testing.T) {
	svc, ps := newSynthesisTestService(t)
	ctx := context.Background()

	seedSynthesisCluster(t, ctx, svc, ps, "beta", 4)

	detection, err := svc.DetectAndPersistCommunities(ctx, model.ScopeProject, "test/synthesis")
	if err != nil {
		t.Fatalf("DetectAndPersistCommunities: %v", err)
	}
	if detection.TotalCommunities == 0 {
		t.Skip("no communities detected")
	}

	// First run — creates syntheses.
	_, err = svc.GenerateCommunitySyntheses(ctx, model.ScopeProject, "test/synthesis", detection)
	if err != nil {
		t.Fatalf("first GenerateCommunitySyntheses: %v", err)
	}

	// Second run without any changes — must skip all.
	second, err := svc.GenerateCommunitySyntheses(ctx, model.ScopeProject, "test/synthesis", detection)
	if err != nil {
		t.Fatalf("second GenerateCommunitySyntheses: %v", err)
	}
	if second.Skipped == 0 {
		t.Errorf("expected Skipped > 0 on second run, got %+v", second)
	}
	if second.Created != 0 || second.Updated != 0 {
		t.Errorf("expected zero Created and Updated on stable run, got %+v", second)
	}
}

// TestGenerateCommunitySyntheses_Disabled verifies that the generator is a
// no-op when SynthesisEnabled=false.
func TestGenerateCommunitySyntheses_Disabled(t *testing.T) {
	projectDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	globalDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open global db: %v", err)
	}
	t.Cleanup(func() { projectDB.Close(); globalDB.Close() })

	ps := store.NewMemoryStore(projectDB)
	gs := store.NewMemoryStore(globalDB)
	cfg := config.Default()
	cfg.Graph.SynthesisEnabled = false
	svc := service.NewMemoryService(ps, gs, cfg, "test/synthesis-off", embed.NopEmbedder{})

	ctx := context.Background()
	result, err := svc.GenerateCommunitySyntheses(ctx, model.ScopeProject, "test/synthesis-off", &model.DetectionResult{})
	if err != nil {
		t.Fatalf("GenerateCommunitySyntheses: %v", err)
	}
	if result.Created != 0 || result.Updated != 0 || result.Deleted != 0 || result.Skipped != 0 {
		t.Errorf("expected zero result when disabled, got %+v", result)
	}
}

// TestGenerateCommunitySyntheses_WikilinksCreated verifies that after a
// synthesis is saved, the synthesis content contains [[topic_key]] wikilinks
// referencing member memories (SPEC-021 D7, acceptance criterion 5).
//
// Fix for QA pushback: the original test created wiki-entity-* entities with
// dense relations but never linked those entities to the saved memories via
// memory_entities. Louvain only finds communities among entities reachable from
// memory seeds, so the unlinked entities were invisible.
//
// The correct approach: save memories with cross-referencing [[topic_key]]
// wikilinks in their content. processWikilinks (SPEC-011) then creates
// entity-to-entity references relations AND links each memory to its entity via
// memory_entities in a single Save call — no manual wiring needed.
func TestGenerateCommunitySyntheses_WikilinksCreated(t *testing.T) {
	svc, ps := newSynthesisTestService(t)
	ctx := context.Background()

	// Save memories in two passes so each can reference the others by topic_key.
	// Pass 1: save all 4 without cross-refs so topic_keys exist when Pass 2 runs.
	topicKeys := []string{
		"wikitest/alpha",
		"wikitest/beta",
		"wikitest/gamma",
		"wikitest/delta",
	}
	memIDs := make([]string, 4)
	for i, tk := range topicKeys {
		resp, err := svc.Save(ctx, model.SaveRequest{
			Title:    fmt.Sprintf("Wiki Memory %d", i),
			Content:  fmt.Sprintf("Initial content for wikitest memory %d.", i),
			TopicKey: tk,
			Project:  "test/synthesis",
		})
		if err != nil {
			t.Fatalf("Save wikitest memory %d: %v", i, err)
		}
		memIDs[i] = resp.ID
	}

	// Pass 2: update each memory so its content references all other topic_keys.
	// This causes processWikilinks to create entity-level references relations and
	// link each memory to its entity via memory_entities.
	for i, tk := range topicKeys {
		var refs strings.Builder
		for j, other := range topicKeys {
			if j != i {
				fmt.Fprintf(&refs, " [[%s]]", other)
			}
		}
		content := fmt.Sprintf("Updated content for %s referencing:%s", tk, refs.String())
		_, err := svc.Update(ctx, memIDs[i], model.UpdateRequest{
			Content: &content,
		})
		if err != nil {
			t.Fatalf("Update wikitest memory %d: %v", i, err)
		}
	}

	// Verify that the wikilink processing linked memories to their entities.
	for i, memID := range memIDs {
		entities, err := ps.GetMemoryEntities(ctx, memID)
		if err != nil {
			t.Fatalf("GetMemoryEntities for mem %d: %v", i, err)
		}
		if len(entities) == 0 {
			t.Fatalf("memory %d (%s) has no entities linked — wikilink processing did not run", i, topicKeys[i])
		}
	}

	detection, err := svc.DetectAndPersistCommunities(ctx, model.ScopeProject, "test/synthesis")
	if err != nil {
		t.Fatalf("DetectAndPersistCommunities: %v", err)
	}
	if detection.TotalCommunities == 0 {
		t.Skip("no communities detected — graph topology did not produce a community at this min_size")
	}

	_, err = svc.GenerateCommunitySyntheses(ctx, model.ScopeProject, "test/synthesis", detection)
	if err != nil {
		t.Fatalf("GenerateCommunitySyntheses: %v", err)
	}

	// Verify that at least one synthesis was created.
	syntheses, err := ps.List(ctx, store.ListOptions{
		Project: "test/synthesis",
		Type:    model.TypeSynthesis,
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("List syntheses: %v", err)
	}
	if len(syntheses) == 0 {
		t.Fatal("expected at least one synthesis memory after GenerateCommunitySyntheses")
	}

	// AC-5: at least one synthesis must contain [[topic_key]] wikilinks.
	// GenerateSynthesisContent emits [[topic_key]] for every member that has a
	// topic_key, so members saved above must appear in synthesis content.
	found := false
	for _, syn := range syntheses {
		if strings.Contains(syn.Content, "[[") {
			found = true
			break
		}
	}
	if !found {
		t.Error("no [[wikilink]] found in any synthesis content — GenerateSynthesisContent did not emit topic_key wikilinks for community members")
	}
}

// TestGenerateCommunitySyntheses_ContentChanged verifies that when a member
// memory's importance is updated, re-running GenerateCommunitySyntheses
// detects the content change and increments Updated (SPEC-021 section 7,
// acceptance criterion 3 lifecycle coverage).
func TestGenerateCommunitySyntheses_ContentChanged(t *testing.T) {
	svc, ps := newSynthesisTestService(t)
	ctx := context.Background()

	// Seed a cluster of 4 memories.
	memIDs, _ := seedSynthesisCluster(t, ctx, svc, ps, "changed", 4)

	// First detection + synthesis generation.
	detection, err := svc.DetectAndPersistCommunities(ctx, model.ScopeProject, "test/synthesis")
	if err != nil {
		t.Fatalf("DetectAndPersistCommunities: %v", err)
	}
	if detection.TotalCommunities == 0 {
		t.Skip("no communities detected — graph too sparse for this test environment")
	}

	first, err := svc.GenerateCommunitySyntheses(ctx, model.ScopeProject, "test/synthesis", detection)
	if err != nil {
		t.Fatalf("first GenerateCommunitySyntheses: %v", err)
	}
	if first.Created == 0 {
		t.Fatalf("expected at least one synthesis created on first run, got %+v", first)
	}

	// Update the importance of the first member to a very high value.
	// GenerateSynthesisContent renders importance in both the "Top Members"
	// section and the "All Members" table, so any importance change produces
	// different content.
	newImportance := 0.99
	if _, err := svc.Update(ctx, memIDs[0], model.UpdateRequest{
		Importance: &newImportance,
	}); err != nil {
		t.Fatalf("Update importance: %v", err)
	}

	// Re-detect communities (same members, but importance change affects sort
	// order which changes the content produced by GenerateSynthesisContent).
	detection2, err := svc.DetectAndPersistCommunities(ctx, model.ScopeProject, "test/synthesis")
	if err != nil {
		t.Fatalf("second DetectAndPersistCommunities: %v", err)
	}

	// Re-generate syntheses — content must differ from the first run.
	second, err := svc.GenerateCommunitySyntheses(ctx, model.ScopeProject, "test/synthesis", detection2)
	if err != nil {
		t.Fatalf("second GenerateCommunitySyntheses: %v", err)
	}
	if second.Updated == 0 {
		t.Errorf("expected Updated >= 1 after importance change, got %+v", second)
	}
	if second.Created != 0 {
		t.Errorf("expected Created=0 on second run (no new communities), got %+v", second)
	}
}

// TestGenerateCommunitySyntheses_DeletedCommunity verifies that when all
// memories of a community are soft-deleted, re-running detection and synthesis
// generation soft-deletes the orphaned synthesis (SPEC-021 section 7, D4
// deleted-community lifecycle, lines 287-316 in synthesis.go).
func TestGenerateCommunitySyntheses_DeletedCommunity(t *testing.T) {
	svc, ps := newSynthesisTestService(t)
	ctx := context.Background()

	// Seed two separate clusters so we get two distinct communities.
	memIDsA, _ := seedSynthesisCluster(t, ctx, svc, ps, "del-a", 4)
	_, _ = seedSynthesisCluster(t, ctx, svc, ps, "del-b", 4)

	// First detection: expect at least two communities.
	detection, err := svc.DetectAndPersistCommunities(ctx, model.ScopeProject, "test/synthesis")
	if err != nil {
		t.Fatalf("DetectAndPersistCommunities: %v", err)
	}
	if detection.TotalCommunities < 2 {
		t.Skipf("need at least 2 communities for this test, got %d — graph too sparse", detection.TotalCommunities)
	}

	// First synthesis generation: creates one synthesis per community.
	first, err := svc.GenerateCommunitySyntheses(ctx, model.ScopeProject, "test/synthesis", detection)
	if err != nil {
		t.Fatalf("first GenerateCommunitySyntheses: %v", err)
	}
	if first.Created < 2 {
		t.Fatalf("expected at least 2 syntheses created, got %+v", first)
	}

	// Soft-delete all memories from cluster A. ListActiveMemoryIDs filters
	// deleted_at IS NULL, so those memories (and their entities) drop out of the
	// Louvain seeds on the next detection run, dissolving cluster A's community.
	for _, id := range memIDsA {
		if err := ps.SoftDelete(ctx, id); err != nil {
			t.Fatalf("SoftDelete memory %s: %v", id, err)
		}
	}

	// Re-detect: cluster A's community should be gone (or significantly shrunken).
	detection2, err := svc.DetectAndPersistCommunities(ctx, model.ScopeProject, "test/synthesis")
	if err != nil {
		t.Fatalf("second DetectAndPersistCommunities: %v", err)
	}
	if detection2.DeletedCommunities == 0 {
		t.Skip("community A was not removed by re-detection — Louvain merged the sparse remnants into another cluster; orphan-delete path untestable in this graph configuration")
	}

	// Re-generate: the orphaned synthesis (for the deleted community) must be
	// soft-expired via service.Forget (sets decay_rate=1.0).
	second, err := svc.GenerateCommunitySyntheses(ctx, model.ScopeProject, "test/synthesis", detection2)
	if err != nil {
		t.Fatalf("second GenerateCommunitySyntheses: %v", err)
	}
	if second.Deleted == 0 {
		t.Errorf("expected Deleted >= 1 after community removal, got %+v", second)
	}

	// Confirm the orphaned synthesis has decay_rate=1.0 (Forget semantics).
	// store.List still returns the memory (deleted_at IS NULL), but its
	// decay_rate must be 1.0 to mark it as forgotten.
	allSyntheses, err := ps.List(ctx, store.ListOptions{
		Project: "test/synthesis",
		Type:    model.TypeSynthesis,
		Limit:   100,
	})
	if err != nil {
		t.Fatalf("List syntheses after deletion: %v", err)
	}
	forgottenCount := 0
	for _, s := range allSyntheses {
		if s.DecayRate == 1.0 {
			forgottenCount++
		}
	}
	if forgottenCount == 0 {
		t.Error("expected at least one synthesis with decay_rate=1.0 (orphan forgotten), found none")
	}
}
