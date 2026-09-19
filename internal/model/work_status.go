package model

// WorkStatus is the delivery-v2 lifecycle state of a work contract.
type WorkStatus string

// WorkStatus values form the closed delivery-v2 lifecycle.
const (
	WorkStatusDraft             WorkStatus = "draft"
	WorkStatusLocked            WorkStatus = "locked"
	WorkStatusImplementing      WorkStatus = "implementing"
	WorkStatusVerifying         WorkStatus = "verifying"
	WorkStatusCorrecting        WorkStatus = "correcting"
	WorkStatusTargetedVerifying WorkStatus = "targeted_verifying"
	WorkStatusEscalated         WorkStatus = "escalated"
	WorkStatusDone              WorkStatus = "done"
	WorkStatusAbandoned         WorkStatus = "abandoned"
)

var allWorkStatuses = []WorkStatus{WorkStatusDraft, WorkStatusLocked, WorkStatusImplementing, WorkStatusVerifying, WorkStatusCorrecting, WorkStatusTargetedVerifying, WorkStatusEscalated, WorkStatusDone, WorkStatusAbandoned}
var workTransitions = map[WorkStatus][]WorkStatus{
	WorkStatusDraft: {WorkStatusLocked, WorkStatusAbandoned}, WorkStatusLocked: {WorkStatusImplementing, WorkStatusAbandoned},
	WorkStatusImplementing: {WorkStatusVerifying, WorkStatusAbandoned}, WorkStatusVerifying: {WorkStatusDone, WorkStatusCorrecting, WorkStatusEscalated, WorkStatusAbandoned},
	WorkStatusCorrecting: {WorkStatusTargetedVerifying, WorkStatusAbandoned}, WorkStatusTargetedVerifying: {WorkStatusDone, WorkStatusEscalated, WorkStatusAbandoned},
	WorkStatusEscalated: {WorkStatusImplementing, WorkStatusAbandoned},
}

// Valid reports whether the status is part of the closed delivery lifecycle.
func (s WorkStatus) Valid() bool {
	for _, candidate := range allWorkStatuses {
		if s == candidate {
			return true
		}
	}
	return false
}

// Terminal reports whether no further transition is legal.
func (s WorkStatus) Terminal() bool { return s == WorkStatusDone || s == WorkStatusAbandoned }

// WorkStatuses returns a copy of the complete status population for exhaustive checks.
func WorkStatuses() []WorkStatus { return append([]WorkStatus(nil), allWorkStatuses...) }

// CanTransitionWork reports whether the exact lifecycle edge is declared.
func CanTransitionWork(from, to WorkStatus) bool {
	for _, candidate := range workTransitions[from] {
		if candidate == to {
			return true
		}
	}
	return false
}

// AutomaticWorkTransitions returns only machine-driven edges, excluding abandonment and human restart.
func AutomaticWorkTransitions() map[WorkStatus][]WorkStatus {
	out := map[WorkStatus][]WorkStatus{}
	for from, tos := range workTransitions {
		for _, to := range tos {
			if to != WorkStatusAbandoned && (from != WorkStatusEscalated || to != WorkStatusImplementing) {
				out[from] = append(out[from], to)
			}
		}
	}
	return out
}
