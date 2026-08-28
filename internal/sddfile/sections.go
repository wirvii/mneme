package sddfile

import (
	"regexp"
	"strconv"
	"strings"
)

// The closed vocabulary of section marker kinds a record may contain (D26).
// Every marker mneme itself writes begins with EXACTLY "<!-- mneme:" at
// column 0 — zero leading backslashes. A content line that happens to start
// the same way is always escaped before being embedded (escape.go), so this
// prefix alone is what makes a real marker line unambiguous during parsing.
const (
	markerKindRefinement = "refinement"
	markerKindHistory    = "history"
	markerKindPushback   = "pushback"
	markerKindQuestion   = "question"
	markerKindResolution = "resolution"
)

// markerPrefix is the literal opening every structural marker line shares.
const markerPrefix = "<!-- mneme:"

// markerHeaderRe recognises a well-formed marker line and splits it into
// its kind and the raw " key=value ..." attribute tail.
var markerHeaderRe = regexp.MustCompile(`^<!-- mneme:(\w+)((?:\s+\S+=(?:"(?:[^"\\]|\\.)*"|\S+))*)\s*-->$`)

// markerAttrRe extracts one key=value pair at a time from a marker's
// attribute tail. Values are either a Go-quoted string (parsed with
// strconv.Unquote, same convention as the frontmatter's title field) or a
// bare token (used for the unquoted numeric/boolean attributes: seq,
// resolved).
var markerAttrRe = regexp.MustCompile(`(\w+)=("(?:[^"\\]|\\.)*"|\S+)`)

// isMarkerLine reports whether line is a REAL structural marker (as opposed
// to escaped content that merely looks like one after de-escaping). Real
// markers always start with markerPrefix at column 0 with no leading
// backslash — see escape.go's writeEscapeRe/readUnescapeRe for the
// invariant that guarantees this.
func isMarkerLine(line string) bool {
	return strings.HasPrefix(line, markerPrefix)
}

// buildMarkerLine renders a section header. attrs is a flat
// key,value,key,value,... list; a value is rendered with %q UNLESS its key
// is listed in bareKeys (numeric/boolean attributes written without
// quotes, matching the literal examples in spec.md §6.2/§6.3: `seq=1`,
// `resolved=true`).
func buildMarkerLine(kind string, bareKeys map[string]bool, kv ...string) string {
	var b strings.Builder
	b.WriteString(markerPrefix)
	b.WriteString(kind)
	for i := 0; i+1 < len(kv); i += 2 {
		key, value := kv[i], kv[i+1]
		b.WriteByte(' ')
		b.WriteString(key)
		b.WriteByte('=')
		if bareKeys[key] {
			b.WriteString(value)
		} else {
			b.WriteString(strconv.Quote(value))
		}
	}
	b.WriteString(" -->")
	return b.String()
}

// parseMarkerLine parses a line already known to be a marker (isMarkerLine)
// into its kind and attribute map. ok is false when the line starts with
// markerPrefix but is not well-formed — the caller (format.go) treats that
// as a parse error for the whole record, never a silently-skipped line.
func parseMarkerLine(line string) (kind string, attrs map[string]string, ok bool) {
	m := markerHeaderRe.FindStringSubmatch(line)
	if m == nil {
		return "", nil, false
	}
	kind = m[1]
	attrs = make(map[string]string)
	for _, am := range markerAttrRe.FindAllStringSubmatch(m[2], -1) {
		key, raw := am[1], am[2]
		value := raw
		if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
			if u, err := strconv.Unquote(raw); err == nil {
				value = u
			}
		}
		attrs[key] = value
	}
	return kind, attrs, true
}
