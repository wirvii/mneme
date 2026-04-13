package service

import (
	"context"
	"fmt"
	"sort"
	"unicode/utf8"

	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/scoring"
	"github.com/juanftp/mneme/internal/store"
)

// Context assembles a curated memory bundle for an agent at session start.
// It lists active project memories, optionally mixes in global memories, boosts
// memories that match a focus query, and packs as many as possible into the token
// budget. The session summary memory is always included first, exempt from the
// budget limit.
//
// Budget defaults to config.Context.DefaultBudget when zero or negative.
// Project defaults to the service's project when omitted.
func (svc *MemoryService) Context(ctx context.Context, req model.ContextRequest) (*model.ContextResponse, error) {
	if req.Project == "" {
		req.Project = svc.project
	}
	budget := req.Budget
	if budget <= 0 {
		budget = svc.config.Context.DefaultBudget
	}

	// Collect project-scoped memories ordered by importance DESC.
	projectMemories, err := svc.store.List(ctx, store.ListOptions{
		Project: req.Project,
		Scope:   model.ScopeProject,
		OrderBy: "importance DESC",
		Limit:   svc.config.Storage.ProjectBudget,
	})
	if err != nil {
		return nil, fmt.Errorf("service: context: list project memories: %w", err)
	}

	candidates := make([]*model.Memory, 0, len(projectMemories))
	candidates = append(candidates, projectMemories...)

	// Optionally mix in global memories that exceed the minimum importance threshold.
	if svc.config.Context.IncludeGlobal {
		globalMemories, err := svc.store.List(ctx, store.ListOptions{
			Scope:   model.ScopeGlobal,
			OrderBy: "importance DESC",
			Limit:   svc.config.Storage.GlobalBudget,
		})
		if err != nil {
			return nil, fmt.Errorf("service: context: list global memories: %w", err)
		}
		for _, m := range globalMemories {
			if m.Importance >= svc.config.Context.GlobalMinImportance {
				candidates = append(candidates, m)
			}
		}
	}

	totalAvailable := len(candidates)

	// Build a focus boost set when a focus query is provided.
	focusIDs := make(map[string]bool)
	if req.Focus != "" {
		focusResults, err := svc.store.FTS5Search(ctx, req.Focus, store.SearchOptions{
			Project: req.Project,
			Limit:   20,
		})
		if err != nil {
			// Focus search failure is non-fatal; degrade gracefully.
			focusResults = nil
		}
		for _, r := range focusResults {
			focusIDs[r.Memory.ID] = true
		}
	}

	// Score each candidate using effective importance (decay applied) plus focus
	// boost and architecture type boost.
	type scored struct {
		mem   *model.Memory
		score float64
	}

	scoredCandidates := make([]scored, 0, len(candidates))
	for _, m := range candidates {
		lastAccessed := m.CreatedAt
		if m.LastAccessed != nil {
			lastAccessed = *m.LastAccessed
		}
		eff := scoring.EffectiveImportance(m.Importance, m.DecayRate, lastAccessed)

		// Architecture memories get a 1.5x multiplier so they appear near the top
		// even when their raw importance has decayed.
		if m.Type == model.TypeArchitecture {
			eff *= 1.5
		}

		// Focus-matched memories get a +0.3 additive boost.
		if focusIDs[m.ID] {
			eff += 0.3
		}

		scoredCandidates = append(scoredCandidates, scored{mem: m, score: eff})
	}

	sort.Slice(scoredCandidates, func(i, j int) bool {
		return scoredCandidates[i].score > scoredCandidates[j].score
	})

	// Retrieve the last session summary — included first, exempt from budget.
	lastSess, err := svc.store.GetLastSession(ctx, req.Project)
	if err != nil {
		return nil, fmt.Errorf("service: context: get last session: %w", err)
	}

	var lastSession *model.SessionSummary
	if lastSess != nil && lastSess.SummaryID != "" {
		summaryMem, err := svc.store.Get(ctx, lastSess.SummaryID)
		if err == nil && summaryMem != nil {
			lastSession = &model.SessionSummary{
				ID:      summaryMem.ID,
				Summary: summaryMem.Content,
				EndedAt: lastSess.EndedAt,
			}
		}
	}

	// Pack memories into the budget, starting with non-session-summary records.
	// Session summaries are excluded from the packed list because the last session
	// is already surfaced via LastSession; packing it again would waste budget.
	tokenBudget := budget
	if lastSession != nil {
		// Deduct the session summary from the budget estimate so callers can rely on
		// TokenEstimate as an accurate total.
		tokenBudget -= estimateTokens(lastSession.Summary)
		if tokenBudget < 0 {
			tokenBudget = 0
		}
	}

	packed := make([]model.Memory, 0, len(scoredCandidates))
	tokenUsed := 0

	for _, sc := range scoredCandidates {
		if sc.mem.Type == model.TypeSessionSummary {
			// Handled via LastSession; skip to avoid duplication.
			continue
		}
		cost := estimateTokens(sc.mem.Title) + estimateTokens(sc.mem.Content)
		if tokenUsed+cost > tokenBudget {
			break
		}
		packed = append(packed, *sc.mem)
		tokenUsed += cost
	}

	totalTokens := tokenUsed
	if lastSession != nil {
		totalTokens += estimateTokens(lastSession.Summary)
	}

	return &model.ContextResponse{
		Project:        req.Project,
		Memories:       packed,
		TokenEstimate:  totalTokens,
		TotalAvailable: totalAvailable,
		Included:       len(packed),
		LastSession:    lastSession,
	}, nil
}

// estimateTokens returns a rough token count for the given text using the
// approximation of 1 token per 3 characters (valid for typical English/Spanish
// prose and Markdown). This avoids a dependency on a tokeniser library while
// giving a conservative-enough estimate for budget calculations.
func estimateTokens(text string) int {
	return int(float64(utf8.RuneCountInString(text)) / 3.0)
}
