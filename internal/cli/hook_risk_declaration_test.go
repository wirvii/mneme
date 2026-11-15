package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/risk"
)

// openStandardImplementingSpec opens a standard spec, moves it to
// implementing and sets its scope, returning its id.
func openStandardImplementingSpec(f *sddFixture, title, scope string) string {
	f.t.Helper()
	ctx := context.Background()
	item, err := f.svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: title, Lane: model.LaneStandard})
	if err != nil {
		f.t.Fatalf("BacklogAdd: %v", err)
	}
	if _, err := f.svc.BacklogRefine(ctx, model.BacklogRefineRequest{ID: item.ID, Refinement: "r"}); err != nil {
		f.t.Fatalf("BacklogRefine: %v", err)
	}
	spec, err := f.svc.BacklogPromote(ctx, item.ID)
	if err != nil {
		f.t.Fatalf("BacklogPromote: %v", err)
	}
	f.setSpec(spec.ID, "status", "implementing")
	f.setSpec(spec.ID, "scope", scope)
	return spec.ID
}

// writeTareaFixture writes a tarea.md with the given sections. The marker
// format copies tareaVersionLine, taskStartMarker and taskEndMarker of
// internal/service/sdd_task.go, which are not exported.
func writeTareaFixture(t *testing.T, path string, sections map[model.TaskSection]string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("<!-- mneme:tarea v=1 -->\n# SPEC-X — T\n")
	for _, s := range model.TaskSections {
		heading, _ := s.Heading()
		fmt.Fprintf(&b, "\n<!-- mneme:tarea:%s:start -->\n%s\n", s, heading)
		if body := sections[s]; body != "" {
			b.WriteString("\n" + body + "\n")
		}
		fmt.Fprintf(&b, "<!-- mneme:tarea:%s:end -->\n", s)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// riskDeclEnv is a config with temporary data and workflow directories and a
// fixture over the project database it names.
type riskDeclEnv struct {
	cfg  *config.Config
	slug string
	f    *sddFixture
}

func newRiskDeclEnv(t *testing.T) riskDeclEnv {
	t.Helper()
	chdirTemp(t, t.TempDir()) // SPEC-085: keep base-sha capture away from this repository
	cfg := config.Default()
	cfg.Storage.DataDir = t.TempDir()
	cfg.Workflow.Dir = t.TempDir()
	const slug = "proj"
	return riskDeclEnv{cfg: cfg, slug: slug, f: newSDDFixture(t, cfg.ProjectDBPath(slug), slug)}
}

func (e riskDeclEnv) tarea(id string) string {
	return filepath.Join(e.cfg.ProjectWorkflowDir(e.slug), "specs", id, "tarea.md")
}

// declared loads the declarations and asks one question.
func (e riskDeclEnv) declared(t *testing.T, item risk.Item, rel string, synthetic bool) (bool, string, string) {
	t.Helper()
	specs, err := loadRiskDeclarations(e.cfg, e.slug)
	if err != nil {
		t.Fatalf("loadRiskDeclarations: %v", err)
	}
	return declaredFrom(specs)(item, rel, synthetic)
}

const migrationRel = "internal/db/migrations/024_x.sql"

func TestLoadRiskDeclarations(t *testing.T) {
	t.Run("no database file", func(t *testing.T) {
		cfg := config.Default()
		cfg.Storage.DataDir = t.TempDir()
		specs, err := loadRiskDeclarations(cfg, "proj")
		if err != nil || specs != nil {
			t.Errorf("= (%v, %v), want no specs and no error", specs, err)
		}
	})

	sources := []struct {
		name     string
		sections map[model.TaskSection]string
		wantOK   bool
		source   string
	}{
		{"plan", map[model.TaskSection]string{model.TaskSectionPlan: "pasos\n\nRisk: item 1 (migración)"}, true, riskSourcePlan},
		{"diseno", map[model.TaskSection]string{model.TaskSectionDesign: "Risk: item 1"}, true, riskSourceDiseno},
		{"fenced", map[model.TaskSection]string{model.TaskSectionPlan: "```\nRisk: item 1\n```"}, false, ""},
		{"malformed plan falls to diseno", map[model.TaskSection]string{
			model.TaskSectionPlan: "Risk: item 9", model.TaskSectionDesign: "Risk: item 1",
		}, true, riskSourceDiseno},
		{"delivery never declares", map[model.TaskSection]string{model.TaskSectionDelivery: "Risk: item 1"}, false, ""},
	}
	for _, tt := range sources {
		t.Run(tt.name, func(t *testing.T) {
			e := newRiskDeclEnv(t)
			id := openStandardImplementingSpec(e.f, "s", "")
			writeTareaFixture(t, e.tarea(id), tt.sections)
			ok, spec, source := e.declared(t, risk.ItemIrreversibleData, migrationRel, false)
			if ok != tt.wantOK || source != tt.source || (ok && spec != id) {
				t.Errorf("declared = (%v, %q, %q), want (%v, %q, %q)", ok, spec, source, tt.wantOK, id, tt.source)
			}
		})
	}

	t.Run("ignored specs", func(t *testing.T) {
		e := newRiskDeclEnv(t)
		trivial, _ := e.f.openTrivialSpec("t", "internal/db/**")
		writeTareaFixture(t, e.tarea(trivial), map[model.TaskSection]string{model.TaskSectionPlan: "Risk: item 1"})
		archived := openStandardImplementingSpec(e.f, "a", "")
		writeTareaFixture(t, e.tarea(archived), map[model.TaskSection]string{model.TaskSectionPlan: "Risk: item 1"})
		if _, err := e.f.db.Exec(`UPDATE backlog_items SET status = 'archived'
			WHERE id = (SELECT backlog_id FROM specs WHERE id = ?)`, archived); err != nil {
			t.Fatalf("archive: %v", err)
		}
		specs, err := loadRiskDeclarations(e.cfg, e.slug)
		if err != nil || len(specs) != 0 {
			t.Errorf("= (%+v, %v), want neither the trivial nor the frozen spec", specs, err)
		}
	})

	t.Run("tarea.md absent", func(t *testing.T) {
		e := newRiskDeclEnv(t)
		id := openStandardImplementingSpec(e.f, "s", "")
		_ = os.Remove(e.tarea(id))
		specs, err := loadRiskDeclarations(e.cfg, e.slug)
		if err != nil || len(specs) != 1 || specs[0].Plan != nil {
			t.Errorf("= (%+v, %v), want the spec with no declaration and no error", specs, err)
		}
	})

	t.Run("tarea.md is a directory", func(t *testing.T) {
		e := newRiskDeclEnv(t)
		id := openStandardImplementingSpec(e.f, "s", "")
		_ = os.Remove(e.tarea(id))
		if err := os.MkdirAll(e.tarea(id), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := loadRiskDeclarations(e.cfg, e.slug); err == nil {
			t.Error("a directory named tarea.md must be a lookup error")
		}
	})

	t.Run("tarea.md without markers", func(t *testing.T) {
		e := newRiskDeclEnv(t)
		id := openStandardImplementingSpec(e.f, "s", "")
		if err := os.MkdirAll(filepath.Dir(e.tarea(id)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(e.tarea(id), []byte("Risk: item 1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadRiskDeclarations(e.cfg, e.slug); err == nil {
			t.Error("a tarea.md without markers must be a lookup error")
		}
	})

	t.Run("database that cannot be queried", func(t *testing.T) {
		cfg := config.Default()
		cfg.Storage.DataDir = t.TempDir()
		path := cfg.ProjectDBPath("proj")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadRiskDeclarations(cfg, "proj"); err == nil {
			t.Error("a database without the specs table must be a lookup error")
		}
	})
}

func TestDeclaredFrom(t *testing.T) {
	item1 := &risk.Declaration{Items: []risk.Item{risk.ItemIrreversibleData}}
	item5 := &risk.Declaration{Items: []risk.Item{risk.ItemDelivery}}
	item6 := &risk.Declaration{Items: []risk.Item{risk.ItemUntestable}}
	tests := []struct {
		name      string
		spec      openStandardSpec
		item      risk.Item
		rel       string
		synthetic bool
		wantOK    bool
		source    string
	}{
		{"scope outside bounds the plan line", openStandardSpec{ID: "SPEC-1", Scope: "apps/**", Plan: item1}, 1, migrationRel, false, false, ""},
		{"empty scope declares project-wide", openStandardSpec{ID: "SPEC-1", Plan: item1}, 1, migrationRel, false, true, riskSourcePlan},
		{"scope covering the path declares 1", openStandardSpec{ID: "SPEC-1", Scope: "internal/db/**"}, 1, migrationRel, false, true, riskSourceScope},
		{"scope covering the path declares 2", openStandardSpec{ID: "SPEC-1", Scope: "internal/**"}, 2, "internal/.env", false, true, riskSourceScope},
		{"scope never declares 6", openStandardSpec{ID: "SPEC-1", Scope: "internal/db/**"}, 6, "internal/db/x_test.go", false, false, ""},
		{"plan declares 6 inside scope", openStandardSpec{ID: "SPEC-1", Scope: "internal/store/**", Plan: item6}, 6, "internal/store/x_test.go", false, true, riskSourcePlan},
		{"synthetic not bounded by scope", openStandardSpec{ID: "SPEC-1", Scope: "internal/**", Plan: item5}, 5, "go.mod", true, true, riskSourcePlan},
		{"scope never declares a synthetic write", openStandardSpec{ID: "SPEC-1", Scope: "**/go.mod"}, 5, "go.mod", true, false, ""},
		{"diseno after plan", openStandardSpec{ID: "SPEC-1", Plan: item5, Diseno: item1}, 1, migrationRel, false, true, riskSourceDiseno},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, spec, source := declaredFrom([]openStandardSpec{tt.spec})(tt.item, tt.rel, tt.synthetic)
			if ok != tt.wantOK || source != tt.source || (ok && spec != tt.spec.ID) {
				t.Errorf("= (%v, %q, %q), want (%v, %q, %q)", ok, spec, source, tt.wantOK, tt.spec.ID, tt.source)
			}
		})
	}
	if ok, _, _ := declaredFrom(nil)(1, migrationRel, false); ok {
		t.Error("no specs must declare nothing")
	}
}
