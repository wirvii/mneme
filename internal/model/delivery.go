package model

import (
	"sort"
	"time"
)

// DeliveryVerdict records whether delivery evidence proves the contract passed.
type DeliveryVerdict string

// DeliveryVerdict values are the closed outcomes a delivery certificate may report.
const (
	DeliveryVerdictPass DeliveryVerdict = "pass"
	DeliveryVerdictFail DeliveryVerdict = "fail"
)

// Valid reports whether the verdict belongs to the closed delivery vocabulary.
func (v DeliveryVerdict) Valid() bool { return v == DeliveryVerdictPass || v == DeliveryVerdictFail }

// DeliveryCheckStatus records the outcome of an individual delivery check.
type DeliveryCheckStatus string

// DeliveryCheckStatus values describe how an individual check was evaluated.
const (
	DeliveryCheckPass        DeliveryCheckStatus = "pass"
	DeliveryCheckFail        DeliveryCheckStatus = "fail"
	DeliveryCheckSkipped     DeliveryCheckStatus = "skipped"
	DeliveryCheckNotReviewed DeliveryCheckStatus = "not_reviewed"
)

// Valid reports whether the check status belongs to the closed delivery vocabulary.
func (s DeliveryCheckStatus) Valid() bool {
	return s == DeliveryCheckPass || s == DeliveryCheckFail || s == DeliveryCheckSkipped || s == DeliveryCheckNotReviewed
}

// DeliveryCheckEffect records how a check contributes to a delivery verdict.
type DeliveryCheckEffect string

// DeliveryCheckEffect values classify whether a check blocks, measures, is absent, or was stopped.
const (
	DeliveryEffectBlocks   DeliveryCheckEffect = "blocks"
	DeliveryEffectMeasures DeliveryCheckEffect = "measures"
	DeliveryEffectAbsent   DeliveryCheckEffect = "absent"
	DeliveryEffectStopped  DeliveryCheckEffect = "stopped"
)

// Valid reports whether the check effect belongs to the closed delivery vocabulary.
func (e DeliveryCheckEffect) Valid() bool {
	return e == DeliveryEffectBlocks || e == DeliveryEffectMeasures || e == DeliveryEffectAbsent || e == DeliveryEffectStopped
}

// ExecutionModel selects the engine that owns a spec's execution lifecycle.
type ExecutionModel string

// ExecutionModel values preserve legacy specs while selecting delivery-v2 for new work.
const (
	ExecutionModelLegacy     ExecutionModel = "legacy"
	ExecutionModelDeliveryV2 ExecutionModel = "delivery_v2"
)

// Valid reports whether the execution model belongs to the closed model vocabulary.
func (m ExecutionModel) Valid() bool {
	return m == ExecutionModelLegacy || m == ExecutionModelDeliveryV2
}

// DeliveryCertificate binds one delivery verdict to a contract revision and commit.
type DeliveryCertificate struct {
	ID, Project, WorkID              string
	ContractRevision                 int
	ContractHash, HeadSHA, BaseSHA   string
	Verdict                          DeliveryVerdict
	Dirty                            bool
	Evidence, MnemeVersion           string
	StartedAt, FinishedAt, CreatedAt time.Time
	DurationMs                       int64
}

// DeliveryCheck is one factual row contributing to or measuring a delivery certificate.
type DeliveryCheck struct {
	ID                       int64
	CertificateID            string
	Seq                      int
	Kind, Name               string
	Status                   DeliveryCheckStatus
	Effect                   DeliveryCheckEffect
	Detail                   string
	DurationMs               int64
	OutputSHA256, OutputTail string
	CreatedAt                time.Time
}

// CompletionInput contains every independent fact required to close work.
type CompletionInput struct {
	Status                WorkStatus
	ContractRevision      int
	ContractHash, HeadSHA string
	Certificate           *DeliveryCertificate
	OpenBlockingFindings  int
}

// MissingConstraintVerdicts returns sorted constraint keys absent from architecture checks.
func MissingConstraintVerdicts(constraints []WorkConstraint, checks []DeliveryCheck) []string {
	seen := map[string]bool{}
	for _, c := range checks {
		if c.Kind == "architecture" {
			seen[c.Name] = true
		}
	}
	var out []string
	for _, c := range constraints {
		if !seen[c.Key] {
			out = append(out, c.Key)
		}
	}
	sort.Strings(out)
	return out
}

// DeriveDeliveryVerdict fails closed unless at least one blocking row passes and none fails or remains unreviewed.
func DeriveDeliveryVerdict(checks []DeliveryCheck) DeliveryVerdict {
	count := 0
	for _, c := range checks {
		if c.Effect != DeliveryEffectBlocks {
			continue
		}
		count++
		if c.Status != DeliveryCheckPass {
			return DeliveryVerdictFail
		}
	}
	if count == 0 {
		return DeliveryVerdictFail
	}
	return DeliveryVerdictPass
}

// CanComplete explains the first unmet closing condition in stable order.
func CanComplete(in CompletionInput) (bool, string) {
	if in.Status != WorkStatusVerifying && in.Status != WorkStatusTargetedVerifying {
		return false, "work is not in a verifying state"
	}
	if in.Certificate == nil {
		return false, "delivery certificate is missing"
	}
	if in.Certificate.Verdict != DeliveryVerdictPass {
		return false, "delivery certificate did not pass"
	}
	if in.Certificate.HeadSHA != in.HeadSHA {
		return false, "certificate commit does not match current commit"
	}
	if in.Certificate.ContractHash != in.ContractHash {
		return false, "certificate contract hash does not match"
	}
	if in.Certificate.ContractRevision != in.ContractRevision {
		return false, "certificate contract revision does not match"
	}
	if in.OpenBlockingFindings != 0 {
		return false, "blocking findings remain open"
	}
	return true, ""
}
