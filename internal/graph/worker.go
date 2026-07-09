package graph

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/store"
)

// StrengtheningEvent represents a co-access pair that should be strengthened
// in the knowledge graph. The worker pool consumes these events asynchronously.
type StrengtheningEvent struct {
	// SourceEntityID and TargetEntityID are the entity IDs stored in canonical
	// order (lexicographically smaller ID first) to prevent duplicate edges.
	SourceEntityID string
	TargetEntityID string

	// RelationType is the edge type to use when creating a new relation.
	// Hebbian always uses RelRelatedTo — the generic co-access relation type.
	RelationType model.RelationType

	// Delta is the weight increment to apply to an existing relation.
	Delta float64
}

// HebbianWorkerPool processes StrengtheningEvents asynchronously. It owns a
// buffered channel and a single worker goroutine (D2a) that applies
// UpdateRelationWeight or CreateRelation calls to the store.
//
// Drop policy: when the channel is full, Enqueue discards the event and emits
// a slog Warn. The read path is never blocked. The worker runs until the
// context is cancelled (MCP long-running) or Drain is called (CLI shutdown).
type HebbianWorkerPool struct {
	ch     chan StrengtheningEvent
	store  *store.MemoryStore
	wg     sync.WaitGroup
	logger *slog.Logger
	config config.GraphConfig
}

// NewHebbianWorkerPool constructs a HebbianWorkerPool. Start must be called
// before the pool processes any events.
func NewHebbianWorkerPool(s *store.MemoryStore, cfg config.GraphConfig, logger *slog.Logger) *HebbianWorkerPool {
	bufSize := cfg.HebbianBufferSize
	if bufSize < 0 {
		bufSize = 0
	}
	return &HebbianWorkerPool{
		ch:     make(chan StrengtheningEvent, bufSize),
		store:  s,
		logger: logger,
		config: cfg,
	}
}

// Start launches the single worker goroutine. It must be called exactly once
// before any Enqueue calls. The worker terminates when ctx is cancelled
// (appropriate for the MCP long-running server path).
func (p *HebbianWorkerPool) Start(ctx context.Context) {
	p.logger.Info("hebbian tracker started",
		"event", "tracker_started",
		"window", p.config.HebbianWindow,
		"buffer_size", p.config.HebbianBufferSize,
		"increment", p.config.HebbianIncrement,
	)

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-p.ch:
				if !ok {
					return
				}
				p.applyStrengthening(ctx, evt)
			}
		}
	}()
}

// Enqueue sends an event to the worker pool. Returns true when the event was
// accepted, false when the channel is full (event dropped per D2).
func (p *HebbianWorkerPool) Enqueue(evt StrengtheningEvent) bool {
	select {
	case p.ch <- evt:
		return true
	default:
		p.logger.Warn("hebbian event dropped: buffer full",
			"event", "hebbian_buffer_full",
			"source", evt.SourceEntityID,
			"target", evt.TargetEntityID,
		)
		return false
	}
}

// Drain closes the event channel and waits for the worker to process all
// remaining events, with a deadline enforced by timeout. This is the
// shutdown path for CLI commands that create a pool for a single invocation.
//
// After Drain returns the pool must not be used again.
func (p *HebbianWorkerPool) Drain(timeout time.Duration) {
	close(p.ch)
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		p.logger.Warn("hebbian drain timed out",
			"event", "hebbian_drain_timeout",
			"timeout", timeout,
		)
	}
}

// applyStrengthening resolves the event against the store: if a relation
// already exists (in either direction) it is strengthened; otherwise a new
// relation is created with HebbianInitialWeight. Errors are logged and
// skipped — a missed strengthening never corrupts data (D2b).
func (p *HebbianWorkerPool) applyStrengthening(ctx context.Context, evt StrengtheningEvent) {
	existing, err := p.store.FindRelationBidirectional(ctx, evt.SourceEntityID, evt.TargetEntityID, evt.RelationType)
	if err != nil {
		p.logger.Error("hebbian: find relation failed",
			"event", "hebbian_error",
			"source", evt.SourceEntityID,
			"target", evt.TargetEntityID,
			"error", err,
		)
		return
	}

	if existing != nil {
		// Strengthen existing relation.
		_, err = p.store.UpdateRelationWeight(ctx, existing.ID, evt.Delta, time.Now().UTC())
		if err != nil {
			p.logger.Error("hebbian: update relation weight failed",
				"event", "hebbian_error",
				"relation_id", existing.ID,
				"error", err,
			)
			return
		}
		p.logger.Debug("hebbian: relation strengthened",
			"event", "hebbian_strengthen",
			"relation_id", existing.ID,
			"delta", evt.Delta,
		)
		return
	}

	// No relation exists — create one with HebbianInitialWeight.
	rel := &model.Relation{
		SourceID: evt.SourceEntityID,
		TargetID: evt.TargetEntityID,
		Type:     evt.RelationType,
		Weight:   p.config.HebbianInitialWeight,
		// Set LastTraversedAt so this Hebbian-created edge is eligible for
		// future decay (D6: NULL = never traversed = excluded from decay).
		LastTraversedAt: time.Now().UTC(),
	}
	created, err := p.store.CreateRelation(ctx, rel)
	if err != nil {
		p.logger.Error("hebbian: create relation failed",
			"event", "hebbian_error",
			"source", evt.SourceEntityID,
			"target", evt.TargetEntityID,
			"error", err,
		)
		return
	}
	p.logger.Debug("hebbian: relation created",
		"event", "hebbian_create_edge",
		"relation_id", created.ID,
		"weight", p.config.HebbianInitialWeight,
	)
}
