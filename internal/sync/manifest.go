package sync

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// manifestVersion is the current schema version for manifest files. Bump this
// when making backwards-incompatible changes to the manifest structure.
const manifestVersion = 1

// manifestFileName is the path relative to a project directory where the
// manifest is persisted.
const manifestFileName = ".mneme/sync/manifest.json"

// Manifest records every export performed from a directory so that tooling can
// quickly answer "what was last exported, and when?" without inspecting every
// .jsonl.gz file. It is written as a JSON file alongside the export archives.
type Manifest struct {
	// Version is the manifest schema version. Currently always 1.
	Version int `json:"version"`

	// Exports lists one entry per project that has been exported. Each entry is
	// updated (not appended) when the same project is exported again.
	Exports []ExportEntry `json:"exports"`
}

// ExportEntry describes a single project's most recent export.
type ExportEntry struct {
	// Project is the project slug as supplied to Export.
	Project string `json:"project"`

	// File is the path of the .jsonl.gz archive, relative to the directory that
	// contains the manifest.
	File string `json:"file"`

	// Count is the number of memories included in the archive.
	Count int `json:"count"`

	// ExportedAt is the RFC 3339 timestamp of the export.
	ExportedAt string `json:"exported_at"`
}

// LoadManifest reads the manifest from <dir>/.mneme/sync/manifest.json.
// If the file does not exist, a new empty Manifest (Version 1, no entries) is
// returned without error — callers need not distinguish a missing manifest from
// a freshly initialised one.
func LoadManifest(dir string) (*Manifest, error) {
	path := filepath.Join(dir, manifestFileName)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Manifest{Version: manifestVersion}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sync: load manifest: read: %w", err)
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("sync: load manifest: unmarshal: %w", err)
	}

	return &m, nil
}

// Save writes the manifest to <dir>/.mneme/sync/manifest.json, creating
// parent directories as needed. The file is written atomically by writing to a
// temporary file in the same directory and renaming it, preventing a partial
// manifest from being observed by concurrent readers.
func (m *Manifest) Save(dir string) error {
	syncDir := filepath.Join(dir, ".mneme", "sync")
	if err := os.MkdirAll(syncDir, 0o755); err != nil {
		return fmt.Errorf("sync: save manifest: create directory: %w", err)
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("sync: save manifest: marshal: %w", err)
	}

	// Write to a temp file then rename for atomic replacement.
	tmp, err := os.CreateTemp(syncDir, "manifest-*.json.tmp")
	if err != nil {
		return fmt.Errorf("sync: save manifest: create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("sync: save manifest: write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("sync: save manifest: close temp file: %w", err)
	}

	dest := filepath.Join(dir, manifestFileName)
	if err := os.Rename(tmpPath, dest); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("sync: save manifest: rename: %w", err)
	}

	return nil
}

// AddExport inserts or updates the ExportEntry for entry.Project. If an entry
// for that project already exists it is replaced in place, preserving the
// slice order for all other projects.
func (m *Manifest) AddExport(entry ExportEntry) {
	for idx, e := range m.Exports {
		if e.Project == entry.Project {
			m.Exports[idx] = entry
			return
		}
	}
	m.Exports = append(m.Exports, entry)
}
