package sync_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/store"
	mnemeSync "github.com/juanftp/mneme/internal/sync"
)

// marshalManifestTarGz builds a minimal .manifest.tar.gz from a ManifestRoot.
// Used in tests that need a crafted (not real-export) archive.
func marshalManifestTarGz(t *testing.T, root mnemeSync.ManifestRoot) ([]byte, error) {
	t.Helper()
	data, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	hdr := &tar.Header{Name: "manifest.json", Size: int64(len(data)), Mode: 0o644}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz close: %v", err)
	}

	return buf.Bytes(), nil
}

// TestManifestImport_FullData verifies the round-trip: export from one store,
// import into another, and verify all four types are present.
func TestManifestImport_FullData(t *testing.T) {
	src := newTestStore(t)
	dst := newTestStore(t)
	ctx := context.Background()

	const project = "full/roundtrip"

	// Populate source store.
	mem, err := src.Create(ctx, makeMemory(project))
	if err != nil {
		t.Fatalf("Create memory: %v", err)
	}

	e1 := makeEntityInStore(t, src, project, "entity-alpha")
	e2 := makeEntityInStore(t, src, project, "entity-beta")

	rel := &model.Relation{SourceID: e1.ID, TargetID: e2.ID, Type: model.RelRelatedTo}
	if _, err := src.CreateRelation(ctx, rel); err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}

	makeSessionInStore(t, src, project)

	// Export.
	var buf bytes.Buffer
	exp := mnemeSync.NewManifestExporter(src, "mneme", "test")
	if _, err := exp.Export(ctx, project, &buf); err != nil {
		t.Fatalf("Export: %v", err)
	}

	// Import into dst.
	imp := mnemeSync.NewManifestImporter(dst)
	result, err := imp.Import(ctx, &buf)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if result.MemoriesCreated != 1 {
		t.Errorf("MemoriesCreated: got %d, want 1", result.MemoriesCreated)
	}
	if result.EntitiesCreated != 2 {
		t.Errorf("EntitiesCreated: got %d, want 2", result.EntitiesCreated)
	}
	if result.RelationsCreated != 1 {
		t.Errorf("RelationsCreated: got %d, want 1", result.RelationsCreated)
	}
	if result.SessionsCreated != 1 {
		t.Errorf("SessionsCreated: got %d, want 1", result.SessionsCreated)
	}

	// Verify memory content was transferred.
	memories, err := dst.List(ctx, store.ListOptions{Project: project, Limit: 100})
	if err != nil {
		t.Fatalf("dst.List: %v", err)
	}
	if len(memories) != 1 {
		t.Fatalf("dst memories: got %d, want 1", len(memories))
	}
	if memories[0].Title != mem.Title {
		t.Errorf("memory title: got %q, want %q", memories[0].Title, mem.Title)
	}
}

// TestManifestImport_MemoryDedup verifies deduplication semantics for memories:
// - Memories with topic_key are upserted (update on re-import, not duplicate).
// - Memories without topic_key receive a new ID each create and cannot be
//   deduped by ID across different stores — this matches the JSONL importer
//   behaviour documented in sync.go:215-224.
func TestManifestImport_MemoryDedup(t *testing.T) {
	src := newTestStore(t)
	dst := newTestStore(t)
	ctx := context.Background()

	const project = "dedup/memories"

	// Memory with topic_key (will be upserted, not duplicated).
	withTopic := makeMemoryWithTopic(project, "arch/db")
	if _, err := src.Create(ctx, withTopic); err != nil {
		t.Fatalf("Create withTopic: %v", err)
	}

	exportBuf := func() bytes.Buffer {
		var buf bytes.Buffer
		exp := mnemeSync.NewManifestExporter(src, "mneme", "test")
		if _, err := exp.Export(ctx, project, &buf); err != nil {
			t.Fatalf("Export: %v", err)
		}
		return buf
	}

	imp := mnemeSync.NewManifestImporter(dst)

	// First import: topic_key memory created.
	buf1 := exportBuf()
	r1, err := imp.Import(ctx, &buf1)
	if err != nil {
		t.Fatalf("First Import: %v", err)
	}
	if r1.MemoriesCreated != 1 {
		t.Errorf("first import MemoriesCreated: got %d, want 1", r1.MemoriesCreated)
	}

	// Second import: topic_key memory updated (upsert), not recreated.
	buf2 := exportBuf()
	r2, err := imp.Import(ctx, &buf2)
	if err != nil {
		t.Fatalf("Second Import: %v", err)
	}
	if r2.MemoriesCreated != 0 {
		t.Errorf("second import MemoriesCreated: got %d, want 0", r2.MemoriesCreated)
	}
	if r2.MemoriesUpdated != 1 {
		t.Errorf("second import MemoriesUpdated: got %d, want 1", r2.MemoriesUpdated)
	}
}

// TestManifestImport_EntityDedup verifies that importing entities that already
// exist by (name, project) results in skips, not duplicates.
func TestManifestImport_EntityDedup(t *testing.T) {
	src := newTestStore(t)
	dst := newTestStore(t)
	ctx := context.Background()

	const project = "dedup/entities"
	makeEntityInStore(t, src, project, "my-service")

	var buf bytes.Buffer
	exp := mnemeSync.NewManifestExporter(src, "mneme", "test")
	if _, err := exp.Export(ctx, project, &buf); err != nil {
		t.Fatalf("Export: %v", err)
	}
	// Pre-create the entity in dst.
	makeEntityInStore(t, dst, project, "my-service")

	imp := mnemeSync.NewManifestImporter(dst)
	bufCopy := make([]byte, buf.Len())
	copy(bufCopy, buf.Bytes())
	result, err := imp.Import(ctx, bytes.NewReader(bufCopy))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if result.EntitiesCreated != 0 {
		t.Errorf("EntitiesCreated: got %d, want 0", result.EntitiesCreated)
	}
	if result.EntitiesSkipped != 1 {
		t.Errorf("EntitiesSkipped: got %d, want 1", result.EntitiesSkipped)
	}
}

// TestManifestImport_RelationDedup verifies that importing relations that already
// exist results in skips.
func TestManifestImport_RelationDedup(t *testing.T) {
	src := newTestStore(t)
	dst := newTestStore(t)
	ctx := context.Background()

	const project = "dedup/relations"

	e1 := makeEntityInStore(t, src, project, "svc-a")
	e2 := makeEntityInStore(t, src, project, "svc-b")
	if _, err := src.CreateRelation(ctx, &model.Relation{SourceID: e1.ID, TargetID: e2.ID, Type: model.RelRelatedTo}); err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}

	var buf bytes.Buffer
	exp := mnemeSync.NewManifestExporter(src, "mneme", "test")
	if _, err := exp.Export(ctx, project, &buf); err != nil {
		t.Fatalf("Export: %v", err)
	}

	// First import should create entities + relation.
	imp := mnemeSync.NewManifestImporter(dst)
	r1, err := imp.Import(ctx, &buf)
	if err != nil {
		t.Fatalf("First Import: %v", err)
	}
	if r1.RelationsCreated != 1 {
		t.Errorf("first import RelationsCreated: got %d, want 1", r1.RelationsCreated)
	}

	// Re-export from src and import again — relation should be skipped.
	var buf2 bytes.Buffer
	exp2 := mnemeSync.NewManifestExporter(src, "mneme", "test")
	if _, err := exp2.Export(ctx, project, &buf2); err != nil {
		t.Fatalf("Re-export: %v", err)
	}
	r2, err := imp.Import(ctx, &buf2)
	if err != nil {
		t.Fatalf("Second Import: %v", err)
	}
	if r2.RelationsCreated != 0 {
		t.Errorf("second import RelationsCreated: got %d, want 0", r2.RelationsCreated)
	}
	if r2.RelationsSkipped != 1 {
		t.Errorf("second import RelationsSkipped: got %d, want 1", r2.RelationsSkipped)
	}
}

// TestManifestImport_SessionDedup verifies that sessions already present by ID
// are skipped on re-import.
func TestManifestImport_SessionDedup(t *testing.T) {
	src := newTestStore(t)
	dst := newTestStore(t)
	ctx := context.Background()

	const project = "dedup/sessions"
	makeSessionInStore(t, src, project)

	exportSession := func() bytes.Buffer {
		var buf bytes.Buffer
		exp := mnemeSync.NewManifestExporter(src, "mneme", "test")
		if _, err := exp.Export(ctx, project, &buf); err != nil {
			t.Fatalf("Export: %v", err)
		}
		return buf
	}

	imp := mnemeSync.NewManifestImporter(dst)

	buf1 := exportSession()
	r1, err := imp.Import(ctx, &buf1)
	if err != nil {
		t.Fatalf("First Import: %v", err)
	}
	if r1.SessionsCreated != 1 {
		t.Errorf("first SessionsCreated: got %d, want 1", r1.SessionsCreated)
	}

	buf2 := exportSession()
	r2, err := imp.Import(ctx, &buf2)
	if err != nil {
		t.Fatalf("Second Import: %v", err)
	}
	if r2.SessionsCreated != 0 {
		t.Errorf("second SessionsCreated: got %d, want 0", r2.SessionsCreated)
	}
	if r2.SessionsSkipped != 1 {
		t.Errorf("second SessionsSkipped: got %d, want 1", r2.SessionsSkipped)
	}
}

// TestManifestImport_UnknownVersion verifies that importing a manifest with an
// unknown version returns ErrUnsupportedManifestVersion without any side effects.
func TestManifestImport_UnknownVersion(t *testing.T) {
	dst := newTestStore(t)
	ctx := context.Background()

	// Build a fake manifest with version "2.0".
	root := mnemeSync.ManifestRoot{
		Version:    "2.0",
		ExportedAt: "2026-05-01T00:00:00Z",
		Producer:   mnemeSync.ManifestProducer{Name: "future-tool", Version: "2.0"},
		Project:    "test",
		Memories:   []*model.Memory{},
	}
	manifestJSON, _ := marshalManifestTarGz(t, root)

	imp := mnemeSync.NewManifestImporter(dst)
	_, err := imp.Import(ctx, bytes.NewReader(manifestJSON))
	if err == nil {
		t.Fatal("expected error for unknown version, got nil")
	}
	if !errors.Is(err, mnemeSync.ErrUnsupportedManifestVersion) {
		t.Errorf("error type: got %v, want ErrUnsupportedManifestVersion", err)
	}
}

// TestManifestImport_OrphanRelation verifies that a relation referencing a
// missing entity is skipped with a warning rather than failing the import.
func TestManifestImport_OrphanRelation(t *testing.T) {
	src := newTestStore(t)
	dst := newTestStore(t)
	ctx := context.Background()

	const project = "orphan/test"

	e1 := makeEntityInStore(t, src, project, "real-entity")
	e2 := makeEntityInStore(t, src, project, "another-entity")
	if _, err := src.CreateRelation(ctx, &model.Relation{SourceID: e1.ID, TargetID: e2.ID, Type: model.RelRelatedTo}); err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}

	var buf bytes.Buffer
	exp := mnemeSync.NewManifestExporter(src, "mneme", "test")
	if _, err := exp.Export(ctx, project, &buf); err != nil {
		t.Fatalf("Export: %v", err)
	}

	// Import entities only into dst (skip the relation's entity), then import
	// the full manifest — relation should be skipped.
	// We achieve this by importing normally (entities are imported first) and
	// then importing again with a store that has the entities — actually let's
	// test the case where entities were NOT imported first by using a different
	// approach: a manually crafted manifest with a relation referencing a fake ID.
	fakeRelManifest := mnemeSync.ManifestRoot{
		Version:    mnemeSync.ManifestVersion,
		ExportedAt: "2026-05-01T00:00:00Z",
		Producer:   mnemeSync.ManifestProducer{Name: "test", Version: "0.1"},
		Project:    project,
		Memories:   []*model.Memory{},
		Relations: []*model.Relation{
			{
				ID:       "00000000-0000-7000-0000-000000000099",
				SourceID: "00000000-0000-7000-0000-000000000001", // does not exist
				TargetID: "00000000-0000-7000-0000-000000000002", // does not exist
				Type:     model.RelRelatedTo,
				Weight:   0.5,
			},
		},
	}

	fakeArchive, _ := marshalManifestTarGz(t, fakeRelManifest)

	imp := mnemeSync.NewManifestImporter(dst)
	result, err := imp.Import(ctx, bytes.NewReader(fakeArchive))
	if err != nil {
		t.Fatalf("Import with orphan relation: %v", err)
	}

	if result.RelationsCreated != 0 {
		t.Errorf("RelationsCreated: got %d, want 0", result.RelationsCreated)
	}
	if result.RelationsSkipped != 1 {
		t.Errorf("RelationsSkipped: got %d, want 1", result.RelationsSkipped)
	}
}
