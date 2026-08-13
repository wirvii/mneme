// Package quality — this file defines the format-agnostic coverage model
// (SPEC-116 EPIC-calidad S2 D2/D9/D18): FileCoverage/Profile, the
// ProfileParser interface every dialect implements, and the registry
// ParseProfile/Formats consult. Nothing downstream of Profile (coverage.go,
// baseline.go, the service layer) ever learns which format produced it —
// that is the entire point of normalizing here (D18).
package quality

import (
	"errors"
	"fmt"
	"sort"
)

// ErrUnknownFormat is returned by ParseProfile when format does not name a
// registered ProfileParser. The set of accepted values is declared, never
// guessed (D18) — Formats() is the single source of truth the constitution
// parser (Parse, constitution.go) validates the `format` key against.
var ErrUnknownFormat = errors.New("quality: unknown coverage profile format")

// ErrInvalidProfile is returned when a profile's STRUCTURAL records are
// malformed — the records the entire coverage calculation rests on (D9).
// Records a parser does not recognise (a tool-specific dialect line) are
// ignored without error; only a record the parser DOES recognise but cannot
// make sense of degrades to this error.
var ErrInvalidProfile = errors.New("quality: invalid coverage profile")

// FileCoverage is one file's line-level coverage, normalized from whatever
// dialect produced it. Lines maps a source line number to the HIGHEST hit
// count any profile record declared for that line — never a sum, and never
// "last one wins" (D9/AC7/AC8): taking the max is what makes "a line is
// covered if ANY contributing record declares hits > 0" true regardless of
// how many merged LCOV records, or overlapping go-cover blocks, touch it.
type FileCoverage struct {
	Lines map[int]int
}

// Instrumented reports whether line has at least one record in this file's
// profile — regardless of whether it was covered.
func (fc FileCoverage) Instrumented(line int) bool {
	_, ok := fc.Lines[line]
	return ok
}

// Covered reports whether line is instrumented AND at least one contributing
// record declared hits > 0 (D9). A line absent from Lines is neither
// instrumented nor covered — Covered is false for it, same as an
// instrumented-but-never-hit line; callers that need to distinguish the two
// use Instrumented separately.
func (fc FileCoverage) Covered(line int) bool {
	return fc.Lines[line] > 0
}

// mergeLine records that some profile record declared hits for line,
// keeping the maximum hits ever seen for it (D9's "some record with hits >
// 0" rule, expressed as a running max rather than a boolean OR so a single
// map value serves both Instrumented and Covered).
func (fc *FileCoverage) mergeLine(line, hits int) {
	if fc.Lines == nil {
		fc.Lines = make(map[int]int)
	}
	if existing, ok := fc.Lines[line]; !ok || hits > existing {
		fc.Lines[line] = hits
	}
}

// Profile is the normalized coverage model every dialect parser produces
// and every calculation (ComputeDiffCoverage, ComputeGlobalStats,
// NormalizeSourcePath's caller) consumes. Files are keyed by the path
// exactly as the producing tool wrote it — NormalizeSourcePath (coverage.go)
// reconciles that against the repository's own file list; Profile itself
// does no path reasoning at all.
type Profile struct {
	Files map[string]FileCoverage
}

// mergeLine records a (path, line, hits) observation into the profile,
// creating the file's entry if this is its first record. Both dialect
// parsers call this exclusively — neither touches a Profile's map directly
// — so the merge rule (FileCoverage.mergeLine) lives in exactly one place.
func (p *Profile) mergeLine(path string, line, hits int) {
	if p.Files == nil {
		p.Files = make(map[string]FileCoverage)
	}
	fc := p.Files[path]
	fc.mergeLine(line, hits)
	p.Files[path] = fc
}

// ensureFile registers path in the profile with no lines yet, if it is not
// already present — used by the LCOV parser's SF: directive, which may
// introduce a file with zero DA: records (an empty source file, or one
// whose test run touched nothing).
func (p *Profile) ensureFile(path string) {
	if p.Files == nil {
		p.Files = make(map[string]FileCoverage)
	}
	if _, ok := p.Files[path]; !ok {
		p.Files[path] = FileCoverage{}
	}
}

// ProfileParser translates one coverage-profile dialect's raw bytes into
// the normalized Profile model. Adding a format mneme understands is
// exactly this: a type implementing ProfileParser, plus one entry in the
// registry below (D18) — nothing else in the package changes.
type ProfileParser interface {
	Parse(data []byte) (*Profile, error)
}

// registry is the SINGLE source of truth for which coverage profile formats
// mneme understands (D18). Parse (constitution.go) validates the
// constitution's declared `format` key against Formats() — never against a
// second, parallel literal list — so the constitution parser and the
// profile parser can never drift out of sync (AC6). Precedent: the same
// registry shape internal/codegraph/extractor.go already uses for
// language extractors (V12 of the design).
var registry = map[string]ProfileParser{
	"lcov":     lcovParser{},
	"go-cover": goCoverParser{},
}

// Formats returns the sorted list of format names ParseProfile accepts.
// Sorted so callers (and error messages enumerating "valores aceptados")
// get a deterministic order rather than Go's randomized map iteration.
func Formats() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ParseProfile parses data as the named format, returning ErrUnknownFormat
// when format is not registered (Formats()) rather than silently guessing a
// dialect from the bytes — a declared, closed vocabulary is what D18
// requires: guessing wrong produces a profile with zero files, which is the
// empty-denominator trap this entire EPIC exists to close.
func ParseProfile(format string, data []byte) (*Profile, error) {
	parser, ok := registry[format]
	if !ok {
		return nil, fmt.Errorf("quality: coverage profile format %q: %w", format, ErrUnknownFormat)
	}
	return parser.Parse(data)
}
