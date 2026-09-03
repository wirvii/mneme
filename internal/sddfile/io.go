package sddfile

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WriteRecord writes data to path atomically: a hidden temp file in the
// same directory, followed by os.Rename. Copied in shape from
// internal/vault/writer.go's writeMemory — WITHOUT its updated_at
// idempotency probe (shouldSkip): D15 abandons "skip when already
// current" for the SDD mechanism (the file is always rewritten in full,
// D14), so there is nothing here to probe.
//
// Parent directories are created as needed (0o755). A failure at any step
// removes the temp file rather than leaving it behind.
func WriteRecord(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("sddfile: write record: mkdir %s: %w", dir, err)
	}

	tmpPath := filepath.Join(dir, "."+filepath.Base(path)+".tmp")

	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("sddfile: write record: open tmp %s: %w", tmpPath, err)
	}

	_, writeErr := f.Write(data)
	closeErr := f.Close()

	if writeErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("sddfile: write record: write %s: %w", tmpPath, writeErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("sddfile: write record: close %s: %w", tmpPath, closeErr)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("sddfile: write record: rename %s -> %s: %w", tmpPath, path, err)
	}
	return nil
}

// ReadRecord reads the raw bytes at path. A thin wrapper over os.ReadFile
// so callers of this package never import os directly for the common case.
//
// CRLF line endings are normalized to LF right here, at the byte-reading
// boundary — never inside parseFrontmatterBlock's line-by-line scan. A
// machine that cloned this repository with `core.autocrlf=true` sees every
// "\n" this package wrote turned into "\r\n" on checkout (SPEC-140 D12):
// trimming the "\r" per line while keeping parseFrontmatterBlock's
// `offset += len(lines[i]) + 1` arithmetic would shift the body's start by
// one byte per header line. Normalizing the whole buffer before anything
// else touches it sidesteps that arithmetic entirely — every downstream
// consumer (UnmarshalBacklog, UnmarshalSpec, the divergence comparison in
// scanSDDDivergent) sees plain LF, as if the file had never left this
// package's own writer.
func ReadRecord(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("sddfile: read record: %w", err)
	}
	return normalizeCRLF(data), nil
}

// normalizeCRLF rewrites every "\r\n" sequence in data to "\n". Only the
// exact two-byte sequence is touched (R2): a lone "\r" without a following
// "\n" is left alone, so a file that is genuinely corrupt in some other way
// keeps failing for that reason instead of being silently patched here.
func normalizeCRLF(data []byte) []byte {
	if !bytes.Contains(data, []byte("\r\n")) {
		return data
	}
	return bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
}

// CleanStaleTmp removes any leftover ".*.tmp" file under root — debris
// from a previous interrupted write. Copied in shape from
// internal/vault/writer.go's cleanStaleTmp. Non-fatal by construction: a
// WalkDir error on an individual entry is skipped, never propagated,
// because a partially-unreadable directory must not block the export it
// is meant to make safe.
func CleanStaleTmp(root string) error {
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") && strings.HasSuffix(d.Name(), ".tmp") {
			_ = os.Remove(path)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("sddfile: clean stale tmp: %w", err)
	}
	return nil
}

// ListRecords returns every "*.md" file path found by walking dir
// (typically RootDir(repoRoot)), sorted by the OS walk order (lexical —
// filepath.WalkDir's own documented guarantee), which is what makes
// mneme sdd status's file listing deterministic across runs without an
// extra sort step.
func ListRecords(dir string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(d.Name(), ".md") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("sddfile: list records: %w", err)
	}
	return paths, nil
}
