package graph

import (
	"log/slog"
	"testing"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/model"
)

// newTestTrackerAndPool builds an AccessTracker backed by a buffered-channel
// HebbianWorkerPool with no running worker. Events accumulate in the channel
// so tests can drain and inspect them without a real store.
func newTestTrackerAndPool(window int) (*AccessTracker, *HebbianWorkerPool) {
	pool := &HebbianWorkerPool{
		ch:     make(chan StrengtheningEvent, 10000),
		logger: slog.Default(),
		config: config.GraphConfig{HebbianIncrement: 0.05, HebbianInitialWeight: 0.1},
	}
	cfg := config.GraphConfig{
		HebbianWindow:    window,
		HebbianIncrement: 0.05,
	}
	tracker := NewAccessTracker(pool, cfg, slog.Default())
	return tracker, pool
}

// drainEvents reads all pending events from the pool's channel without blocking.
func drainEvents(pool *HebbianWorkerPool) []StrengtheningEvent {
	var evts []StrengtheningEvent
	for {
		select {
		case e := <-pool.ch:
			evts = append(evts, e)
		default:
			return evts
		}
	}
}

// TestTracker_Record_GeneratesPairs verifies that recording two memories with
// distinct entities generates a strengthening event for the co-occurring pair.
func TestTracker_Record_GeneratesPairs(t *testing.T) {
	tracker, pool := newTestTrackerAndPool(5)

	tracker.Record("mem-a", model.TypeDiscovery, model.ScopeProject, []string{"ent-1"})
	tracker.Record("mem-b", model.TypeDecision, model.ScopeProject, []string{"ent-2"})

	evts := drainEvents(pool)
	if len(evts) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evts))
	}
	// Canonical order: "ent-1" < "ent-2".
	if evts[0].SourceEntityID != "ent-1" || evts[0].TargetEntityID != "ent-2" {
		t.Errorf("pair = (%s, %s), want (ent-1, ent-2)", evts[0].SourceEntityID, evts[0].TargetEntityID)
	}
	if evts[0].RelationType != model.RelRelatedTo {
		t.Errorf("relation type = %s, want related_to", evts[0].RelationType)
	}
	if evts[0].Delta != 0.05 {
		t.Errorf("delta = %f, want 0.05", evts[0].Delta)
	}
}

// TestTracker_Record_SelfLoopGuard verifies that recording the same ID twice
// consecutively produces no events (D4).
func TestTracker_Record_SelfLoopGuard(t *testing.T) {
	tracker, pool := newTestTrackerAndPool(5)

	tracker.Record("mem-a", model.TypeDiscovery, model.ScopeProject, []string{"ent-1"})
	tracker.Record("mem-a", model.TypeDiscovery, model.ScopeProject, []string{"ent-1"})

	evts := drainEvents(pool)
	if len(evts) != 0 {
		t.Errorf("expected 0 events (self-loop guard), got %d", len(evts))
	}
}

// TestTracker_Record_ExcludesRules verifies that TypeRule memories are not
// registered in the tracker (D5).
func TestTracker_Record_ExcludesRules(t *testing.T) {
	tracker, pool := newTestTrackerAndPool(5)

	tracker.Record("rule-1", model.TypeRule, model.ScopeProject, []string{"ent-1"})
	tracker.Record("mem-b", model.TypeDiscovery, model.ScopeProject, []string{"ent-2"})

	evts := drainEvents(pool)
	if len(evts) != 0 {
		t.Errorf("expected 0 events (rule excluded), got %d", len(evts))
	}
}

// TestTracker_Record_ExcludesSessionSummary verifies that TypeSessionSummary
// memories are not registered in the tracker (D5).
func TestTracker_Record_ExcludesSessionSummary(t *testing.T) {
	tracker, pool := newTestTrackerAndPool(5)

	tracker.Record("summary-1", model.TypeSessionSummary, model.ScopeProject, []string{"ent-1"})
	tracker.Record("mem-b", model.TypeDiscovery, model.ScopeProject, []string{"ent-2"})

	evts := drainEvents(pool)
	if len(evts) != 0 {
		t.Errorf("expected 0 events (session_summary excluded), got %d", len(evts))
	}
}

// TestTracker_Record_CrossScopeIgnored verifies that pairs spanning project and
// global scopes are discarded silently (D1).
func TestTracker_Record_CrossScopeIgnored(t *testing.T) {
	tracker, pool := newTestTrackerAndPool(5)

	tracker.Record("mem-project", model.TypeDiscovery, model.ScopeProject, []string{"ent-1"})
	tracker.Record("mem-global", model.TypeDiscovery, model.ScopeGlobal, []string{"ent-2"})

	evts := drainEvents(pool)
	if len(evts) != 0 {
		t.Errorf("expected 0 events (cross-scope discarded), got %d", len(evts))
	}
}

// TestTracker_Record_WindowDedup verifies that repeated entities in the window
// are deduplicated — [A,B,A,B,A] behaves as {A,B} for pairing (D3).
func TestTracker_Record_WindowDedup(t *testing.T) {
	tracker, pool := newTestTrackerAndPool(5)

	tracker.Record("mem-a", model.TypeDiscovery, model.ScopeProject, []string{"ent-1"})
	tracker.Record("mem-b", model.TypeDiscovery, model.ScopeProject, []string{"ent-2"})
	tracker.Record("mem-aa", model.TypeDiscovery, model.ScopeProject, []string{"ent-1"})
	tracker.Record("mem-bb", model.TypeDecision, model.ScopeProject, []string{"ent-2"})
	_ = drainEvents(pool) // discard intermediate events

	// Recording mem-c should pair ent-3 with each unique entity in the window.
	// The window contains {ent-1, ent-2} so 2 unique pairs are expected.
	tracker.Record("mem-c", model.TypeDecision, model.ScopeProject, []string{"ent-3"})

	evts := drainEvents(pool)
	if len(evts) != 2 {
		t.Errorf("expected 2 unique events (dedup), got %d", len(evts))
	}
}

// TestTracker_Record_HebbianWindowZero verifies that window=0 disables the
// tracker entirely (toggle-off per D8 / edge case 8.5).
func TestTracker_Record_HebbianWindowZero(t *testing.T) {
	tracker, pool := newTestTrackerAndPool(0)

	tracker.Record("mem-a", model.TypeDiscovery, model.ScopeProject, []string{"ent-1"})
	tracker.Record("mem-b", model.TypeDiscovery, model.ScopeProject, []string{"ent-2"})

	evts := drainEvents(pool)
	if len(evts) != 0 {
		t.Errorf("expected 0 events (window=0 disables tracking), got %d", len(evts))
	}
}

// TestTracker_Record_HebbianWindowOne verifies window=1 behaviour: the ring
// holds one slot so each new memory pairs with the previous one.
func TestTracker_Record_HebbianWindowOne(t *testing.T) {
	tracker, pool := newTestTrackerAndPool(1)

	tracker.Record("mem-a", model.TypeDiscovery, model.ScopeProject, []string{"ent-1"})
	tracker.Record("mem-b", model.TypeDiscovery, model.ScopeProject, []string{"ent-2"})

	evts := drainEvents(pool)
	// window=1 holds mem-a; recording mem-b pairs ent-2 with ent-1 → 1 event.
	if len(evts) != 1 {
		t.Errorf("expected 1 event (window=1), got %d", len(evts))
	}
}

// TestTracker_Record_EmptyEntityIDs verifies that memories with no linked
// entities are silently ignored (edge case 8.8).
func TestTracker_Record_EmptyEntityIDs(t *testing.T) {
	tracker, pool := newTestTrackerAndPool(5)

	tracker.Record("mem-a", model.TypeDiscovery, model.ScopeProject, nil)
	tracker.Record("mem-b", model.TypeDiscovery, model.ScopeProject, []string{"ent-2"})

	evts := drainEvents(pool)
	if len(evts) != 0 {
		t.Errorf("expected 0 events (no-entity memory ignored), got %d", len(evts))
	}
}

// TestTracker_Record_SameEntityGuard verifies that two memories sharing the
// same entity produce no self-pair (entity-level self-loop, edge case 8.9).
func TestTracker_Record_SameEntityGuard(t *testing.T) {
	tracker, pool := newTestTrackerAndPool(5)

	sharedEnt := "shared-entity-id"
	tracker.Record("mem-a", model.TypeDiscovery, model.ScopeProject, []string{sharedEnt})
	tracker.Record("mem-b", model.TypeDiscovery, model.ScopeProject, []string{sharedEnt})

	evts := drainEvents(pool)
	if len(evts) != 0 {
		t.Errorf("expected 0 events (same entity — self-pair discarded), got %d", len(evts))
	}
}

// TestTracker_Record_RingBufferOverflow verifies that only the last
// HebbianWindow entries are retained when more memories than the window size
// are recorded.
func TestTracker_Record_RingBufferOverflow(t *testing.T) {
	tracker, pool := newTestTrackerAndPool(3)

	tracker.Record("mem-1", model.TypeDiscovery, model.ScopeProject, []string{"ent-1"})
	tracker.Record("mem-2", model.TypeDiscovery, model.ScopeProject, []string{"ent-2"})
	tracker.Record("mem-3", model.TypeDiscovery, model.ScopeProject, []string{"ent-3"})
	tracker.Record("mem-4", model.TypeDiscovery, model.ScopeProject, []string{"ent-4"})
	tracker.Record("mem-5", model.TypeDiscovery, model.ScopeProject, []string{"ent-5"})
	_ = drainEvents(pool) // discard intermediate events

	// Record mem-6 — window should contain only ent-3, ent-4, ent-5.
	tracker.Record("mem-6", model.TypeDecision, model.ScopeProject, []string{"ent-6"})
	evts := drainEvents(pool)

	if len(evts) != 3 {
		t.Errorf("expected 3 events (window=3), got %d", len(evts))
	}

	for _, e := range evts {
		if e.SourceEntityID == "ent-1" || e.TargetEntityID == "ent-1" {
			t.Error("ent-1 should have been evicted from the ring buffer")
		}
		if e.SourceEntityID == "ent-2" || e.TargetEntityID == "ent-2" {
			t.Error("ent-2 should have been evicted from the ring buffer")
		}
	}
}

// TestTracker_Record_CanonicalOrdering verifies that pairs are always emitted
// with the lexicographically smaller entity ID as the source regardless of the
// access order.
func TestTracker_Record_CanonicalOrdering(t *testing.T) {
	tracker, pool := newTestTrackerAndPool(5)

	// "zzz" > "aaa", so pair should be (aaa, zzz).
	tracker.Record("mem-a", model.TypeDiscovery, model.ScopeProject, []string{"zzz-entity"})
	tracker.Record("mem-b", model.TypeDiscovery, model.ScopeProject, []string{"aaa-entity"})

	evts := drainEvents(pool)
	if len(evts) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evts))
	}
	if evts[0].SourceEntityID != "aaa-entity" || evts[0].TargetEntityID != "zzz-entity" {
		t.Errorf("pair = (%s, %s), want (aaa-entity, zzz-entity)", evts[0].SourceEntityID, evts[0].TargetEntityID)
	}
}

// TestTracker_Record_ExcludesSynthesis verifies that TypeSynthesis memories
// are silently ignored and generate no co-access pairs (SPEC-021 D6).
func TestTracker_Record_ExcludesSynthesis(t *testing.T) {
	tracker, pool := newTestTrackerAndPool(5)

	// Record a regular memory first to populate the window.
	tracker.Record("mem-regular", model.TypeDiscovery, model.ScopeProject, []string{"ent-regular"})

	// Record a synthesis memory — must not produce any pairs.
	tracker.Record("mem-synthesis", model.TypeSynthesis, model.ScopeProject, []string{"ent-synthesis"})

	evts := drainEvents(pool)
	// Only the synthesis record is new — it should produce zero events.
	// The window still contains mem-regular, but the synthesis is filtered out.
	if len(evts) != 0 {
		t.Errorf("expected 0 events for TypeSynthesis access, got %d", len(evts))
	}
}
