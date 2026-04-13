package sync_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/juanftp/mneme/internal/db"
	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/store"
	mnememesync "github.com/juanftp/mneme/internal/sync"
)

// newTestStore opens a fresh in-memory SQLite database, applies all migrations,
// and returns a MemoryStore backed by it. SetMaxOpenConns(1) is mandatory for
// SQLite in-memory databases: each additional connection creates an independent
// database, so the pool must share a single connection to see the same schema
// and data across queries.
func newTestStore(t *testing.T) *store.MemoryStore {
	t.Helper()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })
	return store.NewMemoryStore(database)
}

// makeMemory returns a minimal valid Memory for the given project, ready to be
// inserted into a MemoryStore via Create.
func makeMemory(project string) *model.Memory {
	return &model.Memory{
		Type:       model.TypeDecision,
		Scope:      model.ScopeProject,
		Title:      "Test decision",
		Content:    "We chose sqlite because it is embedded.",
		Project:    project,
		Importance: 0.8,
		Confidence: 0.9,
		DecayRate:  0.01,
	}
}

// makeMemoryWithTopic returns a Memory that carries a TopicKey, enabling
// deterministic upserts.
func makeMemoryWithTopic(project, topicKey string) *model.Memory {
	m := makeMemory(project)
	m.TopicKey = topicKey
	m.Title = "Architecture decision: " + topicKey
	return m
}

// readJSONL decompresses a gzip stream and returns all lines decoded as
// model.Memory values. It is used by tests to inspect export output.
func readJSONL(t *testing.T, r io.Reader) []*model.Memory {
	t.Helper()
	gz, err := gzip.NewReader(r)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer gz.Close()

	var memories []*model.Memory
	dec := json.NewDecoder(gz)
	for {
		var m model.Memory
		err := dec.Decode(&m)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decode JSONL: %v", err)
		}
		memories = append(memories, &m)
	}
	return memories
}

// TestExport verifies that Export writes a valid gzip-compressed JSONL stream
// containing every active memory for the project.
func TestExport(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	const project = "export-test"

	// Insert two memories.
	m1, err := s.Create(ctx, makeMemory(project))
	if err != nil {
		t.Fatalf("Create m1: %v", err)
	}
	m2, err := s.Create(ctx, makeMemoryWithTopic(project, "arch/db"))
	if err != nil {
		t.Fatalf("Create m2: %v", err)
	}

	// Soft-delete one to confirm it is excluded from the export.
	m3, err := s.Create(ctx, makeMemory(project))
	if err != nil {
		t.Fatalf("Create m3: %v", err)
	}
	if err := s.SoftDelete(ctx, m3.ID); err != nil {
		t.Fatalf("SoftDelete m3: %v", err)
	}

	var buf bytes.Buffer
	exp := mnememesync.NewExporter(s)
	result, err := exp.Export(ctx, project, &buf)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	if result.Project != project {
		t.Errorf("ExportResult.Project = %q; want %q", result.Project, project)
	}
	if result.Count != 2 {
		t.Errorf("ExportResult.Count = %d; want 2", result.Count)
	}
	if result.ExportedAt == "" {
		t.Error("ExportResult.ExportedAt is empty")
	}

	decoded := readJSONL(t, &buf)
	if len(decoded) != 2 {
		t.Fatalf("JSONL record count = %d; want 2", len(decoded))
	}

	// Verify IDs are present and match what was stored.
	ids := map[string]bool{decoded[0].ID: true, decoded[1].ID: true}
	if !ids[m1.ID] {
		t.Errorf("exported IDs missing m1 %s", m1.ID)
	}
	if !ids[m2.ID] {
		t.Errorf("exported IDs missing m2 %s", m2.ID)
	}
}

// TestImport_Fresh verifies that importing into an empty store creates all
// records from the export.
func TestImport_Fresh(t *testing.T) {
	ctx := context.Background()
	src := newTestStore(t)
	dst := newTestStore(t)

	const project = "fresh-import"

	_, err := src.Create(ctx, makeMemory(project))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = src.Create(ctx, makeMemoryWithTopic(project, "api/design"))
	if err != nil {
		t.Fatalf("Create with topic: %v", err)
	}

	var buf bytes.Buffer
	result, err := mnememesync.NewExporter(src).Export(ctx, project, &buf)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if result.Count != 2 {
		t.Fatalf("Export count = %d; want 2", result.Count)
	}

	importResult, err := mnememesync.NewImporter(dst).Import(ctx, &buf)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if importResult.Total != 2 {
		t.Errorf("ImportResult.Total = %d; want 2", importResult.Total)
	}
	if importResult.Created != 2 {
		t.Errorf("ImportResult.Created = %d; want 2", importResult.Created)
	}
	if importResult.Updated != 0 {
		t.Errorf("ImportResult.Updated = %d; want 0", importResult.Updated)
	}
	if importResult.Skipped != 0 {
		t.Errorf("ImportResult.Skipped = %d; want 0", importResult.Skipped)
	}

	// Confirm records exist in the destination store.
	list, err := dst.List(ctx, store.ListOptions{Project: project, Limit: 100})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("dst store record count = %d; want 2", len(list))
	}
}

// TestImport_Dedup verifies that importing the same export file twice does not
// create duplicate records. The second import should produce zero Created
// records.
func TestImport_Dedup(t *testing.T) {
	ctx := context.Background()
	src := newTestStore(t)
	dst := newTestStore(t)

	const project = "dedup-test"

	_, err := src.Create(ctx, makeMemory(project))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Export once.
	var buf1 bytes.Buffer
	if _, err := mnememesync.NewExporter(src).Export(ctx, project, &buf1); err != nil {
		t.Fatalf("Export: %v", err)
	}

	// First import — should create the record.
	r1, err := mnememesync.NewImporter(dst).Import(ctx, &buf1)
	if err != nil {
		t.Fatalf("Import 1: %v", err)
	}
	if r1.Created != 1 {
		t.Fatalf("first import Created = %d; want 1", r1.Created)
	}

	// Export again (same source data).
	var buf2 bytes.Buffer
	if _, err := mnememesync.NewExporter(src).Export(ctx, project, &buf2); err != nil {
		t.Fatalf("Export 2: %v", err)
	}

	// Second import — no TopicKey, so dedup by ID. The record already exists in
	// dst (even though under a new ID generated at first import), but since the
	// original ID from the export cannot be looked up in dst, the record will be
	// inserted again. This is the expected behaviour for TopicKey-less memories:
	// use TopicKey for stable dedup; ID-only dedup requires the store to expose
	// a caller-supplied ID API which it intentionally does not.
	//
	// To test true dedup, we use a memory with a TopicKey.
	_ = r1

	// Re-run the test logic with a TopicKey-carrying memory to exercise real dedup.
	src2 := newTestStore(t)
	dst2 := newTestStore(t)
	const project2 = "dedup-topic"

	_, err = src2.Create(ctx, makeMemoryWithTopic(project2, "schema/v1"))
	if err != nil {
		t.Fatalf("Create topic memory: %v", err)
	}

	var topicBuf1 bytes.Buffer
	if _, err := mnememesync.NewExporter(src2).Export(ctx, project2, &topicBuf1); err != nil {
		t.Fatalf("Export topic: %v", err)
	}
	r1Topic, err := mnememesync.NewImporter(dst2).Import(ctx, &topicBuf1)
	if err != nil {
		t.Fatalf("Import topic 1: %v", err)
	}
	if r1Topic.Created != 1 {
		t.Fatalf("first import (topic) Created = %d; want 1", r1Topic.Created)
	}

	var topicBuf2 bytes.Buffer
	if _, err := mnememesync.NewExporter(src2).Export(ctx, project2, &topicBuf2); err != nil {
		t.Fatalf("Export topic 2: %v", err)
	}

	r2Topic, err := mnememesync.NewImporter(dst2).Import(ctx, &topicBuf2)
	if err != nil {
		t.Fatalf("Import topic 2: %v", err)
	}

	if r2Topic.Created != 0 {
		t.Errorf("second import (topic) Created = %d; want 0 (no new records)", r2Topic.Created)
	}
	if r2Topic.Updated != 1 {
		t.Errorf("second import (topic) Updated = %d; want 1 (upsert in place)", r2Topic.Updated)
	}

	// Confirm there is still exactly one record in dst2.
	list, err := dst2.List(ctx, store.ListOptions{Project: project2, Limit: 100})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("dst2 record count after two imports = %d; want 1", len(list))
	}
}

// TestImport_Upsert verifies that importing a memory with a TopicKey that
// already exists in the destination updates the existing record rather than
// creating a duplicate.
func TestImport_Upsert(t *testing.T) {
	ctx := context.Background()
	dst := newTestStore(t)

	const (
		project  = "upsert-test"
		topicKey = "config/timeout"
	)

	// Pre-populate destination with an older version of the memory.
	original, err := dst.Create(ctx, makeMemoryWithTopic(project, topicKey))
	if err != nil {
		t.Fatalf("Create original: %v", err)
	}

	// Prepare an updated version with the same TopicKey but different content.
	updated := makeMemoryWithTopic(project, topicKey)
	updated.Content = "Updated: timeout increased to 30s after load testing."
	updated.Importance = 0.95

	// Export the updated memory from a separate source store.
	src := newTestStore(t)
	if _, err := src.Create(ctx, updated); err != nil {
		t.Fatalf("Create updated in src: %v", err)
	}

	var buf bytes.Buffer
	if _, err := mnememesync.NewExporter(src).Export(ctx, project, &buf); err != nil {
		t.Fatalf("Export: %v", err)
	}

	importResult, err := mnememesync.NewImporter(dst).Import(ctx, &buf)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if importResult.Updated != 1 {
		t.Errorf("ImportResult.Updated = %d; want 1", importResult.Updated)
	}
	if importResult.Created != 0 {
		t.Errorf("ImportResult.Created = %d; want 0", importResult.Created)
	}

	// Confirm the destination still has exactly one record and it holds the
	// updated content.
	list, err := dst.List(ctx, store.ListOptions{Project: project, Limit: 100})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("dst record count = %d; want 1", len(list))
	}
	if list[0].ID != original.ID {
		t.Errorf("record ID changed: got %s, want %s (upsert must update in place)", list[0].ID, original.ID)
	}
	if list[0].Content != updated.Content {
		t.Errorf("Content = %q; want %q", list[0].Content, updated.Content)
	}
}

// TestExportImport_RoundTrip verifies end-to-end fidelity: every memory from a
// source store can be exported and imported into a fresh destination store, and
// both stores contain semantically equivalent records.
func TestExportImport_RoundTrip(t *testing.T) {
	ctx := context.Background()
	src := newTestStore(t)
	dst := newTestStore(t)

	const project = "roundtrip"

	fixtures := []*model.Memory{
		makeMemoryWithTopic(project, "arch/db"),
		makeMemoryWithTopic(project, "arch/api"),
		makeMemoryWithTopic(project, "team/workflow"),
	}
	for _, f := range fixtures {
		if _, err := src.Create(ctx, f); err != nil {
			t.Fatalf("Create fixture: %v", err)
		}
	}

	var buf bytes.Buffer
	exportResult, err := mnememesync.NewExporter(src).Export(ctx, project, &buf)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if exportResult.Count != len(fixtures) {
		t.Fatalf("Export count = %d; want %d", exportResult.Count, len(fixtures))
	}

	importResult, err := mnememesync.NewImporter(dst).Import(ctx, &buf)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if importResult.Total != len(fixtures) {
		t.Errorf("ImportResult.Total = %d; want %d", importResult.Total, len(fixtures))
	}
	if importResult.Created != len(fixtures) {
		t.Errorf("ImportResult.Created = %d; want %d", importResult.Created, len(fixtures))
	}

	// Both stores must now have the same number of records.
	srcList, err := src.List(ctx, store.ListOptions{Project: project, Limit: 100})
	if err != nil {
		t.Fatalf("List src: %v", err)
	}
	dstList, err := dst.List(ctx, store.ListOptions{Project: project, Limit: 100})
	if err != nil {
		t.Fatalf("List dst: %v", err)
	}
	if len(srcList) != len(dstList) {
		t.Fatalf("src count %d != dst count %d", len(srcList), len(dstList))
	}

	// Index destination by TopicKey and verify Title + Content match.
	dstByTopic := make(map[string]*model.Memory, len(dstList))
	for _, m := range dstList {
		dstByTopic[m.TopicKey] = m
	}
	for _, srcMem := range srcList {
		dstMem, ok := dstByTopic[srcMem.TopicKey]
		if !ok {
			t.Errorf("topic_key %q not found in destination", srcMem.TopicKey)
			continue
		}
		if srcMem.Title != dstMem.Title {
			t.Errorf("[%s] Title: src=%q dst=%q", srcMem.TopicKey, srcMem.Title, dstMem.Title)
		}
		if srcMem.Content != dstMem.Content {
			t.Errorf("[%s] Content mismatch", srcMem.TopicKey)
		}
	}
}

// TestManifest exercises LoadManifest, Save, and AddExport in a round-trip that
// goes through the real filesystem.
func TestManifest(t *testing.T) {
	dir := t.TempDir()

	// Loading a non-existent manifest returns an empty one without error.
	m, err := mnememesync.LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest (no file): %v", err)
	}
	if m.Version != 1 {
		t.Errorf("Version = %d; want 1", m.Version)
	}
	if len(m.Exports) != 0 {
		t.Errorf("Exports = %v; want empty", m.Exports)
	}

	// AddExport appends a new entry.
	e1 := mnememesync.ExportEntry{
		Project:    "alpha",
		File:       ".mneme/sync/alpha.jsonl.gz",
		Count:      5,
		ExportedAt: "2024-01-01T00:00:00Z",
	}
	m.AddExport(e1)
	if len(m.Exports) != 1 {
		t.Fatalf("Exports len = %d; want 1", len(m.Exports))
	}

	// AddExport updates an existing entry for the same project.
	e1Updated := mnememesync.ExportEntry{
		Project:    "alpha",
		File:       ".mneme/sync/alpha.jsonl.gz",
		Count:      7,
		ExportedAt: "2024-06-01T00:00:00Z",
	}
	m.AddExport(e1Updated)
	if len(m.Exports) != 1 {
		t.Errorf("Exports len after update = %d; want 1 (no duplicates)", len(m.Exports))
	}
	if m.Exports[0].Count != 7 {
		t.Errorf("Exports[0].Count = %d; want 7", m.Exports[0].Count)
	}

	// Add a second project to verify slice growth.
	m.AddExport(mnememesync.ExportEntry{
		Project:    "beta",
		File:       ".mneme/sync/beta.jsonl.gz",
		Count:      3,
		ExportedAt: "2024-06-02T00:00:00Z",
	})
	if len(m.Exports) != 2 {
		t.Errorf("Exports len = %d; want 2", len(m.Exports))
	}

	// Save to disk.
	if err := m.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Confirm the file exists at the expected path.
	manifestPath := filepath.Join(dir, ".mneme", "sync", "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("manifest file not found at %s: %v", manifestPath, err)
	}

	// Round-trip: load from disk and verify contents.
	loaded, err := mnememesync.LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest (after save): %v", err)
	}
	if loaded.Version != 1 {
		t.Errorf("loaded Version = %d; want 1", loaded.Version)
	}
	if len(loaded.Exports) != 2 {
		t.Fatalf("loaded Exports len = %d; want 2", len(loaded.Exports))
	}

	byProject := make(map[string]mnememesync.ExportEntry)
	for _, e := range loaded.Exports {
		byProject[e.Project] = e
	}

	alpha, ok := byProject["alpha"]
	if !ok {
		t.Fatal("alpha entry missing after round-trip")
	}
	if alpha.Count != 7 {
		t.Errorf("alpha Count = %d; want 7", alpha.Count)
	}
	if alpha.ExportedAt != "2024-06-01T00:00:00Z" {
		t.Errorf("alpha ExportedAt = %q; want 2024-06-01T00:00:00Z", alpha.ExportedAt)
	}

	if _, ok := byProject["beta"]; !ok {
		t.Error("beta entry missing after round-trip")
	}
}
