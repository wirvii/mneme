package codegraph

import (
	"os"
	"path/filepath"
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
