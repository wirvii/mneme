package lane

import (
	"strings"
	"testing"
)

// buildStats is a helper that builds a DiffStats from a slice of
// (added, removed, path) tuples. Pass added=-1 to mark a binary file.
func buildStats(files []struct{ added, removed int; path string }) *DiffStats {
	stats := &DiffStats{}
	for _, f := range files {
		fs := FileStat{Path: f.path}
		if f.added >= 0 {
			fs.Added = f.added
			fs.Removed = f.removed
		}
		stats.Files = append(stats.Files, fs)
	}
	return stats
}

// TestAudit_Happy verifies that a minimal trivial change (2 files, 15 lines,
// in scope, no public symbols) produces a passing result.
func TestAudit_Happy(t *testing.T) {
	stats := buildStats([]struct{ added, removed int; path string }{
		{8, 7, "internal/store/sdd.go"},
		{0, 0, "internal/store/sdd_test.go"},
	})
	input := AuditInput{Scope: "internal/store/**", RepoDir: "/tmp"}
	result, err := auditFromStats(stats, input, nil, "")
	if err != nil {
		t.Fatalf("auditFromStats: %v", err)
	}
	if !result.Passed {
		t.Errorf("expected Passed=true, got breaches: %v", result.Breaches)
	}
	if result.FileCount != 2 {
		t.Errorf("FileCount: got %d, want 2", result.FileCount)
	}
	if result.LinesChanged != 15 {
		t.Errorf("LinesChanged: got %d, want 15", result.LinesChanged)
	}
}

// TestAudit_FileCountBreach verifies that 5 changed files triggers a breach.
func TestAudit_FileCountBreach(t *testing.T) {
	stats := buildStats([]struct{ added, removed int; path string }{
		{1, 0, "a.go"},
		{1, 0, "b.go"},
		{1, 0, "c.go"},
		{1, 0, "d.go"},
		{1, 0, "e.go"},
	})
	result, err := auditFromStats(stats, AuditInput{}, nil, "")
	if err != nil {
		t.Fatalf("auditFromStats: %v", err)
	}
	if result.Passed {
		t.Error("expected Passed=false for 5-file change")
	}
	found := false
	for _, b := range result.Breaches {
		if strings.Contains(b, "file count 5 exceeds trivial limit of 3") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected file count breach, got %v", result.Breaches)
	}
}

// TestAudit_LineCountBreach verifies that 47 changed lines triggers a breach.
func TestAudit_LineCountBreach(t *testing.T) {
	stats := buildStats([]struct{ added, removed int; path string }{
		{30, 17, "a.go"},
	})
	result, err := auditFromStats(stats, AuditInput{}, nil, "")
	if err != nil {
		t.Fatalf("auditFromStats: %v", err)
	}
	if result.Passed {
		t.Error("expected Passed=false for 47-line change")
	}
	found := false
	for _, b := range result.Breaches {
		if strings.Contains(b, "line count 47 exceeds trivial limit of 20") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected line count breach, got %v", result.Breaches)
	}
}

// TestAudit_ForbiddenSQL verifies that a .sql file triggers a forbidden-path breach.
func TestAudit_ForbiddenSQL(t *testing.T) {
	stats := buildStats([]struct{ added, removed int; path string }{
		{1, 0, "internal/db/migrations/012_something.sql"},
	})
	result, err := auditFromStats(stats, AuditInput{}, nil, "")
	if err != nil {
		t.Fatalf("auditFromStats: %v", err)
	}
	if result.Passed {
		t.Error("expected Passed=false for .sql file")
	}
	found := false
	for _, b := range result.Breaches {
		if strings.Contains(b, "forbidden path modified") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected forbidden path breach, got %v", result.Breaches)
	}
}

// TestAudit_ForbiddenCmd verifies that a file under cmd/** triggers a breach.
func TestAudit_ForbiddenCmd(t *testing.T) {
	stats := buildStats([]struct{ added, removed int; path string }{
		{1, 0, "cmd/mneme/main.go"},
	})
	result, err := auditFromStats(stats, AuditInput{}, nil, "")
	if err != nil {
		t.Fatalf("auditFromStats: %v", err)
	}
	if result.Passed {
		t.Error("expected Passed=false for cmd/** file")
	}
}

// TestAudit_ForbiddenInstallAssets verifies that a file in install/assets/** triggers a breach.
func TestAudit_ForbiddenInstallAssets(t *testing.T) {
	stats := buildStats([]struct{ added, removed int; path string }{
		{1, 0, "internal/install/assets/orchestrator.md"},
	})
	result, err := auditFromStats(stats, AuditInput{}, nil, "")
	if err != nil {
		t.Fatalf("auditFromStats: %v", err)
	}
	if result.Passed {
		t.Error("expected Passed=false for install/assets file")
	}
}

// TestAudit_OutOfScope verifies that files outside the declared scope trigger a breach.
func TestAudit_OutOfScope(t *testing.T) {
	stats := buildStats([]struct{ added, removed int; path string }{
		{1, 0, "internal/service/sdd.go"},
		{1, 0, "internal/model/sdd.go"},
	})
	input := AuditInput{Scope: "internal/service/**"}
	result, err := auditFromStats(stats, input, nil, "")
	if err != nil {
		t.Fatalf("auditFromStats: %v", err)
	}
	if result.Passed {
		t.Error("expected Passed=false for out-of-scope file")
	}
	found := false
	for _, b := range result.Breaches {
		if strings.Contains(b, "out of scope: internal/model/sdd.go") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected out-of-scope breach for model file, got %v", result.Breaches)
	}
}

// TestAudit_ExportedGoSymbol verifies that adding or removing an exported Go
// symbol triggers a public-symbol breach.
func TestAudit_ExportedGoSymbol(t *testing.T) {
	before := `package foo
// ExistingFunc does something.
func ExistingFunc() {}
`
	after := `package foo
// ExistingFunc does something.
func ExistingFunc() {}
// NewExportedFunc was added — triggers a breach.
func NewExportedFunc() {}
`
	beforeNames := exportedGoNames(before)
	afterNames := exportedGoNames(after)

	var breaches []string
	for name := range afterNames {
		if _, ok := beforeNames[name]; !ok {
			breaches = append(breaches, "public symbol changed: "+name+" in foo.go")
		}
	}
	for name := range beforeNames {
		if _, ok := afterNames[name]; !ok {
			breaches = append(breaches, "public symbol changed: "+name+" in foo.go")
		}
	}

	if len(breaches) == 0 {
		t.Error("expected at least one breach for new exported function")
	}
	found := false
	for _, b := range breaches {
		if strings.Contains(b, "NewExportedFunc") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected breach for NewExportedFunc, got %v", breaches)
	}
}

// TestAudit_TSExport verifies that adding or removing an `export` in a TS file
// triggers a public-export breach using the diff heuristic.
func TestAudit_TSExport(t *testing.T) {
	// Simulate a diff line that adds an export.
	diffLine := "+export function newThing(): void {}"
	matched := reExportAdded.MatchString(diffLine)
	if !matched {
		t.Errorf("expected reExportAdded to match %q", diffLine)
	}

	// A context line (no +/-) should not match.
	contextLine := " export function existing(): void {}"
	if reExportAdded.MatchString(contextLine) {
		t.Errorf("reExportAdded should NOT match context line %q", contextLine)
	}
	if reExportRemoved.MatchString(contextLine) {
		t.Errorf("reExportRemoved should NOT match context line %q", contextLine)
	}
}

// TestAudit_Combined verifies that 5 files with a forbidden SQL file produces
// at least 2 distinct breaches (file count + forbidden path).
func TestAudit_Combined(t *testing.T) {
	stats := buildStats([]struct{ added, removed int; path string }{
		{1, 0, "a.go"},
		{1, 0, "b.go"},
		{1, 0, "c.go"},
		{1, 0, "d.go"},
		{1, 0, "internal/db/migrations/013.sql"},
	})
	result, err := auditFromStats(stats, AuditInput{}, nil, "")
	if err != nil {
		t.Fatalf("auditFromStats: %v", err)
	}
	if result.Passed {
		t.Error("expected Passed=false")
	}
	if len(result.Breaches) < 2 {
		t.Errorf("expected at least 2 breaches (file count + forbidden), got %d: %v",
			len(result.Breaches), result.Breaches)
	}
}

// TestMatchGlobStar verifies the ** glob expansion logic with various patterns.
func TestMatchGlobStar(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"**/*.sql", "internal/db/migrations/011.sql", true},
		{"**/*.sql", "foo.sql", true},
		{"**/*.sql", "foo.go", false},
		{"**/migrations/**", "internal/db/migrations/011_add.sql", true},
		{"**/migrations/**", "internal/db/migrations/subdir/file.sql", true},
		{"**/migrations/**", "internal/db/other.go", false},
		{"cmd/**", "cmd/mneme/main.go", true},
		{"cmd/**", "internal/cmd/foo.go", false},
		{"internal/install/assets/**", "internal/install/assets/foo.md", true},
		{"internal/install/assets/**", "internal/install/other.go", false},
		{"internal/store/**", "internal/store/sdd.go", true},
		{"internal/store/**", "internal/store/subdir/file.go", true},
		{"internal/store/**", "internal/service/sdd.go", false},
		{"internal/store/*.go", "internal/store/sdd.go", true},
		{"internal/store/*.go", "internal/store/subdir/sdd.go", false},
	}
	for _, tc := range tests {
		got := matchGlobStar(tc.pattern, tc.path)
		if got != tc.want {
			t.Errorf("matchGlobStar(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

// TestExportedGoNames verifies that the AST-based exported name extractor
// correctly identifies exported functions, types, and consts.
func TestExportedGoNames(t *testing.T) {
	src := `package foo
func Exported() {}
func unexported() {}
type ExportedType struct {}
type unexportedType struct {}
const ExportedConst = 1
const unexportedConst = 2
var ExportedVar = 3
`
	names := exportedGoNames(src)
	want := []string{"Exported", "ExportedType", "ExportedConst", "ExportedVar"}
	unwant := []string{"unexported", "unexportedType", "unexportedConst"}

	for _, name := range want {
		if _, ok := names[name]; !ok {
			t.Errorf("expected %q to be in exported names", name)
		}
	}
	for _, name := range unwant {
		if _, ok := names[name]; ok {
			t.Errorf("did not expect %q to be in exported names", name)
		}
	}
}
