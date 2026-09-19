package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/quality"
	"github.com/wirvii/mneme/internal/store"
)

func TestWorkVerifyWiringProvidesProductionRunner(t *testing.T) {
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "base")

	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalWD) })
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".mneme"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".mneme", "config.toml"), []byte("[workflow]\nengine = \"delivery_v2\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	oldDataDir, oldProject := flagDataDir, flagProject
	flagDataDir, flagProject = dataDir, "p"
	t.Cleanup(func() { flagDataDir, flagProject = oldDataDir, oldProject })

	svc, cleanup, err := initSDDService()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	criterion := model.WorkCriterionInput{Key: "file", Declaration: `[[criterion]]
id = "file"
mode = "assert"
text = "tracked file exists"
  [[criterion.assert]]
  verb = "file_exists"
  path = "tracked.txt"
  new = false`}
	work, err := svc.WorkBegin(context.Background(), model.WorkBeginRequest{Goal: "wiring", Scope: []string{"**"}, Verification: []model.VerificationKind{model.VerificationAcceptance}, Criteria: []model.WorkCriterionInput{criterion}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.WorkLock(context.Background(), model.WorkLockRequest{ID: work.Contract.ID, By: "test"}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Storage.DataDir = dataDir
	database, err := db.Open(cfg.ProjectDBPath("p"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := store.NewSDDStore(database).TransitionWork(context.Background(), work.Contract.ID, model.WorkStatusImplementing, model.WorkStatusVerifying, "test", ""); err != nil {
		t.Fatal(err)
	}
	result, err := svc.WorkVerify(context.Background(), model.WorkActionRequest{ID: work.Contract.ID})
	if err != nil {
		t.Fatal(err)
	}
	if result.Certificate == nil || len(result.Checks) == 0 {
		t.Fatalf("result=%#v", result)
	}

	reviewWork, err := svc.WorkBegin(context.Background(), model.WorkBeginRequest{
		Goal: "review wiring", Scope: []string{"**"}, Verification: []model.VerificationKind{model.VerificationAcceptance},
		Criteria: []model.WorkCriterionInput{criterion}, Constraints: []model.WorkConstraintInput{{Key: "layers", Text: "dependencies point inward"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.WorkLock(context.Background(), model.WorkLockRequest{ID: reviewWork.Contract.ID, By: "test"}); err != nil {
		t.Fatal(err)
	}
	head, err := (&quality.Git{RepoDir: svc.RepoDir()}).HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	review, err := svc.WorkReview(context.Background(), model.WorkReviewRequest{
		ID: reviewWork.Contract.ID, By: "qa-tester", HeadSHA: head,
		ArchitectureVerdicts: []model.WorkArchitectureVerdictInput{{ConstraintKey: "layers", Status: model.DeliveryCheckPass, EvidenceKind: model.ReviewEvidenceFile, Evidence: "tracked.txt"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if review.Certificate == nil || review.Certificate.HeadSHA != head || review.Certificate.MnemeVersion == "" || review.ReviewPhase != model.ReviewPhaseInitial || review.Work.Contract.Status != review.NextStatus || review.NextStatus != model.WorkStatusCorrecting || review.CorrectionMandate == nil {
		t.Fatalf("review=%#v", review)
	}
}
