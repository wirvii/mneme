package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/enforcement"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// sddFixture is a real SQLite project database driven through the real
// SDDService, so the scope query is tested against the schema and the
// freeze semantics the service actually produces.
type sddFixture struct {
	t       *testing.T
	db      *db.DB
	svc     *service.SDDService
	project string
}

func newSDDFixture(t *testing.T, dbPath, project string) *sddFixture {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	svc := service.NewSDDService(store.NewSDDStore(database), config.Default(), project, nil)
	return &sddFixture{t: t, db: database, svc: svc, project: project}
}

// openTrivialSpec creates a trivial spec through the BL -> spec route and
// moves it to implementing; it returns the spec and its backlog item id.
func (f *sddFixture) openTrivialSpec(title, scope string) (specID, backlogID string) {
	f.t.Helper()
	ctx := context.Background()
	item, err := f.svc.BacklogAdd(ctx, model.BacklogAddRequest{Title: title, Lane: model.LaneTrivial, Scope: scope})
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
	if _, err := f.svc.SpecQuick(ctx, model.SpecQuickRequest{ID: spec.ID, Rationale: "trivial", By: "orchestrator"}); err != nil {
		f.t.Fatalf("SpecQuick: %v", err)
	}
	return spec.ID, item.ID
}

// setSpec forces a column of one spec, for states the service only reaches
// through git-dependent steps (audit, done) or never reaches at all.
func (f *sddFixture) setSpec(id, column, value string) {
	f.t.Helper()
	if _, err := f.db.Exec("UPDATE specs SET "+column+" = ? WHERE project = ? AND id = ?", value, f.project, id); err != nil {
		f.t.Fatalf("update specs.%s: %v", column, err)
	}
}

// TestTrivialScopesFromDB covers criterion "which-specs".
func TestTrivialScopesFromDB(t *testing.T) {
	const project = "wirvii/mneme"
	chdirTemp(t, t.TempDir()) // SPEC-085: keep base-sha capture away from this repository
	dbPath := filepath.Join(t.TempDir(), "p.db")
	f := newSDDFixture(t, dbPath, project)
	ctx := context.Background()

	open1, _ := f.openTrivialSpec("open one", "internal/model/*.go")
	open2, _ := f.openTrivialSpec("open two", "docs/*.md")

	for _, st := range []string{"draft", "rationale", "audit", "done"} {
		id, _ := f.openTrivialSpec("state "+st, "excluded/"+st+"/*")
		f.setSpec(id, "status", st)
	}

	std, err := f.svc.SpecNew(ctx, model.SpecNewRequest{Title: "standard", Lane: model.LaneStandard})
	if err != nil {
		t.Fatal(err)
	}
	f.setSpec(std.ID, "status", "implementing")
	f.setSpec(std.ID, "scope", "excluded/standard/*")

	emptyID, _ := f.openTrivialSpec("empty scope", "excluded/empty/*")
	f.setSpec(emptyID, "scope", "")

	frozenID, frozenBL := f.openTrivialSpec("archived item", "excluded/archived/*")
	if _, err := f.svc.BacklogArchive(ctx, model.BacklogArchiveRequest{ID: frozenBL, Reason: "discarded"}); err != nil {
		t.Fatalf("BacklogArchive: %v", err)
	}
	if spec, err := f.svc.SpecStatus(ctx, frozenID); err != nil || spec.Frozen == nil {
		t.Fatalf("precondition: spec must be frozen by the real service (err=%v)", err)
	}

	missingID, _ := f.openTrivialSpec("missing item", "excluded/missing/*")
	f.setSpec(missingID, "backlog_id", "BL-9999")

	// A spec of ANOTHER project with the same shape must not leak in.
	foreign, err := f.svc.SpecNew(ctx, model.SpecNewRequest{Title: "foreign", Lane: model.LaneTrivial, Scope: "excluded/foreign/*", Project: "someone/else"})
	if err != nil {
		t.Fatalf("SpecNew foreign: %v", err)
	}
	if _, err := f.db.Exec("UPDATE specs SET status = 'implementing' WHERE project = ? AND id = ?", "someone/else", foreign.ID); err != nil {
		t.Fatalf("foreign update: %v", err)
	}

	got, err := trivialScopesFromDB(dbPath, project)()
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	sort.Strings(got)
	want := []string{"docs/*.md", "internal/model/*.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("scopes = %v, want %v (open specs %s, %s)", got, want, open1, open2)
	}
}

// TestTrivialScopesFromDB_Errors: a missing database and a database without
// the schema are errors, never an empty answer.
func TestTrivialScopesFromDB_Errors(t *testing.T) {
	dir := t.TempDir()
	if _, err := trivialScopesFromDB(filepath.Join(dir, "none.db"), "p")(); err == nil {
		t.Error("missing database must be an error")
	}
	if err := os.WriteFile(filepath.Join(dir, "corrupt.db"), []byte("not a database"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := trivialScopesFromDB(filepath.Join(dir, "corrupt.db"), "p")(); err == nil {
		t.Error("corrupt database must be an error")
	}

	noSchema := filepath.Join(dir, "noschema.db")
	database, err := db.Open(noSchema)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("DROP TABLE specs"); err != nil {
		t.Skipf("cannot drop specs in this schema: %v", err)
	}
	database.Close()
	if _, err := trivialScopesFromDB(noSchema, "p")(); err == nil {
		t.Error("database without the specs table must be an error")
	}
}

// blockingBase is an OwnershipFunc that always blocks with a fixed owner,
// counting its calls.
func blockingBase(owner string, calls *int) enforcement.OwnershipFunc {
	return func(string) (bool, string) {
		*calls++
		return true, owner
	}
}

// TestWithTrivialInline_Table covers criterion "grant-in-scope" at the
// wrapper level.
func TestWithTrivialInline_Table(t *testing.T) {
	cwd := t.TempDir()
	tests := []struct {
		name      string
		target    string
		scopes    []string
		wantBlock bool
		wantGrant string
	}{
		{"in scope relative", "internal/model/sdd.go", []string{"internal/model/*.go"}, false, "internal/model/sdd.go"},
		{"in scope absolute", filepath.Join(cwd, "internal/model/sdd.go"), []string{"internal/model/*.go"}, false, "internal/model/sdd.go"},
		{"outside scope", "internal/store/x.go", []string{"internal/model/*.go"}, true, ""},
		{"second scope matches", "docs/a.md", []string{"internal/model/*.go", "docs/*.md"}, false, "docs/a.md"},
		{"no scopes", "internal/model/sdd.go", nil, true, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			var granted string
			own := withTrivialInline(blockingBase("legacy", &calls),
				func() ([]string, error) { return tc.scopes, nil }, cwd,
				func(rel string) { granted = rel })
			block, owner := own(tc.target)
			if block != tc.wantBlock {
				t.Errorf("block = %v, want %v", block, tc.wantBlock)
			}
			if tc.wantBlock && owner != "legacy" {
				t.Errorf("owner = %q, want the base owner", owner)
			}
			if granted != tc.wantGrant {
				t.Errorf("granted = %q, want %q", granted, tc.wantGrant)
			}
		})
	}
}

// TestWithTrivialInline_ForbiddenAndOutOfTree covers criterion
// "forbidden-globs".
func TestWithTrivialInline_ForbiddenAndOutOfTree(t *testing.T) {
	cwd := t.TempDir()
	calls := 0
	own := withTrivialInline(blockingBase("legacy", &calls),
		func() ([]string, error) { return []string{"**"}, nil }, cwd, nil)

	for _, target := range []string{
		"internal/db/migrations/023_x.sql",
		"cmd/mneme/main.go",
		"internal/install/assets/x.md",
		"schema.go",
	} {
		if block, _ := own(target); !block {
			t.Errorf("%s granted, want blocked (forbidden glob)", target)
		}
	}
	if block, _ := own(filepath.Join(filepath.Dir(cwd), "outside", "x.go")); !block {
		t.Error("path outside the cwd tree granted")
	}
	if block, _ := own(""); !block {
		t.Error("empty target granted")
	}
	if block, _ := own("internal/ok.go"); block {
		t.Error("control: an ordinary path under scope ** must be granted")
	}
}

// TestWithTrivialInline_FailClosed covers criterion "fail-closed".
func TestWithTrivialInline_FailClosed(t *testing.T) {
	cwd := t.TempDir()

	t.Run("lookup error returns base result", func(t *testing.T) {
		calls := 0
		own := withTrivialInline(blockingBase("backend", &calls),
			func() ([]string, error) { return []string{"**"}, errors.New("db broken") }, cwd, nil)
		block, owner := own("internal/x.go")
		if !block || owner != "backend" {
			t.Errorf("got (%v, %q), want the base result (true, backend)", block, owner)
		}
	})

	t.Run("base allowing never consults the lookup", func(t *testing.T) {
		lookups := 0
		own := withTrivialInline(func(string) (bool, string) { return false, "" },
			func() ([]string, error) { lookups++; return nil, nil }, cwd, nil)
		if block, _ := own("internal/x.go"); block {
			t.Error("base allowed, result blocked")
		}
		if lookups != 0 {
			t.Errorf("lookup called %d times, want 0", lookups)
		}
	})

	t.Run("lookup memoised", func(t *testing.T) {
		lookups := 0
		calls := 0
		own := withTrivialInline(blockingBase("x", &calls),
			func() ([]string, error) { lookups++; return []string{"a/*"}, nil }, cwd, nil)
		own("a/1")
		own("a/2")
		own("b/1")
		if lookups != 1 {
			t.Errorf("lookup called %d times, want 1", lookups)
		}
	})

	t.Run("missing database via production lookup", func(t *testing.T) {
		calls := 0
		own := withTrivialInline(blockingBase("legacy", &calls),
			trivialScopesFromDB(filepath.Join(t.TempDir(), "none.db"), "p"), cwd, nil)
		if block, owner := own("internal/x.go"); !block || owner != "legacy" {
			t.Errorf("got (%v, %q), want (true, legacy)", block, owner)
		}
	})
}

// fakeScopeRows scripts a scopeRows: it yields values one by one, then fails
// Scan at scanFailAt (-1 never) and reports errAfter from Err.
type fakeScopeRows struct {
	values     []string
	pos        int
	scanFailAt int
	errAfter   error
}

func (f *fakeScopeRows) Next() bool { f.pos++; return f.pos <= len(f.values) }

func (f *fakeScopeRows) Scan(dest ...any) error {
	if f.pos-1 == f.scanFailAt {
		return errors.New("scan boom")
	}
	*(dest[0].(*string)) = f.values[f.pos-1]
	return nil
}

func (f *fakeScopeRows) Err() error { return f.errAfter }

// TestCollectTrivialScopes_ReadErrors: a Scan or iteration failure yields an
// error and no scopes, and withTrivialInline then keeps the block
// (fail-closed).
func TestCollectTrivialScopes_ReadErrors(t *testing.T) {
	tests := []struct {
		name    string
		rows    *fakeScopeRows
		wantErr string
	}{
		{"scan fails on first row", &fakeScopeRows{values: []string{"a/*"}, scanFailAt: 0}, "scan"},
		{"scan fails after a good row", &fakeScopeRows{values: []string{"a/*", "b/*"}, scanFailAt: 1}, "scan"},
		{"iteration error", &fakeScopeRows{values: []string{"a/*"}, scanFailAt: -1, errAfter: errors.New("rows boom")}, "rows"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := collectTrivialScopes(tt.rows)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want one mentioning %q", err, tt.wantErr)
			}
			if got != nil {
				t.Errorf("scopes = %v, want nil on error", got)
			}
			tt.rows.pos = 0
			calls := 0
			own := withTrivialInline(blockingBase("legacy", &calls),
				func() ([]string, error) { return collectTrivialScopes(tt.rows) }, "/repo", nil)
			if block, owner := own("a/1"); !block || owner != "legacy" {
				t.Errorf("got (%v, %q), want (true, legacy): a read error must fail closed", block, owner)
			}
		})
	}

	t.Run("healthy rows", func(t *testing.T) {
		got, err := collectTrivialScopes(&fakeScopeRows{values: []string{"a/*", "b/*"}, scanFailAt: -1})
		if err != nil || !reflect.DeepEqual(got, []string{"a/*", "b/*"}) {
			t.Errorf("got (%v, %v)", got, err)
		}
	})
}

// --- evaluateDelegation wiring (real config.toml + real database) ---------

// enableTrivialInline writes the opt-in into the sandboxed HOME's config.
func enableTrivialInline(t *testing.T) {
	t.Helper()
	path := config.DefaultPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[delegation]\norchestrator_trivial_inline = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func orchestratorInput(tool, path string) hookPreToolInput {
	in := hookPreToolInput{ToolName: tool}
	in.ToolInput.FilePath = path
	return in
}

// TestEvaluateDelegation_TrivialInline_DefaultOff covers "default-off": with
// the option undeclared the edit is blocked as always and the scope lookup is
// never even built.
func TestEvaluateDelegation_TrivialInline_DefaultOff(t *testing.T) {
	slug, dbPath := setupDelegationRepo(t)
	newSDDFixture(t, dbPath, slug).openTrivialSpec("open", "internal/model/*.go")

	built := 0
	orig := newTrivialScopeLookup
	newTrivialScopeLookup = func(path, project string) trivialScopeLookup {
		built++
		return orig(path, project)
	}
	t.Cleanup(func() { newTrivialScopeLookup = orig })

	cwd, _ := os.Getwd()
	got := evaluateDelegation(orchestratorInput("Edit", "internal/model/sdd.go"), cwd)
	if !got.Block {
		t.Fatalf("Block = false with the option off: %+v", got)
	}
	if built != 0 {
		t.Errorf("lookup built %d times with the option off, want 0", built)
	}
}

// TestEvaluateDelegation_TrivialInline_Grants covers "grant-in-scope"
// end to end, including NotebookEdit staying blocked and the audit event.
func TestEvaluateDelegation_TrivialInline_Grants(t *testing.T) {
	slug, dbPath := setupDelegationRepo(t)
	newSDDFixture(t, dbPath, slug).openTrivialSpec("open", "internal/model/*.go")
	enableTrivialInline(t)
	cwd, _ := os.Getwd()

	for _, tool := range []string{"Edit", "Write", "MultiEdit"} {
		if got := evaluateDelegation(orchestratorInput(tool, "internal/model/sdd.go"), cwd); got.Block {
			t.Errorf("%s inside the scope blocked: %+v", tool, got)
		}
	}
	if got := evaluateDelegation(orchestratorInput("Edit", "internal/store/x.go"), cwd); !got.Block {
		t.Error("Edit outside the scope allowed")
	}
	nb := hookPreToolInput{ToolName: "NotebookEdit"}
	nb.ToolInput.NotebookPath = "internal/model/sdd.go"
	if got := evaluateDelegation(nb, cwd); !got.Block {
		t.Error("NotebookEdit must stay blocked")
	}

	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		t.Fatal(err)
	}
	log, err := os.ReadFile(enforcelogPath(cfg.Storage.DataDir, slug))
	if err != nil || !containsAll(string(log), "trivial_inline_scope", "internal/model/sdd.go", `"allow"`) {
		t.Errorf("grant event missing from the enforcement log (err=%v): %s", err, log)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}

// TestEvaluateDelegation_TrivialInline_BashUnrelaxed covers "bash-unrelaxed".
func TestEvaluateDelegation_TrivialInline_BashUnrelaxed(t *testing.T) {
	slug, dbPath := setupDelegationRepo(t)
	newSDDFixture(t, dbPath, slug).openTrivialSpec("open", "internal/model/*.go")
	enableTrivialInline(t)
	cwd, _ := os.Getwd()

	for _, cmd := range []string{"echo x > internal/model/sdd.go", "sed -i s/a/b/ internal/model/sdd.go"} {
		in := hookPreToolInput{ToolName: "Bash"}
		in.ToolInput.Command = cmd
		if got := evaluateDelegation(in, cwd); !got.Block {
			t.Errorf("Bash %q allowed with the option on: %+v", cmd, got)
		}
	}
}

// TestEvaluateDelegation_TrivialInline_SubagentUnchanged covers
// "subagents-unaffected": the decision for a subagent is the same with the
// option on and off.
func TestEvaluateDelegation_TrivialInline_SubagentUnchanged(t *testing.T) {
	slug, dbPath := setupDelegationRepo(t)
	newSDDFixture(t, dbPath, slug).openTrivialSpec("open", "internal/model/*.go")
	seedManifest(t, dbPath, slug, `[{"role":"backend","archetype":"backend","areas":["internal/store/**"],"areas_complete":true}]`)
	cwd, _ := os.Getwd()

	in := orchestratorInput("Edit", "internal/model/sdd.go")
	in.AgentID, in.AgentType = "agent-1", "backend"

	off := evaluateDelegation(in, cwd)
	enableTrivialInline(t)
	on := evaluateDelegation(in, cwd)
	if !reflect.DeepEqual(off, on) {
		t.Errorf("subagent decision changed with the option on: off=%+v on=%+v", off, on)
	}
}
