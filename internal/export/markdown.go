// Package export converts mneme memories into human-readable formats.
// The primary consumer is the CLI "export markdown" command, but the rendering
// functions are designed to be reusable by any caller that needs a textual
// representation of memory collections (e.g. a future TUI review panel).
//
// Design rationale: we use fmt.Fprintf directly rather than text/template.
// The output is a linear sequence of formatted sections — templates would add
// indirection and make the whitespace rules harder to audit visually.
package export

import (
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/juanftp/mneme/internal/model"
)

// MarkdownOptions controls how Markdown export output is rendered.
type MarkdownOptions struct {
	// Project is the project slug shown in the document header.
	Project string

	// SingleFile renders all types grouped under a single top-level heading
	// when true. When false (directory mode) each call to RenderType produces
	// a standalone document with its own header.
	SingleFile bool
}

// RenderAll writes all memories grouped by type to w as a single Markdown
// document. Memories are iterated in the canonical order returned by
// model.AllMemoryTypes(); within each type they are rendered in the order they
// appear in memories (callers should pre-sort by importance DESC). Types with
// no matching memories are silently skipped.
func RenderAll(w io.Writer, memories []*model.Memory, opts MarkdownOptions) error {
	now := time.Now().UTC()

	if _, err := fmt.Fprintf(w, "# mneme -- Project Memories\n> Project: %s | Exported: %s\n\n",
		opts.Project, now.Format("2006-01-02")); err != nil {
		return fmt.Errorf("export: render all: write header: %w", err)
	}

	// Group memories by type, preserving per-group order.
	grouped := groupByType(memories)

	for _, memType := range model.AllMemoryTypes() {
		group, ok := grouped[memType]
		if !ok || len(group) == 0 {
			continue
		}

		if _, err := fmt.Fprintf(w, "## %s (%d)\n\n", typeTitle(memType), len(group)); err != nil {
			return fmt.Errorf("export: render all: write section header: %w", err)
		}

		if err := renderMemories(w, group, "###"); err != nil {
			return err
		}
	}

	return nil
}

// RenderType writes memories of a single type to w as a standalone Markdown
// document. This is used in directory mode where each type gets its own file.
// The document begins with a header that names the type and includes the count.
// Memory titles use the "###" heading level since the type heading is implicit
// in the file name.
func RenderType(w io.Writer, memType model.MemoryType, memories []*model.Memory, opts MarkdownOptions) error {
	now := time.Now().UTC()

	if _, err := fmt.Fprintf(w, "# mneme -- %s Memories\n> Project: %s | Exported: %s | Count: %d\n\n",
		typeTitle(memType), opts.Project, now.Format("2006-01-02"), len(memories)); err != nil {
		return fmt.Errorf("export: render type: write header: %w", err)
	}

	return renderMemories(w, memories, "###")
}

// ─── private helpers ──────────────────────────────────────────────────────────

// renderMemories writes a sequence of memory entries to w using headingLevel
// (e.g. "###") for each memory title. A "---" separator is inserted between
// entries, but not after the last one.
func renderMemories(w io.Writer, memories []*model.Memory, headingLevel string) error {
	for i, m := range memories {
		if err := renderMemory(w, m, headingLevel); err != nil {
			return err
		}
		if i < len(memories)-1 {
			if _, err := fmt.Fprintf(w, "---\n\n"); err != nil {
				return fmt.Errorf("export: write separator: %w", err)
			}
		}
	}
	return nil
}

// renderMemory writes a single memory entry. The format is:
//
//	### Title
//	- **ID:** 8-char prefix
//	- **Created:** YYYY-MM-DD | **Updated:** YYYY-MM-DD
//	- **Importance:** 0.90 | **Topic:** key (omitted when empty)
//
//	<content>
//
// A trailing newline is appended to separate the entry from the next one or
// from the end of the section.
func renderMemory(w io.Writer, m *model.Memory, headingLevel string) error {
	if _, err := fmt.Fprintf(w, "%s %s\n", headingLevel, m.Title); err != nil {
		return fmt.Errorf("export: write memory title: %w", err)
	}

	if _, err := fmt.Fprintf(w, "- **ID:** %s\n", truncateID(m.ID)); err != nil {
		return fmt.Errorf("export: write memory id: %w", err)
	}

	if _, err := fmt.Fprintf(w, "- **Created:** %s | **Updated:** %s\n",
		m.CreatedAt.Format("2006-01-02"),
		m.UpdatedAt.Format("2006-01-02")); err != nil {
		return fmt.Errorf("export: write memory dates: %w", err)
	}

	importanceLine := fmt.Sprintf("- **Importance:** %.2f", m.Importance)
	if m.TopicKey != "" {
		importanceLine += fmt.Sprintf(" | **Topic:** %s", m.TopicKey)
	}
	if _, err := fmt.Fprintln(w, importanceLine); err != nil {
		return fmt.Errorf("export: write memory importance: %w", err)
	}

	if _, err := fmt.Fprintf(w, "\n%s\n\n", m.Content); err != nil {
		return fmt.Errorf("export: write memory content: %w", err)
	}

	return nil
}

// groupByType partitions memories into a map keyed by MemoryType.
// The order within each group is preserved from the input slice.
func groupByType(memories []*model.Memory) map[model.MemoryType][]*model.Memory {
	grouped := make(map[model.MemoryType][]*model.Memory)
	for _, m := range memories {
		grouped[m.Type] = append(grouped[m.Type], m)
	}
	return grouped
}

// truncateID returns the first 8 characters of a UUID string, which is enough
// to distinguish memories in human review without the visual noise of the full
// 36-character UUID.
func truncateID(id string) string {
	if utf8.RuneCountInString(id) <= 8 {
		return id
	}
	// Slice safely using rune indices to handle multi-byte input gracefully,
	// though UUIDs are always ASCII.
	runes := []rune(id)
	return string(runes[:8])
}

// typeTitle converts a MemoryType to a human-readable title-case string.
// "session_summary" → "Session Summary", "bugfix" → "Bugfix", etc.
func typeTitle(t model.MemoryType) string {
	s := string(t)
	// Replace underscores with spaces, then title-case each word.
	words := strings.Split(s, "_")
	for i, w := range words {
		if len(w) == 0 {
			continue
		}
		r, size := utf8.DecodeRuneInString(w)
		words[i] = string(unicode.ToUpper(r)) + w[size:]
	}
	return strings.Join(words, " ")
}
