package quality

import (
	"fmt"
	"strconv"
	"strings"
)

// goCoverParser implements ProfileParser for the native `go test
// -coverprofile` dialect (D9/D18/D19) — an EXCEPTION deliberately carved out
// of "mneme stays agnostic of the project's ecosystem" (D9 of the grill),
// approved by the owner, because Go is mneme's own implementation language
// and dogfooding its own coverage requires reading its own toolchain's
// native output without a conversion step.
//
// Format: a mandatory first line "mode: set|count|atomic", followed by
// statement records "path:startLine.startCol,endLine.endCol numStmts
// count". The mode header is REQUIRED — its absence is the signal that
// catches an LCOV file mistakenly declared as go-cover (D18's "detección
// declarada, nunca adivinada": a wrong declared format must fail loudly,
// never guess).
//
// The block-expansion approximation. Go's coverage unit is a BLOCK spanning
// a line range, not a single line: every line in [startLine, endLine] is
// marked with the block's count, matching what the standard ecosystem
// converters (e.g. go tool cover, gocov) do. The known bias — blank lines
// and comments inside a covered block count as covered — is accepted
// because the alternative (marking only startLine) would drastically
// undervalue any block spanning more than one line, and because this
// approximation is what lets the SAME repository produce the same
// percentage whether measured via LCOV conversion or this native profile
// (R4, documented as a limitation in docs/quality.md).
type goCoverParser struct{}

// Parse implements ProfileParser.
func (goCoverParser) Parse(data []byte) (*Profile, error) {
	profile := &Profile{}
	headerSeen := false

	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(strings.TrimRight(rawLine, "\r"))
		if line == "" {
			continue
		}

		if !headerSeen {
			if !strings.HasPrefix(line, "mode:") {
				return nil, fmt.Errorf(
					"quality: go-cover: first non-empty line %q is not a %q header (a profile declared go-cover must start with it — an LCOV file mistakenly declared as go-cover fails exactly here): %w",
					line, "mode:", ErrInvalidProfile)
			}
			headerSeen = true
			continue
		}

		path, startLine, endLine, count, err := parseGoCoverStatement(line)
		if err != nil {
			return nil, fmt.Errorf("quality: go-cover: malformed statement %q: %s: %w", line, err, ErrInvalidProfile)
		}
		for l := startLine; l <= endLine; l++ {
			profile.mergeLine(path, l, count)
		}
	}

	if !headerSeen {
		return nil, fmt.Errorf("quality: go-cover: empty profile, missing %q header: %w", "mode:", ErrInvalidProfile)
	}

	return profile, nil
}

// parseGoCoverStatement parses one "path:l1.c1,l2.c2 numStmts count" record.
// Columns are parsed only to be discarded — this model is line-granular,
// never column-granular — and the path keeps whatever prefix the producer
// wrote (typically the module's full import path); NormalizeSourcePath
// (coverage.go) is what reconciles that against the repository's own files.
func parseGoCoverStatement(line string) (path string, startLine, endLine, count int, err error) {
	fields := strings.Fields(line)
	if len(fields) != 3 {
		return "", 0, 0, 0, fmt.Errorf("expected 3 space-separated fields, got %d", len(fields))
	}

	idx := strings.LastIndex(fields[0], ":")
	if idx < 0 {
		return "", 0, 0, 0, fmt.Errorf("missing ':' separating path from line range in %q", fields[0])
	}
	path = fields[0][:idx]
	if path == "" {
		return "", 0, 0, 0, fmt.Errorf("empty path in %q", fields[0])
	}

	coords := strings.Split(fields[0][idx+1:], ",")
	if len(coords) != 2 {
		return "", 0, 0, 0, fmt.Errorf("line range %q must have exactly two coordinates", fields[0][idx+1:])
	}
	startLine, err = firstIntComponent(coords[0])
	if err != nil {
		return "", 0, 0, 0, fmt.Errorf("start line %q: %w", coords[0], err)
	}
	endLine, err = firstIntComponent(coords[1])
	if err != nil {
		return "", 0, 0, 0, fmt.Errorf("end line %q: %w", coords[1], err)
	}
	if endLine < startLine {
		return "", 0, 0, 0, fmt.Errorf("end line %d before start line %d", endLine, startLine)
	}

	if _, err = strconv.Atoi(fields[1]); err != nil {
		return "", 0, 0, 0, fmt.Errorf("numStmts %q: %w", fields[1], err)
	}
	count, err = strconv.Atoi(fields[2])
	if err != nil {
		return "", 0, 0, 0, fmt.Errorf("count %q: %w", fields[2], err)
	}

	return path, startLine, endLine, count, nil
}

// firstIntComponent parses the leading "<line>" out of a "<line>.<col>"
// coordinate, discarding the column.
func firstIntComponent(s string) (int, error) {
	before, _, _ := strings.Cut(s, ".")
	return strconv.Atoi(before)
}
