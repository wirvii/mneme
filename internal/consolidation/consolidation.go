// Package consolidation implements the background memory maintenance pipeline.
// It periodically sweeps memories for decay-based eviction, detects and merges
// duplicates, resolves contradictions, and enforces memory budgets. This keeps
// the memory store healthy over time without manual curation.
package consolidation

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/scoring"
	"github.com/juanftp/mneme/internal/store"
)

// sweepThreshold is the effective-importance value below which a memory is
// considered stale and eligible for soft-deletion during the sweep phase.
const sweepThreshold = 0.05

// Pipeline orchestrates a single consolidation cycle against a MemoryStore.
// It runs sweep, hard-delete, dedup, and budget-enforcement in sequence.
// Each step is independent: a partial failure returns what was accomplished
// up to the failing step and wraps the underlying error.
type Pipeline struct {
	store   *store.MemoryStore
	config  *config.Config
	logger  *slog.Logger
	project string // slug for project stores; empty string for the global store
}

// NewPipeline constructs a Pipeline. All three arguments are required; passing
// a nil store, config, or logger will cause a nil-pointer panic on the first Run.
// project is the project slug associated with this store. Pass an empty string
// when the store is the global store (which tracks GlobalBudget).
func NewPipeline(s *store.MemoryStore, cfg *config.Config, logger *slog.Logger) *Pipeline {
	return &Pipeline{
		store:  s,
		config: cfg,
		logger: logger,
	}
}

// WithProject returns a copy of the Pipeline associated with the given project
// slug. The project is used for budget enforcement: non-empty values apply
// ProjectBudget and filter by project; an empty string applies GlobalBudget.
func (p *Pipeline) WithProject(project string) *Pipeline {
	cp := *p
	cp.project = project
	return &cp
}

// ConsolidationResult summarises the outcome of a single consolidation cycle.
// All counter fields are non-negative. Duration is measured from the start of
// Run to when the last step completes.
type ConsolidationResult struct {
	// Swept is the number of memories soft-deleted by the decay sweep.
	Swept int `json:"swept"`

	// HardDeleted is the number of memories permanently removed because they
	// had been soft-deleted longer than the configured retention window.
	HardDeleted int `json:"hard_deleted"`

	// Duplicates is the number of duplicate pairs that were merged (one
	// memory marked as superseded, the other kept with merged content).
	Duplicates int `json:"duplicates"`

	// Conflicts is the number of contradictions resolved (reserved for Phase 4
	// conflict-resolution; always 0 in the current Phase 3 implementation).
	Conflicts int `json:"conflicts"`

	// Evicted is the number of memories soft-deleted to bring the store back
	// within the configured budget.
	Evicted int `json:"evicted"`

	// Duration is the total wall-clock time taken by the full cycle.
	Duration time.Duration `json:"duration"`
}

// Run executes one full consolidation cycle and returns a summary of what was
// done. Steps run in order: sweep → hard-delete → dedup → budget enforcement.
// The project field on the store is used as-is for budget enforcement — pass
// an empty string when the store is a global store.
//
// ctx cancellation is honoured between steps; a cancellation mid-cycle returns
// the partial ConsolidationResult alongside ctx.Err().
func (p *Pipeline) Run(ctx context.Context) (*ConsolidationResult, error) {
	start := time.Now()
	result := &ConsolidationResult{}

	swept, err := p.sweep(ctx)
	if err != nil {
		return result, fmt.Errorf("consolidation: run: %w", err)
	}
	result.Swept = swept

	if err := ctx.Err(); err != nil {
		result.Duration = time.Since(start)
		return result, err
	}

	hardDeleted, err := p.hardDelete(ctx)
	if err != nil {
		return result, fmt.Errorf("consolidation: run: %w", err)
	}
	result.HardDeleted = hardDeleted

	if err := ctx.Err(); err != nil {
		result.Duration = time.Since(start)
		return result, err
	}

	// Dedup across all projects that appear in the store. For the project
	// store we run dedup for the configured project slug; for the global
	// store we pass an empty string.
	deduped, err := p.dedup(ctx)
	if err != nil {
		return result, fmt.Errorf("consolidation: run: %w", err)
	}
	result.Duplicates = deduped

	if err := ctx.Err(); err != nil {
		result.Duration = time.Since(start)
		return result, err
	}

	// Budget enforcement: use the configured project slug. When the store is a
	// project store project is non-empty and ProjectBudget applies. When it is
	// the global store project is "" and GlobalBudget applies.
	evicted, err := p.enforceBudget(ctx, p.project)
	if err != nil {
		return result, fmt.Errorf("consolidation: run: %w", err)
	}
	result.Evicted = evicted

	result.Duration = time.Since(start)
	return result, nil
}

// sweep soft-deletes every active memory whose effective importance has fallen
// below sweepThreshold. It lists all active, non-superseded memories and
// evaluates the decay formula in Go so the threshold is applied uniformly
// regardless of SQLite's floating-point behaviour.
//
// Returns the number of memories that were soft-deleted.
func (p *Pipeline) sweep(ctx context.Context) (int, error) {
	// List everything — no project filter so one pipeline instance handles
	// its entire store. Use a large limit to capture the full store.
	memories, err := p.store.List(ctx, store.ListOptions{
		IncludeSuperseded: false,
		Limit:             1_000_000,
		OrderBy:           "created_at ASC",
	})
	if err != nil {
		return 0, fmt.Errorf("consolidation: sweep: list: %w", err)
	}

	now := time.Now().UTC()
	swept := 0

	for _, m := range memories {
		if ctx.Err() != nil {
			return swept, ctx.Err()
		}

		// Determine the reference timestamp for decay calculation.
		lastAccessed := m.UpdatedAt
		if m.LastAccessed != nil {
			lastAccessed = *m.LastAccessed
		}

		effective := scoring.EffectiveImportanceAt(m.Importance, m.DecayRate, lastAccessed, now)
		if effective >= sweepThreshold {
			continue
		}

		if err := p.store.SoftDelete(ctx, m.ID); err != nil {
			return swept, fmt.Errorf("consolidation: sweep: soft-delete %s: %w", m.ID, err)
		}
		swept++

		p.logger.Debug("consolidation: sweep: soft-deleted memory",
			"id", m.ID,
			"title", m.Title,
			"effective_importance", effective,
		)
	}

	return swept, nil
}

// hardDelete permanently removes memories that have been soft-deleted longer
// than the configured retention window (config.Consolidation.RetentionDays).
// A RetentionDays value of 0 falls back to 30. Negative values are accepted
// as-is (a negative window makes the cutoff a future time, which effectively
// hard-deletes all soft-deleted records — useful in tests).
//
// Returns the number of rows that were permanently removed.
func (p *Pipeline) hardDelete(ctx context.Context) (int, error) {
	retentionDays := p.config.Consolidation.RetentionDays
	if retentionDays == 0 {
		retentionDays = 30
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)

	n, err := p.store.HardDelete(ctx, cutoff)
	if err != nil {
		return 0, fmt.Errorf("consolidation: hard delete: %w", err)
	}

	if n > 0 {
		p.logger.Info("consolidation: hard-deleted stale memories",
			"count", n,
			"cutoff", cutoff.Format(time.RFC3339),
		)
	}

	return n, nil
}

// dedup finds pairs of active memories with identical titles in the same
// project and merges them. For each duplicate pair:
//   - The memory with higher importance is kept as the winner.
//   - The loser is marked as superseded by the winner.
//   - If the loser's content differs from the winner's, any text present only
//     in the loser is appended to the winner's content under a "---" separator
//     so no unique information is discarded.
//
// Returns the number of duplicate pairs that were resolved.
//
// This is the Phase 3 implementation (FTS5 only, no vector similarity). It
// only detects exact-title duplicates; fuzzy dedup will be added in Phase 4.
func (p *Pipeline) dedup(ctx context.Context) (int, error) {
	// We run dedup with an empty project string so FindDuplicateTitles
	// considers all records in this store. In practice a project store
	// contains memories for a single project, so this is equivalent to
	// filtering by project.
	pairs, err := p.store.FindDuplicateTitles(ctx, "")
	if err != nil {
		return 0, fmt.Errorf("consolidation: dedup: find duplicates: %w", err)
	}

	merged := 0

	for _, pair := range pairs {
		if ctx.Err() != nil {
			return merged, ctx.Err()
		}

		idA, idB := pair[0], pair[1]

		mA, err := p.store.Get(ctx, idA)
		if err != nil {
			return merged, fmt.Errorf("consolidation: dedup: get %s: %w", idA, err)
		}
		mB, err := p.store.Get(ctx, idB)
		if err != nil {
			return merged, fmt.Errorf("consolidation: dedup: get %s: %w", idB, err)
		}

		// Either may have been deleted or superseded by a prior iteration.
		if mA == nil || mB == nil {
			continue
		}

		// Determine winner (higher importance wins; tie goes to the older record
		// which is idA because FindDuplicateTitles orders a.id < b.id by string
		// comparison — both are UUIDv7 so this is also time-ordered).
		winner, loser := mA, mB
		if mB.Importance > mA.Importance {
			winner, loser = mB, mA
		}

		// If the loser carries content not present in the winner, append it.
		if loser.Content != winner.Content {
			unique := uniqueContent(winner.Content, loser.Content)
			if unique != "" {
				mergedContent := winner.Content + "\n\n---\n\n" + unique
				req := mergeUpdateRequest(mergedContent)
				if err := p.store.Update(ctx, winner.ID, &req); err != nil {
					return merged, fmt.Errorf("consolidation: dedup: update winner %s: %w", winner.ID, err)
				}
			}
		}

		if err := p.store.SetSupersededBy(ctx, loser.ID, winner.ID); err != nil {
			return merged, fmt.Errorf("consolidation: dedup: supersede %s: %w", loser.ID, err)
		}

		p.logger.Info("consolidation: dedup: merged duplicate memories",
			"winner", winner.ID,
			"loser", loser.ID,
			"title", winner.Title,
		)
		merged++
	}

	return merged, nil
}

// enforceBudget soft-deletes the lowest-scored memories when the store
// exceeds the configured project budget. project should be the slug used
// when the store was opened; pass an empty string for a global store (in
// which case GlobalBudget is applied).
//
// Returns the number of memories that were evicted.
func (p *Pipeline) enforceBudget(ctx context.Context, project string) (int, error) {
	budget := p.config.Storage.ProjectBudget
	if project == "" {
		budget = p.config.Storage.GlobalBudget
	}

	count, err := p.store.CountActive(ctx, project)
	if err != nil {
		return 0, fmt.Errorf("consolidation: enforce budget: count active: %w", err)
	}

	if count <= budget {
		return 0, nil
	}

	toEvict := count - budget

	// Retrieve the lowest-scored memories. We ask for exactly toEvict records
	// so we only load what we need to delete.
	candidates, err := p.store.ListByEffectiveImportance(ctx, project, toEvict)
	if err != nil {
		return 0, fmt.Errorf("consolidation: enforce budget: list candidates: %w", err)
	}

	evicted := 0
	for _, m := range candidates {
		if ctx.Err() != nil {
			return evicted, ctx.Err()
		}

		if err := p.store.SoftDelete(ctx, m.ID); err != nil {
			return evicted, fmt.Errorf("consolidation: enforce budget: soft-delete %s: %w", m.ID, err)
		}
		evicted++

		p.logger.Debug("consolidation: enforce budget: evicted memory",
			"id", m.ID,
			"title", m.Title,
			"importance", m.Importance,
		)
	}

	if evicted > 0 {
		p.logger.Info("consolidation: budget enforced",
			"project", project,
			"budget", budget,
			"evicted", evicted,
		)
	}

	return evicted, nil
}

// RunBackground starts a background goroutine that runs the consolidation
// pipeline immediately and then at every interval tick until ctx is cancelled.
// The interval parameter overrides the value stored in config — pass
// config.Consolidation.Interval parsed to a duration before calling this
// method. Results and errors are emitted as structured log entries.
func (p *Pipeline) RunBackground(ctx context.Context, interval time.Duration) {
	run := func() {
		result, err := p.Run(ctx)
		if err != nil {
			p.logger.Error("consolidation: background cycle failed", "error", err)
			return
		}
		p.logger.Info("consolidation: cycle complete",
			"swept", result.Swept,
			"hard_deleted", result.HardDeleted,
			"duplicates", result.Duplicates,
			"evicted", result.Evicted,
			"duration_ms", result.Duration.Milliseconds(),
		)
	}

	go func() {
		// Run immediately so the store is healthy right after startup.
		run()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// uniqueContent returns the portion of loser that is not present in winner.
// The comparison is done at the line level: lines from loser that do not
// appear anywhere in winner are considered unique. If all loser lines appear
// in winner an empty string is returned.
func uniqueContent(winner, loser string) string {
	winnerLines := lineSet(winner)

	var unique []string
	for _, line := range strings.Split(loser, "\n") {
		if _, exists := winnerLines[line]; !exists {
			unique = append(unique, line)
		}
	}

	return strings.Join(unique, "\n")
}

// lineSet builds a set of lines from content for O(1) membership tests.
func lineSet(content string) map[string]struct{} {
	lines := strings.Split(content, "\n")
	set := make(map[string]struct{}, len(lines))
	for _, l := range lines {
		set[l] = struct{}{}
	}
	return set
}

// mergeUpdateRequest builds a model.UpdateRequest that sets only the Content field.
func mergeUpdateRequest(content string) model.UpdateRequest {
	return model.UpdateRequest{Content: &content}
}
