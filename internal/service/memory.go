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

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/scoring"
	"github.com/juanftp/mneme/internal/store"
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
	project      string // detected or configured project slug
}

// NewMemoryService constructs a MemoryService. The caller must provide fully
// initialised MemoryStores and Config. projectStore is used for project-scoped
// memories and globalStore for global/org-scoped memories. project is the
// default project slug used when individual requests omit the Project field —
// typically the slug detected from the working directory's git remote.
func NewMemoryService(projectStore, globalStore *store.MemoryStore, cfg *config.Config, project string) *MemoryService {
	return &MemoryService{
		projectStore: projectStore,
		globalStore:  globalStore,
		config:       cfg,
		project:      project,
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

	result, created, err := svc.storeFor(m.Scope).Upsert(ctx, m)
	if err != nil {
		return nil, fmt.Errorf("service: save: %w", err)
	}

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
