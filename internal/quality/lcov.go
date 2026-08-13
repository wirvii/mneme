package quality

import (
	"fmt"
	"strconv"
	"strings"
)

// lcovParser implements ProfileParser for the LCOV tracefile dialect — the
// lingua franca coverage format most non-Go ecosystems already produce
// (D9/D18).
//
// Recognised directives: SF:<path> opens (or resumes — see below) a file's
// record; DA:<line>,<hits> declares one line's hit count; end_of_record
// closes the current file's record. Every other directive (FN:, FNDA:,
// BRDA:, LF:, LH:, TN:, BRF:, …) is ignored without error — a real-world
// LCOV file mixes dialects across tools, and being strict there would be
// fragile against the producer, which is by design outside mneme's
// ecosystem knowledge (D9 of the grill). LF:/LH: in particular are the
// producer's OWN summary counters; they are never read — the per-line DA:
// records are recomputed independently, so a stale or wrong LF:/LH: can
// never influence the result (D9).
type lcovParser struct{}

// Parse implements ProfileParser.
func (lcovParser) Parse(data []byte) (*Profile, error) {
	profile := &Profile{}

	var current string
	haveFile := false

	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(rawLine, "\r")
		if line == "" {
			continue
		}

		prefix, rest, hasColon := strings.Cut(line, ":")
		if !hasColon {
			if line == "end_of_record" {
				current = ""
				haveFile = false
			}
			continue
		}

		switch prefix {
		case "SF":
			if rest == "" {
				return nil, fmt.Errorf("quality: lcov: SF with empty path: %w", ErrInvalidProfile)
			}
			current = rest
			haveFile = true
			profile.ensureFile(current)

		case "DA":
			if !haveFile {
				return nil, fmt.Errorf("quality: lcov: DA record before any SF: %w", ErrInvalidProfile)
			}
			lineNum, hits, err := parseLcovDA(rest)
			if err != nil {
				return nil, fmt.Errorf("quality: lcov: malformed DA:%s: %s: %w", rest, err, ErrInvalidProfile)
			}
			profile.mergeLine(current, lineNum, hits)

		default:
			// FN:, FNDA:, BRDA:, LF:, LH:, TN:, BRF:, and any other
			// tool-specific directive — ignored without error (D9).
		}
	}

	return profile, nil
}

// parseLcovDA parses the "<line>,<hits>" payload of a DA: directive.
func parseLcovDA(rest string) (line, hits int, err error) {
	parts := strings.Split(rest, ",")
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("expected \"line,hits\"")
	}
	line, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("line %q: %w", parts[0], err)
	}
	hits, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("hits %q: %w", parts[1], err)
	}
	return line, hits, nil
}
