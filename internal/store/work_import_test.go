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
	wantUUID := want.Contract.UUID
	if err := s.CreateWorkFromRecord(context.Background(), want); err != nil {
		t.Fatalf("CreateWorkFromRecord: %v", err)
	}
	got, err := s.GetWorkAggregate(context.Background(), want.Contract.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Contract.UUID != wantUUID || got.Contract.Goal != want.Contract.Goal || got.Contract.CorrectionRounds != 1 || !got.Contract.CreatedAt.Equal(want.Contract.CreatedAt) || len(got.Criteria) != 1 || got.Criteria[0].ID != "criterion-1" || len(got.Constraints) != 1 || len(got.Findings) != 1 || got.Findings[0].Status != model.FindingFixed || len(got.History) != 1 || got.History[0].ID != "history-1" {
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

func TestCreateWorkFromRecord_CompletesOnlySafeMetadata(t *testing.T) {
	s := newTestSDDStore(t)
	agg := importedWorkAggregate()
	agg.Contract.SourceType, agg.Contract.SourceID = model.WorkSourceOrganic, ""
	agg.Contract.Status = model.WorkStatusDraft
	agg.Contract.UUID, agg.Contract.BaseSHA, agg.Contract.ContractHash = "", "", ""
	agg.Contract.ContractRevision = 0
	agg.Contract.CreatedAt, agg.Contract.UpdatedAt, agg.Contract.LockedAt = time.Time{}, time.Time{}, nil
	agg.Criteria[0].ID, agg.Criteria[0].CreatedAt = "", time.Time{}
	agg.Findings, agg.History = nil, nil
	if err := s.CreateWorkFromRecord(context.Background(), agg); err != nil {
		t.Fatalf("CreateWorkFromRecord: %v", err)
	}
	if agg.Contract.UUID == "" || agg.Contract.CreatedAt.IsZero() || agg.Contract.UpdatedAt.IsZero() || agg.Criteria[0].ID == "" || agg.Criteria[0].CreatedAt.IsZero() {
		t.Fatalf("safe metadata was not completed: %+v criterion=%+v", agg.Contract, agg.Criteria[0])
	}
}

func TestValidateWorkFromRecord_RejectsEveryChildFamily(t *testing.T) {
	tests := []struct {
		name   string
		build  func() *model.WorkAggregate
		mutate func(*model.WorkAggregate)
	}{
		{"nil aggregate", func() *model.WorkAggregate { return nil }, func(*model.WorkAggregate) {}},
		{"nil contract", func() *model.WorkAggregate { return &model.WorkAggregate{} }, func(*model.WorkAggregate) {}},
		{"missing project", importedWorkAggregate, func(a *model.WorkAggregate) { a.Contract.Project = "" }},
		{"red evidence timestamp", importedWorkAggregate, func(a *model.WorkAggregate) { a.Contract.DevEvidence.TakenAt = time.Time{} }},
		{"criterion work id", importedWorkAggregate, func(a *model.WorkAggregate) { a.Criteria[0].WorkID = "WORK-other" }},
		{"constraint sequence", importedWorkAggregate, func(a *model.WorkAggregate) { a.Constraints[0].Seq = 0 }},
		{"finding vocabulary", importedWorkAggregate, func(a *model.WorkAggregate) { a.Findings[0].Category = model.FindingCategory("unknown") }},
		{"history transition", importedWorkAggregate, func(a *model.WorkAggregate) { a.History[0].ToStatus = model.WorkStatusDone }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			aggregate := tc.build()
			tc.mutate(aggregate)
			if err := newTestSDDStore(t).ValidateWorkFromRecord(aggregate); !errors.Is(err, model.ErrInvalidContract) {
				t.Fatalf("error = %v, want ErrInvalidContract", err)
			}
		})
	}
}

func TestCompleteImportedWorkMetadata_CoversAllChildFamilies(t *testing.T) {
	aggregate := importedWorkAggregate()
	aggregate.Contract.UUID = ""
	aggregate.Contract.CreatedAt, aggregate.Contract.UpdatedAt = time.Time{}, time.Time{}
	aggregate.Criteria[0].ID, aggregate.Criteria[0].CreatedAt = "", time.Time{}
	aggregate.Constraints[0].ID, aggregate.Constraints[0].CreatedAt = "", time.Time{}
	aggregate.Findings[0].ID, aggregate.Findings[0].CreatedAt = "", time.Time{}
	aggregate.History[0].ID, aggregate.History[0].At = "", time.Time{}
	if err := completeImportedWorkMetadata(aggregate); err != nil {
		t.Fatal(err)
	}
	if aggregate.Contract.UUID == "" || aggregate.Contract.CreatedAt.IsZero() || aggregate.Contract.UpdatedAt.IsZero() ||
		aggregate.Criteria[0].ID == "" || aggregate.Criteria[0].CreatedAt.IsZero() ||
		aggregate.Constraints[0].ID == "" || aggregate.Constraints[0].CreatedAt.IsZero() ||
		aggregate.Findings[0].ID == "" || aggregate.Findings[0].CreatedAt.IsZero() ||
		aggregate.History[0].ID == "" || aggregate.History[0].At.IsZero() {
		t.Fatalf("metadata not completed: %+v", aggregate)
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

func TestCreateWorkFromRecord_RollsBackStorageFailures(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, *SDDStore)
	}{
		{name: "source marker", setup: func(t *testing.T, s *SDDStore) {
			if _, err := s.db.Exec(`DROP TABLE specs`); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "contract", setup: func(t *testing.T, s *SDDStore) {
			if _, err := s.db.Exec(`CREATE TRIGGER fail_contract BEFORE INSERT ON execution_contracts BEGIN SELECT RAISE(FAIL, 'contract'); END`); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "definitions", setup: func(t *testing.T, s *SDDStore) {
			if _, err := s.db.Exec(`CREATE TRIGGER fail_definition BEFORE INSERT ON execution_criteria BEGIN SELECT RAISE(FAIL, 'definition'); END`); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "audit", setup: func(t *testing.T, s *SDDStore) {
			if _, err := s.db.Exec(`CREATE TRIGGER fail_audit BEFORE INSERT ON execution_findings BEGIN SELECT RAISE(FAIL, 'audit'); END`); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestSDDStore(t)
			createImportSourceSpec(t, s)
			tc.setup(t, s)
			if err := s.CreateWorkFromRecord(context.Background(), importedWorkAggregate()); err == nil {
				t.Fatal("CreateWorkFromRecord succeeded, want storage error")
			}
			var count int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM execution_contracts`).Scan(&count); err == nil && count != 0 {
				t.Fatalf("contracts=%d, want rollback", count)
			}
		})
	}
}

func TestWorkFromRecord_ReportsClosedDatabase(t *testing.T) {
	s := newTestSDDStore(t)
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateWorkFromRecord(context.Background(), importedWorkAggregate()); err == nil {
		t.Fatal("CreateWorkFromRecord succeeded on closed database")
	}
	if err := s.UpdateWorkFromRecord(context.Background(), importedWorkAggregate()); err == nil {
		t.Fatal("UpdateWorkFromRecord succeeded on closed database")
	}
}

func TestUpdateWorkFromRecord_RollsBackStorageFailures(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, *SDDStore)
	}{
		{name: "source marker", setup: func(t *testing.T, s *SDDStore) {
			if _, err := s.db.Exec(`DROP TABLE specs`); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "missing contract", setup: func(t *testing.T, s *SDDStore) {
			if _, err := s.db.Exec(`DELETE FROM execution_contracts`); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "delete criteria", setup: func(t *testing.T, s *SDDStore) {
			if _, err := s.db.Exec(`CREATE TRIGGER fail_delete_criteria BEFORE DELETE ON execution_criteria BEGIN SELECT RAISE(FAIL, 'criteria'); END`); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "delete constraints", setup: func(t *testing.T, s *SDDStore) {
			if _, err := s.db.Exec(`CREATE TRIGGER fail_delete_constraints BEFORE DELETE ON execution_constraints BEGIN SELECT RAISE(FAIL, 'constraints'); END`); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "definitions", setup: func(t *testing.T, s *SDDStore) {
			if _, err := s.db.Exec(`CREATE TRIGGER fail_update_definition BEFORE INSERT ON execution_criteria BEGIN SELECT RAISE(FAIL, 'definition'); END`); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "audit", setup: func(t *testing.T, s *SDDStore) {
			if _, err := s.db.Exec(`CREATE TRIGGER fail_update_audit BEFORE INSERT ON execution_findings BEGIN SELECT RAISE(FAIL, 'audit'); END`); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestSDDStore(t)
			createImportSourceSpec(t, s)
			original := importedWorkAggregate()
			if err := s.CreateWorkFromRecord(context.Background(), original); err != nil {
				t.Fatal(err)
			}
			tc.setup(t, s)
			incoming := importedWorkAggregate()
			incoming.Contract.Goal = "must roll back"
			incoming.Contract.ContractHash = model.ContractHash(*incoming.Contract, incoming.Criteria, incoming.Constraints)
			if err := s.UpdateWorkFromRecord(context.Background(), incoming); err == nil {
				t.Fatal("UpdateWorkFromRecord succeeded, want storage error")
			}
			got, err := s.GetWorkAggregate(context.Background(), original.Contract.ID)
			if err == nil && got.Contract.Goal != original.Contract.Goal {
				t.Fatalf("goal=%q, want rollback to %q", got.Contract.Goal, original.Contract.Goal)
			}
		})
	}
}

func TestImportedContractValues_CoversOptionalFields(t *testing.T) {
	aggregate := importedWorkAggregate()
	aggregate.Contract.DevEvidence = nil
	completed := aggregate.Contract.UpdatedAt.Add(time.Hour)
	aggregate.Contract.CompletedAt = &completed
	values, err := importedContractValues(aggregate.Contract)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 25 || values[15] != "" || values[24] == "" {
		t.Fatalf("unexpected values: %#v", values)
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
