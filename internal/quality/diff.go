package quality

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// hunkHeaderPattern matches a unified-diff hunk header's numeric fields.
// Capture group 1 is the destination-side start line; group 2 is its
// (optional) line count — absent when the count is 1, per the unified diff
// format's own shorthand (e.g. "@@ -4 +4 @@").
var hunkHeaderPattern = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// ParseUnifiedDiff is a pure function: it extracts, from raw `git diff
// --unified=0`-style output, which line numbers on the NEW (destination)
// side of each hunk were added or modified — the exact set D8 needs, with
// no git process involved. ChangedLines (git.go) is exclusively "run git
// and hand its output to this function"; every raw-bytes edge case is
// tested here, against bytes, without a repository (AC9).
//
// The returned map is keyed by the file's path on the NEW side (the "+++
// b/<path>" line), which is also what a rename resolves to — a hunk that is
// part of a rename attributes its lines to the file's new name, never its
// old one. A pure deletion hunk (destination count 0) contributes no lines.
// "+++ /dev/null" (a deleted file) is recognised and contributes nothing.
func ParseUnifiedDiff(diff []byte) (map[string][]int, error) {
	result := make(map[string][]int)

	var currentFile string
	for _, raw := range strings.Split(string(diff), "\n") {
		line := strings.TrimRight(raw, "\r")

		switch {
		case strings.HasPrefix(line, "+++ "):
			path := strings.TrimPrefix(line, "+++ ")
			if path == "/dev/null" {
				currentFile = ""
				continue
			}
			// --dst-prefix=b/ is fixed in the invocation (D8) — the prefix
			// is always exactly "b/", never sniffed from diff.noprefix.
			currentFile = strings.TrimPrefix(path, "b/")

		case strings.HasPrefix(line, "@@ "):
			if currentFile == "" {
				// A hunk under +++ /dev/null (a deletion) or before any
				// +++ line has been seen — nothing on the new side to mark.
				continue
			}
			start, count, err := parseHunkHeader(line)
			if err != nil {
				return nil, fmt.Errorf("quality: parse unified diff: %w", err)
			}
			for i := 0; i < count; i++ {
				result[currentFile] = append(result[currentFile], start+i)
			}
		}
	}

	return result, nil
}

// parseHunkHeader parses a "@@ -a,b +c,d @@" (or the no-comma, count=1
// shorthand "@@ -a +c @@") header, returning the destination side's start
// line and line count.
func parseHunkHeader(line string) (start, count int, err error) {
	m := hunkHeaderPattern.FindStringSubmatch(line)
	if m == nil {
		return 0, 0, fmt.Errorf("malformed hunk header %q", line)
	}
	start, err = strconv.Atoi(m[1])
	if err != nil {
		return 0, 0, fmt.Errorf("hunk header %q: start line: %w", line, err)
	}
	if m[2] == "" {
		count = 1
	} else {
		count, err = strconv.Atoi(m[2])
		if err != nil {
			return 0, 0, fmt.Errorf("hunk header %q: line count: %w", line, err)
		}
	}
	return start, count, nil
}
