package service

import (
	"fmt"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/specreview"
)

// validateReviewDoc parses a fresh-review report before spec_doc_write puts
// it on disk (SPEC-181 D1/D2): review-spec content must be a valid report of
// the spec moment, review-plan content a valid report of the plan moment. Any
// other kind returns nil. The error wraps model.ErrInvalidReviewReport and
// keeps the specreview cause, so a rejected document never touches the disk.
func validateReviewDoc(kind model.SpecDocKind, content string) error {
	var moment specreview.Moment
	switch kind {
	case model.SpecDocKindReviewSpec:
		moment = specreview.MomentSpec
	case model.SpecDocKindReviewPlan:
		moment = specreview.MomentPlan
	default:
		return nil
	}
	if _, err := specreview.Parse([]byte(content), moment); err != nil {
		return fmt.Errorf("service: spec doc write: %w: %w", model.ErrInvalidReviewReport, err)
	}
	return nil
}
