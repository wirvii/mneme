package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/model"
)

func workInReview(t *testing.T, s *SDDStore, id string, status model.WorkStatus) {
	t.Helper()
	createTestWork(t, s, id)
	if err := s.LockWork(context.Background(), id, "base"); err != nil {
		t.Fatal(err)
	}
	if status == model.WorkStatusLocked {
		return
	}
	if err := s.TransitionWork(context.Background(), id, model.WorkStatusLocked, model.WorkStatusImplementing, "b", ""); err != nil {
		t.Fatal(err)
	}
	if status == model.WorkStatusImplementing {
		return
	}
	if err := s.TransitionWork(context.Background(), id, model.WorkStatusImplementing, model.WorkStatusVerifying, "b", ""); err != nil {
		t.Fatal(err)
	}
	if status == model.WorkStatusVerifying {
		return
	}
	if err := s.TransitionWork(context.Background(), id, model.WorkStatusVerifying, model.WorkStatusCorrecting, "b", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.TransitionWork(context.Background(), id, model.WorkStatusCorrecting, model.WorkStatusTargetedVerifying, "b", ""); err != nil {
		t.Fatal(err)
	}
}

func TestResolveFinding_BlockingCannotBeAccepted(t *testing.T) {
	s := newTestSDDStore(t)
	workInReview(t, s, "WORK-001", model.WorkStatusVerifying)
	f := &model.WorkFinding{WorkID: "WORK-001", Category: model.FindingRegression, Severity: model.PriorityHigh, Description: "broke", Origin: model.FindingOriginReview, ReviewPhase: model.ReviewPhaseInitial}
	if err := s.AddFinding(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolveFinding(context.Background(), f.ID, model.FindingAccepted, "human", "accept", ""); !errors.Is(err, model.ErrCannotAcceptBlockingFinding) {
		t.Fatalf("got %v", err)
	}
	list, err := s.ListFindings(context.Background(), "WORK-001")
	if err != nil || len(list) != 1 || list[0].Status != model.FindingOpen {
		t.Fatalf("list=%v err=%v", list, err)
	}
}

func TestResolveFinding_BlockingStatusIsClosed(t *testing.T) {
	for _, status := range []model.FindingStatus{model.FindingAccepted, model.FindingBacklogged} {
		t.Run(string(status), func(t *testing.T) {
			s := newTestSDDStore(t)
			workInReview(t, s, "WORK-001", model.WorkStatusVerifying)
			f := &model.WorkFinding{WorkID: "WORK-001", Category: model.FindingRegression, Severity: model.PriorityHigh, Description: "broke", Origin: model.FindingOriginReview, ReviewPhase: model.ReviewPhaseInitial}
			if err := s.AddFinding(context.Background(), f); err != nil {
				t.Fatal(err)
			}
			err := s.ResolveFinding(context.Background(), f.ID, status, "human", "reason", "BL-999")
			if !errors.Is(err, model.ErrCannotAcceptBlockingFinding) {
				t.Fatalf("status %s error = %v", status, err)
			}
		})
	}
}

func TestCountOpenBlockingFindings_DerivesCategories(t *testing.T) {
	s := newTestSDDStore(t)
	workInReview(t, s, "WORK-001", model.WorkStatusVerifying)
	for _, cat := range []model.FindingCategory{model.FindingContractViolation, model.FindingRegression, model.FindingArchitectureViolation, model.FindingDiscovery, model.FindingImprovement} {
		f := &model.WorkFinding{WorkID: "WORK-001", Category: cat, Severity: model.PriorityMedium, Description: string(cat), Origin: model.FindingOriginReview, ReviewPhase: model.ReviewPhaseInitial}
		if err := s.AddFinding(context.Background(), f); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.CountOpenBlockingFindings(context.Background(), "WORK-001")
	if err != nil || n != 3 {
		t.Fatalf("count=%d err=%v", n, err)
	}
}

func TestInsertDeliveryCertificate_AtomicChecks(t *testing.T) {
	s := newTestSDDStore(t)
	workInReview(t, s, "WORK-001", model.WorkStatusVerifying)
	_, err := s.db.Exec(`CREATE TRIGGER abort_second_delivery_check BEFORE INSERT ON delivery_checks WHEN NEW.seq=2 BEGIN SELECT RAISE(ABORT,'second'); END`)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := deliveryEvaluationFixture(t, s, "WORK-001")
	cert.Verdict = model.DeliveryVerdictFail
	err = s.InsertDeliveryCertificate(context.Background(), cert, []*model.DeliveryCheck{{Kind: "gate", Name: "build", Status: model.DeliveryCheckPass, Effect: model.DeliveryEffectBlocks}, {Kind: "gate", Name: "lint", Status: model.DeliveryCheckPass, Effect: model.DeliveryEffectBlocks}})
	if err == nil {
		t.Fatal("expected trigger error")
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM delivery_certificates WHERE work_id='WORK-001'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("certificates=%d err=%v", n, err)
	}
}

func deliveryEvaluationFixture(t *testing.T, s *SDDStore, workID string) (*model.DeliveryCertificate, []*model.DeliveryCheck) {
	t.Helper()
	work, err := s.GetWork(context.Background(), workID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	cert := &model.DeliveryCertificate{
		Project: work.Project, WorkID: work.ID,
		ContractRevision: work.ContractRevision, ContractHash: work.ContractHash,
		HeadSHA: "head", BaseSHA: work.BaseSHA, Verdict: model.DeliveryVerdictPass,
		StartedAt: now, FinishedAt: now,
	}
	checks := []*model.DeliveryCheck{
		{Kind: "criterion", Name: "AC1", Status: model.DeliveryCheckPass, Effect: model.DeliveryEffectBlocks},
		{Kind: "criterion", Name: "AC2", Status: model.DeliveryCheckPass, Effect: model.DeliveryEffectBlocks},
	}
	return cert, checks
}

func closableWork(t *testing.T, status model.WorkStatus) (*SDDStore, *model.DeliveryCertificate, []*model.DeliveryCheck) {
	t.Helper()
	s := newTestSDDStore(t)
	workInReview(t, s, "WORK-001", status)
	cert, checks := deliveryEvaluationFixture(t, s, "WORK-001")
	if err := s.InsertDeliveryCertificate(context.Background(), cert, checks); err != nil {
		t.Fatal(err)
	}
	return s, cert, checks
}

func TestCompleteWork_ClosesBothVerifyingStates(t *testing.T) {
	for _, status := range []model.WorkStatus{model.WorkStatusVerifying, model.WorkStatusTargetedVerifying} {
		t.Run(string(status), func(t *testing.T) {
			s, cert, checks := closableWork(t, status)
			result, err := s.CompleteWork(context.Background(), "WORK-001", "head", "orchestrator")
			if err != nil {
				t.Fatal(err)
			}
			work, err := s.GetWork(context.Background(), "WORK-001")
			if err != nil || work.Status != model.WorkStatusDone || work.CompletedAt == nil {
				t.Fatalf("work=%#v err=%v", work, err)
			}
			if result.Certificate.ID != cert.ID || len(result.Checks) != len(checks) || result.Checks[0].ID != checks[0].ID {
				t.Fatalf("result=%#v", result)
			}
		})
	}
}

func TestCompleteWork_LatestCertificateWins(t *testing.T) {
	s, _, _ := closableWork(t, model.WorkStatusVerifying)
	cert, checks := deliveryEvaluationFixture(t, s, "WORK-001")
	cert.Verdict = model.DeliveryVerdictFail
	checks[0].Status = model.DeliveryCheckFail
	if err := s.InsertDeliveryCertificate(context.Background(), cert, checks); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteWork(context.Background(), "WORK-001", "head", "orchestrator"); !errors.Is(err, model.ErrInvalidWorkTransition) {
		t.Fatalf("error=%v", err)
	}
}

func TestCompleteWork_RejectsCertificateIdentityAndDirty(t *testing.T) {
	tests := []struct{ name, column, value string }{
		{"dirty", "dirty", "1"},
		{"base", "base_sha", "other"},
		{"hash", "contract_hash", "other"},
		{"revision", "contract_revision", "2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, cert, _ := closableWork(t, model.WorkStatusVerifying)
			if _, err := s.db.Exec(`UPDATE delivery_certificates SET `+tc.column+`=? WHERE id=?`, tc.value, cert.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CompleteWork(context.Background(), "WORK-001", "head", "orchestrator"); !errors.Is(err, model.ErrInvalidWorkTransition) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestCompleteWork_BlockingCategoriesOnly(t *testing.T) {
	tests := []struct {
		name    string
		cats    []model.FindingCategory
		wantErr bool
	}{
		{"blocking", []model.FindingCategory{model.FindingRegression}, true},
		{"non-blocking", []model.FindingCategory{model.FindingDiscovery, model.FindingImprovement}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := closableWork(t, model.WorkStatusVerifying)
			for _, cat := range tc.cats {
				f := &model.WorkFinding{WorkID: "WORK-001", Category: cat, Severity: model.PriorityMedium, Description: string(cat), Evidence: "evidence", Origin: model.FindingOriginReview, ReviewPhase: model.ReviewPhaseInitial}
				if err := s.AddFinding(context.Background(), f); err != nil {
					t.Fatal(err)
				}
			}
			_, err := s.CompleteWork(context.Background(), "WORK-001", "head", "orchestrator")
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestCompleteWork_RollsBackWhenHistoryFails(t *testing.T) {
	s, _, _ := closableWork(t, model.WorkStatusVerifying)
	before, _ := s.GetWork(context.Background(), "WORK-001")
	if _, err := s.db.Exec(`CREATE TRIGGER fail_done_history BEFORE INSERT ON execution_history WHEN NEW.to_status='done' BEGIN SELECT RAISE(FAIL,'forced completion history failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteWork(context.Background(), "WORK-001", "head", "orchestrator"); err == nil {
		t.Fatal("expected history failure")
	}
	after, _ := s.GetWork(context.Background(), "WORK-001")
	if after.Status != before.Status || after.CompletedAt != nil || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("before=%#v after=%#v", before, after)
	}
}

func TestCompleteWork_IsNotRepeatable(t *testing.T) {
	s, _, _ := closableWork(t, model.WorkStatusVerifying)
	if _, err := s.CompleteWork(context.Background(), "WORK-001", "head", "orchestrator"); err != nil {
		t.Fatal(err)
	}
	before, _ := s.GetWorkAggregate(context.Background(), "WORK-001")
	if _, err := s.CompleteWork(context.Background(), "WORK-001", "head", "orchestrator"); !errors.Is(err, model.ErrInvalidWorkTransition) {
		t.Fatalf("error=%v", err)
	}
	after, _ := s.GetWorkAggregate(context.Background(), "WORK-001")
	if !after.Contract.CompletedAt.Equal(*before.Contract.CompletedAt) || len(after.History) != len(before.History) {
		t.Fatalf("before=%#v after=%#v", before.Contract, after.Contract)
	}
}

func TestInsertInitialReview_AtomicallyPersistsEverythingAndTransitions(t *testing.T) {
	s := newTestSDDStore(t)
	workInReview(t, s, "WORK-001", model.WorkStatusImplementing)
	before, err := s.GetWorkAggregate(context.Background(), "WORK-001")
	if err != nil {
		t.Fatal(err)
	}
	cert, checks := deliveryEvaluationFixture(t, s, "WORK-001")
	now := time.Now().UTC()
	observations := []model.CriterionObservation{{
		CriterionID: before.Criteria[0].ID, Status: model.CriterionPass,
		Evidence: "passed", CheckedBy: "reviewer", CheckedAt: now,
	}}
	findings := []*model.WorkFinding{
		{WorkID: "WORK-001", Category: model.FindingContractViolation, Severity: model.PriorityLow, Description: "contract mismatch", Evidence: "review evidence"},
		{WorkID: "WORK-001", Category: model.FindingImprovement, Severity: model.PriorityCritical, Description: "optional cleanup", Evidence: "review evidence"},
	}

	if _, err := s.InsertInitialReview(context.Background(), InitialReviewWrite{
		Certificate: cert, Checks: checks, Observations: observations, Findings: findings, By: "qa-tester",
	}); err != nil {
		t.Fatal(err)
	}

	after, err := s.GetWorkAggregate(context.Background(), "WORK-001")
	if err != nil {
		t.Fatal(err)
	}
	if after.Contract.Status != model.WorkStatusCorrecting || after.Contract.CorrectionRounds != before.Contract.CorrectionRounds+1 || len(after.History) != len(before.History)+2 {
		t.Fatalf("contract/history = %#v / %#v", after.Contract, after.History)
	}
	first := after.History[len(after.History)-2]
	last := after.History[len(after.History)-1]
	if first.FromStatus != model.WorkStatusImplementing || first.ToStatus != model.WorkStatusVerifying || last.FromStatus != model.WorkStatusVerifying || last.ToStatus != model.WorkStatusCorrecting || last.By != "qa-tester" {
		t.Fatalf("history = %#v / %#v", first, last)
	}
	if len(after.Findings) != 2 {
		t.Fatalf("findings = %#v", after.Findings)
	}
	for i, finding := range after.Findings {
		if finding.ID == "" || finding.Seq != i+1 || finding.Origin != model.FindingOriginReview || finding.ReviewPhase != model.ReviewPhaseInitial || finding.Status != model.FindingOpen {
			t.Fatalf("finding %d = %#v", i, finding)
		}
	}
	if cert.ID == "" || checks[0].CertificateID != cert.ID || checks[0].Seq != 1 || checks[1].Seq != 2 {
		t.Fatalf("certificate/checks = %#v / %#v", cert, checks)
	}
	storedChecks, err := s.ListDeliveryChecks(context.Background(), cert.ID)
	if err != nil || len(storedChecks) != 2 || storedChecks[0].Name != "AC1" || storedChecks[1].Name != "AC2" {
		t.Fatalf("stored checks = %#v, %v", storedChecks, err)
	}
	if after.Criteria[0].Status != model.CriterionPass || after.Criteria[0].Evidence != "passed" || after.Criteria[0].CheckedBy != "reviewer" {
		t.Fatalf("criterion = %#v", after.Criteria[0])
	}
}

func TestInsertInitialReview_DecidesFinalStatus(t *testing.T) {
	tests := []struct {
		name      string
		verdict   model.DeliveryVerdict
		finding   model.FindingCategory
		max       int
		rounds    int
		want      model.WorkStatus
		wantRound int
		wantEdges int
	}{
		{name: "green", verdict: model.DeliveryVerdictPass, max: 1, want: model.WorkStatusVerifying, wantEdges: 1},
		{name: "factual red", verdict: model.DeliveryVerdictFail, max: 1, want: model.WorkStatusCorrecting, wantRound: 1, wantEdges: 2},
		{name: "blocking finding", verdict: model.DeliveryVerdictPass, finding: model.FindingRegression, max: 1, want: model.WorkStatusCorrecting, wantRound: 1, wantEdges: 2},
		{name: "critical discovery does not block", verdict: model.DeliveryVerdictPass, finding: model.FindingDiscovery, max: 1, want: model.WorkStatusVerifying, wantEdges: 1},
		{name: "zero budget escalates", verdict: model.DeliveryVerdictFail, max: 0, want: model.WorkStatusEscalated, wantEdges: 2},
		{name: "exhausted budget escalates", verdict: model.DeliveryVerdictFail, max: 1, rounds: 1, want: model.WorkStatusEscalated, wantRound: 1, wantEdges: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestSDDStore(t)
			workInReview(t, s, "WORK-001", model.WorkStatusImplementing)
			if _, err := s.db.Exec(`UPDATE execution_contracts SET max_correction_rounds=?,correction_rounds=? WHERE id='WORK-001'`, tc.max, tc.rounds); err != nil {
				t.Fatal(err)
			}
			before, _ := s.GetWorkAggregate(context.Background(), "WORK-001")
			cert, checks := deliveryEvaluationFixture(t, s, "WORK-001")
			cert.Verdict = tc.verdict
			var findings []*model.WorkFinding
			if tc.finding != "" {
				findings = []*model.WorkFinding{{Category: tc.finding, Severity: model.PriorityCritical, Description: "finding", Evidence: "evidence"}}
			}
			result, err := s.InsertInitialReview(context.Background(), InitialReviewWrite{Certificate: cert, Checks: checks, Findings: findings, By: "qa-tester"})
			if err != nil {
				t.Fatal(err)
			}
			after, _ := s.GetWorkAggregate(context.Background(), "WORK-001")
			if result.Status != tc.want || after.Contract.Status != tc.want || after.Contract.CorrectionRounds != tc.wantRound || len(after.History)-len(before.History) != tc.wantEdges {
				t.Fatalf("result=%#v contract=%#v history delta=%d", result, after.Contract, len(after.History)-len(before.History))
			}
		})
	}
}

func TestInsertInitialReview_RollsBackEveryEffect(t *testing.T) {
	s := newTestSDDStore(t)
	workInReview(t, s, "WORK-001", model.WorkStatusImplementing)
	work, err := s.GetWorkAggregate(context.Background(), "WORK-001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER abort_initial_review_second_finding BEFORE INSERT ON execution_findings WHEN NEW.seq=2 BEGIN SELECT RAISE(ABORT,'second'); END`); err != nil {
		t.Fatal(err)
	}
	cert, checks := deliveryEvaluationFixture(t, s, "WORK-001")
	in := InitialReviewWrite{
		Certificate: cert,
		Checks:      checks,
		Observations: []model.CriterionObservation{{
			CriterionID: work.Criteria[0].ID, Status: model.CriterionFail,
			Evidence: "must roll back", CheckedBy: "reviewer", CheckedAt: time.Now().UTC(),
		}},
		Findings: []*model.WorkFinding{
			{Category: model.FindingRegression, Severity: model.PriorityHigh, Description: "one", Evidence: "e1"},
			{Category: model.FindingImprovement, Severity: model.PriorityLow, Description: "two", Evidence: "e2"},
		},
		By: "qa-tester",
	}
	if _, err := s.InsertInitialReview(context.Background(), in); err == nil {
		t.Fatal("expected trigger error")
	}
	after, err := s.GetWorkAggregate(context.Background(), "WORK-001")
	if err != nil {
		t.Fatal(err)
	}
	var certificates, checksCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM delivery_certificates WHERE work_id='WORK-001'`).Scan(&certificates); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM delivery_checks`).Scan(&checksCount); err != nil {
		t.Fatal(err)
	}
	if len(after.Findings) != 0 || certificates != 0 || checksCount != 0 || after.Criteria[0].Status != model.CriterionPending || after.Contract.Status != model.WorkStatusImplementing || len(after.History) != len(work.History) {
		t.Fatalf("partial review: work=%#v findings=%#v certificates=%d checks=%d", after.Contract, after.Findings, certificates, checksCount)
	}
}

func TestInsertInitialReview_RollsBackFinalDecision(t *testing.T) {
	s := newTestSDDStore(t)
	workInReview(t, s, "WORK-001", model.WorkStatusImplementing)
	before, _ := s.GetWorkAggregate(context.Background(), "WORK-001")
	if _, err := s.db.Exec(`CREATE TRIGGER abort_initial_final_history BEFORE INSERT ON execution_history WHEN NEW.from_status='verifying' BEGIN SELECT RAISE(ABORT,'final history'); END`); err != nil {
		t.Fatal(err)
	}
	cert, checks := deliveryEvaluationFixture(t, s, "WORK-001")
	cert.Verdict = model.DeliveryVerdictFail
	if _, err := s.InsertInitialReview(context.Background(), InitialReviewWrite{Certificate: cert, Checks: checks, Findings: []*model.WorkFinding{{Category: model.FindingRegression, Severity: model.PriorityHigh, Description: "regression", Evidence: "evidence"}}, By: "qa-tester"}); err == nil {
		t.Fatal("expected final history failure")
	}
	after, err := s.GetWorkAggregate(context.Background(), "WORK-001")
	if err != nil {
		t.Fatal(err)
	}
	var certificates, checksCount int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM delivery_certificates WHERE work_id='WORK-001'`).Scan(&certificates)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM delivery_checks`).Scan(&checksCount)
	if after.Contract.Status != model.WorkStatusImplementing || after.Contract.CorrectionRounds != before.Contract.CorrectionRounds || len(after.History) != len(before.History) || len(after.Findings) != 0 || certificates != 0 || checksCount != 0 {
		t.Fatalf("partial final decision: contract=%#v history=%#v findings=%#v certificates=%d checks=%d", after.Contract, after.History, after.Findings, certificates, checksCount)
	}
}

func TestInsertInitialReview_RejectsStaleSnapshotAndSecondReview(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*model.DeliveryCertificate)
	}{
		{"project", func(c *model.DeliveryCertificate) { c.Project = "other" }},
		{"revision", func(c *model.DeliveryCertificate) { c.ContractRevision++ }},
		{"hash", func(c *model.DeliveryCertificate) { c.ContractHash = "stale" }},
		{"base", func(c *model.DeliveryCertificate) { c.BaseSHA = "stale" }},
		{"head", func(c *model.DeliveryCertificate) { c.HeadSHA = "" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestSDDStore(t)
			workInReview(t, s, "WORK-001", model.WorkStatusImplementing)
			cert, checks := deliveryEvaluationFixture(t, s, "WORK-001")
			tc.mutate(cert)
			_, err := s.InsertInitialReview(context.Background(), InitialReviewWrite{Certificate: cert, Checks: checks, By: "qa-tester"})
			if !errors.Is(err, model.ErrInvalidContract) {
				t.Fatalf("error = %v, want ErrInvalidContract", err)
			}
			after, getErr := s.GetWorkAggregate(context.Background(), "WORK-001")
			if getErr != nil || after.Contract.Status != model.WorkStatusImplementing || len(after.Findings) != 0 {
				t.Fatalf("stale review wrote effects: %#v, %v", after, getErr)
			}
		})
	}

	s := newTestSDDStore(t)
	workInReview(t, s, "WORK-001", model.WorkStatusImplementing)
	cert, checks := deliveryEvaluationFixture(t, s, "WORK-001")
	if _, err := s.InsertInitialReview(context.Background(), InitialReviewWrite{Certificate: cert, Checks: checks, By: "qa-tester"}); err != nil {
		t.Fatal(err)
	}
	second, secondChecks := deliveryEvaluationFixture(t, s, "WORK-001")
	if _, err := s.InsertInitialReview(context.Background(), InitialReviewWrite{Certificate: second, Checks: secondChecks, By: "qa-tester"}); !errors.Is(err, model.ErrInvalidWorkTransition) {
		t.Fatalf("second review error = %v", err)
	}
}

func TestInsertInitialReview_RejectsInvalidInputsBeforeEffects(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*InitialReviewWrite)
		wantErr error
	}{
		{"invalid evaluation", func(in *InitialReviewWrite) { in.Certificate = nil }, model.ErrInvalidContract},
		{"missing reviewer", func(in *InitialReviewWrite) { in.By = " " }, model.ErrInvalidContract},
		{"invalid finding", func(in *InitialReviewWrite) { in.Findings = []*model.WorkFinding{nil} }, model.ErrInvalidContract},
		{"missing work", func(in *InitialReviewWrite) { in.Certificate.WorkID = "WORK-MISSING" }, model.ErrWorkNotFound},
		{"invalid observation", func(in *InitialReviewWrite) {
			in.Observations = []model.CriterionObservation{{
				CriterionID: "missing", Status: model.CriterionPass,
				CheckedBy: "qa-tester", CheckedAt: time.Now().UTC(),
			}}
		}, model.ErrInvalidContract},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestSDDStore(t)
			workInReview(t, s, "WORK-001", model.WorkStatusImplementing)
			cert, checks := deliveryEvaluationFixture(t, s, "WORK-001")
			in := InitialReviewWrite{Certificate: cert, Checks: checks, By: "qa-tester"}
			tc.mutate(&in)
			if _, err := s.InsertInitialReview(context.Background(), in); !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			work, err := s.GetWorkAggregate(context.Background(), "WORK-001")
			if err != nil || work.Contract.Status != model.WorkStatusImplementing || len(work.Findings) != 0 {
				t.Fatalf("invalid review wrote effects: work=%#v err=%v", work, err)
			}
		})
	}
}

func resumedInitialReviewFixture(t *testing.T) (*SDDStore, InitialReviewWrite) {
	t.Helper()
	s := newTestSDDStore(t)
	workInReview(t, s, "WORK-001", model.WorkStatusImplementing)
	if _, err := s.db.Exec(`UPDATE execution_contracts SET max_correction_rounds=0 WHERE id='WORK-001'`); err != nil {
		t.Fatal(err)
	}
	first, firstChecks := deliveryEvaluationFixture(t, s, "WORK-001")
	first.Verdict = model.DeliveryVerdictFail
	firstChecks[0].Status = model.DeliveryCheckFail
	_, err := s.InsertInitialReview(context.Background(), InitialReviewWrite{
		Certificate: first, Checks: firstChecks, By: "qa-tester",
		Findings: []*model.WorkFinding{
			{Category: model.FindingRegression, Severity: model.PriorityHigh, Description: "regression", Evidence: "red"},
			{Category: model.FindingImprovement, Severity: model.PriorityLow, Description: "improvement", Evidence: "note"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ResumeWork(context.Background(), "WORK-001", "orchestrator", "another attempt"); err != nil {
		t.Fatal(err)
	}
	next, nextChecks := deliveryEvaluationFixture(t, s, "WORK-001")
	return s, InitialReviewWrite{
		Certificate: next, Checks: nextChecks, By: "qa-tester",
		Resolutions: []model.WorkFindingResolutionInput{{FindingSeq: 1, Status: model.FindingFixed, Evidence: "green"}},
	}
}

func TestInsertInitialReview_ResumedResolutionSet(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SDDStore, *InitialReviewWrite)
		ok     bool
	}{
		{"exact", func(*SDDStore, *InitialReviewWrite) {}, true},
		{"omitted", func(_ *SDDStore, in *InitialReviewWrite) { in.Resolutions = nil }, false},
		{"duplicate", func(_ *SDDStore, in *InitialReviewWrite) { in.Resolutions = append(in.Resolutions, in.Resolutions[0]) }, false},
		{"unknown", func(_ *SDDStore, in *InitialReviewWrite) { in.Resolutions[0].FindingSeq = 99 }, false},
		{"non-blocking", func(_ *SDDStore, in *InitialReviewWrite) { in.Resolutions[0].FindingSeq = 2 }, false},
		{"already-resolved", func(s *SDDStore, in *InitialReviewWrite) {
			if _, err := s.db.Exec(`UPDATE execution_findings SET status='fixed' WHERE work_id='WORK-001' AND seq=1`); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"invalid-state", func(s *SDDStore, in *InitialReviewWrite) {
			if _, err := s.db.Exec(`UPDATE execution_contracts SET status='verifying' WHERE id='WORK-001'`); err != nil {
				t.Fatal(err)
			}
		}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, in := resumedInitialReviewFixture(t)
			tc.mutate(s, &in)
			_, err := s.InsertInitialReview(context.Background(), in)
			if (err == nil) != tc.ok {
				t.Fatalf("error=%v ok=%v", err, tc.ok)
			}
		})
	}
}

func TestInsertInitialReview_ResumedFindingSequence(t *testing.T) {
	s, in := resumedInitialReviewFixture(t)
	in.Findings = []*model.WorkFinding{{Category: model.FindingDiscovery, Severity: model.PriorityLow, Description: "new", Evidence: "evidence"}}
	if _, err := s.InsertInitialReview(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	findings, err := s.ListFindings(context.Background(), "WORK-001")
	if err != nil || len(findings) != 3 || findings[2].Seq != 3 {
		t.Fatalf("findings=%#v err=%v", findings, err)
	}
}

func TestInsertInitialReview_ResumedRollback(t *testing.T) {
	s, in := resumedInitialReviewFixture(t)
	in.Findings = []*model.WorkFinding{{Category: model.FindingDiscovery, Severity: model.PriorityLow, Description: "new", Evidence: "evidence"}}
	before, _ := s.GetWorkAggregate(context.Background(), "WORK-001")
	if _, err := s.db.Exec(`CREATE TRIGGER fail_resumed_certificate BEFORE INSERT ON delivery_certificates BEGIN SELECT RAISE(FAIL,'forced certificate failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertInitialReview(context.Background(), in); err == nil {
		t.Fatal("expected certificate failure")
	}
	after, _ := s.GetWorkAggregate(context.Background(), "WORK-001")
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("before=%#v after=%#v", before, after)
	}
}

func targetedReviewFixture(t *testing.T) (*SDDStore, *model.DeliveryCertificate, TargetedReviewWrite) {
	t.Helper()
	s := newTestSDDStore(t)
	workInReview(t, s, "WORK-001", model.WorkStatusImplementing)
	initial, checks := deliveryEvaluationFixture(t, s, "WORK-001")
	_, err := s.InsertInitialReview(context.Background(), InitialReviewWrite{
		Certificate: initial, Checks: checks,
		Findings: []*model.WorkFinding{
			{Category: model.FindingRegression, Severity: model.PriorityHigh, Description: "regression", Evidence: "red test"},
			{Category: model.FindingImprovement, Severity: model.PriorityLow, Description: "optional", Evidence: "review note"},
		},
		By: "qa-tester",
	})
	if err != nil {
		t.Fatal(err)
	}
	targeted, targetedChecks := deliveryEvaluationFixture(t, s, "WORK-001")
	targeted.HeadSHA = "new-head"
	targetedChecks = append(targetedChecks, &model.DeliveryCheck{Kind: "review", Name: "targeted", Status: model.DeliveryCheckPass, Effect: model.DeliveryEffectMeasures, Detail: `[{"finding_seq":1,"status":"fixed","evidence":"green test"}]`})
	return s, initial, TargetedReviewWrite{
		Certificate: targeted, Checks: targetedChecks, ExpectedCertificateID: initial.ID,
		Resolutions: []model.WorkFindingResolutionInput{{FindingSeq: 1, Status: model.FindingFixed, Evidence: "green test"}},
		By:          "qa-tester",
	}
}

func TestInsertTargetedReview_RequiresExactResolutionSet(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*TargetedReviewWrite)
		valid  bool
	}{
		{name: "exact", valid: true},
		{name: "missing", mutate: func(in *TargetedReviewWrite) { in.Resolutions = nil }},
		{name: "duplicate", mutate: func(in *TargetedReviewWrite) { in.Resolutions = append(in.Resolutions, in.Resolutions[0]) }},
		{name: "unknown", mutate: func(in *TargetedReviewWrite) { in.Resolutions[0].FindingSeq = 99 }},
		{name: "fixed without evidence", mutate: func(in *TargetedReviewWrite) { in.Resolutions[0].Evidence = "" }},
		{name: "invalid without reason", mutate: func(in *TargetedReviewWrite) { in.Resolutions[0].Status = model.FindingInvalid }},
		{name: "backlogged", mutate: func(in *TargetedReviewWrite) { in.Resolutions[0].Status = model.FindingBacklogged }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, _, in := targetedReviewFixture(t)
			if tc.mutate != nil {
				tc.mutate(&in)
			}
			before, _ := s.GetWorkAggregate(context.Background(), "WORK-001")
			result, err := s.InsertTargetedReview(context.Background(), in)
			if tc.valid {
				if err != nil || result.Status != model.WorkStatusTargetedVerifying {
					t.Fatalf("result=%#v error=%v", result, err)
				}
				return
			}
			if !errors.Is(err, model.ErrInvalidContract) {
				t.Fatalf("error=%v", err)
			}
			after, _ := s.GetWorkAggregate(context.Background(), "WORK-001")
			if after.Contract.Status != before.Contract.Status || len(after.History) != len(before.History) || after.Findings[0].Status != model.FindingOpen {
				t.Fatalf("invalid write changed aggregate: %#v", after)
			}
		})
	}
}

func TestInsertTargetedReview_PreservesFindingFacts(t *testing.T) {
	s, _, in := targetedReviewFixture(t)
	before, _ := s.GetWorkAggregate(context.Background(), "WORK-001")
	in.Resolutions[0].Status = model.FindingInvalid
	in.Resolutions[0].Reason = "not reproducible"
	in.Checks[len(in.Checks)-1].Detail = `[{"finding_seq":1,"status":"invalid","evidence":"current run","reason":"not reproducible"}]`
	if _, err := s.InsertTargetedReview(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	after, _ := s.GetWorkAggregate(context.Background(), "WORK-001")
	want, got := before.Findings[0], after.Findings[0]
	if got.Category != want.Category || got.Severity != want.Severity || got.Description != want.Description || got.Location != want.Location || got.Evidence != want.Evidence || got.Origin != want.Origin || got.ReviewPhase != want.ReviewPhase || got.Status != model.FindingInvalid || got.ResolutionReason != "not reproducible" || got.ResolvedBy != "qa-tester" || got.ResolvedAt == nil {
		t.Fatalf("before=%#v after=%#v", want, got)
	}
	checks, err := s.ListDeliveryChecks(context.Background(), in.Certificate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail := checks[len(checks)-1].Detail; !strings.Contains(detail, `"evidence":"current run"`) || !strings.Contains(detail, `"finding_seq":1`) {
		t.Fatalf("targeted resolution detail = %s", detail)
	}
}

func TestInsertTargetedReview_DecidesFinalStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*TargetedReviewWrite)
		want   model.WorkStatus
		edges  int
	}{
		{name: "green", want: model.WorkStatusTargetedVerifying, edges: 1},
		{name: "new blocker", want: model.WorkStatusEscalated, edges: 2, mutate: func(in *TargetedReviewWrite) {
			in.Findings = []*model.WorkFinding{{Category: model.FindingRegression, Severity: model.PriorityHigh, Description: "new regression", Evidence: "new red test"}}
		}},
		{name: "factual red", want: model.WorkStatusEscalated, edges: 2, mutate: func(in *TargetedReviewWrite) { in.Certificate.Verdict = model.DeliveryVerdictFail }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, in := targetedReviewFixture(t)
			before, _ := s.GetWorkAggregate(context.Background(), "WORK-001")
			if tc.mutate != nil {
				tc.mutate(&in)
			}
			result, err := s.InsertTargetedReview(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			after, _ := s.GetWorkAggregate(context.Background(), "WORK-001")
			if result.Status != tc.want || after.Contract.Status != tc.want || after.Contract.CorrectionRounds != before.Contract.CorrectionRounds || len(after.History)-len(before.History) != tc.edges {
				t.Fatalf("result=%#v contract=%#v history delta=%d", result, after.Contract, len(after.History)-len(before.History))
			}
		})
	}
}

func TestInsertTargetedReview_RollsBackEveryEffect(t *testing.T) {
	s, _, in := targetedReviewFixture(t)
	before, _ := s.GetWorkAggregate(context.Background(), "WORK-001")
	in.Certificate.Verdict = model.DeliveryVerdictFail
	if _, err := s.db.Exec(`CREATE TRIGGER abort_targeted_escalation_history BEFORE INSERT ON execution_history WHEN NEW.from_status='targeted_verifying' BEGIN SELECT RAISE(ABORT,'escalation history'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertTargetedReview(context.Background(), in); err == nil {
		t.Fatal("expected escalation history failure")
	}
	after, _ := s.GetWorkAggregate(context.Background(), "WORK-001")
	latest, _ := s.GetLatestDeliveryCertificate(context.Background(), "p", "WORK-001")
	if after.Contract.Status != before.Contract.Status || after.Contract.CorrectionRounds != before.Contract.CorrectionRounds || len(after.History) != len(before.History) || after.Findings[0].Status != model.FindingOpen || latest.ID != in.ExpectedCertificateID {
		t.Fatalf("partial targeted review: before=%#v after=%#v latest=%#v", before, after, latest)
	}
}

func TestInsertTargetedReview_DeliveryPrimitivesRemainAvailable(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	workInReview(t, s, "WORK-001", model.WorkStatusVerifying)

	blocking := &model.WorkFinding{WorkID: "WORK-001", Category: model.FindingRegression, Severity: model.PriorityHigh, Description: "regression", Origin: model.FindingOriginReview, ReviewPhase: model.ReviewPhaseInitial}
	discovery := &model.WorkFinding{WorkID: "WORK-001", Category: model.FindingDiscovery, Severity: model.PriorityLow, Description: "discovery", Origin: model.FindingOriginReview, ReviewPhase: model.ReviewPhaseInitial}
	for _, finding := range []*model.WorkFinding{blocking, discovery} {
		if err := s.AddFinding(ctx, finding); err != nil {
			t.Fatal(err)
		}
	}
	if count, err := s.CountOpenBlockingFindings(ctx, "WORK-001"); err != nil || count != 1 {
		t.Fatalf("blocking count=%d err=%v", count, err)
	}
	if err := s.ResolveFinding(ctx, blocking.ID, model.FindingFixed, "qa", "fixed", ""); err != nil {
		t.Fatal(err)
	}
	findings, err := s.ListFindings(ctx, "WORK-001")
	if err != nil || len(findings) != 2 || findings[0].Status != model.FindingFixed || findings[0].ResolvedAt == nil {
		t.Fatalf("findings=%#v err=%v", findings, err)
	}

	work, err := s.GetWorkAggregate(ctx, "WORK-001")
	if err != nil {
		t.Fatal(err)
	}
	cert, checks := deliveryEvaluationFixture(t, s, "WORK-001")
	cert.Dirty = true
	observation := model.CriterionObservation{CriterionID: work.Criteria[0].ID, Status: model.CriterionPass, Evidence: "verified", CheckedBy: "qa", CheckedAt: time.Now().UTC()}
	if err := s.InsertDeliveryEvaluation(ctx, cert, checks, []model.CriterionObservation{observation}); err != nil {
		t.Fatal(err)
	}
	latest, err := s.GetLatestDeliveryCertificate(ctx, cert.Project, cert.WorkID)
	if err != nil || latest.ID != cert.ID || !latest.Dirty {
		t.Fatalf("latest=%#v err=%v", latest, err)
	}
	storedChecks, err := s.ListDeliveryChecks(ctx, cert.ID)
	if err != nil || len(storedChecks) != len(checks) || storedChecks[1].Seq != 2 {
		t.Fatalf("checks=%#v err=%v", storedChecks, err)
	}
}

func TestInsertTargetedReview_ClosedStoreReturnsErrors(t *testing.T) {
	s := newTestSDDStore(t)
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	finding := &model.WorkFinding{WorkID: "WORK-001", Category: model.FindingDiscovery, Severity: model.PriorityLow, Description: "discovery", Origin: model.FindingOriginReview, ReviewPhase: model.ReviewPhaseInitial}
	if err := s.AddFinding(ctx, finding); err == nil {
		t.Error("AddFinding succeeded on closed store")
	}
	if err := s.ResolveFinding(ctx, "missing", model.FindingFixed, "qa", "", ""); err == nil {
		t.Error("ResolveFinding succeeded on closed store")
	}
	if _, err := s.ListFindings(ctx, "WORK-001"); err == nil {
		t.Error("ListFindings succeeded on closed store")
	}
	if _, err := s.CountOpenBlockingFindings(ctx, "WORK-001"); err == nil {
		t.Error("CountOpenBlockingFindings succeeded on closed store")
	}
	cert := &model.DeliveryCertificate{Project: "p", WorkID: "WORK-001", Verdict: model.DeliveryVerdictPass}
	if err := s.InsertDeliveryCertificate(ctx, cert, nil); err == nil {
		t.Error("InsertDeliveryCertificate succeeded on closed store")
	}
	if _, err := s.GetLatestDeliveryCertificate(ctx, "p", "WORK-001"); err == nil {
		t.Error("GetLatestDeliveryCertificate succeeded on closed store")
	}
	if _, err := s.ListDeliveryChecks(ctx, "missing"); err == nil {
		t.Error("ListDeliveryChecks succeeded on closed store")
	}
}

func TestInsertDeliveryEvaluation_AtomicallyWritesChecksAndObservations(t *testing.T) {
	s := newTestSDDStore(t)
	workInReview(t, s, "WORK-001", model.WorkStatusVerifying)
	work, err := s.GetWorkAggregate(context.Background(), "WORK-001")
	if err != nil {
		t.Fatal(err)
	}
	cert, checks := deliveryEvaluationFixture(t, s, "WORK-001")
	now := time.Now().UTC()
	observations := []model.CriterionObservation{
		{CriterionID: work.Criteria[0].ID, Status: model.CriterionPass, Evidence: "passed", CheckedBy: "verifier", CheckedAt: now},
		{CriterionID: work.Criteria[1].ID, Status: model.CriterionVacuous, Evidence: "already true", CheckedBy: "verifier", CheckedAt: now},
	}

	if err := s.InsertDeliveryEvaluation(context.Background(), cert, checks, observations); err != nil {
		t.Fatal(err)
	}
	if checks[0].Seq != 1 || checks[1].Seq != 2 || checks[0].CertificateID != cert.ID || checks[1].CertificateID != cert.ID {
		t.Fatalf("checks=%#v", checks)
	}
	got, err := s.GetWorkAggregate(context.Background(), "WORK-001")
	if err != nil {
		t.Fatal(err)
	}
	if got.Criteria[0].Status != model.CriterionPass || got.Criteria[0].Evidence != "passed" || got.Criteria[0].CheckedBy != "verifier" || got.Criteria[0].CheckedAt == nil {
		t.Fatalf("criterion 1=%#v", got.Criteria[0])
	}
	if got.Criteria[1].Status != model.CriterionVacuous || got.Criteria[1].Evidence != "already true" || got.Criteria[1].CheckedAt == nil {
		t.Fatalf("criterion 2=%#v", got.Criteria[1])
	}
}

func TestInsertTargetedReview_PreservesSignedObservation(t *testing.T) {
	s := newTestSDDStore(t)
	workInReview(t, s, "WORK-001", model.WorkStatusVerifying)
	work, err := s.GetWorkAggregate(context.Background(), "WORK-001")
	if err != nil {
		t.Fatal(err)
	}
	signedAt := time.Now().UTC().Add(-time.Minute)
	if err := s.UpdateCriterionResult(context.Background(), work.Criteria[0].ID, model.CriterionSigned, "human evidence", "owner", signedAt); err != nil {
		t.Fatal(err)
	}
	cert, checks := deliveryEvaluationFixture(t, s, "WORK-001")
	observation := model.CriterionObservation{CriterionID: work.Criteria[0].ID, Status: model.CriterionFail, Evidence: "current failure", CheckedBy: "verifier", CheckedAt: time.Now().UTC()}
	if err := s.InsertDeliveryEvaluation(context.Background(), cert, checks, []model.CriterionObservation{observation}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetWorkAggregate(context.Background(), "WORK-001")
	if err != nil {
		t.Fatal(err)
	}
	if got.Criteria[0].Status != model.CriterionSigned || got.Criteria[0].Evidence != "human evidence" || got.Criteria[0].CheckedBy != "owner" || got.Criteria[0].CheckedAt == nil || !got.Criteria[0].CheckedAt.Equal(signedAt) {
		t.Fatalf("signed criterion overwritten: %#v", got.Criteria[0])
	}
}

func TestInsertTargetedReview_DeliveryEvaluationValidatesWorkSnapshot(t *testing.T) {
	tests := []struct {
		name   string
		status model.WorkStatus
		mutate func(*model.DeliveryCertificate)
	}{
		{name: "locked state", status: model.WorkStatusLocked},
		{name: "revision", status: model.WorkStatusVerifying, mutate: func(c *model.DeliveryCertificate) { c.ContractRevision++ }},
		{name: "hash", status: model.WorkStatusVerifying, mutate: func(c *model.DeliveryCertificate) { c.ContractHash = "stale" }},
		{name: "base", status: model.WorkStatusVerifying, mutate: func(c *model.DeliveryCertificate) { c.BaseSHA = "stale" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestSDDStore(t)
			workInReview(t, s, "WORK-001", tt.status)
			cert, checks := deliveryEvaluationFixture(t, s, "WORK-001")
			if tt.mutate != nil {
				tt.mutate(cert)
			}
			if err := s.InsertDeliveryEvaluation(context.Background(), cert, checks, nil); !errors.Is(err, model.ErrInvalidWorkTransition) && !errors.Is(err, model.ErrInvalidContract) {
				t.Fatalf("error=%v", err)
			}
			var count int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM delivery_certificates WHERE work_id='WORK-001'`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("certificates=%d err=%v", count, err)
			}
		})
	}
}

func TestInsertTargetedReview_DeliveryEvaluationRollsBackCertificateChecksAndObservations(t *testing.T) {
	s := newTestSDDStore(t)
	workInReview(t, s, "WORK-001", model.WorkStatusVerifying)
	work, err := s.GetWorkAggregate(context.Background(), "WORK-001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER abort_evaluation_second_check BEFORE INSERT ON delivery_checks WHEN NEW.seq=2 BEGIN SELECT RAISE(ABORT,'second'); END`); err != nil {
		t.Fatal(err)
	}
	cert, checks := deliveryEvaluationFixture(t, s, "WORK-001")
	observations := []model.CriterionObservation{{CriterionID: work.Criteria[0].ID, Status: model.CriterionFail, Evidence: "must roll back", CheckedBy: "verifier", CheckedAt: time.Now().UTC()}}
	if err := s.InsertDeliveryEvaluation(context.Background(), cert, checks, observations); err == nil {
		t.Fatal("expected trigger error")
	}
	var certificates, rows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM delivery_certificates WHERE work_id='WORK-001'`).Scan(&certificates); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM delivery_checks`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetWorkAggregate(context.Background(), "WORK-001")
	if err != nil {
		t.Fatal(err)
	}
	if certificates != 0 || rows != 0 || got.Criteria[0].Status != model.CriterionPending || got.Criteria[0].Evidence != "" || got.Criteria[0].CheckedAt != nil {
		t.Fatalf("certificates=%d checks=%d criterion=%#v", certificates, rows, got.Criteria[0])
	}
}

func TestInsertTargetedReview_DeliveryCertificateRoundTrip(t *testing.T) {
	s := newTestSDDStore(t)
	workInReview(t, s, "WORK-001", model.WorkStatusVerifying)
	cert, _ := deliveryEvaluationFixture(t, s, "WORK-001")
	cert.Evidence = "e"
	cert.DurationMs = 12
	cert.Dirty = true
	checks := []*model.DeliveryCheck{{Kind: "gate", Name: "build", Status: model.DeliveryCheckPass, Effect: model.DeliveryEffectBlocks}}
	if err := s.InsertDeliveryCertificate(context.Background(), cert, checks); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetLatestDeliveryCertificate(context.Background(), cert.Project, "WORK-001")
	if err != nil || got.ID != cert.ID || got.Verdict != model.DeliveryVerdictPass || !got.Dirty {
		t.Fatalf("got=%#v err=%v", got, err)
	}
	rows, err := s.ListDeliveryChecks(context.Background(), cert.ID)
	if err != nil || len(rows) != 1 || rows[0].Seq != 1 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
}

func TestInsertTargetedReview_FindingResolutionVariantsAndTargetedPhase(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	workInReview(t, s, "WORK-001", model.WorkStatusVerifying)
	discovery := &model.WorkFinding{WorkID: "WORK-001", Category: model.FindingDiscovery, Severity: model.PriorityLow, Description: "note", Origin: model.FindingOriginHuman, ReviewPhase: model.ReviewPhaseInitial}
	if err := s.AddFinding(ctx, discovery); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolveFinding(ctx, discovery.ID, model.FindingAccepted, "owner", "", ""); !errors.Is(err, model.ErrReasonRequired) {
		t.Fatalf("accepted without reason=%v", err)
	}
	if err := s.ResolveFinding(ctx, discovery.ID, model.FindingAccepted, "owner", "not now", ""); err != nil {
		t.Fatal(err)
	}
	backlog := &model.WorkFinding{WorkID: "WORK-001", Category: model.FindingImprovement, Severity: model.PriorityLow, Description: "later", Origin: model.FindingOriginReview, ReviewPhase: model.ReviewPhaseInitial}
	if err := s.AddFinding(ctx, backlog); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolveFinding(ctx, backlog.ID, model.FindingBacklogged, "owner", "", ""); err == nil {
		t.Fatal("backlogged without id accepted")
	}
	if err := s.ResolveFinding(ctx, backlog.ID, model.FindingBacklogged, "owner", "", "BL-999"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetLatestDeliveryCertificate(ctx, "p", "WORK-001"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("latest missing=%v", err)
	}

	workInReview(t, s, "WORK-002", model.WorkStatusTargetedVerifying)
	targeted := &model.WorkFinding{WorkID: "WORK-002", Category: model.FindingRegression, Severity: model.PriorityHigh, Description: "new regression", Origin: model.FindingOriginReview, ReviewPhase: model.ReviewPhaseTargeted}
	if err := s.AddFinding(ctx, targeted); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFinding(ctx, &model.WorkFinding{WorkID: "WORK-002", Category: model.FindingDiscovery, Severity: model.PriorityLow, Description: "wrong phase", Origin: model.FindingOriginReview, ReviewPhase: model.ReviewPhaseInitial}); !errors.Is(err, model.ErrInvalidWorkTransition) {
		t.Fatalf("wrong phase=%v", err)
	}
}

func TestInsertTargetedReview_DeliveryStoreValidationErrors(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	workInReview(t, s, "WORK-001", model.WorkStatusVerifying)
	if err := s.AddFinding(ctx, &model.WorkFinding{WorkID: "WORK-001", Category: "unknown", Severity: model.PriorityLow, Description: "x", Origin: model.FindingOriginReview, ReviewPhase: model.ReviewPhaseInitial}); !errors.Is(err, model.ErrInvalidContract) {
		t.Errorf("invalid finding=%v", err)
	}
	if err := s.ResolveFinding(ctx, "missing", model.FindingFixed, "qa", "", ""); !errors.Is(err, model.ErrFindingNotFound) {
		t.Errorf("missing finding=%v", err)
	}
	finding := &model.WorkFinding{WorkID: "WORK-001", Category: model.FindingRegression, Severity: model.PriorityHigh, Description: "x", Origin: model.FindingOriginReview, ReviewPhase: model.ReviewPhaseInitial}
	if err := s.AddFinding(ctx, finding); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolveFinding(ctx, finding.ID, model.FindingFixed, "qa", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolveFinding(ctx, finding.ID, model.FindingFixed, "qa", "", ""); err == nil {
		t.Error("resolved finding resolved twice")
	}
	badCert := &model.DeliveryCertificate{Project: "p", WorkID: "WORK-001", Verdict: "unknown"}
	if err := s.InsertDeliveryCertificate(ctx, badCert, nil); !errors.Is(err, model.ErrInvalidContract) {
		t.Errorf("invalid certificate=%v", err)
	}
	now := time.Now().UTC()
	cert := &model.DeliveryCertificate{Project: "p", WorkID: "WORK-001", ContractRevision: 1, ContractHash: "h", HeadSHA: "head", Verdict: model.DeliveryVerdictPass, StartedAt: now, FinishedAt: now}
	if err := s.InsertDeliveryCertificate(ctx, cert, []*model.DeliveryCheck{{Kind: "gate", Name: "x", Status: "unknown", Effect: model.DeliveryEffectBlocks}}); !errors.Is(err, model.ErrInvalidContract) {
		t.Errorf("invalid check=%v", err)
	}
	invalidObservation := []model.CriterionObservation{{CriterionID: "", Status: model.CriterionPass, CheckedAt: now}}
	if err := s.InsertDeliveryEvaluation(ctx, cert, nil, invalidObservation); !errors.Is(err, model.ErrInvalidContract) {
		t.Errorf("invalid observation=%v", err)
	}
	missingCert := *cert
	missingCert.WorkID = "WORK-404"
	if err := s.InsertDeliveryEvaluation(ctx, &missingCert, nil, nil); !errors.Is(err, model.ErrWorkNotFound) {
		t.Errorf("missing work=%v", err)
	}
}

func TestInsertTargetedReview_FindingStoreAdditionalValidationBranches(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	missing := &model.WorkFinding{WorkID: "WORK-404", Category: model.FindingDiscovery, Severity: model.PriorityLow, Description: "x", Origin: model.FindingOriginReview, ReviewPhase: model.ReviewPhaseInitial}
	if err := s.AddFinding(ctx, missing); !errors.Is(err, model.ErrWorkNotFound) {
		t.Errorf("finding missing work=%v", err)
	}
	workInReview(t, s, "WORK-001", model.WorkStatusVerifying)
	f := &model.WorkFinding{WorkID: "WORK-001", Category: model.FindingDiscovery, Severity: model.PriorityLow, Description: "x", Origin: model.FindingOriginReview, ReviewPhase: model.ReviewPhaseInitial}
	if err := s.AddFinding(ctx, f); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolveFinding(ctx, f.ID, model.FindingOpen, "qa", "", ""); !errors.Is(err, model.ErrInvalidContract) {
		t.Errorf("open resolution=%v", err)
	}
	if err := s.ResolveFinding(ctx, f.ID, model.FindingInvalid, "qa", "", ""); !errors.Is(err, model.ErrReasonRequired) {
		t.Errorf("invalid without reason=%v", err)
	}
}
