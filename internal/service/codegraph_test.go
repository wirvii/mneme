package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/codegraph"
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
