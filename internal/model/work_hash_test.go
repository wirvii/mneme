package model

import (
	"bytes"
	"reflect"
	"testing"
)

func TestContractHashInput_Golden(t *testing.T) {
	c := validWorkContract()
	c.Goal = " ship\r\nphase "
	c.Scope = []string{"z/**", " a/** ", "z/**"}
	c.Verification = []VerificationKind{VerificationLint, VerificationAcceptance, VerificationLint}
	criteria := []WorkCriterion{{Key: "B", Declaration: " beta "}, {Key: "A", Declaration: "alpha\r\n"}}
	constraints := []WorkConstraint{{Key: "C2", Text: " two "}, {Key: "C1", Text: "one"}}
	want := []byte("mneme/execution-contract\x1ev1\x1egoal\x1f10\x1fship\nphase\x1e" +
		"scope\x1f2\x1eitem\x1f4\x1fa/**\x1eitem\x1f4\x1fz/**\x1e" +
		"criteria\x1f2\x1ek\x1f1\x1fA\x1ev\x1f5\x1falpha\x1ek\x1f1\x1fB\x1ev\x1f4\x1fbeta\x1e" +
		"constraints\x1f2\x1ek\x1f2\x1fC1\x1ev\x1f3\x1fone\x1ek\x1f2\x1fC2\x1ev\x1f3\x1ftwo\x1e" +
		"verification\x1f2\x1eitem\x1f10\x1facceptance\x1eitem\x1f4\x1flint\x1e" +
		"development_method\x1f3\x1ftdd\x1e")
	if got := ContractHashInput(c, criteria, constraints); !bytes.Equal(got, want) {
		t.Fatalf("canonical bytes:\n got %q\nwant %q", got, want)
	}
}

func TestContractHash_IncludedAndExcludedFields(t *testing.T) {
	c := validWorkContract()
	criteria := []WorkCriterion{{Key: "AC1", Declaration: "one"}}
	constraints := []WorkConstraint{{Key: "C1", Text: "one"}}
	base := ContractHash(c, criteria, constraints)
	included := []struct {
		name   string
		mutate func(*WorkContract, []WorkCriterion, []WorkConstraint)
	}{
		{"goal", func(c *WorkContract, _ []WorkCriterion, _ []WorkConstraint) { c.Goal = "different" }},
		{"scope", func(c *WorkContract, _ []WorkCriterion, _ []WorkConstraint) { c.Scope = []string{"other/**"} }},
		{"criterion", func(_ *WorkContract, c []WorkCriterion, _ []WorkConstraint) { c[0].Declaration = "two" }},
		{"constraint", func(_ *WorkContract, _ []WorkCriterion, c []WorkConstraint) { c[0].Text = "two" }},
		{"verification", func(c *WorkContract, _ []WorkCriterion, _ []WorkConstraint) {
			c.Verification = []VerificationKind{VerificationBuild}
		}},
		{"method", func(c *WorkContract, _ []WorkCriterion, _ []WorkConstraint) {
			c.DevelopmentMethod = DevelopmentMethodStandard
		}},
	}
	for _, tt := range included {
		t.Run(tt.name, func(t *testing.T) {
			cc := c
			cr := append([]WorkCriterion(nil), criteria...)
			co := append([]WorkConstraint(nil), constraints...)
			tt.mutate(&cc, cr, co)
			if ContractHash(cc, cr, co) == base {
				t.Fatal("included field did not change hash")
			}
		})
	}
	cc := c
	cc.CreatedBy = "other"
	cc.MaxCorrectionRounds = 99
	cc.SourceID = "SPEC-999"
	cr := append([]WorkCriterion(nil), criteria...)
	cr[0].Status = CriterionFail
	co := append([]WorkConstraint(nil), constraints...)
	co[0].Source = "other"
	if got := ContractHash(cc, cr, co); got != base {
		t.Fatalf("excluded fields changed hash: %s != %s", got, base)
	}
}

func TestContractHash_NormalizesOrderAndLineEndings(t *testing.T) {
	a := validWorkContract()
	a.Goal = "x\r\ny"
	a.Scope = []string{"b", "a", "b"}
	a.Verification = []VerificationKind{VerificationLint, VerificationBuild}
	b := a
	b.Goal = "x\ny"
	b.Scope = []string{"a", "b"}
	b.Verification = []VerificationKind{VerificationBuild, VerificationLint}
	if ContractHash(a, nil, nil) != ContractHash(b, nil, nil) {
		t.Fatal("normalization changed hash")
	}
}

func TestContractHashInput_IsInjectiveForLineJoinCollision(t *testing.T) {
	a := validWorkContract()
	a.Goal = "a"
	a.Scope = []string{"b"}
	b := validWorkContract()
	b.Goal = "a\nb"
	b.Scope = nil
	if bytes.Equal(ContractHashInput(a, nil, nil), ContractHashInput(b, nil, nil)) {
		t.Fatal("distinct contracts share canonical bytes")
	}
}

func TestContractHashInput_DoesNotMutateCallerChildren(t *testing.T) {
	c := validWorkContract()
	criteria := []WorkCriterion{{Key: "B", Declaration: "beta", Status: CriterionPass, Evidence: "evidence", Seq: 2}, {Key: "A", Declaration: "alpha", Status: CriterionFail, Evidence: "failure", Seq: 1}}
	constraints := []WorkConstraint{{Key: "C2", Text: "second", Source: "source-2", Seq: 2}, {Key: "C1", Text: "first", Source: "source-1", Seq: 1}}
	wantCriteria := append([]WorkCriterion(nil), criteria...)
	wantConstraints := append([]WorkConstraint(nil), constraints...)

	_ = ContractHashInput(c, criteria, constraints)

	if !reflect.DeepEqual(criteria, wantCriteria) {
		t.Fatalf("criteria changed:\n got %#v\nwant %#v", criteria, wantCriteria)
	}
	if !reflect.DeepEqual(constraints, wantConstraints) {
		t.Fatalf("constraints changed:\n got %#v\nwant %#v", constraints, wantConstraints)
	}
}
