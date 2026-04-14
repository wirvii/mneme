// Package service implements the business logic layer for mneme. It orchestrates
// operations across the store, scoring, and project packages to fulfill memory
// management requests from the CLI and MCP interfaces. Service methods validate
// inputs, apply business rules (importance scoring, upsert logic, access tracking),
// and return domain-typed responses.
package service

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/embed"
	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/scoring"
	"github.com/juanftp/mneme/internal/store"
	syncpkg "github.com/juanftp/mneme/internal/sync"
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
type MemoryService struct {
	projectStore *store.MemoryStore // for project-scoped memories
	globalStore  *store.MemoryStore // for global/org-scoped memories
	config       *config.Config
	project      string        // detected or configured project slug
	embedder     embed.Embedder // generates vector representations for semantic search
}

// NewMemoryService constructs a MemoryService. The caller must provide fully
// initialised MemoryStores and Config. projectStore is used for project-scoped
// memories and globalStore for global/org-scoped memories. project is the
// default project slug used when individual requests omit the Project field —
// typically the slug detected from the working directory's git remote.
//
// embedder provides the text embedding strategy. Pass embed.NopEmbedder{} to
// disable embeddings and fall back to FTS5-only retrieval.
func NewMemoryService(projectStore, globalStore *store.MemoryStore, cfg *config.Config, project string, embedder embed.Embedder) *MemoryService {
	return &MemoryService{
		projectStore: projectStore,
		globalStore:  globalStore,
		config:       cfg,
		project:      project,
		embedder:     embedder,
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

	return m, nil
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
	}, nil
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

		// Process in batches to avoid holding the connection for too long.
		if (i+1)%batchSize == 0 {
			// Yield briefly between batches; the context check above handles cancellation.
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
