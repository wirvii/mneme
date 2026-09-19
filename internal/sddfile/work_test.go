package sddfile

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/model"
)

func completeWorkRecord() *WorkRecord {
	created := time.Date(2026, 9, 19, 12, 0, 0, 123456789, time.UTC)
	updated := created.Add(time.Minute)
	locked := created.Add(2 * time.Minute)
	completed := created.Add(3 * time.Minute)
	checked := created.Add(4 * time.Minute)
	resolved := created.Add(5 * time.Minute)
	contract := &model.WorkContract{
		ID: "WORK-007", UUID: "01990000-0000-7000-8000-000000000007", Project: "wirvii/mneme",
		SourceType: model.WorkSourceSpec, SourceID: "SPEC-149", Status: model.WorkStatusDone,
		Goal: "goal\n<!-- mneme:finding -->\n", Scope: []string{"internal/a.go", "internal/b.go"},
		Verification:      []model.VerificationKind{model.VerificationAcceptance, model.VerificationAffectedTests, model.VerificationBuild, model.VerificationLint},
		DevelopmentMethod: model.DevelopmentMethodTDD, BaseSHA: "base", ContractRevision: 2,
		CorrectionRounds: 1, MaxCorrectionRounds: 2, CreatedBy: "backend",
		CreatedAt: created, UpdatedAt: updated, LockedAt: &locked, CompletedAt: &completed,
		DevEvidence: &model.RedTestEvidence{Command: []string{"go", "test", "./..."}, ExitCode: 1, OutputTail: "red\n", CommitSHA: "deadbeef", TakenAt: created.Add(time.Second)},
	}
	criteria := []model.WorkCriterion{
		{ID: "criterion-a", WorkID: contract.ID, Seq: 1, Key: "a", Declaration: "file_exists\n", Status: model.CriterionPass, Evidence: "present\n", CheckedBy: "qa", CheckedAt: &checked, CreatedAt: created},
		{ID: "criterion-b", WorkID: contract.ID, Seq: 2, Key: "b", Declaration: "pattern_count", Status: model.CriterionPending, CreatedAt: updated},
	}
	constraints := []model.WorkConstraint{
		{ID: "constraint-a", WorkID: contract.ID, Seq: 1, Key: "layers", Text: "service only\n", Source: "SPEC-149", CreatedAt: created},
		{ID: "constraint-b", WorkID: contract.ID, Seq: 2, Key: "pure", Text: "no cgo", CreatedAt: updated},
	}
	contract.ContractHash = model.ContractHash(*contract, criteria, constraints)
	return &WorkRecord{Aggregate: &model.WorkAggregate{
		Contract: contract, Criteria: criteria, Constraints: constraints,
		Findings: []model.WorkFinding{
			{ID: "finding-a", WorkID: contract.ID, Seq: 1, Category: model.FindingRegression, Severity: model.PriorityHigh, Description: "broken\n<!-- mneme:work-history -->\n", Location: "a.go:1", Evidence: "trace\n", Origin: model.FindingOriginReview, ReviewPhase: model.ReviewPhaseInitial, Status: model.FindingFixed, ResolutionReason: "fixed\n", ResolvedBy: "backend", CreatedAt: created, ResolvedAt: &resolved},
			{ID: "finding-b", WorkID: contract.ID, Seq: 2, Category: model.FindingDiscovery, Severity: model.PriorityLow, Description: "note", Evidence: "fact", Origin: model.FindingOriginHuman, ReviewPhase: model.ReviewPhaseTargeted, Status: model.FindingBacklogged, BacklogID: "BL-999", ResolutionReason: "later", ResolvedBy: "owner", CreatedAt: updated, ResolvedAt: &resolved},
		},
		History: []model.WorkHistoryEntry{
			{ID: "history-a", WorkID: contract.ID, FromStatus: model.WorkStatusDraft, ToStatus: model.WorkStatusLocked, ContractRevision: 1, By: "backend", Reason: "lock\n", At: created},
			{ID: "history-b", WorkID: contract.ID, FromStatus: model.WorkStatusLocked, ToStatus: model.WorkStatusImplementing, ContractRevision: 1, By: "backend", Reason: "start", At: updated},
			{ID: "history-c", WorkID: contract.ID, FromStatus: model.WorkStatusVerifying, ToStatus: model.WorkStatusDone, ContractRevision: 2, By: "owner", Reason: "complete", At: completed},
		},
	}}
}

func TestWorkRecord_RoundTripComplete(t *testing.T) {
	want := completeWorkRecord()
	data, err := MarshalWork(want)
	if err != nil {
		t.Fatalf("MarshalWork: %v", err)
	}
	got, err := UnmarshalWork(data)
	if err != nil {
		t.Fatalf("UnmarshalWork: %v", err)
	}
	if !equalWorkRecord(want, got) {
		t.Fatal("complete work record changed during round trip")
	}
	if strings.Contains(string(data), "certificate") || strings.Contains(string(data), "delivery_check") {
		t.Fatalf("work record transported delivery evidence:\n%s", data)
	}
}

func TestWorkRecord_RejectsConflictAndNewerSchema(t *testing.T) {
	rec := completeWorkRecord()
	data, err := MarshalWork(rec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UnmarshalWork(append(data, []byte("<<<<<<< ours\n=======\n>>>>>>> theirs\n")...)); !errors.Is(err, ErrConflictMarkers) {
		t.Fatalf("conflict error = %v, want ErrConflictMarkers", err)
	}
	newer := strings.Replace(string(data), "schema: 1", "schema: 2", 1)
	if _, err := UnmarshalWork([]byte(newer)); !errors.Is(err, ErrSchemaOutOfRange) {
		t.Fatalf("schema error = %v, want ErrSchemaOutOfRange", err)
	}
}

func TestWorkRecord_ReadRecordNormalizesCRLF(t *testing.T) {
	data, err := MarshalWork(completeWorkRecord())
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/work.md"
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(data), "\n", "\r\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	read, err := ReadRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UnmarshalWork(read); err != nil {
		t.Fatalf("UnmarshalWork after CRLF normalization: %v", err)
	}
}

func TestWorkRecord_NilRejected(t *testing.T) {
	if _, err := MarshalWork(nil); err == nil {
		t.Fatal("MarshalWork(nil) succeeded")
	}
	if _, err := MarshalWork(&WorkRecord{}); err == nil {
		t.Fatal("MarshalWork without aggregate succeeded")
	}
}

func TestWorkRecord_RejectsUnknownMarkerKindAndMalformedStructure(t *testing.T) {
	data, err := MarshalWork(completeWorkRecord())
	if err != nil {
		t.Fatal(err)
	}
	unknown := append(append([]byte(nil), data...), []byte("<!-- mneme:future-section -->\nvalue\n")...)
	if _, err := UnmarshalWork(unknown); err == nil || !strings.Contains(err.Error(), "unexpected section kind") {
		t.Fatalf("unknown marker error = %v", err)
	}
	wrongKind := strings.Replace(string(data), "kind: work", "kind: spec", 1)
	if _, err := UnmarshalWork([]byte(wrongKind)); err == nil || !strings.Contains(err.Error(), "is not work") {
		t.Fatalf("wrong kind error = %v", err)
	}
	badTime := strings.Replace(string(data), "created_at: 2026-09-19T12:00:00.123456789Z", "created_at: yesterday", 1)
	if _, err := UnmarshalWork([]byte(badTime)); err == nil || !strings.Contains(err.Error(), "invalid created_at") {
		t.Fatalf("invalid time error = %v", err)
	}
}
