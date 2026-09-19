package service

import (
	"errors"
	"testing"

	"github.com/wirvii/mneme/internal/model"
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
