package sddfile

import (
	"regexp"
	"strings"
)

// writeEscapeRe matches any line that, once its leading backslashes are
// stripped, begins with "<!-- mneme:" — the exact prefix every structural
// section marker uses (D26). It matches ZERO or more leading backslashes
// deliberately: a line that has already been escaped once (by a previous
// round-trip) matches again, so re-escaping is well-defined and never
// double-counts by accident.
var writeEscapeRe = regexp.MustCompile(`^\\*<!-- mneme:`)

// readUnescapeRe matches a line with ONE OR MORE leading backslashes before
// "<!-- mneme:". A REAL structural marker mneme itself writes always has
// ZERO leading backslashes, so this regex can never match one — only an
// escaped content line matches, which is exactly what makes the boundary
// between "this line is a section header" and "this line is content that
// merely looks like one" unambiguous during parsing.
var readUnescapeRe = regexp.MustCompile(`^\\+<!-- mneme:`)

// escapeContent applies D27(a)'s "escape the escape" rule to every line of
// content: a line matching writeEscapeRe gains exactly one leading
// backslash. This is total and invertible — see unescapeContent, its exact
// inverse — and needs no knowledge of how many times the content has
// already round-tripped through this package, because escaping again is
// always well-defined (writeEscapeRe matches any backslash count).
//
// This is NOT hypothetical: SPEC-130's own grill ledger (BL-195) and the
// spec.md document that describes this format both contain literal
// "<!-- mneme:" text in the column 0 position. Without this escape, the
// first ledger stored through this mechanism would corrupt its own file on
// the very first write.
func escapeContent(content string) string {
	if content == "" {
		return content
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if writeEscapeRe.MatchString(line) {
			lines[i] = "\\" + line
		}
	}
	return strings.Join(lines, "\n")
}

// unescapeContent reverses escapeContent: every line matching
// readUnescapeRe (one or more leading backslashes then "<!-- mneme:") loses
// exactly one leading backslash. A line that started with zero backslashes
// (ordinary content, or a marker mneme did not itself escape) is left
// untouched.
func unescapeContent(content string) string {
	if content == "" {
		return content
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if readUnescapeRe.MatchString(line) {
			lines[i] = line[1:]
		}
	}
	return strings.Join(lines, "\n")
}
