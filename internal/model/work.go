package model

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var workKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,31}$`)

// WorkSourceType records whether a work contract was created directly or from a spec.
type WorkSourceType string

const (
	// WorkSourceOrganic identifies work with no originating spec.
	WorkSourceOrganic WorkSourceType = "organic"
	// WorkSourceSpec identifies work whose source is a persisted spec.
	WorkSourceSpec WorkSourceType = "spec"
)

// Valid reports whether the source type preserves the closed source vocabulary.
func (s WorkSourceType) Valid() bool { return s == WorkSourceOrganic || s == WorkSourceSpec }

// DevelopmentMethod names the method whose evidence a contract may record.
type DevelopmentMethod string

const (
	// DevelopmentMethodStandard does not require red-test evidence.
	DevelopmentMethodStandard DevelopmentMethod = "standard"
	// DevelopmentMethodTDD permits red-test evidence for test-driven development.
	DevelopmentMethodTDD DevelopmentMethod = "tdd"
)

// Valid reports whether the method belongs to the closed vocabulary.
func (m DevelopmentMethod) Valid() bool {
	return m == DevelopmentMethodStandard || m == DevelopmentMethodTDD
}

// VerificationKind is a required verification whose presence contributes to the contract hash.
type VerificationKind string

const (
	// VerificationAcceptance requires the declared acceptance criteria to be evaluated.
	VerificationAcceptance VerificationKind = "acceptance"
	// VerificationAffectedTests requires tests affected by the change.
	VerificationAffectedTests VerificationKind = "affected-tests"
	// VerificationBuild requires a successful build.
	VerificationBuild VerificationKind = "build"
	// VerificationLint requires static analysis.
	VerificationLint VerificationKind = "lint"
)

// Valid reports whether the verification kind is recognised.
func (v VerificationKind) Valid() bool {
	return v == VerificationAcceptance || v == VerificationAffectedTests || v == VerificationBuild || v == VerificationLint
}

// CriterionStatus records the most recent observation of a stored criterion.
type CriterionStatus string

const (
	CriterionPending CriterionStatus = "pending"
	CriterionPass    CriterionStatus = "pass"
	CriterionFail    CriterionStatus = "fail"
	CriterionVacuous CriterionStatus = "vacuous"
	CriterionSigned  CriterionStatus = "signed"
)

// Valid reports whether the criterion status belongs to the closed vocabulary.
func (s CriterionStatus) Valid() bool {
	return s == CriterionPending || s == CriterionPass || s == CriterionFail || s == CriterionVacuous || s == CriterionSigned
}

// WorkContract is both the work item and the immutable-after-lock execution contract.
type WorkContract struct {
	ID, UUID, Project     string
	SourceType            WorkSourceType
	SourceID              string
	Status                WorkStatus
	Goal                  string
	Scope                 []string
	Verification          []VerificationKind
	DevelopmentMethod     DevelopmentMethod
	BaseSHA               string
	ContractRevision      int
	ContractHash          string
	CorrectionRounds      int
	MaxCorrectionRounds   int
	DevEvidence           *RedTestEvidence
	CreatedBy             string
	CreatedAt, UpdatedAt  time.Time
	LockedAt, CompletedAt *time.Time
}

// WorkCriterion stores the literal TOML declaration and its latest observation.
type WorkCriterion struct {
	ID, WorkID          string
	Seq                 int
	Key, Declaration    string
	Status              CriterionStatus
	Evidence, CheckedBy string
	CheckedAt           *time.Time
	CreatedAt           time.Time
}

// WorkConstraint stores one named architecture rule; verdicts live on delivery checks.
type WorkConstraint struct {
	ID, WorkID        string
	Seq               int
	Key, Text, Source string
	CreatedAt         time.Time
}

// RedTestEvidence records a red test without using its exit code as a presence marker.
type RedTestEvidence struct {
	Command               []string
	ExitCode              int
	OutputTail, CommitSHA string
	TakenAt               time.Time
}

// Present reports whether evidence was recorded, including legitimate exit-zero evidence.
func (e RedTestEvidence) Present() bool { return !e.TakenAt.IsZero() }

// WorkAggregate is the complete persisted work record needed for lossless export.
type WorkAggregate struct {
	Contract    *WorkContract
	Criteria    []WorkCriterion
	Constraints []WorkConstraint
	Findings    []WorkFinding
	History     []WorkHistoryEntry
}

// WorkHistoryEntry is an immutable audit record of a work status transition.
type WorkHistoryEntry struct {
	ID, WorkID           string
	FromStatus, ToStatus WorkStatus
	ContractRevision     int
	By, Reason           string
	At                   time.Time
}

// AmendWorkRequest carries the only authorised replacement of locked contract content.
type AmendWorkRequest struct {
	WorkID, Goal, By, Reason string
	Scope                    []string
	Verification             []VerificationKind
	DevelopmentMethod        DevelopmentMethod
	Criteria                 []WorkCriterion
	Constraints              []WorkConstraint
}

// ValidWorkKey applies the criterion and constraint key grammar shared with quality criteria.
func ValidWorkKey(key string) bool { return workKeyPattern.MatchString(key) }

// Validate rejects contracts that cannot express a complete and internally consistent definition of done.
func (c WorkContract) Validate(criteria []WorkCriterion, constraints []WorkConstraint) error {
	invalid := func(field, reason string) error { return fmt.Errorf("%w: %s: %s", ErrInvalidContract, field, reason) }
	if strings.TrimSpace(c.Goal) == "" {
		return invalid("goal", "required")
	}
	if !c.SourceType.Valid() {
		return invalid("source_type", "invalid")
	}
	if (c.SourceType == WorkSourceOrganic && c.SourceID != "") || (c.SourceType == WorkSourceSpec && strings.TrimSpace(c.SourceID) == "") {
		return invalid("source_id", "inconsistent with source_type")
	}
	if !c.Status.Valid() {
		return invalid("status", "invalid")
	}
	if len(c.Scope) == 0 {
		return invalid("scope", "required")
	}
	seen := map[string]bool{}
	for _, raw := range c.Scope {
		value := normalizeContractText(raw)
		if value == "" {
			return invalid("scope", "empty pattern")
		}
		if seen[value] {
			return invalid("scope", "duplicate")
		}
		seen[value] = true
	}
	if len(c.Verification) == 0 {
		return invalid("verification", "required")
	}
	seenVerification := map[VerificationKind]bool{}
	hasAcceptance := false
	for _, v := range c.Verification {
		if !v.Valid() {
			return invalid("verification", "invalid")
		}
		if seenVerification[v] {
			return invalid("verification", "duplicate")
		}
		seenVerification[v] = true
		hasAcceptance = hasAcceptance || v == VerificationAcceptance
	}
	if len(criteria) > 0 && !hasAcceptance {
		return invalid("verification", "acceptance required when criteria exist")
	}
	if !c.DevelopmentMethod.Valid() {
		return invalid("development_method", "invalid")
	}
	if c.MaxCorrectionRounds < 0 {
		return invalid("max_correction_rounds", "must be non-negative")
	}
	keys := map[string]bool{}
	for _, criterion := range criteria {
		if !ValidWorkKey(criterion.Key) {
			return invalid("criterion_key", "invalid")
		}
		if keys[criterion.Key] {
			return invalid("criterion_key", "duplicate")
		}
		keys[criterion.Key] = true
		if strings.TrimSpace(criterion.Declaration) == "" {
			return invalid("criterion_declaration", "required")
		}
	}
	keys = map[string]bool{}
	for _, constraint := range constraints {
		if !ValidWorkKey(constraint.Key) {
			return invalid("constraint_key", "invalid")
		}
		if keys[constraint.Key] {
			return invalid("constraint_key", "duplicate")
		}
		keys[constraint.Key] = true
		if strings.TrimSpace(constraint.Text) == "" {
			return invalid("constraint_text", "required")
		}
	}
	if c.Status == WorkStatusDraft {
		if c.BaseSHA != "" || c.ContractHash != "" || c.ContractRevision != 0 {
			return invalid("lock", "draft must be unlocked")
		}
	} else if c.Status != WorkStatusAbandoned {
		if c.BaseSHA == "" || c.ContractHash == "" || c.ContractRevision < 1 {
			return invalid("lock", "locked fields required")
		}
	}
	return nil
}
