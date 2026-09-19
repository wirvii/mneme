package model

import "testing"

func TestWorkTransitions_All81Pairs(t *testing.T) {
	states := WorkStatuses()
	if len(states) != 9 {
		t.Fatalf("got %d statuses, want 9", len(states))
	}
	want := map[[2]WorkStatus]bool{
		{WorkStatusDraft, WorkStatusLocked}: true, {WorkStatusDraft, WorkStatusAbandoned}: true,
		{WorkStatusLocked, WorkStatusImplementing}: true, {WorkStatusLocked, WorkStatusAbandoned}: true,
		{WorkStatusImplementing, WorkStatusVerifying}: true, {WorkStatusImplementing, WorkStatusAbandoned}: true,
		{WorkStatusVerifying, WorkStatusDone}: true, {WorkStatusVerifying, WorkStatusCorrecting}: true,
		{WorkStatusVerifying, WorkStatusEscalated}: true, {WorkStatusVerifying, WorkStatusAbandoned}: true,
		{WorkStatusCorrecting, WorkStatusTargetedVerifying}: true, {WorkStatusCorrecting, WorkStatusAbandoned}: true,
		{WorkStatusTargetedVerifying, WorkStatusDone}: true, {WorkStatusTargetedVerifying, WorkStatusEscalated}: true,
		{WorkStatusTargetedVerifying, WorkStatusAbandoned}: true,
		{WorkStatusEscalated, WorkStatusImplementing}:      true, {WorkStatusEscalated, WorkStatusAbandoned}: true,
	}
	for _, from := range states {
		for _, to := range states {
			if got := CanTransitionWork(from, to); got != want[[2]WorkStatus{from, to}] {
				t.Errorf("%s -> %s = %v, want %v", from, to, got, want[[2]WorkStatus{from, to}])
			}
		}
	}
	if CanTransitionWork(WorkStatusTargetedVerifying, WorkStatusCorrecting) {
		t.Fatal("targeted_verifying -> correcting must stay absent")
	}
}

func TestAutomaticWorkTransitions_Acyclic(t *testing.T) {
	edges := AutomaticWorkTransitions()
	visiting, visited := map[WorkStatus]bool{}, map[WorkStatus]bool{}
	var visit func(WorkStatus) bool
	visit = func(n WorkStatus) bool {
		if visiting[n] {
			return false
		}
		if visited[n] {
			return true
		}
		visiting[n] = true
		for _, next := range edges[n] {
			if !visit(next) {
				return false
			}
		}
		visiting[n], visited[n] = false, true
		return true
	}
	for _, state := range WorkStatuses() {
		if !visit(state) {
			t.Fatalf("automatic graph has a cycle at %s", state)
		}
	}
}
