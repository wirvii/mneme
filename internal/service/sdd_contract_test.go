// Package service — tests for sdd_contract.go's two guardians (SPEC-156
// P2). Table-driven, no mocks, a real in-memory SQLite store throughout —
// this package's own established convention.
package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/store"
)

// writeSpecCriteria writes a minimal, valid criteria.toml for spec directly
// to svc's real workflow directory, using the SAME specDocPath function
// production reads from (V5 of the design: reader and writer never
// resolve different paths). Every declared id is a mode="manual" criterion
// — the cheapest shape ParseCriteria accepts, since these fixtures only
// need their ids to exist, never to be evaluated. HOME is isolated by
// internal/testenv (see TestMain in sdd_test.go), so this never touches a
// real user directory.
func writeSpecCriteria(t *testing.T, svc *SDDService, spec *model.Spec, ids ...string) {
	t.Helper()
	if svc.config == nil {
		t.Fatal("writeSpecCriteria: svc.config is nil")
	}
	path, err := specDocPath(svc.config.WorkflowDir(), spec.Project, spec.ID, model.SpecDocKindCriteria)
	if err != nil {
		t.Fatalf("specDocPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	var b strings.Builder
	b.WriteString("schema_version = 1\n")
	for _, id := range ids {
		fmt.Fprintf(&b, "\n[[criterion]]\nid = %q\nmode = \"manual\"\ntext = \"fixture criterion %s\"\nevidence_required = \"fixture\"\n", id, id)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write criteria.toml: %v", err)
	}
}

// newContractTestService builds an SDDService with a real (non-empty)
// workflow directory, mirroring newTestSDDServiceWithWorkflowDir but kept
// local to this file so sdd_contract_test.go documents its own fixture
// rather than reaching across files for one used elsewhere for a
// different purpose.
func newContractTestService(t *testing.T, project string) (svc *SDDService, workflowDir string) {
	t.Helper()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })

	sddStore := store.NewSDDStore(database)
	cfg := config.Default()
	workflowDir = t.TempDir()
	cfg.Workflow.Dir = workflowDir
	svc = NewSDDService(sddStore, cfg, project, nil)
	return svc, workflowDir
}

// insertContractTestSpec inserts a spec directly via the store (bypassing
// SpecAdvance's full transition chain), mirroring quality_test.go's own
// insertTestSpec but through svc.store so the fixture matches the service
// under test.
func insertContractTestSpec(t *testing.T, svc *SDDService, id string, lane model.Lane, status model.SpecStatus) *model.Spec {
	t.Helper()
	spec := &model.Spec{
		ID: id, Title: "Contract fixture", Status: status, Project: svc.project, Lane: lane,
	}
	if err := svc.store.CreateSpec(context.Background(), spec); err != nil {
		t.Fatalf("CreateSpec: %v", err)
	}
	return spec
}

// --- ensureCriteriaDeclared ---

func TestEnsureCriteriaDeclared(t *testing.T) {
	tests := []struct {
		name       string
		lane       model.Lane
		status     model.SpecStatus
		next       model.SpecStatus
		writeDoc   bool
		invalidDoc bool
		wantErr    error
		wantMsg    string
	}{
		{
			name:    "standard, speccing->specced, no file: ErrCriteriaNotFound with the path",
			lane:    model.LaneStandard, status: model.SpecStatusSpeccing, next: model.SpecStatusSpecced,
			wantErr: model.ErrCriteriaNotFound,
			wantMsg: "criteria.toml",
		},
		{
			name:    "standard, speccing->specced, valid file: nil",
			lane:    model.LaneStandard, status: model.SpecStatusSpeccing, next: model.SpecStatusSpecced,
			writeDoc: true,
			wantErr:  nil,
		},
		{
			name:    "trivial, any transition: nil",
			lane:    model.LaneTrivial, status: model.SpecStatusSpeccing, next: model.SpecStatusSpecced,
			wantErr: nil,
		},
		{
			name:    "standard, planned->implementing, no file: nil (not the gated transition)",
			lane:    model.LaneStandard, status: model.SpecStatusPlanned, next: model.SpecStatusImplementing,
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newContractTestService(t, "proj")
			spec := insertContractTestSpec(t, svc, "SPEC-100", tt.lane, tt.status)
			if tt.writeDoc {
				writeSpecCriteria(t, svc, spec, "AC1")
			}

			err := svc.ensureCriteriaDeclared(spec, tt.next)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("ensureCriteriaDeclared: got %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ensureCriteriaDeclared: got %v, want errors.Is(_, %v)", err, tt.wantErr)
			}
			if tt.wantMsg != "" && !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error message %q does not contain %q", err.Error(), tt.wantMsg)
			}
		})
	}
}

// TestEnsureCriteriaDeclared_InvalidDocument_NotFoundOnDisk documents the
// "en la practica imposible" branch (spec.md D2 point 4): SpecDocWrite
// already refuses to persist an invalid criteria.toml, so this test
// constructs the impossible case by hand-writing a malformed file directly
// to disk (bypassing SpecDocWrite entirely) and confirms
// ensureCriteriaDeclared fails closed with ErrInvalidCriteria rather than
// treating an unparseable file as absent.
func TestEnsureCriteriaDeclared_InvalidDocument(t *testing.T) {
	svc, _ := newContractTestService(t, "proj")
	spec := insertContractTestSpec(t, svc, "SPEC-101", model.LaneStandard, model.SpecStatusSpeccing)

	path, err := specDocPath(svc.config.WorkflowDir(), spec.Project, spec.ID, model.SpecDocKindCriteria)
	if err != nil {
		t.Fatalf("specDocPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("this is not valid toml {{{"), 0o644); err != nil {
		t.Fatalf("write invalid criteria.toml: %v", err)
	}

	err = svc.ensureCriteriaDeclared(spec, model.SpecStatusSpecced)
	if !errors.Is(err, model.ErrInvalidCriteria) {
		t.Fatalf("ensureCriteriaDeclared: got %v, want errors.Is(_, ErrInvalidCriteria)", err)
	}
}

// TestEnsureCriteriaDeclared_WorkflowDirEmpty_Passes covers D2 point 5: a
// config whose WorkflowDir() is empty (constructed by hand — config.Default()
// never produces this) means the mechanism is off, never a fallback to
// os.Getwd().
func TestEnsureCriteriaDeclared_WorkflowDirEmpty_Passes(t *testing.T) {
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	sddStore := store.NewSDDStore(database)

	cfg := &config.Config{} // Workflow.Dir left empty by hand.
	svc := NewSDDService(sddStore, cfg, "proj", nil)
	spec := insertContractTestSpec(t, svc, "SPEC-102", model.LaneStandard, model.SpecStatusSpeccing)

	if err := svc.ensureCriteriaDeclared(spec, model.SpecStatusSpecced); err != nil {
		t.Fatalf("ensureCriteriaDeclared with empty WorkflowDir: got %v, want nil", err)
	}
}

// --- ensureRejectFindings ---

func TestEnsureRejectFindings(t *testing.T) {
	tests := []struct {
		name     string
		lane     model.Lane
		status   model.SpecStatus
		writeDoc bool
		criteria []string
		findings []model.RejectFinding
		wantErr  error
		wantMsg  string
	}{
		{
			name: "standard from qa, with criteria, no findings: ErrFindingsRequired",
			lane: model.LaneStandard, status: model.SpecStatusQA,
			writeDoc: true, criteria: []string{"AC1", "AC2"},
			wantErr: model.ErrFindingsRequired,
		},
		{
			name: "standard from qa, with criteria, finding with empty Detail: ErrFindingsRequired",
			lane: model.LaneStandard, status: model.SpecStatusQA,
			writeDoc: true, criteria: []string{"AC1"},
			findings: []model.RejectFinding{{CriterionID: "AC1", Detail: "  "}},
			wantErr:  model.ErrFindingsRequired,
		},
		{
			name: "standard from qa, with criteria, unknown CriterionID: ErrUnknownCriterion naming id and declared set",
			lane: model.LaneStandard, status: model.SpecStatusQA,
			writeDoc: true, criteria: []string{"AC1", "AC2"},
			findings: []model.RejectFinding{{CriterionID: "AC99", Detail: "broken"}},
			wantErr:  model.ErrUnknownCriterion,
			wantMsg:  "AC99",
		},
		{
			name: "standard from qa, with criteria, all ids declared: nil",
			lane: model.LaneStandard, status: model.SpecStatusQA,
			writeDoc: true, criteria: []string{"AC1", "AC2"},
			findings: []model.RejectFinding{
				{CriterionID: "AC1", Detail: "broken"},
				{CriterionID: "AC2", Detail: "also broken"},
			},
			wantErr: nil,
		},
		{
			name: "trivial from audit, no findings: nil (exclusion 1)",
			lane: model.LaneTrivial, status: model.SpecStatusAudit,
			writeDoc: false,
			wantErr:  nil,
		},
		{
			name: "standard from done, no findings: nil (exclusion 2)",
			lane: model.LaneStandard, status: model.SpecStatusDone,
			writeDoc: false,
			wantErr:  nil,
		},
		{
			name: "standard from qa, NO criteria.toml, no findings: nil (exclusion 3)",
			lane: model.LaneStandard, status: model.SpecStatusQA,
			writeDoc: false,
			wantErr:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newContractTestService(t, "proj")
			spec := insertContractTestSpec(t, svc, "SPEC-200", tt.lane, tt.status)
			if tt.writeDoc {
				writeSpecCriteria(t, svc, spec, tt.criteria...)
			}

			err := svc.ensureRejectFindings(spec, model.SpecRejectRequest{
				ID: spec.ID, Reason: "review found issues", By: "qa-tester", Findings: tt.findings,
			})
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("ensureRejectFindings: got %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ensureRejectFindings: got %v, want errors.Is(_, %v)", err, tt.wantErr)
			}
			if tt.wantMsg != "" && !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error message %q does not contain %q", err.Error(), tt.wantMsg)
			}
		})
	}
}

// --- renderRejectReason ---

func TestRenderRejectReason(t *testing.T) {
	tests := []struct {
		name     string
		reason   string
		findings []model.RejectFinding
		want     string
	}{
		{
			name:   "zero findings: byte-for-byte today's format",
			reason: "edge case in payment flow",
			want:   "rejected: edge case in payment flow",
		},
		{
			name:   "one finding, no evidence",
			reason: "review found issues",
			findings: []model.RejectFinding{
				{CriterionID: "AC3", Detail: "does not hold"},
			},
			want: "rejected: review found issues\n\n[AC3] does not hold",
		},
		{
			name:   "one finding, with evidence",
			reason: "review found issues",
			findings: []model.RejectFinding{
				{CriterionID: "AC3", Detail: "does not hold", Evidence: "go test output"},
			},
			want: "rejected: review found issues\n\n[AC3] does not hold\n      evidencia: go test output",
		},
		{
			name:   "two findings, mixed evidence",
			reason: "review found issues",
			findings: []model.RejectFinding{
				{CriterionID: "AC3", Detail: "does not hold", Evidence: "go test output"},
				{CriterionID: "AC7", Detail: "also broken"},
			},
			want: "rejected: review found issues\n\n[AC3] does not hold\n      evidencia: go test output\n[AC7] also broken",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderRejectReason(tt.reason, tt.findings)
			if got != tt.want {
				t.Errorf("renderRejectReason() =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}
