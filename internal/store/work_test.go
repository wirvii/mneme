package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/model"
)

func testWork(id string) *model.WorkContract {
	return &model.WorkContract{ID: id, Project: "p", SourceType: model.WorkSourceOrganic, Status: model.WorkStatusDraft, Goal: "goal", Scope: []string{"internal/**"}, Verification: []model.VerificationKind{model.VerificationAcceptance, model.VerificationBuild}, DevelopmentMethod: model.DevelopmentMethodTDD, MaxCorrectionRounds: 1, CreatedBy: "backend"}
}
func testCriteria() []model.WorkCriterion {
	return []model.WorkCriterion{{Key: "AC1", Declaration: "[[criterion]]\nid = \"AC1\"\nmode = \"manual\"\ndescription = \"one\""}, {Key: "AC2", Declaration: "[[criterion]]\nid = \"AC2\"\nmode = \"manual\"\ndescription = \"two\""}}
}
func testConstraints() []model.WorkConstraint {
	return []model.WorkConstraint{{Key: "C1", Text: "dependency points inward", Source: "memory:x"}}
}
func createTestWork(t *testing.T, s *SDDStore, id string) *model.WorkContract {
	t.Helper()
	w := testWork(id)
	if err := s.CreateWork(context.Background(), w, testCriteria(), testConstraints()); err != nil {
		t.Fatal(err)
	}
	return w
}

func TestNextWorkID_NumericPast1000(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	id, err := s.NextWorkID(ctx, "p")
	if err != nil || id != "WORK-001" {
		t.Fatalf("%q %v", id, err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, v := range []string{"WORK-999", "WORK-1000"} {
		_, err = s.db.ExecContext(ctx, `INSERT INTO execution_contracts(id,project,source_type,status,goal,scope_json,verification_json,development_method,created_at,updated_at) VALUES(?, 'p','organic','draft','g','["x"]','["build"]','standard',?,?)`, v, now, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	id, err = s.NextWorkID(ctx, "p")
	if err != nil || id != "WORK-1001" {
		t.Fatalf("%q %v", id, err)
	}
}

func TestCreateWork_GetAggregate(t *testing.T) {
	s := newTestSDDStore(t)
	w := createTestWork(t, s, "WORK-001")
	if w.UUID == "" {
		t.Fatal("UUID empty")
	}
	got, err := s.GetWorkAggregate(context.Background(), w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Contract.Goal != "goal" || len(got.Criteria) != 2 || got.Criteria[1].Seq != 2 || len(got.Constraints) != 1 || got.Constraints[0].Seq != 1 {
		t.Fatalf("unexpected aggregate %#v", got)
	}
}

func TestCreateWork_AtomicOnSecondCriterionFailure(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	_, err := s.db.Exec(`CREATE TRIGGER abort_second_criterion BEFORE INSERT ON execution_criteria WHEN NEW.seq=2 BEGIN SELECT RAISE(ABORT,'second'); END`)
	if err != nil {
		t.Fatal(err)
	}
	err = s.CreateWork(ctx, testWork("WORK-001"), testCriteria(), testConstraints())
	if err == nil {
		t.Fatal("expected error")
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM execution_contracts WHERE id='WORK-001'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("contracts=%d err=%v", n, err)
	}
}

func TestCreateWork_MissingSpecRollsBack(t *testing.T) {
	s := newTestSDDStore(t)
	w := testWork("WORK-001")
	w.SourceType = model.WorkSourceSpec
	w.SourceID = "SPEC-404"
	if err := s.CreateWork(context.Background(), w, testCriteria(), nil); !errors.Is(err, model.ErrSpecNotFound) {
		t.Fatalf("got %v", err)
	}
	if _, err := s.GetWork(context.Background(), w.ID); !errors.Is(err, model.ErrWorkNotFound) {
		t.Fatalf("work persisted: %v", err)
	}
}

func TestListWorks_TotalLimitAndUnreadable(t *testing.T) {
	s := newTestSDDStore(t)
	createTestWork(t, s, "WORK-001")
	createTestWork(t, s, "WORK-002")
	items, total, unreadable, err := s.ListWorks(context.Background(), "p", "", 1)
	if err != nil || len(items) != 1 || total != 2 || len(unreadable) != 0 {
		t.Fatalf("items=%d total=%d unreadable=%v err=%v", len(items), total, unreadable, err)
	}
	_, err = s.db.Exec(`UPDATE execution_contracts SET scope_json='{' WHERE id='WORK-001'`)
	if err != nil {
		t.Fatal(err)
	}
	items, total, unreadable, err = s.ListWorks(context.Background(), "p", "", 0)
	if err != nil || len(items) != 1 || total != 2 || len(unreadable) != 1 || unreadable[0].ID != "WORK-001" {
		t.Fatalf("items=%d total=%d unreadable=%v err=%v", len(items), total, unreadable, err)
	}
}

func TestLockWork_HashesPersistedAggregate(t *testing.T) {
	s := newTestSDDStore(t)
	createTestWork(t, s, "WORK-001")
	if _, err := s.db.Exec(`UPDATE execution_criteria SET declaration='changed' WHERE work_id='WORK-001' AND seq=1`); err != nil {
		t.Fatal(err)
	}
	if err := s.LockWork(context.Background(), "WORK-001", "base"); err != nil {
		t.Fatal(err)
	}
	agg, err := s.GetWorkAggregate(context.Background(), "WORK-001")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Contract.Status != model.WorkStatusLocked || agg.Contract.ContractRevision != 1 || agg.Contract.BaseSHA != "base" || agg.Contract.LockedAt == nil {
		t.Fatalf("not locked: %#v", agg.Contract)
	}
	if got, want := agg.Contract.ContractHash, model.ContractHash(*agg.Contract, agg.Criteria, agg.Constraints); got != want {
		t.Fatalf("hash=%s want=%s", got, want)
	}
	history := len(agg.History)
	if err := s.LockWork(context.Background(), "WORK-001", "other"); !errors.Is(err, model.ErrInvalidWorkTransition) {
		t.Fatalf("second lock=%v", err)
	}
	after, _ := s.GetWorkAggregate(context.Background(), "WORK-001")
	if len(after.History) != history || after.Contract.BaseSHA != "base" {
		t.Fatal("second lock mutated work")
	}
}

func TestLockWork_RejectsBlankBaseSHAWithoutWriting(t *testing.T) {
	for _, baseSHA := range []string{"", " \t\n "} {
		t.Run("base="+fmt.Sprintf("%q", baseSHA), func(t *testing.T) {
			s := newTestSDDStore(t)
			createTestWork(t, s, "WORK-001")
			before, err := s.GetWorkAggregate(context.Background(), "WORK-001")
			if err != nil {
				t.Fatal(err)
			}

			err = s.LockWork(context.Background(), "WORK-001", baseSHA)
			if !errors.Is(err, model.ErrInvalidContract) {
				t.Fatalf("LockWork() error = %v, want ErrInvalidContract", err)
			}
			after, err := s.GetWorkAggregate(context.Background(), "WORK-001")
			if err != nil {
				t.Fatal(err)
			}
			if after.Contract.Status != before.Contract.Status || after.Contract.BaseSHA != before.Contract.BaseSHA || after.Contract.ContractRevision != before.Contract.ContractRevision || after.Contract.ContractHash != before.Contract.ContractHash || len(after.History) != len(before.History) {
				t.Fatalf("blank base SHA wrote contract or history: before=%#v after=%#v", before, after)
			}
		})
	}
}

func TestGetWorkAggregate_RejectsCorruptChildCreatedAt(t *testing.T) {
	tests := []struct {
		name, table string
	}{
		{name: "criterion", table: "execution_criteria"},
		{name: "constraint", table: "execution_constraints"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestSDDStore(t)
			createTestWork(t, s, "WORK-001")
			if _, err := s.db.Exec(`UPDATE ` + tt.table + ` SET created_at='not-a-time' WHERE work_id='WORK-001'`); err != nil {
				t.Fatal(err)
			}

			_, err := s.GetWorkAggregate(context.Background(), "WORK-001")
			if err == nil {
				t.Fatal("GetWorkAggregate() accepted corrupt child created_at")
			}
			if !strings.Contains(err.Error(), "store: get work aggregate: "+tt.name+" created_at") {
				t.Fatalf("GetWorkAggregate() error = %v, want %q context", err, "store: get work aggregate: "+tt.name+" created_at")
			}
		})
	}
}

func TestTransitionWork_StaleFromIsAtomic(t *testing.T) {
	s := newTestSDDStore(t)
	createTestWork(t, s, "WORK-001")
	if err := s.LockWork(context.Background(), "WORK-001", "base"); err != nil {
		t.Fatal(err)
	}
	before, _ := s.GetWorkAggregate(context.Background(), "WORK-001")
	err := s.TransitionWork(context.Background(), "WORK-001", model.WorkStatusDraft, model.WorkStatusLocked, "backend", "")
	if !errors.Is(err, model.ErrInvalidWorkTransition) {
		t.Fatalf("got %v", err)
	}
	after, _ := s.GetWorkAggregate(context.Background(), "WORK-001")
	if after.Contract.Status != model.WorkStatusLocked || len(after.History) != len(before.History) {
		t.Fatal("stale transition wrote state")
	}
}

func TestTransitionWork_PerWorkCorrectionBudget(t *testing.T) {
	s := newTestSDDStore(t)
	for _, tc := range []struct {
		id      string
		max     int
		wantErr error
	}{{"WORK-001", 1, model.ErrCorrectionBudgetExhausted}, {"WORK-002", 2, nil}} {
		w := testWork(tc.id)
		w.MaxCorrectionRounds = tc.max
		if err := s.CreateWork(context.Background(), w, testCriteria(), nil); err != nil {
			t.Fatal(err)
		}
		if err := s.LockWork(context.Background(), tc.id, "base"); err != nil {
			t.Fatal(err)
		}
		for _, step := range [][2]model.WorkStatus{{model.WorkStatusLocked, model.WorkStatusImplementing}, {model.WorkStatusImplementing, model.WorkStatusVerifying}, {model.WorkStatusVerifying, model.WorkStatusCorrecting}} {
			if err := s.TransitionWork(context.Background(), tc.id, step[0], step[1], "b", ""); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.db.Exec(`UPDATE execution_contracts SET status='verifying' WHERE id=?`, tc.id); err != nil {
			t.Fatal(err)
		}
		err := s.TransitionWork(context.Background(), tc.id, model.WorkStatusVerifying, model.WorkStatusCorrecting, "b", "")
		if !errors.Is(err, tc.wantErr) {
			t.Fatalf("max=%d error=%v want=%v", tc.max, err, tc.wantErr)
		}
	}

	if err := s.TransitionWork(context.Background(), "WORK-001", model.WorkStatusVerifying, model.WorkStatusEscalated, "b", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.TransitionWork(context.Background(), "WORK-001", model.WorkStatusEscalated, model.WorkStatusImplementing, "b", "restart"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetWork(context.Background(), "WORK-001")
	if got.CorrectionRounds != 0 {
		t.Fatal("restart did not reset correction rounds")
	}
}

func TestTransitionWork_BudgetExhaustedWithoutRestart(t *testing.T) {
	s := newTestSDDStore(t)
	w := createTestWork(t, s, "WORK-001")
	_ = w
	if err := s.LockWork(context.Background(), "WORK-001", "base"); err != nil {
		t.Fatal(err)
	}
	steps := [][2]model.WorkStatus{{model.WorkStatusLocked, model.WorkStatusImplementing}, {model.WorkStatusImplementing, model.WorkStatusVerifying}, {model.WorkStatusVerifying, model.WorkStatusCorrecting}, {model.WorkStatusCorrecting, model.WorkStatusTargetedVerifying}, {model.WorkStatusTargetedVerifying, model.WorkStatusEscalated}}
	for _, step := range steps {
		if err := s.TransitionWork(context.Background(), "WORK-001", step[0], step[1], "b", ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`UPDATE execution_contracts SET status='verifying' WHERE id='WORK-001'`); err != nil {
		t.Fatal(err)
	}
	if err := s.TransitionWork(context.Background(), "WORK-001", model.WorkStatusVerifying, model.WorkStatusCorrecting, "b", ""); !errors.Is(err, model.ErrCorrectionBudgetExhausted) {
		t.Fatalf("got %v", err)
	}
}

func TestAmendWork_InvalidatesPreviousCertificate(t *testing.T) {
	s := newTestSDDStore(t)
	createTestWork(t, s, "WORK-001")
	if err := s.LockWork(context.Background(), "WORK-001", "base"); err != nil {
		t.Fatal(err)
	}
	before, _ := s.GetWork(context.Background(), "WORK-001")
	cert := &model.DeliveryCertificate{Verdict: model.DeliveryVerdictPass, ContractRevision: before.ContractRevision, ContractHash: before.ContractHash, HeadSHA: "head"}
	if err := s.AmendWork(context.Background(), model.AmendWorkRequest{WorkID: "WORK-001", Goal: "new", Scope: []string{"new/**"}, Verification: []model.VerificationKind{model.VerificationAcceptance}, DevelopmentMethod: model.DevelopmentMethodTDD, Criteria: testCriteria(), Constraints: testConstraints(), By: "coordinator", Reason: "scope changed"}); err != nil {
		t.Fatal(err)
	}
	after, _ := s.GetWork(context.Background(), "WORK-001")
	if after.ContractRevision != 2 || after.Status != model.WorkStatusImplementing || after.ContractHash == before.ContractHash {
		t.Fatalf("after=%#v", after)
	}
	ok, _ := model.CanComplete(model.CompletionInput{Status: model.WorkStatusVerifying, ContractRevision: after.ContractRevision, ContractHash: after.ContractHash, HeadSHA: "head", Certificate: cert})
	if ok {
		t.Fatal("old certificate completed amended work")
	}
}

func TestTransitionWork_TargetedVerificationCannotCorrect(t *testing.T) {
	s := newTestSDDStore(t)
	workInReview(t, s, "WORK-001", model.WorkStatusTargetedVerifying)
	before, _ := s.GetWorkAggregate(context.Background(), "WORK-001")
	err := s.TransitionWork(context.Background(), "WORK-001", model.WorkStatusTargetedVerifying, model.WorkStatusCorrecting, "backend", "")
	if !errors.Is(err, model.ErrInvalidWorkTransition) {
		t.Fatalf("error=%v", err)
	}
	after, _ := s.GetWorkAggregate(context.Background(), "WORK-001")
	if after.Contract.Status != model.WorkStatusTargetedVerifying || len(after.History) != len(before.History) {
		t.Fatal("forbidden edge changed persisted state")
	}
}

func TestSetRedTestEvidence_RejectsStandardMethod(t *testing.T) {
	s := newTestSDDStore(t)
	w := testWork("WORK-001")
	w.DevelopmentMethod = model.DevelopmentMethodStandard
	if err := s.CreateWork(context.Background(), w, testCriteria(), nil); err != nil {
		t.Fatal(err)
	}
	err := s.SetRedTestEvidence(context.Background(), w.ID, model.RedTestEvidence{TakenAt: time.Now().UTC()})
	if !errors.Is(err, model.ErrEvidenceNotApplicable) {
		t.Fatalf("error=%v", err)
	}
}

func TestSetRedTestEvidence_ExitZeroIsPresent(t *testing.T) {
	s := newTestSDDStore(t)
	createTestWork(t, s, "WORK-001")
	ev := model.RedTestEvidence{Command: []string{"go", "test"}, ExitCode: 0, OutputTail: "red assertion", TakenAt: time.Now().UTC()}
	if err := s.SetRedTestEvidence(context.Background(), "WORK-001", ev); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetWork(context.Background(), "WORK-001")
	if got.DevEvidence == nil || !got.DevEvidence.Present() || got.DevEvidence.ExitCode != 0 {
		t.Fatalf("evidence=%#v", got.DevEvidence)
	}
}

func TestUpdateCriterionResult_LockBoundaryAndRoundTrip(t *testing.T) {
	s := newTestSDDStore(t)
	createTestWork(t, s, "WORK-001")
	agg, _ := s.GetWorkAggregate(context.Background(), "WORK-001")
	err := s.UpdateCriterionResult(context.Background(), agg.Criteria[0].ID, model.CriterionFail, "red", "qa", time.Now().UTC())
	if !errors.Is(err, model.ErrContractNotLocked) {
		t.Fatalf("draft error=%v", err)
	}
	if err := s.LockWork(context.Background(), "WORK-001", "base"); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	if err := s.UpdateCriterionResult(context.Background(), agg.Criteria[0].ID, model.CriterionPass, "green", "qa", at); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetWorkAggregate(context.Background(), "WORK-001")
	if got.Criteria[0].Status != model.CriterionPass || got.Criteria[0].Evidence != "green" || got.Criteria[0].CheckedAt == nil {
		t.Fatalf("criterion=%#v", got.Criteria[0])
	}
}

func TestWorkStore_PreconditionErrors(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	if err := s.LockWork(ctx, "WORK-404", "base"); !errors.Is(err, model.ErrWorkNotFound) {
		t.Errorf("lock missing=%v", err)
	}
	if err := s.TransitionWork(ctx, "WORK-404", model.WorkStatusLocked, model.WorkStatusImplementing, "b", ""); !errors.Is(err, model.ErrWorkNotFound) {
		t.Errorf("transition missing=%v", err)
	}
	createTestWork(t, s, "WORK-001")
	if err := s.LockWork(ctx, "WORK-001", "base"); err != nil {
		t.Fatal(err)
	}
	if err := s.TransitionWork(ctx, "WORK-001", model.WorkStatusLocked, model.WorkStatusAbandoned, "b", ""); !errors.Is(err, model.ErrReasonRequired) {
		t.Errorf("abandon reason=%v", err)
	}
	if err := s.AmendWork(ctx, model.AmendWorkRequest{WorkID: "WORK-001"}); !errors.Is(err, model.ErrReasonRequired) {
		t.Errorf("amend reason=%v", err)
	}
	if _, err := s.GetWork(ctx, "WORK-404"); !errors.Is(err, model.ErrWorkNotFound) {
		t.Errorf("get missing=%v", err)
	}
}

func TestCreateWork_FromExistingSpec(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	spec := &model.Spec{ID: "SPEC-001", Title: "source", Status: model.SpecStatusDraft, Project: "p", Lane: model.LaneStandard}
	if err := s.CreateSpec(ctx, spec); err != nil {
		t.Fatal(err)
	}
	w := testWork("WORK-001")
	w.SourceType = model.WorkSourceSpec
	w.SourceID = spec.ID
	if err := s.CreateWork(ctx, w, testCriteria(), nil); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetWork(ctx, w.ID)
	if got.SourceType != model.WorkSourceSpec || got.SourceID != spec.ID {
		t.Fatalf("work=%#v", got)
	}
}

func TestCreateWork_FromSpecMarksExecutionModelAtomically(t *testing.T) {
	for _, tc := range []struct {
		name     string
		rollback bool
	}{
		{name: "commit"},
		{name: "rollback", rollback: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestSDDStore(t)
			ctx := context.Background()
			spec := &model.Spec{ID: "SPEC-001", Title: "source", Status: model.SpecStatusDraft, Project: "p", Lane: model.LaneStandard}
			if err := s.CreateSpec(ctx, spec); err != nil {
				t.Fatal(err)
			}
			if tc.rollback {
				if _, err := s.db.Exec(`CREATE TRIGGER abort_second_work_criterion BEFORE INSERT ON execution_criteria WHEN NEW.seq=2 BEGIN SELECT RAISE(ABORT,'second'); END`); err != nil {
					t.Fatal(err)
				}
			}
			w := testWork("WORK-001")
			w.SourceType = model.WorkSourceSpec
			w.SourceID = spec.ID
			err := s.CreateWork(ctx, w, testCriteria(), nil)
			if tc.rollback {
				if err == nil {
					t.Fatal("CreateWork succeeded despite abort trigger")
				}
				var contracts int
				if err := s.db.QueryRow(`SELECT COUNT(*) FROM execution_contracts`).Scan(&contracts); err != nil || contracts != 0 {
					t.Fatalf("contracts=%d err=%v", contracts, err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			got, err := s.GetSpec(ctx, spec.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := model.ExecutionModelDeliveryV2
			if tc.rollback {
				want = model.ExecutionModelLegacy
			}
			if got.ExecutionModel != want {
				t.Fatalf("execution model = %q, want %q", got.ExecutionModel, want)
			}
		})
	}
}

func TestLockWorkAndStart_CapturesBothTransitions(t *testing.T) {
	for _, tc := range []struct {
		name     string
		rollback bool
	}{
		{name: "commit"},
		{name: "rollback", rollback: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestSDDStore(t)
			ctx := context.Background()
			createTestWork(t, s, "WORK-001")
			if tc.rollback {
				if _, err := s.db.Exec(`CREATE TRIGGER abort_start_history BEFORE INSERT ON execution_history WHEN NEW.to_status='implementing' BEGIN SELECT RAISE(ABORT,'start'); END`); err != nil {
					t.Fatal(err)
				}
			}
			err := s.LockWorkAndStart(ctx, "WORK-001", "base", "backend")
			if tc.rollback {
				if err == nil {
					t.Fatal("LockWorkAndStart succeeded despite abort trigger")
				}
				agg, getErr := s.GetWorkAggregate(ctx, "WORK-001")
				if getErr != nil {
					t.Fatal(getErr)
				}
				if agg.Contract.Status != model.WorkStatusDraft || agg.Contract.BaseSHA != "" || agg.Contract.ContractHash != "" || agg.Contract.ContractRevision != 0 || agg.Contract.LockedAt != nil || len(agg.History) != 0 {
					t.Fatalf("failed start was not rolled back: %#v history=%#v", agg.Contract, agg.History)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			agg, err := s.GetWorkAggregate(ctx, "WORK-001")
			if err != nil {
				t.Fatal(err)
			}
			if agg.Contract.Status != model.WorkStatusImplementing || agg.Contract.BaseSHA != "base" || agg.Contract.ContractRevision != 1 || agg.Contract.LockedAt == nil {
				t.Fatalf("work not started: %#v", agg.Contract)
			}
			if agg.Contract.ContractHash != model.ContractHash(*agg.Contract, agg.Criteria, agg.Constraints) {
				t.Fatal("contract hash does not match persisted aggregate")
			}
			if len(agg.History) != 2 || agg.History[0].FromStatus != model.WorkStatusDraft || agg.History[0].ToStatus != model.WorkStatusLocked || agg.History[1].FromStatus != model.WorkStatusLocked || agg.History[1].ToStatus != model.WorkStatusImplementing {
				t.Fatalf("history = %#v", agg.History)
			}
			for _, entry := range agg.History {
				if entry.By != "backend" || entry.ContractRevision != 1 {
					t.Fatalf("history actor/revision = %#v", entry)
				}
			}
		})
	}
}

func TestGetWork_RejectsCorruptPersistedFields(t *testing.T) {
	s := newTestSDDStore(t)
	createTestWork(t, s, "WORK-001")
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tests := []struct{ column, bad, restore string }{
		{"verification_json", "{", `["acceptance","build"]`}, {"created_at", "bad", now}, {"updated_at", "bad", now},
		{"locked_at", "bad", ""}, {"completed_at", "bad", ""}, {"dev_evidence_at", "bad", ""},
	}
	for _, tt := range tests {
		if _, err := s.db.Exec(`UPDATE execution_contracts SET `+tt.column+`=? WHERE id='WORK-001'`, tt.bad); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetWork(ctx, "WORK-001"); err == nil {
			t.Errorf("column %s corruption accepted", tt.column)
		}
		if _, err := s.db.Exec(`UPDATE execution_contracts SET `+tt.column+`=? WHERE id='WORK-001'`, tt.restore); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`UPDATE execution_contracts SET dev_evidence_at=?,dev_evidence_command='{' WHERE id='WORK-001'`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetWork(ctx, "WORK-001"); err == nil {
		t.Error("corrupt evidence command accepted")
	}
}

func TestListWorks_StatusFilter(t *testing.T) {
	s := newTestSDDStore(t)
	createTestWork(t, s, "WORK-001")
	createTestWork(t, s, "WORK-002")
	if err := s.LockWork(context.Background(), "WORK-002", "base"); err != nil {
		t.Fatal(err)
	}
	items, total, _, err := s.ListWorks(context.Background(), "p", model.WorkStatusLocked, 0)
	if err != nil || total != 1 || len(items) != 1 || items[0].ID != "WORK-002" {
		t.Fatalf("items=%v total=%d err=%v", items, total, err)
	}
}

func TestWorkStore_AdditionalValidationBranches(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	createTestWork(t, s, "WORK-001")
	agg, _ := s.GetWorkAggregate(ctx, "WORK-001")
	if err := s.UpdateCriterionResult(ctx, agg.Criteria[0].ID, "unknown", "", "", time.Now()); !errors.Is(err, model.ErrInvalidContract) {
		t.Errorf("invalid criterion status=%v", err)
	}
	if err := s.AmendWork(ctx, model.AmendWorkRequest{WorkID: "WORK-001", Reason: "premature"}); !errors.Is(err, model.ErrInvalidWorkTransition) {
		t.Errorf("amend draft=%v", err)
	}
	if err := s.TransitionWork(ctx, "WORK-001", model.WorkStatusDraft, model.WorkStatusDone, "b", ""); !errors.Is(err, model.ErrInvalidWorkTransition) {
		t.Errorf("invalid edge=%v", err)
	}
	if err := s.SetRedTestEvidence(ctx, "WORK-404", model.RedTestEvidence{}); !errors.Is(err, model.ErrWorkNotFound) {
		t.Errorf("evidence missing=%v", err)
	}
	if _, err := s.db.Exec(`UPDATE execution_contracts SET status='done' WHERE id='WORK-001'`); err != nil {
		t.Fatal(err)
	}
	if err := s.AmendWork(ctx, model.AmendWorkRequest{WorkID: "WORK-001", Reason: "reopen"}); !errors.Is(err, model.ErrInvalidWorkTransition) {
		t.Errorf("amend terminal=%v", err)
	}
}

func TestWorkStore_ClosedDatabaseReturnsErrors(t *testing.T) {
	s := newTestSDDStore(t)
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.NextWorkID(context.Background(), "p"); err == nil {
		t.Error("NextWorkID accepted closed database")
	}
	if err := s.CreateWork(context.Background(), testWork("WORK-001"), testCriteria(), nil); err == nil {
		t.Error("CreateWork accepted closed database")
	}
	if _, err := s.GetWork(context.Background(), "WORK-001"); err == nil {
		t.Error("GetWork accepted closed database")
	}
}
