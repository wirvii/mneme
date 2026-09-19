package model

import (
	"testing"
	"time"
)

func metricTime(hour, minute int) time.Time {
	return time.Date(2026, time.September, 19, hour, minute, 0, 0, time.UTC)
}

func TestDeriveWorkMetric_Duration(t *testing.T) {
	locked := metricTime(10, 0)
	doneAt := metricTime(10, 5)
	abandonedAt := metricTime(10, 3)
	earlier := metricTime(9, 59)

	tests := []struct {
		name      string
		contract  WorkContract
		history   []WorkHistoryEntry
		wantMs    *int64
		wantFinal bool
		wantErr   bool
	}{
		{name: "done", contract: WorkContract{ID: "WORK-001", Status: WorkStatusDone, LockedAt: &locked, CompletedAt: &doneAt}, wantMs: metricInt64(300000), wantFinal: true},
		{name: "abandoned", contract: WorkContract{ID: "WORK-002", Status: WorkStatusAbandoned, LockedAt: &locked}, history: []WorkHistoryEntry{{FromStatus: WorkStatusImplementing, ToStatus: WorkStatusAbandoned, At: abandonedAt}}, wantMs: metricInt64(180000), wantFinal: true},
		{name: "draft", contract: WorkContract{ID: "WORK-003", Status: WorkStatusDraft}},
		{name: "active", contract: WorkContract{ID: "WORK-004", Status: WorkStatusImplementing, LockedAt: &locked}},
		{name: "completed before locked", contract: WorkContract{ID: "WORK-005", Status: WorkStatusDone, LockedAt: &locked, CompletedAt: &earlier}, wantErr: true},
		{name: "terminal without required end", contract: WorkContract{ID: "WORK-006", Status: WorkStatusAbandoned, LockedAt: &locked}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DeriveWorkMetric(WorkMetricFacts{Contract: tt.contract, History: tt.history})
			if (err != nil) != tt.wantErr {
				t.Fatalf("DeriveWorkMetric() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if !sameOptionalInt64(got.CycleDurationMs, tt.wantMs) {
				t.Fatalf("CycleDurationMs = %v, want %v", got.CycleDurationMs, tt.wantMs)
			}
			if got.DurationFinal != tt.wantFinal {
				t.Fatalf("DurationFinal = %v, want %v", got.DurationFinal, tt.wantFinal)
			}
		})
	}
}

func TestDeriveWorkMetric_LifecycleCounters(t *testing.T) {
	locked := metricTime(10, 0)
	doneAt := metricTime(10, 30)
	history := metricHistory(
		WorkStatusDraft, WorkStatusLocked,
		WorkStatusLocked, WorkStatusImplementing,
		WorkStatusImplementing, WorkStatusVerifying,
		WorkStatusVerifying, WorkStatusCorrecting,
		WorkStatusCorrecting, WorkStatusTargetedVerifying,
		WorkStatusTargetedVerifying, WorkStatusEscalated,
		WorkStatusEscalated, WorkStatusImplementing,
		WorkStatusImplementing, WorkStatusVerifying,
		WorkStatusVerifying, WorkStatusCorrecting,
		WorkStatusCorrecting, WorkStatusTargetedVerifying,
		WorkStatusTargetedVerifying, WorkStatusDone,
	)
	got, err := DeriveWorkMetric(WorkMetricFacts{Contract: WorkContract{
		ID: "WORK-001", Status: WorkStatusDone, LockedAt: &locked, CompletedAt: &doneAt, CorrectionRounds: 1,
	}, History: history})
	if err != nil {
		t.Fatal(err)
	}
	assertMetricCounters(t, got, []int{2, 2, 2, 1, 1, 0, 0, 1, 1})

	abandoned := WorkMetricFacts{Contract: WorkContract{ID: "WORK-002", Status: WorkStatusAbandoned, LockedAt: &locked}, History: metricHistory(
		WorkStatusImplementing, WorkStatusVerifying,
		WorkStatusVerifying, WorkStatusCorrecting,
		WorkStatusCorrecting, WorkStatusAbandoned,
	)}
	abandoned.Contract.CompletedAt = nil
	got, err = DeriveWorkMetric(abandoned)
	if err != nil {
		t.Fatal(err)
	}
	assertMetricCounters(t, got, []int{1, 0, 1, 0, 0, 1, 0, 0, 0})

	pending := WorkMetricFacts{Contract: WorkContract{ID: "WORK-003", Status: WorkStatusCorrecting, LockedAt: &locked}, History: metricHistory(
		WorkStatusImplementing, WorkStatusVerifying,
		WorkStatusVerifying, WorkStatusCorrecting,
	)}
	got, err = DeriveWorkMetric(pending)
	if err != nil {
		t.Fatal(err)
	}
	assertMetricCounters(t, got, []int{1, 0, 1, 0, 0, 0, 1, 0, 0})
}

func TestDeriveWorkMetric_AmendmentsIgnoreResume(t *testing.T) {
	locked := metricTime(10, 0)
	got, err := DeriveWorkMetric(WorkMetricFacts{Contract: WorkContract{
		ID: "WORK-001", Status: WorkStatusImplementing, LockedAt: &locked, ContractRevision: 3,
	}, History: metricHistory(WorkStatusEscalated, WorkStatusImplementing)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Amendments != 2 {
		t.Fatalf("Amendments = %d, want 2", got.Amendments)
	}
}

func TestDeriveWorkMetric_DoneCategoriesAreDisjoint(t *testing.T) {
	locked := metricTime(10, 0)
	doneAt := metricTime(10, 30)
	tests := []struct {
		name    string
		history []WorkHistoryEntry
		want    [3]bool
	}{
		{name: "direct", history: metricHistory(WorkStatusImplementing, WorkStatusVerifying, WorkStatusVerifying, WorkStatusDone), want: [3]bool{true, false, false}},
		{name: "corrected", history: metricHistory(WorkStatusImplementing, WorkStatusVerifying, WorkStatusVerifying, WorkStatusCorrecting, WorkStatusCorrecting, WorkStatusTargetedVerifying, WorkStatusTargetedVerifying, WorkStatusDone), want: [3]bool{false, true, false}},
		{name: "resumed and corrected", history: metricHistory(WorkStatusEscalated, WorkStatusImplementing, WorkStatusImplementing, WorkStatusVerifying, WorkStatusVerifying, WorkStatusCorrecting, WorkStatusCorrecting, WorkStatusTargetedVerifying, WorkStatusTargetedVerifying, WorkStatusDone), want: [3]bool{false, false, true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DeriveWorkMetric(WorkMetricFacts{Contract: WorkContract{ID: "WORK-001", Status: WorkStatusDone, LockedAt: &locked, CompletedAt: &doneAt}, History: tt.history})
			if err != nil {
				t.Fatal(err)
			}
			actual := [3]bool{got.DoneWithoutCorrectionOrEscalation, got.DoneAfterCorrection, got.DoneAfterResume}
			if actual != tt.want {
				t.Fatalf("done categories = %v, want %v", actual, tt.want)
			}
		})
	}

	active, err := DeriveWorkMetric(WorkMetricFacts{Contract: WorkContract{ID: "WORK-002", Status: WorkStatusImplementing, LockedAt: &locked}})
	if err != nil {
		t.Fatal(err)
	}
	if active.DoneWithoutCorrectionOrEscalation || active.DoneAfterCorrection || active.DoneAfterResume {
		t.Fatalf("non-done work has a done category: %+v", active)
	}
}

func TestDeriveWorkMetric_LocalEvidence(t *testing.T) {
	locked := metricTime(10, 0)
	certStart := metricTime(10, 10)
	certFinish := metricTime(10, 11)
	certificates := []DeliveryCertificate{
		{Verdict: DeliveryVerdictPass, StartedAt: certStart, FinishedAt: certFinish, DurationMs: 10},
		{Verdict: DeliveryVerdictPass, StartedAt: certStart, FinishedAt: certFinish, DurationMs: 20},
		{Verdict: DeliveryVerdictFail, StartedAt: certStart, FinishedAt: certFinish, DurationMs: 30},
	}
	tests := []struct {
		name         string
		history      []WorkHistoryEntry
		certs        []DeliveryCertificate
		wantEvidence WorkMetricEvidence
	}{
		{name: "not started", wantEvidence: WorkMetricEvidenceNotStarted},
		{name: "partial", history: metricHistory(WorkStatusImplementing, WorkStatusVerifying, WorkStatusImplementing, WorkStatusVerifying), certs: certificates[:1], wantEvidence: WorkMetricEvidencePartial},
		{name: "complete with extra certificates", history: metricHistory(WorkStatusImplementing, WorkStatusVerifying), certs: certificates, wantEvidence: WorkMetricEvidenceComplete},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DeriveWorkMetric(WorkMetricFacts{Contract: WorkContract{ID: "WORK-001", Status: WorkStatusImplementing, LockedAt: &locked}, History: tt.history, Certificates: tt.certs})
			if err != nil {
				t.Fatal(err)
			}
			if got.VerificationEvidence != tt.wantEvidence {
				t.Fatalf("VerificationEvidence = %q, want %q", got.VerificationEvidence, tt.wantEvidence)
			}
			if len(tt.certs) == 3 {
				if got.DeliveryCertificates != 3 || got.PassedCertificates != 2 || got.FailedCertificates != 1 || got.LocalVerificationDurationMs != 60 {
					t.Fatalf("certificate metrics = %+v", got)
				}
			}
		})
	}

	for _, tt := range []struct {
		name string
		cert DeliveryCertificate
	}{
		{name: "negative duration", cert: DeliveryCertificate{Verdict: DeliveryVerdictPass, StartedAt: certStart, FinishedAt: certFinish, DurationMs: -1}},
		{name: "finished before started", cert: DeliveryCertificate{Verdict: DeliveryVerdictPass, StartedAt: certFinish, FinishedAt: certStart, DurationMs: 1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DeriveWorkMetric(WorkMetricFacts{Contract: WorkContract{ID: "WORK-002", Status: WorkStatusImplementing, LockedAt: &locked}, Certificates: []DeliveryCertificate{tt.cert}})
			if err == nil {
				t.Fatal("DeriveWorkMetric() error = nil, want invalid certificate error")
			}
		})
	}
}

func metricHistory(edges ...WorkStatus) []WorkHistoryEntry {
	history := make([]WorkHistoryEntry, 0, len(edges)/2)
	for i := 0; i < len(edges); i += 2 {
		history = append(history, WorkHistoryEntry{FromStatus: edges[i], ToStatus: edges[i+1], At: metricTime(10, i+1)})
	}
	return history
}

func assertMetricCounters(t *testing.T, got WorkMetric, want []int) {
	t.Helper()
	values := []int{got.InitialReviews, got.TargetedReviews, got.AutomaticCorrections, got.CorrectionCompleted, got.CorrectionEscalated, got.CorrectionAbandoned, got.CorrectionPending, got.Escalations, got.Resumptions}
	for i := range want {
		if values[i] != want[i] {
			t.Fatalf("counter %d = %d, want %d; metric = %+v", i, values[i], want[i], got)
		}
	}
}

func metricInt64(value int64) *int64 { return &value }

func sameOptionalInt64(got, want *int64) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return *got == *want
}
