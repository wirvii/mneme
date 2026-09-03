// Package managedblock implements the marker-fenced idempotent block upsert
// primitive shared by mneme's installers. A "managed block" is a region of a
// text file delimited by a versioned start marker and a fixed end marker,
// e.g. "<!-- mneme:managed:start v=1 -->" ... "<!-- mneme:managed:end -->".
// Content inside the markers is fully owned by the caller and replaced on
// every upsert; content outside the markers (user prose, other sections) is
// always preserved byte-for-byte.
//
// This package is a leaf: stdlib only, no imports of other internal mneme
// packages. It exists so that adapters at the edge (internal/install today,
// internal/subagents in the future) can reuse the same idempotent primitive
// without depending on each other.
package managedblock

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// StartMarker returns the opening delimiter for a managed block identified by
// marker at the given version, e.g. StartMarker("managed", 1) returns
// "<!-- mneme:managed:start v=1 -->".
func StartMarker(marker string, version int) string {
	return fmt.Sprintf("<!-- mneme:%s:start v=%d -->", marker, version)
}

// EndMarker returns the closing delimiter for a managed block identified by
// marker, e.g. EndMarker("managed") returns "<!-- mneme:managed:end -->".
// The end marker is version-independent so a stale block can always be found
// and replaced regardless of which version originally wrote it.
func EndMarker(marker string) string {
	return fmt.Sprintf("<!-- mneme:%s:end -->", marker)
}

// StartMarkerPrefix returns the version-independent prefix every start
// marker for marker begins with, e.g. StartMarkerPrefix("managed") returns
// "<!-- mneme:managed:start". It exists so a caller counting occurrences of
// the start marker (SPEC-140 AC4 — proving UpsertText never duplicates a
// block) never has to duplicate the literal marker syntax in its own code:
// findBlock already searches for exactly this prefix.
func StartMarkerPrefix(marker string) string {
	return "<!-- mneme:" + marker + ":start"
}

// UpsertText returns the result of upserting content into a single versioned
// block (identified by marker) within text. Rules:
//
//   - If text is empty, the result contains only the block.
//   - If text contains the marker's start/end delimiters (any version), the
//     entire block (inclusive of markers) is replaced with the new content
//     and the start marker is refreshed to version.
//   - If text does not contain the marker's delimiters, the block is
//     appended (preceded by a blank line when text is non-empty).
//
// UpsertText is a pure function: it does no I/O and is safe to unit test in
// isolation. Upsert is the file-backed wrapper around it.
//
// The operation is idempotent: calling UpsertText twice with identical
// arguments produces byte-identical output.
//
// findBlock now tolerates a "\r\n"-terminated start marker line (SPEC-140
// D12), so replacing a block inside a file that was checked out with
// core.autocrlf=true is found and replaced rather than appended as a
// duplicate. The declared trade-off: the block this function writes always
// uses "\n", so a CRLF file ends up with mixed line endings after a
// replace — the new block in LF, the rest of the file still in CRLF. Left
// deliberately: git normalizes on the next `commit` under
// `core.autocrlf`, so it produces no diff noise, and it is incomparably
// better than the alternative it replaces (a duplicated block). Do not
// "fix" this by rewriting the whole file to one line ending — that is out
// of scope here and belongs to whatever wrote the file's line endings in
// the first place.
func UpsertText(text, marker string, version int, content string) string {
	end := EndMarker(marker)
	block := StartMarker(marker, version) + "\n" + content + "\n" + end

	startIdx, endIdx := findBlock(text, marker)
	if startIdx != -1 && endIdx != -1 {
		before := text[:startIdx]
		after := text[endIdx+len(end):]

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
		return b.String()
	}

	// No existing block found — append (or start fresh if text is empty).
	var b strings.Builder
	trimmedText := strings.TrimRight(text, "\n")
	if trimmedText != "" {
		b.WriteString(trimmedText)
		b.WriteString("\n\n")
	}
	b.WriteString(block)
	b.WriteString("\n")
	return b.String()
}

// ReadText returns the content between the marker's delimiters in text.
// content is the raw text between the start and end markers (exclusive).
// version is the version number parsed from the start marker (0 when not
// found or unparsable). present reports whether the block was found.
func ReadText(text, marker string) (content string, version int, present bool) {
	startIdx, endIdx := findBlock(text, marker)
	if startIdx == -1 || endIdx == -1 {
		return "", 0, false
	}

	startLineEnd := strings.Index(text[startIdx:], "\n")
	if startLineEnd == -1 {
		return "", 0, false
	}
	// SPEC-140 D12: trim a trailing "\r" before Sscanf, same reasoning as
	// findBlock's own suffix check — a CRLF file otherwise leaves "\r"
	// glued to the closing "-->", which Sscanf's literal format string does
	// not match, so the version silently reads back as 0.
	startLine := strings.TrimSuffix(text[startIdx:startIdx+startLineEnd], "\r")

	var v int
	scanFormat := fmt.Sprintf("<!-- mneme:%s:start v=%%d -->", marker)
	if _, err := fmt.Sscanf(startLine, scanFormat, &v); err != nil {
		v = 0
	}

	bodyStart := startIdx + startLineEnd + 1 // skip the \n after the start marker
	body := text[bodyStart:endIdx]
	body = strings.TrimRight(body, "\n")

	return body, v, true
}

// findBlock returns the byte offsets of the start and end markers for marker
// in text. The start index points to the first character of the start
// marker; the end index points to the first character of the end marker.
// Returns (-1, -1) when the block is not found. Matches any version of the
// start marker.
func findBlock(text, marker string) (startIdx, endIdx int) {
	prefix := StartMarkerPrefix(marker)
	startIdx = strings.Index(text, prefix)
	if startIdx == -1 {
		return -1, -1
	}

	lineEnd := strings.Index(text[startIdx:], "\n")
	if lineEnd == -1 {
		return -1, -1
	}
	startLine := text[startIdx : startIdx+lineEnd]
	// SPEC-140 D12: a file checked out with core.autocrlf=true has "\r\n"
	// line endings, so startLine ends in " -->\r" here — trim it before the
	// suffix check, or every managed block in a CRLF file reads as absent
	// (findBlock returns (-1,-1)), which is what made UpsertText append a
	// duplicate block instead of replacing the existing one.
	startLine = strings.TrimSuffix(startLine, "\r")
	if !strings.HasSuffix(startLine, " -->") {
		return -1, -1
	}

	end := EndMarker(marker)
	endIdx = strings.Index(text, end)
	if endIdx == -1 || endIdx < startIdx {
		return -1, -1
	}
	return startIdx, endIdx
}

// RemoveText returns text with marker's managed block (any version) removed
// entirely — start marker, content, and end marker — while preserving
// everything outside it byte-for-byte (module leading/trailing blank-line
// normalisation, matching UpsertText's own whitespace handling). Idempotent:
// calling RemoveText on text that does not contain marker's block returns
// text unchanged. Other markers' blocks are left untouched.
func RemoveText(text, marker string) string {
	startIdx, endIdx := findBlock(text, marker)
	if startIdx == -1 || endIdx == -1 {
		return text
	}

	end := EndMarker(marker)
	before := strings.TrimRight(text[:startIdx], "\n")
	after := strings.TrimLeft(text[endIdx+len(end):], "\n")

	if before == "" {
		return after
	}
	if after == "" {
		return before + "\n"
	}
	return before + "\n\n" + after
}

// Remove is the file-backed wrapper around RemoveText: it reads filePath,
// removes marker's managed block, and writes the result back. A missing
// file, or a file that does not contain marker's block, is a no-op (no
// error) — Remove is safe to call unconditionally during a profile
// deactivate/switch.
func Remove(filePath, marker string) error {
	text, err := readFileOrEmpty(filePath)
	if err != nil {
		return fmt.Errorf("managedblock: remove: read %s: %w", filePath, err)
	}
	if text == "" {
		return nil
	}

	result := RemoveText(text, marker)
	if result == text {
		return nil
	}

	if err := os.WriteFile(filePath, []byte(result), 0o644); err != nil {
		return fmt.Errorf("managedblock: remove: write %s: %w", filePath, err)
	}
	return nil
}

// Upsert is the file-backed wrapper around UpsertText: it reads filePath (a
// missing file is treated as empty text), computes the upserted result, and
// writes it back. Parent directories are created as needed.
func Upsert(filePath, marker string, version int, content string) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return fmt.Errorf("managedblock: upsert: mkdir: %w", err)
	}

	text, err := readFileOrEmpty(filePath)
	if err != nil {
		return fmt.Errorf("managedblock: upsert: read %s: %w", filePath, err)
	}

	result := UpsertText(text, marker, version, content)
	if err := os.WriteFile(filePath, []byte(result), 0o644); err != nil {
		return fmt.Errorf("managedblock: upsert: write %s: %w", filePath, err)
	}
	return nil
}

// Read is the file-backed wrapper around ReadText. A missing file returns
// present=false and a nil error (not an error condition).
func Read(filePath, marker string) (content string, version int, present bool, err error) {
	data, readErr := os.ReadFile(filePath)
	if os.IsNotExist(readErr) {
		return "", 0, false, nil
	}
	if readErr != nil {
		return "", 0, false, fmt.Errorf("managedblock: read %s: %w", filePath, readErr)
	}

	content, version, present = ReadText(string(data), marker)
	return content, version, present, nil
}

// readFileOrEmpty reads filePath, returning an empty string (no error) when
// the file does not exist.
func readFileOrEmpty(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}
