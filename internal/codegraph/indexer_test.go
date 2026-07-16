package codegraph

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// newTestIndexer opens an in-memory CodeGraphDB, wraps it in a Store, and
// returns a fresh Indexer ready for test use. The database is closed when the
// test ends via t.Cleanup.
func newTestIndexer(t *testing.T) (*Indexer, *Store) {
	t.Helper()
	cdb, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { cdb.Close() })
	s := NewStore(cdb)
	return NewIndexer(s), s
}

// writeGoFile creates a .go source file with the given content under dir.
func writeGoFile(t *testing.T, dir, name, source string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// TestIndexer_IndexesGoFiles verifies that the indexer walks a temp directory,
// extracts nodes from two .go files, and persists them to the store.
func TestIndexer_IndexesGoFiles(t *testing.T) {
	dir := t.TempDir()

	writeGoFile(t, dir, "a.go", `package main

// Hello returns a greeting.
func Hello() string {
	return "hello"
}
`)
	writeGoFile(t, dir, "b.go", `package main

// World returns the world.
func World() string {
	return "world"
}
`)

	ix, s := newTestIndexer(t)
	result, err := ix.Index(IndexOptions{RootDir: dir})
	if err != nil {
		t.Fatalf("Index: %v", err)
	}

	if result.FilesIndexed != 2 {
		t.Errorf("FilesIndexed = %d, want 2", result.FilesIndexed)
	}
	if result.FilesErrored != 0 {
		t.Errorf("FilesErrored = %d, want 0", result.FilesErrored)
	}

	stats, err := s.GetStats()
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	// Each file produces at least 1 function node + 1 file node.
	if stats.NodeCount < 4 {
		t.Errorf("NodeCount = %d, want >= 4", stats.NodeCount)
	}
	if stats.FileCount != 2 {
		t.Errorf("FileCount = %d, want 2", stats.FileCount)
	}
}

// TestIndexer_Incremental verifies that re-indexing unmodified files skips them.
func TestIndexer_Incremental(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "main.go", `package main

func Run() {}
`)

	ix, _ := newTestIndexer(t)

	first, err := ix.Index(IndexOptions{RootDir: dir})
	if err != nil {
		t.Fatalf("first Index: %v", err)
	}
	if first.FilesIndexed != 1 {
		t.Errorf("first pass FilesIndexed = %d, want 1", first.FilesIndexed)
	}

	second, err := ix.Index(IndexOptions{RootDir: dir})
	if err != nil {
		t.Fatalf("second Index: %v", err)
	}
	if second.FilesSkipped < 1 {
		t.Errorf("second pass FilesSkipped = %d, want >= 1", second.FilesSkipped)
	}
	if second.FilesIndexed != 0 {
		t.Errorf("second pass FilesIndexed = %d, want 0", second.FilesIndexed)
	}
}

// TestIndexer_Incremental_Modified verifies that only modified files are re-indexed.
func TestIndexer_Incremental_Modified(t *testing.T) {
	dir := t.TempDir()
	path := writeGoFile(t, dir, "main.go", `package main

func Original() {}
`)
	writeGoFile(t, dir, "other.go", `package main

func Stable() {}
`)

	ix, _ := newTestIndexer(t)
	if _, err := ix.Index(IndexOptions{RootDir: dir}); err != nil {
		t.Fatalf("first Index: %v", err)
	}

	// Modify one file.
	if err := os.WriteFile(path, []byte(`package main

func Modified() {}
`), 0o644); err != nil {
		t.Fatalf("write modified file: %v", err)
	}

	result, err := ix.Index(IndexOptions{RootDir: dir})
	if err != nil {
		t.Fatalf("second Index: %v", err)
	}
	if result.FilesIndexed != 1 {
		t.Errorf("FilesIndexed = %d, want 1 (only modified file)", result.FilesIndexed)
	}
	if result.FilesSkipped != 1 {
		t.Errorf("FilesSkipped = %d, want 1 (unchanged file)", result.FilesSkipped)
	}
}

// TestIndexer_RespectsIgnoreDirs verifies that ignored directories are not indexed.
// It covers both the explicit ignoredDirs map (vendor, node_modules, dist, build,
// testdata, .codegraph) and the hidden-directory skip (.next, .turbo, coverage/.foo
// are representative of the new entries added in SPEC-046).
func TestIndexer_RespectsIgnoreDirs(t *testing.T) {
	dir := t.TempDir()

	// Create a real file at the root — should be indexed.
	writeGoFile(t, dir, "real.go", `package main

func Real() {}
`)

	// Create files inside each ignored directory (pre-existing set).
	for _, ignored := range []string{"vendor", "node_modules", ".git", "dist", "build", "testdata", ".codegraph"} {
		subDir := filepath.Join(dir, ignored)
		if err := os.MkdirAll(subDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", subDir, err)
		}
		writeGoFile(t, subDir, "ignored.go", `package ignored

func Ignored() {}
`)
	}

	// SPEC-046: also verify that hidden dirs and explicitly added entries are skipped.
	// .next and .turbo are covered by the hidden-dir skip AND ignoredDirs.
	// coverage is a non-hidden dir that requires an explicit ignoredDirs entry.
	// .foo is an arbitrary hidden dir — tests that the generic hidden-dir skip works.
	for _, ignored := range []string{".next", ".turbo", "coverage", ".foo"} {
		subDir := filepath.Join(dir, ignored)
		if err := os.MkdirAll(subDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", subDir, err)
		}
		writeGoFile(t, subDir, "ignored.go", `package ignored

func Ignored() {}
`)
	}

	ix, s := newTestIndexer(t)
	result, err := ix.Index(IndexOptions{RootDir: dir})
	if err != nil {
		t.Fatalf("Index: %v", err)
	}

	// Only the root-level real.go should be indexed.
	if result.FilesIndexed != 1 {
		t.Errorf("FilesIndexed = %d, want 1 (ignore dirs must be skipped)", result.FilesIndexed)
	}

	stats, err := s.GetStats()
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if stats.FileCount != 1 {
		t.Errorf("FileCount = %d, want 1", stats.FileCount)
	}
}

// TestIndexer_DeletedFile verifies that files removed from disk are cleaned up
// from the store during a subsequent index run.
func TestIndexer_DeletedFile(t *testing.T) {
	dir := t.TempDir()
	toDelete := writeGoFile(t, dir, "ephemeral.go", `package main

func Ephemeral() {}
`)
	writeGoFile(t, dir, "permanent.go", `package main

func Permanent() {}
`)

	ix, s := newTestIndexer(t)
	if _, err := ix.Index(IndexOptions{RootDir: dir}); err != nil {
		t.Fatalf("first Index: %v", err)
	}

	statsAfterFirst, err := s.GetStats()
	if err != nil {
		t.Fatalf("GetStats after first index: %v", err)
	}
	if statsAfterFirst.FileCount != 2 {
		t.Errorf("FileCount after first index = %d, want 2", statsAfterFirst.FileCount)
	}

	// Remove one file from disk.
	if err := os.Remove(toDelete); err != nil {
		t.Fatalf("remove ephemeral.go: %v", err)
	}

	if _, err := ix.Index(IndexOptions{RootDir: dir}); err != nil {
		t.Fatalf("second Index: %v", err)
	}

	statsAfterSecond, err := s.GetStats()
	if err != nil {
		t.Fatalf("GetStats after second index: %v", err)
	}
	if statsAfterSecond.FileCount != 1 {
		t.Errorf("FileCount after second index = %d, want 1 (deleted file must be cleaned up)", statsAfterSecond.FileCount)
	}
}

// TestIndexer_PrunesNodesFromNowIgnoredDir verifies AC3/AC8 of SPEC-046: nodes
// for files that were indexed in a prior run but now fall under an ignored
// directory must be purged from the store on the next index run.
//
// This test reproduces the production failure observed in migratio: the indexer
// skipped .claude/worktrees/... on re-index (hidden-dir skip), but pruneDeleted
// only iterated the files table. Because those paths had no files-table entry
// (removed by an earlier pruneDeleted pass that cleaned the files record but
// missed the nodes), the nodes accumulated as permanent orphans.
func TestIndexer_PrunesNodesFromNowIgnoredDir(t *testing.T) {
	dir := t.TempDir()

	// Create a permanent file that should always be indexed.
	writeGoFile(t, dir, "real.go", `package main

func Permanent() {}
`)

	// Create a file inside a subdirectory that is not yet ignored.
	// We simulate the "before ignoredDirs was updated" state by placing the
	// file in a directory that does NOT match any ignored-dir name.
	tempSubDir := filepath.Join(dir, "willbeignored")
	if err := os.MkdirAll(tempSubDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", tempSubDir, err)
	}
	writeGoFile(t, tempSubDir, "stale.go", `package stale

func Stale() {}
`)

	ix, s := newTestIndexer(t)

	// First index: both real.go and willbeignored/stale.go are indexed.
	first, err := ix.Index(IndexOptions{RootDir: dir})
	if err != nil {
		t.Fatalf("first Index: %v", err)
	}
	if first.FilesIndexed != 2 {
		t.Errorf("first pass FilesIndexed = %d, want 2", first.FilesIndexed)
	}

	statsFirst, err := s.GetStats()
	if err != nil {
		t.Fatalf("GetStats after first index: %v", err)
	}
	if statsFirst.FileCount != 2 {
		t.Errorf("FileCount after first index = %d, want 2", statsFirst.FileCount)
	}
	if statsFirst.NodeCount == 0 {
		t.Fatalf("NodeCount after first index = 0, expected > 0")
	}

	// Simulate the dir becoming ignored: remove the files-table entry manually
	// (as if a previous pruneDeleted pass removed it but left the nodes behind,
	// which is exactly the production bug). The nodes for willbeignored/stale.go
	// must still exist at this point.
	if err := s.DeleteFile("willbeignored/stale.go"); err != nil {
		t.Fatalf("DeleteFile (simulate partial prune): %v", err)
	}

	// Verify the orphan state: stale.go has nodes but no files entry.
	nodesBeforeReindex, err := s.ListDistinctNodeFilePaths()
	if err != nil {
		t.Fatalf("ListDistinctNodeFilePaths: %v", err)
	}
	foundOrphan := false
	for _, p := range nodesBeforeReindex {
		if p == "willbeignored/stale.go" {
			foundOrphan = true
			break
		}
	}
	if !foundOrphan {
		t.Fatal("expected orphan node for willbeignored/stale.go after simulated partial prune, not found")
	}

	// Now remove the physical dir so the walk skips it (mirrors the ignored-dir
	// behaviour: the walk simply never visits those paths).
	if err := os.RemoveAll(tempSubDir); err != nil {
		t.Fatalf("remove willbeignored dir: %v", err)
	}

	// Second index: real.go is skipped (unchanged hash), willbeignored/ is gone.
	// pruneDeleted must detect the orphan node and remove it.
	second, err := ix.Index(IndexOptions{RootDir: dir})
	if err != nil {
		t.Fatalf("second Index: %v", err)
	}
	_ = second // result details are not the focus; node cleanup is

	statsSecond, err := s.GetStats()
	if err != nil {
		t.Fatalf("GetStats after second index: %v", err)
	}
	if statsSecond.FileCount != 1 {
		t.Errorf("FileCount after second index = %d, want 1 (only real.go)", statsSecond.FileCount)
	}

	// All nodes for willbeignored/stale.go must be gone.
	allPaths, err := s.ListDistinctNodeFilePaths()
	if err != nil {
		t.Fatalf("ListDistinctNodeFilePaths after second index: %v", err)
	}
	for _, p := range allPaths {
		if p == "willbeignored/stale.go" {
			t.Errorf("orphan nodes for willbeignored/stale.go still present after reindex — pruneDeleted did not clean them up")
		}
	}
}

// TestIndexer_DryRun verifies that DryRun=true produces counts but writes nothing.
func TestIndexer_DryRun(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "main.go", `package main

func Hello() {}
`)

	ix, s := newTestIndexer(t)
	result, err := ix.Index(IndexOptions{RootDir: dir, DryRun: true})
	if err != nil {
		t.Fatalf("Index dry-run: %v", err)
	}

	// The result must report the file as scanned but not written.
	if result.FilesScanned < 1 {
		t.Errorf("DryRun FilesScanned = %d, want >= 1", result.FilesScanned)
	}

	// No data should be in the DB.
	stats, err := s.GetStats()
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if stats.NodeCount != 0 {
		t.Errorf("DryRun NodeCount = %d, want 0 (no writes expected)", stats.NodeCount)
	}
	if stats.FileCount != 0 {
		t.Errorf("DryRun FileCount = %d, want 0 (no writes expected)", stats.FileCount)
	}
}

// TestIndexer_Force verifies that Force=true re-indexes all files even when
// their content hash has not changed.
func TestIndexer_Force(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "main.go", `package main

func Hello() {}
`)

	ix, _ := newTestIndexer(t)

	first, err := ix.Index(IndexOptions{RootDir: dir})
	if err != nil {
		t.Fatalf("first Index: %v", err)
	}
	if first.FilesIndexed != 1 {
		t.Errorf("first pass FilesIndexed = %d, want 1", first.FilesIndexed)
	}

	// Second pass without Force — file should be skipped.
	second, err := ix.Index(IndexOptions{RootDir: dir})
	if err != nil {
		t.Fatalf("second Index: %v", err)
	}
	if second.FilesSkipped != 1 {
		t.Errorf("second pass (no force) FilesSkipped = %d, want 1", second.FilesSkipped)
	}

	// Third pass with Force — file should be re-indexed even though hash is same.
	third, err := ix.Index(IndexOptions{RootDir: dir, Force: true})
	if err != nil {
		t.Fatalf("third Index (force): %v", err)
	}
	if third.FilesIndexed != 1 {
		t.Errorf("Force FilesIndexed = %d, want 1", third.FilesIndexed)
	}
	if third.FilesSkipped != 0 {
		t.Errorf("Force FilesSkipped = %d, want 0", third.FilesSkipped)
	}
}

// ---------------------------------------------------------------------------
// SPEC-088 G2 — indexer abort-vs-continue asymmetry for extractor failures
// ---------------------------------------------------------------------------

// fakeExtractor is a test double for the Extractor interface. It records
// every filePath it was asked to extract (so tests can prove whether the
// walk stopped after the first call) and always returns the same
// pre-configured error, mirroring the real TSExtractor's contract: a non-nil
// error always comes with a nil *ExtractionResult (see TSExtractor.Extract).
type fakeExtractor struct {
	mu        sync.Mutex
	returnErr error
	calls     []string
}

func (f *fakeExtractor) Extract(filePath string, _ []byte) (*ExtractionResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, filePath)
	f.mu.Unlock()
	return nil, f.returnErr
}

func (f *fakeExtractor) Language() string { return "typescript" }

func (f *fakeExtractor) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// registerFakeExtractor swaps the "typescript" entry in the package-global
// extractorRegistry for a fake, restoring the original on test cleanup.
// extractorRegistry has no mutex (R7 in SPEC-088) — the caller must not run
// this test with t.Parallel(), and every other test in this package that
// indexes .ts/.js files must not run concurrently with it either.
func registerFakeExtractor(t *testing.T, fake *fakeExtractor) {
	t.Helper()
	orig := extractorRegistry["typescript"]
	t.Cleanup(func() { extractorRegistry["typescript"] = orig })
	RegisterExtractor("typescript", func() Extractor { return fake })
}

// TestIndexer_AbortsOnIncompatibleExtractor verifies the systemic half of the
// D4 asymmetry: when the extractor returns ErrExtractorIncompatible, Index
// must abort the walk (return the error, errors.Is-checkable) instead of
// counting the failure and continuing — and it must abort BEFORE the second
// .ts file is ever handed to the extractor.
//
// Mutation proof (SPEC-088 AC9, executed manually — see the implementation
// report): removing the `errors.Is(err, ErrExtractorIncompatible)` branch in
// indexer.go's WalkDir callback (falling through to the ordinary
// `result.FilesErrored++` path) turns this red: Index then returns nil and
// both .ts files get called, since nothing aborts the walk anymore.
func TestIndexer_AbortsOnIncompatibleExtractor(t *testing.T) {
	fake := &fakeExtractor{returnErr: ErrExtractorIncompatible}
	registerFakeExtractor(t, fake)

	dir := t.TempDir()
	writeGoFile(t, dir, "a.ts", "export const a = 1;\n")
	writeGoFile(t, dir, "b.ts", "export const b = 2;\n")
	writeGoFile(t, dir, "c.go", "package main\n\nfunc C() {}\n")

	ix, _ := newTestIndexer(t)
	_, err := ix.Index(IndexOptions{RootDir: dir})
	if err == nil {
		t.Fatal("Index() error = nil, want ErrExtractorIncompatible")
	}
	if !errors.Is(err, ErrExtractorIncompatible) {
		t.Errorf("Index() error = %v, want errors.Is ErrExtractorIncompatible", err)
	}
	if calls := fake.callCount(); calls != 1 {
		t.Errorf("extractor called %d times, want exactly 1 (walk must abort after the first systemic failure, never reaching the second .ts file)", calls)
	}
}

// TestIndexer_ContinuesOnPerFileExtractorError is the anti-regression twin of
// TestIndexer_AbortsOnIncompatibleExtractor (SPEC-088 D4): an ORDINARY
// extractor error (not ErrExtractorIncompatible) must still be treated as
// per-file — Index returns nil, FilesErrored counts both failures, the walk
// visits every file, and the unrelated .go file still gets indexed. This is
// the degradation guarantee (AC4): a repo without a working TS toolchain
// must not lose its Go indexing.
func TestIndexer_ContinuesOnPerFileExtractorError(t *testing.T) {
	fake := &fakeExtractor{returnErr: errors.New("boom: ordinary per-file extraction failure")}
	registerFakeExtractor(t, fake)

	dir := t.TempDir()
	writeGoFile(t, dir, "a.ts", "export const a = 1;\n")
	writeGoFile(t, dir, "b.ts", "export const b = 2;\n")
	writeGoFile(t, dir, "c.go", "package main\n\nfunc C() {}\n")

	ix, s := newTestIndexer(t)
	result, err := ix.Index(IndexOptions{RootDir: dir})
	if err != nil {
		t.Fatalf("Index() error = %v, want nil (an ordinary per-file error must not abort)", err)
	}
	if result.FilesErrored != 2 {
		t.Errorf("FilesErrored = %d, want 2 (both .ts files)", result.FilesErrored)
	}
	if calls := fake.callCount(); calls != 2 {
		t.Errorf("extractor called %d times, want 2 (both .ts files; the walk must not abort)", calls)
	}

	stats, err := s.GetStats()
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if stats.FileCount != 1 {
		t.Errorf("FileCount = %d, want 1 (only c.go, the unaffected .go file, gets indexed)", stats.FileCount)
	}
}
