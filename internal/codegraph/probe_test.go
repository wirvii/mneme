package codegraph

import (
	"path/filepath"
	"testing"
	"time"
)

// TestProbeGraph_AbsentFile verifies that ProbeGraph returns hasNodes=false and
// nil error when the database file does not exist (fail-open semantics).
func TestProbeGraph_AbsentFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent-codegraph.db")
	hasNodes, lastUpdated, err := ProbeGraph(path)
	if err != nil {
		t.Fatalf("expected nil error for absent file, got: %v", err)
	}
	if hasNodes {
		t.Errorf("hasNodes = true, want false for absent file")
	}
	if lastUpdated != 0 {
		t.Errorf("lastUpdatedUnixMs = %d, want 0 for absent file", lastUpdated)
	}
}

// TestProbeGraph_EmptyDB verifies that ProbeGraph returns hasNodes=false when
// the database exists but has no nodes (schema applied, 0 rows in nodes table).
func TestProbeGraph_EmptyDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "empty-codegraph.db")

	cdb, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	cdb.Close()

	hasNodes, lastUpdated, err := ProbeGraph(dbPath)
	if err != nil {
		t.Fatalf("expected nil error for empty DB, got: %v", err)
	}
	if hasNodes {
		t.Errorf("hasNodes = true, want false for empty DB")
	}
	if lastUpdated != 0 {
		t.Errorf("lastUpdatedUnixMs = %d, want 0 for empty DB", lastUpdated)
	}
}

// TestProbeGraph_WithNodes verifies that ProbeGraph returns hasNodes=true and
// the stored updated_at value when at least one node exists in the database.
func TestProbeGraph_WithNodes(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "with-nodes-codegraph.db")

	cdb, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}

	wantTs := time.Now().UnixMilli()
	n := sampleNode("aabbccddeeff0011", "function", "Foo", "pkg/foo.go")
	n.UpdatedAt = wantTs
	st := NewStore(cdb)
	if uErr := st.UpsertNode(n); uErr != nil {
		t.Fatalf("UpsertNode: %v", uErr)
	}
	cdb.Close()

	hasNodes, lastUpdated, err := ProbeGraph(dbPath)
	if err != nil {
		t.Fatalf("ProbeGraph: %v", err)
	}
	if !hasNodes {
		t.Errorf("hasNodes = false, want true")
	}
	if lastUpdated != wantTs {
		t.Errorf("lastUpdatedUnixMs = %d, want %d", lastUpdated, wantTs)
	}
}

// TestProbeGraph_StalenessTimestamp verifies that ProbeGraph returns the
// MAX(updated_at) across multiple nodes (not just any arbitrary row).
func TestProbeGraph_StalenessTimestamp(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "multi-nodes-codegraph.db")

	cdb, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}

	base := time.Now().UnixMilli()
	oldest := base - 3_600_000  // 1 hour ago
	middle := base - 1_800_000  // 30 min ago
	newest := base - 600_000    // 10 min ago

	st := NewStore(cdb)
	for _, tc := range []struct {
		id  string
		ts  int64
	}{
		{"node-oldest", oldest},
		{"node-middle", middle},
		{"node-newest", newest},
	} {
		n := sampleNode(tc.id, "function", "Fn"+tc.id, "pkg/a.go")
		n.UpdatedAt = tc.ts
		if uErr := st.UpsertNode(n); uErr != nil {
			t.Fatalf("UpsertNode %s: %v", tc.id, uErr)
		}
	}
	cdb.Close()

	hasNodes, lastUpdated, err := ProbeGraph(dbPath)
	if err != nil {
		t.Fatalf("ProbeGraph: %v", err)
	}
	if !hasNodes {
		t.Errorf("hasNodes = false, want true")
	}
	if lastUpdated != newest {
		t.Errorf("lastUpdatedUnixMs = %d, want %d (newest)", lastUpdated, newest)
	}
}

// TestProbeDegraded_AbsentFile verifies fail-open semantics matching
// ProbeGraph: an absent database file means nothing to declare, no error.
func TestProbeDegraded_AbsentFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent-codegraph.db")
	langs, err := ProbeDegraded(path)
	if err != nil {
		t.Fatalf("expected nil error for absent file, got: %v", err)
	}
	if langs != nil {
		t.Errorf("langs = %+v, want nil for absent file", langs)
	}
}

// TestProbeDegraded_NoMark verifies a healthy database with no degraded
// languages recorded returns nil, nil.
func TestProbeDegraded_NoMark(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "healthy-codegraph.db")

	cdb, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	cdb.Close()

	langs, err := ProbeDegraded(dbPath)
	if err != nil {
		t.Fatalf("ProbeDegraded: %v", err)
	}
	if langs != nil {
		t.Errorf("langs = %+v, want nil for a graph with no mark", langs)
	}
}

// TestProbeDegraded_WithMark verifies ProbeDegraded reads back a degraded
// mark set via Store.SetDegradedLanguages, without needing a write-capable
// connection of its own.
func TestProbeDegraded_WithMark(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "marked-codegraph.db")

	cdb, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	st := NewStore(cdb)
	if err := st.SetDegradedLanguages([]DegradedLanguage{
		{Language: "typescript", Cause: CauseToolchainIncompatible},
	}); err != nil {
		t.Fatalf("SetDegradedLanguages: %v", err)
	}
	cdb.Close()

	langs, err := ProbeDegraded(dbPath)
	if err != nil {
		t.Fatalf("ProbeDegraded: %v", err)
	}
	if len(langs) != 1 || langs[0].Language != "typescript" {
		t.Errorf("langs = %+v, want one entry for typescript", langs)
	}
}
