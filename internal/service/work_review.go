package service

import (
	"fmt"
	"strings"

	"github.com/wirvii/mneme/internal/model"
)

func validateWorkReviewRequest(req model.WorkReviewRequest) error {
	for name, value := range map[string]string{"id": req.ID, "by": req.By, "head_sha": req.HeadSHA} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%w: %s: required", model.ErrInvalidContract, name)
		}
	}
	for i, finding := range req.Findings {
		if finding.Category != model.FindingContractViolation && finding.Category != model.FindingRegression && finding.Category != model.FindingDiscovery && finding.Category != model.FindingImprovement {
			return fmt.Errorf("%w: findings[%d].category", model.ErrInvalidContract, i)
		}
		if !finding.Severity.Valid() || strings.TrimSpace(finding.Description) == "" || strings.TrimSpace(finding.Evidence) == "" {
			return fmt.Errorf("%w: findings[%d]", model.ErrInvalidContract, i)
		}
	}
	for i, verdict := range req.ArchitectureVerdicts {
		if strings.TrimSpace(verdict.ConstraintKey) == "" || (verdict.Status != model.DeliveryCheckPass && verdict.Status != model.DeliveryCheckFail) || !verdict.EvidenceKind.Valid() || strings.TrimSpace(verdict.Evidence) == "" {
			return fmt.Errorf("%w: architecture_verdicts[%d]", model.ErrInvalidContract, i)
		}
		if verdict.Status == model.DeliveryCheckFail {
			if !verdict.Severity.Valid() || strings.TrimSpace(verdict.Description) == "" {
				return fmt.Errorf("%w: architecture_verdicts[%d]: failed verdict requires severity and description", model.ErrInvalidContract, i)
			}
			continue
		}
		if verdict.Severity != "" || strings.TrimSpace(verdict.Description) != "" || strings.TrimSpace(verdict.Location) != "" {
			return fmt.Errorf("%w: architecture_verdicts[%d]: passing verdict forbids failure fields", model.ErrInvalidContract, i)
		}
	}
	return nil
}
