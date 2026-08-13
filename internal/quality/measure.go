package quality

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// NormalizeSourcePath reconciles a path as a coverage-profile PRODUCER wrote
// it (raw) against repoFiles — the repository's own list of relative file
// paths — returning the repo-relative path a profile entry actually refers
// to, or ok=false when raw cannot be resolved to exactly one repo file
// (D14).
//
// The five rules collapse into ONE algorithm: normalize separators and
// "./", then try progressively SHORTER suffixes of raw (dropping one
// leading path segment at a time) against repoFiles, at each length
// requiring an exact match OR a "/"-bounded suffix match. The first length
// with exactly one match wins — this is what makes an absolute path
// hanging off the repository root, a path already relative and present
// verbatim, and an import-path-prefixed path (go-cover's native form, e.g.
// "github.com/wirvii/mneme/internal/x/y.go") all resolve through the exact
// same mechanism, longest match first. A length with MORE than one match
// is ambiguous and NormalizeSourcePath refuses immediately — it never
// falls back to a shorter, differently-ambiguous-or-not candidate,
// because picking "the first" of an ambiguous match is exactly how a
// mechanism starts silently attributing coverage to the wrong file.
func NormalizeSourcePath(raw string, repoFiles []string) (rel string, ok bool) {
	normalized := strings.ReplaceAll(raw, `\`, "/")
	normalized = path.Clean(normalized)
	normalized = strings.TrimPrefix(normalized, "/")

	if normalized == "" || normalized == "." {
		return "", false
	}

	segments := strings.Split(normalized, "/")
	for i := range segments {
		candidate := strings.Join(segments[i:], "/")
		if candidate == "" {
			continue
		}

		var matched []string
		for _, f := range repoFiles {
			rf := filepath.ToSlash(f)
			if rf == candidate || strings.HasSuffix(rf, "/"+candidate) {
				matched = append(matched, rf)
			}
		}

		switch len(matched) {
		case 0:
			continue
		case 1:
			return matched[0], true
		default:
			// AMBIGUOUS at this length: refuse rather than guess (rule 5).
			return "", false
		}
	}

	return "", false
}

// DiffCoverageStats is the result of ComputeDiffCoverage: how many of a
// diff's ELIGIBLE lines (instrumented ones — an uninstrumented line, a
// blank line or a comment, is neither counted nor penalized) are covered,
// plus exactly which lines are not, per file — the detail AC24 requires
// ("qué líneas concretas quedan sin cubrir", never just a percentage).
type DiffCoverageStats struct {
	// LinesEligible is the count of changed lines that ARE instrumented —
	// the denominator. A changed line the profile never mentions (a
	// comment, a blank line, a non-code file) contributes to neither this
	// nor LinesCovered.
	LinesEligible int

	// LinesCovered is how many of LinesEligible have hits > 0.
	LinesCovered int

	// Pct is LinesCovered/LinesEligible*100, derived ONCE from the integer
	// counts above (R8) — 0 when LinesEligible is 0 (the caller is
	// responsible for treating that as "skipped", per min_changed_lines,
	// never as a percentage of its own).
	Pct float64

	// Missing lists, per file, the eligible-but-uncovered line numbers —
	// the "which lines" AC24 and a `fail` check's Summary need.
	Missing map[string][]int

	// FilesConsidered counts the changed files that passed the exclude
	// filter — the candidate set the coverage/changed-files-in-profile
	// check (D13/AC16) reasons about.
	FilesConsidered int

	// FilesMatched counts, of FilesConsidered, how many were found in
	// profile at ALL (regardless of whether any of their changed lines
	// were eligible). FilesConsidered > 0 && FilesMatched == 0 is the
	// path-mapping-is-broken signal AC16/R3 exist to catch.
	FilesMatched int
}

// ComputeDiffCoverage computes diff coverage for changed (repo-relative
// path -> changed line numbers, as Git.ChangedLines returns) against
// profile — whose keys MUST already be repo-relative (the caller
// normalizes every profile entry via NormalizeSourcePath before calling
// this; ComputeDiffCoverage itself does no path reasoning). exclude globs
// (doublestar) drop a file from BOTH the numerator and the denominator —
// applying to a generated file is the point (AC13): if it were excluded
// only from the numerator, an uncovered generated file would silently
// LOWER the reported percentage instead of being invisible to it.
func ComputeDiffCoverage(changed map[string][]int, profile *Profile, exclude []string) DiffCoverageStats {
	stats := DiffCoverageStats{Missing: map[string][]int{}}

	// Sort file names for deterministic Missing iteration order in callers
	// that print it (a map has no stable order otherwise).
	files := make([]string, 0, len(changed))
	for f := range changed {
		files = append(files, f)
	}
	sort.Strings(files)

	for _, file := range files {
		if matchesAnyGlob(exclude, file) {
			continue
		}
		stats.FilesConsidered++
		fc, ok := profile.Files[file]
		if !ok {
			continue
		}
		stats.FilesMatched++
		for _, ln := range changed[file] {
			if !fc.Instrumented(ln) {
				continue
			}
			stats.LinesEligible++
			if fc.Covered(ln) {
				stats.LinesCovered++
			} else {
				stats.Missing[file] = append(stats.Missing[file], ln)
			}
		}
	}

	stats.Pct = percentage(stats.LinesCovered, stats.LinesEligible)
	return stats
}

// ComputeGlobalStats computes the repository-wide aggregate coverage over
// every file profile mentions, for the ratchet's baseline comparison
// (D1/D11). profile's keys must already be repo-relative, same contract as
// ComputeDiffCoverage. exclude applies identically to both the numerator
// and denominator, for the same reason (AC13's third row: exclusions apply
// to the aggregate too, not only the delta).
func ComputeGlobalStats(profile *Profile, exclude []string) (linesTotal, linesCovered int, pct float64) {
	for file, fc := range profile.Files {
		if matchesAnyGlob(exclude, file) {
			continue
		}
		for _, hits := range fc.Lines {
			linesTotal++
			if hits > 0 {
				linesCovered++
			}
		}
	}
	return linesTotal, linesCovered, percentage(linesCovered, linesTotal)
}

// ScopeHash fingerprints the MEASUREMENT SCOPE a coverage percentage was
// computed under — format plus exclude patterns, sorted and deduplicated —
// so the ratchet's baseline-comparable check (D11) can detect the second-
// order leak: a later spec widening `exclude` (or switching format, which
// changes instrumentation itself) changes what "100%" even means, and a
// baseline measured under a DIFFERENT scope is not comparable, however
// tempting the raw percentages look side by side.
func ScopeHash(format string, exclude []string) string {
	uniq := make(map[string]struct{}, len(exclude))
	for _, e := range exclude {
		uniq[e] = struct{}{}
	}
	sorted := make([]string, 0, len(uniq))
	for e := range uniq {
		sorted = append(sorted, e)
	}
	sort.Strings(sorted)

	parts := append([]string{format}, sorted...)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

// percentage derives a percentage from integer counts EXACTLY ONCE (R8) —
// never accumulated across float additions, and never compared with `==`
// by a caller. Returns 0 for a zero denominator; callers decide separately
// whether a zero denominator means "skipped" (D6's min_changed_lines floor)
// rather than treating this 0 as a real measurement.
func percentage(covered, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(covered) / float64(total) * 100
}

// matchesAnyGlob reports whether path matches any of the doublestar
// patterns — the shared exclude-list semantics ComputeDiffCoverage and
// ComputeGlobalStats both use. An invalid glob never reaches here: Parse
// (constitution.go) validates every `exclude` entry's syntax at parse time
// (D6), so a malformed pattern is a constitution error, never a silent
// non-match at evaluation time.
func matchesAnyGlob(patterns []string, path string) bool {
	for _, p := range patterns {
		if ok, _ := doublestar.Match(p, path); ok {
			return true
		}
	}
	return false
}
