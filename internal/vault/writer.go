package vault

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/juanftp/mneme/internal/model"
)

// ExportOptions controls how memories are written to the vault.
type ExportOptions struct {
	// VaultRoot is the root directory of the vault
	// (e.g. ~/.mneme/vaults/wirvii-mneme). It is created if it does not exist.
	VaultRoot string

	// Project is the project slug recorded in the marker file.
	Project string

	// Scope identifies whether this is a "project" or "global" vault.
	// Used in the marker file; does not affect path layout.
	Scope string

	// DryRun performs full analysis but writes nothing when true.
	DryRun bool

	// IncludeSuperseded exports superseded memories when true.
	// Soft-deleted memories are never exported regardless of this flag.
	IncludeSuperseded bool
}

// ExportResult summarises a vault export operation.
type ExportResult struct {
	// VaultRoot is the absolute path to the vault root.
	VaultRoot string

	// Total is the number of memories evaluated.
	Total int

	// Written is the number of .md files written (created or updated).
	Written int

	// Skipped is the number of memories whose vault file was already up to date.
	Skipped int

	// Errors is the number of individual memory writes that failed (non-fatal).
	// Export continues after per-memory errors and reports the count here.
	Errors int

	// Paths lists up to 20 file paths that were written (or would be written
	// in dry-run mode). Paths are relative to VaultRoot.
	Paths []string
}

// VaultMarker is the JSON structure of the .mneme-vault marker file written at
// the vault root. It lets subsequent exports verify that the vault belongs to
// the expected project before overwriting files.
type VaultMarker struct {
	VaultVersion int    `json:"vault_version"`
	Project      string `json:"project"`
	Scope        string `json:"scope"`
	CreatedAt    string `json:"created_at"`
	LastExportAt string `json:"last_export_at"`
	MemoryCount  int    `json:"memory_count"`
}

// markerFile is the name of the marker file at the vault root.
const markerFile = ".mneme-vault"

// Writer writes memory files into a vault directory using atomic tmp+rename
// writes and updated_at-based idempotency checks.
type Writer struct {
	opts ExportOptions
	// usedPaths tracks path -> memoryID to detect collisions during a single
	// export run. Collisions receive a "-2", "-3", … suffix.
	usedPaths map[string]string
}

// NewWriter creates a Writer for the given options. Call Export to run a full
// export of a memory slice, or call WriteMemory/WriteMarker individually.
func NewWriter(opts ExportOptions) *Writer {
	return &Writer{
		opts:      opts,
		usedPaths: make(map[string]string),
	}
}

// Export writes memories to the vault and returns a summary result.
//
// Steps:
//  1. Verify or initialise the vault root.
//  2. Read the existing marker file (if any) and abort if the project mismatches.
//  3. Clean up stale .*.tmp files from a previous interrupted export.
//  4. Write each memory file using atomic tmp+rename and updated_at idempotency.
//  5. Write the marker file last.
func (w *Writer) Export(memories []*model.Memory) (*ExportResult, error) {
	result := &ExportResult{VaultRoot: w.opts.VaultRoot}

	if !w.opts.DryRun {
		if err := os.MkdirAll(w.opts.VaultRoot, 0o755); err != nil {
			return nil, fmt.Errorf("vault: export: create vault root %q: %w", w.opts.VaultRoot, err)
		}

		// Verify marker file does not belong to a different project.
		if err := w.checkMarker(); err != nil {
			return nil, err
		}

		// Clean up stale .*.tmp files from previous interrupted exports.
		if err := cleanStaleTmp(w.opts.VaultRoot); err != nil {
			// Non-fatal — log and continue.
			fmt.Fprintf(os.Stderr, "vault: export: cleanup stale tmp files: %v\n", err)
		}
	}

	for _, m := range memories {
		result.Total++

		written, relPath, err := w.writeMemory(m)
		if err != nil {
			result.Errors++
			fmt.Fprintf(os.Stderr, "vault: export: write memory %s: %v\n", m.ID, err)
			continue
		}
		if written {
			result.Written++
			if len(result.Paths) < 20 {
				result.Paths = append(result.Paths, relPath)
			}
		} else {
			result.Skipped++
		}
	}

	if !w.opts.DryRun {
		if err := w.writeMarker(result); err != nil {
			return result, fmt.Errorf("vault: export: write marker: %w", err)
		}
	}

	return result, nil
}

// WriteMemory writes a single memory to the vault. It is safe to call directly
// when the caller manages iteration. Returns true if the file was written
// (created or updated), false if it was skipped (already up to date).
// The returned string is the path relative to the vault root.
func (w *Writer) WriteMemory(m *model.Memory) (written bool, relPath string, err error) {
	return w.writeMemory(m)
}

// WriteMarker writes the .mneme-vault marker file using an ExportResult.
func (w *Writer) WriteMarker(result *ExportResult) error {
	return w.writeMarker(result)
}

// writeMemory is the internal implementation of WriteMemory.
func (w *Writer) writeMemory(m *model.Memory) (written bool, relPath string, err error) {
	relPath = w.resolveCollision(MemoryPath(m), m.ID)

	if w.opts.DryRun {
		// In dry-run mode, register the path as used and report written=true
		// without touching the filesystem.
		w.usedPaths[relPath] = m.ID
		return true, relPath, nil
	}

	absPath := filepath.Join(w.opts.VaultRoot, relPath)

	// Idempotency check: if the file exists and its updated_at >= memory's,
	// skip writing.
	if skip := w.shouldSkip(absPath, m.UpdatedAt); skip {
		return false, relPath, nil
	}

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return false, relPath, fmt.Errorf("mkdir %s: %w", filepath.Dir(absPath), err)
	}

	// Write to a hidden tmp file in the same directory for atomic rename.
	tmpPath := filepath.Join(filepath.Dir(absPath), "."+filepath.Base(absPath)+".tmp")

	tmpFile, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return false, relPath, fmt.Errorf("open tmp file %s: %w", tmpPath, err)
	}

	writeErr := writeNote(tmpFile, m)
	closeErr := tmpFile.Close()

	if writeErr != nil {
		_ = os.Remove(tmpPath)
		return false, relPath, fmt.Errorf("write note content: %w", writeErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return false, relPath, fmt.Errorf("close tmp file: %w", closeErr)
	}

	if err := os.Rename(tmpPath, absPath); err != nil {
		_ = os.Remove(tmpPath)
		return false, relPath, fmt.Errorf("rename %s -> %s: %w", tmpPath, absPath, err)
	}

	return true, relPath, nil
}

// resolveCollision checks whether relPath is already registered for a different
// memory in this export run. If there is a collision it appends "-2", "-3", …
// before the .md extension until an unused path is found.
func (w *Writer) resolveCollision(relPath, memID string) string {
	if existingID, taken := w.usedPaths[relPath]; !taken || existingID == memID {
		w.usedPaths[relPath] = memID
		return relPath
	}

	base := strings.TrimSuffix(relPath, ".md")
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d.md", base, n)
		if existingID, taken := w.usedPaths[candidate]; !taken || existingID == memID {
			w.usedPaths[candidate] = memID
			return candidate
		}
	}
}

// shouldSkip returns true when the file at absPath already has an updated_at
// timestamp >= the memory's UpdatedAt, meaning the on-disk version is current.
func (w *Writer) shouldSkip(absPath string, memUpdatedAt time.Time) bool {
	f, err := os.Open(absPath)
	if err != nil {
		// File does not exist or cannot be opened — don't skip.
		return false
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return false
	}

	fileUpdatedAt, ok := parseUpdatedAt(buf[:n])
	if !ok {
		return false
	}

	return !fileUpdatedAt.Before(memUpdatedAt)
}

// writeNote writes the complete vault note (frontmatter + blank line + content)
// for m to w.
func writeNote(w io.Writer, m *model.Memory) error {
	fm := FromMemory(m)
	if _, err := fm.WriteTo(w); err != nil {
		return fmt.Errorf("write frontmatter: %w", err)
	}

	if _, err := fmt.Fprintf(w, "\n%s\n", m.Content); err != nil {
		return fmt.Errorf("write content: %w", err)
	}

	return nil
}

// writeMarker writes the .mneme-vault JSON marker file to the vault root.
func (w *Writer) writeMarker(result *ExportResult) error {
	now := time.Now().UTC().Format(time.RFC3339)

	marker := VaultMarker{
		VaultVersion: 1,
		Project:      w.opts.Project,
		Scope:        w.opts.Scope,
		LastExportAt: now,
		MemoryCount:  result.Total - result.Errors,
	}

	// Preserve the original created_at if the marker already exists.
	existing, _, _ := w.readMarker()
	if existing != nil && existing.CreatedAt != "" {
		marker.CreatedAt = existing.CreatedAt
	} else {
		marker.CreatedAt = now
	}

	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal marker: %w", err)
	}

	absPath := filepath.Join(w.opts.VaultRoot, markerFile)
	tmpPath := absPath + ".tmp"

	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write tmp marker: %w", err)
	}

	if err := os.Rename(tmpPath, absPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename marker: %w", err)
	}

	return nil
}

// checkMarker reads the existing marker file (if any) and returns an error when
// the vault belongs to a different project than w.opts.Project.
func (w *Writer) checkMarker() error {
	marker, _, err := w.readMarker()
	if err != nil || marker == nil {
		// No marker or unreadable — allow export to create a new one.
		return nil
	}

	if marker.Project != "" && w.opts.Project != "" && marker.Project != w.opts.Project {
		return fmt.Errorf("vault at %q belongs to project %q, not %q — use a different --output directory",
			w.opts.VaultRoot, marker.Project, w.opts.Project)
	}

	return nil
}

// readMarker reads and parses the .mneme-vault JSON marker at the vault root.
// Returns nil, nil if the file does not exist.
func (w *Writer) readMarker() (*VaultMarker, []byte, error) {
	absPath := filepath.Join(w.opts.VaultRoot, markerFile)
	data, err := os.ReadFile(absPath)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read marker: %w", err)
	}

	var m VaultMarker
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, data, fmt.Errorf("parse marker: %w", err)
	}

	return &m, data, nil
}

// cleanStaleTmp removes any .*.tmp files under root that were left by a
// previous interrupted export.
func cleanStaleTmp(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if !d.IsDir() && strings.HasPrefix(d.Name(), ".") && strings.HasSuffix(d.Name(), ".tmp") {
			_ = os.Remove(path)
		}
		return nil
	})
}
