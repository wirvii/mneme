package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/juanftp/mneme/internal/model"
)

// Relate creates a relationship between two named entities in the knowledge graph.
// The source and target entities are resolved by name within the given project,
// and created with the supplied kinds when they do not exist yet. If an identical
// relation (same source, target, and type) already exists, the existing relation
// is returned with Created=false.
//
// Validation rules (applied in order):
//   - Source name must not be empty
//   - Target name must not be empty
//   - Relation type must not be empty and must be a recognised RelationType
//   - SourceKind defaults to KindConcept when omitted
//   - TargetKind defaults to KindConcept when omitted
//   - Project defaults to the service's project when omitted
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

	if req.SourceKind == "" {
		req.SourceKind = model.KindConcept
	}
	if req.TargetKind == "" {
		req.TargetKind = model.KindConcept
	}
	if req.Project == "" {
		req.Project = svc.project
	}

	// Resolve or create both entities using the project store.
	srcEntity, err := svc.projectStore.FindOrCreateEntity(ctx, req.Source, req.SourceKind, req.Project)
	if err != nil {
		return nil, fmt.Errorf("service: relate: resolve source entity: %w", err)
	}

	tgtEntity, err := svc.projectStore.FindOrCreateEntity(ctx, req.Target, req.TargetKind, req.Project)
	if err != nil {
		return nil, fmt.Errorf("service: relate: resolve target entity: %w", err)
	}

	// Check whether the relation already exists.
	existing, err := svc.projectStore.FindRelation(ctx, srcEntity.ID, tgtEntity.ID, req.Relation)
	if err != nil {
		return nil, fmt.Errorf("service: relate: check existing relation: %w", err)
	}
	if existing != nil {
		return &model.RelateResponse{
			RelationID: existing.ID,
			SourceID:   srcEntity.ID,
			TargetID:   tgtEntity.ID,
			Created:    false,
		}, nil
	}

	// Create the new relation.
	rel := &model.Relation{
		SourceID: srcEntity.ID,
		TargetID: tgtEntity.ID,
		Type:     req.Relation,
		Weight:   1.0,
	}
	created, err := svc.projectStore.CreateRelation(ctx, rel)
	if err != nil {
		return nil, fmt.Errorf("service: relate: create relation: %w", err)
	}

	return &model.RelateResponse{
		RelationID: created.ID,
		SourceID:   srcEntity.ID,
		TargetID:   tgtEntity.ID,
		Created:    true,
	}, nil
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
func makeTimelinePreview(content string) string {
	const maxLen = 200
	runes := []rune(content)
	if len(runes) <= maxLen {
		return content
	}
	return string(runes[:maxLen]) + "..."
}
