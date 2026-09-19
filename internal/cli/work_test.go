package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/model"
)

func TestNewWorkCmdRegistersSevenOperations(t *testing.T) {
	cmd := newWorkCmd()
	want := map[string]bool{
		"begin": false, "get": false, "lock": false, "amend": false,
		"review": false, "verify": false, "complete": false,
	}
	for _, child := range cmd.Commands() {
		if _, ok := want[child.Name()]; ok {
			want[child.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("work subcommand %q is not registered", name)
		}
	}
	if len(cmd.Commands()) != len(want) {
		t.Fatalf("work has %d subcommands, want %d", len(cmd.Commands()), len(want))
	}
}

func TestWorkBeginAndAmendRequireInput(t *testing.T) {
	for _, operation := range []string{"begin", "amend"} {
		t.Run(operation, func(t *testing.T) {
			cmd := newWorkCmd()
			cmd.SetArgs([]string{operation})
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), `required flag(s) "input" not set`) {
				t.Fatalf("Execute() error = %v, want missing --input", err)
			}
		})
	}
}

func TestReadWorkInputUsesOnlyExplicitSource(t *testing.T) {
	cmd := newWorkCmd()
	cmd.SetIn(strings.NewReader(`{"goal":"stdin"}`))

	got, err := readWorkInput(cmd, "-")
	if err != nil {
		t.Fatalf("read stdin: %v", err)
	}
	if string(got) != `{"goal":"stdin"}` {
		t.Fatalf("stdin = %q", got)
	}

	path := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(path, []byte(`{"goal":"file"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = readWorkInput(cmd, path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(got) != `{"goal":"file"}` {
		t.Fatalf("file = %q", got)
	}
}

func TestWorkCLIInputLimitAndExplicitStdin(t *testing.T) {
	cmd := newWorkCmd()
	cmd.SetIn(strings.NewReader(`{"goal":"stdin"}`))
	got, err := readWorkInput(cmd, "-")
	if err != nil || string(got) != `{"goal":"stdin"}` {
		t.Fatalf("explicit stdin = %q, %v", got, err)
	}

	cmd.SetIn(bytes.NewReader(make([]byte, workInputLimit+1)))
	if _, err := readWorkInput(cmd, "-"); err == nil || !strings.Contains(err.Error(), "10 MiB") {
		t.Fatalf("readWorkInput() error = %v, want size limit", err)
	}
}

func TestWriteWorkCapabilityStartsUnavailable(t *testing.T) {
	var out bytes.Buffer
	result := model.WorkCapabilityResult{
		Operation: "review", Available: false, Performed: false,
		ReasonCode: "not_available", Reason: "deferred",
	}
	if err := writeWorkCapability(&out, result); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "NO DISPONIBLE") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestWriteWorkVerifyCapabilitySummarizesCertificate(t *testing.T) {
	var out bytes.Buffer
	result := model.WorkCapabilityResult{
		Operation: "verify", Available: true, Performed: true,
		Work:        model.WorkGetResponse{Contract: model.WorkContractView{ID: "WORK-007", ContractRevision: 3, ContractHash: "fedcba9876543210"}},
		Certificate: &model.DeliveryCertificate{Verdict: model.DeliveryVerdictFail, HeadSHA: "0123456789abcdef"},
		Checks: []model.DeliveryCheck{
			{Status: model.DeliveryCheckPass},
			{Status: model.DeliveryCheckFail},
			{Status: model.DeliveryCheckNotReviewed},
			{Status: model.DeliveryCheckSkipped},
		},
	}
	if err := writeWorkCapability(&out, result); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"VERIFICADO WORK-007", "verdict:fail", "head:0123456789abcdef", "revision:3", "hash:fedcba987654", "pass:1", "fail:1", "not-reviewed:1", "stopped:1"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("summary %q does not contain %q", out.String(), want)
		}
	}
	if strings.Contains(out.String(), "NO DISPONIBLE") {
		t.Fatalf("verify output remained unavailable: %q", out.String())
	}
}

func TestWorkVerifyJSONIncludesSharedCertificateAndChecks(t *testing.T) {
	result := model.WorkCapabilityResult{
		Operation: "verify", Available: true, Performed: true,
		Certificate: &model.DeliveryCertificate{ID: "certificate-1", Verdict: model.DeliveryVerdictPass},
		Checks:      []model.DeliveryCheck{{CertificateID: "certificate-1", Kind: "gate", Name: "build", Status: model.DeliveryCheckPass, Effect: model.DeliveryEffectBlocks}},
	}
	var out bytes.Buffer
	if err := printJSON(&out, result); err != nil {
		t.Fatal(err)
	}
	var decoded model.WorkCapabilityResult
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Certificate == nil || decoded.Certificate.ID != "certificate-1" || len(decoded.Checks) != 1 || decoded.Checks[0].Name != "build" {
		t.Fatalf("decoded=%#v", decoded)
	}
}

func TestWriteWorkSummaryIncludesContractAndAggregateFacts(t *testing.T) {
	work := model.WorkGetResponse{
		Contract: model.WorkContractView{
			ID: "WORK-007", SourceType: model.WorkSourceSpec, SourceID: "SPEC-144",
			Status: model.WorkStatusImplementing, ContractRevision: 3,
			BaseSHA: "0123456789abcdef", ContractHash: "fedcba9876543210",
		},
		Criteria:    make([]model.WorkCriterionView, 2),
		Constraints: make([]model.WorkConstraintView, 1),
		Findings:    make([]model.WorkFinding, 4),
	}
	var out bytes.Buffer
	if err := writeWorkSummary(&out, "TRABAJO", work); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"TRABAJO WORK-007", "source:spec:SPEC-144", "status:implementing",
		"revision:3", "base:0123456789abcdef", "hash:fedcba987654",
		"criteria:2", "constraints:1", "findings:4",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("summary %q does not contain %q", out.String(), want)
		}
	}
}

func TestWriteWorkSummaryShowsOrganicSourceAndEmptyGitFacts(t *testing.T) {
	work := model.WorkGetResponse{Contract: model.WorkContractView{
		ID: "WORK-001", SourceType: model.WorkSourceOrganic, Status: model.WorkStatusDraft,
	}}
	var out bytes.Buffer
	if err := writeWorkSummary(&out, "CREADO", work); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"source:organic", "base:-", "hash:-", "criteria:0", "constraints:0", "findings:0"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("summary %q does not contain %q", out.String(), want)
		}
	}
}
