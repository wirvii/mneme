package model

import "time"

type FindingCategory string

const (
	FindingContractViolation     FindingCategory = "contract_violation"
	FindingRegression            FindingCategory = "regression"
	FindingArchitectureViolation FindingCategory = "architecture_violation"
	FindingDiscovery             FindingCategory = "discovery"
	FindingImprovement           FindingCategory = "improvement"
)

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

type FindingOrigin string

const (
	FindingOriginReview FindingOrigin = "review"
	FindingOriginGate   FindingOrigin = "gate"
	FindingOriginHuman  FindingOrigin = "human"
)

func (o FindingOrigin) Valid() bool {
	return o == FindingOriginReview || o == FindingOriginGate || o == FindingOriginHuman
}

type ReviewPhase string

const (
	ReviewPhaseInitial  ReviewPhase = "initial"
	ReviewPhaseTargeted ReviewPhase = "targeted"
)

func (p ReviewPhase) Valid() bool { return p == ReviewPhaseInitial || p == ReviewPhaseTargeted }

type FindingStatus string

const (
	FindingOpen       FindingStatus = "open"
	FindingFixed      FindingStatus = "fixed"
	FindingBacklogged FindingStatus = "backlogged"
	FindingAccepted   FindingStatus = "accepted"
	FindingInvalid    FindingStatus = "invalid"
)

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
