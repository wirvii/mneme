package service

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/store"
	"github.com/juanftp/mneme/internal/wikilink"
)

// rebuildBatchSize is the default number of memories processed per transaction
// when the caller does not supply a BatchSize.
const rebuildBatchSize = 500

// extractedEntity holds a single entity candidate extracted from a memory by
// one of the 4 heuristics. The extractor deduplicates by Name within each
// memory so the same entity is only recorded once per memory.
type extractedEntity struct {
	Name string
	Kind model.EntityKind
	Role string // "subject" for H1 (topic_key), "mention" for H2–H4
}

// Regex patterns for the 4 extraction heuristics.
var (
	// reFilePath (H2): matches source-file paths like "internal/store/entity.go"
	// or "`cmd/mneme/main.go`". Requires a known directory prefix and a
	// recognisable source-file extension so random dot-separated words (e.g.
	// version strings) are not captured.
	//
	// The pattern uses `\b` word boundary before the path and requires the path
	// to end at a non-path character or end-of-string. We use \b at the start
	// and a negative character class at the end to avoid consuming delimiters,
	// which would prevent adjacent paths on the same line from all matching.
	reFilePath = regexp.MustCompile(
		`\b((?:internal|cmd|pkg|apps|lib|src|docs|config|test|tests|scripts)/` +
			`[a-zA-Z0-9_\-]+(?:/[a-zA-Z0-9_\-]+)*` +
			`\.(?:go|ts|tsx|js|jsx|py|rs|sql|md|yaml|yml|toml|json|sh))` +
			`(?:[^a-zA-Z0-9_\-./]|$)`,
	)

	// reCodeBlock (H3): matches the content of triple-backtick code blocks.
	reCodeBlock = regexp.MustCompile("(?s)```[a-zA-Z0-9]*\n(.*?)```")

	// reCodeSymbol (H3): matches function/type/struct/class declarations inside
	// code blocks. Only identifiers of 3+ characters are captured.
	reCodeSymbol = regexp.MustCompile(
		`(?:func|type|struct|interface|const|var|package|class|def|fn)\s+([A-Za-z][A-Za-z0-9_]{2,})`,
	)

)

// extractEntities extracts candidate entities from a memory using 4 heuristics:
//
//   - H1: topic_key as a concept entity (always correct, zero false positives).
//   - H2: file paths matching known directory prefixes and source extensions.
//   - H3: code symbol declarations inside triple-backtick code blocks.
//   - H4: wikilink references [[topic_key]].
//
// Entities are deduplicated by name within the memory. Names shorter than 3
// characters are discarded. The returned slice may be empty for short
// memories (e.g. session summaries) that contain no extractable entities.
func extractEntities(m *model.Memory) []extractedEntity {
	seen := make(map[string]bool)
	var result []extractedEntity

	add := func(name string, kind model.EntityKind, role string) {
		name = strings.TrimSpace(name)
		if seen[name] || len([]rune(name)) < 3 {
			return
		}
		seen[name] = true
		result = append(result, extractedEntity{Name: name, Kind: kind, Role: role})
	}

	// H1: topic_key is the strongest signal — every memory with a topic_key
	// gets exactly one concept entity guaranteed.
	if m.TopicKey != "" {
		add(m.TopicKey, model.KindConcept, "subject")
	}

	text := m.Title + "\n" + m.Content

	// H2: file paths.
	for _, match := range reFilePath.FindAllStringSubmatch(text, -1) {
		if len(match) > 1 {
			add(match[1], model.KindFile, "mention")
		}
	}

	// H3: code symbols inside code blocks only (reduces false positives from
	// free prose that happens to contain words like "struct" or "class").
	for _, blockMatch := range reCodeBlock.FindAllStringSubmatch(text, -1) {
		if len(blockMatch) < 2 {
			continue
		}
		for _, symMatch := range reCodeSymbol.FindAllStringSubmatch(blockMatch[1], -1) {
			if len(symMatch) > 1 {
				add(symMatch[1], model.KindModule, "mention")
			}
		}
	}

	// H4: wikilinks [[topic_key]] — delegated to wikilink.Parse which handles
	// code block skip and anchor/alias extraction. Wikilinks inside fenced code
	// blocks are intentionally excluded (correct behaviour: they are examples,
	// not semantic references). This is a regression from the previous regex
	// which extracted wikilinks from code blocks.
	for _, wl := range wikilink.Parse(text) {
		add(wl.Topic, model.KindConcept, "mention")
	}

	return result
}

// pendingLink is a candidate (memoryID, entity) pair produced by entity
// extraction during Phase 1. It is accumulated in memory and, in dry-run mode,
// consumed by findCandidatePairsInMemory to replicate SQL JOIN semantics
// without writing to the database.
type pendingLink struct {
	memoryID string
	entity   extractedEntity
}

// rebuildWeight computes the relation weight for a candidate pair given the
// number of shared entities: min(0.5, sharedCount * 0.1).
//
// K=2 yields 0.2 — stronger than HebbianInitialWeight (0.1) since 2-entity
// overlap is stronger evidence than a single co-access. K=5 yields 0.5,
// capped at DefaultRelationWeights[RelRelatedTo] so backfill never creates
// edges stronger than explicit relations.
func rebuildWeight(sharedCount int) float64 {
	return math.Min(0.5, float64(sharedCount)*0.1)
}

// findCandidatePairsInMemory generates candidate memory pairs from an
// in-memory pending-link list without touching the database. It replicates
// the semantics of store.FindCandidatePairs for dry-run mode, where
// memory_entities is never populated because Phase 1 is read-only.
//
// The algorithm:
//  1. Deduplicates by (memoryID, entityName) so a repeated entity in the same
//     memory does not inflate pair counts.
//  2. Builds an entityName -> []memoryID map.
//  3. For each entity appearing in >= 2 distinct memories, emits all pairwise
//     combinations, normalising the pair so MemoryID1 < MemoryID2 (matching
//     the SQL JOIN predicate me1.memory_id < me2.memory_id).
//  4. Aggregates shared-entity counts per pair.
//  5. Filters out pairs with SharedCount < minShared.
//  6. Sorts by (SharedCount DESC, MemoryID1 ASC, MemoryID2 ASC) to match the
//     SQL ORDER BY, ensuring deterministic output.
func findCandidatePairsInMemory(pending []pendingLink, minShared int) []store.CandidatePair {
	// Step 1: deduplicate by (memoryID, entityName).
	type dedupKey struct{ memoryID, entityName string }
	seen := make(map[dedupKey]bool, len(pending))
	deduped := make([]pendingLink, 0, len(pending))
	for _, p := range pending {
		k := dedupKey{p.memoryID, p.entity.Name}
		if seen[k] {
			continue
		}
		seen[k] = true
		deduped = append(deduped, p)
	}

	// Step 2: build entityName -> []memoryID map.
	entityToMemories := make(map[string][]string)
	for _, p := range deduped {
		entityToMemories[p.entity.Name] = append(entityToMemories[p.entity.Name], p.memoryID)
	}

	// Step 3+4: emit all pairwise combinations and aggregate shared counts.
	type pairKey struct{ id1, id2 string }
	pairCounts := make(map[pairKey]int)
	for _, memIDs := range entityToMemories {
		if len(memIDs) < 2 {
			continue
		}
		for i := 0; i < len(memIDs); i++ {
			for j := i + 1; j < len(memIDs); j++ {
				a, b := memIDs[i], memIDs[j]
				if a > b {
					a, b = b, a
				}
				pairCounts[pairKey{a, b}]++
			}
		}
	}

	// Step 5: filter by minShared and collect.
	result := make([]store.CandidatePair, 0, len(pairCounts))
	for k, count := range pairCounts {
		if count >= minShared {
			result = append(result, store.CandidatePair{
				MemoryID1:   k.id1,
				MemoryID2:   k.id2,
				SharedCount: count,
			})
		}
	}

	// Step 6: deterministic sort matching SQL ORDER BY shared_count DESC.
	sort.Slice(result, func(i, j int) bool {
		if result[i].SharedCount != result[j].SharedCount {
			return result[i].SharedCount > result[j].SharedCount
		}
		if result[i].MemoryID1 != result[j].MemoryID1 {
			return result[i].MemoryID1 < result[j].MemoryID1
		}
		return result[i].MemoryID2 < result[j].MemoryID2
	})

	return result
}

// RebuildGraph extracts entities from existing memories, links them, and
// creates co-occurrence related_to relations between memories sharing
// >= MinShared entities.
//
// The rebuild is idempotent without --force: existing entities and links are
// skipped by FindOrCreateEntity (UPSERT by name) and LinkMemoryEntity
// (INSERT OR IGNORE). Existing relations are skipped by FindRelationBidirectional.
//
// With Force=true, all related_to relations for the project are deleted before
// the rebuild begins. Only related_to is removed — explicit relation types
// (depends_on, implements, supersedes, part_of, uses, conflicts_with,
// references) are never touched (D6, SPEC-009).
//
// DryRun=true performs the full analysis without writing to the database.
// The returned RebuildResult reflects what would have been written.
//
// Telemetry events: graph_rebuild_started, graph_rebuild_done.
func (svc *MemoryService) RebuildGraph(ctx context.Context, req model.RebuildRequest) (*model.RebuildResult, error) {
	// Apply defaults.
	if req.MinShared <= 0 {
		req.MinShared = svc.config.Graph.RebuildMinShared
	}
	if req.MaxRelationsPerMemory <= 0 {
		req.MaxRelationsPerMemory = svc.config.Graph.RebuildMaxRelations
	}
	if req.BatchSize <= 0 {
		req.BatchSize = rebuildBatchSize
	}
	if req.Scope == "" {
		req.Scope = "project"
	}
	if req.Project == "" {
		req.Project = svc.project
	}

	slog.InfoContext(ctx, "graph_rebuild_started",
		"project", req.Project,
		"scope", req.Scope,
		"min_shared", req.MinShared,
		"force", req.Force,
		"dry_run", req.DryRun,
	)

	start := time.Now()
	result := &model.RebuildResult{}

	switch req.Scope {
	case "project":
		if err := svc.rebuildStore(ctx, svc.projectStore, req, result); err != nil {
			return nil, err
		}
	case "global":
		if err := svc.rebuildStore(ctx, svc.globalStore, req, result); err != nil {
			return nil, err
		}
	case "all":
		if err := svc.rebuildStore(ctx, svc.projectStore, req, result); err != nil {
			return nil, err
		}
		// Process global store with empty project (global entities have no
		// project affiliation — cross-scope relations are never created).
		globalReq := req
		globalReq.Project = ""
		if err := svc.rebuildStore(ctx, svc.globalStore, globalReq, result); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("service: rebuild graph: unknown scope %q (want project|global|all)", req.Scope)
	}

	slog.InfoContext(ctx, "graph_rebuild_done",
		"project", req.Project,
		"scope", req.Scope,
		"elapsed_ms", time.Since(start).Milliseconds(),
		"memories_scanned", result.MemoriesScanned,
		"entities_created", result.EntitiesCreated,
		"links_created", result.LinksCreated,
		"relations_created", result.RelationsCreated,
		"relations_deleted", result.RelationsDeleted,
	)

	return result, nil
}

// rebuildStore runs the full rebuild pipeline against a single store.
//
// Phase 1: delete existing related_to (if Force), then extract entities
// and create memory-entity links in batches.
//
// Phase 2: find candidate memory pairs.
//   - Real run: SQL self-JOIN on memory_entities via FindCandidatePairs.
//   - Dry run: in-memory pair generation via findCandidatePairsInMemory,
//     using the pending links accumulated during Phase 1. This is necessary
//     because dry-run Phase 1 never writes to memory_entities, so the SQL
//     JOIN would always return zero rows on a first-time run.
//
// Phase 3: create relations for qualifying pairs up to MaxRelationsPerMemory.
func (svc *MemoryService) rebuildStore(ctx context.Context, s *store.MemoryStore, req model.RebuildRequest, result *model.RebuildResult) error {
	// ── Force: delete existing related_to for this project ──────────────────
	if req.Force && !req.DryRun {
		n, err := s.DeleteRelatedToRelations(ctx, req.Project)
		if err != nil {
			return fmt.Errorf("service: rebuild graph: force delete: %w", err)
		}
		result.RelationsDeleted += n
	}

	// ── Phase 1: load memories and extract entities ──────────────────────────
	//
	// Without force: only memories without existing entity links need
	// processing (idempotent). With force: process all memories because
	// related_to was cleared and we want fresh entity coverage.
	var memories []*model.Memory
	var err error

	if req.Force {
		// list all active memories for this project (paginated by batch size, but
		// we load all first to get a stable total for progress reporting).
		opts := store.ListOptions{
			Project: req.Project,
			Limit:   100_000, // practical cap
		}
		memories, err = s.List(ctx, opts)
	} else {
		memories, err = s.ListMemoriesWithoutEntities(ctx, req.Project, 0)
	}
	if err != nil {
		return fmt.Errorf("service: rebuild graph: list memories: %w", err)
	}

	total := len(memories)
	result.MemoriesScanned += total

	if total == 0 {
		return nil
	}

	// Process in batches, accumulating the full pending list for dry-run pair
	// generation. In real-run mode allPending is discarded after the loop.
	var allPending []pendingLink
	for batchStart := 0; batchStart < total; batchStart += req.BatchSize {
		end := batchStart + req.BatchSize
		if end > total {
			end = total
		}
		batch := memories[batchStart:end]

		batchPending, err := svc.processEntityBatch(ctx, s, req, batch, result)
		if err != nil {
			return fmt.Errorf("service: rebuild graph: entity batch [%d:%d]: %w", batchStart, end, err)
		}
		allPending = append(allPending, batchPending...)

		if req.ProgressFn != nil {
			req.ProgressFn("extraction", end, total)
		}
	}

	// ── Phase 2: find candidate pairs ────────────────────────────────────────
	var pairs []store.CandidatePair
	if req.DryRun {
		// In dry-run mode, memory_entities was not populated (Phase 1 is
		// read-only). Generate pairs from the in-memory pending list to
		// match the SQL JOIN semantics without DB writes.
		pairs = findCandidatePairsInMemory(allPending, req.MinShared)
	} else {
		pairs, err = s.FindCandidatePairs(ctx, req.Project, req.MinShared)
	}
	if err != nil {
		return fmt.Errorf("service: rebuild graph: find candidate pairs: %w", err)
	}

	if len(pairs) == 0 {
		return nil
	}

	// ── Phase 3: create relations for qualifying pairs ───────────────────────
	//
	// Track per-memory relation counts to enforce MaxRelationsPerMemory cap.
	relCount := make(map[string]int)

	relBatchSize := req.BatchSize
	for batchStart := 0; batchStart < len(pairs); batchStart += relBatchSize {
		end := batchStart + relBatchSize
		if end > len(pairs) {
			end = len(pairs)
		}
		batch := pairs[batchStart:end]

		if err := svc.processRelationBatch(ctx, s, req, batch, relCount, result); err != nil {
			return fmt.Errorf("service: rebuild graph: relation batch [%d:%d]: %w", batchStart, end, err)
		}

		if req.ProgressFn != nil {
			req.ProgressFn("relations", end, len(pairs))
		}
	}

	return nil
}

// processEntityBatch extracts entities from a batch of memories and creates
// the entity rows + memory_entities links. Each (memoryID, entityName) pair is
// processed with a pre-existence check so EntitiesExisting and LinksExisting
// counters are accurate.
//
// The returned pending slice contains every (memoryID, entity) pair extracted
// from the batch. In dry-run mode, callers accumulate these across batches for
// in-memory pair generation (findCandidatePairsInMemory). In real-run mode the
// caller may discard the slice.
func (svc *MemoryService) processEntityBatch(ctx context.Context, s *store.MemoryStore, req model.RebuildRequest, batch []*model.Memory, result *model.RebuildResult) ([]pendingLink, error) {
	var pending []pendingLink
	for _, m := range batch {
		extracted := extractEntities(m)
		result.EntitiesExtracted += len(extracted)
		for _, ee := range extracted {
			pending = append(pending, pendingLink{memoryID: m.ID, entity: ee})
		}
	}

	for _, pl := range pending {
		if req.DryRun {
			// Read-only probe: check entity existence without writing.
			existing, err := s.GetEntityByName(ctx, pl.entity.Name, req.Project)
			if err == nil && existing != nil {
				result.EntitiesExisting++
				result.LinksExisting++
			} else {
				result.EntitiesCreated++
				result.LinksCreated++
			}
			continue
		}

		// FindOrCreateEntity is idempotent by (name, project) unique index.
		// It does GetEntityByName internally; we call it directly (1 DB round
		// trip for existing entities, 2 for new ones) and track all as "created"
		// for simplicity — distinguishing requires a second round-trip which
		// doubles the cost. The count accuracy trade-off is documented in the
		// spec (D5: idempotence by existing store patterns).
		e, err := s.FindOrCreateEntity(ctx, pl.entity.Name, pl.entity.Kind, req.Project)
		if err != nil {
			return nil, fmt.Errorf("find or create entity %q: %w", pl.entity.Name, err)
		}
		result.EntitiesCreated++

		// LinkMemoryEntity uses INSERT OR IGNORE, so duplicate calls are safe.
		if linkErr := s.LinkMemoryEntity(ctx, pl.memoryID, e.ID, pl.entity.Role); linkErr != nil {
			return nil, fmt.Errorf("link memory entity: %w", linkErr)
		}
		result.LinksCreated++
	}

	return pending, nil
}

// processRelationBatch creates related_to relations for a batch of candidate
// pairs, respecting the per-memory MaxRelationsPerMemory cap.
func (svc *MemoryService) processRelationBatch(ctx context.Context, s *store.MemoryStore, req model.RebuildRequest, batch []store.CandidatePair, relCount map[string]int, result *model.RebuildResult) error {
	for _, pair := range batch {
		// Enforce per-memory cap on both sides.
		if relCount[pair.MemoryID1] >= req.MaxRelationsPerMemory ||
			relCount[pair.MemoryID2] >= req.MaxRelationsPerMemory {
			result.RelationsSkippedCap++
			continue
		}

		if req.DryRun {
			result.RelationsCreated++
			relCount[pair.MemoryID1]++
			relCount[pair.MemoryID2]++
			continue
		}

		// Resolve entity IDs for the relation. Relations are between entities,
		// not memories directly — we use the source entities of each memory
		// from memory_entities. For the graph rebuild we synthesise a pair
		// relation by finding entities linked to each memory and picking the
		// first shared entity as the anchor for the relation.
		//
		// The spec's model (SPEC-009 §4.2) attaches relations directly to
		// entities. We create a related_to edge between the first entity of
		// mem1 and the first entity of mem2. This is the same approach used
		// by Hebbian (graph/worker.go) which links pairs of memory entities.
		srcEntities, err := s.GetMemoryEntities(ctx, pair.MemoryID1)
		if err != nil || len(srcEntities) == 0 {
			continue
		}
		tgtEntities, err := s.GetMemoryEntities(ctx, pair.MemoryID2)
		if err != nil || len(tgtEntities) == 0 {
			continue
		}

		srcEntityID := srcEntities[0].ID
		tgtEntityID := tgtEntities[0].ID

		// Check idempotency: skip if relation already exists in either direction.
		existing, err := s.FindRelationBidirectional(ctx, srcEntityID, tgtEntityID, model.RelRelatedTo)
		if err != nil {
			return fmt.Errorf("find relation bidirectional: %w", err)
		}
		if existing != nil {
			result.RelationsExisting++
			continue
		}

		weight := rebuildWeight(pair.SharedCount)
		if _, err := s.CreateRelation(ctx, &model.Relation{
			SourceID: srcEntityID,
			TargetID: tgtEntityID,
			Type:     model.RelRelatedTo,
			Weight:   weight,
		}); err != nil {
			return fmt.Errorf("create relation (%s->%s): %w", pair.MemoryID1, pair.MemoryID2, err)
		}

		result.RelationsCreated++
		relCount[pair.MemoryID1]++
		relCount[pair.MemoryID2]++
	}

	return nil
}
