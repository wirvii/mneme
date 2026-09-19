package model

import (
	"errors"
	"testing"
	"time"
)

func validWorkContract() WorkContract {
	return WorkContract{
		ID: "WORK-001", Project: "wirvii/mneme", SourceType: WorkSourceOrganic,
		Status: WorkStatusDraft, Goal: "deliver phase one", Scope: []string{"internal/**"},
		Verification: []VerificationKind{VerificationAcceptance}, DevelopmentMethod: DevelopmentMethodTDD,
		MaxCorrectionRounds: 1, CreatedBy: "backend",
	}
}

func TestWorkContractValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*WorkContract)
	}{
		{"goal", func(c *WorkContract) { c.Goal = "  " }},
		{"scope-empty", func(c *WorkContract) { c.Scope = nil }},
		{"scope-duplicate", func(c *WorkContract) { c.Scope = []string{"a", " a "} }},
		{"verification-empty", func(c *WorkContract) { c.Verification = nil }},
		{"verification-invalid", func(c *WorkContract) { c.Verification = []VerificationKind{"lnt"} }},
		{"verification-duplicate", func(c *WorkContract) { c.Verification = []VerificationKind{VerificationBuild, VerificationBuild} }},
		{"criteria-require-acceptance", func(c *WorkContract) { c.Verification = []VerificationKind{VerificationBuild} }},
		{"method", func(c *WorkContract) { c.DevelopmentMethod = "pair" }},
		{"budget", func(c *WorkContract) { c.MaxCorrectionRounds = -1 }},
		{"organic-source", func(c *WorkContract) { c.SourceID = "SPEC-143" }},
		{"locked-fields", func(c *WorkContract) { c.Status = WorkStatusLocked }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validWorkContract()
			criteria := []WorkCriterion{{Key: "AC1", Declaration: "[[criterion]]\nid = \"AC1\""}}
			tt.mutate(&c)
			if err := c.Validate(criteria, nil); !errors.Is(err, ErrInvalidContract) {
				t.Fatalf("Validate() error = %v, want ErrInvalidContract", err)
			}
		})
	}
}

func TestRedTestEvidencePresenceUsesTakenAt(t *testing.T) {
	if (RedTestEvidence{ExitCode: 1}).Present() {
		t.Fatal("exit code must not mark evidence present")
	}
	if !(RedTestEvidence{ExitCode: 0, TakenAt: time.Unix(1, 0)}).Present() {
		t.Fatal("TakenAt must mark exit-zero evidence present")
	}
}

func TestWorkKeys(t *testing.T) {
	tests := []struct {
		key   string
		valid bool
	}{
		{"", false}, {"-AC", false}, {"A", true}, {"A_b.c-1", true},
		{"12345678901234567890123456789012", true},
		{"123456789012345678901234567890123", false}, {"A B", false}, {"Á", false},
	}
	for _, tt := range tests {
		if got := ValidWorkKey(tt.key); got != tt.valid {
			t.Errorf("ValidWorkKey(%q) = %v, want %v", tt.key, got, tt.valid)
		}
	}
}
