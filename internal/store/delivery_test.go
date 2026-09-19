package store

import (
	"context"
	"errors"
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

	if err := s.InsertInitialReview(context.Background(), InitialReviewWrite{
		Certificate: cert, Checks: checks, Observations: observations, Findings: findings, By: "qa-tester",
	}); err != nil {
		t.Fatal(err)
	}

	after, err := s.GetWorkAggregate(context.Background(), "WORK-001")
	if err != nil {
		t.Fatal(err)
	}
	if after.Contract.Status != model.WorkStatusVerifying || after.Contract.CorrectionRounds != before.Contract.CorrectionRounds || len(after.History) != len(before.History)+1 {
		t.Fatalf("contract/history = %#v / %#v", after.Contract, after.History)
	}
	last := after.History[len(after.History)-1]
	if last.FromStatus != model.WorkStatusImplementing || last.ToStatus != model.WorkStatusVerifying || last.By != "qa-tester" {
		t.Fatalf("history = %#v", last)
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
	if err := s.InsertInitialReview(context.Background(), in); err == nil {
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
			err := s.InsertInitialReview(context.Background(), InitialReviewWrite{Certificate: cert, Checks: checks, By: "qa-tester"})
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
	if err := s.InsertInitialReview(context.Background(), InitialReviewWrite{Certificate: cert, Checks: checks, By: "qa-tester"}); err != nil {
		t.Fatal(err)
	}
	second, secondChecks := deliveryEvaluationFixture(t, s, "WORK-001")
	if err := s.InsertInitialReview(context.Background(), InitialReviewWrite{Certificate: second, Checks: secondChecks, By: "qa-tester"}); !errors.Is(err, model.ErrInvalidWorkTransition) {
		t.Fatalf("second review error = %v", err)
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

func TestInsertDeliveryEvaluation_PreservesSignedObservation(t *testing.T) {
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

func TestInsertDeliveryEvaluation_ValidatesWorkSnapshot(t *testing.T) {
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

func TestInsertDeliveryEvaluation_RollsBackCertificateChecksAndObservations(t *testing.T) {
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

func TestDeliveryCertificateRoundTrip(t *testing.T) {
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

func TestFindingResolutionVariantsAndTargetedPhase(t *testing.T) {
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

func TestDeliveryStore_ValidationErrors(t *testing.T) {
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

func TestFindingStore_AdditionalValidationBranches(t *testing.T) {
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
