package model

import (
	"encoding/json"
	"reflect"
	"testing"
)

func validCompletionInput() CompletionInput {
	return CompletionInput{Status: WorkStatusVerifying, ContractRevision: 1, ContractHash: "contract", HeadSHA: "head", BaseSHA: "base", Certificate: &DeliveryCertificate{Verdict: DeliveryVerdictPass, ContractRevision: 1, ContractHash: "contract", HeadSHA: "head", BaseSHA: "base"}}
}

func TestWorkLifecycleRequests_JSONContract(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
		out  any
	}{
		{"complete", WorkCompleteRequest{ID: "WORK-001", By: "orchestrator"}, `{"id":"WORK-001","by":"orchestrator"}`, &WorkCompleteRequest{}},
		{"resume", WorkResumeRequest{ID: "WORK-001", By: "orchestrator", Reason: "owner approved another attempt"}, `{"id":"WORK-001","by":"orchestrator","reason":"owner approved another attempt"}`, &WorkResumeRequest{}},
	}
	for _, tt := range tests {
		raw, err := json.Marshal(tt.in)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != tt.want {
			t.Fatalf("%s json = %s, want %s", tt.name, raw, tt.want)
		}
		if err := json.Unmarshal(raw, tt.out); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(reflect.ValueOf(tt.out).Elem().Interface(), tt.in) {
			t.Fatalf("%s round trip = %#v, want %#v", tt.name, tt.out, tt.in)
		}
	}
}

func TestCanComplete_EachFactorHasDistinctReason(t *testing.T) {
	base := validCompletionInput()
	if ok, reason := CanComplete(base); !ok || reason != "" {
		t.Fatalf("valid completion=%v %q", ok, reason)
	}
	tests := []struct {
		name   string
		mutate func(*CompletionInput)
	}{
		{"status", func(i *CompletionInput) { i.Status = WorkStatusImplementing }}, {"certificate", func(i *CompletionInput) { i.Certificate = nil }},
		{"verdict", func(i *CompletionInput) { i.Certificate.Verdict = DeliveryVerdictFail }}, {"dirty", func(i *CompletionInput) { i.Certificate.Dirty = true }},
		{"head", func(i *CompletionInput) { i.Certificate.HeadSHA = "other" }}, {"base", func(i *CompletionInput) { i.BaseSHA = "other" }},
		{"hash", func(i *CompletionInput) { i.Certificate.ContractHash = "other" }}, {"revision", func(i *CompletionInput) { i.Certificate.ContractRevision = 2 }},
		{"findings", func(i *CompletionInput) { i.OpenBlockingFindings = 1 }},
	}
	reasons := map[string]bool{}
	for _, tt := range tests {
		in := base
		cert := *base.Certificate
		in.Certificate = &cert
		tt.mutate(&in)
		ok, reason := CanComplete(in)
		if ok || reason == "" {
			t.Errorf("%s got %v %q", tt.name, ok, reason)
		}
		if reasons[reason] {
			t.Errorf("duplicate reason %q", reason)
		}
		reasons[reason] = true
	}
}

func TestMissingConstraintVerdicts(t *testing.T) {
	constraints := []WorkConstraint{{Key: "C2"}, {Key: "C1"}, {Key: "C3"}}
	checks := []DeliveryCheck{{Kind: "architecture", Name: "C2"}, {Kind: "gate", Name: "C1"}}
	got := MissingConstraintVerdicts(constraints, checks)
	if len(got) != 2 || got[0] != "C1" || got[1] != "C3" {
		t.Fatalf("got %v", got)
	}
}

func TestDeriveDeliveryVerdict_MeasuresNeverBlocks(t *testing.T) {
	tests := []struct {
		name   string
		checks []DeliveryCheck
		want   DeliveryVerdict
	}{
		{"empty", nil, DeliveryVerdictFail}, {"no-blocks", []DeliveryCheck{{Status: DeliveryCheckFail, Effect: DeliveryEffectMeasures}}, DeliveryVerdictFail},
		{"measures-with-pass-block", []DeliveryCheck{{Status: DeliveryCheckPass, Effect: DeliveryEffectBlocks}, {Status: DeliveryCheckFail, Effect: DeliveryEffectMeasures}}, DeliveryVerdictPass},
		{"not-reviewed", []DeliveryCheck{{Status: DeliveryCheckNotReviewed, Effect: DeliveryEffectBlocks}}, DeliveryVerdictFail},
		{"blocking-fail", []DeliveryCheck{{Status: DeliveryCheckFail, Effect: DeliveryEffectBlocks}}, DeliveryVerdictFail},
	}
	for _, tt := range tests {
		if got := DeriveDeliveryVerdict(tt.checks); got != tt.want {
			t.Errorf("%s got %s want %s", tt.name, got, tt.want)
		}
	}
}
