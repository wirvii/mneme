package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/quality"
)

func validWorkReviewRequest() model.WorkReviewRequest {
	return model.WorkReviewRequest{
		ID:      "WORK-001",
		By:      "qa-tester",
		HeadSHA: "abc123",
		Findings: []model.WorkReviewFindingInput{{
			Category: model.FindingDiscovery, Severity: model.PriorityLow,
			Description: "useful follow-up", Evidence: "internal/service/work.go:1",
		}},
		ArchitectureVerdicts: []model.WorkArchitectureVerdictInput{{
			ConstraintKey: "dependency-rule", Status: model.DeliveryCheckPass,
			EvidenceKind: model.ReviewEvidenceFile, Evidence: "internal/service/work.go",
		}},
	}
}

func TestWorkReviewRequest_ResolutionJSONContract(t *testing.T) {
	empty, err := json.Marshal(model.WorkReviewRequest{ID: "WORK-001", By: "qa", HeadSHA: "head"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(empty), "resolutions") || strings.Contains(string(empty), "phase") {
		t.Fatalf("empty request JSON = %s", empty)
	}
	resultEmpty, err := json.Marshal(model.WorkCapabilityResult{})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"review_phase", "next_status", "correction_mandate"} {
		if strings.Contains(string(resultEmpty), field) {
			t.Fatalf("empty result contains %q: %s", field, resultEmpty)
		}
	}

	want := model.WorkReviewRequest{
		ID: "WORK-001", By: "qa", HeadSHA: "new-head",
		Resolutions: []model.WorkFindingResolutionInput{{FindingSeq: 7, Status: model.FindingInvalid, Evidence: "test output", Reason: "false positive"}},
	}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"finding_seq", "status", "evidence", "reason"} {
		if !strings.Contains(string(raw), `"`+field+`"`) {
			t.Fatalf("request JSON lacks %q: %s", field, raw)
		}
	}
	var got model.WorkReviewRequest
	if err := json.Unmarshal(raw, &got); err != nil || len(got.Resolutions) != 1 || got.Resolutions[0] != want.Resolutions[0] {
		t.Fatalf("request round trip = %#v, %v", got, err)
	}

	mandate := &model.CorrectionMandate{
		WorkID: "WORK-001", ContractRevision: 2, ContractHash: "hash", CertificateID: "cert",
		CertificateHeadSHA: "old-head", CorrectionRound: 1,
		BlockingFindings: []model.WorkFinding{{Seq: 7}},
		BlockingChecks:   []model.DeliveryCheck{{Kind: "gate", Name: "test", Status: model.DeliveryCheckFail}},
	}
	result := model.WorkCapabilityResult{ReviewPhase: model.ReviewPhaseTargeted, NextStatus: model.WorkStatusEscalated, CorrectionMandate: mandate}
	raw, err = json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip model.WorkCapabilityResult
	if err := json.Unmarshal(raw, &roundTrip); err != nil || roundTrip.ReviewPhase != model.ReviewPhaseTargeted || roundTrip.NextStatus != model.WorkStatusEscalated || roundTrip.CorrectionMandate == nil || roundTrip.CorrectionMandate.CertificateID != "cert" || len(roundTrip.CorrectionMandate.BlockingFindings) != 1 || len(roundTrip.CorrectionMandate.BlockingChecks) != 1 {
		t.Fatalf("result round trip = %#v, %v", roundTrip, err)
	}
}

func TestWorkReview_ValidatesIdentityAndClosedInput(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*model.WorkReviewRequest)
	}{
		{"missing id", func(r *model.WorkReviewRequest) { r.ID = "" }},
		{"missing reviewer", func(r *model.WorkReviewRequest) { r.By = "" }},
		{"missing head", func(r *model.WorkReviewRequest) { r.HeadSHA = "" }},
		{"general architecture violation", func(r *model.WorkReviewRequest) { r.Findings[0].Category = model.FindingArchitectureViolation }},
		{"unknown general category", func(r *model.WorkReviewRequest) { r.Findings[0].Category = "unknown" }},
		{"invalid severity", func(r *model.WorkReviewRequest) { r.Findings[0].Severity = "urgent" }},
		{"missing finding description", func(r *model.WorkReviewRequest) { r.Findings[0].Description = "" }},
		{"missing finding evidence", func(r *model.WorkReviewRequest) { r.Findings[0].Evidence = "" }},
		{"missing constraint key", func(r *model.WorkReviewRequest) { r.ArchitectureVerdicts[0].ConstraintKey = "" }},
		{"not reviewed verdict", func(r *model.WorkReviewRequest) { r.ArchitectureVerdicts[0].Status = model.DeliveryCheckNotReviewed }},
		{"free evidence type", func(r *model.WorkReviewRequest) { r.ArchitectureVerdicts[0].EvidenceKind = "url" }},
		{"missing architecture evidence", func(r *model.WorkReviewRequest) { r.ArchitectureVerdicts[0].Evidence = "" }},
		{"failed without severity", func(r *model.WorkReviewRequest) { r.ArchitectureVerdicts[0].Status = model.DeliveryCheckFail }},
		{"failed without description", func(r *model.WorkReviewRequest) {
			r.ArchitectureVerdicts[0].Status = model.DeliveryCheckFail
			r.ArchitectureVerdicts[0].Severity = model.PriorityHigh
		}},
		{"pass with failure severity", func(r *model.WorkReviewRequest) { r.ArchitectureVerdicts[0].Severity = model.PriorityLow }},
		{"pass with failure description", func(r *model.WorkReviewRequest) { r.ArchitectureVerdicts[0].Description = "not allowed" }},
		{"pass with failure location", func(r *model.WorkReviewRequest) { r.ArchitectureVerdicts[0].Location = "work.go:1" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := validWorkReviewRequest()
			tc.mutate(&req)
			if err := validateWorkReviewRequest(req); !errors.Is(err, model.ErrInvalidContract) {
				t.Fatalf("error = %v, want ErrInvalidContract", err)
			}
		})
	}

	valid := validWorkReviewRequest()
	valid.ArchitectureVerdicts = append(valid.ArchitectureVerdicts, model.WorkArchitectureVerdictInput{
		ConstraintKey: "layers", Status: model.DeliveryCheckFail,
		EvidenceKind: model.ReviewEvidenceSymbol, Evidence: "service.WorkReview",
		Severity: model.PriorityHigh, Description: "calls the store directly", Location: "work_review.go:10",
	})
	if err := validateWorkReviewRequest(valid); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
}

func reviewService(t *testing.T, status model.WorkStatus) (*SDDService, *deliveryRunnerStub, string) {
	t.Helper()
	svc, runner := deliveryVerifyService(t, status)
	head, err := (&quality.Git{RepoDir: svc.repoDir}).HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	return svc, runner, head
}

func reviewRequest(head string) model.WorkReviewRequest {
	return model.WorkReviewRequest{ID: "WORK-001", By: "qa-tester", HeadSHA: head}
}

func commitReviewConstitution(t *testing.T, svc *SDDService) string {
	t.Helper()
	path := filepath.Join(svc.repoDir, constitutionRelPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	constitution := strings.ReplaceAll(deliveryConstitution("build"), "required = false", "required = true")
	if err := os.WriteFile(path, []byte(constitution), 0o600); err != nil {
		t.Fatal(err)
	}
	runVerifyGit(t, svc.repoDir, "add", ".")
	runVerifyGit(t, svc.repoDir, "commit", "-q", "-m", "quality")
	head, err := (&quality.Git{RepoDir: svc.repoDir}).HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	return head
}

func TestWorkReview_RequiresImplementingAndExactCleanHead(t *testing.T) {
	for _, status := range []model.WorkStatus{model.WorkStatusDraft, model.WorkStatusLocked, model.WorkStatusVerifying} {
		t.Run(string(status), func(t *testing.T) {
			svc, runner, head := reviewService(t, status)
			if _, err := svc.WorkReview(context.Background(), reviewRequest(head)); !errors.Is(err, model.ErrInvalidWorkTransition) {
				t.Fatalf("error = %v", err)
			}
			if runner.calls != 0 {
				t.Fatalf("runner calls = %d", runner.calls)
			}
		})
	}

	t.Run("stale head", func(t *testing.T) {
		svc, runner, _ := reviewService(t, model.WorkStatusImplementing)
		if _, err := svc.WorkReview(context.Background(), reviewRequest("stale")); !errors.Is(err, model.ErrInvalidContract) {
			t.Fatalf("error = %v", err)
		}
		if runner.calls != 0 {
			t.Fatalf("runner calls = %d", runner.calls)
		}
	})

	t.Run("dirty untracked path", func(t *testing.T) {
		svc, runner, head := reviewService(t, model.WorkStatusImplementing)
		if err := os.WriteFile(filepath.Join(svc.repoDir, "untracked.txt"), []byte("dirty\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.WorkReview(context.Background(), reviewRequest(head)); !errors.Is(err, model.ErrInvalidContract) {
			t.Fatalf("error = %v", err)
		}
		if runner.calls != 0 {
			t.Fatalf("runner calls = %d", runner.calls)
		}
	})
}

func TestWorkReview_LegacyHasNoEffects(t *testing.T) {
	svc := newTestSDDService(t, "p")
	seedServiceWork(t, svc, "WORK-001")
	if _, err := svc.WorkReview(context.Background(), reviewRequest("head")); !errors.Is(err, model.ErrWorkflowEngineDisabled) {
		t.Fatalf("error = %v", err)
	}
	aggregate, err := svc.store.GetWorkAggregate(context.Background(), "WORK-001")
	if err != nil || aggregate.Contract.Status != model.WorkStatusDraft || len(aggregate.Findings) != 0 || len(aggregate.History) != 0 {
		t.Fatalf("legacy effects = %#v, %v", aggregate, err)
	}
}

func TestWorkReview_PersistsOneCompleteReviewWithoutLaterTransition(t *testing.T) {
	svc, _, _ := reviewService(t, model.WorkStatusImplementing)
	head := commitReviewConstitution(t, svc)
	result, err := svc.WorkReview(context.Background(), reviewRequest(head))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Available || !result.Performed || result.Operation != "review" || result.Certificate == nil || len(result.Checks) == 0 {
		t.Fatalf("result = %#v", result)
	}
	if result.Work.Contract.Status != model.WorkStatusVerifying || result.Work.Contract.CorrectionRounds != 0 {
		t.Fatalf("contract = %#v", result.Work.Contract)
	}
	initial := 0
	for _, check := range result.Checks {
		if check.Kind == "review" && check.Name == "initial" && check.Status == model.DeliveryCheckPass && check.Effect == model.DeliveryEffectMeasures {
			initial++
		}
	}
	if initial != 1 {
		t.Fatalf("review/initial count = %d, checks=%#v", initial, result.Checks)
	}
	if _, err := svc.WorkReview(context.Background(), reviewRequest(head)); !errors.Is(err, model.ErrInvalidWorkTransition) {
		t.Fatalf("second review error = %v", err)
	}
}

func TestWorkReview_InitialDecisionAndMandate(t *testing.T) {
	t.Run("green", func(t *testing.T) {
		svc, _, _ := reviewService(t, model.WorkStatusImplementing)
		head := commitReviewConstitution(t, svc)
		result, err := svc.WorkReview(context.Background(), reviewRequest(head))
		if err != nil {
			t.Fatal(err)
		}
		if result.ReviewPhase != model.ReviewPhaseInitial || result.NextStatus != model.WorkStatusVerifying || result.CorrectionMandate != nil || result.Work.Contract.CorrectionRounds != 0 {
			t.Fatalf("result = %#v", result)
		}
	})
	t.Run("correction", func(t *testing.T) {
		svc, _, head := reviewService(t, model.WorkStatusImplementing)
		req := reviewRequest(head)
		req.Findings = []model.WorkReviewFindingInput{{Category: model.FindingRegression, Severity: model.PriorityHigh, Description: "regression", Evidence: "test output"}}
		result, err := svc.WorkReview(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if result.ReviewPhase != model.ReviewPhaseInitial || result.NextStatus != model.WorkStatusCorrecting || result.CorrectionMandate == nil || result.Work.Contract.CorrectionRounds != 1 {
			t.Fatalf("result = %#v", result)
		}
	})
}

func TestWorkReview_CorrectionMandateIsExact(t *testing.T) {
	svc, head := reviewServiceWithConstraints(t, nil)
	req := reviewRequest(head)
	req.Findings = []model.WorkReviewFindingInput{
		{Category: model.FindingRegression, Severity: model.PriorityHigh, Description: "must fix", Evidence: "red test"},
		{Category: model.FindingImprovement, Severity: model.PriorityCritical, Description: "optional", Evidence: "review note"},
	}
	result, err := svc.WorkReview(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	mandate := result.CorrectionMandate
	if mandate == nil || mandate.WorkID != "WORK-001" || mandate.ContractRevision != result.Work.Contract.ContractRevision || mandate.ContractHash != result.Work.Contract.ContractHash || mandate.CertificateID != result.Certificate.ID || mandate.CertificateHeadSHA != head || mandate.CorrectionRound != 1 {
		t.Fatalf("mandate identity = %#v", mandate)
	}
	if len(mandate.BlockingFindings) != 1 || mandate.BlockingFindings[0].Description != "must fix" {
		t.Fatalf("blocking findings = %#v", mandate.BlockingFindings)
	}
	for _, check := range mandate.BlockingChecks {
		if check.Effect != model.DeliveryEffectBlocks || check.Status == model.DeliveryCheckPass || check.Status == model.DeliveryCheckSkipped {
			t.Fatalf("non-mandatory check included: %#v", check)
		}
	}
	if len(mandate.BlockingChecks) == 0 {
		t.Fatal("mandate has no blocking checks")
	}
}

func targetedReviewService(t *testing.T) (*SDDService, *deliveryRunnerStub, model.WorkReviewRequest, model.WorkCapabilityResult) {
	t.Helper()
	svc, runner, _ := reviewService(t, model.WorkStatusImplementing)
	initialHead := commitReviewConstitution(t, svc)
	initialReq := reviewRequest(initialHead)
	initialReq.Findings = []model.WorkReviewFindingInput{{Category: model.FindingRegression, Severity: model.PriorityHigh, Description: "regression", Evidence: "red test"}}
	initial, err := svc.WorkReview(context.Background(), initialReq)
	if err != nil {
		t.Fatal(err)
	}
	if initial.NextStatus != model.WorkStatusCorrecting {
		t.Fatalf("initial result = %#v", initial)
	}
	if err := os.WriteFile(filepath.Join(svc.repoDir, "tracked.txt"), []byte("corrected\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runVerifyGit(t, svc.repoDir, "add", "tracked.txt")
	runVerifyGit(t, svc.repoDir, "commit", "-q", "-m", "correct")
	head, err := (&quality.Git{RepoDir: svc.repoDir}).HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	runner.calls = 0
	runner.gates = nil
	req := reviewRequest(head)
	req.Resolutions = []model.WorkFindingResolutionInput{{FindingSeq: 1, Status: model.FindingFixed, Evidence: "green test"}}
	return svc, runner, req, initial
}

func resumedReviewService(t *testing.T) (*SDDService, *deliveryRunnerStub, model.WorkReviewRequest) {
	t.Helper()
	svc, runner, targeted, _ := targetedReviewService(t)
	targeted.Findings = []model.WorkReviewFindingInput{{Category: model.FindingRegression, Severity: model.PriorityHigh, Description: "still broken", Evidence: "red again"}}
	result, err := svc.WorkReview(context.Background(), targeted)
	if err != nil {
		t.Fatal(err)
	}
	if result.NextStatus != model.WorkStatusEscalated {
		t.Fatalf("targeted result=%#v", result)
	}
	if _, err := svc.WorkResume(context.Background(), model.WorkResumeRequest{ID: "WORK-001", By: "orchestrator", Reason: "another attempt"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svc.repoDir, "tracked.txt"), []byte("resumed correction\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runVerifyGit(t, svc.repoDir, "add", "tracked.txt")
	runVerifyGit(t, svc.repoDir, "commit", "-q", "-m", "resumed correction")
	head, err := (&quality.Git{RepoDir: svc.repoDir}).HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	runner.calls = 0
	req := reviewRequest(head)
	req.Resolutions = []model.WorkFindingResolutionInput{{FindingSeq: 2, Status: model.FindingFixed, Evidence: "green after resume"}}
	return svc, runner, req
}

func TestWorkReview_ResumedResolutionSet(t *testing.T) {
	svc, runner, req := resumedReviewService(t)
	result, err := svc.WorkReview(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.ReviewPhase != model.ReviewPhaseInitial || result.NextStatus != model.WorkStatusVerifying || result.Work.Contract.Status != model.WorkStatusVerifying {
		t.Fatalf("result=%#v", result)
	}
	if runner.calls == 0 {
		t.Fatal("resumed review did not evaluate delivery")
	}
	if len(result.Work.Findings) != 2 || result.Work.Findings[1].Status != model.FindingFixed {
		t.Fatalf("findings=%#v", result.Work.Findings)
	}
}

func TestWorkReview_OrdinaryRejectsResolutions(t *testing.T) {
	svc, runner, _ := reviewService(t, model.WorkStatusImplementing)
	head := commitReviewConstitution(t, svc)
	req := reviewRequest(head)
	req.Resolutions = []model.WorkFindingResolutionInput{{FindingSeq: 1, Status: model.FindingFixed, Evidence: "not applicable"}}
	if _, err := svc.WorkReview(context.Background(), req); !errors.Is(err, model.ErrInvalidContract) {
		t.Fatalf("error=%v", err)
	}
	if runner.calls != 0 {
		t.Fatalf("runner calls=%d", runner.calls)
	}
}

func TestWorkReview_ResumedCycleBounded(t *testing.T) {
	t.Run("red gets one correction", func(t *testing.T) {
		svc, _, req := resumedReviewService(t)
		req.Findings = []model.WorkReviewFindingInput{{Category: model.FindingRegression, Severity: model.PriorityHigh, Description: "new regression", Evidence: "new red"}}
		result, err := svc.WorkReview(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if result.NextStatus != model.WorkStatusCorrecting || result.Work.Contract.CorrectionRounds != 1 {
			t.Fatalf("result=%#v", result)
		}
	})
	t.Run("green remains verifying", func(t *testing.T) {
		svc, _, req := resumedReviewService(t)
		result, err := svc.WorkReview(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if result.NextStatus != model.WorkStatusVerifying || result.Work.Contract.Status == model.WorkStatusDone {
			t.Fatalf("result=%#v", result)
		}
	})
}

func TestWorkReview_InfersPhaseFromPersistedState(t *testing.T) {
	initialSvc, runner, head := reviewService(t, model.WorkStatusImplementing)
	badInitial := reviewRequest(head)
	badInitial.Resolutions = []model.WorkFindingResolutionInput{{FindingSeq: 1, Status: model.FindingFixed, Evidence: "e"}}
	if _, err := initialSvc.WorkReview(context.Background(), badInitial); !errors.Is(err, model.ErrInvalidContract) || runner.calls != 0 {
		t.Fatalf("initial resolutions error=%v calls=%d", err, runner.calls)
	}

	svc, _, req, _ := targetedReviewService(t)
	result, err := svc.WorkReview(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.ReviewPhase != model.ReviewPhaseTargeted || result.NextStatus != model.WorkStatusTargetedVerifying {
		t.Fatalf("targeted result = %#v", result)
	}
	for _, status := range []model.WorkStatus{model.WorkStatusVerifying, model.WorkStatusTargetedVerifying, model.WorkStatusEscalated, model.WorkStatusDone} {
		other, otherRunner, otherHead := reviewService(t, status)
		if _, err := other.WorkReview(context.Background(), reviewRequest(otherHead)); !errors.Is(err, model.ErrInvalidWorkTransition) || otherRunner.calls != 0 {
			t.Fatalf("status %s error=%v calls=%d", status, err, otherRunner.calls)
		}
	}
}

func TestWorkReview_TargetedRequiresNewCleanExactHead(t *testing.T) {
	svc, runner, _ := reviewService(t, model.WorkStatusImplementing)
	head := commitReviewConstitution(t, svc)
	initial := reviewRequest(head)
	initial.Findings = []model.WorkReviewFindingInput{{Category: model.FindingRegression, Severity: model.PriorityHigh, Description: "regression", Evidence: "red"}}
	if _, err := svc.WorkReview(context.Background(), initial); err != nil {
		t.Fatal(err)
	}
	runner.calls = 0
	req := reviewRequest(head)
	req.Resolutions = []model.WorkFindingResolutionInput{{FindingSeq: 1, Status: model.FindingFixed, Evidence: "claimed green"}}
	if _, err := svc.WorkReview(context.Background(), req); !errors.Is(err, model.ErrInvalidContract) {
		t.Fatalf("same head error = %v", err)
	}
	if runner.calls != 0 {
		t.Fatalf("runner calls = %d", runner.calls)
	}
}

func TestWorkReview_TargetedGreenStopsBeforeCompletion(t *testing.T) {
	svc, _, req, _ := targetedReviewService(t)
	result, err := svc.WorkReview(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.NextStatus != model.WorkStatusTargetedVerifying || result.Work.Contract.Status != model.WorkStatusTargetedVerifying || result.Work.Contract.CompletedAt != nil || result.Work.Contract.CorrectionRounds != 1 {
		t.Fatalf("result = %#v", result)
	}
}

func TestWorkReview_RejectsSecondCorrection(t *testing.T) {
	for _, escalate := range []bool{false, true} {
		t.Run(fmt.Sprintf("escalated=%t", escalate), func(t *testing.T) {
			svc, _, req, _ := targetedReviewService(t)
			if escalate {
				req.Findings = []model.WorkReviewFindingInput{{Category: model.FindingRegression, Severity: model.PriorityHigh, Description: "new regression", Evidence: "red"}}
			}
			if _, err := svc.WorkReview(context.Background(), req); err != nil {
				t.Fatal(err)
			}
			before, _ := svc.store.GetWorkAggregate(context.Background(), "WORK-001")
			if _, err := svc.WorkReview(context.Background(), req); !errors.Is(err, model.ErrInvalidWorkTransition) {
				t.Fatalf("second targeted review error = %v", err)
			}
			after, _ := svc.store.GetWorkAggregate(context.Background(), "WORK-001")
			if after.Contract.CorrectionRounds != before.Contract.CorrectionRounds || len(after.History) != len(before.History) || len(after.Findings) != len(before.Findings) {
				t.Fatalf("second review changed aggregate: before=%#v after=%#v", before, after)
			}
		})
	}
}

func TestWorkReview_TargetedProducesCompleteCertificate(t *testing.T) {
	svc, _, req, _ := targetedReviewService(t)
	result, err := svc.WorkReview(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"gate", "review", "tdd-evidence"} {
		if len(reviewChecksByKind(result.Checks, kind)) == 0 {
			t.Fatalf("targeted certificate lacks %s: %#v", kind, result.Checks)
		}
	}
	reviewRows := reviewChecksByKind(result.Checks, "review")
	if len(reviewRows) != 2 || reviewRows[0].Name != "targeted" || strings.Contains(reviewRows[0].Detail, `"initial"`) || !strings.Contains(reviewRows[0].Detail, `"finding_seq":1`) {
		t.Fatalf("targeted review rows = %#v", reviewRows)
	}
}

func TestWorkReview_TargetedArchitectureIsNeverInherited(t *testing.T) {
	svc, _ := reviewServiceWithConstraints(t, []model.WorkConstraint{{Key: "layers", Text: "inward"}})
	initialHead := commitReviewConstitution(t, svc)
	initialReq := reviewRequest(initialHead)
	initialReq.Findings = []model.WorkReviewFindingInput{{Category: model.FindingRegression, Severity: model.PriorityHigh, Description: "regression", Evidence: "red"}}
	initialReq.ArchitectureVerdicts = []model.WorkArchitectureVerdictInput{{ConstraintKey: "layers", Status: model.DeliveryCheckPass, EvidenceKind: model.ReviewEvidenceFile, Evidence: "internal/service/work.go"}}
	if _, err := svc.WorkReview(context.Background(), initialReq); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svc.repoDir, "tracked.txt"), []byte("corrected\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runVerifyGit(t, svc.repoDir, "add", "tracked.txt")
	runVerifyGit(t, svc.repoDir, "commit", "-q", "-m", "correct")
	head, _ := (&quality.Git{RepoDir: svc.repoDir}).HeadSHA()
	req := reviewRequest(head)
	req.Resolutions = []model.WorkFindingResolutionInput{{FindingSeq: 1, Status: model.FindingFixed, Evidence: "green"}}
	result, err := svc.WorkReview(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	architecture := reviewChecksByKind(result.Checks, "architecture")
	if len(architecture) != 1 || architecture[0].Name != "layers" || architecture[0].Status != model.DeliveryCheckNotReviewed || result.NextStatus != model.WorkStatusEscalated {
		t.Fatalf("architecture=%#v result=%#v", architecture, result)
	}
}

func TestWorkReview_TargetedNewBlockerEscalates(t *testing.T) {
	svc, _, req, _ := targetedReviewService(t)
	req.Findings = []model.WorkReviewFindingInput{{Category: model.FindingRegression, Severity: model.PriorityHigh, Description: "new regression", Evidence: "new red test"}}
	result, err := svc.WorkReview(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.NextStatus != model.WorkStatusEscalated || len(result.Work.Findings) != 2 || result.Work.Findings[1].ReviewPhase != model.ReviewPhaseTargeted || result.Work.Findings[1].Status != model.FindingOpen {
		t.Fatalf("result = %#v", result)
	}
}

func TestWorkReview_TargetedFactualFailureEscalates(t *testing.T) {
	svc, runner, req, _ := targetedReviewService(t)
	runner.results = []quality.GateResult{{Status: quality.GateStatusFail, OutputTail: "red"}}
	result, err := svc.WorkReview(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.NextStatus != model.WorkStatusEscalated || result.Certificate.Verdict != model.DeliveryVerdictFail || len(result.Work.Findings) != 1 {
		t.Fatalf("result = %#v", result)
	}
}

func reviewServiceWithConstraints(t *testing.T, constraints []model.WorkConstraint) (*SDDService, string) {
	t.Helper()
	svc := deliveryWorkService(t)
	repo, head := deliveryVerifyRepo(t)
	contract := &model.WorkContract{
		ID: "WORK-001", Project: svc.project, SourceType: model.WorkSourceOrganic,
		Status: model.WorkStatusDraft, Goal: "review", Scope: []string{"internal/**"},
		Verification: []model.VerificationKind{model.VerificationBuild}, DevelopmentMethod: model.DevelopmentMethodStandard,
		MaxCorrectionRounds: 1,
	}
	if err := svc.store.CreateWork(context.Background(), contract, nil, constraints); err != nil {
		t.Fatal(err)
	}
	if err := svc.store.LockWorkAndStart(context.Background(), contract.ID, head, "coordinator"); err != nil {
		t.Fatal(err)
	}
	svc.WithRepoDir(repo)
	svc.WithDeliveryVerifier(func(int) quality.Runner { return &deliveryRunnerStub{} }, "test-version")
	return svc, head
}

func reviewChecksByKind(checks []model.DeliveryCheck, kind string) []model.DeliveryCheck {
	var out []model.DeliveryCheck
	for _, check := range checks {
		if check.Kind == kind {
			out = append(out, check)
		}
	}
	return out
}

func TestWorkReview_ArchitectureKeysEvidenceAndMissingVerdicts(t *testing.T) {
	constraints := []model.WorkConstraint{{Key: "layers", Text: "inward"}, {Key: "ports", Text: "service only"}, {Key: "storage", Text: "sqlite"}, {Key: "unreviewed", Text: "explicit"}}
	svc, head := reviewServiceWithConstraints(t, constraints)
	req := reviewRequest(head)
	req.ArchitectureVerdicts = []model.WorkArchitectureVerdictInput{
		{ConstraintKey: "storage", Status: model.DeliveryCheckPass, EvidenceKind: model.ReviewEvidenceCodegraphQuery, Evidence: "callers:Store"},
		{ConstraintKey: "layers", Status: model.DeliveryCheckPass, EvidenceKind: model.ReviewEvidenceFile, Evidence: "internal/service/work_review.go"},
		{ConstraintKey: "ports", Status: model.DeliveryCheckPass, EvidenceKind: model.ReviewEvidenceSymbol, Evidence: "SDDService.WorkReview"},
	}
	result, err := svc.WorkReview(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	rows := reviewChecksByKind(result.Checks, "architecture")
	if len(rows) != 4 || rows[0].Name != "layers" || rows[1].Name != "ports" || rows[2].Name != "storage" || rows[3].Name != "unreviewed" || rows[3].Status != model.DeliveryCheckNotReviewed || rows[3].Effect != model.DeliveryEffectBlocks || result.Certificate.Verdict != model.DeliveryVerdictFail {
		t.Fatalf("architecture rows=%#v certificate=%#v", rows, result.Certificate)
	}
	wantKinds := map[string]model.ReviewEvidenceKind{"layers": model.ReviewEvidenceFile, "ports": model.ReviewEvidenceSymbol, "storage": model.ReviewEvidenceCodegraphQuery}
	for _, row := range rows[:3] {
		var detail struct {
			EvidenceKind model.ReviewEvidenceKind `json:"evidence_kind"`
			Evidence     string                   `json:"evidence"`
		}
		if err := json.Unmarshal([]byte(row.Detail), &detail); err != nil || detail.EvidenceKind != wantKinds[row.Name] || detail.Evidence == "" {
			t.Fatalf("detail %s = %#v, %v", row.Name, detail, err)
		}
	}

	for _, mutate := range []func(*model.WorkReviewRequest){
		func(r *model.WorkReviewRequest) { r.ArchitectureVerdicts[0].ConstraintKey = "unknown" },
		func(r *model.WorkReviewRequest) {
			r.ArchitectureVerdicts = append(r.ArchitectureVerdicts, r.ArchitectureVerdicts[0])
		},
	} {
		other, otherHead := reviewServiceWithConstraints(t, constraints)
		invalid := reviewRequest(otherHead)
		invalid.ArchitectureVerdicts = append([]model.WorkArchitectureVerdictInput(nil), req.ArchitectureVerdicts...)
		mutate(&invalid)
		if _, err := other.WorkReview(context.Background(), invalid); !errors.Is(err, model.ErrInvalidContract) {
			t.Fatalf("invalid architecture error = %v", err)
		}
		agg, _ := other.store.GetWorkAggregate(context.Background(), "WORK-001")
		if agg.Contract.Status != model.WorkStatusImplementing || len(agg.Findings) != 0 {
			t.Fatalf("invalid report wrote effects: %#v", agg)
		}
	}

	without, withoutHead := reviewServiceWithConstraints(t, nil)
	zero, err := without.WorkReview(context.Background(), reviewRequest(withoutHead))
	if err != nil || len(reviewChecksByKind(zero.Checks, "architecture")) != 0 {
		t.Fatalf("zero constraints = %#v, %v", zero.Checks, err)
	}
}

func TestWorkReview_ArchitectureFailureCreatesLinkedFinding(t *testing.T) {
	svc, head := reviewServiceWithConstraints(t, []model.WorkConstraint{{Key: "layers", Text: "inward"}})
	req := reviewRequest(head)
	req.ArchitectureVerdicts = []model.WorkArchitectureVerdictInput{{
		ConstraintKey: "layers", Status: model.DeliveryCheckFail, EvidenceKind: model.ReviewEvidenceSymbol,
		Evidence: "service.BadDependency", Severity: model.PriorityHigh, Description: "dependency points outward", Location: "internal/service/bad.go:10",
	}}
	result, err := svc.WorkReview(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Work.Findings) != 1 {
		t.Fatalf("findings = %#v", result.Work.Findings)
	}
	finding := result.Work.Findings[0]
	if finding.Category != model.FindingArchitectureViolation || finding.Status != model.FindingOpen || finding.Origin != model.FindingOriginReview || finding.ReviewPhase != model.ReviewPhaseInitial || !strings.Contains(finding.Location, "layers") || finding.Evidence != "service.BadDependency" {
		t.Fatalf("finding = %#v", finding)
	}
}

func TestWorkReview_BlockingFindingsAffectCertificateByCategory(t *testing.T) {
	tests := []struct {
		name     string
		category model.FindingCategory
		severity model.Priority
		want     model.DeliveryCheckStatus
	}{
		{"low contract blocks", model.FindingContractViolation, model.PriorityLow, model.DeliveryCheckFail},
		{"critical improvement measures", model.FindingImprovement, model.PriorityCritical, model.DeliveryCheckPass},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, head := reviewServiceWithConstraints(t, nil)
			req := reviewRequest(head)
			req.Findings = []model.WorkReviewFindingInput{{Category: tc.category, Severity: tc.severity, Description: "finding", Evidence: "evidence"}}
			result, err := svc.WorkReview(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			rows := reviewChecksByKind(result.Checks, "review")
			if len(rows) != 2 || rows[1].Name != "open-blocking-findings" || rows[1].Status != tc.want || rows[1].Effect != model.DeliveryEffectBlocks {
				t.Fatalf("review rows = %#v", rows)
			}
		})
	}
}

func TestWorkReview_ProducesOneCompleteCertificate(t *testing.T) {
	svc, head := reviewServiceWithConstraints(t, []model.WorkConstraint{{Key: "layers", Text: "inward"}})
	req := reviewRequest(head)
	req.ArchitectureVerdicts = []model.WorkArchitectureVerdictInput{{ConstraintKey: "layers", Status: model.DeliveryCheckPass, EvidenceKind: model.ReviewEvidenceFile, Evidence: "internal/service/work_review.go"}}
	result, err := svc.WorkReview(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"configuration", "architecture", "review", "tdd-evidence"} {
		if len(reviewChecksByKind(result.Checks, kind)) == 0 {
			t.Fatalf("complete certificate lacks %s: %#v", kind, result.Checks)
		}
	}
	latest, err := svc.store.GetLatestDeliveryCertificate(context.Background(), "p", "WORK-001")
	if err != nil || latest.ID != result.Certificate.ID {
		t.Fatalf("latest certificate = %#v, %v", latest, err)
	}
}
