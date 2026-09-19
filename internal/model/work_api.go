package model

import "time"

// WorkCriterionInput carries one literal criteria.toml fragment into a work contract.
type WorkCriterionInput struct {
	Key         string `json:"key"`
	Declaration string `json:"declaration"`
}

// WorkConstraintInput carries one explicitly approved architectural constraint.
type WorkConstraintInput struct {
	Key    string `json:"key"`
	Text   string `json:"text"`
	Source string `json:"source,omitempty"`
}

// WorkBeginRequest defines a complete new work contract without exposing persistence details.
type WorkBeginRequest struct {
	Project             string                `json:"project,omitempty"`
	Workflow            string                `json:"workflow,omitempty"`
	SpecID              string                `json:"spec_id,omitempty"`
	Goal                string                `json:"goal"`
	Scope               []string              `json:"scope"`
	Verification        []VerificationKind    `json:"verification"`
	DevelopmentMethod   DevelopmentMethod     `json:"development_method,omitempty"`
	MaxCorrectionRounds *int                  `json:"max_correction_rounds,omitempty"`
	Criteria            []WorkCriterionInput  `json:"criteria,omitempty"`
	Constraints         []WorkConstraintInput `json:"constraints,omitempty"`
	CreatedBy           string                `json:"created_by,omitempty"`
}

// WorkGetRequest identifies a work aggregate to read.
type WorkGetRequest struct {
	ID string `json:"id"`
}

// WorkLockRequest identifies the draft contract to lock and its acting coordinator.
type WorkLockRequest struct {
	ID string `json:"id"`
	By string `json:"by,omitempty"`
}

// WorkAmendRequest replaces every normative field of a locked contract.
type WorkAmendRequest struct {
	ID                string                `json:"id"`
	Goal              string                `json:"goal"`
	Scope             []string              `json:"scope"`
	Verification      []VerificationKind    `json:"verification"`
	DevelopmentMethod DevelopmentMethod     `json:"development_method"`
	Criteria          []WorkCriterionInput  `json:"criteria,omitempty"`
	Constraints       []WorkConstraintInput `json:"constraints,omitempty"`
	By                string                `json:"by"`
	Reason            string                `json:"reason"`
}

// WorkActionRequest identifies work for a phase-specific action.
type WorkActionRequest struct {
	ID string `json:"id"`
}

// ReviewEvidenceKind identifies the stable reference shape supplied by a reviewer.
type ReviewEvidenceKind string

// ReviewEvidenceKind values are the closed evidence vocabulary for architecture verdicts.
const (
	ReviewEvidenceFile           ReviewEvidenceKind = "file"
	ReviewEvidenceSymbol         ReviewEvidenceKind = "symbol"
	ReviewEvidenceCodegraphQuery ReviewEvidenceKind = "codegraph_query"
)

// Valid reports whether the evidence kind belongs to the closed review vocabulary.
func (k ReviewEvidenceKind) Valid() bool {
	return k == ReviewEvidenceFile || k == ReviewEvidenceSymbol || k == ReviewEvidenceCodegraphQuery
}

// WorkReviewFindingInput carries one general finding without service-owned persistence fields.
type WorkReviewFindingInput struct {
	Category    FindingCategory `json:"category"`
	Severity    Priority        `json:"severity"`
	Description string          `json:"description"`
	Location    string          `json:"location,omitempty"`
	Evidence    string          `json:"evidence"`
}

// WorkArchitectureVerdictInput carries one reviewer verdict for an approved constraint.
type WorkArchitectureVerdictInput struct {
	ConstraintKey string              `json:"constraint_key"`
	Status        DeliveryCheckStatus `json:"status"`
	EvidenceKind  ReviewEvidenceKind  `json:"evidence_kind"`
	Evidence      string              `json:"evidence"`
	Severity      Priority            `json:"severity,omitempty"`
	Description   string              `json:"description,omitempty"`
	Location      string              `json:"location,omitempty"`
}

// WorkReviewRequest binds one explicit initial review report to an exact commit.
type WorkReviewRequest struct {
	ID                   string                         `json:"id"`
	By                   string                         `json:"by"`
	HeadSHA              string                         `json:"head_sha"`
	Findings             []WorkReviewFindingInput       `json:"findings,omitempty"`
	ArchitectureVerdicts []WorkArchitectureVerdictInput `json:"architecture_verdicts,omitempty"`
}

// WorkContractView is the public contract projection; it deliberately omits the internal UUID anchor.
type WorkContractView struct {
	ID                  string             `json:"id"`
	Project             string             `json:"project"`
	SourceID            string             `json:"source_id,omitempty"`
	SourceType          WorkSourceType     `json:"source_type"`
	Status              WorkStatus         `json:"status"`
	Goal                string             `json:"goal"`
	Scope               []string           `json:"scope"`
	Verification        []VerificationKind `json:"verification"`
	DevelopmentMethod   DevelopmentMethod  `json:"development_method"`
	BaseSHA             string             `json:"base_sha,omitempty"`
	ContractRevision    int                `json:"contract_revision"`
	ContractHash        string             `json:"contract_hash,omitempty"`
	CorrectionRounds    int                `json:"correction_rounds"`
	MaxCorrectionRounds int                `json:"max_correction_rounds"`
	DevEvidence         *RedTestEvidence   `json:"dev_evidence,omitempty"`
	CreatedBy           string             `json:"created_by,omitempty"`
	CreatedAt           time.Time          `json:"created_at"`
	UpdatedAt           time.Time          `json:"updated_at"`
	LockedAt            *time.Time         `json:"locked_at,omitempty"`
	CompletedAt         *time.Time         `json:"completed_at,omitempty"`
}

// WorkCriterionView exposes criterion observations without persistence identifiers.
type WorkCriterionView struct {
	Seq         int             `json:"seq"`
	Key         string          `json:"key"`
	Declaration string          `json:"declaration"`
	Status      CriterionStatus `json:"status"`
	Evidence    string          `json:"evidence,omitempty"`
	CheckedBy   string          `json:"checked_by,omitempty"`
	CheckedAt   *time.Time      `json:"checked_at,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

// WorkConstraintView exposes an approved constraint without its persistence identifier.
type WorkConstraintView struct {
	Seq       int       `json:"seq"`
	Key       string    `json:"key"`
	Text      string    `json:"text"`
	Source    string    `json:"source,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// WorkGetResponse is the complete public work aggregate shared by every frontend.
type WorkGetResponse struct {
	Contract    WorkContractView     `json:"contract"`
	Criteria    []WorkCriterionView  `json:"criteria"`
	Constraints []WorkConstraintView `json:"constraints"`
	Findings    []WorkFinding        `json:"findings"`
	History     []WorkHistoryEntry   `json:"history"`
}

// WorkCapabilityResult reports an intentionally unavailable phase without fabricating a verdict.
type WorkCapabilityResult struct {
	Work        WorkGetResponse      `json:"work"`
	Operation   string               `json:"operation"`
	Available   bool                 `json:"available"`
	Performed   bool                 `json:"performed"`
	ReasonCode  string               `json:"reason_code"`
	Reason      string               `json:"reason"`
	Certificate *DeliveryCertificate `json:"certificate,omitempty"`
	Checks      []DeliveryCheck      `json:"checks,omitempty"`
}
