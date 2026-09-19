package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/sddfile"
)

func seedServiceWork(t *testing.T, svc *SDDService, id string) {
	t.Helper()
	contract := &model.WorkContract{
		ID: id, Project: svc.project, SourceType: model.WorkSourceOrganic,
		Status: model.WorkStatusDraft, Goal: "goal", Scope: []string{"internal/**"},
		Verification:      []model.VerificationKind{model.VerificationBuild},
		DevelopmentMethod: model.DevelopmentMethodStandard, MaxCorrectionRounds: 1,
	}
	if err := svc.store.CreateWork(context.Background(), contract, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestValidateWorkCriteriaThroughQualityParser(t *testing.T) {
	valid := []model.WorkCriterionInput{
		{Key: "file", Declaration: `[[criterion]]
id = "file"
mode = "assert"
text = "file exists"
  [[criterion.assert]]
  verb = "file_exists"
  path = "future.txt"
  new = true`},
		{Key: "count", Declaration: `[[criterion]]
id = "count"
mode = "assert"
text = "pattern count"
  [[criterion.assert]]
  verb = "pattern_count"
  contains = "needle"
  in = ["internal/**/*.go"]
  word = false
  comparator = ">="
  count = 1
  new = true`},
		{Key: "defined", Declaration: `[[criterion]]
id = "defined"
mode = "assert"
text = "symbol defined"
  [[criterion.assert]]
  verb = "symbol_defined"
  symbol = "Future"
  in = ["internal/**/*.go"]
  new = true`},
		{Key: "referenced", Declaration: `[[criterion]]
id = "referenced"
mode = "assert"
text = "symbol referenced"
  [[criterion.assert]]
  verb = "symbol_referenced"
  symbol = "Future"
  defined_in = ["internal/**/*.go"]
  ignore = []
  new = true`},
		{Key: "command", Declaration: `[[criterion]]
id = "command"
mode = "command"
text = "command runs"
command = ["go", "test", "./internal/model"]
timeout = "1m"`},
		{Key: "manual", Declaration: `[[criterion]]
id = "manual"
mode = "manual"
text = "human checks"
evidence_required = "written observation"`},
	}
	got, err := parseWorkCriteria(valid)
	if err != nil || len(got) != len(valid) {
		t.Fatalf("parseWorkCriteria() = %#v, %v", got, err)
	}
	for i := range valid {
		if got[i].Key != valid[i].Key || got[i].Declaration != valid[i].Declaration {
			t.Fatalf("criterion %d changed: %#v", i, got[i])
		}
	}

	tests := []struct {
		name  string
		input []model.WorkCriterionInput
	}{
		{"invalid toml", []model.WorkCriterionInput{{Key: "broken", Declaration: "[[criterion]"}}},
		{"command is string", []model.WorkCriterionInput{{Key: "command", Declaration: strings.Replace(valid[4].Declaration, `["go", "test", "./internal/model"]`, `"go test ./internal/model"`, 1)}}},
		{"external key mismatch", []model.WorkCriterionInput{{Key: "other", Declaration: valid[5].Declaration}}},
		{"two criteria in fragment", []model.WorkCriterionInput{{Key: "manual", Declaration: valid[5].Declaration + "\n" + valid[0].Declaration}}},
		{"fragment without criterion", []model.WorkCriterionInput{{Key: "empty", Declaration: "# nothing"}}},
		{"duplicate ids", []model.WorkCriterionInput{valid[5], valid[5]}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseWorkCriteria(tc.input)
			if !errors.Is(err, model.ErrInvalidCriteria) {
				t.Fatalf("error = %v, want ErrInvalidCriteria", err)
			}
			if err == nil || (!strings.Contains(err.Error(), "criterion") && !strings.Contains(err.Error(), tc.input[0].Key)) {
				t.Fatalf("error lacks criterion context: %v", err)
			}
		})
	}
}

func validWorkAmendRequest(id string) model.WorkAmendRequest {
	return model.WorkAmendRequest{
		ID: id, Goal: "changed", Scope: []string{"internal/**"},
		Verification:      []model.VerificationKind{model.VerificationBuild},
		DevelopmentMethod: model.DevelopmentMethodStandard, By: "coordinator", Reason: "scope changed",
	}
}

func TestWorkServiceLegacyBlocksMutationsButAllowsGet(t *testing.T) {
	svc := newTestSDDService(t, "p")
	seedServiceWork(t, svc, "WORK-001")
	ctx := context.Background()
	checks := []struct {
		name string
		call func() error
	}{
		{"begin", func() error { _, err := svc.WorkBegin(ctx, model.WorkBeginRequest{}); return err }},
		{"lock", func() error { _, err := svc.WorkLock(ctx, model.WorkLockRequest{ID: "WORK-001"}); return err }},
		{"amend", func() error { _, err := svc.WorkAmend(ctx, validWorkAmendRequest("WORK-001")); return err }},
		{"review", func() error { _, err := svc.WorkReview(ctx, model.WorkReviewRequest{ID: "WORK-001"}); return err }},
		{"verify", func() error { _, err := svc.WorkVerify(ctx, model.WorkActionRequest{ID: "WORK-001"}); return err }},
		{"complete", func() error {
			_, err := svc.WorkComplete(ctx, model.WorkCompleteRequest{ID: "WORK-001", By: "coordinator"})
			return err
		}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.call(); !errors.Is(err, model.ErrWorkflowEngineDisabled) {
				t.Fatalf("error = %v, want ErrWorkflowEngineDisabled", err)
			}
		})
	}
	got, err := svc.WorkGet(ctx, model.WorkGetRequest{ID: "WORK-001"})
	if err != nil || got.Contract.Status != model.WorkStatusDraft || len(got.History) != 0 {
		t.Fatalf("WorkGet() = %#v, %v", got, err)
	}
	agg, err := svc.store.GetWorkAggregate(ctx, "WORK-001")
	if err != nil || agg.Contract.Status != model.WorkStatusDraft || len(agg.History) != 0 {
		t.Fatalf("legacy mutation changed work: %#v, %v", agg, err)
	}
}

func TestWorkflowEngineDoesNotChangeLegacySpecOperation(t *testing.T) {
	svc := newTestSDDService(t, "p")
	for _, engine := range []string{config.WorkflowEngineLegacy, config.WorkflowEngineDeliveryV2} {
		svc.config.Workflow.Engine = engine
		item, err := svc.BacklogAdd(context.Background(), model.BacklogAddRequest{Title: engine, Lane: model.LaneStandard})
		if err != nil || item.Status != model.BacklogStatusRaw {
			t.Fatalf("engine %s changed BacklogAdd: %#v, %v", engine, item, err)
		}
	}
}

func deliveryWorkService(t *testing.T) *SDDService {
	t.Helper()
	svc := newTestSDDService(t, "p")
	svc.config.Workflow.Engine = config.WorkflowEngineDeliveryV2
	return svc
}

func TestWorkScopePatternsValidatedBeforeStore(t *testing.T) {
	for _, pattern := range []string{"internal/**/*.go", "*.md", "apps/*/src/**"} {
		if err := validateWorkScope([]string{pattern}); err != nil {
			t.Errorf("valid pattern %q rejected: %v", pattern, err)
		}
	}
	for _, pattern := range []string{"", "   ", "[[bad"} {
		if err := validateWorkScope([]string{pattern}); !errors.Is(err, model.ErrInvalidContract) {
			t.Errorf("invalid pattern %q error = %v", pattern, err)
		}
	}

	svc := deliveryWorkService(t)
	ctx := context.Background()
	_, err := svc.WorkBegin(ctx, model.WorkBeginRequest{Goal: "g", Scope: []string{"[[bad"}, Verification: []model.VerificationKind{model.VerificationBuild}})
	if !errors.Is(err, model.ErrInvalidContract) {
		t.Fatalf("invalid begin error = %v", err)
	}
	if next, _ := svc.store.NextWorkID(ctx, "p"); next != "WORK-001" {
		t.Fatalf("invalid begin wrote a contract; next id = %s", next)
	}
	seedServiceWork(t, svc, "WORK-001")
	if err := svc.store.LockWorkAndStart(ctx, "WORK-001", "base", "coordinator"); err != nil {
		t.Fatal(err)
	}
	before, _ := svc.store.GetWorkAggregate(ctx, "WORK-001")
	request := validWorkAmendRequest("WORK-001")
	request.Scope = []string{"[[bad"}
	_, err = svc.WorkAmend(ctx, request)
	if !errors.Is(err, model.ErrInvalidContract) {
		t.Fatalf("invalid amend error = %v", err)
	}
	after, _ := svc.store.GetWorkAggregate(ctx, "WORK-001")
	if after.Contract.ContractRevision != before.Contract.ContractRevision || after.Contract.ContractHash != before.Contract.ContractHash {
		t.Fatal("invalid amend changed the contract")
	}
}

func TestWorkBeginDefaultsAndOverrides(t *testing.T) {
	svc := deliveryWorkService(t)
	ctx := context.Background()
	defaults, err := svc.WorkBegin(ctx, model.WorkBeginRequest{Goal: "default", Scope: []string{"internal/**"}, Verification: []model.VerificationKind{model.VerificationBuild}})
	if err != nil {
		t.Fatal(err)
	}
	if defaults.Contract.SourceType != model.WorkSourceOrganic || defaults.Contract.DevelopmentMethod != model.DevelopmentMethodStandard || defaults.Contract.MaxCorrectionRounds != 1 {
		t.Fatalf("defaults = %#v", defaults.Contract)
	}
	spec := &model.Spec{ID: "SPEC-001", Title: "source", Status: model.SpecStatusDraft, Project: "p", Lane: model.LaneStandard}
	if err := svc.store.CreateSpec(ctx, spec); err != nil {
		t.Fatal(err)
	}
	zero := 0
	override, err := svc.WorkBegin(ctx, model.WorkBeginRequest{Workflow: config.WorkflowDefaultSDD, SpecID: spec.ID, Goal: "override", Scope: []string{"internal/**"}, Verification: []model.VerificationKind{model.VerificationBuild}, DevelopmentMethod: model.DevelopmentMethodTDD, MaxCorrectionRounds: &zero})
	if err != nil {
		t.Fatal(err)
	}
	if override.Contract.SourceType != model.WorkSourceSpec || override.Contract.SourceID != spec.ID || override.Contract.DevelopmentMethod != model.DevelopmentMethodTDD || override.Contract.MaxCorrectionRounds != 0 {
		t.Fatalf("override = %#v", override.Contract)
	}
	for _, request := range []model.WorkBeginRequest{
		{Workflow: config.WorkflowDefaultOrganic, SpecID: spec.ID, Goal: "bad", Scope: []string{"internal/**"}, Verification: []model.VerificationKind{model.VerificationBuild}},
		{Workflow: config.WorkflowDefaultSDD, Goal: "bad", Scope: []string{"internal/**"}, Verification: []model.VerificationKind{model.VerificationBuild}},
		{Goal: "bad", Scope: []string{"internal/**"}, Verification: []model.VerificationKind{model.VerificationAcceptance}},
	} {
		if _, err := svc.WorkBegin(ctx, request); !errors.Is(err, model.ErrInvalidContract) {
			t.Errorf("invalid relationship error = %v", err)
		}
	}
}

func TestWorkBegin_ReservesGitNativeWorkID(t *testing.T) {
	svc, repoDir := newSDDMaterializeService(t, importTestProject)
	svc.config.Workflow.Engine = config.WorkflowEngineDeliveryV2
	enableSDD(t, repoDir, importTestProject)
	writeRawSDDFile(t, sddfile.WorkPath(repoDir, "WORK-205"), "unreadable but reserved")

	created, err := svc.WorkBegin(context.Background(), model.WorkBeginRequest{
		Workflow: config.WorkflowDefaultOrganic, Goal: "reserved",
		Scope: []string{"internal/**"}, Verification: []model.VerificationKind{model.VerificationBuild},
	})
	if err != nil {
		t.Fatalf("WorkBegin: %v", err)
	}
	if created.Contract.ID != "WORK-206" {
		t.Fatalf("created ID = %s, want WORK-206", created.Contract.ID)
	}
	data, err := sddfile.ReadRecord(sddfile.WorkPath(repoDir, "WORK-206"))
	if err != nil {
		t.Fatalf("reserved work was not materialized: %v", err)
	}
	record, err := sddfile.UnmarshalWork(data)
	if err != nil || record.Aggregate.Contract.ID != "WORK-206" {
		t.Fatalf("materialized record = %+v, %v", record, err)
	}
}

func TestWorkBeginLockAmendAndResume_Materialize(t *testing.T) {
	svc := deliveryWorkService(t)
	repo, _ := initWorkGitRepo(t)
	svc.WithRepoDir(repo)
	enableSDD(t, repo, svc.project)
	ctx := context.Background()
	draft, err := svc.WorkBegin(ctx, model.WorkBeginRequest{Goal: "before", Scope: []string{"internal/**"}, Verification: []model.VerificationKind{model.VerificationBuild}})
	if err != nil {
		t.Fatal(err)
	}
	if got := readMaterializedWork(t, repo, draft.Contract.ID).Aggregate.Contract.Status; got != model.WorkStatusDraft {
		t.Fatalf("begin file status=%s", got)
	}
	locked, err := svc.WorkLock(ctx, model.WorkLockRequest{ID: draft.Contract.ID, By: "coordinator"})
	if err != nil {
		t.Fatal(err)
	}
	if got := readMaterializedWork(t, repo, draft.Contract.ID).Aggregate.Contract.Status; got != model.WorkStatusImplementing {
		t.Fatalf("lock file status=%s", got)
	}
	_, err = svc.WorkAmend(ctx, model.WorkAmendRequest{ID: draft.Contract.ID, Goal: "after", Scope: []string{"cmd/**"}, Verification: []model.VerificationKind{model.VerificationBuild}, DevelopmentMethod: model.DevelopmentMethodStandard, By: "coordinator", Reason: "approved"})
	if err != nil {
		t.Fatal(err)
	}
	if got := readMaterializedWork(t, repo, draft.Contract.ID).Aggregate.Contract; got.Goal != "after" || got.ContractRevision != locked.Contract.ContractRevision+1 {
		t.Fatalf("amend file=%#v", got)
	}
	if err := svc.store.TransitionWork(ctx, draft.Contract.ID, model.WorkStatusImplementing, model.WorkStatusVerifying, "qa", ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.store.TransitionWork(ctx, draft.Contract.ID, model.WorkStatusVerifying, model.WorkStatusEscalated, "qa", ""); err != nil {
		t.Fatal(err)
	}
	_, err = svc.WorkResume(ctx, model.WorkResumeRequest{ID: draft.Contract.ID, By: "owner", Reason: "retry"})
	if err != nil {
		t.Fatal(err)
	}
	if got := readMaterializedWork(t, repo, draft.Contract.ID).Aggregate.Contract.Status; got != model.WorkStatusImplementing {
		t.Fatalf("resume file status=%s", got)
	}
}

func TestWorkGetPublicProjectionOmitsContractUUID(t *testing.T) {
	svc := deliveryWorkService(t)
	ctx := context.Background()
	response, err := svc.WorkBegin(ctx, model.WorkBeginRequest{
		Goal: "public", Scope: []string{"internal/**"}, Verification: []model.VerificationKind{model.VerificationAcceptance},
		Criteria: []model.WorkCriterionInput{
			{Key: "first", Declaration: `[[criterion]]
id = "first"
mode = "manual"
text = "first"
evidence_required = "note"`},
			{Key: "second", Declaration: `[[criterion]]
id = "second"
mode = "manual"
text = "second"
evidence_required = "note"`},
		},
		Constraints: []model.WorkConstraintInput{{Key: "C1", Text: "one"}, {Key: "C2", Text: "two"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	aggregate, _ := svc.store.GetWorkAggregate(ctx, response.Contract.ID)
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.Contract.UUID == "" || strings.Contains(string(encoded), aggregate.Contract.UUID) {
		t.Fatalf("public JSON leaked contract UUID: %s", encoded)
	}
	if len(response.Criteria) != 2 || response.Criteria[0].Key != "first" || response.Criteria[1].Key != "second" || len(response.Constraints) != 2 || response.Constraints[1].Key != "C2" {
		t.Fatalf("public ordering/content = %#v", response)
	}
}

func initWorkGitRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init"}, {"config", "user.email", "test@example.com"}, {"config", "user.name", "Test"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "add", "tracked.txt")
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, output)
	}
	cmd = exec.Command("git", "commit", "-m", "base")
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, output)
	}
	cmd = exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return dir, strings.TrimSpace(string(output))
}

func TestWorkLockCapturesHeadAndStartsAtomically(t *testing.T) {
	svc := deliveryWorkService(t)
	repo, head := initWorkGitRepo(t)
	svc.WithRepoDir(repo)
	ctx := context.Background()
	draft, err := svc.WorkBegin(ctx, model.WorkBeginRequest{Goal: "lock", Scope: []string{"internal/**"}, Verification: []model.VerificationKind{model.VerificationBuild}})
	if err != nil {
		t.Fatal(err)
	}
	locked, err := svc.WorkLock(ctx, model.WorkLockRequest{ID: draft.Contract.ID, By: "coordinator"})
	if err != nil {
		t.Fatal(err)
	}
	if locked.Contract.Status != model.WorkStatusImplementing || locked.Contract.BaseSHA != head || locked.Contract.ContractRevision != 1 || len(locked.History) != 2 {
		t.Fatalf("locked = %#v", locked)
	}

	bad := deliveryWorkService(t)
	bad.WithRepoDir(filepath.Join(t.TempDir(), "missing"))
	seedServiceWork(t, bad, "WORK-001")
	if _, err := bad.WorkLock(ctx, model.WorkLockRequest{ID: "WORK-001"}); err == nil {
		t.Fatal("WorkLock accepted a non-repository")
	}
	unchanged, _ := bad.store.GetWork(ctx, "WORK-001")
	if unchanged.Status != model.WorkStatusDraft || unchanged.ContractRevision != 0 {
		t.Fatalf("git failure changed contract: %#v", unchanged)
	}
}

func TestWorkAmendFullReplacementAndPreservedIdentity(t *testing.T) {
	svc := deliveryWorkService(t)
	repo, _ := initWorkGitRepo(t)
	svc.WithRepoDir(repo)
	ctx := context.Background()
	draft, err := svc.WorkBegin(ctx, model.WorkBeginRequest{Goal: "before", Scope: []string{"internal/**"}, Verification: []model.VerificationKind{model.VerificationBuild}, MaxCorrectionRounds: intPtr(3)})
	if err != nil {
		t.Fatal(err)
	}
	started, err := svc.WorkLock(ctx, model.WorkLockRequest{ID: draft.Contract.ID, By: "coordinator"})
	if err != nil {
		t.Fatal(err)
	}
	request := model.WorkAmendRequest{
		ID: draft.Contract.ID, Goal: "after", Scope: []string{"cmd/**"}, Verification: []model.VerificationKind{model.VerificationAcceptance}, DevelopmentMethod: model.DevelopmentMethodTDD,
		Criteria: []model.WorkCriterionInput{{Key: "AC1", Declaration: `[[criterion]]
id = "AC1"
mode = "manual"
text = "check"
evidence_required = "note"`}},
		Constraints: []model.WorkConstraintInput{{Key: "C1", Text: "inward"}}, By: "coordinator", Reason: "approved change",
	}
	omittedMethod := request
	omittedMethod.DevelopmentMethod = ""
	if _, err := svc.WorkAmend(ctx, omittedMethod); err == nil {
		t.Fatal("amendment with omitted development_method succeeded")
	}
	amended, err := svc.WorkAmend(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if amended.Contract.ContractRevision != started.Contract.ContractRevision+1 || amended.Contract.ContractHash == started.Contract.ContractHash || amended.Contract.Status != model.WorkStatusImplementing || amended.Contract.SourceType != started.Contract.SourceType || amended.Contract.BaseSHA != started.Contract.BaseSHA || amended.Contract.MaxCorrectionRounds != 3 || len(amended.Criteria) != 1 || len(amended.Constraints) != 1 {
		t.Fatalf("amended = %#v", amended)
	}
	last := amended.History[len(amended.History)-1]
	if last.By != request.By || last.Reason != request.Reason {
		t.Fatalf("amend history = %#v", last)
	}
	for _, mutate := range []func(*model.WorkAmendRequest){
		func(r *model.WorkAmendRequest) { r.DevelopmentMethod = "" },
		func(r *model.WorkAmendRequest) { r.By = "" },
		func(r *model.WorkAmendRequest) { r.Reason = "" },
	} {
		invalid := request
		mutate(&invalid)
		if _, err := svc.WorkAmend(ctx, invalid); err == nil {
			t.Fatal("incomplete amendment succeeded")
		}
	}
	after, _ := svc.WorkGet(ctx, model.WorkGetRequest{ID: request.ID})
	if after.Contract.ContractRevision != amended.Contract.ContractRevision || after.Contract.ContractHash != amended.Contract.ContractHash {
		t.Fatal("invalid amendment changed contract")
	}
}

func intPtr(value int) *int { return &value }

func closableService(t *testing.T) (*SDDService, *deliveryRunnerStub, model.WorkCapabilityResult) {
	t.Helper()
	svc, runner := deliveryVerifyService(t, model.WorkStatusVerifying)
	work, err := svc.store.GetWork(context.Background(), "WORK-001")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	cert := &model.DeliveryCertificate{
		Project: work.Project, WorkID: work.ID, ContractRevision: work.ContractRevision,
		ContractHash: work.ContractHash, HeadSHA: strings.TrimSpace(runVerifyGit(t, svc.repoDir, "rev-parse", "HEAD")),
		BaseSHA: work.BaseSHA, Verdict: model.DeliveryVerdictPass, StartedAt: now, FinishedAt: now,
	}
	checks := []*model.DeliveryCheck{{Kind: "gate", Name: "build", Status: model.DeliveryCheckPass, Effect: model.DeliveryEffectBlocks, Detail: "persisted"}}
	if err := svc.store.InsertDeliveryCertificate(context.Background(), cert, checks); err != nil {
		t.Fatal(err)
	}
	persisted, err := svc.store.ListDeliveryChecks(context.Background(), cert.ID)
	if err != nil {
		t.Fatal(err)
	}
	runner.calls = 0
	return svc, runner, model.WorkCapabilityResult{Certificate: cert, Checks: persisted}
}

func TestWorkComplete_UsesRealCleanHEAD(t *testing.T) {
	t.Run("head mismatch", func(t *testing.T) {
		svc, _, _ := closableService(t)
		if err := os.WriteFile(filepath.Join(svc.repoDir, "tracked.txt"), []byte("changed\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		runVerifyGit(t, svc.repoDir, "add", "tracked.txt")
		runVerifyGit(t, svc.repoDir, "commit", "-q", "-m", "new head")
		if _, err := svc.WorkComplete(context.Background(), model.WorkCompleteRequest{ID: "WORK-001", By: "coordinator"}); !errors.Is(err, model.ErrInvalidWorkTransition) {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("dirty", func(t *testing.T) {
		svc, _, _ := closableService(t)
		if err := os.WriteFile(filepath.Join(svc.repoDir, "dirty.txt"), []byte("dirty\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.WorkComplete(context.Background(), model.WorkCompleteRequest{ID: "WORK-001", By: "coordinator"}); !errors.Is(err, model.ErrInvalidContract) {
			t.Fatalf("error=%v", err)
		}
	})
}

func TestWorkComplete_LegacyStopsBeforeGit(t *testing.T) {
	svc := newTestSDDService(t, "p")
	svc.WithRepoDir(filepath.Join(t.TempDir(), "missing"))
	if _, err := svc.WorkComplete(context.Background(), model.WorkCompleteRequest{ID: "WORK-404", By: "coordinator"}); !errors.Is(err, model.ErrWorkflowEngineDisabled) {
		t.Fatalf("error=%v", err)
	}
}

func TestWorkComplete_DoesNotExecuteDeliveryRunner(t *testing.T) {
	svc, runner, _ := closableService(t)
	if _, err := svc.WorkComplete(context.Background(), model.WorkCompleteRequest{ID: "WORK-001", By: "coordinator"}); err != nil {
		t.Fatal(err)
	}
	if runner.calls != 0 {
		t.Fatalf("runner calls=%d", runner.calls)
	}
}

func TestWorkComplete_ReturnsPersistedEvidence(t *testing.T) {
	svc, _, verified := closableService(t)
	result, err := svc.WorkComplete(context.Background(), model.WorkCompleteRequest{ID: "WORK-001", By: "coordinator"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Operation != "complete" || !result.Available || !result.Performed || result.NextStatus != model.WorkStatusDone || result.Work.Contract.Status != model.WorkStatusDone {
		t.Fatalf("result=%#v", result)
	}
	if result.Certificate == nil || verified.Certificate == nil || result.Certificate.ID != verified.Certificate.ID || !reflect.DeepEqual(result.Checks, verified.Checks) {
		t.Fatalf("complete evidence=%#v/%#v verified=%#v/%#v", result.Certificate, result.Checks, verified.Certificate, verified.Checks)
	}
}

func TestWorkComplete_MaterializesDoneState(t *testing.T) {
	svc, _, _ := closableService(t)
	enableSDD(t, svc.repoDir, svc.project)
	runVerifyGit(t, svc.repoDir, "add", ".mneme")
	runVerifyGit(t, svc.repoDir, "commit", "-q", "-m", "enable sdd for completion")
	work, err := svc.store.GetWork(context.Background(), "WORK-001")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	cert := &model.DeliveryCertificate{Project: work.Project, WorkID: work.ID, ContractRevision: work.ContractRevision, ContractHash: work.ContractHash, HeadSHA: strings.TrimSpace(runVerifyGit(t, svc.repoDir, "rev-parse", "HEAD")), BaseSHA: work.BaseSHA, Verdict: model.DeliveryVerdictPass, StartedAt: now, FinishedAt: now}
	if err := svc.store.InsertDeliveryCertificate(context.Background(), cert, []*model.DeliveryCheck{{Kind: "gate", Name: "build", Status: model.DeliveryCheckPass, Effect: model.DeliveryEffectBlocks}}); err != nil {
		t.Fatal(err)
	}
	result, err := svc.WorkComplete(context.Background(), model.WorkCompleteRequest{ID: "WORK-001", By: "coordinator"})
	if err != nil {
		t.Fatal(err)
	}
	record := readMaterializedWork(t, svc.repoDir, "WORK-001")
	if record.Aggregate.Contract.Status != model.WorkStatusDone || record.Aggregate.Contract.CompletedAt == nil || record.Aggregate.Contract.Status != result.Work.Contract.Status {
		t.Fatalf("materialized completion=%#v", record.Aggregate.Contract)
	}
}

func TestWorkResume_RequiresDeliveryV2(t *testing.T) {
	svc := newTestSDDService(t, "p")
	svc.WithRepoDir(filepath.Join(t.TempDir(), "missing"))
	if _, err := svc.WorkResume(context.Background(), model.WorkResumeRequest{ID: "WORK-404", By: "orchestrator", Reason: "again"}); !errors.Is(err, model.ErrWorkflowEngineDisabled) {
		t.Fatalf("error=%v", err)
	}
}

func TestWorkResume_ReturnsImplementingAggregate(t *testing.T) {
	svc, _ := deliveryVerifyService(t, model.WorkStatusEscalated)
	result, err := svc.WorkResume(context.Background(), model.WorkResumeRequest{ID: "WORK-001", By: "orchestrator", Reason: "owner approved another attempt"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Contract.Status != model.WorkStatusImplementing || result.Contract.CorrectionRounds != 0 {
		t.Fatalf("result=%#v", result.Contract)
	}
	last := result.History[len(result.History)-1]
	if last.FromStatus != model.WorkStatusEscalated || last.ToStatus != model.WorkStatusImplementing || last.By != "orchestrator" || last.Reason != "owner approved another attempt" {
		t.Fatalf("history=%#v", last)
	}
}

func TestWorkOperationsDoNotMaterializeSDDFiles(t *testing.T) {
	svc := deliveryWorkService(t)
	repo := t.TempDir()
	svc.WithRepoDir(repo)
	marker := filepath.Join(repo, ".mneme", "sdd", ".mneme-sdd")
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.WorkBegin(context.Background(), model.WorkBeginRequest{Goal: "no files", Scope: []string{"internal/**"}, Verification: []model.VerificationKind{model.VerificationBuild}}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(marker))
	if err != nil || len(entries) != 1 || entries[0].Name() != ".mneme-sdd" {
		t.Fatalf("SDD tree changed: entries=%v err=%v", entries, err)
	}
}

func TestOnlyWorkLockAndCompleteInvokeGit(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("shell fake is Unix-only")
	}
	svc := deliveryWorkService(t)
	ctx := context.Background()
	bin := t.TempDir()
	svc.WithRepoDir(t.TempDir())
	logPath := filepath.Join(bin, "git.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"" + logPath + "\"\nprintf '%s\\n' 0123456789012345678901234567890123456789\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	work, err := svc.WorkBegin(ctx, model.WorkBeginRequest{Goal: "git", Scope: []string{"internal/**"}, Verification: []model.VerificationKind{model.VerificationBuild}})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = svc.WorkGet(ctx, model.WorkGetRequest{ID: work.Contract.ID})
	_, _ = svc.WorkAmend(ctx, validWorkAmendRequest(work.Contract.ID))
	_, _ = svc.WorkReview(ctx, model.WorkReviewRequest{ID: work.Contract.ID})
	if _, err := os.Stat(logPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("non-lock operation invoked git: %v", err)
	}
	_, _ = svc.WorkComplete(ctx, model.WorkCompleteRequest{ID: work.Contract.ID, By: "coordinator"})
	logged, err := os.ReadFile(logPath)
	if err != nil || string(logged) != "rev-parse HEAD\nstatus --porcelain --untracked-files=normal\n" {
		t.Fatalf("complete git calls = %q, err=%v", logged, err)
	}
	if err := os.Remove(logPath); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.WorkLock(ctx, model.WorkLockRequest{ID: work.Contract.ID}); err != nil {
		t.Fatal(err)
	}
	logged, err = os.ReadFile(logPath)
	if err != nil || string(logged) != "rev-parse HEAD\n" {
		t.Fatalf("git calls = %q, err=%v", logged, err)
	}
}

func TestWorkBeginRejectsInvalidSourcesAndCriteria(t *testing.T) {
	svc := deliveryWorkService(t)
	ctx := context.Background()
	base := model.WorkBeginRequest{
		Goal: "invalid", Scope: []string{"internal/**"},
		Verification: []model.VerificationKind{model.VerificationBuild},
	}

	unknownWorkflow := base
	unknownWorkflow.Workflow = "unknown"
	if _, err := svc.WorkBegin(ctx, unknownWorkflow); !errors.Is(err, model.ErrInvalidContract) {
		t.Fatalf("unknown workflow error = %v, want ErrInvalidContract", err)
	}

	invalidCriterion := base
	invalidCriterion.Criteria = []model.WorkCriterionInput{{Key: "broken", Declaration: "[[criterion]"}}
	if _, err := svc.WorkBegin(ctx, invalidCriterion); !errors.Is(err, model.ErrInvalidCriteria) {
		t.Fatalf("invalid criterion error = %v, want ErrInvalidCriteria", err)
	}

	missingSpec := base
	missingSpec.Workflow = config.WorkflowDefaultSDD
	missingSpec.SpecID = "SPEC-404"
	if _, err := svc.WorkBegin(ctx, missingSpec); !errors.Is(err, model.ErrSpecNotFound) {
		t.Fatalf("missing source spec error = %v, want ErrSpecNotFound", err)
	}
}

func TestWorkOperationsRejectMissingInputsAndUnknownWork(t *testing.T) {
	svc := deliveryWorkService(t)
	ctx := context.Background()
	seedServiceWork(t, svc, "WORK-001")

	if _, err := svc.WorkGet(ctx, model.WorkGetRequest{}); !errors.Is(err, model.ErrInvalidContract) {
		t.Fatalf("empty get error = %v, want ErrInvalidContract", err)
	}
	if _, err := svc.WorkGet(ctx, model.WorkGetRequest{ID: "WORK-404"}); !errors.Is(err, model.ErrWorkNotFound) {
		t.Fatalf("unknown get error = %v, want ErrWorkNotFound", err)
	}
	if _, err := svc.WorkLock(ctx, model.WorkLockRequest{}); !errors.Is(err, model.ErrInvalidContract) {
		t.Fatalf("empty lock error = %v, want ErrInvalidContract", err)
	}
	if _, err := svc.WorkLock(ctx, model.WorkLockRequest{ID: "WORK-001"}); !errors.Is(err, model.ErrInvalidContract) {
		t.Fatalf("lock without repository error = %v, want ErrInvalidContract", err)
	}

	repo, _ := initWorkGitRepo(t)
	svc.WithRepoDir(repo)
	if _, err := svc.WorkLock(ctx, model.WorkLockRequest{ID: "WORK-404"}); !errors.Is(err, model.ErrWorkNotFound) {
		t.Fatalf("unknown lock error = %v, want ErrWorkNotFound", err)
	}

	if _, err := svc.WorkAmend(ctx, model.WorkAmendRequest{}); !errors.Is(err, model.ErrInvalidContract) {
		t.Fatalf("empty amend error = %v, want ErrInvalidContract", err)
	}
	invalidCriterion := validWorkAmendRequest("WORK-404")
	invalidCriterion.Criteria = []model.WorkCriterionInput{{Key: "broken", Declaration: "[[criterion]"}}
	if _, err := svc.WorkAmend(ctx, invalidCriterion); !errors.Is(err, model.ErrInvalidCriteria) {
		t.Fatalf("invalid amend criterion error = %v, want ErrInvalidCriteria", err)
	}
	missingAcceptance := validWorkAmendRequest("WORK-404")
	missingAcceptance.Verification = []model.VerificationKind{model.VerificationAcceptance}
	if _, err := svc.WorkAmend(ctx, missingAcceptance); !errors.Is(err, model.ErrInvalidContract) {
		t.Fatalf("amend without acceptance criteria error = %v, want ErrInvalidContract", err)
	}
	if _, err := svc.WorkAmend(ctx, validWorkAmendRequest("WORK-404")); !errors.Is(err, model.ErrWorkNotFound) {
		t.Fatalf("unknown amend error = %v, want ErrWorkNotFound", err)
	}

	for operation, call := range map[string]func() error{
		"review": func() error {
			_, err := svc.WorkReview(ctx, model.WorkReviewRequest{ID: "WORK-404", By: "qa-tester", HeadSHA: "head"})
			return err
		},
		"verify": func() error { _, err := svc.WorkVerify(ctx, model.WorkActionRequest{ID: "WORK-404"}); return err },
		"complete": func() error {
			_, err := svc.WorkComplete(ctx, model.WorkCompleteRequest{ID: "WORK-404", By: "coordinator"})
			return err
		},
	} {
		if err := call(); !errors.Is(err, model.ErrWorkNotFound) {
			t.Errorf("unknown %s error = %v, want ErrWorkNotFound", operation, err)
		}
	}
}

func TestWorkGetReturnsDefensiveCopyOfRedTestEvidence(t *testing.T) {
	svc := deliveryWorkService(t)
	ctx := context.Background()
	contract := &model.WorkContract{
		ID: "WORK-001", Project: svc.project, SourceType: model.WorkSourceOrganic,
		Status: model.WorkStatusDraft, Goal: "evidence", Scope: []string{"internal/**"},
		Verification:      []model.VerificationKind{model.VerificationBuild},
		DevelopmentMethod: model.DevelopmentMethodTDD, MaxCorrectionRounds: 1,
	}
	if err := svc.store.CreateWork(ctx, contract, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := svc.store.LockWorkAndStart(ctx, "WORK-001", "base", "coordinator"); err != nil {
		t.Fatal(err)
	}
	evidence := model.RedTestEvidence{
		Command: []string{"go", "test", "./internal/service"}, ExitCode: 1,
		OutputTail: "expected red", CommitSHA: "deadbeef", TakenAt: time.Date(2026, 9, 19, 7, 0, 0, 0, time.UTC),
	}
	if err := svc.store.SetRedTestEvidence(ctx, "WORK-001", evidence); err != nil {
		t.Fatal(err)
	}

	first, err := svc.WorkGet(ctx, model.WorkGetRequest{ID: "WORK-001"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Contract.DevEvidence == nil || !reflect.DeepEqual(first.Contract.DevEvidence.Command, evidence.Command) || first.Contract.DevEvidence.OutputTail != evidence.OutputTail {
		t.Fatalf("public evidence = %#v", first.Contract.DevEvidence)
	}
	first.Contract.DevEvidence.Command[0] = "changed"
	second, err := svc.WorkGet(ctx, model.WorkGetRequest{ID: "WORK-001"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Contract.DevEvidence.Command[0] != "go" {
		t.Fatalf("response mutation escaped into persistence: %#v", second.Contract.DevEvidence)
	}
}
