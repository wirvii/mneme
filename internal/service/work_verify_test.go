package service

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/quality"
)

type deliveryRunnerStub struct {
	calls int
}

func (r *deliveryRunnerStub) Run(_ context.Context, gate quality.Gate, _ string) quality.GateResult {
	r.calls++
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
