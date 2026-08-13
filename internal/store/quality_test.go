package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/model"
)

// newTestCertificate builds a minimal valid certificate for InsertCertificate,
// with a fixed StartedAt/FinishedAt so DurationMs is deterministic.
func newTestCertificate(project, specID string) *model.QualityCertificate {
	now := time.Now().UTC()
	return &model.QualityCertificate{
		Project:          project,
		SpecID:           specID,
		HeadSHA:          "abc123",
		BaseSHA:          "base123",
		ConstitutionHash: "hash123",
		SchemaVersion:    1,
		Verdict:          model.QualityVerdictPass,
		Dirty:            false,
		MnemeVersion:     "v1.0.0-test",
		StartedAt:        now,
		FinishedAt:       now.Add(time.Second),
		DurationMs:       1000,
	}
}

// TestInsertCertificate_AndListChecks covers AC6: insert a certificate with
// checks against real SQLite in memory, then read both back.
func TestInsertCertificate_AndListChecks(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	cert := newTestCertificate("proj", "SPEC-115")
	checks := []*model.QualityCheck{
		{Kind: "tree", Name: "clean-worktree", Status: "pass"},
		{Kind: "gate", Name: "build", Status: "pass", ExitCode: 0, OutputSHA256: "deadbeef"},
	}

	if err := s.InsertCertificate(ctx, cert, checks); err != nil {
		t.Fatalf("InsertCertificate: %v", err)
	}
	if cert.ID == "" {
		t.Fatal("InsertCertificate did not assign an ID")
	}

	got, err := s.ListChecks(ctx, cert.ID)
	if err != nil {
		t.Fatalf("ListChecks: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListChecks returned %d rows, want 2", len(got))
	}
	if got[0].Seq != 1 || got[0].Name != "clean-worktree" {
		t.Errorf("checks[0] = %+v, want seq=1 name=clean-worktree", got[0])
	}
	if got[1].Seq != 2 || got[1].Name != "build" {
		t.Errorf("checks[1] = %+v, want seq=2 name=build", got[1])
	}
}

// TestGetLatestCertificate_ReturnsMostRecent covers the ordering GetLatestCertificate
// promises: the most recently inserted certificate for (project, spec) wins.
func TestGetLatestCertificate_ReturnsMostRecent(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	first := newTestCertificate("proj", "SPEC-115")
	first.HeadSHA = "sha-first"
	if err := s.InsertCertificate(ctx, first, nil); err != nil {
		t.Fatalf("InsertCertificate (first): %v", err)
	}

	// created_at has second-level string granularity in some SQLite time
	// formats; sleep briefly so ordering by created_at is unambiguous even
	// on a fast machine.
	time.Sleep(2 * time.Millisecond)

	second := newTestCertificate("proj", "SPEC-115")
	second.HeadSHA = "sha-second"
	if err := s.InsertCertificate(ctx, second, nil); err != nil {
		t.Fatalf("InsertCertificate (second): %v", err)
	}

	got, err := s.GetLatestCertificate(ctx, "proj", "SPEC-115")
	if err != nil {
		t.Fatalf("GetLatestCertificate: %v", err)
	}
	if got.HeadSHA != "sha-second" {
		t.Errorf("GetLatestCertificate returned head_sha=%q, want sha-second", got.HeadSHA)
	}
}

// TestGetLatestCertificate_NotFound covers the absence sentinel.
func TestGetLatestCertificate_NotFound(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	_, err := s.GetLatestCertificate(ctx, "proj", "SPEC-999")
	if !errors.Is(err, model.ErrCertificateNotFound) {
		t.Errorf("GetLatestCertificate error = %v, want ErrCertificateNotFound", err)
	}
}

// TestGetCertificate covers direct lookup by ID, and its not-found sentinel.
func TestGetCertificate(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	cert := newTestCertificate("proj", "SPEC-115")
	if err := s.InsertCertificate(ctx, cert, nil); err != nil {
		t.Fatalf("InsertCertificate: %v", err)
	}

	got, err := s.GetCertificate(ctx, cert.ID)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if got.HeadSHA != cert.HeadSHA {
		t.Errorf("GetCertificate head_sha = %q, want %q", got.HeadSHA, cert.HeadSHA)
	}

	_, err = s.GetCertificate(ctx, "does-not-exist")
	if !errors.Is(err, model.ErrCertificateNotFound) {
		t.Errorf("GetCertificate(missing) error = %v, want ErrCertificateNotFound", err)
	}
}

// TestAckCheck_RecalculatesVerdictToPass is the AC6-mandated test: a
// "findings" certificate with a SINGLE finding becomes "pass" once that
// finding is acked.
func TestAckCheck_RecalculatesVerdictToPass(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	cert := newTestCertificate("proj", "SPEC-115")
	cert.Verdict = model.QualityVerdictFindings
	checks := []*model.QualityCheck{
		{Kind: "constitution", Name: "tracked", Status: "finding", Summary: "not tracked"},
	}
	if err := s.InsertCertificate(ctx, cert, checks); err != nil {
		t.Fatalf("InsertCertificate: %v", err)
	}

	if err := s.AckCheck(ctx, cert.ID, 1, "orchestrator", "reviewed, acceptable for this repo"); err != nil {
		t.Fatalf("AckCheck: %v", err)
	}

	gotCheck, err := s.ListChecks(ctx, cert.ID)
	if err != nil {
		t.Fatalf("ListChecks: %v", err)
	}
	if gotCheck[0].Status != "acked" {
		t.Errorf("check status = %q, want acked", gotCheck[0].Status)
	}
	if gotCheck[0].AckedBy != "orchestrator" {
		t.Errorf("check acked_by = %q, want orchestrator", gotCheck[0].AckedBy)
	}
	if gotCheck[0].AckedAt == nil {
		t.Error("check acked_at is nil, want a timestamp")
	}

	gotCert, err := s.GetCertificate(ctx, cert.ID)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if gotCert.Verdict != model.QualityVerdictPass {
		t.Errorf("certificate verdict = %q, want pass after acking its only finding", gotCert.Verdict)
	}
}

// TestAckCheck_NotFound covers acking a non-existent (certificate, seq) pair.
func TestAckCheck_NotFound(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	cert := newTestCertificate("proj", "SPEC-115")
	if err := s.InsertCertificate(ctx, cert, []*model.QualityCheck{{Kind: "gate", Name: "build", Status: "pass"}}); err != nil {
		t.Fatalf("InsertCertificate: %v", err)
	}

	err := s.AckCheck(ctx, cert.ID, 99, "orchestrator", "n/a")
	if !errors.Is(err, model.ErrCertificateNotFound) {
		t.Errorf("AckCheck(missing seq) error = %v, want ErrCertificateNotFound", err)
	}

	// seq 1 exists but is "pass", not "finding" — acking it must also fail.
	err = s.AckCheck(ctx, cert.ID, 1, "orchestrator", "n/a")
	if !errors.Is(err, model.ErrCertificateNotFound) {
		t.Errorf("AckCheck(non-finding seq) error = %v, want ErrCertificateNotFound", err)
	}
}

// TestAckCheck_VerdictStaysFailWhenAnotherCheckFails verifies acking one
// finding does not resurrect a verdict to pass when a DIFFERENT check on the
// same certificate is still "fail".
func TestAckCheck_VerdictStaysFailWhenAnotherCheckFails(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()

	cert := newTestCertificate("proj", "SPEC-115")
	cert.Verdict = model.QualityVerdictFail
	checks := []*model.QualityCheck{
		{Kind: "gate", Name: "build", Status: "fail"},
		{Kind: "constitution", Name: "tracked", Status: "finding"},
	}
	if err := s.InsertCertificate(ctx, cert, checks); err != nil {
		t.Fatalf("InsertCertificate: %v", err)
	}

	if err := s.AckCheck(ctx, cert.ID, 2, "orchestrator", "acceptable"); err != nil {
		t.Fatalf("AckCheck: %v", err)
	}

	gotCert, err := s.GetCertificate(ctx, cert.ID)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if gotCert.Verdict != model.QualityVerdictFail {
		t.Errorf("certificate verdict = %q, want fail (a different check still fails)", gotCert.Verdict)
	}
}
