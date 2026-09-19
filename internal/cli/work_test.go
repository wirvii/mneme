package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/wirvii/mneme/internal/model"
)

func TestWorkCommandsRegisterNineOperations(t *testing.T) {
	cmd := newWorkCmd()
	want := map[string]bool{
		"begin": false, "get": false, "lock": false, "amend": false,
		"review": false, "verify": false, "complete": false, "resume": false, "metrics": false,
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

func TestWorkHelpExplainsDeliverySequence(t *testing.T) {
	cmd := newWorkCmd()
	for _, anchor := range []string{"delivery_v2", "begin", "lock", "review", "verify", "complete", "metrics", "--input", "mneme config show workflow"} {
		if !strings.Contains(cmd.Long, anchor) {
			t.Errorf("work Long help does not contain %q", anchor)
		}
	}
	for _, child := range cmd.Commands() {
		if child.Short == "" {
			t.Errorf("work %s has empty Short help", child.Name())
		}
		if child.Example == "" {
			t.Errorf("work %s has no example", child.Name())
		}
	}
}

func TestTopLevelCommandCountsAndWorkDocumentation(t *testing.T) {
	root := NewRootCmd()
	if got := len(root.Commands()); got != 44 {
		t.Fatalf("mneme registers %d top-level commands, want 44", got)
	}
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	if got := len(root.Commands()); got != 46 {
		t.Fatalf("cobra exposes %d top-level entries, want 46", got)
	}

	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "api", "cli.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(doc)
	sectionStart := strings.Index(text, "## Delivery-v2 WORK")
	if sectionStart < 0 {
		t.Fatal("docs/api/cli.md lacks the Delivery-v2 WORK section")
	}
	section := text[sectionStart:]
	if next := strings.Index(section[len("## Delivery-v2 WORK"):], "\n## "); next >= 0 {
		section = section[:len("## Delivery-v2 WORK")+next]
	}
	for _, child := range newWorkCmd().Commands() {
		if !strings.Contains(section, "`mneme work "+child.Name()) {
			t.Errorf("docs/api/cli.md does not document work %s", child.Name())
		}
	}
	if got := strings.Count(section, "### `mneme work "); got != 9 {
		t.Errorf("docs/api/cli.md documents %d WORK subcommands, want 9", got)
	}
	for _, anchor := range []string{"10 MiB", "--input", "--json", "44", "46"} {
		if !strings.Contains(text, anchor) {
			t.Errorf("docs/api/cli.md does not contain %q", anchor)
		}
	}
	publicCounts := map[string][]string{
		"README.md":            {"44 mneme-registered top-level commands", "46 visible"},
		"docs/ARCHITECTURE.md": {"44 mneme-registered top-level commands", "46 visible"},
	}
	for path, anchors := range publicCounts {
		data, err := os.ReadFile(filepath.Join("..", "..", path))
		if err != nil {
			t.Fatal(err)
		}
		for _, anchor := range anchors {
			if !strings.Contains(string(data), anchor) {
				t.Errorf("%s lacks current CLI count phrase %q", path, anchor)
			}
		}
	}
}

type metricsWorkServiceStub struct {
	workService
	requests []model.WorkMetricsRequest
	result   model.WorkMetricsResponse
}

func (s *metricsWorkServiceStub) WorkMetrics(_ context.Context, req model.WorkMetricsRequest) (model.WorkMetricsResponse, error) {
	s.requests = append(s.requests, req)
	return s.result, nil
}

func TestWorkMetrics_RequestShapeAndJSON(t *testing.T) {
	original := callWorkForCommand
	t.Cleanup(func() { callWorkForCommand = original })
	tests := []struct {
		name string
		args []string
		want model.WorkMetricsRequest
		json bool
	}{
		{name: "defaults", args: []string{"metrics"}, want: model.WorkMetricsRequest{}},
		{name: "ordered ids", args: []string{"metrics", "WORK-003", "WORK-001"}, want: model.WorkMetricsRequest{IDs: []string{"WORK-003", "WORK-001"}}},
		{name: "limit", args: []string{"metrics", "--limit", "10"}, want: model.WorkMetricsRequest{Limit: 10}},
		{name: "json", args: []string{"metrics", "--json"}, want: model.WorkMetricsRequest{}, json: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &metricsWorkServiceStub{result: model.WorkMetricsResponse{Project: "p", Total: 2}}
			callWorkForCommand = func(cmd *cobra.Command, call func(workService) (any, error)) (any, error) {
				return call(stub)
			}
			cmd := newWorkCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetArgs(tt.args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(stub.requests, []model.WorkMetricsRequest{tt.want}) {
				t.Fatalf("requests = %#v, want %#v", stub.requests, tt.want)
			}
			if tt.json {
				var decoded model.WorkMetricsResponse
				if err := json.Unmarshal(out.Bytes(), &decoded); err != nil || decoded.Total != 2 {
					t.Fatalf("JSON output = %q, decoded=%#v err=%v", out.String(), decoded, err)
				}
			}
		})
	}
}

func TestWorkMetrics_HumanOutputIsBoundedAndHonest(t *testing.T) {
	duration := int64(300000)
	result := model.WorkMetricsResponse{
		Project: "p", Total: 3, Included: 2, UnreadableCount: 1,
		Summary: model.WorkMetricsSummary{
			Works: 2, Done: 2, DoneWithoutCorrectionOrEscalation: 1, DoneAfterCorrection: 1,
			AutomaticCorrections: 1, CorrectionCompleted: 1, Escalations: 1,
			LocalEvidenceComplete: 1, LocalEvidencePartial: 1,
			CycleDuration: model.WorkDurationSummary{Count: 2, TotalMs: 600000, MedianMs: &duration, P95Ms: &duration},
		},
		Details: []model.WorkMetric{
			{ID: "WORK-001", Status: model.WorkStatusDone, CycleDurationMs: &duration, DurationFinal: true, VerificationEvidence: model.WorkMetricEvidenceComplete, DeliveryCertificates: 1, LocalVerificationDurationMs: 50, DoneWithoutCorrectionOrEscalation: true},
			{ID: "WORK-002", Status: model.WorkStatusDone, CycleDurationMs: &duration, DurationFinal: true, VerificationEvidence: model.WorkMetricEvidencePartial, DeliveryCertificates: 1, LocalVerificationDurationMs: 40, AutomaticCorrections: 1, Escalations: 1, DoneAfterCorrection: true},
		},
	}
	var out bytes.Buffer
	if err := writeWorkMetrics(&out, result); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"total:3", "legibles:2", "ilegibles:1", "done:2", "duracion-terminal:2/600000ms", "mediana:300000ms", "p95:300000ms",
		"correcciones:1", "escaladas:1", "reanudaciones:0", "evidencia-local:1 completa/1 parcial/0 no-iniciada",
		"WORK-001", "WORK-002", "certificados locales incompletos",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output %q lacks %q", text, want)
		}
	}
	if strings.Contains(text, "0 verificaciones") {
		t.Fatalf("output invents zero verifications: %q", text)
	}
}

func TestWorkMetrics_JSONParity(t *testing.T) {
	duration := int64(42)
	want := model.WorkMetricsResponse{
		Project: "p", Total: 2, Included: 1, UnreadableCount: 1,
		Unreadable: []model.UnreadableRow{{Kind: "work", ID: "WORK-002", Column: "duration_ms", Reason: "invalid"}},
		Summary:    model.WorkMetricsSummary{Works: 1, Done: 1, CycleDuration: model.WorkDurationSummary{Count: 1, TotalMs: 42, MedianMs: &duration, P95Ms: &duration}},
		Details:    []model.WorkMetric{{ID: "WORK-001", Status: model.WorkStatusDone, CycleDurationMs: &duration, DurationFinal: true, VerificationEvidence: model.WorkMetricEvidenceComplete}},
	}
	var out bytes.Buffer
	if err := printJSON(&out, want); err != nil {
		t.Fatal(err)
	}
	var got model.WorkMetricsResponse
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON response = %#v, want %#v", got, want)
	}
}

func TestWorkResumeAndCompleteRequireDecisionFlags(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"complete", "WORK-001"}, `required flag(s) "by" not set`},
		{[]string{"resume", "WORK-001"}, `required flag(s) "by", "reason" not set`},
	}
	for _, tc := range tests {
		cmd := newWorkCmd()
		cmd.SetArgs(tc.args)
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("args=%v error=%v want %q", tc.args, err, tc.want)
		}
	}
	verify := newWorkCmd()
	child, _, err := verify.Find([]string{"verify"})
	if err != nil || child.Flags().Lookup("by") != nil || child.Flags().Lookup("reason") != nil {
		t.Fatalf("verify flags changed: err=%v", err)
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

func TestWorkReviewCLIRequiresBoundedInput(t *testing.T) {
	cmd := newWorkCmd()
	cmd.SetArgs([]string{"review", "WORK-001"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `required flag(s) "input" not set`) {
		t.Fatalf("Execute() error = %v, want missing --input", err)
	}
}

func TestWorkReviewCLIRejectsOversizeInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review.json")
	if err := os.WriteFile(path, make([]byte, workInputLimit+1), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := newWorkCmd()
	cmd.SetArgs([]string{"review", "WORK-001", "--input", path})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "10 MiB") {
		t.Fatalf("Execute() error = %v, want size limit", err)
	}
}

func TestWorkReviewCLI_TargetedInputParity(t *testing.T) {
	cmd := newWorkCmd()
	cmd.SetIn(strings.NewReader(`{"by":"qa","head_sha":"new","resolutions":[{"finding_seq":3,"status":"invalid","evidence":"current run","reason":"false positive"}]}`))
	req, err := decodeWorkReviewRequest(cmd, "-", "WORK-007")
	if err != nil {
		t.Fatal(err)
	}
	if req.ID != "WORK-007" || len(req.Resolutions) != 1 || req.Resolutions[0].FindingSeq != 3 || req.Resolutions[0].Status != model.FindingInvalid || req.Resolutions[0].Evidence != "current run" || req.Resolutions[0].Reason != "false positive" {
		t.Fatalf("request = %#v", req)
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

func TestWorkCompleteOutputSummarizesPersistedEvidence(t *testing.T) {
	var out bytes.Buffer
	result := model.WorkCapabilityResult{
		Operation: "complete", Available: true, Performed: true,
		Work:        model.WorkGetResponse{Contract: model.WorkContractView{ID: "WORK-007", ContractRevision: 3}},
		Certificate: &model.DeliveryCertificate{Verdict: model.DeliveryVerdictPass, HeadSHA: "0123456789abcdef"},
		Checks:      []model.DeliveryCheck{{Name: "build"}, {Name: "test"}},
	}
	if err := writeWorkCapability(&out, result); err != nil {
		t.Fatal(err)
	}
	want := "CERRADO WORK-007 verdict:pass head:0123456789abcdef revision:3 checks:2\n"
	if out.String() != want {
		t.Fatalf("output=%q want=%q", out.String(), want)
	}
}

func TestWriteWorkReviewCapabilitySummarizesReview(t *testing.T) {
	var out bytes.Buffer
	result := model.WorkCapabilityResult{
		Operation: "review", Available: true, Performed: true,
		Work: model.WorkGetResponse{
			Contract: model.WorkContractView{ID: "WORK-007", ContractRevision: 3, ContractHash: "fedcba9876543210"},
			Findings: []model.WorkFinding{
				{Category: model.FindingRegression},
				{Category: model.FindingImprovement},
			},
		},
		Certificate: &model.DeliveryCertificate{Verdict: model.DeliveryVerdictFail, HeadSHA: "0123456789abcdef"},
		Checks: []model.DeliveryCheck{
			{Kind: "architecture", Status: model.DeliveryCheckPass},
			{Kind: "architecture", Status: model.DeliveryCheckFail},
			{Kind: "architecture", Status: model.DeliveryCheckNotReviewed},
		},
	}
	if err := writeWorkCapability(&out, result); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"REVISADO WORK-007", "verdict:fail", "head:0123456789abcdef", "architecture-pass:1", "architecture-fail:1", "architecture-not-reviewed:1", "blocking-findings:1", "non-blocking-findings:1"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("summary %q does not contain %q", out.String(), want)
		}
	}
	if strings.Contains(out.String(), "NO DISPONIBLE") {
		t.Fatalf("review output remained unavailable: %q", out.String())
	}
}

func TestWorkReviewCLI_PrintsInitialCorrectionDecision(t *testing.T) {
	var out bytes.Buffer
	result := model.WorkCapabilityResult{
		Operation: "review", Available: true, Performed: true, ReviewPhase: model.ReviewPhaseInitial, NextStatus: model.WorkStatusCorrecting,
		Work:              model.WorkGetResponse{Contract: model.WorkContractView{ID: "WORK-007", CorrectionRounds: 1}, Findings: []model.WorkFinding{{Category: model.FindingRegression}}},
		Certificate:       &model.DeliveryCertificate{Verdict: model.DeliveryVerdictFail, HeadSHA: "head"},
		CorrectionMandate: &model.CorrectionMandate{CorrectionRound: 1, BlockingFindings: []model.WorkFinding{{Seq: 1}}, BlockingChecks: []model.DeliveryCheck{{Kind: "gate", Name: "test"}}},
	}
	if err := writeWorkCapability(&out, result); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"phase:initial", "next:correcting", "round:1", "mandate-findings:1", "mandate-checks:1"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output %q lacks %q", out.String(), want)
		}
	}
}

func TestWorkReviewCLI_PrintsTargetedOutcome(t *testing.T) {
	var out bytes.Buffer
	result := model.WorkCapabilityResult{
		Operation: "review", Available: true, Performed: true, ReviewPhase: model.ReviewPhaseTargeted, NextStatus: model.WorkStatusEscalated,
		Work:        model.WorkGetResponse{Contract: model.WorkContractView{ID: "WORK-007", CorrectionRounds: 1}, Findings: []model.WorkFinding{{Category: model.FindingRegression, Status: model.FindingFixed}, {Category: model.FindingRegression, Status: model.FindingOpen, ReviewPhase: model.ReviewPhaseTargeted}}},
		Certificate: &model.DeliveryCertificate{Verdict: model.DeliveryVerdictFail, HeadSHA: "head"},
	}
	if err := writeWorkCapability(&out, result); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"phase:targeted", "next:escalated", "resolved:1", "new-blockers:1"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output %q lacks %q", out.String(), want)
		}
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
