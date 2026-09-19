package model

import (
	"fmt"
	"time"
)

// WorkMetricEvidence states how much locally stored certificate evidence is available.
type WorkMetricEvidence string

const (
	// WorkMetricEvidenceNotStarted means neither review history nor local certificates exist.
	WorkMetricEvidenceNotStarted WorkMetricEvidence = "not_started"
	// WorkMetricEvidenceComplete means local certificates cover the minimum reviews proven by history.
	WorkMetricEvidenceComplete WorkMetricEvidence = "complete"
	// WorkMetricEvidencePartial means history proves reviews whose certificates are absent locally.
	WorkMetricEvidencePartial WorkMetricEvidence = "partial"
)

// Valid reports whether evidence belongs to the closed work-metric vocabulary.
func (e WorkMetricEvidence) Valid() bool {
	return e == WorkMetricEvidenceNotStarted || e == WorkMetricEvidenceComplete || e == WorkMetricEvidencePartial
}

// WorkMetricsRequest selects either a project population or an exact ordered cohort.
type WorkMetricsRequest struct {
	Project string   `json:"project,omitempty"`
	IDs     []string `json:"ids,omitempty"`
	Limit   int      `json:"limit,omitempty"`
}

// WorkMetricFacts contains the persisted facts needed to derive one work metric.
type WorkMetricFacts struct {
	Contract     WorkContract
	History      []WorkHistoryEntry
	Certificates []DeliveryCertificate
}

// WorkMetric is the derived, public measurement for one work contract.
type WorkMetric struct {
	ID                                string             `json:"id"`
	SourceID                          string             `json:"source_id,omitempty"`
	SourceType                        WorkSourceType     `json:"source_type"`
	Status                            WorkStatus         `json:"status"`
	ContractRevision                  int                `json:"contract_revision"`
	StartedAt                         *time.Time         `json:"started_at,omitempty"`
	FinishedAt                        *time.Time         `json:"finished_at,omitempty"`
	CycleDurationMs                   *int64             `json:"cycle_duration_ms,omitempty"`
	DurationFinal                     bool               `json:"duration_final"`
	Amendments                        int                `json:"amendments"`
	InitialReviews                    int                `json:"initial_reviews"`
	TargetedReviews                   int                `json:"targeted_reviews"`
	DeliveryCertificates              int                `json:"delivery_certificates"`
	PassedCertificates                int                `json:"passed_certificates"`
	FailedCertificates                int                `json:"failed_certificates"`
	LocalVerificationDurationMs       int64              `json:"local_verification_duration_ms"`
	VerificationEvidence              WorkMetricEvidence `json:"verification_evidence"`
	AutomaticCorrections              int                `json:"automatic_corrections"`
	CorrectionCompleted               int                `json:"correction_completed"`
	CorrectionEscalated               int                `json:"correction_escalated"`
	CorrectionAbandoned               int                `json:"correction_abandoned"`
	CorrectionPending                 int                `json:"correction_pending"`
	Escalations                       int                `json:"escalations"`
	Resumptions                       int                `json:"resumptions"`
	DoneWithoutCorrectionOrEscalation bool               `json:"done_without_correction_or_escalation"`
	DoneAfterCorrection               bool               `json:"done_after_correction"`
	DoneAfterResume                   bool               `json:"done_after_resume"`
}

// DeriveWorkMetric derives one measurement without reading external state or mutating its input.
func DeriveWorkMetric(facts WorkMetricFacts) (WorkMetric, error) {
	contract := facts.Contract
	metric := WorkMetric{
		ID:               contract.ID,
		SourceID:         contract.SourceID,
		SourceType:       contract.SourceType,
		Status:           contract.Status,
		ContractRevision: contract.ContractRevision,
		StartedAt:        contract.LockedAt,
	}
	for _, entry := range facts.History {
		switch {
		case entry.FromStatus == WorkStatusImplementing && entry.ToStatus == WorkStatusVerifying:
			metric.InitialReviews++
		case entry.FromStatus == WorkStatusVerifying && entry.ToStatus == WorkStatusCorrecting:
			metric.AutomaticCorrections++
		case entry.FromStatus == WorkStatusCorrecting && entry.ToStatus == WorkStatusTargetedVerifying:
			metric.TargetedReviews++
		case entry.FromStatus == WorkStatusTargetedVerifying && entry.ToStatus == WorkStatusDone:
			metric.CorrectionCompleted++
		case entry.FromStatus == WorkStatusTargetedVerifying && entry.ToStatus == WorkStatusEscalated:
			metric.CorrectionEscalated++
		case (entry.FromStatus == WorkStatusCorrecting || entry.FromStatus == WorkStatusTargetedVerifying) && entry.ToStatus == WorkStatusAbandoned:
			metric.CorrectionAbandoned++
		}
		if entry.ToStatus == WorkStatusEscalated {
			metric.Escalations++
		}
		if entry.FromStatus == WorkStatusEscalated && entry.ToStatus == WorkStatusImplementing {
			metric.Resumptions++
		}
	}
	metric.CorrectionPending = metric.AutomaticCorrections - metric.CorrectionCompleted - metric.CorrectionEscalated - metric.CorrectionAbandoned
	if metric.CorrectionPending < 0 {
		return WorkMetric{}, fmt.Errorf("%w: correction outcome exceeds automatic corrections", ErrInvalidContract)
	}
	if contract.ContractRevision > 1 {
		metric.Amendments = contract.ContractRevision - 1
	}
	if contract.Status == WorkStatusDone {
		switch {
		case metric.Resumptions > 0:
			metric.DoneAfterResume = true
		case metric.AutomaticCorrections > 0:
			metric.DoneAfterCorrection = true
		case metric.Escalations == 0:
			metric.DoneWithoutCorrectionOrEscalation = true
		}
	}
	for _, certificate := range facts.Certificates {
		if certificate.DurationMs < 0 {
			return WorkMetric{}, fmt.Errorf("%w: certificate duration is negative", ErrInvalidContract)
		}
		if certificate.FinishedAt.Before(certificate.StartedAt) {
			return WorkMetric{}, fmt.Errorf("%w: certificate finished before it started", ErrInvalidContract)
		}
		metric.DeliveryCertificates++
		metric.LocalVerificationDurationMs += certificate.DurationMs
		switch certificate.Verdict {
		case DeliveryVerdictPass:
			metric.PassedCertificates++
		case DeliveryVerdictFail:
			metric.FailedCertificates++
		default:
			return WorkMetric{}, fmt.Errorf("%w: certificate verdict is invalid", ErrInvalidContract)
		}
	}
	reviews := metric.InitialReviews + metric.TargetedReviews
	switch {
	case reviews == 0 && metric.DeliveryCertificates == 0:
		metric.VerificationEvidence = WorkMetricEvidenceNotStarted
	case metric.DeliveryCertificates >= reviews:
		metric.VerificationEvidence = WorkMetricEvidenceComplete
	default:
		metric.VerificationEvidence = WorkMetricEvidencePartial
	}

	if !contract.Status.Terminal() {
		return metric, nil
	}
	if contract.LockedAt == nil {
		return WorkMetric{}, fmt.Errorf("%w: locked_at required for terminal work", ErrInvalidContract)
	}

	var finishedAt *time.Time
	switch contract.Status {
	case WorkStatusDone:
		finishedAt = contract.CompletedAt
	case WorkStatusAbandoned:
		for i := range facts.History {
			entry := &facts.History[i]
			if entry.ToStatus == WorkStatusAbandoned && (finishedAt == nil || entry.At.After(*finishedAt)) {
				at := entry.At
				finishedAt = &at
			}
		}
	}
	if finishedAt == nil {
		return WorkMetric{}, fmt.Errorf("%w: terminal work has no finish time", ErrInvalidContract)
	}
	duration := finishedAt.Sub(*contract.LockedAt).Milliseconds()
	if duration < 0 {
		return WorkMetric{}, fmt.Errorf("%w: cycle duration is negative", ErrInvalidContract)
	}
	metric.FinishedAt = finishedAt
	metric.CycleDurationMs = &duration
	metric.DurationFinal = true
	return metric, nil
}
