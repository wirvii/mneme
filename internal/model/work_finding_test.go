package model

import "testing"

func TestFindingCategoryBlocks(t *testing.T) {
	tests := []struct {
		category FindingCategory
		want     bool
	}{
		{FindingContractViolation, true}, {FindingRegression, true},
		{FindingArchitectureViolation, true}, {FindingDiscovery, false}, {FindingImprovement, false},
	}
	for _, tt := range tests {
		if got := tt.category.Blocks(); got != tt.want {
			t.Errorf("%s.Blocks()=%v", tt.category, got)
		}
	}
}

func TestWorkVocabularies(t *testing.T) {
	valid := []interface{ Valid() bool }{
		WorkSourceOrganic, WorkSourceSpec, DevelopmentMethodStandard, DevelopmentMethodTDD,
		VerificationAcceptance, VerificationAffectedTests, VerificationBuild, VerificationLint,
		CriterionPending, CriterionPass, CriterionFail, CriterionVacuous, CriterionSigned,
		FindingContractViolation, FindingRegression, FindingArchitectureViolation, FindingDiscovery, FindingImprovement,
		FindingOriginReview, FindingOriginGate, FindingOriginHuman, ReviewPhaseInitial, ReviewPhaseTargeted,
		FindingOpen, FindingFixed, FindingBacklogged, FindingAccepted, FindingInvalid,
		DeliveryVerdictPass, DeliveryVerdictFail, DeliveryCheckPass, DeliveryCheckFail, DeliveryCheckSkipped,
		DeliveryCheckNotReviewed, DeliveryEffectBlocks, DeliveryEffectMeasures, DeliveryEffectAbsent, DeliveryEffectStopped,
		ExecutionModelLegacy, ExecutionModelDeliveryV2,
	}
	for _, v := range valid {
		if !v.Valid() {
			t.Errorf("valid vocabulary value rejected: %v", v)
		}
	}
}
