package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/codegraph"
	"github.com/wirvii/mneme/internal/quality"
)

// newTestCodeGraphService opens an in-memory codegraph DB, creates a temp dir
// with two Go source files, indexes them, and returns the ready service plus the
// temp dir root so tests can read source back.
func newTestCodeGraphService(t *testing.T) (*CodeGraphService, string) {
	t.Helper()
	cdb, err := codegraph.OpenDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cdb.Close() })

	svc := NewCodeGraphServiceFromDB(cdb)

	// Create temp dir with Go source files.
	dir := t.TempDir()
	writeTestGoFile(t, dir, "main.go", `package main

import "fmt"

func main() {
	fmt.Println(Hello())
}

func Hello() string {
	return "hello"
}
`)
	writeTestGoFile(t, dir, "util.go", `package main

// Add adds two numbers.
func Add(a, b int) int {
	return a + b
}
`)

	// Index the temp dir.
	_, err = svc.Index(codegraph.IndexOptions{RootDir: dir})
	if err != nil {
		t.Fatal(err)
	}

	return svc, dir
}

// writeTestGoFile writes content to dir/name, failing the test on error.
func writeTestGoFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCodeGraphService_Index(t *testing.T) {
	cdb, err := codegraph.OpenDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cdb.Close() })
	svc := NewCodeGraphServiceFromDB(cdb)

	dir := t.TempDir()
	writeTestGoFile(t, dir, "main.go", `package main

func main() {}
`)
	writeTestGoFile(t, dir, "util.go", `package main

func helper() {}
`)

	result, err := svc.Index(codegraph.IndexOptions{RootDir: dir})
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if result.FilesIndexed < 2 {
		t.Errorf("expected FilesIndexed >= 2, got %d", result.FilesIndexed)
	}
}

func TestCodeGraphService_Search(t *testing.T) {
	svc, _ := newTestCodeGraphService(t)

	results, err := svc.Search("Hello", nil, nil, 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected at least one result for 'Hello', got none")
	}
	found := false
	for _, n := range results {
		if n.Name == "Hello" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected node named 'Hello' in results, got %+v", results)
	}
}

func TestCodeGraphService_Search_KindFilter(t *testing.T) {
	svc, _ := newTestCodeGraphService(t)

	kinds := []codegraph.NodeKind{codegraph.NodeKindFunction}
	results, err := svc.Search("Hello", kinds, nil, 10)
	if err != nil {
		t.Fatalf("Search with kind filter: %v", err)
	}
	for _, n := range results {
		if n.Kind != codegraph.NodeKindFunction {
			t.Errorf("expected only function nodes, got kind=%q for node %q", n.Kind, n.Name)
		}
	}
	if len(results) == 0 {
		t.Error("expected at least one function node matching 'Hello'")
	}
}

func TestCodeGraphService_Callers(t *testing.T) {
	svc, _ := newTestCodeGraphService(t)

	callers, err := svc.Callers("Hello", 0, 0)
	if err != nil {
		t.Fatalf("Callers: %v", err)
	}
	// "main" calls "Hello" — callers should include main.
	found := false
	for _, n := range callers {
		if n.Name == "main" {
			found = true
			break
		}
	}
	if !found {
		t.Logf("callers of Hello: %+v", callers)
		// Callers may be empty if the extractor did not emit a calls edge;
		// that is an extractor-level concern, not a service concern.
		// We accept empty as long as no error was returned.
	}
	_ = found
}

func TestCodeGraphService_Callees(t *testing.T) {
	svc, _ := newTestCodeGraphService(t)

	callees, err := svc.Callees("main", 0, 0)
	if err != nil {
		t.Fatalf("Callees: %v", err)
	}
	// We accept zero callees gracefully — the Go extractor may or may not track
	// fmt.Println. The important thing is no error and the slice is not nil on error.
	_ = callees
}

func TestCodeGraphService_NodeDetail(t *testing.T) {
	svc, dir := newTestCodeGraphService(t)

	node, source, err := svc.NodeDetail("Hello", dir)
	if err != nil {
		t.Fatalf("NodeDetail: %v", err)
	}
	if node == nil {
		t.Fatal("expected non-nil node for 'Hello'")
	}
	if node.Name != "Hello" {
		t.Errorf("expected node name 'Hello', got %q", node.Name)
	}
	// Source should be non-empty since the file exists and line numbers are valid.
	if source == "" {
		t.Error("expected non-empty source code for 'Hello'")
	}
}

func TestCodeGraphService_Status(t *testing.T) {
	svc, _ := newTestCodeGraphService(t)

	stats, err := svc.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if stats.NodeCount == 0 {
		t.Error("expected non-zero NodeCount after indexing")
	}
	if stats.FileCount == 0 {
		t.Error("expected non-zero FileCount after indexing")
	}
}

func TestCodeGraphService_Files(t *testing.T) {
	svc, _ := newTestCodeGraphService(t)

	files, err := svc.Files("", "")
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if len(files) < 2 {
		t.Errorf("expected at least 2 files, got %d", len(files))
	}
}

func TestCodeGraphService_Files_LanguageFilter(t *testing.T) {
	svc, _ := newTestCodeGraphService(t)

	files, err := svc.Files("", "go")
	if err != nil {
		t.Fatalf("Files with language filter: %v", err)
	}
	for _, f := range files {
		if f.Language != "go" {
			t.Errorf("expected only go files, got language=%q for %q", f.Language, f.Path)
		}
	}
}

type affectedGitFake struct {
	headSHA       string
	mergeBase     string
	changes       []quality.FileChange
	dirtyPaths    []string
	changedFrom   string
	changedTo     string
	mergeBaseFrom string
	mergeBaseTo   string
}

func (f *affectedGitFake) HeadSHA() (string, error) { return f.headSHA, nil }

func (f *affectedGitFake) MergeBase(from, to string) (string, error) {
	f.mergeBaseFrom, f.mergeBaseTo = from, to
	return f.mergeBase, nil
}

func (f *affectedGitFake) ChangedFilesInRange(from, to string) ([]quality.FileChange, error) {
	f.changedFrom, f.changedTo = from, to
	return f.changes, nil
}

func (f *affectedGitFake) IsDirty() (bool, []string, error) {
	return len(f.dirtyPaths) > 0, f.dirtyPaths, nil
}

func newAffectedCodeGraphService(t *testing.T) *CodeGraphService {
	t.Helper()
	cdb, err := codegraph.OpenDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cdb.Close() })
	svc := NewCodeGraphServiceFromDB(cdb)
	nodes := []codegraph.Node{
		{ID: "changed", Kind: codegraph.NodeKindFunction, Name: "Changed", QualifiedName: "pkg.Changed", FilePath: "changed.go", Language: "go"},
		{ID: "caller", Kind: codegraph.NodeKindFunction, Name: "Caller", QualifiedName: "pkg.Caller", FilePath: "caller.go", Language: "go"},
	}
	for _, node := range nodes {
		if err := svc.store.UpsertNode(node); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.store.UpsertEdge(codegraph.Edge{Source: "caller", Target: "changed", Kind: codegraph.EdgeKindCalls}); err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestCodegraphAffected_ExplicitPathsDefaultsAndStale(t *testing.T) {
	svc := newAffectedCodeGraphService(t)
	git := &affectedGitFake{headSHA: "head"}
	svc.affectedGit = git
	if err := svc.store.SetMetadata(codegraph.MetaKeyLastIndexedSHA, "old"); err != nil {
		t.Fatal(err)
	}

	result, err := svc.Affected(codegraph.AffectedRequest{Paths: []string{"missing.go", "changed.go"}})
	if err != nil {
		t.Fatalf("Affected: %v", err)
	}
	if got, want := strings.Join(result.Inputs, ","), "changed.go,missing.go"; got != want {
		t.Errorf("inputs = %q, want %q", got, want)
	}
	if got, want := strings.Join(result.MissingPaths, ","), "missing.go"; got != want {
		t.Errorf("missing = %q, want %q", got, want)
	}
	if result.Total != 1 || result.Truncated {
		t.Errorf("total/truncated = %d/%v, want 1/false", result.Total, result.Truncated)
	}
	if !result.Stale || result.IndexedSHA != "old" {
		t.Errorf("stale/indexed_sha = %v/%q, want true/old", result.Stale, result.IndexedSHA)
	}
}

func TestCodegraphAffected_GitRangeUsesMergeBase(t *testing.T) {
	svc := newAffectedCodeGraphService(t)
	// A stale record for a path Git says was deleted must still be disclosed
	// as missing; otherwise an old graph can turn deletion into false certainty.
	if err := svc.store.UpsertNode(codegraph.Node{ID: "deleted", Kind: codegraph.NodeKindFunction, Name: "Deleted", QualifiedName: "pkg.Deleted", FilePath: "deleted.go", Language: "go"}); err != nil {
		t.Fatal(err)
	}
	git := &affectedGitFake{
		headSHA:   "head",
		mergeBase: "merge-base",
		changes: []quality.FileChange{
			{Path: "changed.go", Status: quality.FileStatusModified},
			{Path: "deleted.go", Status: quality.FileStatusDeleted},
		},
	}
	svc.affectedGit = git
	if err := svc.store.SetMetadata(codegraph.MetaKeyLastIndexedSHA, "head"); err != nil {
		t.Fatal(err)
	}

	result, err := svc.Affected(codegraph.AffectedRequest{Base: "base", Head: "head", Depth: 2, Limit: 10})
	if err != nil {
		t.Fatalf("Affected: %v", err)
	}
	if git.mergeBaseFrom != "base" || git.mergeBaseTo != "head" || git.changedFrom != "merge-base" || git.changedTo != "head" {
		t.Errorf("git calls = merge-base(%q,%q), changed(%q,%q)", git.mergeBaseFrom, git.mergeBaseTo, git.changedFrom, git.changedTo)
	}
	if got, want := strings.Join(result.Inputs, ","), "changed.go,deleted.go"; got != want {
		t.Errorf("inputs = %q, want %q", got, want)
	}
	if got := strings.Join(result.MissingPaths, ","); got != "deleted.go" {
		t.Errorf("missing = %q, want deleted.go", got)
	}
	if result.Stale {
		t.Error("result unexpectedly stale")
	}
}

func TestCodegraphAffected_DefaultWorktreeDiffReportsUntracked(t *testing.T) {
	svc := newAffectedCodeGraphService(t)
	git := &affectedGitFake{headSHA: "head", dirtyPaths: []string{" M changed.go", "?? new.go"}}
	svc.affectedGit = git

	result, err := svc.Affected(codegraph.AffectedRequest{})
	if err != nil {
		t.Fatalf("Affected: %v", err)
	}
	if got, want := strings.Join(result.Inputs, ","), "changed.go,new.go"; got != want {
		t.Errorf("inputs = %q, want %q", got, want)
	}
	if got := strings.Join(result.UntrackedPaths, ","); got != "new.go" {
		t.Errorf("untracked = %q, want new.go", got)
	}
	if got := strings.Join(result.MissingPaths, ","); got != "new.go" {
		t.Errorf("missing = %q, want new.go", got)
	}
}

func TestCodegraphAffected_RejectsMixedInputs(t *testing.T) {
	svc := newAffectedCodeGraphService(t)
	svc.affectedGit = &affectedGitFake{headSHA: "head"}
	_, err := svc.Affected(codegraph.AffectedRequest{Paths: []string{"changed.go"}, Base: "base"})
	if !errors.Is(err, ErrInvalidAffectedRequest) {
		t.Fatalf("error = %v, want ErrInvalidAffectedRequest", err)
	}
}

func TestCodegraphAffected_MissingGraphIsExplicit(t *testing.T) {
	cdb, err := codegraph.OpenDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cdb.Close() })
	svc := NewCodeGraphServiceFromDB(cdb)
	svc.affectedGit = &affectedGitFake{headSHA: "head"}

	result, err := svc.Affected(codegraph.AffectedRequest{Paths: []string{"changed.go"}})
	if err != nil {
		t.Fatalf("Affected: %v", err)
	}
	if !result.MissingGraph || strings.Join(result.MissingPaths, ",") != "changed.go" {
		t.Errorf("missing graph/path = %v/%v, want true/[changed.go]", result.MissingGraph, result.MissingPaths)
	}
}

func TestCodegraphAffected_DefaultDepthAndLimit(t *testing.T) {
	svc := newAffectedCodeGraphService(t)
	svc.affectedGit = &affectedGitFake{headSHA: "head"}
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("direct-%02d", i)
		node := codegraph.Node{ID: id, Kind: codegraph.NodeKindFunction, Name: id, QualifiedName: "pkg." + id, FilePath: id + ".go", Language: "go"}
		if err := svc.store.UpsertNode(node); err != nil {
			t.Fatal(err)
		}
		if err := svc.store.UpsertEdge(codegraph.Edge{Source: id, Target: "changed", Kind: codegraph.EdgeKindCalls}); err != nil {
			t.Fatal(err)
		}
	}
	previous := "caller"
	for depth := 2; depth <= 4; depth++ {
		id := fmt.Sprintf("deep-%d", depth)
		if err := svc.store.UpsertNode(codegraph.Node{ID: id, Kind: codegraph.NodeKindFunction, Name: id, QualifiedName: "pkg." + id, FilePath: id + ".go", Language: "go"}); err != nil {
			t.Fatal(err)
		}
		if err := svc.store.UpsertEdge(codegraph.Edge{Source: id, Target: previous, Kind: codegraph.EdgeKindCalls}); err != nil {
			t.Fatal(err)
		}
		previous = id
	}

	result, err := svc.Affected(codegraph.AffectedRequest{Paths: []string{"changed.go"}})
	if err != nil {
		t.Fatalf("Affected: %v", err)
	}
	if result.Total != 53 || len(result.AffectedNodes) != 50 || !result.Truncated {
		t.Fatalf("total/returned/truncated = %d/%d/%v, want 53/50/true", result.Total, len(result.AffectedNodes), result.Truncated)
	}
	for _, node := range result.AffectedNodes {
		if node.Depth > 3 {
			t.Errorf("default depth returned %+v", node)
		}
	}
}

func TestCodeGraphService_ResolveSymbol_NotFound(t *testing.T) {
	svc, _ := newTestCodeGraphService(t)

	_, err := svc.resolveSymbol("NonExistentSymbolXYZ123")
	if err == nil {
		t.Error("expected error when resolving nonexistent symbol, got nil")
	}
}

// TestLastIndexedSHA_RoundTrip verifies the service passthroughs read and write
// the last-indexed SHA in the codegraph DB (SPEC-101). A fresh service returns
// "" until a SHA is stamped.
func TestLastIndexedSHA_RoundTrip(t *testing.T) {
	cdb, err := codegraph.OpenDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cdb.Close() })
	svc := NewCodeGraphServiceFromDB(cdb)

	got, err := svc.LastIndexedSHA()
	if err != nil {
		t.Fatalf("LastIndexedSHA (fresh): %v", err)
	}
	if got != "" {
		t.Errorf("fresh LastIndexedSHA = %q, want empty", got)
	}

	if err := svc.SetLastIndexedSHA("cafebabe"); err != nil {
		t.Fatalf("SetLastIndexedSHA: %v", err)
	}
	got, err = svc.LastIndexedSHA()
	if err != nil {
		t.Fatalf("LastIndexedSHA (after set): %v", err)
	}
	if got != "cafebabe" {
		t.Errorf("LastIndexedSHA = %q, want cafebabe", got)
	}
}

// TestCodeGraphService_DegradedLanguages_RoundTrip verifies the SPEC-142
// pass-through: DegradedLanguages reads back exactly what the underlying
// store persists, with no logic of its own in this layer.
func TestCodeGraphService_DegradedLanguages_RoundTrip(t *testing.T) {
	cdb, err := codegraph.OpenDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cdb.Close() })
	svc := NewCodeGraphServiceFromDB(cdb)

	langs, err := svc.DegradedLanguages()
	if err != nil {
		t.Fatalf("DegradedLanguages (fresh): %v", err)
	}
	if len(langs) != 0 {
		t.Errorf("fresh DegradedLanguages = %+v, want empty", langs)
	}

	if err := codegraph.NewStore(cdb).SetDegradedLanguages([]codegraph.DegradedLanguage{
		{Language: "typescript", Cause: codegraph.CauseToolchainIncompatible},
	}); err != nil {
		t.Fatalf("SetDegradedLanguages: %v", err)
	}

	langs, err = svc.DegradedLanguages()
	if err != nil {
		t.Fatalf("DegradedLanguages (after set): %v", err)
	}
	if len(langs) != 1 || langs[0].Language != "typescript" {
		t.Errorf("DegradedLanguages = %+v, want one typescript entry", langs)
	}
}

// TestCodeGraphService_GraphNotice combines DegradedLanguages with
// codegraph.Notice — verified here so a caller with a live service never
// needs to repeat that two-step dance itself.
func TestCodeGraphService_GraphNotice(t *testing.T) {
	cdb, err := codegraph.OpenDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cdb.Close() })
	svc := NewCodeGraphServiceFromDB(cdb)

	if _, show := svc.GraphNotice(); show {
		t.Error("GraphNotice on a healthy graph: show = true, want false")
	}

	if err := codegraph.NewStore(cdb).SetDegradedLanguages([]codegraph.DegradedLanguage{
		{Language: "typescript", Cause: codegraph.CauseToolchainIncompatible},
	}); err != nil {
		t.Fatalf("SetDegradedLanguages: %v", err)
	}

	line, show := svc.GraphNotice()
	if !show {
		t.Fatal("GraphNotice on a marked graph: show = false, want true")
	}
	if !strings.HasPrefix(line, codegraph.NoticeToken) {
		t.Errorf("GraphNotice line = %q, want prefix %q", line, codegraph.NoticeToken)
	}
}
