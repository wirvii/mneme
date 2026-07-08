package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/juanftp/mneme/internal/managedblock"
)

// managedBlockMarker identifies the mneme managed block used in CLAUDE.md /
// AGENTS.md style files (operating manual, project manual). It is passed to
// internal/managedblock as the marker name.
const managedBlockMarker = "managed"

// managedBlockVersion is the current version of the managed block format.
// The version is embedded in the start marker so upgraders can detect stale
// blocks and re-inject without scanning the whole file.
const managedBlockVersion = 1

// managedBlockStart returns the start marker for the managed block at
// version v. Kept as a package-local helper (backed by internal/managedblock)
// because existing tests in this package reference it directly.
func managedBlockStart(v int) string {
	return managedblock.StartMarker(managedBlockMarker, v)
}

// managedBlockEnd is the end marker for the managed block (version-independent).
const managedBlockEnd = "<!-- mneme:managed:end -->"

// legacyProtocolStart and legacyProtocolEnd are the markers used by the old
// InjectProtocol primitive. upsertManagedBlock detects and removes them as a
// one-time migration when the new managed block is installed. This migration
// is specific to mneme's CLAUDE.md/AGENTS.md history and therefore lives here
// rather than in the generic internal/managedblock leaf.
const (
	legacyProtocolStart = "<!-- mneme:protocol:start -->"
	legacyProtocolEnd   = "<!-- mneme:protocol:end -->"
)

// upsertManagedBlock writes content into the mneme managed block inside
// filePath, delegating the marker-fenced idempotent upsert to
// internal/managedblock. Before doing so, it performs a one-time migration:
// if filePath contains the legacy "mneme:protocol" markers, they are removed
// so only the new managed block remains.
//
// The operation is idempotent: running upsertManagedBlock twice with the same
// content produces a byte-identical file.
func upsertManagedBlock(filePath, content string) error {
	data, err := os.ReadFile(filePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("install: upsert managed block: read %s: %w", filePath, err)
	}

	text := ""
	if err == nil {
		text = removeLegacyProtocol(string(data))
	}

	result := managedblock.UpsertText(text, managedBlockMarker, managedBlockVersion, content)

	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return fmt.Errorf("install: upsert managed block: mkdir: %w", err)
	}
	if err := os.WriteFile(filePath, []byte(result), 0o644); err != nil {
		return fmt.Errorf("install: upsert managed block: write %s: %w", filePath, err)
	}
	return nil
}

// readManagedBlock reads the content between the managed block markers in
// filePath, delegating to internal/managedblock.
func readManagedBlock(filePath string) (content string, version int, present bool, err error) {
	return managedblock.Read(filePath, managedBlockMarker)
}

// removeLegacyProtocol strips the legacy mneme:protocol block from text.
// If the legacy markers are not present, text is returned unchanged.
// The returned string may have leading/trailing newlines that the caller
// should trim before inserting new content.
func removeLegacyProtocol(text string) string {
	startIdx := strings.Index(text, legacyProtocolStart)
	if startIdx == -1 {
		return text
	}
	endIdx := strings.Index(text, legacyProtocolEnd)
	if endIdx == -1 || endIdx < startIdx {
		return text
	}

	before := text[:startIdx]
	after := text[endIdx+len(legacyProtocolEnd):]

	// Trim trailing newlines from before and leading newlines from after,
	// then reassemble with at most one blank line between them.
	beforeTrimmed := strings.TrimRight(before, "\n")
	afterTrimmed := strings.TrimLeft(after, "\n")

	switch {
	case beforeTrimmed != "" && afterTrimmed != "":
		return beforeTrimmed + "\n\n" + afterTrimmed
	case beforeTrimmed != "":
		return beforeTrimmed + "\n"
	case afterTrimmed != "":
		return afterTrimmed
	default:
		return ""
	}
}
