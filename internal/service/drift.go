package service

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// DriftFinding describes a single advisory finding from the drift detector.
// It is purely informational — the detector never modifies any file.
type DriftFinding struct {
	// File is the absolute path of the file where the finding was detected.
	File string

	// Line is the 1-based line number within File.
	Line int

	// Message is the human-readable advisory message.
	Message string
}

// String returns a formatted finding suitable for terminal output.
func (f DriftFinding) String() string {
	return fmt.Sprintf("%s:%d — %s", f.File, f.Line, f.Message)
}

// canonicalSections is the list of heading strings that are covered by the
// global mneme operating manual. Headings from project CLAUDE.md files that
// duplicate these topics are advisory drift findings (category a).
//
// The list is intentionally conservative — only headings that the operating
// manual owns globally. False positives are acceptable (advisory only).
var canonicalSections = []string{
	// Operating manual headings (SPEC-041 §D2)
	"mneme operating manual",
	"how to launch",
	"delegation triggers",
	"session lifecycle",
	"save rules",
	"sdd engine",
	"sdd + lanes",
	"trivial vs standard",
	// Enforcement model docs
	"two-layer enforcement",
	"enforcement model",
	// Lane docs
	"lanes",
	// Skills docs
	"skills",
	// Models docs
	"models",
	// Conflicts docs
	"conflicts",
	// Protocol (legacy heading)
	"mneme — persistent memory",
	"persistent memory",
}

// enforcementContradictions is a list of lowercase phrase fragments whose
// presence in a line indicates a contradiction with the enforcement model.
// Matches are case-insensitive substring checks (advisory only).
var enforcementContradictions = []string{
	"orchestrator can edit code",
	"orchestrator puede editar",
	"orchestrator puede modificar codigo",
	"skip sdd",
	"saltarse sdd",
	"bypass sdd",
	"no necesita spec",
	"sin spec",
	"bypass delegation",
	"saltarse delegacion",
	"orchestrator implements",
}

// DetectDrift scans the file at filePath for two categories of advisory drift:
//
// (a) Headings that duplicate content covered by the global operating manual.
// (b) Phrases that contradict the enforcement model (orchestrator cannot edit code).
//
// The function reads outside the managed block only — content between
// "<!-- mneme:managed:start" and "<!-- mneme:managed:end -->" is skipped
// because that content is owned by mneme, not by the user.
//
// DetectDrift never modifies any file. Exit is always nil (advisory only).
// False positives are acceptable. Returns an empty slice for a clean file.
func DetectDrift(filePath string) ([]DriftFinding, error) {
	f, err := os.Open(filePath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("service: drift: open %s: %w", filePath, err)
	}
	defer f.Close() //nolint:errcheck

	var findings []DriftFinding
	scanner := bufio.NewScanner(f)
	// A CLAUDE.md with one line past bufio.Scanner's unconfigured 64 KiB
	// default token size used to make Scan() fail with bufio.ErrTooLong,
	// which the Err() check below already turned into an error — but that
	// error made DetectDrift's caller (RunDrift) discard every finding
	// already collected, reporting an empty drift report plus a warning
	// instead of the real findings (SPEC-104 DD10). 1 MiB is generous for a
	// single line of project documentation; a line longer than that is a
	// pathological case (e.g. a minified CLAUDE.md) this detector is willing
	// to skip rather than protect against unboundedly.
	const maxDriftLine = 1024 * 1024
	scanner.Buffer(make([]byte, 64*1024), maxDriftLine)
	lineNum := 0
	inManagedBlock := false

	for scanner.Scan() {
		lineNum++
		raw := scanner.Text()
		lower := strings.ToLower(raw)
		trimmed := strings.TrimSpace(lower)

		// Track managed block boundaries — skip content inside them.
		if strings.Contains(raw, "<!-- mneme:managed:start") {
			inManagedBlock = true
			continue
		}
		if strings.Contains(raw, managedBlockEndTag) {
			inManagedBlock = false
			continue
		}
		if inManagedBlock {
			continue
		}

		// (a) Duplicated section check: match markdown headings.
		if strings.HasPrefix(trimmed, "#") {
			headingText := strings.TrimLeft(trimmed, "#")
			headingText = strings.TrimSpace(headingText)
			for _, canon := range canonicalSections {
				if strings.Contains(headingText, canon) {
					findings = append(findings, DriftFinding{
						File: filePath,
						Line: lineNum,
						Message: fmt.Sprintf(
							"duplicates global manual section %q; consider removing (now global)",
							canon,
						),
					})
					break
				}
			}
		}

		// (b) Enforcement contradiction check.
		for _, phrase := range enforcementContradictions {
			if strings.Contains(lower, phrase) {
				findings = append(findings, DriftFinding{
					File: filePath,
					Line: lineNum,
					Message: "contradicts enforcement (orchestrator cannot edit code since v1.4.0); remove or rephrase",
				})
				break
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return findings, fmt.Errorf("service: drift: scan %s: %w", filePath, err)
	}
	return findings, nil
}

// managedBlockEndTag is the literal end marker used by the managed block
// primitive. Defined here to avoid a cross-package dependency on internal/install.
const managedBlockEndTag = "<!-- mneme:managed:end -->"
