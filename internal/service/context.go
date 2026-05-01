package service

import (
	"context"
	"fmt"
	"log/slog"
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
// Rules (type=rule) are handled in a dedicated phase before the general scoring
// loop. They use a separate rules_budget that is not competed for by other
// memories, ensuring they always appear in the context regardless of focus or
// the volume of other high-importance memories.
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

	rulesBudget := svc.config.Context.RulesBudget

	// ── PHASE 1: Rules (dedicated budget) ────────────────────────────────────
	//
	// Rules are packed before general memories. They use a separate token budget
	// so they are always injected regardless of how many other memories compete.
	// When rulesBudget == 0 this phase is skipped entirely (toggle-off).

	var packedRules []model.Memory
	var rulesTokens, rulesTruncated int
	ruleIDs := make(map[string]bool)

	if rulesBudget > 0 {
		allRules, err := svc.loadActiveRules(ctx, req.Project)
		if err != nil {
			return nil, fmt.Errorf("service: context: load active rules: %w", err)
		}

		// Sort rules by severity desc → effective importance desc → updated_at desc
		// so that block rules always appear first and highest-priority rules survive
		// budget truncation.
		sort.Slice(allRules, func(i, j int) bool {
			a, b := allRules[i], allRules[j]
			sa, sb := severityOrder(a.Severity), severityOrder(b.Severity)
			if sa != sb {
				return sa > sb
			}
			lastA := a.CreatedAt
			if a.LastAccessed != nil {
				lastA = *a.LastAccessed
			}
			lastB := b.CreatedAt
			if b.LastAccessed != nil {
				lastB = *b.LastAccessed
			}
			ea := scoring.EffectiveImportance(a.Importance, a.DecayRate, lastA)
			eb := scoring.EffectiveImportance(b.Importance, b.DecayRate, lastB)
			if ea != eb {
				return ea > eb
			}
			return a.UpdatedAt.After(b.UpdatedAt)
		})

		// Pack rules using continue (not break) so smaller rules can still fit
		// after a large rule is skipped. Rules are never partially truncated.
		for i := range allRules {
			r := allRules[i]
			cost := estimateTokens(r.Title) + estimateTokens(r.Content)
			if rulesTokens+cost > rulesBudget {
				rulesTruncated++
				continue
			}
			packedRules = append(packedRules, r)
			rulesTokens += cost
			ruleIDs[r.ID] = true
		}

		slog.Info("rules_injected",
			"event", "rules_injected",
			"project", req.Project,
			"rules_count", len(packedRules),
			"rules_tokens", rulesTokens,
			"rules_truncated", rulesTruncated,
		)
	}

	// ── PHASE 2: General scoring (unchanged from pre-SPEC-002) ───────────────

	// Collect project-scoped memories ordered by importance DESC.
	projectMemories, err := svc.projectStore.List(ctx, store.ListOptions{
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

	// Optionally mix in global memories from the dedicated global store.
	if svc.config.Context.IncludeGlobal {
		globalMemories, err := svc.globalStore.List(ctx, store.ListOptions{
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
	// Both FTS5 and vector signals contribute to the focus set so that
	// semantically related memories are boosted even when they share no
	// tokens with the focus query. The graph channel (PPR or 1-hop) further
	// augments the set with topologically related memories (SPEC-017).
	focusIDs := make(map[string]bool)
	if req.Focus != "" {
		focusOpts := store.SearchOptions{
			Project: req.Project,
			Limit:   20,
		}
		projectFocus, err := svc.projectStore.FTS5Search(ctx, req.Focus, focusOpts)
		if err != nil {
			// Focus search failure is non-fatal; degrade gracefully.
			projectFocus = nil
		}
		globalFocusOpts := focusOpts
		globalFocusOpts.Project = ""
		globalFocus, err := svc.globalStore.FTS5Search(ctx, req.Focus, globalFocusOpts)
		if err != nil {
			globalFocus = nil
		}
		for _, r := range append(projectFocus, globalFocus...) {
			focusIDs[r.ID] = true
		}

		// Augment focus set with vector similarity when embedder is active.
		// Results above the similarity threshold are treated as focus matches
		// regardless of whether they appear in the FTS5 results.
		if svc.embedder.Model() != "none" {
			focusVec := svc.embedder.Embed(req.Focus)
			if len(focusVec) > 0 {
				vOpts := store.VectorSearchOptions{
					Project: req.Project,
					Limit:   20,
				}
				projectVec, err := svc.projectStore.VectorSearch(ctx, focusVec, vOpts)
				if err == nil {
					for _, vr := range projectVec {
						if vr.Similarity > 0.3 {
							focusIDs[vr.MemoryID] = true
						}
					}
				}
				globalVOpts := vOpts
				globalVOpts.Project = ""
				globalVec, err := svc.globalStore.VectorSearch(ctx, focusVec, globalVOpts)
				if err == nil {
					for _, vr := range globalVec {
						if vr.Similarity > 0.3 {
							focusIDs[vr.MemoryID] = true
						}
					}
				}
			}
		}

		// Graph expansion for focus boost: topologically related memories receive
		// the same +0.3 additive boost as text-matched focus results. Only active
		// when the graph is enabled (ExpansionEnabled) and not explicitly disabled
		// for this request via IncludeGraph=false (D4).
		includeGraph := svc.config.Graph.ExpansionEnabled && svc.config.Graph.GraphMode != "off"
		if req.IncludeGraph != nil && !*req.IncludeGraph {
			includeGraph = false
		}
		if includeGraph {
			focusSeedIDs := make([]string, 0, len(focusIDs))
			count := 0
			for id := range focusIDs {
				if count >= svc.config.Graph.ExpansionSeedTopK {
					break
				}
				focusSeedIDs = append(focusSeedIDs, id)
				count++
			}
			graphFocusIDs := svc.contextGraphExpand(ctx, focusSeedIDs)
			for _, id := range graphFocusIDs {
				focusIDs[id] = true
			}

			slog.DebugContext(ctx, "context graph focus expanded",
				"event", "mem_context_with_ppr",
				"mode", svc.config.Graph.GraphMode,
				"seeds", len(focusSeedIDs),
				"graph_focus_ids", len(graphFocusIDs),
			)
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
	// Sessions are always stored in the project store.
	lastSess, err := svc.projectStore.GetLastSession(ctx, req.Project)
	if err != nil {
		return nil, fmt.Errorf("service: context: get last session: %w", err)
	}

	var lastSession *model.SessionSummary
	if lastSess != nil && lastSess.SummaryID != "" {
		summaryMem, err := svc.projectStore.Get(ctx, lastSess.SummaryID)
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
		// DEDUP: skip any memory already included in the rules section.
		// Post-scoring dedup preserves TotalAvailable semantics and backward compat.
		if ruleIDs[sc.mem.ID] {
			continue
		}
		cost := estimateTokens(sc.mem.Title) + estimateTokens(sc.mem.Content)
		if tokenUsed+cost > tokenBudget {
			break
		}
		packed = append(packed, *sc.mem)
		tokenUsed += cost
	}

	// ── PHASE 3: Build response ───────────────────────────────────────────────

	totalTokens := rulesTokens + tokenUsed
	if lastSession != nil {
		totalTokens += estimateTokens(lastSession.Summary)
	}

	return &model.ContextResponse{
		Project:        req.Project,
		Memories:       packed,
		Rules:          packedRules,
		TokenEstimate:  totalTokens,
		TotalAvailable: totalAvailable,
		Included:       len(packed),
		LastSession:    lastSession,
		RulesCount:     len(packedRules),
		RulesTokens:    rulesTokens,
		RulesTruncated: rulesTruncated,
	}, nil
}

// loadActiveRules fetches all active rule-type memories from the project store
// and, when IncludeGlobal is true, from the global store as well. Global rules
// are filtered by GlobalMinImportance, matching the behaviour of the general
// context assembly (context.go phase 2).
//
// A safety cap of 200 rules per store is applied to bound memory and CPU usage
// even when a project accumulates many rules over time.
func (svc *MemoryService) loadActiveRules(ctx context.Context, project string) ([]model.Memory, error) {
	const ruleCap = 200

	projectRules, err := svc.projectStore.List(ctx, store.ListOptions{
		Project: project,
		Type:    model.TypeRule,
		OrderBy: "importance DESC",
		Limit:   ruleCap,
	})
	if err != nil {
		return nil, fmt.Errorf("service: load active rules: project store: %w", err)
	}

	allRules := make([]model.Memory, 0, len(projectRules))
	for _, r := range projectRules {
		allRules = append(allRules, *r)
	}

	if svc.config.Context.IncludeGlobal {
		globalRules, err := svc.globalStore.List(ctx, store.ListOptions{
			Type:    model.TypeRule,
			Scope:   model.ScopeGlobal,
			OrderBy: "importance DESC",
			Limit:   ruleCap,
		})
		if err != nil {
			return nil, fmt.Errorf("service: load active rules: global store: %w", err)
		}
		for _, r := range globalRules {
			if r.Importance >= svc.config.Context.GlobalMinImportance {
				allRules = append(allRules, *r)
			}
		}
	}

	return allRules, nil
}

// severityOrder maps a Severity to a numeric rank for use in the rules packing
// sort. Higher values have higher priority so that block rules are always packed
// before warn, and warn before info — ensuring the most restrictive constraints
// are preserved when the rules budget is exhausted.
func severityOrder(s model.Severity) int {
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

// contextGraphExpand discovers memories topologically related to the focus
// seeds and returns their IDs for inclusion in the focus boost set.
// Routes to graphChannelPPR or graphExpand based on config.Graph.GraphMode.
// Errors are swallowed — graph expansion in context is best-effort and must
// never block context assembly (D4).
func (svc *MemoryService) contextGraphExpand(ctx context.Context, focusSeedIDs []string) []string {
	if len(focusSeedIDs) == 0 {
		return nil
	}

	var memIDs []string

	switch svc.config.Graph.GraphMode {
	case "ppr", "": // empty string treated as "ppr"
		graphResults, _ := svc.graphChannelPPR(ctx, focusSeedIDs)
		for _, gr := range graphResults {
			memIDs = append(memIDs, gr.MemoryID)
		}
	case "1hop":
		graphResults, _ := svc.graphExpand(ctx, focusSeedIDs)
		for _, gr := range graphResults {
			memIDs = append(memIDs, gr.MemoryID)
		}
	default: // "off"
		return nil
	}

	return memIDs
}

// estimateTokens returns a rough token count for the given text using the
// approximation of 1 token per 3 characters (valid for typical English/Spanish
// prose and Markdown). This avoids a dependency on a tokeniser library while
// giving a conservative-enough estimate for budget calculations.
func estimateTokens(text string) int {
	return int(float64(utf8.RuneCountInString(text)) / 3.0)
}
