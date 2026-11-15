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

// newDraftTrivial creates a trivial spec in draft through the service.
func newDraftTrivial(t *testing.T, svc *SDDService) *model.Spec {
	t.Helper()
	spec, err := svc.SpecNew(context.Background(), model.SpecNewRequest{Title: "tiny", Lane: model.LaneTrivial, Scope: "internal/store/**"})
	if err != nil {
		t.Fatalf("SpecNew: %v", err)
	}
	return spec
}

// assertUntouchedDraft checks a refused spec_quick left the spec in draft
// with exactly historyBefore history rows.
func assertUntouchedDraft(t *testing.T, svc *SDDService, id string, historyBefore int) {
	t.Helper()
	ctx := context.Background()
	got, err := svc.store.GetSpec(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.SpecStatusDraft {
		t.Errorf("status = %s, want draft", got.Status)
	}
	history, err := svc.store.GetSpecHistory(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != historyBefore {
		t.Errorf("history rows = %d, want %d (no new row)", len(history), historyBefore)
	}
}

// TestSpecQuick_RiskLine covers SPEC-184 C13 (spec_quick part).
func TestSpecQuick_RiskLine(t *testing.T) {
	ctx := context.Background()

	refused := []struct {
		name      string
		rationale string
		sentinel  error
		contains  string
	}{
		{"item_declared", "Tiny.\nResumption: obvious\nRisk: item 2 (x)", model.ErrRiskDeclared, "lane_reclassify"},
		{"malformed", "Tiny.\nRisk: perhaps", model.ErrRiskMalformed, "Risk: perhaps"},
	}
	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestSDDService(t, "wirvii/mneme")
			spec := newDraftTrivial(t, svc)
			before, _ := svc.store.GetSpecHistory(ctx, spec.ID)
			_, err := svc.SpecQuick(ctx, model.SpecQuickRequest{ID: spec.ID, Rationale: tt.rationale, By: "orchestrator"})
			if !errors.Is(err, tt.sentinel) {
				t.Fatalf("err = %v, want %v", err, tt.sentinel)
			}
			if !strings.Contains(err.Error(), tt.contains) {
				t.Errorf("err %q lacks %q", err, tt.contains)
			}
			assertUntouchedDraft(t, svc, spec.ID, len(before))
		})
	}

	accepted := []struct{ name, rationale string }{
		{"risk_none", "Tiny.\nResumption: obvious\nRisk: none"},
		{"no_risk_line", "Tiny fix."},
		{"fenced_item_ignored", "Tiny.\n```\nRisk: item 1\n```\nRisk: none"},
	}
	for _, tt := range accepted {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestSDDService(t, "wirvii/mneme")
			spec := newDraftTrivial(t, svc)
			got, err := svc.SpecQuick(ctx, model.SpecQuickRequest{ID: spec.ID, Rationale: tt.rationale, By: "orchestrator"})
			if err != nil {
				t.Fatalf("SpecQuick: %v", err)
			}
			if got.Status != model.SpecStatusImplementing {
				t.Errorf("status = %s, want implementing", got.Status)
			}
		})
	}
}

// TestSpecQuick_ScopeTooBroad covers SPEC-184 C10 (service part): the
// trivial creation verbs (BacklogAdd and SpecNew, the operations C10 calls
// CreateBacklogItem/CreateSpec) and SpecQuick refuse a scope that matches
// everything or uses braces.
func TestSpecQuick_ScopeTooBroad(t *testing.T) {
	ctx := context.Background()
	broad := []string{"**", "*", "**/*.go", "{internal,cmd}/**"}

	t.Run("BacklogAdd", func(t *testing.T) {
		svc := newTestSDDService(t, "wirvii/mneme")
		for _, scope := range broad {
			_, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "x", Lane: model.LaneTrivial, Scope: scope})
			if !errors.Is(err, model.ErrScopeTooBroad) {
				t.Errorf("scope %q: err = %v, want ErrScopeTooBroad", scope, err)
			}
		}
		if _, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "x", Lane: model.LaneTrivial, Scope: "internal/store/**"}); err != nil {
			t.Errorf("internal/store/**: %v", err)
		}
		if _, err := svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: "x", Lane: model.LaneStandard, Scope: "**"}); err != nil {
			t.Errorf("standard lane with ** must stay accepted: %v", err)
		}
	})

	t.Run("SpecNew", func(t *testing.T) {
		svc := newTestSDDService(t, "wirvii/mneme")
		for _, scope := range broad {
			_, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "x", Lane: model.LaneTrivial, Scope: scope})
			if !errors.Is(err, model.ErrScopeTooBroad) {
				t.Errorf("scope %q: err = %v, want ErrScopeTooBroad", scope, err)
			}
		}
		if _, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "x", Lane: model.LaneTrivial, Scope: "internal/store/**"}); err != nil {
			t.Errorf("internal/store/**: %v", err)
		}
	})

	t.Run("SpecQuick", func(t *testing.T) {
		svc := newTestSDDService(t, "wirvii/mneme")
		for i, scope := range broad {
			id := "SPEC-" + string(rune('5'+i)) + "01"
			createAuditSpec(t, svc, id, model.SpecStatusDraft, scope, "")
			_, err := svc.SpecQuick(ctx, model.SpecQuickRequest{ID: id, Rationale: "x\nRisk: none", By: "orchestrator"})
			if !errors.Is(err, model.ErrScopeTooBroad) {
				t.Errorf("scope %q: err = %v, want ErrScopeTooBroad", scope, err)
			}
			assertUntouchedDraft(t, svc, id, 0)
		}
	})
}

// initLaneRepo turns dir into a git repo with one commit and returns HEAD.
func initLaneRepo(t *testing.T, dir string) string {
	t.Helper()
	gitRunLaneTest(t, dir, "init", "-b", "main")
	gitRunLaneTest(t, dir, "config", "user.email", "lane-test@example.com")
	gitRunLaneTest(t, dir, "config", "user.name", "lane-test")
	gitRunLaneTest(t, dir, "config", "commit.gpgsign", "false")
	writeLaneFile(t, dir, "README.md", "x\n")
	gitRunLaneTest(t, dir, "add", ".")
	gitRunLaneTest(t, dir, "commit", "-m", "base")
	return strings.TrimSpace(gitRunLaneTest(t, dir, "rev-parse", "HEAD"))
}

// insertAudit records a lane_audits row for id.
func insertAudit(t *testing.T, svc *SDDService, id string, passed bool, breaches string) {
	t.Helper()
	if err := svc.store.InsertLaneAudit(context.Background(), &model.LaneAuditRecord{SpecID: id, Passed: passed, Breaches: breaches, BaseSHA: "b"}); err != nil {
		t.Fatalf("insert lane audit: %v", err)
	}
}

// TestLaneReclassify_FromFailedAudit covers SPEC-184 C12 (service part):
// reclassifying is allowed from audit only after a failed audit, and the
// reclassified spec keeps its base_sha when it re-enters implementing.
func TestLaneReclassify_FromFailedAudit(t *testing.T) {
	ctx := context.Background()
	svc, _, repoDir := newTestSDDServiceWithRepoDir(t, "wirvii/mneme")
	base := initLaneRepo(t, repoDir)

	spec := createAuditSpec(t, svc, "SPEC-701", model.SpecStatusAudit, "internal/**", base)
	insertAudit(t, svc, spec.ID, false, "risk item 1: path x.sql")

	got, err := svc.LaneReclassify(ctx, model.LaneReclassifyRequest{ID: spec.ID, Lane: model.LaneStandard, By: "orchestrator"})
	if err != nil {
		t.Fatalf("LaneReclassify: %v", err)
	}
	if got.Lane != model.LaneStandard || got.Status != model.SpecStatusSpeccing {
		t.Fatalf("lane/status = %s/%s, want standard/speccing", got.Lane, got.Status)
	}
	history, _ := svc.store.GetSpecHistory(ctx, spec.ID)
	last := history[len(history)-1]
	if !strings.Contains(last.Reason, "reclassified from trivial to standard after a failed audit") {
		t.Errorf("history reason = %q", last.Reason)
	}

	t.Run("audit_without_failed_row", func(t *testing.T) {
		none := createAuditSpec(t, svc, "SPEC-702", model.SpecStatusAudit, "internal/**", base)
		passed := createAuditSpec(t, svc, "SPEC-703", model.SpecStatusAudit, "internal/**", base)
		insertAudit(t, svc, passed.ID, false, "out of scope: a")
		insertAudit(t, svc, passed.ID, true, "")
		for _, id := range []string{none.ID, passed.ID} {
			_, err := svc.LaneReclassify(ctx, model.LaneReclassifyRequest{ID: id, Lane: model.LaneStandard, By: "orchestrator"})
			if !errors.Is(err, model.ErrLaneImmutable) {
				t.Errorf("%s: err = %v, want ErrLaneImmutable", id, err)
			}
		}
	})

	t.Run("implementing_stays_immutable", func(t *testing.T) {
		impl := createAuditSpec(t, svc, "SPEC-704", model.SpecStatusImplementing, "internal/**", base)
		_, err := svc.LaneReclassify(ctx, model.LaneReclassifyRequest{ID: impl.ID, Lane: model.LaneStandard, By: "orchestrator"})
		if !errors.Is(err, model.ErrLaneImmutable) {
			t.Errorf("err = %v, want ErrLaneImmutable", err)
		}
	})

	writeLaneFile(t, repoDir, "later.txt", "y\n")
	gitRunLaneTest(t, repoDir, "add", ".")
	gitRunLaneTest(t, repoDir, "commit", "-m", "later")
	head := strings.TrimSpace(gitRunLaneTest(t, repoDir, "rev-parse", "HEAD"))
	if head == base {
		t.Fatal("HEAD did not move")
	}

	t.Run("base_sha_kept_after_reimplementing", func(t *testing.T) {
		advanceTo(t, svc, got, model.SpecStatusImplementing)
		again, err := svc.store.GetSpec(ctx, got.ID)
		if err != nil {
			t.Fatal(err)
		}
		if again.Status != model.SpecStatusImplementing || again.BaseSHA != base {
			t.Errorf("BaseSHA = %q, want the original %q", again.BaseSHA, base)
		}
	})

	t.Run("control_standard_spec_captures_head", func(t *testing.T) {
		std, err := svc.SpecNew(ctx, model.SpecNewRequest{Title: "std", Lane: model.LaneStandard})
		if err != nil {
			t.Fatal(err)
		}
		advanceTo(t, svc, std, model.SpecStatusImplementing)
		std, err = svc.store.GetSpec(ctx, std.ID)
		if err != nil {
			t.Fatal(err)
		}
		if std.BaseSHA != head {
			t.Errorf("BaseSHA = %q, want HEAD %q", std.BaseSHA, head)
		}
	})
}

// TestLaneStats_RiskBreachCount covers SPEC-184 C11 and the P6 rule that
// risk breaches and reclassifications count specs of any current lane.
func TestLaneStats_RiskBreachCount(t *testing.T) {
	ctx := context.Background()

	t.Run("four_trivial_specs", func(t *testing.T) {
		svc := newTestSDDService(t, "wirvii/mneme")
		rows := []string{
			"risk item 1: path x.sql",
			"out of scope: y",
			"forbidden path modified: z",
			notDeclaredBreach,
		}
		for i, b := range rows {
			id := "SPEC-" + string(rune('1'+i)) + "10"
			createAuditSpec(t, svc, id, model.SpecStatusAudit, "internal/**", "")
			insertAudit(t, svc, id, false, b)
		}
		got, err := svc.LaneStats(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		if got.RiskBreachCount != 2 || got.AuditFailCount != 4 {
			t.Errorf("RiskBreachCount = %d AuditFailCount = %d, want 2 and 4", got.RiskBreachCount, got.AuditFailCount)
		}
	})

	t.Run("earlier_risk_failure_then_pass_does_not_count", func(t *testing.T) {
		svc := newTestSDDService(t, "wirvii/mneme")
		createAuditSpec(t, svc, "SPEC-120", model.SpecStatusDone, "internal/**", "")
		insertAudit(t, svc, "SPEC-120", false, "risk item 1: path x.sql")
		insertAudit(t, svc, "SPEC-120", true, "")
		got, err := svc.LaneStats(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		if got.RiskBreachCount != 0 {
			t.Errorf("RiskBreachCount = %d, want 0", got.RiskBreachCount)
		}
	})

	t.Run("reclassified_after_risk_failure_counts", func(t *testing.T) {
		svc := newTestSDDService(t, "wirvii/mneme")
		createAuditSpec(t, svc, "SPEC-130", model.SpecStatusAudit, "internal/**", "b")
		insertAudit(t, svc, "SPEC-130", false, "risk item 1: path x.sql")
		if _, err := svc.LaneReclassify(ctx, model.LaneReclassifyRequest{ID: "SPEC-130", Lane: model.LaneStandard, By: "orchestrator"}); err != nil {
			t.Fatal(err)
		}
		got, err := svc.LaneStats(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		if got.RiskBreachCount != 1 || got.ReclassifyCount != 1 || got.TrivialCount != 0 || got.AuditFailCount != 0 {
			t.Errorf("stats = %+v, want RiskBreach=1 Reclassify=1 Trivial=0 AuditFail=0", got)
		}
	})
}

// notDeclaredBreach is the auditor's missing-declaration breach text.
const notDeclaredBreach = `risk not declared: the implementer or the orchestrator adds "Risk: none" to tarea.md section entrega (spec_doc_write kind task section entrega)`
