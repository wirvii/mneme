package sync_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/wirvii/mneme/internal/model"
	mnemeSync "github.com/wirvii/mneme/internal/sync"
)

// makeEntityInStore creates a named entity in the store for tests.
func makeEntityInStore(t *testing.T, s interface {
	CreateEntity(context.Context, *model.Entity) (*model.Entity, error)
}, project, name string) *model.Entity {
	t.Helper()
	ctx := context.Background()
	e := &model.Entity{Name: name, Kind: model.KindConcept, Project: project}
	created, err := s.CreateEntity(ctx, e)
	if err != nil {
		t.Fatalf("CreateEntity %q: %v", name, err)
	}
	return created
}

// makeSessionInStore inserts a session directly into the store.
func makeSessionInStore(t *testing.T, s interface {
	CreateSession(context.Context, *model.Session) (*model.Session, error)
}, project string) *model.Session {
	t.Helper()
	ctx := context.Background()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	sess := &model.Session{
		ID:        id.String(),
		Project:   project,
		Agent:     "claude-code",
		StartedAt: time.Now().UTC(),
	}
	got, err := s.CreateSession(ctx, sess)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return got
}

// extractManifestTarGz opens a .manifest.tar.gz stream and returns the
// map[entryName]content of all entries.
func extractManifestTarGz(t *testing.T, r io.Reader) map[string][]byte {
	t.Helper()

	gz, err := gzip.NewReader(r)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	entries := make(map[string][]byte)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next: %v", err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read tar entry %q: %v", hdr.Name, err)
		}
		entries[hdr.Name] = data
	}

	return entries
}

// TestManifestExport_MemoriesOnly verifies that export succeeds when entities,
// relations and sessions are empty — memories-only is the minimal valid case.
func TestManifestExport_MemoriesOnly(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const project = "test/project"
	mem, err := s.Create(ctx, makeMemory(project))
	if err != nil {
		t.Fatalf("Create memory: %v", err)
	}

	var buf bytes.Buffer
	exp := mnemeSync.NewManifestExporter(s, "mneme", "test")
	result, err := exp.Export(ctx, project, &buf)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	if result.MemoryCount != 1 {
		t.Errorf("MemoryCount: got %d, want 1", result.MemoryCount)
	}
	if result.EntityCount != 0 {
		t.Errorf("EntityCount: got %d, want 0", result.EntityCount)
	}
	if result.Project != project {
		t.Errorf("Project: got %q, want %q", result.Project, project)
	}

	entries := extractManifestTarGz(t, &buf)
	manifestData, ok := entries["manifest.json"]
	if !ok {
		t.Fatal("manifest.json not found in archive")
	}

	var root mnemeSync.ManifestRoot
	if err := json.Unmarshal(manifestData, &root); err != nil {
		t.Fatalf("unmarshal manifest.json: %v", err)
	}

	if root.Version != mnemeSync.ManifestVersion {
		t.Errorf("version: got %q, want %q", root.Version, mnemeSync.ManifestVersion)
	}
	if len(root.Memories) != 1 {
		t.Fatalf("memories len: got %d, want 1", len(root.Memories))
	}
	if root.Memories[0].ID != mem.ID {
		t.Errorf("memory ID: got %q, want %q", root.Memories[0].ID, mem.ID)
	}
}

// TestManifestExport_FullData verifies that memories, entities, relations, and
// sessions are all exported correctly.
func TestManifestExport_FullData(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const project = "full/data"

	// Create one memory, two entities, one relation, one session.
	if _, err := s.Create(ctx, makeMemory(project)); err != nil {
		t.Fatalf("Create memory: %v", err)
	}

	e1 := makeEntityInStore(t, s, project, "entity-one")
	e2 := makeEntityInStore(t, s, project, "entity-two")

	rel := &model.Relation{SourceID: e1.ID, TargetID: e2.ID, Type: model.RelRelatedTo}
	if _, err := s.CreateRelation(ctx, rel); err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}

	makeSessionInStore(t, s, project)

	var buf bytes.Buffer
	exp := mnemeSync.NewManifestExporter(s, "mneme", "test")
	result, err := exp.Export(ctx, project, &buf)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	if result.MemoryCount != 1 {
		t.Errorf("MemoryCount: got %d, want 1", result.MemoryCount)
	}
	if result.EntityCount != 2 {
		t.Errorf("EntityCount: got %d, want 2", result.EntityCount)
	}
	if result.RelationCount != 1 {
		t.Errorf("RelationCount: got %d, want 1", result.RelationCount)
	}
	if result.SessionCount != 1 {
		t.Errorf("SessionCount: got %d, want 1", result.SessionCount)
	}

	entries := extractManifestTarGz(t, &buf)

	var root mnemeSync.ManifestRoot
	if err := json.Unmarshal(entries["manifest.json"], &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if root.Stats == nil {
		t.Fatal("Stats is nil")
	}
	if root.Stats.MemoryCount != 1 || root.Stats.EntityCount != 2 ||
		root.Stats.RelationCount != 1 || root.Stats.SessionCount != 1 {
		t.Errorf("Stats mismatch: %+v", root.Stats)
	}
}

// TestManifestExport_TarEntryOrder verifies that manifest.json is always the
// first entry in the tar stream. The spec (SPEC-026 §7.7) requires this so
// consumers can read metadata without scanning the full archive.
func TestManifestExport_TarEntryOrder(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const project = "order/test"
	if _, err := s.Create(ctx, makeMemory(project)); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var buf bytes.Buffer
	exp := mnemeSync.NewManifestExporter(s, "mneme", "test")
	if _, err := exp.Export(ctx, project, &buf); err != nil {
		t.Fatalf("Export: %v", err)
	}

	gz, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	first, err := tr.Next()
	if err != nil {
		t.Fatalf("first tar entry: %v", err)
	}

	if first.Name != "manifest.json" {
		t.Errorf("first entry: got %q, want %q", first.Name, "manifest.json")
	}
}

// TestManifestExport_SchemasEmbedded verifies that the five schema files are
// bundled in the archive so consumers can validate offline.
func TestManifestExport_SchemasEmbedded(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const project = "schema/test"

	var buf bytes.Buffer
	exp := mnemeSync.NewManifestExporter(s, "mneme", "test")
	if _, err := exp.Export(ctx, project, &buf); err != nil {
		t.Fatalf("Export: %v", err)
	}

	entries := extractManifestTarGz(t, &buf)

	wantSchemas := []string{
		"schemas/manifest.schema.json",
		"schemas/memory.schema.json",
		"schemas/entity.schema.json",
		"schemas/relation.schema.json",
		"schemas/session.schema.json",
	}

	for _, name := range wantSchemas {
		data, ok := entries[name]
		if !ok {
			t.Errorf("schema %q missing from archive", name)
			continue
		}
		// Each schema must be valid JSON containing a "$schema" field.
		var schema map[string]json.RawMessage
		if err := json.Unmarshal(data, &schema); err != nil {
			t.Errorf("schema %q is not valid JSON: %v", name, err)
			continue
		}
		if _, ok := schema["$schema"]; !ok {
			t.Errorf("schema %q missing $schema field", name)
		}
	}
}

// TestManifestExport_ProducerField verifies that the producer name and version
// are included in the manifest.json.
func TestManifestExport_ProducerField(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const project = "prod/test"

	var buf bytes.Buffer
	exp := mnemeSync.NewManifestExporter(s, "my-tool", "2.3.4")
	if _, err := exp.Export(ctx, project, &buf); err != nil {
		t.Fatalf("Export: %v", err)
	}

	entries := extractManifestTarGz(t, &buf)
	var root mnemeSync.ManifestRoot
	if err := json.Unmarshal(entries["manifest.json"], &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if root.Producer.Name != "my-tool" {
		t.Errorf("Producer.Name: got %q, want %q", root.Producer.Name, "my-tool")
	}
	if root.Producer.Version != "2.3.4" {
		t.Errorf("Producer.Version: got %q, want %q", root.Producer.Version, "2.3.4")
	}
}

// TestManifestExport_EmptyDB verifies that export succeeds even when the
// database has no records at all — an empty manifest is valid.
func TestManifestExport_EmptyDB(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	var buf bytes.Buffer
	exp := mnemeSync.NewManifestExporter(s, "mneme", "test")
	result, err := exp.Export(ctx, "empty/project", &buf)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	if result.MemoryCount != 0 || result.EntityCount != 0 {
		t.Errorf("expected all counts 0, got memories=%d entities=%d",
			result.MemoryCount, result.EntityCount)
	}

	entries := extractManifestTarGz(t, &buf)
	if _, ok := entries["manifest.json"]; !ok {
		t.Fatal("manifest.json missing from empty-DB archive")
	}

	var root mnemeSync.ManifestRoot
	if err := json.Unmarshal(entries["manifest.json"], &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if root.Memories == nil {
		t.Error("Memories must be non-nil (empty array) for empty export")
	}
}
