package codegraph

import (
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// project_metadata accessors (SPEC-101)
// ---------------------------------------------------------------------------

// TestMetadata_RoundTrip verifies a set→get round-trip and that a second set for
// the same key overwrites the previous value (upsert semantics).
func TestMetadata_RoundTrip(t *testing.T) {
	s := newTestStore(t)

	if err := s.SetMetadata(MetaKeyLastIndexedSHA, "abc123"); err != nil {
		t.Fatalf("SetMetadata: %v", err)
	}
	got, err := s.GetMetadata(MetaKeyLastIndexedSHA)
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	if got != "abc123" {
		t.Errorf("GetMetadata = %q, want abc123", got)
	}

	// Second write overwrites.
	if err := s.SetMetadata(MetaKeyLastIndexedSHA, "def456"); err != nil {
		t.Fatalf("SetMetadata (overwrite): %v", err)
	}
	got, err = s.GetMetadata(MetaKeyLastIndexedSHA)
	if err != nil {
		t.Fatalf("GetMetadata (after overwrite): %v", err)
	}
	if got != "def456" {
		t.Errorf("GetMetadata after overwrite = %q, want def456", got)
	}
}

// TestMetadata_AbsentKey verifies that reading a key that was never written
// returns ("", nil) rather than an error.
func TestMetadata_AbsentKey(t *testing.T) {
	s := newTestStore(t)
	got, err := s.GetMetadata("never-written")
	if err != nil {
		t.Fatalf("GetMetadata absent: unexpected error %v", err)
	}
	if got != "" {
		t.Errorf("GetMetadata absent = %q, want empty string", got)
	}
}

// TestMetadata_UpdatesTimestamp verifies that overwriting a key advances its
// updated_at column (proving SetMetadata refreshes the timestamp on conflict).
func TestMetadata_UpdatesTimestamp(t *testing.T) {
	s := newTestStore(t)
	if err := s.SetMetadata("k", "v1"); err != nil {
		t.Fatalf("SetMetadata v1: %v", err)
	}
	var first int64
	if err := s.db.DB.QueryRow(`SELECT updated_at FROM project_metadata WHERE key = 'k'`).Scan(&first); err != nil {
		t.Fatalf("read updated_at: %v", err)
	}
	// Force a distinct second-resolution timestamp.
	if _, err := s.db.DB.Exec(`UPDATE project_metadata SET updated_at = 0 WHERE key = 'k'`); err != nil {
		t.Fatalf("reset updated_at: %v", err)
	}
	if err := s.SetMetadata("k", "v2"); err != nil {
		t.Fatalf("SetMetadata v2: %v", err)
	}
	var second int64
	if err := s.db.DB.QueryRow(`SELECT updated_at FROM project_metadata WHERE key = 'k'`).Scan(&second); err != nil {
		t.Fatalf("read updated_at again: %v", err)
	}
	if second == 0 {
		t.Errorf("updated_at not refreshed on overwrite (still 0)")
	}
}

// TestMetadata_DBClosedErrors verifies both accessors surface a wrapped error
// when the underlying DB is unusable (closed), exercising the error paths.
func TestMetadata_DBClosedErrors(t *testing.T) {
	cdb, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	s := NewStore(cdb)
	if err := cdb.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.SetMetadata("k", "v"); err == nil {
		t.Error("SetMetadata on closed DB: expected error")
	}
	if _, err := s.GetMetadata("k"); err == nil {
		t.Error("GetMetadata on closed DB: expected error")
	}
}

// ---------------------------------------------------------------------------
// IsEligibleSource (SPEC-101)
// ---------------------------------------------------------------------------

func TestIsEligibleSource(t *testing.T) {
	cases := []struct {
		path     string
		wantLang string
		wantOK   bool
	}{
		{"main.go", "go", true},
		{"internal/store/store.go", "go", true},
		{"web/app.tsx", "typescript", true},
		{"web/app.jsx", "javascript", true},
		{"README.md", "", false},                 // unsupported extension
		{"internal/_ignore.go", "", false},       // underscore-prefixed file
		{".hidden.go", "", false},                // hidden file
		{"node_modules/pkg/index.js", "", false}, // ignored dir
		{"vendor/lib/a.go", "", false},           // ignored dir
		{".git/hooks/pre.go", "", false},         // hidden dir component
		{"src/.cache/x.ts", "", false},           // ignored/hidden nested dir
		{"a/b/c/deep.ts", "typescript", true},    // deep eligible
	}
	for _, tc := range cases {
		lang, ok := IsEligibleSource(tc.path)
		if ok != tc.wantOK || lang != tc.wantLang {
			t.Errorf("IsEligibleSource(%q) = (%q,%v), want (%q,%v)",
				tc.path, lang, ok, tc.wantLang, tc.wantOK)
		}
	}
}

// ---------------------------------------------------------------------------
// indexScoped (SPEC-101)
// ---------------------------------------------------------------------------

// nodesForFile returns the number of non-file nodes recorded for relPath.
func nodesForFile(t *testing.T, s *Store, relPath string) int {
	t.Helper()
	nodes, err := s.GetNodesByFilePath(relPath)
	if err != nil {
		t.Fatalf("GetNodesByFilePath(%q): %v", relPath, err)
	}
	return len(nodes)
}

// TestIndexScoped_AddedModified verifies that A/M entries are extracted and that
// FilesScanned reflects only the change set, not a tree walk.
func TestIndexScoped_AddedModified(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "added.go", "package p\n\nfunc Added() {}\n")
	writeGoFile(t, dir, "modified.go", "package p\n\nfunc Modified() {}\n")
	// A distractor file that is on disk but NOT in the change set: scoped mode
	// must ignore it (proving there is no full walk).
	writeGoFile(t, dir, "untouched.go", "package p\n\nfunc Untouched() {}\n")

	ix, s := newTestIndexer(t)
	res, err := ix.Index(IndexOptions{
		RootDir: dir,
		Changes: []ChangedFile{
			{Path: "added.go", Status: ChangeAdded},
			{Path: "modified.go", Status: ChangeModified},
		},
	})
	if err != nil {
		t.Fatalf("Index scoped: %v", err)
	}
	if res.FilesScanned != 2 {
		t.Errorf("FilesScanned = %d, want 2 (change set only)", res.FilesScanned)
	}
	if res.FilesIndexed != 2 {
		t.Errorf("FilesIndexed = %d, want 2", res.FilesIndexed)
	}
	if nodesForFile(t, s, "added.go") == 0 {
		t.Error("added.go: expected symbols in graph")
	}
	if nodesForFile(t, s, "untouched.go") != 0 {
		t.Error("untouched.go: must NOT be indexed in scoped mode")
	}
}

// TestIndexScoped_Deleted verifies that a D entry purges the file's symbols.
func TestIndexScoped_Deleted(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "gone.go", "package p\n\nfunc Gone() {}\n")

	ix, s := newTestIndexer(t)
	// First index the file so there is something to purge.
	if _, err := ix.Index(IndexOptions{RootDir: dir, Changes: []ChangedFile{
		{Path: "gone.go", Status: ChangeAdded},
	}}); err != nil {
		t.Fatalf("seed index: %v", err)
	}
	if nodesForFile(t, s, "gone.go") == 0 {
		t.Fatal("precondition: gone.go should have symbols")
	}

	res, err := ix.Index(IndexOptions{RootDir: dir, Changes: []ChangedFile{
		{Path: "gone.go", Status: ChangeDeleted},
	}})
	if err != nil {
		t.Fatalf("Index delete: %v", err)
	}
	if res.FilesDeleted != 1 {
		t.Errorf("FilesDeleted = %d, want 1", res.FilesDeleted)
	}
	if nodesForFile(t, s, "gone.go") != 0 {
		t.Error("gone.go: symbols must be purged after delete")
	}
	if fr, _ := s.GetFile("gone.go"); fr != nil {
		t.Error("gone.go: file record must be removed after delete")
	}
}

// TestIndexScoped_DeleteNeverIndexed verifies that deleting a path that was never
// indexed is a harmless no-op (no error).
func TestIndexScoped_DeleteNeverIndexed(t *testing.T) {
	dir := t.TempDir()
	ix, _ := newTestIndexer(t)
	res, err := ix.Index(IndexOptions{RootDir: dir, Changes: []ChangedFile{
		{Path: "phantom.go", Status: ChangeDeleted},
	}})
	if err != nil {
		t.Fatalf("Index delete never-indexed: %v", err)
	}
	if res.FilesDeleted != 1 {
		t.Errorf("FilesDeleted = %d, want 1 (no-op still counts)", res.FilesDeleted)
	}
}

// TestIndexScoped_Renamed verifies that a rename purges the old path and extracts
// the new one.
func TestIndexScoped_Renamed(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "old.go", "package p\n\nfunc Renamed() {}\n")

	ix, s := newTestIndexer(t)
	if _, err := ix.Index(IndexOptions{RootDir: dir, Changes: []ChangedFile{
		{Path: "old.go", Status: ChangeAdded},
	}}); err != nil {
		t.Fatalf("seed index: %v", err)
	}

	// Rename old.go → new.go on disk, then feed the rename entry.
	writeGoFile(t, dir, "new.go", "package p\n\nfunc Renamed() {}\n")
	res, err := ix.Index(IndexOptions{RootDir: dir, Changes: []ChangedFile{
		{OldPath: "old.go", Path: "new.go", Status: ChangeRenamed},
	}})
	if err != nil {
		t.Fatalf("Index rename: %v", err)
	}
	if res.FilesDeleted != 1 {
		t.Errorf("FilesDeleted = %d, want 1 (old path purged)", res.FilesDeleted)
	}
	if res.FilesIndexed != 1 {
		t.Errorf("FilesIndexed = %d, want 1 (new path extracted)", res.FilesIndexed)
	}
	if nodesForFile(t, s, "old.go") != 0 {
		t.Error("old.go: symbols must be purged after rename")
	}
	if nodesForFile(t, s, "new.go") == 0 {
		t.Error("new.go: expected symbols after rename")
	}
}

// TestIndexScoped_IneligibleIgnored verifies that ineligible change entries
// (unsupported extension, ignored directory) are silently skipped.
func TestIndexScoped_IneligibleIgnored(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "real.go", "package p\n\nfunc Real() {}\n")

	ix, _ := newTestIndexer(t)
	res, err := ix.Index(IndexOptions{RootDir: dir, Changes: []ChangedFile{
		{Path: "README.md", Status: ChangeAdded},               // unsupported ext
		{Path: "node_modules/x/index.js", Status: ChangeAdded}, // ignored dir
		{Path: "real.go", Status: ChangeModified},              // eligible
	}})
	if err != nil {
		t.Fatalf("Index scoped ineligible: %v", err)
	}
	if res.FilesScanned != 1 {
		t.Errorf("FilesScanned = %d, want 1 (only the eligible entry)", res.FilesScanned)
	}
}

// TestIndexScoped_LanguageOverride verifies that opts.Language overrides the
// detected language for scoped entries.
func TestIndexScoped_LanguageOverride(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "a.go", "package p\n\nfunc A() {}\n")

	ix, s := newTestIndexer(t)
	if _, err := ix.Index(IndexOptions{
		RootDir:  dir,
		Language: "go",
		Changes:  []ChangedFile{{Path: "a.go", Status: ChangeAdded}},
	}); err != nil {
		t.Fatalf("Index scoped with language override: %v", err)
	}
	if nodesForFile(t, s, "a.go") == 0 {
		t.Error("a.go: expected symbols with language override")
	}
}

// TestIndexScoped_RenameToIneligible verifies a rename whose destination is not
// an indexable source file purges the old path and indexes nothing new.
func TestIndexScoped_RenameToIneligible(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "old.go", "package p\n\nfunc Gone() {}\n")

	ix, s := newTestIndexer(t)
	if _, err := ix.Index(IndexOptions{RootDir: dir, Changes: []ChangedFile{
		{Path: "old.go", Status: ChangeAdded},
	}}); err != nil {
		t.Fatalf("seed index: %v", err)
	}

	res, err := ix.Index(IndexOptions{RootDir: dir, Changes: []ChangedFile{
		{OldPath: "old.go", Path: "docs/notes.md", Status: ChangeRenamed},
	}})
	if err != nil {
		t.Fatalf("Index rename to ineligible: %v", err)
	}
	if res.FilesDeleted != 1 {
		t.Errorf("FilesDeleted = %d, want 1", res.FilesDeleted)
	}
	if res.FilesIndexed != 0 {
		t.Errorf("FilesIndexed = %d, want 0 (destination ineligible)", res.FilesIndexed)
	}
	if nodesForFile(t, s, "old.go") != 0 {
		t.Error("old.go: must be purged")
	}
}

// TestIndexScoped_StoreErrors exercises the store-error branches: on a closed DB
// an add is recorded as FilesErrored (non-fatal), while a delete propagates the
// purge error out of indexScoped.
func TestIndexScoped_StoreErrors(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "a.go", "package p\n\nfunc A() {}\n")

	cdb, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	ix := NewIndexer(NewStore(cdb))
	if _, err := ix.Index(IndexOptions{RootDir: dir, Changes: []ChangedFile{
		{Path: "a.go", Status: ChangeAdded},
	}}); err != nil {
		t.Fatalf("seed index: %v", err)
	}
	if err := cdb.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Add/modify on a closed DB: indexFile errors, counted as FilesErrored, no
	// propagation (only ErrExtractorIncompatible aborts).
	res, err := ix.Index(IndexOptions{RootDir: dir, Changes: []ChangedFile{
		{Path: "a.go", Status: ChangeModified},
	}})
	if err != nil {
		t.Fatalf("modified on closed DB should not propagate: %v", err)
	}
	if res.FilesErrored == 0 {
		t.Error("expected FilesErrored > 0 on closed-DB extract")
	}

	// Delete on a closed DB: purge fails and the error propagates.
	if _, err := ix.Index(IndexOptions{RootDir: dir, Changes: []ChangedFile{
		{Path: "a.go", Status: ChangeDeleted},
	}}); err == nil {
		t.Error("expected purge error to propagate on closed DB")
	}
}

// TestIndexScoped_DryRun verifies that DryRun counts but does not write or purge.
func TestIndexScoped_DryRun(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "seed.go", "package p\n\nfunc Seed() {}\n")

	ix, s := newTestIndexer(t)
	// Seed a file for the delete-in-dry-run assertion.
	if _, err := ix.Index(IndexOptions{RootDir: dir, Changes: []ChangedFile{
		{Path: "seed.go", Status: ChangeAdded},
	}}); err != nil {
		t.Fatalf("seed index: %v", err)
	}
	seedNodes := nodesForFile(t, s, "seed.go")

	writeGoFile(t, dir, "fresh.go", "package p\n\nfunc Fresh() {}\n")
	res, err := ix.Index(IndexOptions{
		RootDir: dir,
		DryRun:  true,
		Changes: []ChangedFile{
			{Path: "fresh.go", Status: ChangeAdded},
			{Path: "seed.go", Status: ChangeDeleted},
		},
	})
	if err != nil {
		t.Fatalf("Index dry-run: %v", err)
	}
	if res.FilesIndexed != 1 || res.FilesDeleted != 1 {
		t.Errorf("dry-run counts: FilesIndexed=%d FilesDeleted=%d, want 1/1", res.FilesIndexed, res.FilesDeleted)
	}
	if nodesForFile(t, s, "fresh.go") != 0 {
		t.Error("fresh.go: dry-run must not write nodes")
	}
	if nodesForFile(t, s, "seed.go") != seedNodes {
		t.Error("seed.go: dry-run must not purge existing nodes")
	}
}

// TestIndexScoped_ForceIgnoresChanges verifies that Force=true forces a full scan
// even when Changes is set (the distractor file gets indexed).
func TestIndexScoped_ForceIgnoresChanges(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "a.go", "package p\n\nfunc A() {}\n")
	writeGoFile(t, dir, "b.go", "package p\n\nfunc B() {}\n")

	ix, s := newTestIndexer(t)
	_, err := ix.Index(IndexOptions{
		RootDir: dir,
		Force:   true,
		Changes: []ChangedFile{{Path: "a.go", Status: ChangeAdded}}, // ignored under Force
	})
	if err != nil {
		t.Fatalf("Index force: %v", err)
	}
	// Full scan means b.go (not in Changes) is also indexed.
	if nodesForFile(t, s, "b.go") == 0 {
		t.Error("b.go: Force must fall back to full scan and index all files")
	}
}

// TestIndex_NilChangesIsFullWalk verifies that Changes==nil preserves the full
// walk behaviour (regression guard): the distractor is indexed and deletions of
// vanished files are pruned.
func TestIndex_NilChangesIsFullWalk(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "x.go", "package p\n\nfunc X() {}\n")
	writeGoFile(t, dir, "y.go", "package p\n\nfunc Y() {}\n")

	ix, s := newTestIndexer(t)
	res, err := ix.Index(IndexOptions{RootDir: dir}) // Changes nil
	if err != nil {
		t.Fatalf("Index full: %v", err)
	}
	if res.FilesIndexed != 2 {
		t.Errorf("FilesIndexed = %d, want 2 (full walk)", res.FilesIndexed)
	}
	if res.FilesDeleted != 0 {
		t.Errorf("FilesDeleted = %d, want 0 (full walk never sets it)", res.FilesDeleted)
	}

	// Remove y.go on disk and re-run full walk: pruneDeleted must drop it.
	if err := os.Remove(filepath.Join(dir, "y.go")); err != nil {
		t.Fatalf("remove y.go: %v", err)
	}
	if _, err := ix.Index(IndexOptions{RootDir: dir}); err != nil {
		t.Fatalf("Index full second: %v", err)
	}
	if nodesForFile(t, s, "y.go") != 0 {
		t.Error("y.go: full walk must prune deleted file")
	}
}
