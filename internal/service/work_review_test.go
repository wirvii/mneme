package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
