// Package frontmatter implements a surgical editor for the YAML frontmatter
// of Claude Code agent markdown files. It fixes a fixed set of well-known
// keys (name, description, model, tools, permissionMode) while preserving
// every other byte verbatim: unknown keys, comments, blank lines, and the
// body after the closing "---" delimiter. This avoids the corruption risk of
// a full YAML re-serialization (bug I1: description round-trip corruption).
//
// This package is a leaf: stdlib only, no imports of other internal mneme
// packages. It exists so both internal/install (today) and internal/subagents
// (future, per the agnostic-agents EPIC) can regenerate agent frontmatter
// without depending on each other.
package frontmatter

import (
	"bytes"
	"fmt"
	"strings"
)

// Fields is the set of well-known frontmatter keys SetFrontmatter can create
// or update. A nil field leaves the corresponding key (and its current value,
// or its absence) completely untouched. A non-nil field — including a
// pointer to an empty string — sets the key to that value, creating the line
// if it does not already exist.
type Fields struct {
	Name           *string
	Description    *string
	Model          *string
	Tools          *string
	PermissionMode *string
}

// canonicalKeyOrder defines the relative order used when a requested key is
// absent from the frontmatter block and must be inserted: a new line is
// anchored immediately after the nearest preceding canonical key that is
// already present, falling back to just before the closing "---" delimiter
// when none of the preceding canonical keys exist either.
var canonicalKeyOrder = []string{"name", "description", "model", "tools", "permissionMode"}

// SetFrontmatter fixes the keys named in fields inside the YAML frontmatter
// of content, preserving everything else byte-for-byte.
//
// Rules:
//   - The file must begin with a "---" delimiter line (line 0).
//   - The closing "---" delimiter must appear somewhere after line 0.
//   - For each non-nil field, if a line starting with "<key>:" already
//     exists inside the frontmatter block, it is replaced with "<key>: <value>".
//   - If no such line exists, one is inserted: immediately after the nearest
//     preceding canonical key (per canonicalKeyOrder) that is present, or
//     just before the closing delimiter if none of the preceding canonical
//     keys are present either.
//   - Lines for keys not requested (nil fields), unknown keys, comments, and
//     list-continuation lines are left completely untouched, in their
//     original order.
//   - The body (everything after the closing delimiter) is never touched.
//
// SetFrontmatter is idempotent: calling it twice with the same fields
// produces the same result the second time as the first.
func SetFrontmatter(content []byte, fields Fields) ([]byte, error) {
	lines := splitLines(content)

	if len(lines) == 0 || lines[0] != "---" {
		return nil, fmt.Errorf("frontmatter: missing opening --- delimiter")
	}

	closeIdx := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			closeIdx = i
			break
		}
	}
	if closeIdx == -1 {
		return nil, fmt.Errorf("frontmatter: missing closing --- delimiter")
	}

	values := fieldValues(fields)

	// existingLine tracks the current line index of each requested key found
	// in the frontmatter block. Indices are kept in sync as insertions shift
	// the slice below.
	existingLine := map[string]int{}
	for i := 1; i < closeIdx; i++ {
		key, ok := frontmatterKey(lines[i])
		if !ok {
			continue
		}
		if _, known := values[key]; known {
			existingLine[key] = i
		}
	}

	// Replace values for keys that already exist.
	for key, idx := range existingLine {
		if val := values[key]; val != nil {
			lines[idx] = key + ": " + *val
		}
	}

	// Insert missing keys, anchored after the nearest preceding canonical key
	// that is present (falling back to just before the closing delimiter).
	for _, key := range canonicalKeyOrder {
		val := values[key]
		if val == nil {
			continue
		}
		if _, present := existingLine[key]; present {
			continue
		}

		newLine := key + ": " + *val
		anchor := nearestPrecedingAnchor(existingLine, key)

		var insertAt int
		if anchor == -1 {
			insertAt = closeIdx
		} else {
			insertAt = anchor + 1
		}

		lines = insertLine(lines, insertAt, newLine)
		closeIdx++
		shiftIndicesFrom(existingLine, insertAt)
		existingLine[key] = insertAt
	}

	return joinLines(lines), nil
}

// fieldValues maps each known frontmatter key to its requested value pointer
// (nil when the caller did not request a change).
func fieldValues(fields Fields) map[string]*string {
	return map[string]*string{
		"name":           fields.Name,
		"description":    fields.Description,
		"model":          fields.Model,
		"tools":          fields.Tools,
		"permissionMode": fields.PermissionMode,
	}
}

// nearestPrecedingAnchor returns the current line index of the closest
// canonical key before key (in canonicalKeyOrder) that is already present in
// existingLine, or -1 if none of them are present.
func nearestPrecedingAnchor(existingLine map[string]int, key string) int {
	pos := indexOf(canonicalKeyOrder, key)
	for i := pos - 1; i >= 0; i-- {
		if ln, ok := existingLine[canonicalKeyOrder[i]]; ok {
			return ln
		}
	}
	return -1
}

// shiftIndicesFrom increments every tracked line index at or after insertAt,
// reflecting that a new line has just been inserted at that position.
func shiftIndicesFrom(existingLine map[string]int, insertAt int) {
	for k, ln := range existingLine {
		if ln >= insertAt {
			existingLine[k] = ln + 1
		}
	}
}

// insertLine returns a copy of lines with newLine inserted at index at.
func insertLine(lines []string, at int, newLine string) []string {
	result := make([]string, 0, len(lines)+1)
	result = append(result, lines[:at]...)
	result = append(result, newLine)
	result = append(result, lines[at:]...)
	return result
}

// indexOf returns the index of s in list, or -1 if not found.
func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

// frontmatterKey extracts the key token (everything before the first ":")
// from line, returning ok=false for lines that are not top-level "key:"
// declarations: blank lines, comments ("#"), indented lines (list
// continuations), and lines with no colon at all.
func frontmatterKey(line string) (string, bool) {
	if line == "" {
		return "", false
	}
	if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") ||
		strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
		return "", false
	}
	idx := strings.Index(line, ":")
	if idx == -1 {
		return "", false
	}
	return line[:idx], true
}

// splitLines splits content into individual lines, preserving trailing
// newlines correctly. The last line is included even if it has no trailing
// newline. This function is used to manipulate lines without YAML parsing.
func splitLines(content []byte) []string {
	if len(content) == 0 {
		return nil
	}
	raw := bytes.Split(content, []byte{'\n'})
	lines := make([]string, len(raw))
	for i, r := range raw {
		lines[i] = string(r)
	}
	return lines
}

// joinLines reassembles lines into a byte slice, joining with \n.
func joinLines(lines []string) []byte {
	result := strings.Join(lines, "\n")
	return []byte(result)
}
