package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/model"
)

func importedWorkAggregate() *model.WorkAggregate {
	created := time.Date(2026, 9, 19, 10, 0, 0, 123, time.UTC)
	updated := created.Add(time.Minute)
	locked := created.Add(2 * time.Minute)
	checked := created.Add(3 * time.Minute)
	resolved := created.Add(4 * time.Minute)
	c := &model.WorkContract{
		ID: "WORK-007", UUID: "01990000-0000-7000-8000-000000000007", Project: "p",
		SourceType: model.WorkSourceSpec, SourceID: "SPEC-001", Status: model.WorkStatusVerifying,
		Goal: "imported goal", Scope: []string{"internal/**"},
		Verification:      []model.VerificationKind{model.VerificationAcceptance, model.VerificationBuild},
		DevelopmentMethod: model.DevelopmentMethodTDD, BaseSHA: "base", ContractRevision: 1,
		CorrectionRounds: 1, MaxCorrectionRounds: 2, CreatedBy: "backend",
		CreatedAt: created, UpdatedAt: updated, LockedAt: &locked,
		DevEvidence: &model.RedTestEvidence{Command: []string{"go", "test"}, ExitCode: 1, OutputTail: "red", CommitSHA: "head", TakenAt: created},
	}
	criteria := []model.WorkCriterion{{ID: "criterion-1", WorkID: c.ID, Seq: 1, Key: "AC1", Declaration: "manual", Status: model.CriterionPass, Evidence: "observed", CheckedBy: "qa", CheckedAt: &checked, CreatedAt: created}}
	constraints := []model.WorkConstraint{{ID: "constraint-1", WorkID: c.ID, Seq: 1, Key: "layers", Text: "inward", Source: "spec", CreatedAt: created}}
	c.ContractHash = model.ContractHash(*c, criteria, constraints)
	return &model.WorkAggregate{Contract: c, Criteria: criteria, Constraints: constraints,
		Findings: []model.WorkFinding{{ID: "finding-1", WorkID: c.ID, Seq: 1, Category: model.FindingRegression, Severity: model.PriorityHigh, Description: "fixed issue", Location: "a.go:1", Evidence: "proof", Origin: model.FindingOriginReview, ReviewPhase: model.ReviewPhaseInitial, Status: model.FindingFixed, ResolutionReason: "fixed", ResolvedBy: "backend", CreatedAt: created, ResolvedAt: &resolved}},
		History:  []model.WorkHistoryEntry{{ID: "history-1", WorkID: c.ID, FromStatus: model.WorkStatusDraft, ToStatus: model.WorkStatusLocked, ContractRevision: 1, By: "backend", Reason: "lock", At: locked}},
	}
}

func createImportSourceSpec(t *testing.T, s *SDDStore) {
	t.Helper()
	spec := &model.Spec{ID: "SPEC-001", Title: "source", Status: model.SpecStatusDraft, Project: "p", Lane: model.LaneStandard}
	if err := s.CreateSpec(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
}

func TestCreateWorkFromRecord_PreservesAggregateAndExcludesCertificates(t *testing.T) {
	s := newTestSDDStore(t)
	createImportSourceSpec(t, s)
	want := importedWorkAggregate()
	if err := s.CreateWorkFromRecord(context.Background(), want); err != nil {
		t.Fatalf("CreateWorkFromRecord: %v", err)
	}
	got, err := s.GetWorkAggregate(context.Background(), want.Contract.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Contract.UUID != want.Contract.UUID || got.Contract.Goal != want.Contract.Goal || got.Contract.CorrectionRounds != 1 || !got.Contract.CreatedAt.Equal(want.Contract.CreatedAt) || len(got.Criteria) != 1 || got.Criteria[0].ID != "criterion-1" || len(got.Constraints) != 1 || len(got.Findings) != 1 || got.Findings[0].Status != model.FindingFixed || len(got.History) != 1 || got.History[0].ID != "history-1" {
		t.Fatalf("aggregate not preserved: %#v", got)
	}
	spec, err := s.GetSpec(context.Background(), "SPEC-001")
	if err != nil || spec.ExecutionModel != model.ExecutionModelDeliveryV2 {
		t.Fatalf("source execution model = %q, err=%v", spec.ExecutionModel, err)
	}
	for _, table := range []string{"delivery_certificates", "delivery_checks"} {
		var count int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
}

func TestCreateWorkFromRecord_RollsBackInvalidAggregate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*model.WorkAggregate)
		want   error
	}{
		{name: "missing source spec", mutate: func(a *model.WorkAggregate) {}, want: model.ErrSpecNotFound},
		{name: "invalid second child", mutate: func(a *model.WorkAggregate) {
			a.Contract.SourceType, a.Contract.SourceID = model.WorkSourceOrganic, ""
			a.Criteria = append(a.Criteria, a.Criteria[0])
			a.Criteria[1].ID, a.Criteria[1].Seq, a.Criteria[1].Key = "criterion-2", 1, "AC2"
		}, want: model.ErrInvalidContract},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestSDDStore(t)
			agg := importedWorkAggregate()
			tt.mutate(agg)
			err := s.CreateWorkFromRecord(context.Background(), agg)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error=%v, want %v", err, tt.want)
			}
			var count int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM execution_contracts`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("contracts=%d err=%v", count, err)
			}
		})
	}
}

func TestUpdateWorkFromRecord_ReplacesDefinitionsAndMergesAudit(t *testing.T) {
	s := newTestSDDStore(t)
	createImportSourceSpec(t, s)
	original := importedWorkAggregate()
	if err := s.CreateWorkFromRecord(context.Background(), original); err != nil {
		t.Fatal(err)
	}
	created := original.Contract.CreatedAt.Add(10 * time.Minute)
	if _, err := s.db.Exec(`INSERT INTO execution_findings(id,work_id,seq,category,severity,description,evidence,origin,review_phase,status,created_at) VALUES('local-finding','WORK-007',2,'discovery','low','local','fact','human','targeted','open',?)`, formatTime(created)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO execution_history(id,work_id,from_status,to_status,contract_revision,by,reason,at) VALUES('local-history','WORK-007','locked','implementing',1,'local','local',?)`, formatTime(created)); err != nil {
		t.Fatal(err)
	}

	incoming := importedWorkAggregate()
	incoming.Contract.Goal = "changed goal"
	incoming.Criteria = []model.WorkCriterion{{ID: "criterion-new", WorkID: "WORK-007", Seq: 1, Key: "AC2", Declaration: "new", Status: model.CriterionPending, CreatedAt: created}}
	incoming.Constraints = nil
	incoming.Contract.ContractHash = model.ContractHash(*incoming.Contract, incoming.Criteria, incoming.Constraints)
	incoming.Findings[0].ResolutionReason = "updated resolution"
	incoming.History = append(incoming.History, model.WorkHistoryEntry{ID: "history-2", WorkID: "WORK-007", FromStatus: model.WorkStatusImplementing, ToStatus: model.WorkStatusVerifying, ContractRevision: 1, By: "qa", At: created.Add(time.Second)})

	for run := 0; run < 2; run++ {
		if err := s.UpdateWorkFromRecord(context.Background(), incoming); err != nil {
			t.Fatalf("UpdateWorkFromRecord run %d: %v", run+1, err)
		}
	}
	got, err := s.GetWorkAggregate(context.Background(), "WORK-007")
	if err != nil {
		t.Fatal(err)
	}
	if got.Contract.Goal != "changed goal" || len(got.Criteria) != 1 || got.Criteria[0].ID != "criterion-new" || len(got.Constraints) != 0 {
		t.Fatalf("definitions not replaced: %#v", got)
	}
	if len(got.Findings) != 2 || got.Findings[0].ResolutionReason != "updated resolution" {
		t.Fatalf("findings not merged: %#v", got.Findings)
	}
	if len(got.History) != 3 {
		t.Fatalf("history count=%d, want 3", len(got.History))
	}
	if got.Contract.ContractHash != model.ContractHash(*got.Contract, got.Criteria, got.Constraints) {
		t.Fatal("contract hash does not match replaced definitions")
	}
}

func TestRefsForUUIDs_ResolvesWorkAnchors(t *testing.T) {
	s := newTestSDDStore(t)
	createImportSourceSpec(t, s)
	agg := importedWorkAggregate()
	if err := s.CreateWorkFromRecord(context.Background(), agg); err != nil {
		t.Fatal(err)
	}
	got, err := s.RefsForUUIDs(context.Background(), []string{agg.Contract.UUID, "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	if got[agg.Contract.UUID] != "WORK-007" {
		t.Fatalf("RefsForUUIDs = %v", got)
	}
	if _, exists := got["unknown"]; exists {
		t.Fatalf("unknown anchor resolved: %v", got)
	}
}
