// Package service implements the business logic layer for mneme. It orchestrates
// operations across the store, scoring, and project packages to fulfill memory
// management requests from the CLI and MCP interfaces. Service methods validate
// inputs, apply business rules (importance scoring, upsert logic, access tracking),
// and return domain-typed responses.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/embed"
	"github.com/juanftp/mneme/internal/graph"
	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/scoring"
	"github.com/juanftp/mneme/internal/store"
	syncpkg "github.com/juanftp/mneme/internal/sync"
	"github.com/juanftp/mneme/internal/wikilink"
)

// MemoryService orchestrates memory operations. It owns the business rules for
// validation, default resolution, importance scoring, and upsert semantics.
// All methods require a context.Context as first argument to propagate deadlines
// and cancellations to the underlying store.
//
// It holds two separate stores to enforce the single-database-per-scope
// invariant from the spec: projectStore is backed by
// ~/.mneme/projects/{slug}.db and globalStore is backed by ~/.mneme/global.db.
// Memories with scope=global or scope=org are always routed to globalStore.
//
// The hebbianPool and tracker implement Hebbian auto-strengthening (SPEC-006):
// co-accessed memories generate StrengtheningEvents that update relation weights
// asynchronously. Call Start(ctx) to launch the worker and DrainHebbian to flush
// pending events on shutdown.
type MemoryService struct {
	projectStore *store.MemoryStore // for project-scoped memories
	globalStore  *store.MemoryStore // for global/org-scoped memories
	config       *config.Config
	project      string         // detected or configured project slug
	embedder     embed.Embedder // generates vector representations for semantic search
	hebbianPool  *graph.HebbianWorkerPool
	tracker      *graph.AccessTracker
}

// NewMemoryService constructs a MemoryService. The caller must provide fully
// initialised MemoryStores and Config. projectStore is used for project-scoped
// memories and globalStore for global/org-scoped memories. project is the
// default project slug used when individual requests omit the Project field —
// typically the slug detected from the working directory's git remote.
//
// embedder provides the text embedding strategy. Pass embed.NopEmbedder{} to
// disable embeddings and fall back to FTS5-only retrieval.
//
// The Hebbian worker pool is created here but not started. Call Start(ctx) to
// launch the worker goroutine. For CLI commands call DrainHebbian after the
// command completes to flush pending strengthening events.
func NewMemoryService(projectStore, globalStore *store.MemoryStore, cfg *config.Config, project string, embedder embed.Embedder) *MemoryService {
	logger := slog.Default()
	pool := graph.NewHebbianWorkerPool(projectStore, cfg.Graph, logger)
	tracker := graph.NewAccessTracker(pool, cfg.Graph, logger)

	return &MemoryService{
		projectStore: projectStore,
		globalStore:  globalStore,
		config:       cfg,
		project:      project,
		embedder:     embedder,
		hebbianPool:  pool,
		tracker:      tracker,
	}
}

// storeFor returns the appropriate MemoryStore for the given scope.
// Global and org memories go to globalStore; all other scopes use projectStore.
func (svc *MemoryService) storeFor(scope model.Scope) *store.MemoryStore {
	if scope == model.ScopeGlobal || scope == model.ScopeOrg {
		return svc.globalStore
	}
	return svc.projectStore
}

// Save persists a new memory or updates an existing one via topic key upsert.
//
// Validation rules (applied in order):
//   - Title must not be empty (ErrTitleRequired)
//   - Content must not be empty (ErrContentRequired)
//   - Type defaults to TypeDiscovery when omitted
//   - Scope defaults to ScopeProject when omitted
//   - Validated Type and Scope must be known values (ErrInvalidType / ErrInvalidScope)
//   - Project defaults to the service's project when omitted
//
// When TopicKey is non-empty and a memory with the same (topic_key, project,
// scope) triple already exists, Save updates the existing record and returns
// action "updated". Otherwise it creates a new record and returns action "created".
func (svc *MemoryService) Save(ctx context.Context, req model.SaveRequest) (*model.SaveResponse, error) {
	if req.Title == "" {
		return nil, fmt.Errorf("service: save: %w", model.ErrTitleRequired)
	}
	if req.Content == "" {
		return nil, fmt.Errorf("service: save: %w", model.ErrContentRequired)
	}

	if req.Type == "" {
		req.Type = model.TypeDiscovery
	}
	if req.Scope == "" {
		req.Scope = model.ScopeProject
	}

	if !req.Type.Valid() {
		return nil, fmt.Errorf("service: save: %w", model.ErrInvalidType)
	}
	if !req.Scope.Valid() {
		return nil, fmt.Errorf("service: save: %w", model.ErrInvalidScope)
	}

	// Validate and normalise rule-specific fields. Rules require a non-empty
	// applies_to list. Non-rules must not carry applies_to. Severity defaults
	// to "warn" when omitted for rules; non-rules always get empty severity.
	if req.Type == model.TypeRule {
		if len(req.AppliesTo) == 0 {
			return nil, fmt.Errorf("service: save: %w", model.ErrAppliesToRequired)
		}
		for _, p := range req.AppliesTo {
			if p == "" {
				return nil, fmt.Errorf("service: save: %w", model.ErrEmptyPattern)
			}
		}
		if req.Severity == "" {
			req.Severity = model.SeverityWarn
		}
		if !req.Severity.Valid() {
			return nil, fmt.Errorf("service: save: %w", model.ErrInvalidSeverity)
		}
	} else {
		if len(req.AppliesTo) > 0 {
			return nil, fmt.Errorf("service: save: %w", model.ErrAppliesToForbidden)
		}
		// Normalise severity to empty for non-rules so it is stored as ''
		// in the database, consistent with the CHECK constraint default.
		req.Severity = ""
	}

	if req.Project == "" {
		req.Project = svc.project
	}

	importance := scoring.InitialImportance(req.Type, req.Importance)
	decayRate := scoring.DecayRateForType(req.Type)

	m := &model.Memory{
		Type:       req.Type,
		Scope:      req.Scope,
		Title:      req.Title,
		Content:    req.Content,
		TopicKey:   req.TopicKey,
		Project:    req.Project,
		SessionID:  req.SessionID,
		CreatedBy:  req.CreatedBy,
		Files:      req.Files,
		Importance: importance,
		Confidence: model.DefaultConfidence,
		DecayRate:  decayRate,
		AppliesTo:  req.AppliesTo,
		Severity:   req.Severity,
	}

	targetStore := svc.storeFor(m.Scope)
	result, created, err := targetStore.Upsert(ctx, m)
	if err != nil {
		return nil, fmt.Errorf("service: save: %w", err)
	}

	// Generate and persist the embedding synchronously (best-effort).
	// TF-IDF embed takes <1 ms so there is no value in deferring it to a
	// background goroutine at this scale. Failures are logged but never
	// returned to the caller — a missing embedding only degrades search
	// quality, not correctness.
	svc.embedMemory(ctx, targetStore, result)
	svc.processWikilinks(ctx, result, targetStore)
	// SPEC-012: after processWikilinks has registered any new unresolved refs,
	// attempt to resolve pending refs that point to result's topic_key. This
	// runs post-commit so the new memory is already visible to FindByTopicKey
	// inside createWikilinkRelation. No-op when result.TopicKey is empty.
	svc.autoResolveUnresolved(ctx, result, targetStore)

	action := "created"
	if !created {
		action = "updated"
	}

	return &model.SaveResponse{
		ID:            result.ID,
		Action:        action,
		RevisionCount: result.RevisionCount,
		Title:         result.Title,
		TopicKey:      result.TopicKey,
	}, nil
}

// Get retrieves a memory by its UUIDv7 id and increments its access counter.
// The access increment is best-effort: failures are logged but not returned to
// the caller so a read never fails due to a counter-update glitch.
// Returns ErrNotFound when no active memory exists with that id in either store.
//
// If the memory has entities linked in the knowledge graph, Get records the
// access in the Hebbian tracker so co-access pairs can strengthen relations
// asynchronously. Rules and session summaries are excluded from tracking (D5).
func (svc *MemoryService) Get(ctx context.Context, id string) (*model.Memory, error) {
	// Search project store first, then fall back to global store.
	m, foundIn, err := svc.getFromEitherStore(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("service: get: %w", err)
	}
	if m == nil {
		return nil, fmt.Errorf("service: get: %w", model.ErrNotFound)
	}

	if err := foundIn.IncrementAccess(ctx, id); err != nil {
		log.Printf("service: get: increment access for %s: %v", id, err)
	}

	// Hebbian tracking: record this access for co-access pair generation.
	// GetMemoryEntities is O(log n) and returns empty when no entities are linked.
	// The tracker itself filters rules/session_summaries and cross-scope pairs.
	svc.recordHebbianAccess(ctx, foundIn, m)

	return m, nil
}

// recordHebbianAccess fetches the entity IDs linked to m and calls
// tracker.Record. Failures are best-effort: a missed tracking event never
// affects correctness.
func (svc *MemoryService) recordHebbianAccess(ctx context.Context, s *store.MemoryStore, m *model.Memory) {
	entities, err := s.GetMemoryEntities(ctx, m.ID)
	if err != nil {
		log.Printf("service: hebbian: get entities for %s: %v", m.ID, err)
		return
	}
	entityIDs := make([]string, len(entities))
	for i, e := range entities {
		entityIDs[i] = e.ID
	}
	svc.tracker.Record(m.ID, m.Type, m.Scope, entityIDs)
}

// getFromEitherStore looks up id in projectStore first, then globalStore.
// It returns the memory, the store it was found in, and any error.
// When the memory is not found in either store, m is nil and err is nil.
func (svc *MemoryService) getFromEitherStore(ctx context.Context, id string) (*model.Memory, *store.MemoryStore, error) {
	m, err := svc.projectStore.Get(ctx, id)
	if err != nil {
		return nil, nil, fmt.Errorf("project store: %w", err)
	}
	if m != nil {
		return m, svc.projectStore, nil
	}

	m, err = svc.globalStore.Get(ctx, id)
	if err != nil {
		return nil, nil, fmt.Errorf("global store: %w", err)
	}
	if m != nil {
		return m, svc.globalStore, nil
	}

	return nil, nil, nil
}

// Update applies a partial update to an existing memory identified by id.
// Only non-nil fields in req are applied; other fields remain unchanged.
// Returns ErrNotFound when no active memory exists with that id in either store.
func (svc *MemoryService) Update(ctx context.Context, id string, req model.UpdateRequest) (*model.SaveResponse, error) {
	_, targetStore, err := svc.getFromEitherStore(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("service: update: %w", err)
	}
	if targetStore == nil {
		return nil, fmt.Errorf("service: update: %w", model.ErrNotFound)
	}

	if err := targetStore.Update(ctx, id, &req); err != nil {
		return nil, fmt.Errorf("service: update: %w", err)
	}

	updated, err := targetStore.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("service: update: reload: %w", err)
	}
	if updated == nil {
		return nil, fmt.Errorf("service: update: reload: %w", model.ErrNotFound)
	}

	// Re-embed when title or content changed — the embedding must reflect
	// the current text so vector search stays accurate.
	if req.Title != nil || req.Content != nil {
		svc.embedMemory(ctx, targetStore, updated)
	}
	// Process wikilinks when content changed — new links create relations,
	// removed links are intentionally not deleted (append-only, D9 SPEC-011).
	if req.Content != nil {
		svc.processWikilinks(ctx, updated, targetStore)
	}

	return &model.SaveResponse{
		ID:            updated.ID,
		Action:        "updated",
		RevisionCount: updated.RevisionCount,
		Title:         updated.Title,
		TopicKey:      updated.TopicKey,
	}, nil
}

// Forget soft-expires a memory by setting its decay rate to 1.0, causing it to
// lose importance rapidly on subsequent scoring passes. The reason parameter is
// accepted for future use (Phase 3 metadata storage) but is not persisted in
// this implementation. Returns ErrNotFound when no active memory exists with
// the given id in either store.
func (svc *MemoryService) Forget(ctx context.Context, id string, reason string) error {
	_, targetStore, err := svc.getFromEitherStore(ctx, id)
	if err != nil {
		return fmt.Errorf("service: forget: %w", err)
	}
	if targetStore == nil {
		return fmt.Errorf("service: forget: %w", model.ErrNotFound)
	}

	if err := targetStore.SetDecayRate(ctx, id, 1.0); err != nil {
		return fmt.Errorf("service: forget: %w", err)
	}

	return nil
}

// ProjectSlug returns the project slug associated with this service instance.
// It is either the value detected from git or the value provided at construction.
func (svc *MemoryService) ProjectSlug() string {
	return svc.project
}

// DrainHebbian closes the Hebbian worker pool and waits up to 200 ms for
// pending strengthening events to be processed. CLI commands should call this
// after their main work completes so that co-access signals are not silently
// discarded on process exit.
func (svc *MemoryService) DrainHebbian() {
	svc.hebbianPool.Drain(200 * time.Millisecond)
}

// Config returns the configuration used by this service instance.
// Callers may use it to derive paths (e.g. database locations) for display.
func (svc *MemoryService) Config() *config.Config {
	return svc.config
}

// Count returns the number of active (non-deleted) memories for the given
// project slug from the project store.
func (svc *MemoryService) Count(ctx context.Context, project string) (int, error) {
	n, err := svc.projectStore.Count(ctx, project)
	if err != nil {
		return 0, fmt.Errorf("service: count: %w", err)
	}
	return n, nil
}

// CountGlobal returns the number of active (non-deleted) memories in the
// global store. The empty project string matches all records stored there.
func (svc *MemoryService) CountGlobal(ctx context.Context) (int, error) {
	n, err := svc.globalStore.Count(ctx, "")
	if err != nil {
		return 0, fmt.Errorf("service: count global: %w", err)
	}
	return n, nil
}

// List returns active memories matching the given filters. It delegates directly
// to the underlying store's List method, selecting the appropriate store based
// on the requested scope. When opts.Project is empty it defaults to the service's
// configured project slug so callers can omit it for the common case.
func (svc *MemoryService) List(ctx context.Context, opts store.ListOptions) ([]*model.Memory, error) {
	if opts.Project == "" {
		opts.Project = svc.project
	}

	targetStore := svc.projectStore
	if opts.Scope == model.ScopeGlobal || opts.Scope == model.ScopeOrg {
		targetStore = svc.globalStore
	}

	memories, err := targetStore.List(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("service: list: %w", err)
	}
	return memories, nil
}

// ListRulesOptions parameterises a ListRules query.
type ListRulesOptions struct {
	// Scope restricts results. Empty or "all" queries both stores.
	Scope string

	// Severity filters by severity. Empty means all severities.
	Severity model.Severity

	// Limit caps results after merge. Defaults to 50 when zero.
	Limit int
}

// ListRules returns active rule-type memories from the project and/or global
// stores, sorted by severity descending, importance descending, and then
// updated_at descending. When opts.Scope is empty or "all" both stores are
// queried and their results merged. Severity filtering is applied post-merge.
func (svc *MemoryService) ListRules(ctx context.Context, opts ListRulesOptions) ([]*model.Memory, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}

	var merged []*model.Memory

	// Query project store unless the caller requested global only.
	if opts.Scope == "" || opts.Scope == "all" || opts.Scope == string(model.ScopeProject) {
		projectRules, err := svc.projectStore.List(ctx, store.ListOptions{
			Project: svc.project,
			Type:    model.TypeRule,
			OrderBy: "importance DESC, updated_at DESC",
			Limit:   limit,
		})
		if err != nil {
			return nil, fmt.Errorf("service: list rules: project store: %w", err)
		}
		merged = append(merged, projectRules...)
	}

	// Query global store unless the caller requested project only.
	if opts.Scope == "" || opts.Scope == "all" || opts.Scope == string(model.ScopeGlobal) {
		globalRules, err := svc.globalStore.List(ctx, store.ListOptions{
			Type:    model.TypeRule,
			OrderBy: "importance DESC, updated_at DESC",
			Limit:   limit,
		})
		if err != nil {
			return nil, fmt.Errorf("service: list rules: global store: %w", err)
		}
		merged = append(merged, globalRules...)
	}

	// Apply severity filter post-merge.
	if opts.Severity != "" {
		filtered := merged[:0]
		for _, m := range merged {
			if m.Severity == opts.Severity {
				filtered = append(filtered, m)
			}
		}
		merged = filtered
	}

	// Stable sort: severity desc, importance desc, updated_at desc.
	sortRules(merged)

	// Apply limit to the merged+filtered result.
	if len(merged) > limit {
		merged = merged[:limit]
	}

	return merged, nil
}

// sortRules sorts rule memories by severity descending, importance descending,
// then updated_at descending. This is the canonical ordering used for displaying
// rules to users and for context injection.
func sortRules(rules []*model.Memory) {
	sevOrder := func(s model.Severity) int {
		switch s {
		case model.SeverityBlock:
			return 3
		case model.SeverityWarn:
			return 2
		case model.SeverityInfo:
			return 1
		default:
			return 0
		}
	}

	// Use a simple insertion-style stable sort via sort.SliceStable.
	n := len(rules)
	for i := 1; i < n; i++ {
		for j := i; j > 0; j-- {
			a, b := rules[j-1], rules[j]
			sa, sb := sevOrder(a.Severity), sevOrder(b.Severity)
			if sa < sb {
				rules[j-1], rules[j] = rules[j], rules[j-1]
				continue
			}
			if sa == sb && a.Importance < b.Importance {
				rules[j-1], rules[j] = rules[j], rules[j-1]
				continue
			}
			if sa == sb && a.Importance == b.Importance && a.UpdatedAt.Before(b.UpdatedAt) {
				rules[j-1], rules[j] = rules[j], rules[j-1]
				continue
			}
			break
		}
	}
}

// ExportToFile exports all active memories for the service's current project to
// a gzip-compressed JSONL archive at <dir>/.mneme/sync/<slug>.jsonl.gz. It
// delegates to sync.ExportToFile and returns the archive path, an ExportResult
// summary, and any error. This method exists so the CLI layer does not need to
// access the internal project store directly.
func (svc *MemoryService) ExportToFile(ctx context.Context, dir string) (string, *syncpkg.ExportResult, error) {
	path, result, err := syncpkg.ExportToFile(ctx, svc.projectStore, svc.project, dir)
	if err != nil {
		return "", nil, fmt.Errorf("service: export to file: %w", err)
	}
	return path, result, nil
}

// ImportFromFile imports memories from the gzip-compressed JSONL archive at
// path into the project store. It delegates to sync.ImportFromFile and returns
// an ImportResult summary, or any error.
func (svc *MemoryService) ImportFromFile(ctx context.Context, path string) (*syncpkg.ImportResult, error) {
	result, err := syncpkg.ImportFromFile(ctx, svc.projectStore, path)
	if err != nil {
		return nil, fmt.Errorf("service: import from file: %w", err)
	}
	return result, nil
}

// ExportManifestToFile exports all data for the current project (memories,
// entities, relations, sessions) as a Memory Manifest v1.0 tarball at
// <dir>/.mneme/sync/<project-slug>.manifest.tar.gz.
//
// producerVer is the mneme binary version string (e.g. "1.0.0" or "dev").
func (svc *MemoryService) ExportManifestToFile(ctx context.Context, dir, producerVer string) (string, *syncpkg.ManifestExportResult, error) {
	path, result, err := syncpkg.ExportManifestToFile(ctx, svc.projectStore, "mneme", producerVer, svc.project, dir)
	if err != nil {
		return "", nil, fmt.Errorf("service: export manifest to file: %w", err)
	}
	return path, result, nil
}

// ImportManifestFromFile imports records from the archive at path into the
// project store. The format is auto-detected from the file name and content:
// .manifest.tar.gz archives are processed as Memory Manifests; .jsonl.gz
// archives fall back to the legacy JSONL importer.
//
// Returns a ManifestImportResult with counts for all imported types.
func (svc *MemoryService) ImportManifestFromFile(ctx context.Context, path string) (*syncpkg.ManifestImportResult, error) {
	result, err := syncpkg.ImportManifestFromFile(ctx, svc.projectStore, path)
	if err != nil {
		return nil, fmt.Errorf("service: import manifest from file: %w", err)
	}
	return result, nil
}

// Stats aggregates metrics about the memory store for the given project slug.
// It queries the project store for per-type/per-scope counts, active vs.
// superseded vs. forgotten tallies, oldest/newest timestamps, and average
// importance. The DB size is derived from the file on disk using the path
// returned by config.Config.ProjectDBPath.
//
// Pass an empty project to aggregate over the global store instead.
func (svc *MemoryService) Stats(ctx context.Context, project string) (*model.StatsResponse, error) {
	s := svc.projectStore
	if project == "" {
		s = svc.globalStore
	}

	byType, err := s.CountByType(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("service: stats: by type: %w", err)
	}

	byScope, err := s.CountByScope(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("service: stats: by scope: %w", err)
	}

	active, err := s.CountActive(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("service: stats: active: %w", err)
	}

	superseded, err := s.CountSuperseded(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("service: stats: superseded: %w", err)
	}

	forgotten, err := s.CountForgotten(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("service: stats: forgotten: %w", err)
	}

	total, err := s.CountTotal(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("service: stats: total: %w", err)
	}

	oldest, newest, err := s.OldestNewest(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("service: stats: oldest/newest: %w", err)
	}

	avgImportance, err := s.AvgImportance(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("service: stats: avg importance: %w", err)
	}

	// Resolve the DB file path from config so we can stat it for size.
	var dbPath string
	if project == "" {
		dbPath = svc.config.GlobalDBPath()
	} else {
		dbPath = svc.config.ProjectDBPath(project)
	}

	var dbSize int64
	if info, statErr := os.Stat(dbPath); statErr == nil {
		dbSize = info.Size()
	}

	projectLabel := project
	if projectLabel == "" {
		projectLabel = "global"
	}

	embCount, err := s.CountEmbeddings(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("service: stats: embeddings count: %w", err)
	}

	// Knowledge gaps summary — populate when there are unresolved wikilink refs.
	gapTotal, gapErr := s.CountDistinctGaps(ctx, project)
	if gapErr != nil {
		return nil, fmt.Errorf("service: stats: count distinct gaps: %w", gapErr)
	}

	var knowledgeGaps *model.KnowledgeGaps
	if gapTotal > 0 {
		topGaps, _, listErr := s.ListGaps(ctx, project, 5, 1)
		if listErr != nil {
			return nil, fmt.Errorf("service: stats: list top gaps: %w", listErr)
		}
		// Load up to 3 samples for each of the top 5 gaps.
		for i := range topGaps {
			samples, sErr := s.ListGapSamples(ctx, topGaps[i].TargetTopicKey, project, 3)
			if sErr != nil {
				return nil, fmt.Errorf("service: stats: list gap samples: %w", sErr)
			}
			topGaps[i].Samples = samples
		}
		knowledgeGaps = &model.KnowledgeGaps{
			Total: gapTotal,
			Top:   topGaps,
		}
	}

	return &model.StatsResponse{
		Project:         projectLabel,
		TotalMemories:   total,
		ByType:          byType,
		ByScope:         byScope,
		Active:          active,
		Superseded:      superseded,
		Forgotten:       forgotten,
		DBSizeBytes:     dbSize,
		OldestMemory:    oldest,
		NewestMemory:    newest,
		AvgImportance:   avgImportance,
		EmbeddingsCount: embCount,
		KnowledgeGaps:   knowledgeGaps,
	}, nil
}

// Gaps returns aggregated knowledge gaps from the unresolved_references table.
// It supports multi-scope queries (project, global, all) and optionally loads
// up to 3 sample source memories for each gap.
//
// Default behaviour (zero-value GapsRequest):
//   - scope=project, querying the project store only.
//   - limit=20 (clamped to 100 max).
//   - minMentions=1 (all gaps included).
//   - include_samples=true.
func (svc *MemoryService) Gaps(ctx context.Context, req model.GapsRequest) (*model.GapsResponse, error) {
	// Apply defaults and clamp.
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	minMentions := req.MinMentions
	if minMentions <= 0 {
		minMentions = 1
	}

	includeSamples := true
	if req.IncludeSamples != nil {
		includeSamples = *req.IncludeSamples
	}

	scope := req.Scope
	if scope == "" {
		scope = string(model.ScopeProject)
	}

	project := req.Project
	if project == "" {
		project = svc.project
	}

	var merged []model.Gap
	var total int

	// Query project store unless the caller requested global only.
	if scope == string(model.ScopeProject) || scope == "all" {
		pGaps, pTotal, err := svc.projectStore.ListGaps(ctx, project, limit, minMentions)
		if err != nil {
			return nil, fmt.Errorf("service: gaps: project store: %w", err)
		}
		merged = append(merged, pGaps...)
		total += pTotal
	}

	// Query global store unless the caller requested project only.
	if scope == string(model.ScopeGlobal) || scope == "all" {
		gGaps, gTotal, err := svc.globalStore.ListGaps(ctx, "", limit, minMentions)
		if err != nil {
			return nil, fmt.Errorf("service: gaps: global store: %w", err)
		}
		merged = append(merged, gGaps...)
		total += gTotal
	}

	// Re-sort merged results when both stores were queried.
	if scope == "all" {
		sortGapsByMentions(merged)
		if len(merged) > limit {
			merged = merged[:limit]
		}
	}

	// Load samples for each gap.
	if includeSamples {
		for i := range merged {
			// Determine which store and project to query for this gap.
			// Gaps from the global store have an empty project in the table.
			gapProject := project
			s := svc.projectStore
			if scope == string(model.ScopeGlobal) {
				gapProject = ""
				s = svc.globalStore
			}
			samples, err := s.ListGapSamples(ctx, merged[i].TargetTopicKey, gapProject, 3)
			if err != nil {
				return nil, fmt.Errorf("service: gaps: list samples for %q: %w", merged[i].TargetTopicKey, err)
			}
			merged[i].Samples = samples
		}
	}

	if merged == nil {
		merged = []model.Gap{}
	}

	projectLabel := project
	if projectLabel == "" {
		projectLabel = "global"
	}

	return &model.GapsResponse{
		Gaps:    merged,
		Total:   total,
		Project: projectLabel,
	}, nil
}

// sortGapsByMentions sorts gaps by TotalMentions descending then SourceCount
// descending. Used to merge and re-sort results from multiple stores.
func sortGapsByMentions(gaps []model.Gap) {
	n := len(gaps)
	for i := 1; i < n; i++ {
		for j := i; j > 0; j-- {
			a, b := gaps[j-1], gaps[j]
			if a.TotalMentions < b.TotalMentions ||
				(a.TotalMentions == b.TotalMentions && a.SourceCount < b.SourceCount) {
				gaps[j-1], gaps[j] = gaps[j], gaps[j-1]
				continue
			}
			break
		}
	}
}

// EmbedBackfillResult summarises the outcome of an EmbedBackfill run.
type EmbedBackfillResult struct {
	// Total is the number of memories that lacked an embedding at the start.
	Total int
	// Embedded is the number of embeddings successfully generated and stored.
	Embedded int
	// Failed is the number of memories for which embedding failed.
	Failed int
}

// EmbedBackfill generates embeddings for all active memories in the project
// (and optionally the global) store that do not yet have one. Progress is
// reported via the progressFn callback, which receives the current memory's
// title and its 1-based index. Pass a nil progressFn to suppress progress output.
//
// EmbedBackfill is a no-op when the embedder is NopEmbedder.
func (svc *MemoryService) EmbedBackfill(ctx context.Context, project string, batchSize int, progressFn func(title string, current, total int)) (*EmbedBackfillResult, error) {
	if svc.embedder.Model() == "none" {
		return &EmbedBackfillResult{}, nil
	}

	if batchSize <= 0 {
		batchSize = 100
	}

	// Process the project store.
	projectResult, err := svc.backfillStore(ctx, svc.projectStore, project, batchSize, progressFn, 0)
	if err != nil {
		return nil, fmt.Errorf("service: embed backfill: project store: %w", err)
	}

	// Process the global store — use empty project to cover all global memories.
	globalResult, err := svc.backfillStore(ctx, svc.globalStore, "", batchSize, progressFn, projectResult.Total)
	if err != nil {
		return nil, fmt.Errorf("service: embed backfill: global store: %w", err)
	}

	return &EmbedBackfillResult{
		Total:    projectResult.Total + globalResult.Total,
		Embedded: projectResult.Embedded + globalResult.Embedded,
		Failed:   projectResult.Failed + globalResult.Failed,
	}, nil
}

// backfillStore generates embeddings for memories without one in the given store.
// offset is the count of already-processed memories (from previous stores) so
// the progressFn reports consistent global indices.
func (svc *MemoryService) backfillStore(ctx context.Context, s *store.MemoryStore, project string, batchSize int, progressFn func(string, int, int), offset int) (*EmbedBackfillResult, error) {
	memories, err := s.ListMemoriesWithoutEmbedding(ctx, project, 0)
	if err != nil {
		return nil, err
	}

	result := &EmbedBackfillResult{Total: len(memories)}
	total := offset + len(memories)

	for i, m := range memories {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		vec := svc.embedder.Embed(m.Title + " " + m.Content)
		if len(vec) == 0 {
			result.Failed++
			continue
		}

		emb := &model.Embedding{
			MemoryID:   m.ID,
			Vector:     vec,
			Model:      svc.embedder.Model(),
			Dimensions: svc.embedder.Dimensions(),
			CreatedAt:  time.Now().UTC(),
		}
		if saveErr := s.SaveEmbedding(ctx, emb); saveErr != nil {
			log.Printf("service: backfill: embed memory %s: %v", m.ID, saveErr)
			result.Failed++
			continue
		}

		result.Embedded++

		if progressFn != nil {
			progressFn(m.Title, offset+i+1, total)
		}

	}

	return result, nil
}

// embedMemory generates an embedding for m and persists it to targetStore.
// Failures are logged and suppressed — embedding is always best-effort.
// This method is a no-op when the embedder is NopEmbedder.
func (svc *MemoryService) embedMemory(ctx context.Context, targetStore *store.MemoryStore, m *model.Memory) {
	vec := svc.embedder.Embed(m.Title + " " + m.Content)
	if len(vec) == 0 {
		return
	}
	emb := &model.Embedding{
		MemoryID:   m.ID,
		Vector:     vec,
		Model:      svc.embedder.Model(),
		Dimensions: svc.embedder.Dimensions(),
		CreatedAt:  time.Now().UTC(),
	}
	if err := targetStore.SaveEmbedding(ctx, emb); err != nil {
		log.Printf("service: embed memory %s: %v", m.ID, err)
	}
}

// processWikilinks parses wikilinks from the memory's content and creates
// reference relations to resolved targets. It is called synchronously at the
// end of Save and Update. Individual link failures are best-effort: a single
// unresolvable link never blocks the overall operation.
//
// No-op when: WikilinksEnabled=false, content is empty, or type is
// TypeSessionSummary (session summaries are high-churn synthetic content).
func (svc *MemoryService) processWikilinks(ctx context.Context, m *model.Memory, primaryStore *store.MemoryStore) {
	if !svc.config.Graph.WikilinksEnabled {
		return
	}
	if m.Content == "" || m.Type == model.TypeSessionSummary {
		return
	}

	links := wikilink.Parse(m.Content)
	if len(links) == 0 {
		return
	}

	slog.DebugContext(ctx, "wikilinks_parsed",
		"memory_id", m.ID,
		"topic_key", m.TopicKey,
		"count", len(links),
	)

	project := m.Project
	for _, link := range links {
		svc.resolveWikilink(ctx, m, link, primaryStore, project)
	}
}

// resolveWikilink attempts to resolve a single wikilink to a target memory and
// create a references relation. Self-loops, cross-scope violations, and
// unresolved targets are silently skipped. Unresolved targets are logged at
// debug level so the caller can distinguish from errors.
func (svc *MemoryService) resolveWikilink(
	ctx context.Context,
	source *model.Memory,
	link wikilink.Link,
	primaryStore *store.MemoryStore,
	project string,
) {
	// D6: self-loop guard.
	if source.TopicKey != "" && link.Topic == source.TopicKey {
		slog.DebugContext(ctx, "wikilink_self_loop_skipped",
			"memory_id", source.ID,
			"topic_key", link.Topic,
		)
		return
	}

	// D5: resolve topic_key to a target memory, scope-aware.
	var target *model.Memory
	var err error

	target, err = primaryStore.GetByTopicKey(ctx, link.Topic, project)
	if err != nil {
		slog.ErrorContext(ctx, "wikilink_resolve_error",
			"memory_id", source.ID,
			"topic_key", link.Topic,
			"error", err,
		)
		return
	}
	if target == nil && source.Scope == model.ScopeProject {
		// Fallback: project memories may reference global-scoped memories stored
		// in globalStore. Global memories are stored with the project slug of the
		// agent that created them, so we pass the same project to GetByTopicKey.
		target, err = svc.globalStore.GetByTopicKey(ctx, link.Topic, project)
		if err != nil {
			slog.ErrorContext(ctx, "wikilink_resolve_global_error",
				"memory_id", source.ID,
				"topic_key", link.Topic,
				"error", err,
			)
			return
		}
	}

	if target == nil {
		// SPEC-012: unresolved target — log at debug and register the gap for
		// later auto-resolution when a memory with that topic_key is saved.
		slog.DebugContext(ctx, "wikilink_unresolved",
			"memory_id", source.ID,
			"topic_key", link.Topic,
		)
		svc.registerUnresolved(ctx, source, link.Topic, primaryStore, project)
		return
	}

	// D5 cross-scope guard: a global or org source must not create relations into
	// a project-scoped target (same invariant as Hebbian SPEC-006 D1). A
	// project-scoped source may reference a global target (the fallback path
	// above), because the project store contains the source and can hold a
	// mirrored entity for the global target.
	if (source.Scope == model.ScopeGlobal || source.Scope == model.ScopeOrg) &&
		target.Scope == model.ScopeProject {
		slog.DebugContext(ctx, "wikilink_cross_scope_skipped",
			"source_id", source.ID,
			"target_id", target.ID,
			"source_scope", source.Scope,
			"target_scope", target.Scope,
		)
		return
	}

	// Entity and relation operations always use primaryStore (the store of the
	// source memory). When the target was found via globalStore fallback, we
	// mirror its entity name in primaryStore so both entities and the relation
	// live in the same SQLite database — cross-DB foreign keys are not possible.
	svc.createWikilinkRelation(ctx, source, target, primaryStore, project, link.Anchor)
}

// createWikilinkRelation creates entities for source and target (or reuses
// existing ones) and upserts a references relation between them. It is shared
// by resolveWikilink (live resolution) and autoResolveUnresolved (deferred
// resolution). The anchor parameter is stored as JSON metadata on the relation
// when non-empty; pass an empty string when the link has no anchor.
//
// All operations are best-effort: failures are logged and never propagated to
// the caller. Idempotency is ensured by FindRelationBidirectional before
// CreateRelation.
func (svc *MemoryService) createWikilinkRelation(
	ctx context.Context,
	source, target *model.Memory,
	entityStore *store.MemoryStore,
	project, anchor string,
) {
	// D7: FindOrCreateEntity for source.
	sourceName := source.TopicKey
	if sourceName == "" {
		sourceName = source.ID
	}
	srcEntity, err := entityStore.FindOrCreateEntity(ctx, sourceName, model.KindConcept, project)
	if err != nil {
		slog.ErrorContext(ctx, "wikilink_src_entity_error",
			"memory_id", source.ID,
			"error", err,
		)
		return
	}

	// D7: FindOrCreateEntity for target. When the target was resolved via the
	// global-store fallback, its project field is empty. We use the source's
	// project so the entity lives in the same namespace as all project entities,
	// which is required since entityStore is always primaryStore.
	tgtName := target.TopicKey
	if tgtName == "" {
		tgtName = target.ID
	}
	// For global/org targets mirrored in primaryStore, use the source's project
	// so the entity is co-located with source entities in the same DB namespace.
	// For project-scoped targets, use the target's own project field.
	tgtProject := project
	if target.Scope != model.ScopeGlobal && target.Scope != model.ScopeOrg {
		tgtProject = target.Project
	}
	tgtEntity, err := entityStore.FindOrCreateEntity(ctx, tgtName, model.KindConcept, tgtProject)
	if err != nil {
		slog.ErrorContext(ctx, "wikilink_tgt_entity_error",
			"memory_id", target.ID,
			"error", err,
		)
		return
	}

	// D7: idempotency — check if relation already exists in either direction.
	existing, err := entityStore.FindRelationBidirectional(ctx, srcEntity.ID, tgtEntity.ID, model.RelReferences)
	if err != nil {
		slog.ErrorContext(ctx, "wikilink_find_relation_error",
			"src_entity", srcEntity.ID,
			"tgt_entity", tgtEntity.ID,
			"error", err,
		)
		return
	}

	if existing != nil {
		// Relation exists — touch to refresh last_traversed_at (protects from edge decay).
		if touchErr := entityStore.TouchRelation(ctx, existing.ID, time.Now().UTC()); touchErr != nil {
			slog.ErrorContext(ctx, "wikilink_touch_relation_error",
				"relation_id", existing.ID,
				"error", touchErr,
			)
		}
		return
	}

	// D8: build anchor metadata JSON when the wikilink includes an anchor.
	var metadata string
	if anchor != "" {
		anchorJSON, marshalErr := json.Marshal(map[string]string{"anchor": anchor})
		if marshalErr == nil {
			metadata = string(anchorJSON)
		}
	}

	// Create the references relation with the configured weight.
	rel := &model.Relation{
		SourceID: srcEntity.ID,
		TargetID: tgtEntity.ID,
		Type:     model.RelReferences,
		Weight:   svc.config.Graph.WikilinkRelationWeight,
		Metadata: metadata,
	}
	if _, createErr := entityStore.CreateRelation(ctx, rel); createErr != nil {
		// Best-effort: log and continue. Concurrent saves may cause UNIQUE constraint
		// violations (D5.9), which are harmless.
		slog.DebugContext(ctx, "wikilink_create_relation_error",
			"src_entity", srcEntity.ID,
			"tgt_entity", tgtEntity.ID,
			"error", createErr,
		)
		return
	}

	// Link both memories to their respective entities so the graph is navigable
	// from either direction.
	if linkErr := entityStore.LinkMemoryEntity(ctx, source.ID, srcEntity.ID, "mention"); linkErr != nil {
		slog.DebugContext(ctx, "wikilink_link_src_entity_error", "error", linkErr)
	}
	if linkErr := entityStore.LinkMemoryEntity(ctx, target.ID, tgtEntity.ID, "mention"); linkErr != nil {
		slog.DebugContext(ctx, "wikilink_link_tgt_entity_error", "error", linkErr)
	}

	slog.DebugContext(ctx, "wikilink_relation_created",
		"source_id", source.ID,
		"target_id", target.ID,
		"weight", svc.config.Graph.WikilinkRelationWeight,
	)
}

// registerUnresolved persists an unresolved wikilink reference to the store.
// Best-effort: failures are logged and never returned to the caller so that a
// store failure never blocks the parent Save or Update operation.
func (svc *MemoryService) registerUnresolved(
	ctx context.Context,
	source *model.Memory,
	targetTopicKey string,
	primaryStore *store.MemoryStore,
	project string,
) {
	ref := &model.UnresolvedReference{
		SourceMemoryID: source.ID,
		TargetTopicKey: targetTopicKey,
		Project:        project,
	}
	if err := primaryStore.RegisterUnresolved(ctx, ref); err != nil {
		slog.ErrorContext(ctx, "wikilink_register_unresolved_error",
			"source_id", source.ID,
			"target_topic_key", targetTopicKey,
			"error", err,
		)
	}
}

// autoResolveUnresolved resolves all pending unresolved_references that point to
// the topic_key of the newly saved memory m. For each such reference it loads
// the source memory, checks cross-scope constraints, creates the wikilink
// relation, and deletes the now-resolved row.
//
// This runs after processWikilinks in Save so that the new memory is already
// committed before FindUnresolvedByTarget queries for it. Best-effort: partial
// failures are logged but do not block the caller. SQLite's serial write model
// prevents data races — a concurrent save that processes the same ref first will
// leave zero rows for the second resolver to find.
//
// No-op conditions (zero queries issued):
//   - m.TopicKey == "" (memories without a topic key cannot be wikilink targets)
//   - m.Type == TypeSessionSummary (excluded from the knowledge graph)
func (svc *MemoryService) autoResolveUnresolved(
	ctx context.Context,
	m *model.Memory,
	primaryStore *store.MemoryStore,
) {
	if m.TopicKey == "" || m.Type == model.TypeSessionSummary {
		return
	}

	refs, err := primaryStore.FindUnresolvedByTarget(ctx, m.TopicKey, m.Project)
	if err != nil {
		slog.ErrorContext(ctx, "auto_resolve_query_error",
			"topic_key", m.TopicKey,
			"error", err,
		)
		return
	}
	if len(refs) == 0 {
		return
	}

	resolved := 0
	for _, ref := range refs {
		source, getErr := primaryStore.Get(ctx, ref.SourceMemoryID)
		if getErr != nil || source == nil {
			// Source was deleted or inaccessible. Clean up the stale ref.
			_ = primaryStore.DeleteUnresolved(ctx, ref.ID)
			continue
		}

		// Cross-scope guard (SPEC-011 D5 / SPEC-012 D6): a global or org source
		// must not create a relation into a project-scoped target.
		if (source.Scope == model.ScopeGlobal || source.Scope == model.ScopeOrg) &&
			m.Scope == model.ScopeProject {
			continue
		}

		// Create the relation using the shared helper. Anchor is empty for
		// auto-resolved refs — the original anchor is not stored in the
		// unresolved_references table (not needed for graph connectivity).
		svc.createWikilinkRelation(ctx, source, m, primaryStore, ref.Project, "")

		if delErr := primaryStore.DeleteUnresolved(ctx, ref.ID); delErr != nil {
			slog.ErrorContext(ctx, "auto_resolve_delete_error",
				"ref_id", ref.ID,
				"error", delErr,
			)
		}
		resolved++
	}

	slog.DebugContext(ctx, "auto_resolve_completed",
		"topic_key", m.TopicKey,
		"candidates", len(refs),
		"resolved", resolved,
	)
}
