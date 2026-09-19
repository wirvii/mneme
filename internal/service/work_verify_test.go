package service

import (
	"context"
	"errors"
	"fmt"
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

func TestWorkVerify_PreservesOnlyCurrentMarkedReview(t *testing.T) {
	t.Run("matching review preserves architecture and recalculates blockers", func(t *testing.T) {
		svc, _ := reviewServiceWithConstraints(t, []model.WorkConstraint{{Key: "layers", Text: "inward"}})
		head := commitReviewConstitution(t, svc)
		req := reviewRequest(head)
		req.ArchitectureVerdicts = []model.WorkArchitectureVerdictInput{{ConstraintKey: "layers", Status: model.DeliveryCheckPass, EvidenceKind: model.ReviewEvidenceFile, Evidence: "internal/service/work_review.go"}}
		_, err := svc.WorkReview(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		finding := &model.WorkFinding{WorkID: "WORK-001", Category: model.FindingRegression, Severity: model.PriorityLow, Description: "regression", Evidence: "test", Origin: model.FindingOriginReview, ReviewPhase: model.ReviewPhaseInitial}
		if err := svc.store.AddFinding(context.Background(), finding); err != nil {
			t.Fatal(err)
		}
		if err := svc.store.ResolveFinding(context.Background(), finding.ID, model.FindingFixed, "backend", "fixed", ""); err != nil {
			t.Fatal(err)
		}
		verified, err := svc.WorkVerify(context.Background(), model.WorkActionRequest{ID: "WORK-001"})
		if err != nil {
			t.Fatal(err)
		}
		architecture := reviewChecksByKind(verified.Checks, "architecture")
		reviewRows := reviewChecksByKind(verified.Checks, "review")
		if len(architecture) != 1 || architecture[0].Name != "layers" || architecture[0].Status != model.DeliveryCheckPass {
			t.Fatalf("architecture = %#v", architecture)
		}
		if len(reviewRows) != 2 || reviewRows[0].Name != "initial" || reviewRows[1].Name != "open-blocking-findings" || reviewRows[1].Status != model.DeliveryCheckPass || reviewRows[1].Detail != "0 open blocking finding(s)" {
			t.Fatalf("review rows = %#v", reviewRows)
		}
	})

	t.Run("certificate without marker is not reused", func(t *testing.T) {
		svc, _, _ := reviewService(t, model.WorkStatusVerifying)
		first, err := svc.WorkVerify(context.Background(), model.WorkActionRequest{ID: "WORK-001"})
		if err != nil {
			t.Fatal(err)
		}
		if len(reviewChecksByKind(first.Checks, "review")) != 0 {
			t.Fatalf("unmarked certificate gained review rows: %#v", first.Checks)
		}
		second, err := svc.WorkVerify(context.Background(), model.WorkActionRequest{ID: "WORK-001"})
		if err != nil {
			t.Fatal(err)
		}
		if len(reviewChecksByKind(second.Checks, "review")) != 0 {
			t.Fatalf("unmarked certificate reused review rows: %#v", second.Checks)
		}
	})

	t.Run("changed head rejects old review", func(t *testing.T) {
		svc, _ := reviewServiceWithConstraints(t, []model.WorkConstraint{{Key: "layers", Text: "inward"}})
		head := commitReviewConstitution(t, svc)
		req := reviewRequest(head)
		req.ArchitectureVerdicts = []model.WorkArchitectureVerdictInput{{ConstraintKey: "layers", Status: model.DeliveryCheckPass, EvidenceKind: model.ReviewEvidenceFile, Evidence: "internal/service/work_review.go"}}
		if _, err := svc.WorkReview(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(svc.repoDir, "next.txt"), []byte("next\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		runVerifyGit(t, svc.repoDir, "add", "next.txt")
		runVerifyGit(t, svc.repoDir, "commit", "-q", "-m", "next")
		verified, err := svc.WorkVerify(context.Background(), model.WorkActionRequest{ID: "WORK-001"})
		if err != nil {
			t.Fatal(err)
		}
		architecture := reviewChecksByKind(verified.Checks, "architecture")
		if len(architecture) != 1 || architecture[0].Status != model.DeliveryCheckNotReviewed || len(reviewChecksByKind(verified.Checks, "review")) != 0 {
			t.Fatalf("stale review reused: %#v", verified.Checks)
		}
	})

	t.Run("changed revision and hash reject old review", func(t *testing.T) {
		svc, _ := reviewServiceWithConstraints(t, []model.WorkConstraint{{Key: "layers", Text: "inward"}})
		head := commitReviewConstitution(t, svc)
		req := reviewRequest(head)
		req.ArchitectureVerdicts = []model.WorkArchitectureVerdictInput{{ConstraintKey: "layers", Status: model.DeliveryCheckPass, EvidenceKind: model.ReviewEvidenceFile, Evidence: "internal/service/work_review.go"}}
		if _, err := svc.WorkReview(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.WorkAmend(context.Background(), model.WorkAmendRequest{
			ID: "WORK-001", Goal: "changed", Scope: []string{"internal/**"},
			Verification: []model.VerificationKind{model.VerificationBuild}, DevelopmentMethod: model.DevelopmentMethodStandard,
			Constraints: []model.WorkConstraintInput{{Key: "layers", Text: "changed"}}, By: "coordinator", Reason: "change contract",
		}); err != nil {
			t.Fatal(err)
		}
		if err := svc.store.TransitionWork(context.Background(), "WORK-001", model.WorkStatusImplementing, model.WorkStatusVerifying, "coordinator", ""); err != nil {
			t.Fatal(err)
		}
		verified, err := svc.WorkVerify(context.Background(), model.WorkActionRequest{ID: "WORK-001"})
		if err != nil {
			t.Fatal(err)
		}
		architecture := reviewChecksByKind(verified.Checks, "architecture")
		if len(architecture) != 1 || architecture[0].Status != model.DeliveryCheckNotReviewed || len(reviewChecksByKind(verified.Checks, "review")) != 0 {
			t.Fatalf("old contract review reused: %#v", verified.Checks)
		}
	})
}

func TestWorkVerify_PreservesOnlyCurrentTargetedReview(t *testing.T) {
	svc, _, req, _ := targetedReviewService(t)
	targeted, err := svc.WorkReview(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := svc.WorkVerify(context.Background(), model.WorkActionRequest{ID: "WORK-001"})
	if err != nil {
		t.Fatal(err)
	}
	rows := reviewChecksByKind(verified.Checks, "review")
	if len(rows) != 2 || rows[0].Name != "targeted" || rows[0].Detail != reviewChecksByKind(targeted.Checks, "review")[0].Detail || rows[1].Name != "open-blocking-findings" || rows[1].Status != model.DeliveryCheckPass {
		t.Fatalf("review rows = %#v", rows)
	}
}

func TestWorkVerify_TargetedFailureDoesNotTransition(t *testing.T) {
	svc, runner, req, _ := targetedReviewService(t)
	if _, err := svc.WorkReview(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	before, _ := svc.store.GetWorkAggregate(context.Background(), "WORK-001")
	runner.calls = 0
	runner.results = []quality.GateResult{{Status: quality.GateStatusFail, OutputTail: "red"}}
	result, err := svc.WorkVerify(context.Background(), model.WorkActionRequest{ID: "WORK-001"})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := svc.store.GetWorkAggregate(context.Background(), "WORK-001")
	if result.Certificate.Verdict != model.DeliveryVerdictFail || after.Contract.Status != model.WorkStatusTargetedVerifying || after.Contract.CorrectionRounds != before.Contract.CorrectionRounds || len(after.History) != len(before.History) {
		t.Fatalf("result=%#v before=%#v after=%#v", result, before.Contract, after.Contract)
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
	tail     *[]int
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
	tail := []int{}
	svc.WithRepoDir(repo)
	svc.WithDeliveryVerifier(func(maxTailBytes int) quality.Runner {
		tail = append(tail, maxTailBytes)
		return runner
	}, "test-version")
	return criteriaVerifyFixture{svc: svc, runner: runner, database: database, repo: repo, tail: &tail}
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

func TestWorkVerify_CriteriaCascadeStopsBeforeCommand(t *testing.T) {
	tests := []struct {
		name            string
		blocking        model.WorkCriterionInput
		wantCheck       model.DeliveryCheckStatus
		wantObservation model.CriterionStatus
	}{
		{name: "fail", blocking: model.WorkCriterionInput{Key: "blocking", Declaration: `[[criterion]]
id = "blocking"
mode = "assert"
text = "required file exists"
  [[criterion.assert]]
  verb = "file_exists"
  path = "missing.txt"
  new = true`}, wantCheck: model.DeliveryCheckFail, wantObservation: model.CriterionFail},
		{name: "not reviewed", blocking: model.WorkCriterionInput{Key: "blocking", Declaration: `[[criterion]]
id = "blocking"
mode = "assert"
text = "existing file remains"
  [[criterion.assert]]
  verb = "file_exists"
  path = "tracked.txt"
  new = false`}, wantCheck: model.DeliveryCheckNotReviewed, wantObservation: model.CriterionVacuous},
	}
	command := model.WorkCriterionInput{Key: "command", Declaration: `[[criterion]]
id = "command"
mode = "command"
text = "must not run after a blocker"
command = ["project-check"]
timeout = "1m"`}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newCriteriaVerifyFixture(t, []model.WorkCriterionInput{tt.blocking, command}, map[string]string{"tracked.txt": "base\n"}, nil)

			result, err := fixture.svc.WorkVerify(context.Background(), model.WorkActionRequest{ID: "WORK-001"})
			if err != nil {
				t.Fatal(err)
			}
			if fixture.runner.calls != 0 {
				t.Fatalf("runner calls=%d, want 0", fixture.runner.calls)
			}
			if len(result.Checks) < 3 || result.Checks[1].Name != "blocking" || result.Checks[1].Status != tt.wantCheck || result.Checks[2].Name != "command" {
				t.Fatalf("criterion order or blocker changed: %#v", result.Checks)
			}
			stopped := result.Checks[2]
			if stopped.Status != model.DeliveryCheckSkipped || stopped.Effect != model.DeliveryEffectStopped {
				t.Fatalf("stopped criterion=%#v", stopped)
			}
			if result.Work.Criteria[0].Status != tt.wantObservation || result.Work.Criteria[1].Status != model.CriterionPending {
				t.Fatalf("criteria=%#v", result.Work.Criteria)
			}
			persisted, err := fixture.svc.store.ListDeliveryChecks(context.Background(), result.Certificate.ID)
			if err != nil {
				t.Fatal(err)
			}
			persistedStopped := deliveryCheckByName(t, persisted, "criterion", "command")
			if persistedStopped.Status != model.DeliveryCheckSkipped || persistedStopped.Effect != model.DeliveryEffectStopped {
				t.Fatalf("persisted stopped criterion=%#v", persistedStopped)
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

func deliveryConstitution(gates ...string) string {
	var b strings.Builder
	b.WriteString("schema_version = 1\nenabled = false\n\n[execution]\noutput_tail_bytes = 7\n")
	for _, name := range gates {
		fmt.Fprintf(&b, "\n[[gate]]\nname = %q\ncommand = [%q]\ntimeout = \"1m\"\nrequired = false\n", name, "run-"+name)
	}
	return b.String()
}

func newGateVerifyFixture(t *testing.T, verification []model.VerificationKind, constitution string) criteriaVerifyFixture {
	t.Helper()
	repo := t.TempDir()
	runVerifyGit(t, repo, "init", "-q")
	runVerifyGit(t, repo, "config", "user.name", "Test")
	runVerifyGit(t, repo, "config", "user.email", "test@example.com")
	writeVerifyFiles(t, repo, map[string]string{"tracked.txt": "base\n"})
	if constitution != "" {
		writeVerifyFiles(t, repo, map[string]string{constitutionRelPath: constitution})
	}
	runVerifyGit(t, repo, "add", ".")
	runVerifyGit(t, repo, "commit", "-q", "-m", "base")
	head, err := (&quality.Git{RepoDir: repo}).HeadSHA()
	if err != nil {
		t.Fatal(err)
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
		Goal: "gates", Scope: []string{"**"}, Verification: verification,
		DevelopmentMethod: model.DevelopmentMethodStandard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.store.LockWorkAndStart(context.Background(), work.Contract.ID, head, "coordinator"); err != nil {
		t.Fatal(err)
	}
	if err := svc.store.TransitionWork(context.Background(), work.Contract.ID, model.WorkStatusImplementing, model.WorkStatusVerifying, "coordinator", ""); err != nil {
		t.Fatal(err)
	}
	runner := &deliveryRunnerStub{}
	tail := []int{}
	svc.WithRepoDir(repo)
	svc.WithDeliveryVerifier(func(maxTailBytes int) quality.Runner {
		tail = append(tail, maxTailBytes)
		return runner
	}, "test-version")
	return criteriaVerifyFixture{svc: svc, runner: runner, database: database, repo: repo, tail: &tail}
}

func TestWorkVerify_RequiredChecksUseClosedMappingAndContractAuthority(t *testing.T) {
	fixture := newGateVerifyFixture(t,
		[]model.VerificationKind{model.VerificationLint, model.VerificationBuild, model.VerificationAffectedTests},
		deliveryConstitution("lint", "unused", "test", "build"),
	)
	fixture.runner.results = []quality.GateResult{
		{Status: quality.GateStatusPass, ExitCode: 0, OutputSHA256: "test-sha", OutputTail: "1234567", DurationMs: 1},
		{Status: quality.GateStatusPass, ExitCode: 0, OutputSHA256: "build-sha", OutputTail: "build", DurationMs: 2},
		{Status: quality.GateStatusPass, ExitCode: 0, OutputSHA256: "lint-sha", OutputTail: "lint", DurationMs: 3},
	}
	result, err := fixture.svc.WorkVerify(context.Background(), model.WorkActionRequest{ID: "WORK-001"})
	if err != nil {
		t.Fatal(err)
	}
	if fixture.runner.calls != 3 || len(fixture.runner.gates) != 3 || fixture.runner.gates[0].Name != "test" || fixture.runner.gates[1].Name != "build" || fixture.runner.gates[2].Name != "lint" {
		t.Fatalf("gates=%#v", fixture.runner.gates)
	}
	if len(*fixture.tail) != 1 || (*fixture.tail)[0] != 7 {
		t.Fatalf("tail limits=%v", *fixture.tail)
	}
	if result.Certificate.Verdict != model.DeliveryVerdictPass {
		t.Fatalf("certificate=%#v checks=%#v", result.Certificate, result.Checks)
	}
	testCheck := deliveryCheckByName(t, result.Checks, "gate", string(model.VerificationAffectedTests))
	if testCheck.OutputSHA256 != "test-sha" || testCheck.OutputTail != "1234567" || testCheck.DurationMs != 1 {
		t.Fatalf("test check=%#v", testCheck)
	}
}

func TestWorkVerify_CascadeStopsCommandsAfterFirstBlockingResult(t *testing.T) {
	fixture := newGateVerifyFixture(t,
		[]model.VerificationKind{model.VerificationAffectedTests, model.VerificationBuild, model.VerificationLint},
		deliveryConstitution("test", "build", "lint"),
	)
	fixture.runner.results = []quality.GateResult{{Status: quality.GateStatusFail, ExitCode: 2, OutputSHA256: "failed", OutputTail: "red"}}
	result, err := fixture.svc.WorkVerify(context.Background(), model.WorkActionRequest{ID: "WORK-001"})
	if err != nil {
		t.Fatal(err)
	}
	if fixture.runner.calls != 1 {
		t.Fatalf("runner calls=%d", fixture.runner.calls)
	}
	if deliveryCheckByName(t, result.Checks, "gate", string(model.VerificationAffectedTests)).Status != model.DeliveryCheckFail {
		t.Fatalf("checks=%#v", result.Checks)
	}
	for _, kind := range []model.VerificationKind{model.VerificationBuild, model.VerificationLint} {
		check := deliveryCheckByName(t, result.Checks, "gate", string(kind))
		if check.Status != model.DeliveryCheckSkipped || check.Effect != model.DeliveryEffectStopped {
			t.Fatalf("stopped %s=%#v", kind, check)
		}
	}
}

func TestWorkVerify_RequiredChecksRejectMissingInvalidOrIncompleteConfiguration(t *testing.T) {
	tests := []struct {
		name         string
		constitution string
	}{
		{name: "missing"},
		{name: "invalid", constitution: "not toml = ["},
		{name: "gate absent", constitution: deliveryConstitution("lint")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newGateVerifyFixture(t, []model.VerificationKind{model.VerificationBuild}, tt.constitution)
			result, err := fixture.svc.WorkVerify(context.Background(), model.WorkActionRequest{ID: "WORK-001"})
			if err != nil {
				t.Fatal(err)
			}
			if fixture.runner.calls != 0 || result.Certificate.Verdict != model.DeliveryVerdictFail {
				t.Fatalf("calls=%d certificate=%#v checks=%#v", fixture.runner.calls, result.Certificate, result.Checks)
			}
			check := deliveryCheckByName(t, result.Checks, "configuration", string(model.VerificationBuild))
			if check.Status != model.DeliveryCheckFail || check.Effect != model.DeliveryEffectBlocks {
				t.Fatalf("configuration check=%#v", check)
			}
		})
	}
}

func TestWorkVerify_DirtyTreePersistsFailureWithoutCommands(t *testing.T) {
	fixture := newGateVerifyFixture(t, []model.VerificationKind{model.VerificationBuild}, deliveryConstitution("build"))
	if err := os.WriteFile(filepath.Join(fixture.repo, "untracked.txt"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.svc.WorkVerify(context.Background(), model.WorkActionRequest{ID: "WORK-001"})
	if err != nil {
		t.Fatal(err)
	}
	check := deliveryCheckByName(t, result.Checks, "environment", "clean-worktree")
	if !result.Certificate.Dirty || result.Certificate.Verdict != model.DeliveryVerdictFail || check.Status != model.DeliveryCheckFail || fixture.runner.calls != 0 || len(*fixture.tail) != 0 {
		t.Fatalf("certificate=%#v check=%#v calls=%d tails=%v", result.Certificate, check, fixture.runner.calls, *fixture.tail)
	}
}

func TestWorkVerify_ConstraintsAreExplicitAndSorted(t *testing.T) {
	fixture := newGateVerifyFixture(t, []model.VerificationKind{model.VerificationBuild}, deliveryConstitution("build"))
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, row := range []struct {
		id, key string
		seq     int
	}{{"constraint-z", "zeta", 1}, {"constraint-a", "alpha", 2}} {
		if _, err := fixture.database.Exec(`INSERT INTO execution_constraints(id,work_id,seq,constraint_key,text,source,created_at) VALUES(?,?,?,?,?,?,?)`, row.id, "WORK-001", row.seq, row.key, "must hold", "owner", now); err != nil {
			t.Fatal(err)
		}
	}
	result, err := fixture.svc.WorkVerify(context.Background(), model.WorkActionRequest{ID: "WORK-001"})
	if err != nil {
		t.Fatal(err)
	}
	var architecture []model.DeliveryCheck
	for _, check := range result.Checks {
		if check.Kind == "architecture" {
			architecture = append(architecture, check)
		}
	}
	if len(architecture) != 2 || architecture[0].Name != "alpha" || architecture[1].Name != "zeta" {
		t.Fatalf("architecture=%#v", architecture)
	}
	for _, check := range architecture {
		if check.Status != model.DeliveryCheckNotReviewed || check.Effect != model.DeliveryEffectBlocks {
			t.Fatalf("architecture check=%#v", check)
		}
	}
	if result.Certificate.Verdict != model.DeliveryVerdictFail || result.Certificate.Verdict != model.DeriveDeliveryVerdict(result.Checks) {
		t.Fatalf("certificate=%#v checks=%#v", result.Certificate, result.Checks)
	}

	without := newGateVerifyFixture(t, []model.VerificationKind{model.VerificationBuild}, deliveryConstitution("build"))
	clean, err := without.svc.WorkVerify(context.Background(), model.WorkActionRequest{ID: "WORK-001"})
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range clean.Checks {
		if check.Kind == "architecture" {
			t.Fatalf("invented architecture check=%#v", check)
		}
	}
}

func TestWorkVerify_TDDEvidenceIsVisibleButNeverBlocks(t *testing.T) {
	tests := []struct {
		name       string
		method     model.DevelopmentMethod
		withRed    bool
		wantStatus model.DeliveryCheckStatus
		wantEffect model.DeliveryCheckEffect
	}{
		{name: "tdd with evidence", method: model.DevelopmentMethodTDD, withRed: true, wantStatus: model.DeliveryCheckPass, wantEffect: model.DeliveryEffectMeasures},
		{name: "tdd without evidence", method: model.DevelopmentMethodTDD, wantStatus: model.DeliveryCheckNotReviewed, wantEffect: model.DeliveryEffectMeasures},
		{name: "standard", method: model.DevelopmentMethodStandard, wantStatus: model.DeliveryCheckSkipped, wantEffect: model.DeliveryEffectAbsent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newGateVerifyFixture(t, []model.VerificationKind{model.VerificationBuild}, deliveryConstitution("build"))
			if _, err := fixture.database.Exec(`UPDATE execution_contracts SET development_method=? WHERE id='WORK-001'`, tt.method); err != nil {
				t.Fatal(err)
			}
			if tt.withRed {
				if _, err := fixture.database.Exec(`UPDATE execution_contracts SET dev_evidence_command='["go","test"]',dev_evidence_exit=1,dev_evidence_tail='red output',dev_evidence_sha='abc',dev_evidence_at=? WHERE id='WORK-001'`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
					t.Fatal(err)
				}
			}
			result, err := fixture.svc.WorkVerify(context.Background(), model.WorkActionRequest{ID: "WORK-001"})
			if err != nil {
				t.Fatal(err)
			}
			check := deliveryCheckByName(t, result.Checks, "tdd-evidence", "red-test")
			if check.Status != tt.wantStatus || check.Effect != tt.wantEffect {
				t.Fatalf("check=%#v", check)
			}
			if result.Certificate.Verdict != model.DeliveryVerdictPass {
				t.Fatalf("TDD evidence changed verdict: certificate=%#v checks=%#v", result.Certificate, result.Checks)
			}
		})
	}
}

func TestWorkVerify_CertificateAndEvidenceComeFromPersistedChecks(t *testing.T) {
	fixture := newGateVerifyFixture(t, []model.VerificationKind{model.VerificationAffectedTests, model.VerificationBuild}, deliveryConstitution("test", "build"))
	fixture.runner.results = []quality.GateResult{
		{Status: quality.GateStatusPass, ExitCode: 0, OutputSHA256: "one"},
		{Status: quality.GateStatusFail, ExitCode: 1, OutputSHA256: "two"},
	}
	before, err := fixture.svc.store.GetWorkAggregate(context.Background(), "WORK-001")
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.svc.WorkVerify(context.Background(), model.WorkActionRequest{ID: "WORK-001"})
	if err != nil {
		t.Fatal(err)
	}
	cert := result.Certificate
	if cert.Project != before.Contract.Project || cert.WorkID != before.Contract.ID || cert.ContractRevision != before.Contract.ContractRevision || cert.ContractHash != before.Contract.ContractHash || cert.BaseSHA != before.Contract.BaseSHA || cert.HeadSHA == "" || cert.MnemeVersion != "test-version" || cert.StartedAt.IsZero() || cert.FinishedAt.Before(cert.StartedAt) || cert.DurationMs < 0 {
		t.Fatalf("certificate=%#v work=%#v", cert, before.Contract)
	}
	if cert.Verdict != model.DeriveDeliveryVerdict(result.Checks) || cert.Verdict != model.DeliveryVerdictFail {
		t.Fatalf("certificate=%#v checks=%#v", cert, result.Checks)
	}
	if cert.Evidence != "1 pass, 1 fail, 0 not reviewed, 1 skipped" {
		t.Fatalf("evidence=%q checks=%#v", cert.Evidence, result.Checks)
	}
}
