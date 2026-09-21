package model

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestDeriveWorkMetric_TerminalPendingUnreadable(t *testing.T) {
	locked, completed := metricTime(10, 0), metricTime(10, 5)
	history := metricHistory(WorkStatusImplementing, WorkStatusVerifying, WorkStatusVerifying, WorkStatusCorrecting)
	for _, status := range []WorkStatus{WorkStatusDone, WorkStatusAbandoned} {
		contract := WorkContract{ID: "WORK-001", Status: status, LockedAt: &locked, CompletedAt: &completed}
		if _, err := DeriveWorkMetric(WorkMetricFacts{Contract: contract, History: history}); !errors.Is(err, ErrInvalidContract) {
			t.Fatalf("%s: %v", status, err)
		}
	}
	active, err := DeriveWorkMetric(WorkMetricFacts{Contract: WorkContract{ID: "WORK-001", Status: WorkStatusCorrecting, LockedAt: &locked}, History: history})
	if err != nil || active.CorrectionPending != 1 {
		t.Fatalf("active=%+v, err=%v", active, err)
	}
}

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
			if tt.contract.LockedAt != nil && (got.StartedAt == nil || !got.StartedAt.Equal(*tt.contract.LockedAt)) {
				t.Fatalf("StartedAt = %v, want locked_at %v", got.StartedAt, tt.contract.LockedAt)
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

	twoEscalations, err := DeriveWorkMetric(WorkMetricFacts{Contract: WorkContract{ID: "WORK-004", Status: WorkStatusImplementing, LockedAt: &locked}, History: metricHistory(
		WorkStatusVerifying, WorkStatusEscalated,
		WorkStatusEscalated, WorkStatusImplementing,
		WorkStatusVerifying, WorkStatusEscalated,
		WorkStatusEscalated, WorkStatusImplementing,
	)})
	if err != nil {
		t.Fatal(err)
	}
	if twoEscalations.Escalations != 2 {
		t.Fatalf("Escalations = %d, want 2", twoEscalations.Escalations)
	}
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

func TestSummarizeWorkMetrics_DurationDistribution(t *testing.T) {
	tests := []struct {
		name       string
		durations  []int64
		wantMedian *int64
		wantP95    *int64
	}{
		{name: "empty"},
		{name: "one", durations: []int64{10}, wantMedian: metricInt64(10), wantP95: metricInt64(10)},
		{name: "odd", durations: []int64{30, 10, 20}, wantMedian: metricInt64(20), wantP95: metricInt64(30)},
		{name: "even", durations: []int64{40, 10, 30, 20}, wantMedian: metricInt64(25), wantP95: metricInt64(40)},
		{name: "nearest rank boundary", durations: []int64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}, wantMedian: metricInt64(55), wantP95: metricInt64(100)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := make([]WorkMetric, 0, len(tt.durations))
			for _, duration := range tt.durations {
				d := duration
				items = append(items, WorkMetric{Status: WorkStatusDone, CycleDurationMs: &d})
			}
			before := append([]int64(nil), tt.durations...)
			got := SummarizeWorkMetrics(items).CycleDuration
			if got.Count != len(tt.durations) || !sameOptionalInt64(got.MedianMs, tt.wantMedian) || !sameOptionalInt64(got.P95Ms, tt.wantP95) {
				t.Fatalf("CycleDuration = %+v, want count=%d median=%v p95=%v", got, len(tt.durations), tt.wantMedian, tt.wantP95)
			}
			if !reflect.DeepEqual(tt.durations, before) {
				t.Fatalf("input durations mutated: got %v, want %v", tt.durations, before)
			}
		})
	}
}

func TestSummarizeWorkMetrics_CountsWholePopulation(t *testing.T) {
	d100, d200, d300, d400 := int64(100), int64(200), int64(300), int64(400)
	items := []WorkMetric{
		{Status: WorkStatusDraft, VerificationEvidence: WorkMetricEvidenceNotStarted},
		{Status: WorkStatusImplementing, Amendments: 1, InitialReviews: 1, VerificationEvidence: WorkMetricEvidencePartial, DeliveryCertificates: 1, PassedCertificates: 1, LocalVerificationDurationMs: 10},
		{Status: WorkStatusEscalated, Escalations: 2, VerificationEvidence: WorkMetricEvidenceComplete, DeliveryCertificates: 2, PassedCertificates: 1, FailedCertificates: 1, LocalVerificationDurationMs: 20},
		{Status: WorkStatusDone, CycleDurationMs: &d100, DoneWithoutCorrectionOrEscalation: true, VerificationEvidence: WorkMetricEvidenceComplete},
		{Status: WorkStatusDone, CycleDurationMs: &d200, DoneAfterCorrection: true, AutomaticCorrections: 1, TargetedReviews: 1, CorrectionCompleted: 1, VerificationEvidence: WorkMetricEvidencePartial},
		{Status: WorkStatusDone, CycleDurationMs: &d300, DoneAfterResume: true, Resumptions: 1, Escalations: 1, VerificationEvidence: WorkMetricEvidenceComplete},
		{Status: WorkStatusAbandoned, CycleDurationMs: &d400, AutomaticCorrections: 2, CorrectionAbandoned: 1, CorrectionPending: 1, VerificationEvidence: WorkMetricEvidenceNotStarted},
		{Status: WorkStatusAbandoned, VerificationEvidence: WorkMetricEvidenceNotStarted},
	}
	got := SummarizeWorkMetrics(items)
	wantInts := map[string][2]int{
		"works": {got.Works, 8}, "draft": {got.Draft, 1}, "active": {got.Active, 2}, "escalated_current": {got.EscalatedCurrent, 1},
		"done": {got.Done, 3}, "abandoned": {got.Abandoned, 2}, "terminal_with_duration": {got.TerminalWithDuration, 4}, "terminal_without_duration": {got.TerminalWithoutDuration, 1},
		"direct_done": {got.DoneWithoutCorrectionOrEscalation, 1}, "corrected_done": {got.DoneAfterCorrection, 1}, "resumed_done": {got.DoneAfterResume, 1},
		"amendments": {got.Amendments, 1}, "initial_reviews": {got.InitialReviews, 1}, "targeted_reviews": {got.TargetedReviews, 1},
		"automatic_corrections": {got.AutomaticCorrections, 3}, "correction_completed": {got.CorrectionCompleted, 1}, "correction_escalated": {got.CorrectionEscalated, 0}, "correction_abandoned": {got.CorrectionAbandoned, 1}, "correction_pending": {got.CorrectionPending, 1},
		"escalations": {got.Escalations, 3}, "resumptions": {got.Resumptions, 1}, "evidence_complete": {got.LocalEvidenceComplete, 3}, "evidence_partial": {got.LocalEvidencePartial, 2}, "evidence_not_started": {got.LocalEvidenceNotStarted, 3},
		"certificates": {got.DeliveryCertificates, 3}, "passed": {got.PassedCertificates, 2}, "failed": {got.FailedCertificates, 1},
	}
	for name, pair := range wantInts {
		if pair[0] != pair[1] {
			t.Errorf("%s = %d, want %d", name, pair[0], pair[1])
		}
	}
	if got.LocalVerificationDurationMs != 30 {
		t.Errorf("LocalVerificationDurationMs = %d, want 30", got.LocalVerificationDurationMs)
	}
	if got.CycleDuration.Count != 4 || got.CycleDuration.TotalMs != 1000 {
		t.Errorf("CycleDuration = %+v, want count 4 total 1000", got.CycleDuration)
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
