package service

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/wirvii/mneme/internal/model"
)

// Relate creates a relationship between two graph endpoints in the knowledge
// graph. SPEC-031 introduces hybrid resolution: each of Source/Target is treated
// as either a memory reference (UUID full, UUID prefix, or topic_key) or as an
// entity name, in that order. When resolution lands on a memory, the memory is
// linked to the proxy entity via memory_entities so the relation is reachable
// from mem_explore. Legacy callers passing an explicit non-default kind (e.g.
// source_kind="service") preserve the original entity-only semantics.
//
// Validation rules (applied in order):
//   - Source name must not be empty
//   - Target name must not be empty
//   - Relation type must not be empty and must be a recognised RelationType
//   - SourceKind defaults to KindConcept when omitted
//   - TargetKind defaults to KindConcept when omitted
//   - Project defaults to the service's project when omitted
//
// Cross-scope guard: when both endpoints resolve to memories and the source is
// global/org while the target is project-scoped, ErrCrossScopeRelation is
// returned (mirrors the wikilink invariant, SPEC-006 D1).
func (svc *MemoryService) Relate(ctx context.Context, req model.RelateRequest) (*model.RelateResponse, error) {
	if req.Source == "" {
		return nil, fmt.Errorf("service: relate: source is required")
	}
	if req.Target == "" {
		return nil, fmt.Errorf("service: relate: target is required")
	}
	if req.Relation == "" {
		return nil, fmt.Errorf("service: relate: %w", model.ErrInvalidRelationType)
	}
	if !req.Relation.Valid() {
		return nil, fmt.Errorf("service: relate: %w", model.ErrInvalidRelationType)
	}

	// Validate weight when explicitly provided (zero is treated as "use default").
	if req.Weight != 0 {
		if math.IsNaN(req.Weight) || math.IsInf(req.Weight, 0) || req.Weight < 0 || req.Weight > 1 {
			return nil, fmt.Errorf("service: relate: %w", model.ErrInvalidWeight)
		}
	}

	if req.SourceKind == "" {
		req.SourceKind = model.KindConcept
	}
	if req.TargetKind == "" {
		req.TargetKind = model.KindConcept
	}
	if req.Project == "" {
		req.Project = svc.project
	}

	srcRes, err := svc.resolveRelateEndpoint(ctx, req.Source, req.SourceKind, req.Project)
	if err != nil {
		return nil, fmt.Errorf("service: relate: resolve source: %w", err)
	}

	tgtRes, err := svc.resolveRelateEndpoint(ctx, req.Target, req.TargetKind, req.Project)
	if err != nil {
		return nil, fmt.Errorf("service: relate: resolve target: %w", err)
	}

	// Cross-scope guard mirrors SPEC-006 D1 / wikilinks: a global or org source
	// memory must not create relations into a project-scoped target memory.
	if srcRes.memory != nil && tgtRes.memory != nil {
		if (srcRes.memory.Scope == model.ScopeGlobal || srcRes.memory.Scope == model.ScopeOrg) &&
			tgtRes.memory.Scope == model.ScopeProject {
			return nil, fmt.Errorf("service: relate: %w", model.ErrCrossScopeRelation)
		}
	}

	// Check whether the relation already exists.
	existing, err := svc.projectStore.FindRelation(ctx, srcRes.entity.ID, tgtRes.entity.ID, req.Relation)
	if err != nil {
		return nil, fmt.Errorf("service: relate: check existing relation: %w", err)
	}
	if existing != nil {
		return &model.RelateResponse{
			RelationID: existing.ID,
			SourceID:   srcRes.entity.ID,
			TargetID:   tgtRes.entity.ID,
			Created:    false,
			Weight:     existing.Weight,
		}, nil
	}

	// Resolve weight: explicit caller value takes priority; otherwise use the
	// type-specific default. The store also applies DefaultWeight when Weight==0,
	// but we set it here so the response reflects the chosen value.
	weight := req.Weight
	if weight == 0 {
		weight = model.DefaultWeight(req.Relation)
	}

	// Create the new relation.
	rel := &model.Relation{
		SourceID: srcRes.entity.ID,
		TargetID: tgtRes.entity.ID,
		Type:     req.Relation,
		Weight:   weight,
	}
	created, err := svc.projectStore.CreateRelation(ctx, rel)
	if err != nil {
		return nil, fmt.Errorf("service: relate: create relation: %w", err)
	}

	return &model.RelateResponse{
		RelationID: created.ID,
		SourceID:   srcRes.entity.ID,
		TargetID:   tgtRes.entity.ID,
		Created:    true,
		Weight:     created.Weight,
	}, nil
}

// relateResolution captures the outcome of resolving a Relate endpoint string.
// entity is always non-nil on success and is the proxy entity used by the
// relation. memory is non-nil only when the string resolved to an existing
// memory (UUID, UUID prefix, or topic_key); in that case memory_entities has
// been linked between memory and entity (idempotently).
type relateResolution struct {
	entity *model.Entity
	memory *model.Memory
}

// resolveRelateEndpoint maps a Relate source/target string to a proxy entity,
// optionally also identifying the memory it represents. The lookup order is:
//
//  1. UUID (full or 8+ hex prefix) → memory in either store.
//  2. If kind == KindConcept (i.e. caller did not specify a non-default kind):
//     topic_key → memory in project store, then global store.
//  3. Entity by name in the project's entity table (creates with kind when
//     missing).
//
// When the lookup ends in a memory, FindOrCreateEntity creates a proxy entity
// named after the memory's TopicKey (or ID if no topic_key) and LinkMemoryEntity
// is called to ensure the puente row exists in memory_entities. INSERT OR
// IGNORE makes the link idempotent across repeat calls.
func (svc *MemoryService) resolveRelateEndpoint(ctx context.Context, name string, kind model.EntityKind, project string) (*relateResolution, error) {
	// 1. UUID full or prefix → memory.
	if mem := svc.tryResolveMemoryByID(ctx, name); mem != nil {
		entity, err := svc.linkMemoryAsProxy(ctx, mem, project)
		if err != nil {
			return nil, err
		}
		return &relateResolution{entity: entity, memory: mem}, nil
	}

	// 2. topic_key → memory (only when kind is the default KindConcept).
	if kind == model.KindConcept {
		mem, err := svc.tryResolveMemoryByTopicKey(ctx, name, project)
		if err != nil {
			return nil, err
		}
		if mem != nil {
			entity, linkErr := svc.linkMemoryAsProxy(ctx, mem, project)
			if linkErr != nil {
				return nil, linkErr
			}
			return &relateResolution{entity: entity, memory: mem}, nil
		}
	}

	// 3. Entity by name (or create with kind).
	entity, err := svc.projectStore.FindOrCreateEntity(ctx, name, kind, project)
	if err != nil {
		return nil, fmt.Errorf("find or create entity %q: %w", name, err)
	}
	return &relateResolution{entity: entity}, nil
}

// tryResolveMemoryByID attempts to interpret name as a memory UUID (full or
// 8+ hex prefix) and looks it up in either store. Returns nil silently when
// the name is not a UUID or no memory matches.
func (svc *MemoryService) tryResolveMemoryByID(ctx context.Context, name string) *model.Memory {
	if looksLikeUUID(name) {
		mem, _, err := svc.getFromEitherStore(ctx, name)
		if err != nil {
			slog.DebugContext(ctx, "relate_resolve_uuid_error", "name", name, "error", err)
			return nil
		}
		return mem
	}
	if len(name) >= 8 && len(name) < 36 && isAllHex(name) {
		mem, err := svc.projectStore.GetByIDPrefix(ctx, name)
		if err == nil && mem != nil {
			return mem
		}
		mem, err = svc.globalStore.GetByIDPrefix(ctx, name)
		if err == nil && mem != nil {
			return mem
		}
	}
	return nil
}

// tryResolveMemoryByTopicKey looks up name as a topic_key in the project store
// and falls back to the global store. Returns nil when no match is found.
// Errors from the underlying store are propagated.
func (svc *MemoryService) tryResolveMemoryByTopicKey(ctx context.Context, name, project string) (*model.Memory, error) {
	mem, err := svc.projectStore.GetByTopicKey(ctx, name, project)
	if err != nil {
		return nil, fmt.Errorf("topic_key lookup (project): %w", err)
	}
	if mem != nil {
		return mem, nil
	}
	mem, err = svc.globalStore.GetByTopicKey(ctx, name, project)
	if err != nil {
		return nil, fmt.Errorf("topic_key lookup (global): %w", err)
	}
	if mem != nil {
		return mem, nil
	}
	// Some legacy global memories were stored with an empty project field —
	// retry with empty project for safety.
	mem, err = svc.globalStore.GetByTopicKey(ctx, name, "")
	if err != nil {
		return nil, fmt.Errorf("topic_key lookup (global empty proj): %w", err)
	}
	return mem, nil
}

// linkMemoryAsProxy ensures a proxy entity exists for the given memory and that
// memory_entities contains the puente row connecting them. The proxy entity is
// always created in the project's entity namespace (the project parameter, not
// the memory's project) so cross-scope memories share the same SQLite database
// as the source — entities live alongside the source memory, not the target.
func (svc *MemoryService) linkMemoryAsProxy(ctx context.Context, mem *model.Memory, project string) (*model.Entity, error) {
	proxyName := mem.TopicKey
	if proxyName == "" {
		proxyName = mem.ID
	}
	entity, err := svc.projectStore.FindOrCreateEntity(ctx, proxyName, model.KindConcept, project)
	if err != nil {
		return nil, fmt.Errorf("proxy entity for memory %s: %w", mem.ID, err)
	}
	if linkErr := svc.projectStore.LinkMemoryEntity(ctx, mem.ID, entity.ID, "relate"); linkErr != nil {
		// Best-effort: the link is INSERT OR IGNORE so duplicates are harmless.
		// A real error here (e.g. FOREIGN KEY when memory not in projectStore)
		// is logged but does not abort relate.
		slog.DebugContext(ctx, "relate_link_memory_entity_error",
			"memory_id", mem.ID,
			"entity_id", entity.ID,
			"error", linkErr,
		)
	}
	return entity, nil
}

// UpdateRelationWeight adjusts the weight of an existing relation by delta,
// clamping the result to [0.0, 1.0]. It also updates last_traversed_at to now.
// Returns the relation after the update. This is the primary API for Hebbian
// auto-strengthening (SPEC-G2); agents should call this after co-accessing two
// memories to reinforce the edge between them.
func (svc *MemoryService) UpdateRelationWeight(ctx context.Context, relationID string, delta float64) (*model.Relation, error) {
	if math.IsNaN(delta) || math.IsInf(delta, 0) {
		return nil, fmt.Errorf("service: update relation weight: delta must be a finite number")
	}
	return svc.projectStore.UpdateRelationWeight(ctx, relationID, delta, time.Now().UTC())
}

// Timeline returns memories ordered chronologically around a specific point in
// time. The anchor point is either a memory UUID (the memory's created_at is
// used) or an ISO 8601 timestamp string. The window parameter controls how wide
// the search range is (default "7d"); memories within ±window/2 of the anchor
// are returned.
//
// The result is packaged as a SearchResponse for uniformity with other memory
// retrieval endpoints.
func (svc *MemoryService) Timeline(ctx context.Context, req model.TimelineRequest) (*model.SearchResponse, error) {
	if req.Around == "" {
		return nil, fmt.Errorf("service: timeline: around is required")
	}

	anchor, err := svc.resolveAnchor(ctx, req.Around)
	if err != nil {
		return nil, fmt.Errorf("service: timeline: resolve anchor: %w", err)
	}

	window, err := parseWindow(req.Window)
	if err != nil {
		return nil, fmt.Errorf("service: timeline: %w", err)
	}

	half := window / 2
	from := anchor.Add(-half)
	to := anchor.Add(half)

	project := req.Project
	if project == "" {
		project = svc.project
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}

	memories, err := svc.projectStore.ListMemoriesInRange(ctx, from, to, project, limit)
	if err != nil {
		return nil, fmt.Errorf("service: timeline: %w", err)
	}

	results := make([]model.SearchResult, 0, len(memories))
	for _, m := range memories {
		results = append(results, model.SearchResult{
			Memory:         m,
			Preview:        makeTimelinePreview(m.Content),
			RelevanceScore: 1.0,
			BM25Score:      0,
		})
	}

	return &model.SearchResponse{
		Results: results,
		Total:   len(results),
		Query:   req.Around,
	}, nil
}

// resolveAnchor resolves the "around" field of a TimelineRequest into a time.Time.
// If around looks like a UUID it fetches the memory's created_at; otherwise it
// attempts to parse it as an ISO 8601 / RFC3339 timestamp.
func (svc *MemoryService) resolveAnchor(ctx context.Context, around string) (time.Time, error) {
	// Heuristic: UUID v7 strings are 36 characters with hyphens in specific positions.
	if looksLikeUUID(around) {
		m, _, err := svc.getFromEitherStore(ctx, around)
		if err != nil {
			return time.Time{}, fmt.Errorf("lookup memory %q: %w", around, err)
		}
		if m == nil {
			return time.Time{}, fmt.Errorf("memory %q: %w", around, model.ErrNotFound)
		}
		return m.CreatedAt, nil
	}

	// Try ISO 8601 / RFC3339 formats.
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, around); err == nil {
			return t.UTC(), nil
		}
	}

	return time.Time{}, fmt.Errorf("cannot parse %q as a memory ID or timestamp", around)
}

// looksLikeUUID returns true when s has the standard UUID shape (8-4-4-4-12 hex
// groups separated by hyphens), which is the format used for UUIDv7 IDs.
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	dashes := [4]int{8, 13, 18, 23}
	for _, pos := range dashes {
		if s[pos] != '-' {
			return false
		}
	}
	for i, ch := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		if !isHexRune(ch) {
			return false
		}
	}
	return true
}

// isHexRune reports whether r is a valid hexadecimal digit.
func isHexRune(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

// parseWindow converts a human-readable duration string like "7d", "24h", "30d"
// into a time.Duration. The suffix must be one of: "d" (days), "h" (hours),
// "m" (minutes). The empty string defaults to 7 days.
func parseWindow(w string) (time.Duration, error) {
	if w == "" {
		return 7 * 24 * time.Hour, nil
	}

	w = strings.TrimSpace(w)
	if len(w) < 2 {
		return 0, fmt.Errorf("invalid window %q: must be a number followed by d/h/m", w)
	}

	suffix := w[len(w)-1:]
	numStr := w[:len(w)-1]

	n, err := strconv.ParseFloat(numStr, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid window %q: numeric part must be a positive number", w)
	}

	switch suffix {
	case "d":
		return time.Duration(n * float64(24*time.Hour)), nil
	case "h":
		return time.Duration(n * float64(time.Hour)), nil
	case "m":
		return time.Duration(n * float64(time.Minute)), nil
	default:
		return 0, fmt.Errorf("invalid window %q: suffix must be d (days), h (hours), or m (minutes)", w)
	}
}

// makeTimelinePreview returns a short excerpt of the memory content, capped at
// 200 characters. This mirrors the behaviour of makePreview in the search store.
//
// Delegates to model.Excerpt (SPEC-109 D8) so the rune-safe truncation logic
// exists in exactly one place instead of three; behaviour is unchanged. The
// 200 literal is replaced by model.ListExcerptRunes once that constant lands
// in step 4 of SPEC-109's plan.
func makeTimelinePreview(content string) string {
	const maxLen = 200
	excerpt, truncated := model.Excerpt(content, maxLen)
	if truncated {
		return excerpt + "..."
	}
	return excerpt
}
