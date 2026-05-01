package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/juanftp/mneme/internal/consolidation"
	"github.com/juanftp/mneme/internal/model"
)

// RunConsolidation executes one full consolidation cycle against the project
// store. If config.Context.IncludeGlobal is true, a second cycle is also run
// against the global store and its results are added to the returned summary.
//
// The community detection callback (SPEC-020) is wired into the pipeline so
// the detectCommunities step runs automatically after edge decay.
//
// A nil-safe logger is used when no structured logger is available at the
// service level; callers that care about log output should wire a logger
// through the application initialisation path instead.
func (svc *MemoryService) RunConsolidation(ctx context.Context) (*consolidation.ConsolidationResult, error) {
	logger := slog.Default()

	projectDetector := func(ctx context.Context) (*model.DetectionResult, error) {
		return svc.DetectAndPersistCommunities(ctx, model.ScopeProject, svc.project)
	}

	projectPipeline := consolidation.NewPipeline(svc.projectStore, svc.config, logger).
		WithProject(svc.project).
		WithCommunityDetector(projectDetector)
	result, err := projectPipeline.Run(ctx)
	if err != nil {
		return result, fmt.Errorf("service: run consolidation: project store: %w", err)
	}

	if svc.config.Context.IncludeGlobal {
		globalDetector := func(ctx context.Context) (*model.DetectionResult, error) {
			return svc.DetectAndPersistCommunities(ctx, model.ScopeGlobal, "")
		}

		// Global store uses an empty project slug — GlobalBudget applies.
		globalPipeline := consolidation.NewPipeline(svc.globalStore, svc.config, logger).
			WithCommunityDetector(globalDetector)
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

	projectDetector := func(ctx context.Context) (*model.DetectionResult, error) {
		return svc.DetectAndPersistCommunities(ctx, model.ScopeProject, svc.project)
	}
	consolidation.NewPipeline(svc.projectStore, svc.config, logger).
		WithProject(svc.project).
		WithCommunityDetector(projectDetector).
		RunBackground(ctx, interval)

	if svc.config.Context.IncludeGlobal {
		globalDetector := func(ctx context.Context) (*model.DetectionResult, error) {
			return svc.DetectAndPersistCommunities(ctx, model.ScopeGlobal, "")
		}
		// Global store uses empty project (GlobalBudget).
		consolidation.NewPipeline(svc.globalStore, svc.config, logger).
			WithCommunityDetector(globalDetector).
			RunBackground(ctx, interval)
	}
}

// Start launches all background tasks associated with the service:
//   - Background consolidation (decay, dedup, budget enforcement).
//   - Hebbian worker pool (async relation strengthening from co-access events).
//
// Both goroutines terminate when ctx is cancelled. For the MCP long-running
// server path, ctx cancellation is the shutdown signal. For CLI commands use
// DrainHebbian() instead of Start() to flush events on process exit.
func (svc *MemoryService) Start(ctx context.Context) {
	svc.StartBackgroundConsolidation(ctx)
	svc.hebbianPool.Start(ctx)
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
	a.EdgeDecayed += b.EdgeDecayed
	a.CommunitiesDetected += b.CommunitiesDetected
	a.CommunitiesNew += b.CommunitiesNew
	a.CommunitiesDeleted += b.CommunitiesDeleted
	if b.Duration > a.Duration {
		a.Duration = b.Duration
	}
	return a
}
