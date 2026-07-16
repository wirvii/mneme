package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
)

// insertTestManifest inserts a subagents/manifest memory row into database
// with the given raw JSON content, mirroring insertTestRule's helper style
// (hook_pre_tool_use_test.go). project is required (SPEC-084 D4/D6):
// manifestQuery filters on it, so a row inserted without one would never be
// found by queryManifestContent. id is caller-supplied (rather than a fixed
// "m1", also mirroring insertTestRule) so a single test can insert more than
// one manifest row without violating the memories primary key — needed by
// the A2 regression test below, which inserts two rows sharing a topic_key.
func insertTestManifest(database *db.DB, id, project, content string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := database.Exec(
		`INSERT INTO memories
		 (id, type, scope, topic_key, project, title, content, created_at, updated_at, importance, confidence, decay_rate)
		 VALUES (?, 'config', 'project', ?, ?, 'Subagent manifest', ?, ?, ?, 0.9, 0.8, 0.02)`,
		id, manifestTopicKey, project, content, now, now,
	)
	return err
}

// --- queryManifestContent (D8 last row: DB-level fail-open / not-found) ----

// TestQueryManifestContent_Found verifies the happy path: a manifest row
// present in the DB is returned verbatim.
func TestQueryManifestContent_Found(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	want := `[{"role":"backend","areas":["internal/**"]}]`
	if insertErr := insertTestManifest(database, "m1", "test/project", want); insertErr != nil {
		t.Fatalf("insertTestManifest: %v", insertErr)
	}
	database.Close()

	content, found, err := queryManifestContent(dbPath, "test/project")
	if err != nil {
		t.Fatalf("queryManifestContent: %v", err)
	}
	if !found {
		t.Fatal("found = false, want true")
	}
	if content != want {
		t.Errorf("content = %q, want %q", content, want)
	}
}

// TestQueryManifestContent_NoRow verifies that an existing, empty database
// (no manifest memory saved) reports found=false with a nil error — the D8
// "manifest absent" branch, not a hard error.
func TestQueryManifestContent_NoRow(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	database.Close()

	_, found, err := queryManifestContent(dbPath, "test/project")
	if err != nil {
		t.Fatalf("queryManifestContent: %v", err)
	}
	if found {
		t.Error("found = true, want false")
	}
}

// TestQueryManifestContent_DBFileMissing verifies that a non-existent
// database file reports found=false with a nil error, matching
// queryRulesFromDB's "new project" convention.
func TestQueryManifestContent_DBFileMissing(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "does-not-exist.db")

	_, found, err := queryManifestContent(dbPath, "test/project")
	if err != nil {
		t.Fatalf("queryManifestContent: %v", err)
	}
	if found {
		t.Error("found = true, want false")
	}
}

// TestQueryManifestContent_CorruptDBFile verifies AC9's fail-open branch: a
// file that exists but is not a valid SQLite database produces a non-nil
// error (which runHookPathOwned/resolvePathOwnership callers must treat as
// ALLOW, per D8's last row).
func TestQueryManifestContent_CorruptDBFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "corrupt.db")
	if err := os.WriteFile(dbPath, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatalf("write corrupt db: %v", err)
	}

	_, _, err := queryManifestContent(dbPath, "test/project")
	if err == nil {
		t.Fatal("expected a hard error for a corrupt database file, got nil")
	}
}

// TestQueryManifestContent_ScopedToProject is the A2 regression test
// (SPEC-084 D6): two manifest rows share the same topic_key but belong to
// different projects — reproducing the real contamination found in
// wirvii-mneme.db, where test runs and the real project coexist in one
// database. Before the SPEC-084 fix, manifestQuery had no project filter and
// could return either row nondeterministically (in practice, whichever the
// query planner picked first); after the fix it must always return the row
// matching the requested project.
func TestQueryManifestContent_ScopedToProject(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	other := `[{"role":"backend","areas":["apps/core-srv"]}]`
	want := `[{"role":"backend","areas":["internal/**"]}]`
	if insertErr := insertTestManifest(database, "m-other", "test/project", other); insertErr != nil {
		t.Fatalf("insertTestManifest (other project): %v", insertErr)
	}
	if insertErr := insertTestManifest(database, "m-real", "wirvii/mneme", want); insertErr != nil {
		t.Fatalf("insertTestManifest (real project): %v", insertErr)
	}
	database.Close()

	content, found, err := queryManifestContent(dbPath, "wirvii/mneme")
	if err != nil {
		t.Fatalf("queryManifestContent: %v", err)
	}
	if !found {
		t.Fatal("found = false, want true")
	}
	if content != want {
		t.Errorf("content = %q, want %q (got the other project's manifest instead)", content, want)
	}
}

// --- resolvePathOwnership (D7/D8 decision table) ----------------------------

const testCWD = "/repo"

// TestResolvePathOwnership_BlockedByImplementer covers AC7: a path matching
// an implementer's area is blocked, reporting that role.
func TestResolvePathOwnership_BlockedByImplementer(t *testing.T) {
	manifest := `[{"role":"backend","areas":["internal/**"]}]`

	got := resolvePathOwnership("internal/foo.go", testCWD, true, manifest)

	if got.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2", got.ExitCode)
	}
	if got.Owner != "backend" {
		t.Errorf("Owner = %q, want backend", got.Owner)
	}
}

// TestResolvePathOwnership_AllowedWhenNotOwned covers AC8: a path outside
// every implementer's areas is allowed.
func TestResolvePathOwnership_AllowedWhenNotOwned(t *testing.T) {
	manifest := `[{"role":"backend","areas":["internal/**"]}]`

	got := resolvePathOwnership("README.md", testCWD, true, manifest)

	if got.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", got.ExitCode)
	}
}

// TestResolvePathOwnership_ManifestAbsent_LegacyBlock covers AC9's first
// half: no manifest row at all blocks with owner "legacy".
func TestResolvePathOwnership_ManifestAbsent_LegacyBlock(t *testing.T) {
	got := resolvePathOwnership("internal/foo.go", testCWD, false, "")

	if got.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2", got.ExitCode)
	}
	if got.Owner != "legacy" {
		t.Errorf("Owner = %q, want legacy", got.Owner)
	}
}

// TestResolvePathOwnership_ManifestEmptyArray_LegacyBlock verifies D8's
// "manifest present but empty []" row also blocks as legacy.
func TestResolvePathOwnership_ManifestEmptyArray_LegacyBlock(t *testing.T) {
	got := resolvePathOwnership("internal/foo.go", testCWD, true, "[]")

	if got.ExitCode != 2 || got.Owner != "legacy" {
		t.Errorf("got %+v, want ExitCode=2 Owner=legacy", got)
	}
}

// TestResolvePathOwnership_CorruptManifestJSON_FailOpen covers AC9's second
// half: a manifest row whose content fails to parse as JSON fails open.
func TestResolvePathOwnership_CorruptManifestJSON_FailOpen(t *testing.T) {
	got := resolvePathOwnership("internal/foo.go", testCWD, true, "{not valid json")

	if got.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0 (fail-open on corrupt manifest JSON)", got.ExitCode)
	}
}

// TestResolvePathOwnership_OutOfTreePath_Allow verifies D7 point 4: a path
// outside the project tree cannot be owned, so it is allowed even with a
// matching-looking manifest.
func TestResolvePathOwnership_OutOfTreePath_Allow(t *testing.T) {
	manifest := `[{"role":"backend","areas":["**"]}]`

	got := resolvePathOwnership("/etc/passwd", testCWD, true, manifest)

	if got.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0 for an out-of-tree path", got.ExitCode)
	}
}

// TestResolvePathOwnership_NonImplementerRoleIgnored verifies D7 point 3:
// only implementer roles (subagents.IsImplementer) can own a path — an
// architect-only manifest never blocks, even with a matching area.
func TestResolvePathOwnership_NonImplementerRoleIgnored(t *testing.T) {
	manifest := `[{"role":"architect","areas":["internal/**"]}]`

	got := resolvePathOwnership("internal/foo.go", testCWD, true, manifest)

	if got.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0 — architect is not an implementer role", got.ExitCode)
	}
}

// TestResolvePathOwnership_OverlapReportsFirstManifestMatch verifies D7
// point 6: when multiple implementer entries could own the path, the
// reported owner is the first match in manifest order — overlap never
// changes the block decision, only which role is named.
func TestResolvePathOwnership_OverlapReportsFirstManifestMatch(t *testing.T) {
	manifest := `[
		{"role":"frontend","areas":["internal/**"]},
		{"role":"backend","areas":["internal/**"]}
	]`

	got := resolvePathOwnership("internal/foo.go", testCWD, true, manifest)

	if got.ExitCode != 2 || got.Owner != "frontend" {
		t.Errorf("got %+v, want ExitCode=2 Owner=frontend (first manifest match)", got)
	}
}

// TestResolvePathOwnership_AbsolutePathUnderCWD verifies R4: an absolute
// path inside the project tree normalises the same as its relative form.
func TestResolvePathOwnership_AbsolutePathUnderCWD(t *testing.T) {
	manifest := `[{"role":"backend","areas":["internal/**"]}]`

	got := resolvePathOwnership(filepath.Join(testCWD, "internal", "foo.go"), testCWD, true, manifest)

	if got.ExitCode != 2 || got.Owner != "backend" {
		t.Errorf("got %+v, want ExitCode=2 Owner=backend for an absolute in-tree path", got)
	}
}

// --- runHookPathOwned wrapper (ALLOW branches only — see comment) ----------
//
// Any branch of runHookPathOwned that reaches ExitCode==2 calls os.Exit(2),
// which would kill the test binary — exactly the same constraint that keeps
// runHookPreToolUse's block path tested only through its components
// (queryRulesFromDB + rules.Match + renderPreToolUseOutput), never
// end-to-end (see hook_pre_tool_use_test.go). AC7-AC9's exit-code/stdout
// contract is covered directly by TestResolvePathOwnership_* and
// TestQueryManifestContent_* above. The tests below only exercise
// runHookPathOwned's ALLOW paths, isolating $HOME (via t.Setenv) so they
// never touch the real ~/.mneme databases.

// chdirTemp changes the working directory to dir for the duration of the
// test and restores the original directory in a cleanup func.
func chdirTemp(t *testing.T, dir string) {
	t.Helper()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if chErr := os.Chdir(dir); chErr != nil {
		t.Fatalf("chdir: %v", chErr)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
}

// TestRunHookPathOwned_UnreadableConfig_AllowsWithoutExit verifies the D8
// hard-error fail-open branch closest to the process boundary: when
// config.Load(config.DefaultPath()) itself fails (malformed TOML), the
// function must return nil without ever reaching the manifest query, and
// therefore never call os.Exit.
func TestRunHookPathOwned_UnreadableConfig_AllowsWithoutExit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".mneme"), 0o755); err != nil {
		t.Fatalf("mkdir .mneme: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".mneme", "config.toml"), []byte("not = [valid toml"), 0o600); err != nil {
		t.Fatalf("write malformed config.toml: %v", err)
	}

	if runErr := runHookPathOwned("internal/foo.go"); runErr != nil {
		t.Fatalf("runHookPathOwned returned error: %v", runErr)
	}
}

// TestRunHookPathOwned_ManifestExistsButPathNotOwned_AllowsWithoutExit
// exercises the full runHookPathOwned wiring (config, project detection,
// manifest DB read, ownership match) end-to-end for the ALLOW case: a real
// manifest exists for the detected project, but it does not own the target
// path (AC8, exercised through the actual CLI entrypoint rather than only
// through resolvePathOwnership).
func TestRunHookPathOwned_ManifestExistsButPathNotOwned_AllowsWithoutExit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	repoDir := t.TempDir()
	initGitRepo(t, repoDir)
	chdirTemp(t, repoDir)

	slug := strings.ToLower(filepath.Base(repoDir))
	dbPath := filepath.Join(home, ".mneme", "projects", slug+".db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("mkdir projects dir: %v", err)
	}
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if insertErr := insertTestManifest(database, "m1", slug, `[{"role":"backend","areas":["internal/**"]}]`); insertErr != nil {
		t.Fatalf("insertTestManifest: %v", insertErr)
	}
	database.Close()

	if runErr := runHookPathOwned("README.md"); runErr != nil {
		t.Fatalf("runHookPathOwned returned error: %v", runErr)
	}
}

// --- AC11: guardian tests against internal/service drift -------------------

// TestManifestTopicKey_MatchesService guards against drift between the
// hook's locally-defined manifestTopicKey (SPEC-068 D13, kept local to avoid
// importing internal/service into a per-tool-call hook) and the real
// service.SubagentManifestTopicKey constant.
func TestManifestTopicKey_MatchesService(t *testing.T) {
	if manifestTopicKey != service.SubagentManifestTopicKey {
		t.Errorf("manifestTopicKey = %q, want %q (service.SubagentManifestTopicKey)", manifestTopicKey, service.SubagentManifestTopicKey)
	}
}

// TestManifestQuery_ScopeMatchesSaveManifest is the SPEC-084 D6 guardian: it
// inspects the real manifestQuery constant for the `scope = 'project'`
// clause and pins that clause to model.ScopeProject — the value SaveManifest
// (internal/service/subagents.go) actually writes on every manifest save.
// This reads the production constant itself (not a local literal standing
// in for it), so it fails if manifestQuery's scope clause ever drifts from
// what SaveManifest writes — instead of manifestQuery silently filtering
// out every real manifest row (found=false, D8's legacy-block branch —
// indistinguishable from "no manifest" without this guard).
func TestManifestQuery_ScopeMatchesSaveManifest(t *testing.T) {
	wantClause := "scope = '" + string(model.ScopeProject) + "'"
	if !strings.Contains(manifestQuery, wantClause) {
		t.Errorf("manifestQuery = %q, want it to contain %q (model.ScopeProject, what SaveManifest writes)",
			manifestQuery, wantClause)
	}
}

// TestHookManifestEntry_RoundTripsServiceManifestEntry guards against shape
// drift: a real service.ManifestEntry (as persisted by SaveManifest, the
// shape path-owned actually reads off disk) must deserialise into
// hookManifestEntry with Role and Areas intact.
func TestHookManifestEntry_RoundTripsServiceManifestEntry(t *testing.T) {
	full := []service.ManifestEntry{
		{
			Role:          "qa-tester",
			Path:          "/repo/.claude/agents/qa-tester.md",
			Areas:         []string{"internal/**", "cmd/**"},
			Archetype:     "bug-hunter",
			AreasComplete: true,
		},
	}
	data, err := json.Marshal(full)
	if err != nil {
		t.Fatalf("marshal service.ManifestEntry: %v", err)
	}

	var lite []hookManifestEntry
	if err := json.Unmarshal(data, &lite); err != nil {
		t.Fatalf("unmarshal into hookManifestEntry: %v", err)
	}

	if len(lite) != 1 {
		t.Fatalf("len(lite) = %d, want 1", len(lite))
	}
	if lite[0].Role != string(full[0].Role) {
		t.Errorf("Role = %q, want %q", lite[0].Role, full[0].Role)
	}
	if strings.Join(lite[0].Areas, ",") != strings.Join(full[0].Areas, ",") {
		t.Errorf("Areas = %v, want %v", lite[0].Areas, full[0].Areas)
	}
	if lite[0].Archetype != string(full[0].Archetype) {
		t.Errorf("Archetype = %q, want %q", lite[0].Archetype, full[0].Archetype)
	}
	if lite[0].AreasComplete != full[0].AreasComplete {
		t.Errorf("AreasComplete = %v, want %v", lite[0].AreasComplete, full[0].AreasComplete)
	}
}

// --- SPEC-086 D4: archetype-aware implementer lookup (the real bug fix) ----

// TestResolvePathOwnership_CustomRoleArchetypeIsProtected is the mutation-
// tested reproduction of AC11: a manifest entry whose Role is a custom name
// ("qa-tester") but whose Archetype is an implementer archetype
// ("bug-hunter") must have its declared area protected. Before D4, this
// entry was invisible to resolvePathOwnership's subagents.IsImplementer(Role)
// lookup — deleting the archetype-aware isImplementer() (falling back to
// comparing Role directly against PermissionTable) reproduces the bug and
// turns this test red.
func TestResolvePathOwnership_CustomRoleArchetypeIsProtected(t *testing.T) {
	manifest := `[{"role":"qa-tester","archetype":"bug-hunter","areas":["internal/**"]}]`

	got := resolvePathOwnership("internal/foo.go", testCWD, true, manifest)

	if got.ExitCode != 2 || got.Owner != "qa-tester" {
		t.Errorf("got %+v, want ExitCode=2 Owner=qa-tester — archetype bug-hunter must be recognised as an implementer", got)
	}
}

// TestResolvePathOwnership_OldManifestEntryWithoutArchetype_StillProtected
// covers the compat fallback: an entry with no archetype field at all
// (every manifest written before SPEC-086) falls back to Role, so a plain
// "backend" entry is unaffected — zero migration required (AC9).
func TestResolvePathOwnership_OldManifestEntryWithoutArchetype_StillProtected(t *testing.T) {
	manifest := `[{"role":"backend","areas":["internal/**"]}]`

	got := resolvePathOwnership("internal/foo.go", testCWD, true, manifest)

	if got.ExitCode != 2 || got.Owner != "backend" {
		t.Errorf("got %+v, want ExitCode=2 Owner=backend", got)
	}
}

// --- areaMatches / cleanArea (SPEC-084 D1/D2) -------------------------------

// TestAreaMatches_Table covers D2's table (bare dir, already-glob
// idempotency, wildcard dir, exact file) plus the empirical anchor pairs
// from the diagnosis memory (SPEC-084 §1 table A1): the repro that showed a
// bare directory area never matches anything inside it.
func TestAreaMatches_Table(t *testing.T) {
	tests := []struct {
		name string
		area string
		path string
		want bool
	}{
		{
			name: "bare dir matches descendant (A1 empirical anchor)",
			area: "apps/web-ui",
			path: "apps/web-ui/lib/version.ts",
			want: true,
		},
		{
			name: "bare dir matches itself",
			area: "apps/web-ui",
			path: "apps/web-ui",
			want: true,
		},
		{
			name: "already-glob area is idempotent (A1 empirical anchor)",
			area: "internal/**",
			path: "internal/service/foo.go",
			want: true,
		},
		{
			name: "wildcard dir matches descendant",
			area: "apps/*-ui",
			path: "apps/web-ui/lib/version.ts",
			want: true,
		},
		{
			name: "exact file matches",
			area: "cmd/mneme/main.go",
			path: "cmd/mneme/main.go",
			want: true,
		},
		{
			name: "unrelated path does not match",
			area: "apps/web-ui",
			path: "internal/service/foo.go",
			want: false,
		},
		{
			name: "sibling directory with shared prefix does not match",
			area: "apps/web-ui",
			path: "apps/web-ui-admin/lib/foo.ts",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := areaMatches(tt.area, tt.path)
			if got != tt.want {
				t.Errorf("areaMatches(%q, %q) = %v, want %v", tt.area, tt.path, got, tt.want)
			}
		})
	}
}

// TestAreaMatches_DegenerateAreas covers D2's normative edge cases: an empty
// or whitespace-only area must never own anything (R3 — it must never
// silently become "**" and block the entire repo), while "." and "./"
// explicitly do own the whole tree.
func TestAreaMatches_DegenerateAreas(t *testing.T) {
	tests := []struct {
		name string
		area string
		path string
		want bool
	}{
		{"empty area never matches", "", "internal/foo.go", false},
		{"whitespace-only area never matches", "   ", "internal/foo.go", false},
		{"dot area owns the whole tree", ".", "internal/foo.go", true},
		{"dot-slash area owns the whole tree", "./", "internal/foo.go", true},
		{"leading dot-slash is stripped before matching", "./internal", "internal/foo.go", true},
		{"trailing slash is stripped before matching", "internal/", "internal/foo.go", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := areaMatches(tt.area, tt.path)
			if got != tt.want {
				t.Errorf("areaMatches(%q, %q) = %v, want %v", tt.area, tt.path, got, tt.want)
			}
		})
	}
}

// TestCleanArea covers cleanArea directly, isolating the normalisation step
// from the glob match so a future areaMatches change can't silently mask a
// cleaning regression.
func TestCleanArea(t *testing.T) {
	tests := []struct {
		name        string
		area        string
		wantCleaned string
		wantIgnore  bool
	}{
		{"empty", "", "", true},
		{"whitespace only", "   ", "", true},
		{"dot", ".", "**", false},
		{"dot slash", "./", "**", false},
		{"bare dir", "apps/web-ui", "apps/web-ui", false},
		{"bare dir trailing slash", "apps/web-ui/", "apps/web-ui", false},
		{"leading dot slash", "./apps/web-ui", "apps/web-ui", false},
		{"already a glob", "internal/**", "internal/**", false},
		{"surrounding whitespace trimmed", "  apps/web-ui  ", "apps/web-ui", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleaned, ignore := cleanArea(tt.area)
			if cleaned != tt.wantCleaned || ignore != tt.wantIgnore {
				t.Errorf("cleanArea(%q) = (%q, %v), want (%q, %v)", tt.area, cleaned, ignore, tt.wantCleaned, tt.wantIgnore)
			}
		})
	}
}

// TestResolvePathOwnership_BareDirArea_BlocksDescendant is an integration
// test at the resolvePathOwnership level (rather than areaMatches directly)
// reproducing AC2's repro shape: a manifest whose area is a bare directory
// (as every pre-SPEC-084 grill-generated manifest declares them) must block
// a file underneath it.
func TestResolvePathOwnership_BareDirArea_BlocksDescendant(t *testing.T) {
	manifest := `[{"role":"frontend","areas":["apps/web-ui"]}]`

	got := resolvePathOwnership("apps/web-ui/lib/version.ts", testCWD, true, manifest)

	if got.ExitCode != 2 || got.Owner != "frontend" {
		t.Errorf("got %+v, want ExitCode=2 Owner=frontend", got)
	}
}

// --- normalisePathForOwnership -----------------------------------------------

// TestNormalisePathForOwnership covers R4: relative, absolute in-tree, and
// out-of-tree paths.
func TestNormalisePathForOwnership(t *testing.T) {
	tests := []struct {
		name          string
		path          string
		wantRel       string
		wantOutOfTree bool
	}{
		{"empty path", "", "", false},
		{"relative in-tree", "internal/foo.go", "internal/foo.go", false},
		{"absolute in-tree", filepath.Join(testCWD, "internal", "foo.go"), "internal/foo.go", false},
		{"absolute out-of-tree", "/etc/passwd", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rel, outOfTree := normalisePathForOwnership(tt.path, testCWD)
			if rel != tt.wantRel || outOfTree != tt.wantOutOfTree {
				t.Errorf("normalisePathForOwnership(%q, %q) = (%q, %v), want (%q, %v)",
					tt.path, testCWD, rel, outOfTree, tt.wantRel, tt.wantOutOfTree)
			}
		})
	}
}
