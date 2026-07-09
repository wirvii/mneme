// Package vault provides a filesystem-backed markdown mirror of mneme memories.
// Each memory is written as an individual .md file with YAML frontmatter under a
// directory tree rooted at a user-chosen vault root. The tree structure mirrors
// topic_key segments so the vault is navigable in any file manager or Obsidian.
//
// This package is write-only in SPEC-M1. The bidirectional watcher (SPEC-M2) will
// extend it with filesystem-to-SQLite sync. Imports flow inward: vault imports
// only model (the leaf). Service and store layers call vault, never the reverse.
package vault

import (
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/juanftp/mneme/internal/model"
)

const (
	// maxSegmentLen is the maximum number of characters allowed in a single
	// path segment after sanitization. Leaves headroom below the 255-byte
	// limit most filesystems impose per component.
	maxSegmentLen = 200

	// maxPathLen is the total path length (relative to vault root) beyond which
	// a memory falls back to the no-topic-key path. macOS caps at ~1024 total;
	// Linux at ~4096. We use 900 to stay comfortably below both.
	maxPathLen = 900

	// noTopicDir is the subdirectory holding memories that have no topic_key.
	noTopicDir = "notes/_no-topic"
)

// MemoryPath returns the filesystem path relative to the vault root for m.
//
// When m.TopicKey is non-empty each slash-separated segment is sanitized and the
// result placed under "notes/". When the topic_key is empty, or when the
// assembled path would exceed maxPathLen characters, the memory is placed in
// notes/_no-topic/<id8>.md where id8 is the first 8 characters of m.ID.
//
// Collision handling is the caller's responsibility: the Writer maintains a
// path->ID map and appends "-2", "-3", … suffixes for duplicates.
func MemoryPath(m *model.Memory) string {
	if m.TopicKey == "" {
		return noTopicPath(m.ID)
	}

	rel := topicKeyToRelPath(m.TopicKey)
	if rel == "" {
		// topic_key sanitized entirely to empty — treat as no-topic
		return noTopicPath(m.ID)
	}

	full := filepath.Join("notes", rel+".md")
	if len(full) > maxPathLen {
		return noTopicPath(m.ID)
	}
	return full
}

// SanitizeSegment replaces filesystem-unsafe characters in a single path
// segment (a component between slashes in a topic_key).
//
// Rules applied in order:
//  1. Leading and trailing dots and spaces are trimmed first (Windows/macOS safety)
//  2. Characters in the set [: * ? < > | \ " \0] are replaced with _
//  3. Spaces are replaced with - (more readable than _, which looks like
//     original content)
//  4. The segment is capped at maxSegmentLen runes
//
// An empty string is returned when the segment is empty after trimming.
func SanitizeSegment(s string) string {
	if s == "" {
		return ""
	}

	// Trim leading and trailing dots and spaces first so that a segment
	// consisting entirely of those characters becomes empty before replacement.
	s = strings.Trim(s, ". ")
	if s == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(s))

	for _, r := range s {
		switch r {
		case ':', '*', '?', '<', '>', '|', '\\', '"', '\x00':
			b.WriteByte('_')
		case ' ':
			b.WriteByte('-')
		default:
			b.WriteRune(r)
		}
	}

	result := b.String()

	if utf8.RuneCountInString(result) > maxSegmentLen {
		runes := []rune(result)
		result = string(runes[:maxSegmentLen])
	}

	return result
}

// topicKeyToRelPath converts a topic_key into a relative file path (without
// the "notes/" prefix and without the ".md" suffix). Empty segments are
// skipped. Returns "" when no non-empty segments remain.
func topicKeyToRelPath(key string) string {
	parts := strings.Split(key, "/")
	sanitized := make([]string, 0, len(parts))
	for _, p := range parts {
		s := SanitizeSegment(p)
		if s != "" {
			sanitized = append(sanitized, s)
		}
	}
	if len(sanitized) == 0 {
		return ""
	}
	return strings.Join(sanitized, "/")
}

// UUIDPath returns the flat, topic_key-independent vault-relative path used by
// PathModeUUID: notes/<uuid>.md. Every memory gets its own file keyed only by
// its immutable ID, so concurrent creations by different team members can
// never collide at the git level (SPEC-053 D1) — only edits to the SAME
// memory can conflict, and git resolves that the normal way. Unlike
// noTopicPath, the full UUID is used (not truncated) since collisions here
// would mean two distinct memories sharing an ID, which cannot happen.
func UUIDPath(id string) string {
	return filepath.Join("notes", id+".md")
}

// noTopicPath returns the vault-relative path for a memory with no topic_key.
// Uses the first 8 characters of the UUIDv7 ID to keep filenames short while
// avoiding practical collisions (32 bits of entropy, time-prefix guarantees
// monotonic ordering).
func noTopicPath(id string) string {
	prefix := truncateID(id)
	return filepath.Join(noTopicDir, prefix+".md")
}

// truncateID returns the first 8 runes of a UUID string. This mirrors the
// pattern used in internal/export/markdown.go.
func truncateID(id string) string {
	runes := []rune(id)
	if len(runes) <= 8 {
		return id
	}
	return string(runes[:8])
}
