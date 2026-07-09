package service

import (
	"strings"
	"testing"

	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/subagents"
)

// manifestWith builds a minimal manifest slice from role/path pairs for test
// readability.
func manifestWith(entries ...ManifestEntry) []ManifestEntry {
	return entries
}

func entry(role subagents.Role, path string) ManifestEntry {
	return ManifestEntry{Role: role, Path: path}
}

// TestResolveStageExecutor_ImplementingWithBackend covers AC1: a manifest
// with a backend entry resolves implementing to a delegated subagent.
func TestResolveStageExecutor_ImplementingWithBackend(t *testing.T) {
	manifest := manifestWith(entry(subagents.RoleBackend, "/repo/.claude/agents/backend.md"))

	got := ResolveStageExecutor(model.SpecStatusImplementing, model.LaneStandard, manifest)

	if got.Executor != executorSubagent {
		t.Errorf("Executor = %q, want %q", got.Executor, executorSubagent)
	}
	if !got.Delegate {
		t.Error("Delegate = false, want true")
	}
	if got.Degraded {
		t.Error("Degraded = true, want false")
	}
	if len(got.Subagents) != 1 || got.Subagents[0].Role != "backend" {
		t.Errorf("Subagents = %+v, want single backend entry", got.Subagents)
	}
}

// TestResolveStageExecutor_ImplementingWithoutImplementer covers AC2: a
// manifest with only qa-tester (no backend/frontend) degrades implementing
// to the orchestrator, with a hint mentioning the degraded mode.
func TestResolveStageExecutor_ImplementingWithoutImplementer(t *testing.T) {
	manifest := manifestWith(entry(subagents.RoleQATester, "/repo/.claude/agents/qa-tester.md"))

	got := ResolveStageExecutor(model.SpecStatusImplementing, model.LaneStandard, manifest)

	if got.Executor != executorOrchestrator {
		t.Errorf("Executor = %q, want %q", got.Executor, executorOrchestrator)
	}
	if got.Delegate {
		t.Error("Delegate = true, want false")
	}
	if !got.Degraded {
		t.Error("Degraded = false, want true")
	}
	if len(got.Subagents) != 0 {
		t.Errorf("Subagents = %+v, want empty", got.Subagents)
	}
	lowerHint := strings.ToLower(got.Hint)
	if !strings.Contains(lowerHint, "degradad") {
		t.Errorf("Hint = %q, want it to mention degraded mode", got.Hint)
	}
	if !strings.Contains(lowerHint, "materializ") {
		t.Errorf("Hint = %q, want it to mention materializing a subagent", got.Hint)
	}
}

// TestResolveStageExecutor_ImplementingExcludesBugHunter verifies SPEC-068
// D4: bug-hunter is IsImplementer==true but must never be offered as a
// candidate for the implementing stage.
func TestResolveStageExecutor_ImplementingExcludesBugHunter(t *testing.T) {
	manifest := manifestWith(entry(subagents.RoleBugHunter, "/repo/.claude/agents/bug-hunter.md"))

	got := ResolveStageExecutor(model.SpecStatusImplementing, model.LaneStandard, manifest)

	if got.Executor != executorOrchestrator || !got.Degraded {
		t.Errorf("bug-hunter-only manifest must degrade implementing, got Executor=%q Degraded=%v", got.Executor, got.Degraded)
	}
}

// TestResolveStageExecutor_Speccing covers AC3 for the architect role: with
// architect present, speccing delegates; without it, speccing degrades.
func TestResolveStageExecutor_Speccing(t *testing.T) {
	t.Run("with architect", func(t *testing.T) {
		manifest := manifestWith(entry(subagents.RoleArchitect, "/repo/.claude/agents/architect.md"))
		got := ResolveStageExecutor(model.SpecStatusSpeccing, model.LaneStandard, manifest)
		if got.Executor != executorSubagent || !got.Delegate || got.Degraded {
			t.Errorf("got %+v, want delegated non-degraded subagent resolution", got)
		}
		if len(got.Subagents) != 1 || got.Subagents[0].Role != "architect" {
			t.Errorf("Subagents = %+v, want single architect entry", got.Subagents)
		}
	})

	t.Run("without architect", func(t *testing.T) {
		manifest := manifestWith(entry(subagents.RoleBackend, "/repo/.claude/agents/backend.md"))
		got := ResolveStageExecutor(model.SpecStatusSpeccing, model.LaneStandard, manifest)
		if got.Executor != executorOrchestrator || got.Delegate || !got.Degraded {
			t.Errorf("got %+v, want degraded orchestrator fallback", got)
		}
	})
}

// TestResolveStageExecutor_QA mirrors the architect coverage for the
// qa-tester role, completing AC6's "4 etapas delegables" table.
func TestResolveStageExecutor_QA(t *testing.T) {
	t.Run("with qa-tester", func(t *testing.T) {
		manifest := manifestWith(entry(subagents.RoleQATester, "/repo/.claude/agents/qa-tester.md"))
		got := ResolveStageExecutor(model.SpecStatusQA, model.LaneStandard, manifest)
		if got.Executor != executorSubagent || !got.Delegate || got.Degraded {
			t.Errorf("got %+v, want delegated non-degraded subagent resolution", got)
		}
	})

	t.Run("without qa-tester", func(t *testing.T) {
		manifest := manifestWith(entry(subagents.RoleArchitect, "/repo/.claude/agents/architect.md"))
		got := ResolveStageExecutor(model.SpecStatusQA, model.LaneStandard, manifest)
		if got.Executor != executorOrchestrator || got.Delegate || !got.Degraded {
			t.Errorf("got %+v, want degraded orchestrator fallback", got)
		}
	})
}

// TestResolveStageExecutor_Planning exercises the remaining delegable stage
// (planning, also owned by architect) to round out AC6.
func TestResolveStageExecutor_Planning(t *testing.T) {
	manifest := manifestWith(entry(subagents.RoleArchitect, "/repo/.claude/agents/architect.md"))
	got := ResolveStageExecutor(model.SpecStatusPlanning, model.LaneStandard, manifest)
	if got.Executor != executorSubagent || !got.Delegate || got.Degraded {
		t.Errorf("got %+v, want delegated non-degraded subagent resolution", got)
	}
}

// TestResolveStageExecutor_Gates covers AC4: gates and deterministic/terminal
// stages always resolve to the orchestrator, never degraded, regardless of
// manifest contents.
func TestResolveStageExecutor_Gates(t *testing.T) {
	manifest := manifestWith(
		entry(subagents.RoleArchitect, "/repo/.claude/agents/architect.md"),
		entry(subagents.RoleBackend, "/repo/.claude/agents/backend.md"),
		entry(subagents.RoleQATester, "/repo/.claude/agents/qa-tester.md"),
	)

	gateStages := []model.SpecStatus{
		model.SpecStatusSpecced,
		model.SpecStatusPlanned,
		model.SpecStatusRationale,
		model.SpecStatusAudit,
		model.SpecStatusDone,
		model.SpecStatusDraft,
		model.SpecStatusNeedsGrill,
	}

	for _, stage := range gateStages {
		t.Run(string(stage), func(t *testing.T) {
			got := ResolveStageExecutor(stage, model.LaneStandard, manifest)
			if got.Executor != executorOrchestrator {
				t.Errorf("Executor = %q, want %q", got.Executor, executorOrchestrator)
			}
			if got.Delegate {
				t.Error("Delegate = true, want false")
			}
			if got.Degraded {
				t.Error("Degraded = true, want false — gates are never a degraded fallback")
			}
			if got.ResponsibleRole != responsibleOrchestrator {
				t.Errorf("ResponsibleRole = %q, want %q", got.ResponsibleRole, responsibleOrchestrator)
			}
		})
	}
}

// TestResolveStageExecutor_GatesBothLanes verifies AC4's "verificable para
// ambos lanes" clause: trivial-lane-only stages (rationale, audit) and
// standard-lane-only stages (specced, planned) both resolve identically
// regardless of the lane argument, since stageResponsibleRole keys purely on
// status.
func TestResolveStageExecutor_GatesBothLanes(t *testing.T) {
	for _, lane := range []model.Lane{model.LaneStandard, model.LaneTrivial} {
		for _, stage := range []model.SpecStatus{model.SpecStatusRationale, model.SpecStatusAudit, model.SpecStatusSpecced, model.SpecStatusPlanned, model.SpecStatusDone} {
			got := ResolveStageExecutor(stage, lane, nil)
			if got.Executor != executorOrchestrator || got.Delegate || got.Degraded {
				t.Errorf("lane=%s stage=%s: got %+v, want plain orchestrator resolution", lane, stage, got)
			}
		}
	}
}

// TestResolveStageExecutor_NilManifest covers AC5: a nil manifest (no grill
// has run) never panics and produces the expected Degraded/empty-Subagents
// shape for delegable stages, and a non-degraded orchestrator resolution for
// gates.
func TestResolveStageExecutor_NilManifest(t *testing.T) {
	tests := []struct {
		stage        model.SpecStatus
		wantDegraded bool
	}{
		{model.SpecStatusSpeccing, true},
		{model.SpecStatusPlanning, true},
		{model.SpecStatusImplementing, true},
		{model.SpecStatusQA, true},
		{model.SpecStatusSpecced, false},
		{model.SpecStatusPlanned, false},
		{model.SpecStatusDone, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.stage), func(t *testing.T) {
			got := ResolveStageExecutor(tt.stage, model.LaneStandard, nil)
			if got.Degraded != tt.wantDegraded {
				t.Errorf("Degraded = %v, want %v", got.Degraded, tt.wantDegraded)
			}
			if len(got.Subagents) != 0 {
				t.Errorf("Subagents = %+v, want empty for nil manifest", got.Subagents)
			}
			if got.Stage != tt.stage {
				t.Errorf("Stage = %q, want %q", got.Stage, tt.stage)
			}
		})
	}
}

// TestResolveStageExecutor_UnknownStatusDefaultsToOrchestrator guards the
// stageResponsibleRole fallback branch for any status absent from the map.
func TestResolveStageExecutor_UnknownStatusDefaultsToOrchestrator(t *testing.T) {
	got := ResolveStageExecutor(model.SpecStatus("bogus"), model.LaneStandard, nil)
	if got.Executor != executorOrchestrator || got.Delegate || got.Degraded {
		t.Errorf("got %+v, want plain orchestrator resolution for unknown status", got)
	}
	if got.ResponsibleRole != responsibleOrchestrator {
		t.Errorf("ResponsibleRole = %q, want %q", got.ResponsibleRole, responsibleOrchestrator)
	}
}
