package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// managedBlockVersion is the current version of the managed block format.
// The version is embedded in the start marker so upgraders can detect stale blocks
// and re-inject without scanning the whole file.
const managedBlockVersion = 1

// managedBlockStart returns the start marker for the managed block at version v.
func managedBlockStart(v int) string {
	return fmt.Sprintf("<!-- mneme:managed:start v=%d -->", v)
}

// managedBlockEnd is the end marker for the managed block (version-independent).
const managedBlockEnd = "<!-- mneme:managed:end -->"

// legacyProtocolStart and legacyProtocolEnd are the markers used by the old
// InjectProtocol primitive. upsertManagedBlock detects and removes them as a
// one-time migration when the new managed block is installed.
const (
	legacyProtocolStart = "<!-- mneme:protocol:start -->"
	legacyProtocolEnd   = "<!-- mneme:protocol:end -->"
)

// upsertManagedBlock writes content into a single versioned managed block inside
// filePath. The block is delimited by "<!-- mneme:managed:start v=N -->" and
// "<!-- mneme:managed:end -->". Rules:
//
//   - If the file does not exist, it is created containing only the block.
//   - If the file exists and contains the managed markers, the entire block
//     (inclusive of markers) is replaced with the new content and the start
//     marker is refreshed to the current version.
//   - If the file exists but has no managed markers, the block is appended
//     (preceded by a blank line).
//
// Additionally, if the file contains the legacy "mneme:protocol" markers, they
// are removed before the new block is written (one-time migration).
//
// The operation is idempotent: running upsertManagedBlock twice with the same
// content produces a byte-identical file.
func upsertManagedBlock(filePath, content string) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return fmt.Errorf("install: upsert managed block: mkdir: %w", err)
	}

	block := managedBlockStart(managedBlockVersion) + "\n" + content + "\n" + managedBlockEnd

	existing, err := os.ReadFile(filePath)
	if os.IsNotExist(err) {
		// New file — write only the block.
		return os.WriteFile(filePath, []byte(block+"\n"), 0o644)
	}
	if err != nil {
		return fmt.Errorf("install: upsert managed block: read %s: %w", filePath, err)
	}

	text := string(existing)

	// One-time migration: remove legacy mneme:protocol block if present.
	text = removeLegacyProtocol(text)

	// Find the managed block markers (any version).
	startIdx, endIdx := findManagedBlock(text)
	if startIdx != -1 && endIdx != -1 {
		// Replace the existing managed block.
		before := text[:startIdx]
		after := text[endIdx+len(managedBlockEnd):]

		var b strings.Builder
		trimmed := strings.TrimRight(before, "\n")
		b.WriteString(trimmed)
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(block)
		afterTrimmed := strings.TrimLeft(after, "\n")
		if afterTrimmed != "" {
			b.WriteString("\n\n")
			b.WriteString(afterTrimmed)
		} else {
			b.WriteString("\n")
		}
		return os.WriteFile(filePath, []byte(b.String()), 0o644)
	}

	// No managed markers found — append (or create if text is now empty).
	var b strings.Builder
	trimmedText := strings.TrimRight(text, "\n")
	if trimmedText != "" {
		b.WriteString(trimmedText)
		b.WriteString("\n\n")
	}
	b.WriteString(block)
	b.WriteString("\n")
	return os.WriteFile(filePath, []byte(b.String()), 0o644)
}

// readManagedBlock reads the content between the managed block markers in filePath.
// content is the raw text between the start and end markers (exclusive).
// version is the version number parsed from the start marker (0 when not found).
// present reports whether a managed block was found.
func readManagedBlock(filePath string) (content string, version int, present bool, err error) {
	data, err := os.ReadFile(filePath)
	if os.IsNotExist(err) {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, fmt.Errorf("install: read managed block: %w", err)
	}

	text := string(data)
	startIdx, endIdx := findManagedBlock(text)
	if startIdx == -1 || endIdx == -1 {
		return "", 0, false, nil
	}

	// Extract the start marker line to parse the version.
	startLineEnd := strings.Index(text[startIdx:], "\n")
	if startLineEnd == -1 {
		return "", 0, false, nil
	}
	startMarker := text[startIdx : startIdx+startLineEnd]

	var v int
	_, parseErr := fmt.Sscanf(startMarker, "<!-- mneme:managed:start v=%d -->", &v)
	if parseErr != nil {
		v = 0
	}

	// Content is between the end of the start marker line and the end marker.
	bodyStart := startIdx + startLineEnd + 1 // skip the \n after start marker
	body := text[bodyStart:endIdx]
	body = strings.TrimRight(body, "\n")

	return body, v, true, nil
}

// findManagedBlock returns the byte offsets of the start and end markers in text.
// The start index points to the first character of the start marker; the end index
// points to the first character of the end marker. Returns (-1, -1) when not found.
// This function matches any version of the start marker.
func findManagedBlock(text string) (startIdx, endIdx int) {
	// The start marker prefix is stable regardless of version.
	const prefix = "<!-- mneme:managed:start"
	startIdx = strings.Index(text, prefix)
	if startIdx == -1 {
		return -1, -1
	}

	// Verify the start marker line ends with " -->".
	lineEnd := strings.Index(text[startIdx:], "\n")
	if lineEnd == -1 {
		return -1, -1
	}
	startLine := text[startIdx : startIdx+lineEnd]
	if !strings.HasSuffix(startLine, " -->") {
		return -1, -1
	}

	endIdx = strings.Index(text, managedBlockEnd)
	if endIdx == -1 || endIdx < startIdx {
		return -1, -1
	}
	return startIdx, endIdx
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
