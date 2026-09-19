package service

import (
	"context"
	"encoding/json"
	"errors"
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
	svc, _, head := reviewService(t, model.WorkStatusImplementing)
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
