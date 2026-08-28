package sddfile

import (
	"fmt"
	"strconv"
	"strings"
)

// fmWriter accumulates frontmatter lines in a fixed, deterministic order —
// the same manual-serialization posture internal/vault/frontmatter.go uses
// (no YAML library, no reflection, no random map iteration order).
type fmWriter struct {
	b strings.Builder
}

func (w *fmWriter) scalar(key, value string) {
	fmt.Fprintf(&w.b, "%s: %s\n", key, value)
}

func (w *fmWriter) omitScalar(key, value string) {
	if value == "" {
		return
	}
	w.scalar(key, value)
}

func (w *fmWriter) quoted(key, value string) {
	fmt.Fprintf(&w.b, "%s: %q\n", key, value)
}

func (w *fmWriter) omitQuoted(key, value string) {
	if value == "" {
		return
	}
	w.quoted(key, value)
}

func (w *fmWriter) integer(key string, value int) {
	fmt.Fprintf(&w.b, "%s: %d\n", key, value)
}

func (w *fmWriter) list(key string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(&w.b, "%s:\n", key)
	for _, item := range items {
		fmt.Fprintf(&w.b, "  - %s\n", item)
	}
}

// String returns the accumulated frontmatter body (without the enclosing
// "---" delimiters — writeFrontmatterBlock adds those).
func (w *fmWriter) String() string {
	return w.b.String()
}

// writeFrontmatterBlock wraps body (already terminated by "\n" per field)
// between the opening and closing "---" delimiters.
func writeFrontmatterBlock(body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString(body)
	b.WriteString("---\n")
	return b.String()
}

// fmFields is the parsed result of a frontmatter block: scalar values keyed
// by field name, and list values keyed by their header name. Both maps are
// always non-nil so callers never need a nil check before indexing.
type fmFields struct {
	scalars map[string]string
	lists   map[string][]string
}

// parseFrontmatterBlock parses the "---"-delimited block at the start of
// data and returns its fields plus the byte offset of the first byte after
// the closing delimiter (where the body begins).
//
// The scan is line-based and mirrors internal/vault/reader.go's
// parseFrontmatter: "key: value" lines are scalars, "key:" with nothing
// after is a list header, and "  - item" lines belong to the most recently
// opened list header. Unknown keys are collected too — format.go decides
// which ones it cares about, and an unrecognised key inside a schema this
// package accepts is a WARNING at the caller level, never a silent drop
// (D28).
func parseFrontmatterBlock(data []byte) (fmFields, int, error) {
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || lines[0] != "---" {
		return fmFields{}, 0, fmt.Errorf("sddfile: missing opening --- delimiter")
	}

	fields := fmFields{
		scalars: make(map[string]string),
		lists:   make(map[string][]string),
	}

	var currentList string
	closingLine := -1

	for i := 1; i < len(lines); i++ {
		line := lines[i]

		if line == "---" {
			closingLine = i
			break
		}

		if strings.HasPrefix(line, "  - ") {
			item := strings.TrimPrefix(line, "  - ")
			if currentList == "" {
				return fmFields{}, 0, fmt.Errorf("sddfile: list item %q has no list header", line)
			}
			fields.lists[currentList] = append(fields.lists[currentList], item)
			continue
		}

		currentList = ""

		colonIdx := strings.Index(line, ": ")
		var key, value string
		switch {
		case colonIdx >= 0:
			key = line[:colonIdx]
			value = line[colonIdx+2:]
		case strings.HasSuffix(line, ":"):
			key = strings.TrimSuffix(line, ":")
			currentList = key
			continue
		default:
			// Malformed line inside frontmatter — the record as a whole is
			// unparseable rather than silently skipping a byte range that
			// could hide real data loss.
			return fmFields{}, 0, fmt.Errorf("sddfile: malformed frontmatter line %q", line)
		}

		fields.scalars[key] = value
	}

	if closingLine < 0 {
		return fmFields{}, 0, fmt.Errorf("sddfile: missing closing --- delimiter")
	}

	offset := 0
	for i := 0; i <= closingLine; i++ {
		offset += len(lines[i]) + 1
	}

	return fields, offset, nil
}

// unquote strips %q-style double quotes from a frontmatter scalar value,
// mirroring internal/vault/reader.go's unquoteTitle. Values written with
// fmWriter.quoted are always valid Go string literals, so strconv.Unquote
// always succeeds for anything this package itself wrote; a value that
// fails to unquote is returned as-is for forward/hand-edit tolerance.
func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		if u, err := strconv.Unquote(s); err == nil {
			return u
		}
	}
	return s
}

// parseIntField parses an int scalar, defaulting to 0 on any failure
// (forward-compat with a hand-edited or older file — never a hard error for
// a single cosmetic field).
func parseIntField(s string) int {
	v, _ := strconv.Atoi(strings.TrimSpace(s))
	return v
}

// parseBoolField parses a bool scalar written as "true"/"false".
func parseBoolField(s string) bool {
	v, _ := strconv.ParseBool(strings.TrimSpace(s))
	return v
}
