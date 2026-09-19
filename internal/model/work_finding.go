package model

import "time"

// FindingCategory classifies a review observation by its immutable closing effect.
type FindingCategory string

// FindingCategory values distinguish blocking violations from non-blocking observations.
const (
	FindingContractViolation     FindingCategory = "contract_violation"
	FindingRegression            FindingCategory = "regression"
	FindingArchitectureViolation FindingCategory = "architecture_violation"
	FindingDiscovery             FindingCategory = "discovery"
	FindingImprovement           FindingCategory = "improvement"
)

// Valid reports whether the category belongs to the closed finding vocabulary.
func (c FindingCategory) Valid() bool {
	return c == FindingContractViolation || c == FindingRegression || c == FindingArchitectureViolation || c == FindingDiscovery || c == FindingImprovement
}

// Blocks reports the immutable category-level closing effect without configuration.
func (c FindingCategory) Blocks() bool {
	return c == FindingContractViolation || c == FindingRegression || c == FindingArchitectureViolation
}

// BlockingFindingCategories derives the SQL population from the authoritative category predicate.
func BlockingFindingCategories() []FindingCategory {
	all := []FindingCategory{FindingContractViolation, FindingRegression, FindingArchitectureViolation, FindingDiscovery, FindingImprovement}
	out := []FindingCategory{}
	for _, c := range all {
		if c.Blocks() {
			out = append(out, c)
		}
	}
	return out
}

// FindingOrigin identifies the source that recorded a work finding.
type FindingOrigin string

// FindingOrigin values identify the allowed sources of a work finding.
const (
	FindingOriginReview FindingOrigin = "review"
	FindingOriginGate   FindingOrigin = "gate"
	FindingOriginHuman  FindingOrigin = "human"
)

// Valid reports whether the origin belongs to the closed finding vocabulary.
func (o FindingOrigin) Valid() bool {
	return o == FindingOriginReview || o == FindingOriginGate || o == FindingOriginHuman
}

// ReviewPhase identifies whether a finding came from the initial or targeted review.
type ReviewPhase string

// ReviewPhase values distinguish the two review phases allowed by the work lifecycle.
const (
	ReviewPhaseInitial  ReviewPhase = "initial"
	ReviewPhaseTargeted ReviewPhase = "targeted"
)

// Valid reports whether the review phase belongs to the closed finding vocabulary.
func (p ReviewPhase) Valid() bool { return p == ReviewPhaseInitial || p == ReviewPhaseTargeted }

// FindingStatus records the current resolution state of a work finding.
type FindingStatus string

// FindingStatus values describe the allowed resolution states for a work finding.
const (
	FindingOpen       FindingStatus = "open"
	FindingFixed      FindingStatus = "fixed"
	FindingBacklogged FindingStatus = "backlogged"
	FindingAccepted   FindingStatus = "accepted"
	FindingInvalid    FindingStatus = "invalid"
)

// Valid reports whether the finding status belongs to the closed finding vocabulary.
func (s FindingStatus) Valid() bool {
	return s == FindingOpen || s == FindingFixed || s == FindingBacklogged || s == FindingAccepted || s == FindingInvalid
}

// WorkFinding is an immutable finding description plus mutable resolution metadata.
type WorkFinding struct {
	ID, WorkID                              string
	Seq                                     int
	Category                                FindingCategory
	Severity                                Priority
	Description, Location, Evidence         string
	Origin                                  FindingOrigin
	ReviewPhase                             ReviewPhase
	Status                                  FindingStatus
	BacklogID, ResolutionReason, ResolvedBy string
	CreatedAt                               time.Time
	ResolvedAt                              *time.Time
}
