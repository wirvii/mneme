package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/quality"
)

// gitRunBudgetTest runs git with a local, explicit identity and disabled
// GPG signing (R-C) — mirrors internal/quality/git_test.go's own helper so
// this real-repository guardian never depends on the machine's own
// ~/.gitconfig.
func gitRunBudgetTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=budget-test",
		"GIT_AUTHOR_EMAIL=budget-test@example.com",
		"GIT_COMMITTER_NAME=budget-test",
		"GIT_COMMITTER_EMAIL=budget-test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// TestRealGitChain_SymbolDelta is the D20 point 1 / plan-P8 guardian: a
// REAL git repository in t.TempDir(), REAL Go source, and the REAL Go
// extractor (via symbolExtractorAdapter) — not a fake — carried through
// Git.FileAtRef -> CollectSymbols -> DiffSymbols, asserting AC7's five
// classifications symbol by symbol: a function added, one deleted, one
// whose body changed (modified), one left untouched (must not appear
// anywhere), and a file renamed with its function unchanged inside (moved,
// never created+deleted). This is emphatically not the mutation-testing
// recursion the plan calls out: it is this repository's OWN, disposable
// fixture, never mneme's own source tree.
func TestRealGitChain_SymbolDelta(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}

	dir := t.TempDir()
	gitRunBudgetTest(t, dir, "init", "-b", "main")
	gitRunBudgetTest(t, dir, "config", "user.email", "budget-test@example.com")
	gitRunBudgetTest(t, dir, "config", "user.name", "budget-test")
	gitRunBudgetTest(t, dir, "config", "commit.gpgsign", "false")

	write := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	// Base commit: three files, each with one top-level function.
	write("deleted.go", "package fixture\n\nfunc ToBeDeleted() int {\n\treturn 1\n}\n")
	write("modified.go", "package fixture\n\nfunc Modified() int {\n\treturn 1\n}\n")
	write("untouched.go", "package fixture\n\nfunc Untouched() int {\n\treturn 1\n}\n")
	write("before.go", "package fixture\n\nfunc Moved() int {\n\treturn 1\n}\n")
	gitRunBudgetTest(t, dir, "add", ".")
	gitRunBudgetTest(t, dir, "commit", "-m", "base")
	base := strings.TrimSpace(gitRunBudgetTest(t, dir, "rev-parse", "HEAD"))

	// HEAD commit: delete deleted.go, change modified.go's body, add
	// created.go, leave untouched.go alone, and rename before.go->after.go
	// WITHOUT touching its function body.
	if err := os.Remove(filepath.Join(dir, "deleted.go")); err != nil {
		t.Fatalf("remove deleted.go: %v", err)
	}
	write("modified.go", "package fixture\n\nfunc Modified() int {\n\treturn 2\n}\n")
	write("created.go", "package fixture\n\nfunc Created() int {\n\treturn 1\n}\n")
	if err := os.Rename(filepath.Join(dir, "before.go"), filepath.Join(dir, "after.go")); err != nil {
		t.Fatalf("rename before.go: %v", err)
	}
	gitRunBudgetTest(t, dir, "add", "-A")
	gitRunBudgetTest(t, dir, "commit", "-m", "head")

	g := &quality.Git{RepoDir: dir}
	changes, err := g.ChangedFilesInRange(base, "HEAD")
	if err != nil {
		t.Fatalf("ChangedFilesInRange: %v", err)
	}

	renames := map[string]string{}
	var basePaths, headPaths []string
	for _, c := range changes {
		switch c.Status {
		case quality.FileStatusRenamed:
			renames[c.Path] = c.OldPath
			basePaths = append(basePaths, c.OldPath)
			headPaths = append(headPaths, c.Path)
		case quality.FileStatusDeleted:
			basePaths = append(basePaths, c.Path)
		case quality.FileStatusAdded:
			headPaths = append(headPaths, c.Path)
		default: // modified
			basePaths = append(basePaths, c.Path)
			headPaths = append(headPaths, c.Path)
		}
	}
	// untouched.go was NOT reported by git as changed — proving AC9's own
	// property (only the delta's own files are ever read) requires it to
	// be absent from headPaths/basePaths, which it already is (git itself
	// never reported it).

	changedLines, err := g.ChangedLines(base, "HEAD")
	if err != nil {
		t.Fatalf("ChangedLines: %v", err)
	}

	ex := symbolExtractorAdapter{}
	baseSymbols, baseRefs, err := quality.CollectSymbols(g, base, basePaths, ex)
	if err != nil {
		t.Fatalf("CollectSymbols(base): %v", err)
	}
	headSymbols, headRefs, err := quality.CollectSymbols(g, "HEAD", headPaths, ex)
	if err != nil {
		t.Fatalf("CollectSymbols(HEAD): %v", err)
	}
	_ = baseRefs
	_ = headRefs

	delta := quality.DiffSymbols(baseSymbols, headSymbols, renames, changedLines, nil)

	assertContains := func(t *testing.T, syms []quality.Symbol, name string) {
		t.Helper()
		for _, s := range syms {
			if s.QualifiedName == name {
				return
			}
		}
		t.Errorf("expected %q in %+v", name, syms)
	}
	assertAbsent := func(t *testing.T, name string) {
		t.Helper()
		for _, s := range delta.Created {
			if s.QualifiedName == name {
				t.Errorf("%q must not appear in Created", name)
			}
		}
		for _, s := range delta.Modified {
			if s.QualifiedName == name {
				t.Errorf("%q must not appear in Modified", name)
			}
		}
		for _, s := range delta.Deleted {
			if s.QualifiedName == name {
				t.Errorf("%q must not appear in Deleted", name)
			}
		}
	}

	assertContains(t, delta.Created, "Created")
	assertContains(t, delta.Deleted, "ToBeDeleted")
	assertContains(t, delta.Modified, "Modified")
	assertAbsent(t, "Untouched")

	if len(delta.Moved) != 1 || delta.Moved[0].QualifiedName != "Moved" {
		t.Fatalf("Moved = %+v, want exactly one {Moved}", delta.Moved)
	}
	if delta.Moved[0].OldFile != "before.go" || delta.Moved[0].NewFile != "after.go" {
		t.Errorf("Moved = %+v, want OldFile=before.go NewFile=after.go", delta.Moved[0])
	}
}

// --- SPEC-118 P9: runBudgetChecks ---

// noopGraphFacts implements quality.GraphFacts reporting no edges, no
// reachability, and no candidates for anything — the "clean graph" fixture
// runBudgetChecks tests use when they only care about the git-side
// arithmetic (rows 1-6), never the graph query results.
type noopGraphFacts struct {
	contentHash map[string]string
}

func (f *noopGraphFacts) IncomingEdges(ref quality.SymbolRef) ([]quality.SymbolRef, error) {
	return nil, nil
}
func (f *noopGraphFacts) IncomingCalls(ref quality.SymbolRef) ([]quality.SymbolRef, error) {
	return nil, nil
}
func (f *noopGraphFacts) TestReachable(ref quality.SymbolRef, depth int, testGlobs []string) (bool, error) {
	return false, nil
}
func (f *noopGraphFacts) SameNameAndSignature(s quality.Symbol) ([]quality.SymbolRef, error) {
	return nil, nil
}
func (f *noopGraphFacts) IndexedContentHash(path string) (string, bool, error) {
	h, ok := f.contentHash[path]
	return h, ok, nil
}

// budgetTestFixture builds a real git repo with a base commit (one
// existing file, one function) and a HEAD commit that adds N new
// functions in newDir — the minimal shape TestRunBudgetChecks_* tables
// need, parameterised by how many symbols are "delivered".
func budgetTestFixture(t *testing.T, newDir string, newFuncCount int) (dir, base string) {
	t.Helper()
	dir = t.TempDir()
	gitRunBudgetTest(t, dir, "init", "-b", "main")
	gitRunBudgetTest(t, dir, "config", "user.email", "budget-test@example.com")
	gitRunBudgetTest(t, dir, "config", "user.name", "budget-test")
	gitRunBudgetTest(t, dir, "config", "commit.gpgsign", "false")

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	// PreExisting.go lives in BOTH commits, untouched — G10's own
	// regression fixture: a caller that collected symbols over the WHOLE
	// tree instead of the spec's own delta would wrongly see this symbol
	// as newly created at HEAD, since it was never told to look for it at
	// base either.
	if err := os.MkdirAll(filepath.Join(dir, "internal/untouched"), 0o755); err != nil {
		t.Fatalf("mkdir internal/untouched: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal/untouched/pre.go"), []byte("package untouched\n\nfunc PreExisting() int { return 1 }\n"), 0o644); err != nil {
		t.Fatalf("write pre.go: %v", err)
	}
	gitRunBudgetTest(t, dir, "add", ".")
	gitRunBudgetTest(t, dir, "commit", "-m", "base")
	base = strings.TrimSpace(gitRunBudgetTest(t, dir, "rev-parse", "HEAD"))

	if newDir != "" {
		if err := os.MkdirAll(filepath.Join(dir, newDir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", newDir, err)
		}
		var body strings.Builder
		body.WriteString("package fixture\n\n")
		for i := 0; i < newFuncCount; i++ {
			body.WriteString("func Created")
			body.WriteString(string(rune('A' + i)))
			body.WriteString("() int { return 1 }\n\n")
		}
		if err := os.WriteFile(filepath.Join(dir, newDir, "new.go"), []byte(body.String()), 0o644); err != nil {
			t.Fatalf("write new.go: %v", err)
		}
		gitRunBudgetTest(t, dir, "add", ".")
		gitRunBudgetTest(t, dir, "commit", "-m", "head")
	}
	return dir, base
}

// writeBudgetDoc writes budget.toml at the exact path specDocPath resolves
// for (workflowDir, project, id, kind=budget).
func writeBudgetDoc(t *testing.T, workflowDir, project, id, content string) {
	t.Helper()
	path, err := specDocPath(workflowDir, project, id, model.SpecDocKindBudget)
	if err != nil {
		t.Fatalf("specDocPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write budget.toml: %v", err)
	}
}

// writeMinimalBudgetConstitution writes a schema_version=4 constitution
// with everything declared-and-off except [budget].enabled=true — the
// minimal fixture TestQualityService_Status_ReportsBudgetInfo needs to
// drive a real Verify call through runBudgetChecks.
func writeMinimalBudgetConstitution(t *testing.T, repoDir string) {
	t.Helper()
	dir := filepath.Join(repoDir, ".mneme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir .mneme: %v", err)
	}
	doc := `
schema_version = 4
enabled = false
[execution]
output_tail_bytes = 4096
[coverage]
enabled = false
format = "go-cover"
command = ["true"]
profile_path = "tmp/coverage.out"
timeout = "20m"
min_diff_line_pct = 80.0
min_changed_lines = 5
exclude = []
[ratchet]
enabled = false
max_global_line_pct_drop = 0.0
max_baseline_staleness_pct = 1.0
[criteria]
enabled = false
timeout = "5m"
max_manual_pct = 25.0
max_command_pct = 30.0
[budget]
enabled = true
timeout = "2m"
test_globs = ["**/*_test.go"]
test_reach_depth = 3
`
	if err := os.WriteFile(filepath.Join(dir, "quality.toml"), []byte(doc), 0o644); err != nil {
		t.Fatalf("write quality.toml: %v", err)
	}
}

// findBudgetCheck locates a (kind, name) row in checks or fails the test —
// named distinctly from criteria_test.go's own findCheck(checks, kind,
// name) (no *testing.T parameter), so the two coexist without a signature
// clash.
func findBudgetCheck(t *testing.T, checks []*model.QualityCheck, kind, name string) *model.QualityCheck {
	t.Helper()
	for _, c := range checks {
		if c.Kind == kind && c.Name == name {
			return c
		}
	}
	t.Fatalf("no %s/%s row in %+v", kind, name, checks)
	return nil
}

// budgetTestConstitution returns a Constitution with [budget] declared and
// enabled, and the given test_globs/test_reach_depth.
func budgetTestConstitution() *quality.Constitution {
	return &quality.Constitution{
		SchemaVersion: 4, BudgetDeclared: true,
		Budget: quality.BudgetConfig{Enabled: true, TestReachDepth: 3, TestGlobs: []string{"**/*_test.go"}},
	}
}

// TestRunBudgetChecks_SkipReasons covers the uniform-skip causes: an
// earlier gate failure, workflowDir unset (D15, NEVER a fallback — G30),
// schema<4 (apagado por omision), and enabled=false (apagado por
// decision) — all 12 rows skipped, never silently omitted.
func TestRunBudgetChecks_SkipReasons(t *testing.T) {
	spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard}
	g := &quality.Git{RepoDir: t.TempDir()}

	tests := []struct {
		name         string
		svc          *QualityService
		constitution *quality.Constitution
		gatesStopped bool
	}{
		{
			name:         "gate cascade stopped",
			svc:          &QualityService{workflowDir: "/somewhere"},
			constitution: budgetTestConstitution(),
			gatesStopped: true,
		},
		{
			name:         "workflowDir unset (G30: no fallback)",
			svc:          &QualityService{workflowDir: ""},
			constitution: budgetTestConstitution(),
		},
		{
			name:         "schema < 4 (budget not declared)",
			svc:          &QualityService{workflowDir: "/somewhere"},
			constitution: &quality.Constitution{SchemaVersion: 3, BudgetDeclared: false},
		},
		{
			name:         "budget.enabled = false",
			svc:          &QualityService{workflowDir: "/somewhere"},
			constitution: &quality.Constitution{SchemaVersion: 4, BudgetDeclared: true, Budget: quality.BudgetConfig{Enabled: false}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checks, pure, err := tt.svc.runBudgetChecks(context.Background(), g, tt.constitution, spec, tt.gatesStopped)
			if err != nil {
				t.Fatalf("runBudgetChecks: %v", err)
			}
			if len(checks) != 12 || len(pure) != 12 {
				t.Fatalf("len(checks)=%d len(pure)=%d, want 12 each", len(checks), len(pure))
			}
			for _, c := range checks {
				if c.Status != "skipped" {
					t.Errorf("%s/%s status = %q, want skipped", c.Kind, c.Name, c.Status)
				}
			}
		})
	}
}

// TestRunBudgetChecks_BudgetDocMissing covers row 1 = fail, rows 2-12
// skipped, when budget.toml does not exist.
func TestRunBudgetChecks_BudgetDocMissing(t *testing.T) {
	workflowDir := t.TempDir()
	svc := &QualityService{workflowDir: workflowDir, graphFacts: &noopGraphFacts{}}
	spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard, BaseSHA: "deadbeef"}
	g := &quality.Git{RepoDir: t.TempDir()}

	checks, _, err := svc.runBudgetChecks(context.Background(), g, budgetTestConstitution(), spec, false)
	if err != nil {
		t.Fatalf("runBudgetChecks: %v", err)
	}
	declared := findBudgetCheck(t, checks, "budget", "declared")
	if declared.Status != "fail" {
		t.Errorf("budget/declared status = %q, want fail", declared.Status)
	}
	for _, c := range checks[1:] {
		if c.Status != "skipped" {
			t.Errorf("%s/%s status = %q, want skipped", c.Kind, c.Name, c.Status)
		}
	}
}

// TestRunBudgetChecks_BaseUnknown covers row 2 = finding "base-unknown",
// rows 3-12 skipped, when spec.BaseSHA is empty.
func TestRunBudgetChecks_BaseUnknown(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir, _ := budgetTestFixture(t, "", 0)
	workflowDir := t.TempDir()
	writeBudgetDoc(t, workflowDir, "wirvii/mneme", "SPEC-001", `
schema_version = 1
margin = 0
radius = ["**"]
`)
	svc := &QualityService{workflowDir: workflowDir, repoDir: dir, graphFacts: &noopGraphFacts{}}
	spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard, BaseSHA: ""}
	g := &quality.Git{RepoDir: dir}

	checks, _, err := svc.runBudgetChecks(context.Background(), g, budgetTestConstitution(), spec, false)
	if err != nil {
		t.Fatalf("runBudgetChecks: %v", err)
	}
	declared := findBudgetCheck(t, checks, "budget", "declared")
	if declared.Status != "pass" {
		t.Errorf("budget/declared status = %q, want pass", declared.Status)
	}
	symbolDelta := findBudgetCheck(t, checks, "budget", "symbol-delta")
	if symbolDelta.Status != "finding" || !strings.Contains(symbolDelta.Summary, "base-unknown") {
		t.Errorf("budget/symbol-delta = %+v, want finding naming base-unknown", symbolDelta)
	}
	for _, name := range []string{"graph-index", "revision"} {
		c := findBudgetCheck(t, checks, "budget", name)
		if c.Status != "skipped" {
			t.Errorf("budget/%s status = %q, want skipped", name, c.Status)
		}
	}
}

// TestRunBudgetChecks_G6_BothRefsAreDifferent is THE most important
// mutation guardian of the whole spec: with a real 2-commit repository
// where HEAD created 3 new symbols against a quota of 1 (margin 0), the
// certificate MUST fail — proving the delta was computed against the
// spec's actual base, not HEAD twice (which would make the delta empty
// and everything pass).
func TestRunBudgetChecks_G6_BothRefsAreDifferent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir, base := budgetTestFixture(t, "internal/x", 3)
	workflowDir := t.TempDir()
	writeBudgetDoc(t, workflowDir, "wirvii/mneme", "SPEC-001", `
schema_version = 1
margin = 0
radius = ["**"]

[[quota]]
dir = "internal/x"
max_new_symbols = 1
`)
	svc := &QualityService{workflowDir: workflowDir, repoDir: dir, graphFacts: &noopGraphFacts{}}
	spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard, BaseSHA: base}
	g := &quality.Git{RepoDir: dir}

	checks, _, err := svc.runBudgetChecks(context.Background(), g, budgetTestConstitution(), spec, false)
	if err != nil {
		t.Fatalf("runBudgetChecks: %v", err)
	}

	symbolDelta := findBudgetCheck(t, checks, "budget", "symbol-delta")
	if symbolDelta.Status != "pass" {
		t.Fatalf("budget/symbol-delta = %+v, want pass (base is knowable)", symbolDelta)
	}
	if !strings.Contains(symbolDelta.Detail, `"created":3`) {
		t.Errorf("budget/symbol-delta detail = %q, want created:3 (3 new functions since base)", symbolDelta.Detail)
	}

	unbudgeted := findBudgetCheck(t, checks, "detection", "unbudgeted")
	if unbudgeted.Status != "fail" {
		t.Errorf("detection/unbudgeted status = %q, want fail (3 delivered against quota 1, margin 0)", unbudgeted.Status)
	}
}

// TestRunBudgetChecks_G10_UntouchedFileNeverCollected is the regression
// budgetTestFixture's PreExisting.go file exists to catch (G10): a symbol
// living in a file that was NEVER part of the spec's own delta must never
// appear in Created (or any bucket at all) — proving basePaths/headPaths
// come exclusively from the delta, never from a whole-tree listing.
func TestRunBudgetChecks_G10_UntouchedFileNeverCollected(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir, base := budgetTestFixture(t, "internal/x", 1)
	workflowDir := t.TempDir()
	writeBudgetDoc(t, workflowDir, "wirvii/mneme", "SPEC-001", `
schema_version = 1
margin = 5
radius = ["**"]

[[quota]]
dir = "internal/x"
max_new_symbols = 5
`)
	svc := &QualityService{workflowDir: workflowDir, repoDir: dir, graphFacts: &noopGraphFacts{}}
	spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard, BaseSHA: base}
	g := &quality.Git{RepoDir: dir}

	checks, _, err := svc.runBudgetChecks(context.Background(), g, budgetTestConstitution(), spec, false)
	if err != nil {
		t.Fatalf("runBudgetChecks: %v", err)
	}
	symbolDelta := findBudgetCheck(t, checks, "budget", "symbol-delta")
	if !strings.Contains(symbolDelta.Detail, `"created":1`) {
		t.Errorf("budget/symbol-delta detail = %q, want created:1 (PreExisting.go must never be collected)", symbolDelta.Detail)
	}
}

// TestRunBudgetChecks_G22_OneRowPerDetection covers D8/AC17/G22: with THREE
// orphaned symbols (all created, all with zero incoming edges under
// noopGraphFacts), there must be EXACTLY ONE "detection/orphan" row, never
// one per subject — the count lives in Summary/Detail, not in row count.
func TestRunBudgetChecks_G22_OneRowPerDetection(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir, base := budgetTestFixture(t, "internal/x", 3)
	workflowDir := t.TempDir()
	writeBudgetDoc(t, workflowDir, "wirvii/mneme", "SPEC-001", `
schema_version = 1
margin = 5
radius = ["**"]

[[quota]]
dir = "internal/x"
max_new_symbols = 5
`)
	spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard, BaseSHA: base}
	g := &quality.Git{RepoDir: dir}

	// A FRESH graph is required to reach the detection loop at all (rows
	// 7-12 are skipped otherwise) — compute the real HEAD hash of the one
	// changed file so noopGraphFacts reports it fresh.
	content, ok, ferr := g.FileAtRef("HEAD", "internal/x/new.go")
	if ferr != nil || !ok {
		t.Fatalf("FileAtRef: ok=%v err=%v", ok, ferr)
	}
	sum := sha256.Sum256(content)
	facts := &noopGraphFacts{contentHash: map[string]string{"internal/x/new.go": hex.EncodeToString(sum[:])}}
	svc := &QualityService{workflowDir: workflowDir, repoDir: dir, graphFacts: facts}

	checks, _, err := svc.runBudgetChecks(context.Background(), g, budgetTestConstitution(), spec, false)
	if err != nil {
		t.Fatalf("runBudgetChecks: %v", err)
	}

	orphanRows := 0
	for _, c := range checks {
		if c.Kind == "detection" && c.Name == "orphan" {
			orphanRows++
		}
	}
	if orphanRows != 1 {
		t.Fatalf("orphan rows = %d, want exactly 1 (D8's own rule: one row per detection, not per subject)", orphanRows)
	}
	orphan := findBudgetCheck(t, checks, "detection", "orphan")
	if !strings.Contains(orphan.Detail, `"count":3`) {
		t.Errorf("detection/orphan detail = %q, want count:3", orphan.Detail)
	}
}

// TestRunBudgetChecks_OutOfRadius covers row 6: a changed file outside the
// declared radius fails, independent of the margin (G14).
func TestRunBudgetChecks_OutOfRadius(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir, base := budgetTestFixture(t, "internal/x", 1)
	workflowDir := t.TempDir()
	writeBudgetDoc(t, workflowDir, "wirvii/mneme", "SPEC-001", `
schema_version = 1
margin = 5
radius = ["internal/other/**"]

[[quota]]
dir = "internal/x"
max_new_symbols = 5
`)
	svc := &QualityService{workflowDir: workflowDir, repoDir: dir, graphFacts: &noopGraphFacts{}}
	spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard, BaseSHA: base}
	g := &quality.Git{RepoDir: dir}

	checks, _, err := svc.runBudgetChecks(context.Background(), g, budgetTestConstitution(), spec, false)
	if err != nil {
		t.Fatalf("runBudgetChecks: %v", err)
	}
	outOfRadius := findBudgetCheck(t, checks, "detection", "out-of-radius")
	if outOfRadius.Status != "fail" {
		t.Errorf("detection/out-of-radius status = %q, want fail (margin=5 must not save it, G14)", outOfRadius.Status)
	}
	unbudgeted := findBudgetCheck(t, checks, "detection", "unbudgeted")
	if unbudgeted.Status != "pass" {
		t.Errorf("detection/unbudgeted status = %q, want pass (1 delivered against quota 5)", unbudgeted.Status)
	}
}

// TestRunBudgetChecks_GraphFreshness covers AC18's shape end to end: nil
// graphFacts -> row 3 finding, six graph rows skipped, never pass (G21);
// a fresh graph (all content hashes matching HEAD) -> row 3 pass, six
// graph rows evaluated (pass, since noopGraphFacts reports no edges at
// all — an orphan every time, but never a fail).
func TestRunBudgetChecks_GraphFreshness(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir, base := budgetTestFixture(t, "internal/x", 1)
	workflowDir := t.TempDir()
	writeBudgetDoc(t, workflowDir, "wirvii/mneme", "SPEC-001", `
schema_version = 1
margin = 5
radius = ["**"]

[[quota]]
dir = "internal/x"
max_new_symbols = 5
`)
	spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard, BaseSHA: base}
	g := &quality.Git{RepoDir: dir}

	t.Run("nil graphFacts", func(t *testing.T) {
		svc := &QualityService{workflowDir: workflowDir, repoDir: dir, graphFacts: nil}
		checks, _, err := svc.runBudgetChecks(context.Background(), g, budgetTestConstitution(), spec, false)
		if err != nil {
			t.Fatalf("runBudgetChecks: %v", err)
		}
		graphIndex := findBudgetCheck(t, checks, "budget", "graph-index")
		if graphIndex.Status != "finding" {
			t.Errorf("budget/graph-index status = %q, want finding", graphIndex.Status)
		}
		for _, name := range graphDetectionNames {
			c := findBudgetCheck(t, checks, "detection", name)
			if c.Status != "skipped" {
				t.Errorf("detection/%s status = %q, want skipped (never pass, G21)", name, c.Status)
			}
		}
	})

	t.Run("fresh graph", func(t *testing.T) {
		// Compute the real HEAD content hash for the one new file so
		// noopGraphFacts reports it as indexed and matching.
		content, ok, err := g.FileAtRef("HEAD", "internal/x/new.go")
		if err != nil || !ok {
			t.Fatalf("FileAtRef: ok=%v err=%v", ok, err)
		}
		sum := sha256.Sum256(content)
		facts := &noopGraphFacts{contentHash: map[string]string{"internal/x/new.go": hex.EncodeToString(sum[:])}}
		svc := &QualityService{workflowDir: workflowDir, repoDir: dir, graphFacts: facts}

		checks, _, err := svc.runBudgetChecks(context.Background(), g, budgetTestConstitution(), spec, false)
		if err != nil {
			t.Fatalf("runBudgetChecks: %v", err)
		}
		graphIndex := findBudgetCheck(t, checks, "budget", "graph-index")
		if graphIndex.Status != "pass" {
			t.Errorf("budget/graph-index status = %q, want pass", graphIndex.Status)
		}
		// noopGraphFacts reports ZERO incoming edges and ZERO test
		// reachability for everything — which is the CORRECT, honest
		// answer for a genuinely new symbol with no callers indexed yet:
		// orphan and untested-reach must fire (there really are no
		// callers), while test-only/dead/single-use-indirection/
		// reinvention all require at least one candidate edge to have
		// anything to report, so they stay pass.
		wantStatus := map[string]string{
			"orphan":                 "finding",
			"test-only":              "pass",
			"dead":                   "pass",
			"single-use-indirection": "pass",
			"reinvention":            "pass",
			"untested-reach":         "finding",
		}
		for _, name := range graphDetectionNames {
			c := findBudgetCheck(t, checks, "detection", name)
			if c.Status != wantStatus[name] {
				t.Errorf("detection/%s status = %q, want %q", name, c.Status, wantStatus[name])
			}
		}
	})
}

// TestQualityService_Status_ReportsBudgetInfo covers D16: quality_status
// pairs budget.toml's CURRENT disk hash with the hash the latest
// certificate's own budget/declared row recorded, plus the last certified
// figures (margin/budgeted/delivered/overrun) from detection/unbudgeted —
// the window closed with a row, not an argument against
// CertificateUsable.
func TestQualityService_Status_ReportsBudgetInfo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir, base := budgetTestFixture(t, "internal/x", 1)
	workflowDir := t.TempDir()
	docContent := `
schema_version = 1
margin = 5
radius = ["**"]

[[quota]]
dir = "internal/x"
max_new_symbols = 5
`
	writeBudgetDoc(t, workflowDir, "wirvii/mneme", "SPEC-001", docContent)
	writeMinimalBudgetConstitution(t, dir)
	gitRunBudgetTest(t, dir, "add", ".")
	gitRunBudgetTest(t, dir, "commit", "-m", "add constitution")

	s := newTestQualityStore(t)
	spec := insertTestSpec(t, s, "SPEC-001", "wirvii/mneme", model.SpecStatusImplementing, base)

	svc := NewQualityService(s, "wirvii/mneme", dir, &fakeGateRunner{}, WithWorkflowDir(workflowDir), WithGraphFacts(&noopGraphFacts{}))
	if _, err := svc.Verify(context.Background(), model.QualityVerifyRequest{ID: spec.ID}); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	resp, err := svc.Status(context.Background(), model.QualityStatusRequest{ID: spec.ID})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if resp.Budget == nil {
		t.Fatal("resp.Budget = nil, want populated")
	}
	if resp.Budget.DiskHash == "" || resp.Budget.CertificateHash == "" {
		t.Errorf("Budget = %+v, want both hashes populated", resp.Budget)
	}
	if resp.Budget.DiskHash != resp.Budget.CertificateHash {
		t.Errorf("Budget.DiskHash (%s) != Budget.CertificateHash (%s), want equal (document unchanged since certification)",
			resp.Budget.DiskHash, resp.Budget.CertificateHash)
	}
	if resp.Budget.Delivered != 1 {
		t.Errorf("Budget.Delivered = %d, want 1", resp.Budget.Delivered)
	}
}

// TestRunBudgetChecks_Revision covers row 4: no [revision] -> pass; a
// present [revision] -> finding, with the three figures and the
// declaration verbatim in Detail.
func TestRunBudgetChecks_Revision(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir, base := budgetTestFixture(t, "internal/x", 1)
	workflowDir := t.TempDir()
	writeBudgetDoc(t, workflowDir, "wirvii/mneme", "SPEC-001", `
schema_version = 1
margin = 5
radius = ["**"]

[[quota]]
dir = "internal/x"
max_new_symbols = 5

[revision]
by = "architect"
at = "2026-08-14T09:12:00Z"
rationale = "wiring exigio mas simbolos"
margin = 6
  [[revision.quota]]
  dir = "internal/x"
  max_new_symbols = 7
`)
	svc := &QualityService{workflowDir: workflowDir, repoDir: dir, graphFacts: &noopGraphFacts{}}
	spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard, BaseSHA: base}
	g := &quality.Git{RepoDir: dir}

	checks, _, err := svc.runBudgetChecks(context.Background(), g, budgetTestConstitution(), spec, false)
	if err != nil {
		t.Fatalf("runBudgetChecks: %v", err)
	}
	revision := findBudgetCheck(t, checks, "budget", "revision")
	if revision.Status != "finding" {
		t.Errorf("budget/revision status = %q, want finding", revision.Status)
	}
	if !strings.Contains(revision.Detail, "architect") || !strings.Contains(revision.Detail, "wiring exigio mas simbolos") {
		t.Errorf("budget/revision detail = %q, want it to name the revision verbatim", revision.Detail)
	}
	// AC12/G13: "revised" is a NUMBER (the revised quota total, 7) now
	// that a revision with its own [[revision.quota]] exists.
	if !strings.Contains(revision.Detail, `"revised":7`) {
		t.Errorf("budget/revision detail = %q, want \"revised\":7 (the revised quota total)", revision.Detail)
	}
}

// TestRunBudgetChecks_Revision_NoneIsRevisedNull covers AC12's first row:
// without [revision], the detail's "revised" key is present as JSON null
// — never omitted (G13) — so a caller can tell "no revision" from "a
// revision this parser somehow failed to read".
func TestRunBudgetChecks_Revision_NoneIsRevisedNull(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir, base := budgetTestFixture(t, "internal/x", 1)
	workflowDir := t.TempDir()
	writeBudgetDoc(t, workflowDir, "wirvii/mneme", "SPEC-001", `
schema_version = 1
margin = 5
radius = ["**"]

[[quota]]
dir = "internal/x"
max_new_symbols = 5
`)
	svc := &QualityService{workflowDir: workflowDir, repoDir: dir, graphFacts: &noopGraphFacts{}}
	spec := &model.Spec{ID: "SPEC-001", Project: "wirvii/mneme", Lane: model.LaneStandard, BaseSHA: base}
	g := &quality.Git{RepoDir: dir}

	checks, _, err := svc.runBudgetChecks(context.Background(), g, budgetTestConstitution(), spec, false)
	if err != nil {
		t.Fatalf("runBudgetChecks: %v", err)
	}
	revision := findBudgetCheck(t, checks, "budget", "revision")
	if revision.Status != "pass" {
		t.Errorf("budget/revision status = %q, want pass", revision.Status)
	}
	if !strings.Contains(revision.Detail, `"revised":null`) {
		t.Errorf("budget/revision detail = %q, want the literal key \"revised\":null present", revision.Detail)
	}
}
