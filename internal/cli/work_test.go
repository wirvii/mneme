package cli

import (
	"bytes"
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
