package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/juanftp/mneme/internal/consolidation"
)

// RunConsolidation executes one full consolidation cycle against the project
// store. If config.Context.IncludeGlobal is true, a second cycle is also run
// against the global store and its results are added to the returned summary.
//
// A nil-safe logger is used when no structured logger is available at the
// service level; callers that care about log output should wire a logger
// through the application initialisation path instead.
func (svc *MemoryService) RunConsolidation(ctx context.Context) (*consolidation.ConsolidationResult, error) {
	logger := slog.Default()

	projectPipeline := consolidation.NewPipeline(svc.projectStore, svc.config, logger).WithProject(svc.project)
	result, err := projectPipeline.Run(ctx)
	if err != nil {
		return result, fmt.Errorf("service: run consolidation: project store: %w", err)
	}

	if svc.config.Context.IncludeGlobal {
		// Global store uses an empty project slug — GlobalBudget applies.
		globalPipeline := consolidation.NewPipeline(svc.globalStore, svc.config, logger)
		globalResult, globalErr := globalPipeline.Run(ctx)
		if globalErr != nil {
			// Return the combined partial result alongside the error.
			combined := mergeResults(result, globalResult)
			return combined, fmt.Errorf("service: run consolidation: global store: %w", globalErr)
		}
		result = mergeResults(result, globalResult)
	}

	return result, nil
}

// StartBackgroundConsolidation launches a background goroutine that runs the
// consolidation pipeline on the project store (and optionally the global store)
// at the interval configured in config.Consolidation.Interval. It is a no-op
// when config.Consolidation.Enabled is false.
//
// The goroutine terminates when ctx is cancelled. Callers must ensure that ctx
// is cancelled (or the application exits) to avoid a goroutine leak.
func (svc *MemoryService) StartBackgroundConsolidation(ctx context.Context) {
	if !svc.config.Consolidation.Enabled {
		return
	}

	interval, err := time.ParseDuration(svc.config.Consolidation.Interval)
	if err != nil || interval <= 0 {
		// Fall back to the documented default if the config value is unparseable.
		interval = 6 * time.Hour
	}

	logger := slog.Default()
	consolidation.NewPipeline(svc.projectStore, svc.config, logger).WithProject(svc.project).RunBackground(ctx, interval)

	if svc.config.Context.IncludeGlobal {
		// Global store uses empty project (GlobalBudget).
		consolidation.NewPipeline(svc.globalStore, svc.config, logger).RunBackground(ctx, interval)
	}
}

// Start launches all background tasks associated with the service. Currently
// that is only background consolidation, but this method provides a single
// hook for the MCP server to start everything at once.
func (svc *MemoryService) Start(ctx context.Context) {
	svc.StartBackgroundConsolidation(ctx)
}

// mergeResults adds the counters from b into a and returns a. Duration is
// taken as the maximum of the two so it reflects the longer-running cycle.
func mergeResults(a, b *consolidation.ConsolidationResult) *consolidation.ConsolidationResult {
	if b == nil {
		return a
	}
	if a == nil {
		return b
	}
	a.Swept += b.Swept
	a.HardDeleted += b.HardDeleted
	a.Duplicates += b.Duplicates
	a.Conflicts += b.Conflicts
	a.Evicted += b.Evicted
	if b.Duration > a.Duration {
		a.Duration = b.Duration
	}
	return a
}
