package sync

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/store"
)

// ManifestExporter writes a Memory Manifest v1.0 tarball. It queries a
// MemoryStore for all four exported types (memories, entities, relations,
// sessions) and assembles them into a single .manifest.tar.gz archive whose
// first entry is always manifest.json followed by the bundled schemas.
//
// The archive format is defined in SPEC-026 sections 3–4.
type ManifestExporter struct {
	s             *store.MemoryStore
	producerName  string
	producerVer   string
}

// NewManifestExporter constructs a ManifestExporter backed by s. producerName
// and producerVer appear in the manifest's producer block (e.g. "mneme", "1.0.0").
func NewManifestExporter(s *store.MemoryStore, producerName, producerVer string) *ManifestExporter {
	return &ManifestExporter{
		s:            s,
		producerName: producerName,
		producerVer:  producerVer,
	}
}

// Export serialises all active data for project as a Memory Manifest v1.0
// tarball written to w. The archive layout is:
//
//	manifest.json          (first entry — required by spec)
//	schemas/manifest.schema.json
//	schemas/memory.schema.json
//	schemas/entity.schema.json
//	schemas/relation.schema.json
//	schemas/session.schema.json
//
// Returns a ManifestExportResult describing what was written, or an error if
// any query or write step fails.
func (e *ManifestExporter) Export(ctx context.Context, project string, w io.Writer) (*ManifestExportResult, error) {
	// ── 1. Query all data ────────────────────────────────────────────────────
	memories, err := e.s.List(ctx, store.ListOptions{
		Project:           project,
		IncludeSuperseded: false,
		Limit:             100_000,
	})
	if err != nil {
		return nil, fmt.Errorf("sync: manifest export: list memories: %w", err)
	}

	entities, err := e.s.ListEntities(ctx, project, "", 0)
	if err != nil {
		return nil, fmt.Errorf("sync: manifest export: list entities: %w", err)
	}

	// ListEntities has an internal default limit of 50; for export we need all.
	// Re-query without limit by using a high cap if we hit the default.
	if len(entities) == 50 {
		entities, err = e.s.ListEntities(ctx, project, "", 1_000_000)
		if err != nil {
			return nil, fmt.Errorf("sync: manifest export: list entities (full): %w", err)
		}
	}

	relations, err := e.s.ListRelationsByProject(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("sync: manifest export: list relations: %w", err)
	}

	sessions, err := e.s.ListSessionsByProject(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("sync: manifest export: list sessions: %w", err)
	}

	// ── 2. Build root document ────────────────────────────────────────────────
	// Ensure all arrays are non-nil so they serialise as [] not null. This is
	// required for memories (schema required field) and expected for the rest.
	if memories == nil {
		memories = []*model.Memory{}
	}
	if entities == nil {
		entities = []*model.Entity{}
	}
	if relations == nil {
		relations = []*model.Relation{}
	}
	if sessions == nil {
		sessions = []*model.Session{}
	}

	now := time.Now().UTC()
	root := &ManifestRoot{
		Version:    ManifestVersion,
		ExportedAt: exportedAtString(now),
		Producer:   ManifestProducer{Name: e.producerName, Version: e.producerVer},
		Project:    project,
		Scope:      "project",
		Memories:   memories,
		Entities:   entities,
		Relations:  relations,
		Sessions:   sessions,
		Stats: &ManifestStats{
			MemoryCount:   len(memories),
			EntityCount:   len(entities),
			RelationCount: len(relations),
			SessionCount:  len(sessions),
		},
	}

	manifestJSON, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("sync: manifest export: marshal manifest: %w", err)
	}

	// ── 3. Write tar.gz ───────────────────────────────────────────────────────
	gz, err := gzip.NewWriterLevel(w, gzip.BestCompression)
	if err != nil {
		return nil, fmt.Errorf("sync: manifest export: create gzip writer: %w", err)
	}

	tw := tar.NewWriter(gz)

	// manifest.json MUST be the first tar entry (SPEC-026 §4).
	if err := writeTarEntry(tw, manifestFilename, manifestJSON); err != nil {
		return nil, fmt.Errorf("sync: manifest export: write manifest.json: %w", err)
	}

	// Bundle all schemas from the embedded FS.
	if err := bundleSchemas(tw); err != nil {
		return nil, fmt.Errorf("sync: manifest export: bundle schemas: %w", err)
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("sync: manifest export: close tar writer: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("sync: manifest export: close gzip writer: %w", err)
	}

	return &ManifestExportResult{
		Project:       project,
		MemoryCount:   len(memories),
		EntityCount:   len(entities),
		RelationCount: len(relations),
		SessionCount:  len(sessions),
		ExportedAt:    exportedAtString(now),
	}, nil
}

// ExportManifestToFile is a convenience wrapper that exports all data for
// project to <dir>/.mneme/sync/<project-slug>.manifest.tar.gz, creating parent
// directories as needed.
//
// Returns the absolute path of the written file, the export result, and any error.
func ExportManifestToFile(ctx context.Context, s *store.MemoryStore, producerName, producerVer, project, dir string) (string, *ManifestExportResult, error) {
	slug := projectSlug(project)
	syncDir := filepath.Join(dir, ".mneme", "sync")
	if err := os.MkdirAll(syncDir, 0o755); err != nil {
		return "", nil, fmt.Errorf("sync: export manifest to file: create directory: %w", err)
	}

	path := filepath.Join(syncDir, slug+manifestTarGzExtension)

	f, err := os.Create(path)
	if err != nil {
		return "", nil, fmt.Errorf("sync: export manifest to file: create file: %w", err)
	}
	defer f.Close()

	exp := NewManifestExporter(s, producerName, producerVer)
	result, err := exp.Export(ctx, project, f)
	if err != nil {
		return "", nil, err
	}

	return path, result, nil
}

// writeTarEntry adds a single file to tw with the given name and content.
// The header's ModTime is zero (reproducible builds) and Mode is 0644.
func writeTarEntry(tw *tar.Writer, name string, content []byte) error {
	hdr := &tar.Header{
		Name:    name,
		Size:    int64(len(content)),
		Mode:    0o644,
		ModTime: time.Time{},
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("write header for %s: %w", name, err)
	}
	if _, err := tw.Write(content); err != nil {
		return fmt.Errorf("write body for %s: %w", name, err)
	}
	return nil
}

// bundleSchemas walks the embedded schemaFS and writes every .schema.json file
// under schemas/ into the tar archive as schemas/<filename>.
func bundleSchemas(tw *tar.Writer) error {
	return fs.WalkDir(schemaFS, "schemas", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := schemaFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read embedded schema %s: %w", path, err)
		}
		return writeTarEntry(tw, path, data)
	})
}
