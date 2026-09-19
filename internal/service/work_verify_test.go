package service

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/quality"
	"github.com/wirvii/mneme/internal/store"
)

type deliveryRunnerStub struct {
	calls   int
	gates   []quality.Gate
	results []quality.GateResult
}

func (r *deliveryRunnerStub) Run(_ context.Context, gate quality.Gate, _ string) quality.GateResult {
	index := r.calls
	r.calls++
	r.gates = append(r.gates, gate)
	if index < len(r.results) {
		result := r.results[index]
		if result.Name == "" {
			result.Name = gate.Name
		}
		return result
	}
	return quality.GateResult{Name: gate.Name, Status: quality.GateStatusPass}
}

func runVerifyGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

func deliveryVerifyRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	runVerifyGit(t, dir, "init", "-q")
	runVerifyGit(t, dir, "config", "user.name", "Test")
	runVerifyGit(t, dir, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runVerifyGit(t, dir, "add", "tracked.txt")
	runVerifyGit(t, dir, "commit", "-q", "-m", "base")
	head, err := (&quality.Git{RepoDir: dir}).HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	return dir, head
}

func deliveryVerifyService(t *testing.T, status model.WorkStatus) (*SDDService, *deliveryRunnerStub) {
	t.Helper()
	svc := deliveryWorkService(t)
	repo, head := deliveryVerifyRepo(t)
	seedServiceWork(t, svc, "WORK-001")
	ctx := context.Background()
	transition := func(from, to model.WorkStatus) {
		t.Helper()
		reason := ""
		if to == model.WorkStatusAbandoned {
			reason = "test state"
		}
		if err := svc.store.TransitionWork(ctx, "WORK-001", from, to, "coordinator", reason); err != nil {
			t.Fatal(err)
		}
	}
	if status == model.WorkStatusAbandoned {
		transition(model.WorkStatusDraft, model.WorkStatusAbandoned)
	} else if status != model.WorkStatusDraft {
		if status == model.WorkStatusLocked {
			if err := svc.store.LockWork(ctx, "WORK-001", head); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := svc.store.LockWorkAndStart(ctx, "WORK-001", head, "coordinator"); err != nil {
				t.Fatal(err)
			}
			if status != model.WorkStatusImplementing {
				transition(model.WorkStatusImplementing, model.WorkStatusVerifying)
				switch status {
				case model.WorkStatusCorrecting:
					transition(model.WorkStatusVerifying, model.WorkStatusCorrecting)
				case model.WorkStatusTargetedVerifying:
					transition(model.WorkStatusVerifying, model.WorkStatusCorrecting)
					transition(model.WorkStatusCorrecting, model.WorkStatusTargetedVerifying)
				case model.WorkStatusEscalated:
					transition(model.WorkStatusVerifying, model.WorkStatusEscalated)
				case model.WorkStatusDone:
					transition(model.WorkStatusVerifying, model.WorkStatusDone)
				}
			}
		}
	}
	runner := &deliveryRunnerStub{}
	svc.WithRepoDir(repo)
	svc.WithDeliveryVerifier(func(int) quality.Runner { return runner }, "test-version")
	return svc, runner
}

func TestWorkVerify_OnlyVerifyingStatesPersistWithoutTransition(t *testing.T) {
	for _, status := range []model.WorkStatus{
		model.WorkStatusDraft,
		model.WorkStatusLocked,
		model.WorkStatusImplementing,
		model.WorkStatusCorrecting,
		model.WorkStatusEscalated,
		model.WorkStatusDone,
		model.WorkStatusAbandoned,
		model.WorkStatusVerifying,
		model.WorkStatusTargetedVerifying,
	} {
		t.Run(string(status), func(t *testing.T) {
			svc, runner := deliveryVerifyService(t, status)
			allowed := status == model.WorkStatusVerifying || status == model.WorkStatusTargetedVerifying
			if !allowed {
				svc.WithRepoDir(t.TempDir())
			}
			before, err := svc.store.GetWorkAggregate(context.Background(), "WORK-001")
			if err != nil {
				t.Fatal(err)
			}
			result, err := svc.WorkVerify(context.Background(), model.WorkActionRequest{ID: "WORK-001"})
			if !allowed {
				if !errors.Is(err, model.ErrInvalidWorkTransition) {
					t.Fatalf("error=%v", err)
				}
				if _, latestErr := svc.store.GetLatestDeliveryCertificate(context.Background(), "p", "WORK-001"); !errors.Is(latestErr, model.ErrNotFound) {
					t.Fatalf("certificate error=%v", latestErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !result.Available || !result.Performed || result.Operation != "verify" || result.Certificate == nil || len(result.Checks) == 0 {
				t.Fatalf("result=%#v", result)
			}
			if result.Certificate.ID == "" || result.Checks[0].CertificateID != result.Certificate.ID {
				t.Fatalf("certificate=%#v checks=%#v", result.Certificate, result.Checks)
			}
			head, headErr := (&quality.Git{RepoDir: svc.repoDir}).HeadSHA()
			if headErr != nil {
				t.Fatal(headErr)
			}
			if result.Certificate.Project != before.Contract.Project || result.Certificate.WorkID != before.Contract.ID || result.Certificate.ContractRevision != before.Contract.ContractRevision || result.Certificate.ContractHash != before.Contract.ContractHash || result.Certificate.BaseSHA != before.Contract.BaseSHA || result.Certificate.HeadSHA != head || result.Certificate.MnemeVersion != "test-version" {
				t.Fatalf("certificate does not match snapshot: cert=%#v work=%#v head=%q", result.Certificate, before.Contract, head)
			}
			after, getErr := svc.store.GetWorkAggregate(context.Background(), "WORK-001")
			if getErr != nil {
				t.Fatal(getErr)
			}
			if after.Contract.Status != status || len(after.History) != len(before.History) {
				t.Fatalf("state/history changed: before=%#v after=%#v", before.Contract, after.Contract)
			}
			if runner.calls != 0 {
				t.Fatalf("skeleton verification executed %d commands", runner.calls)
			}
		})
	}
}

func TestWorkVerify_RequiresExplicitDependenciesBeforePersisting(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SDDService)
	}{
		{name: "repo", mutate: func(svc *SDDService) { svc.WithRepoDir("") }},
		{name: "runner factory", mutate: func(svc *SDDService) { svc.WithDeliveryVerifier(nil, "test-version") }},
		{name: "version", mutate: func(svc *SDDService) {
			svc.WithDeliveryVerifier(func(int) quality.Runner { return &deliveryRunnerStub{} }, "")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, runner := deliveryVerifyService(t, model.WorkStatusVerifying)
			tt.mutate(svc)
			if _, err := svc.WorkVerify(context.Background(), model.WorkActionRequest{ID: "WORK-001"}); !errors.Is(err, model.ErrInvalidContract) {
				t.Fatalf("error=%v", err)
			}
			if runner.calls != 0 {
				t.Fatalf("runner calls=%d", runner.calls)
			}
			if _, err := svc.store.GetLatestDeliveryCertificate(context.Background(), "p", "WORK-001"); !errors.Is(err, model.ErrNotFound) {
				t.Fatalf("certificate error=%v", err)
			}
		})
	}
}

func TestWorkVerify_LegacyHasNoEffects(t *testing.T) {
	svc := newTestSDDService(t, "p")
	seedServiceWork(t, svc, "WORK-001")
	if _, err := svc.WorkVerify(context.Background(), model.WorkActionRequest{ID: "WORK-001"}); !errors.Is(err, model.ErrWorkflowEngineDisabled) {
		t.Fatalf("error=%v", err)
	}
	if _, err := svc.store.GetLatestDeliveryCertificate(context.Background(), "p", "WORK-001"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("certificate error=%v", err)
	}
}

type criteriaVerifyFixture struct {
	svc      *SDDService
	runner   *deliveryRunnerStub
	database *db.DB
	repo     string
}

func writeVerifyFiles(t *testing.T, repo string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func newCriteriaVerifyFixture(t *testing.T, criteria []model.WorkCriterionInput, baseFiles, headFiles map[string]string) criteriaVerifyFixture {
	t.Helper()
	repo := t.TempDir()
	runVerifyGit(t, repo, "init", "-q")
	runVerifyGit(t, repo, "config", "user.name", "Test")
	runVerifyGit(t, repo, "config", "user.email", "test@example.com")
	writeVerifyFiles(t, repo, baseFiles)
	runVerifyGit(t, repo, "add", ".")
	runVerifyGit(t, repo, "commit", "-q", "-m", "base")
	base, err := (&quality.Git{RepoDir: repo}).HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	writeVerifyFiles(t, repo, headFiles)
	if len(headFiles) > 0 {
		runVerifyGit(t, repo, "add", ".")
		runVerifyGit(t, repo, "commit", "-q", "-m", "head")
	}

	database, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })
	cfg := config.Default()
	cfg.Workflow.Engine = config.WorkflowEngineDeliveryV2
	svc := NewSDDService(store.NewSDDStore(database), cfg, "p", nil)
	work, err := svc.WorkBegin(context.Background(), model.WorkBeginRequest{
		Goal: "criteria", Scope: []string{"**"}, Verification: []model.VerificationKind{model.VerificationAcceptance},
		DevelopmentMethod: model.DevelopmentMethodStandard, Criteria: criteria,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.store.LockWorkAndStart(context.Background(), work.Contract.ID, base, "coordinator"); err != nil {
		t.Fatal(err)
	}
	if err := svc.store.TransitionWork(context.Background(), work.Contract.ID, model.WorkStatusImplementing, model.WorkStatusVerifying, "coordinator", ""); err != nil {
		t.Fatal(err)
	}
	runner := &deliveryRunnerStub{}
	svc.WithRepoDir(repo)
	svc.WithDeliveryVerifier(func(int) quality.Runner { return runner }, "test-version")
	return criteriaVerifyFixture{svc: svc, runner: runner, database: database, repo: repo}
}

func deliveryCheckByName(t *testing.T, checks []model.DeliveryCheck, kind, name string) model.DeliveryCheck {
	t.Helper()
	for _, check := range checks {
		if check.Kind == kind && check.Name == name {
			return check
		}
	}
	t.Fatalf("missing check %s/%s in %#v", kind, name, checks)
	return model.DeliveryCheck{}
}

func TestWorkVerify_CriteriaParserRejectsStoredDrift(t *testing.T) {
	criterion := model.WorkCriterionInput{Key: "manual", Declaration: `[[criterion]]
id = "manual"
mode = "manual"
text = "review manually"
evidence_required = "review note"`}
	fixture := newCriteriaVerifyFixture(t, []model.WorkCriterionInput{criterion}, map[string]string{"tracked.txt": "base\n"}, nil)
	if _, err := fixture.database.Exec(`UPDATE execution_criteria SET declaration=replace(declaration,'id = "manual"','id = "changed"') WHERE work_id='WORK-001'`); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.svc.WorkVerify(context.Background(), model.WorkActionRequest{ID: "WORK-001"})
	if err != nil {
		t.Fatal(err)
	}
	check := deliveryCheckByName(t, result.Checks, "acceptance", "parse")
	if check.Status != model.DeliveryCheckFail || check.Effect != model.DeliveryEffectBlocks || fixture.runner.calls != 0 || result.Certificate.Verdict != model.DeliveryVerdictFail {
		t.Fatalf("check=%#v calls=%d certificate=%#v", check, fixture.runner.calls, result.Certificate)
	}
}

func TestWorkVerify_AssertOutcomesAndExistingVerbs(t *testing.T) {
	tests := []struct {
		name          string
		criterion     model.WorkCriterionInput
		baseFiles     map[string]string
		headFiles     map[string]string
		mutateBase    bool
		wantStatus    model.DeliveryCheckStatus
		wantOutcome   string
		wantCriterion model.CriterionStatus
	}{
		{
			name: "file exists passes only at head",
			criterion: model.WorkCriterionInput{Key: "file", Declaration: `[[criterion]]
id = "file"
mode = "assert"
text = "new file exists"
  [[criterion.assert]]
  verb = "file_exists"
  path = "future.txt"
  new = true`},
			baseFiles: map[string]string{"tracked.txt": "base\n"}, headFiles: map[string]string{"future.txt": "new\n"},
			wantStatus: model.DeliveryCheckPass, wantOutcome: `"outcome":"pass"`, wantCriterion: model.CriterionPass,
		},
		{
			name: "vacuous at base",
			criterion: model.WorkCriterionInput{Key: "file", Declaration: `[[criterion]]
id = "file"
mode = "assert"
text = "existing file"
  [[criterion.assert]]
  verb = "file_exists"
  path = "tracked.txt"
  new = false`},
			baseFiles:  map[string]string{"tracked.txt": "base\n"},
			wantStatus: model.DeliveryCheckNotReviewed, wantOutcome: `"outcome":"vacuous"`, wantCriterion: model.CriterionVacuous,
		},
		{
			name: "new anchor preexisted",
			criterion: model.WorkCriterionInput{Key: "file", Declaration: `[[criterion]]
id = "file"
mode = "assert"
text = "claimed new file"
  [[criterion.assert]]
  verb = "file_exists"
  path = "tracked.txt"
  new = true`},
			baseFiles:  map[string]string{"tracked.txt": "base\n"},
			wantStatus: model.DeliveryCheckNotReviewed, wantOutcome: `"outcome":"anchor-not-new"`, wantCriterion: model.CriterionFail,
		},
		{
			name: "base unreachable",
			criterion: model.WorkCriterionInput{Key: "file", Declaration: `[[criterion]]
id = "file"
mode = "assert"
text = "existing file"
  [[criterion.assert]]
  verb = "file_exists"
  path = "tracked.txt"
  new = false`},
			baseFiles: map[string]string{"tracked.txt": "base\n"}, mutateBase: true,
			wantStatus: model.DeliveryCheckNotReviewed, wantOutcome: `"outcome":"base-unknown"`, wantCriterion: model.CriterionFail,
		},
		{
			name: "pattern and symbols preserve evaluator semantics",
			criterion: model.WorkCriterionInput{Key: "semantics", Declaration: `[[criterion]]
id = "semantics"
mode = "assert"
text = "existing verbs"
  [[criterion.assert]]
  verb = "pattern_count"
  contains = "Needle"
  in = ["src/*.go"]
  word = true
  comparator = "=="
  count = 1
  new = false
  [[criterion.assert]]
  verb = "symbol_defined"
  symbol = "Widget"
  in = ["src/def.go"]
  new = false
  [[criterion.assert]]
  verb = "symbol_referenced"
  symbol = "Widget"
  defined_in = ["src/def.go"]
  ignore = ["tests/**"]
  new = false`},
			baseFiles:  map[string]string{"src/def.go": "package src\n", "src/use.go": "package src\n", "tests/ignored.go": "package tests\nvar _ = Widget\n"},
			headFiles:  map[string]string{"src/def.go": "package src\ntype Widget struct{}\n", "src/use.go": "package src\nvar Needle = Widget{}\nvar NeedleExtra = 1\n"},
			wantStatus: model.DeliveryCheckPass, wantOutcome: `"outcome":"pass"`, wantCriterion: model.CriterionPass,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newCriteriaVerifyFixture(t, []model.WorkCriterionInput{tt.criterion}, tt.baseFiles, tt.headFiles)
			if tt.mutateBase {
				if _, err := fixture.database.Exec(`UPDATE execution_contracts SET base_sha='ffffffffffffffffffffffffffffffffffffffff' WHERE id='WORK-001'`); err != nil {
					t.Fatal(err)
				}
			}
			result, err := fixture.svc.WorkVerify(context.Background(), model.WorkActionRequest{ID: "WORK-001"})
			if err != nil {
				t.Fatal(err)
			}
			check := deliveryCheckByName(t, result.Checks, "criterion", tt.criterion.Key)
			if check.Status != tt.wantStatus || check.Effect != model.DeliveryEffectBlocks || !strings.Contains(check.Detail, tt.wantOutcome) {
				t.Fatalf("check=%#v", check)
			}
			if result.Work.Criteria[0].Status != tt.wantCriterion {
				t.Fatalf("criterion=%#v", result.Work.Criteria[0])
			}
		})
	}
}

func TestWorkVerify_CommandAndManualRequireCurrentEvidence(t *testing.T) {
	command := model.WorkCriterionInput{Key: "command", Declaration: `[[criterion]]
id = "command"
mode = "command"
text = "command criterion"
command = ["project-check"]
timeout = "1m"`}
	manual := model.WorkCriterionInput{Key: "manual", Declaration: `[[criterion]]
id = "manual"
mode = "manual"
text = "manual criterion"
evidence_required = "review note"`}
	tests := []struct {
		name       string
		criterion  model.WorkCriterionInput
		sign       bool
		gateStatus quality.GateStatus
		wantStatus model.DeliveryCheckStatus
		wantCalls  int
		wantDetail string
	}{
		{name: "green command unsigned", criterion: command, gateStatus: quality.GateStatusPass, wantStatus: model.DeliveryCheckNotReviewed, wantCalls: 1, wantDetail: "vacuity-unprovable"},
		{name: "green command signed", criterion: command, sign: true, gateStatus: quality.GateStatusPass, wantStatus: model.DeliveryCheckPass, wantCalls: 1},
		{name: "red command signed", criterion: command, sign: true, gateStatus: quality.GateStatusFail, wantStatus: model.DeliveryCheckFail, wantCalls: 1},
		{name: "manual pending", criterion: manual, wantStatus: model.DeliveryCheckNotReviewed, wantCalls: 0, wantDetail: "manual-unverified"},
		{name: "manual signed", criterion: manual, sign: true, wantStatus: model.DeliveryCheckPass, wantCalls: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newCriteriaVerifyFixture(t, []model.WorkCriterionInput{tt.criterion}, map[string]string{"tracked.txt": "base\n"}, nil)
			fixture.runner.results = []quality.GateResult{{Status: tt.gateStatus, ExitCode: map[bool]int{true: 0, false: 1}[tt.gateStatus == quality.GateStatusPass], OutputSHA256: "digest", OutputTail: "tail", DurationMs: 7}}
			if tt.sign {
				aggregate, err := fixture.svc.store.GetWorkAggregate(context.Background(), "WORK-001")
				if err != nil {
					t.Fatal(err)
				}
				if err := fixture.svc.store.UpdateCriterionResult(context.Background(), aggregate.Criteria[0].ID, model.CriterionSigned, "owner evidence", "owner", time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			}
			result, err := fixture.svc.WorkVerify(context.Background(), model.WorkActionRequest{ID: "WORK-001"})
			if err != nil {
				t.Fatal(err)
			}
			check := deliveryCheckByName(t, result.Checks, "criterion", tt.criterion.Key)
			if check.Status != tt.wantStatus || fixture.runner.calls != tt.wantCalls || !strings.Contains(check.Detail, tt.wantDetail) {
				t.Fatalf("check=%#v calls=%d", check, fixture.runner.calls)
			}
			if tt.sign && result.Work.Criteria[0].Status != model.CriterionSigned {
				t.Fatalf("signed observation changed: %#v", result.Work.Criteria[0])
			}
		})
	}
}
