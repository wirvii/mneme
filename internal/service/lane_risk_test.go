package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/risk"
)

// createAuditSpec stores a trivial spec directly at status (the store does
// not validate scope or lane rules; the tests below need exact states).
func createAuditSpec(t *testing.T, svc *SDDService, id string, status model.SpecStatus, scope, base string) *model.Spec {
	t.Helper()
	spec := &model.Spec{
		ID: id, Title: "Trivial spec", Status: status, Project: svc.project,
		Lane: model.LaneTrivial, Scope: scope, BaseSHA: base,
	}
	if err := svc.store.CreateSpec(context.Background(), spec); err != nil {
		t.Fatalf("create spec: %v", err)
	}
	return spec
}

// writeDelivery writes the entrega section of spec's tarea.md.
func writeDelivery(t *testing.T, svc *SDDService, spec *model.Spec, content string) {
	t.Helper()
	path, err := specDocPath(svc.config.WorkflowDir(), spec.Project, spec.ID, model.SpecDocKindTask)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := writeTaskSection(path, spec, model.TaskSectionDelivery, content); err != nil {
		t.Fatalf("write entrega: %v", err)
	}
}

// TestDeclaredRisk_Precedence covers SPEC-184 C13 (declaration part): the
// delivery's prose wins over the rationale, fenced lines never count, and
// a missing or malformed tarea.md falls through to the next source.
func TestDeclaredRisk_Precedence(t *testing.T) {
	tests := []struct {
		name       string
		delivery   *string
		rawTarea   string
		rationales []string
		wantNil    bool
		wantNone   bool
		wantItems  []risk.Item
		wantSource string
	}{
		{
			name:       "entrega_wins",
			delivery:   ptr("Done.\n\nRisk: item 5 (x)"),
			rationales: []string{"x\nRisk: none"},
			wantItems:  []risk.Item{5}, wantSource: "entrega",
		},
		{
			name:       "fenced_entrega_ignored",
			delivery:   ptr("Example:\n\n```\nRisk: item 1\n```\n"),
			rationales: []string{"x\nRisk: none"},
			wantNone:   true, wantSource: "rationale",
		},
		{
			name:       "no_tarea_uses_rationale",
			rationales: []string{"x\nRisk: none"},
			wantNone:   true, wantSource: "rationale",
		},
		{
			name:       "malformed_tarea_falls_through",
			rawTarea:   "no markers at all\nRisk: item 1\n",
			rationales: []string{"x\nRisk: none"},
			wantNone:   true, wantSource: "rationale",
		},
		{
			name:       "latest_rationale_entry_wins",
			rationales: []string{"first\nRisk: item 2", "second\nRisk: none"},
			wantNone:   true, wantSource: "rationale",
		},
		{
			name:       "none_anywhere",
			delivery:   ptr("Done, no risk line."),
			rationales: []string{"just a reason"},
			wantNil:    true,
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestSDDService(t, "wirvii/mneme")
			spec := createAuditSpec(t, svc, "SPEC-"+string(rune('1'+i))+"00", model.SpecStatusAudit, "internal/**", "")
			for _, r := range tt.rationales {
				seedRationale(t, svc, spec.ID, r)
			}
			if tt.delivery != nil {
				writeDelivery(t, svc, spec, *tt.delivery)
			}
			if tt.rawTarea != "" {
				path, _ := specDocPath(svc.config.WorkflowDir(), spec.Project, spec.ID, model.SpecDocKindTask)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(tt.rawTarea), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			decl, source, err := svc.declaredRisk(context.Background(), spec)
			if err != nil {
				t.Fatalf("declaredRisk err = %v", err)
			}
			if tt.wantNil {
				if decl != nil || source != "" {
					t.Errorf("declaredRisk = %+v, %q; want nil, \"\"", decl, source)
				}
				return
			}
			if decl == nil {
				t.Fatal("declaredRisk = nil, want a declaration")
			}
			if decl.None != tt.wantNone || !reflect.DeepEqual(decl.Items, tt.wantItems) || source != tt.wantSource {
				t.Errorf("declaredRisk = %+v, %q; want None=%v Items=%v source=%q", decl, source, tt.wantNone, tt.wantItems, tt.wantSource)
			}
		})
	}

	t.Run("malformed_rationale_reports_error", func(t *testing.T) {
		svc := newTestSDDService(t, "wirvii/mneme")
		spec := createAuditSpec(t, svc, "SPEC-900", model.SpecStatusAudit, "internal/**", "")
		seedRationale(t, svc, spec.ID, "Risk: perhaps")
		decl, source, err := svc.declaredRisk(context.Background(), spec)
		if !errors.Is(err, risk.ErrMalformedDeclaration) || source != "rationale" || decl == nil || decl.Line != "Risk: perhaps" {
			t.Errorf("declaredRisk = %+v, %q, %v; want the malformed line from rationale", decl, source, err)
		}
	})
}

func ptr(s string) *string { return &s }

// TestLaneAudit_FailureRemedy covers SPEC-184 C13 (remedy part) end to end
// on real throwaway repos: a failed risk audit names the item and the way
// out, reclassify before override, and the failure memory ends with the
// same remedy.
func TestLaneAudit_FailureRemedy(t *testing.T) {
	ctx := context.Background()

	t.Run("risk_item_remedy", func(t *testing.T) {
		svc := newTestSDDServiceWithMemory(t, "project")
		dir, base := laneAuditFixture(t, func(dir string) {
			writeLaneFile(t, dir, "internal/db/migrations/001.sql", "-- a\n")
		})
		svc.WithRepoDir(dir)
		spec := createAuditSpec(t, svc, "SPEC-301", model.SpecStatusAudit, "internal/**", base)
		seedRationale(t, svc, spec.ID, "x\nRisk: none")

		result, err := svc.LaneAudit(ctx, model.LaneAuditRequest{ID: spec.ID})
		if !errors.Is(err, model.ErrAuditFailed) {
			t.Fatalf("LaneAudit err = %v, want ErrAuditFailed", err)
		}
		for _, want := range []string{"Risk item 1", "lane_reclassify", "mneme lane reclassify"} {
			if !strings.Contains(result.Remedy, want) {
				t.Errorf("Remedy %q lacks %q", result.Remedy, want)
			}
		}
		if strings.Index(result.Remedy, "override") < strings.Index(result.Remedy, "reclassify") {
			t.Errorf("Remedy %q mentions override before reclassify", result.Remedy)
		}
		if result.RiskSource != "rationale" || result.RiskDeclared != "none" {
			t.Errorf("RiskSource = %q RiskDeclared = %q, want rationale/none", result.RiskSource, result.RiskDeclared)
		}

		found, err := svc.memorySvc.Search(ctx, model.SearchRequest{Query: "Lane audit failed", Project: "project"})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		matched := false
		for _, r := range found.Results {
			if r.Memory != nil && strings.HasSuffix(strings.TrimSpace(r.Content), result.Remedy) {
				matched = true
			}
		}
		if !matched {
			t.Errorf("no failure memory ends with the Remedy %q", result.Remedy)
		}
	})

	t.Run("not_declared_remedy", func(t *testing.T) {
		svc := newTestSDDService(t, "project")
		dir, base := laneAuditFixture(t, func(dir string) {
			writeLaneFile(t, dir, "internal/store/small.go", "package store\n")
		})
		svc.WithRepoDir(dir)
		spec := createAuditSpec(t, svc, "SPEC-302", model.SpecStatusAudit, "internal/**", base)

		result, err := svc.LaneAudit(ctx, model.LaneAuditRequest{ID: spec.ID})
		if !errors.Is(err, model.ErrAuditFailed) {
			t.Fatalf("LaneAudit err = %v, want ErrAuditFailed", err)
		}
		if !strings.HasPrefix(result.Remedy, "Declare the risk:") || !strings.Contains(result.Remedy, "lane_reclassify SPEC-302 standard") {
			t.Errorf("Remedy = %q, want the declare-the-risk sentence", result.Remedy)
		}
	})

	t.Run("out_of_scope_remedy", func(t *testing.T) {
		svc := newTestSDDService(t, "project")
		dir, base := laneAuditFixture(t, func(dir string) {
			writeLaneFile(t, dir, "internal/other/x.go", "package other\n")
		})
		svc.WithRepoDir(dir)
		spec := createAuditSpec(t, svc, "SPEC-303", model.SpecStatusAudit, "internal/store/**", base)
		seedRationale(t, svc, spec.ID, "x\nRisk: none")

		result, err := svc.LaneAudit(ctx, model.LaneAuditRequest{ID: spec.ID})
		if !errors.Is(err, model.ErrAuditFailed) {
			t.Fatalf("LaneAudit err = %v, want ErrAuditFailed", err)
		}
		if !strings.HasPrefix(result.Remedy, "Files outside the declared scope changed.") {
			t.Errorf("Remedy = %q, want the out-of-scope sentence", result.Remedy)
		}
	})
}

// TestLaneRemedy pins the exact remedy sentences of SPEC-184 §5.
func TestLaneRemedy(t *testing.T) {
	const (
		single  = "Risk item 1 (see mneme-flow/references/risk.md) makes this change standard. Orchestrator: lane_reclassify SPEC-9 standard (mneme lane reclassify SPEC-9 standard --by <you>). Only if this is a false positive, such as a generated file, the orchestrator may ask the person for lane_override."
		plural  = "Risk items 1, 5 (see mneme-flow/references/risk.md) make this change standard. Orchestrator: lane_reclassify SPEC-9 standard (mneme lane reclassify SPEC-9 standard --by <you>). Only if this is a false positive, such as a generated file, the orchestrator may ask the person for lane_override."
		declare = "Declare the risk: the implementer or the orchestrator writes \"Risk: none\" in tarea.md section entrega (spec_doc_write kind task section entrega), then run lane_audit again; if the change does touch a risk item, the orchestrator runs lane_reclassify SPEC-9 standard (mneme lane reclassify SPEC-9 standard --by <you>)."
		scope   = "Files outside the declared scope changed. Revert them (spec_reject back to implementing) or the orchestrator runs lane_reclassify SPEC-9 standard (mneme lane reclassify SPEC-9 standard --by <you>)."
	)
	tests := []struct {
		name     string
		result   model.LaneAuditResult
		breaches []string
		want     string
	}{
		{name: "passed", result: model.LaneAuditResult{Passed: true}, want: ""},
		{name: "single_item", result: model.LaneAuditResult{RiskItems: []int{1}, RiskDeclared: "none"}, breaches: []string{"risk item 1: path x.sql"}, want: single},
		{name: "two_items_plural", result: model.LaneAuditResult{RiskItems: []int{5}, RiskDeclared: "item 1"}, breaches: []string{"risk item 5: path go.mod", "risk declared: item 1; the trivial lane admits only Risk: none"}, want: plural},
		{name: "risk_wins_over_scope_and_declaration", result: model.LaneAuditResult{RiskItems: []int{1}}, breaches: []string{"out of scope: a", "risk item 1: path a.sql", "risk not declared: x"}, want: single},
		{name: "not_declared", breaches: []string{"risk not declared: x"}, want: declare},
		{name: "malformed", breaches: []string{"risk declaration malformed: Risk: maybe"}, want: declare},
		{name: "out_of_scope", result: model.LaneAuditResult{RiskDeclared: "none"}, breaches: []string{"out of scope: a"}, want: scope},
		{name: "declaration_then_scope", breaches: []string{"out of scope: a", "risk not declared: x"}, want: declare + " " + scope},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.result
			if got := laneRemedy(&result, tt.breaches, "SPEC-9"); got != tt.want {
				t.Errorf("laneRemedy =\n  %q\nwant\n  %q", got, tt.want)
			}
		})
	}
	if laneRemedy(nil, nil, "SPEC-9") != "" {
		t.Error("laneRemedy(nil) != \"\"")
	}
}
