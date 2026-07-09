// Package graph implements the Hebbian auto-strengthening subsystem for mneme's
// knowledge graph. It tracks co-access patterns across memory retrievals and
// asynchronously strengthens relations between entities that are frequently
// accessed together in the same session window.
//
// The two primary types are AccessTracker (sliding window ring buffer that
// records accesses and emits co-access pairs) and HebbianWorkerPool (async
// single-worker that applies UpdateRelationWeight / CreateRelation to the store).
package graph

import (
	"log/slog"
	"sync"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/model"
)

// trackedAccess records a single memory access in the ring buffer. It carries
// the entity IDs linked to that memory so strengthening events can reference
// graph nodes (relations connect entities, not memories).
type trackedAccess struct {
	memoryID  string
	entityIDs []string
	scope     model.Scope
}

// AccessTracker maintains a sliding window of recently accessed memories and
// generates co-access pairs for Hebbian strengthening. It is thread-safe.
//
// The tracker uses a fixed-size ring buffer of HebbianWindow slots. When a
// new memory is recorded, it generates pairs with every unique entity ID in
// the current window (cross-scope pairs are discarded per D1), deduplicates
// them using a canonical-order set, and sends StrengtheningEvents to the
// worker pool.
//
// Set HebbianWindow to 0 in config to disable tracking entirely.
type AccessTracker struct {
	mu     sync.Mutex
	ring   []trackedAccess
	pos    int    // next write position in the ring buffer
	count  int    // number of valid entries (0..HebbianWindow)
	lastID string // last recorded memory ID (for self-loop guard D4)
	pool   *HebbianWorkerPool
	config config.GraphConfig
	logger *slog.Logger
}

// NewAccessTracker constructs an AccessTracker backed by the given worker pool.
// A window size <= 0 in cfg is treated as 0 (tracking disabled).
func NewAccessTracker(pool *HebbianWorkerPool, cfg config.GraphConfig, logger *slog.Logger) *AccessTracker {
	size := cfg.HebbianWindow
	if size < 0 {
		size = 0
	}
	return &AccessTracker{
		ring:   make([]trackedAccess, size),
		pool:   pool,
		config: cfg,
		logger: logger,
	}
}

// Record registers a memory access. It generates co-access pairs between the
// new memory's entities and every unique entity in the current window, then
// enqueues StrengtheningEvents to the worker pool.
//
// The following accesses are silently ignored (no panic, no error):
//   - Memories of type TypeRule or TypeSessionSummary (D5).
//   - Memories with no linked entity IDs (D5 / edge case 8.8).
//   - Repeated access to the same memory ID as the last recorded one (D4).
//   - All accesses when HebbianWindow is 0 (toggle-off).
//
// entityIDs are the IDs of entities linked to this memory via memory_entities.
// scope is used to discard cross-scope pairs (D1).
func (t *AccessTracker) Record(memoryID string, memoryType model.MemoryType, memoryScope model.Scope, entityIDs []string) {
	// D5: exclude noise-generating types. TypeSynthesis is excluded because
	// synthesis memories are injected into mem_context and co-accessed with
	// every memory in the session window, which would create spurious Hebbian
	// strengthening across unrelated entities (SPEC-021 D6).
	if memoryType == model.TypeRule || memoryType == model.TypeSessionSummary || memoryType == model.TypeSynthesis {
		return
	}

	// Toggle-off: window disabled.
	if t.config.HebbianWindow <= 0 {
		return
	}

	// Edge case 8.8: no graph presence — nothing to strengthen.
	if len(entityIDs) == 0 {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// D4: self-loop guard — consecutive access to the same memory produces nothing.
	if memoryID == t.lastID {
		return
	}
	t.lastID = memoryID

	// Pair-dedup set: canonical order (min,max) prevents (A,B) and (B,A) counting twice.
	type pairKey struct{ src, tgt string }
	seen := make(map[pairKey]bool)

	for i := 0; i < t.count; i++ {
		// Walk backwards through the ring to reach most-recent entries first.
		idx := (t.pos - 1 - i + len(t.ring)) % len(t.ring)
		prev := t.ring[idx]

		// D1: cross-scope guard — discard pairs across project / global boundary.
		if prev.scope != memoryScope {
			continue
		}

		for _, newEnt := range entityIDs {
			for _, prevEnt := range prev.entityIDs {
				// Edge case 8.9: same entity on both sides — not a meaningful edge.
				if newEnt == prevEnt {
					continue
				}
				// Canonical order so (A,B) == (B,A).
				src, tgt := newEnt, prevEnt
				if src > tgt {
					src, tgt = tgt, src
				}
				key := pairKey{src, tgt}
				if seen[key] {
					continue
				}
				seen[key] = true

				t.pool.Enqueue(StrengtheningEvent{
					SourceEntityID: src,
					TargetEntityID: tgt,
					RelationType:   model.RelRelatedTo,
					Delta:          t.config.HebbianIncrement,
				})
			}
		}
	}

	// Write new entry to ring buffer, overwriting the oldest slot when full.
	t.ring[t.pos] = trackedAccess{
		memoryID:  memoryID,
		entityIDs: entityIDs,
		scope:     memoryScope,
	}
	t.pos = (t.pos + 1) % len(t.ring)
	if t.count < len(t.ring) {
		t.count++
	}
}
