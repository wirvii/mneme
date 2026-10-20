package skill

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// MainFileSoftLimit is the SKILL.md size in bytes above which the linter
	// warns that detail should move into references/ files.
	MainFileSoftLimit = 4096

	// MainFileHardLimit is the SKILL.md size in bytes above which the linter
	// reports an error, unless the skill uses the legacy five-section format.
	MainFileHardLimit = 8192

	// minDescriptionLen and maxDescriptionLen bound the advisory description
	// length check.
	minDescriptionLen = 20
	maxDescriptionLen = 500
)

// Severity classifies the urgency of a lint finding. Higher severity findings
// block automated operations; lower ones are advisory only.
type Severity int

const (
	// SeverityError means the skill fails a hard requirement. The skill must
	// not be used until the error is resolved.
	SeverityError Severity = iota

	// SeverityWarning means the skill violates a quality guideline but is
	// still usable. Operators should fix warnings at their earliest convenience.
	SeverityWarning

	// SeverityInfo means an advisory observation. No action required.
	SeverityInfo
)

// Finding is a single lint observation with an associated severity and message.
type Finding struct {
	// Severity classifies the urgency of this finding.
	Severity Severity

	// Message is a human-readable description of the issue.
	Message string
}

// LintResult contains all lint findings for a single skill. Passed is true when
// there are no error-severity findings.
type LintResult struct {
	// Name is the skill name, taken from the directory name.
	Name string

	// Errors contains all error-severity findings (hard requirements).
	Errors []Finding

	// Warnings contains all warning-severity findings (quality guidelines).
	Warnings []Finding

	// Infos contains all info-severity findings (advisory observations).
	Infos []Finding

	// Passed is true when Errors is empty after running all checks.
	Passed bool
}

// requiredSections lists the five mandatory H2 section headings. Matching is
// case-insensitive and compares only the trimmed heading text.
var requiredSections = []string{
	"when to use",
	"critical rules",
	"automated checks",
	"verification",
	"workflow",
}

// automatedChecksHeaders lists the exact three column headers required in the
// "Automated Checks" table. Matching is case-insensitive with trimmed values.
var automatedChecksHeaders = []string{
	"check",
	"what it verifies",
	"how to fix",
}

// semverRe matches a semantic version string X.Y.Z optionally followed by a
// pre-release identifier (-[0-9A-Za-z.-]+) and/or build metadata
// (+[0-9A-Za-z.-]+). The pattern is anchored at both ends so that strings
// like "1.2.3garbage" are rejected.
var semverRe = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

// LintFile parses and lints the SKILL.md at path. dirName is the skill
// directory name (used to verify name==dir). References cited from the body
// are resolved against the directory that contains the file.
func LintFile(path, dirName string) LintResult {
	return LintFS(os.DirFS(filepath.Dir(path)), dirName)
}

// LintFS lints the skill rooted at fsys, which must contain SKILL.md. It runs
// every check Lint runs and, because it can reach the directory, also verifies
// that each references/<path> cited in the body exists and stays inside the
// skill directory.
func LintFS(fsys fs.FS, dirName string) LintResult {
	data, err := fs.ReadFile(fsys, "SKILL.md")
	var s *Skill
	if err == nil {
		s, err = Parse(data)
	}
	if err != nil {
		return LintResult{
			Name:   dirName,
			Errors: []Finding{{Severity: SeverityError, Message: "cannot parse SKILL.md: " + err.Error()}},
		}
	}
	result := Lint(s, dirName)
	for _, ref := range citedReferences(s.RawBody) {
		rel := strings.TrimPrefix(ref, "references/")
		if strings.HasPrefix(rel, "/") || containsDotDot(rel) {
			result.Errors = append(result.Errors, Finding{
				Severity: SeverityError,
				Message:  "body: la referencia escapa del directorio: " + ref,
			})
			continue
		}
		if _, statErr := fs.Stat(fsys, path.Clean(ref)); statErr != nil {
			result.Errors = append(result.Errors, Finding{
				Severity: SeverityError,
				Message:  "body: referenced file does not exist: " + ref,
			})
		}
	}
	result.Passed = len(result.Errors) == 0
	return result
}

// referenceRe finds citations of files under references/ in a skill body.
var referenceRe = regexp.MustCompile("(?:^|[^A-Za-z0-9_./-])(references/[^\\s)\\]'\"`<>,;]+)")

// citedReferences returns the deduplicated references/... paths cited in body,
// in order of appearance. It scans the raw text (code blocks included) so the
// result is deterministic without parsing Markdown.
func citedReferences(body string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range referenceRe.FindAllStringSubmatch(body, -1) {
		ref := strings.TrimRight(m[1], ".:")
		if ref == "references/" || seen[ref] {
			continue
		}
		seen[ref] = true
		out = append(out, ref)
	}
	return out
}

// containsDotDot reports whether any path element of rel is "..".
func containsDotDot(rel string) bool {
	for _, el := range strings.Split(rel, "/") {
		if el == ".." {
			return true
		}
	}
	return false
}

// isLegacyFormat reports whether the skill carries all five legacy H2 sections.
func isLegacyFormat(s *Skill) bool {
	have := make(map[string]bool, len(s.Sections))
	for _, sec := range s.Sections {
		have[strings.ToLower(strings.TrimSpace(sec.Heading))] = true
	}
	for _, req := range requiredSections {
		if !have[req] {
			return false
		}
	}
	return true
}

// Lint runs all structural checks against a parsed Skill. dirName is the
// directory name of the skill — the linter verifies that name==dirName.
//
// The checks are purely deterministic Go logic: no scripts are executed, no
// network requests are made, and no LLM calls occur.
func Lint(s *Skill, dirName string) LintResult {
	result := LintResult{Name: dirName}

	// --- Required frontmatter fields ---

	if s.Name == "" {
		result.Errors = append(result.Errors, Finding{
			Severity: SeverityError,
			Message:  "frontmatter: name is required",
		})
	}
	if s.Description == "" {
		result.Errors = append(result.Errors, Finding{
			Severity: SeverityError,
			Message:  "frontmatter: description is required",
		})
	}
	if s.Version == "" {
		result.Infos = append(result.Infos, Finding{
			Severity: SeverityInfo,
			Message:  "frontmatter: version is absent (optional)",
		})
	}

	// --- name == dirName ---

	if s.Name != "" && dirName != "" && s.Name != dirName {
		result.Errors = append(result.Errors, Finding{
			Severity: SeverityError,
			Message:  "frontmatter: name " + s.Name + " does not match directory name " + dirName,
		})
	}

	// --- semver check ---

	if s.Version != "" && !semverRe.MatchString(s.Version) {
		result.Errors = append(result.Errors, Finding{
			Severity: SeverityError,
			Message:  "frontmatter: version " + s.Version + " is not a valid semver (expected X.Y.Z)",
		})
	}

	// --- description length advisory ---

	if s.Description != "" {
		dlen := len(s.Description)
		if dlen < minDescriptionLen {
			result.Warnings = append(result.Warnings, Finding{
				Severity: SeverityWarning,
				Message:  "frontmatter: description is very short (< 20 characters); add context about when to use this skill",
			})
		} else if dlen > maxDescriptionLen {
			result.Warnings = append(result.Warnings, Finding{
				Severity: SeverityWarning,
				Message:  "frontmatter: description exceeds 500 characters; trim to 1-3 focused sentences",
			})
		}
	}

	if s.Description != "" {
		low := strings.ToLower(s.Description)
		if !strings.Contains(low, "when") && !strings.Contains(low, "cuando") {
			result.Warnings = append(result.Warnings, Finding{
				Severity: SeverityWarning,
				Message:  "frontmatter: description should say WHEN to use the skill (include \"when\" or \"cuando\")",
			})
		}
	}

	// --- size limits ---

	if s.Size > MainFileSoftLimit {
		switch {
		case s.Size <= MainFileHardLimit:
			result.Warnings = append(result.Warnings, Finding{
				Severity: SeverityWarning,
				Message:  fmt.Sprintf("SKILL.md mide %d bytes (> %d); mueve detalle a references/", s.Size, MainFileSoftLimit),
			})
		case isLegacyFormat(s):
			result.Warnings = append(result.Warnings, Finding{
				Severity: SeverityWarning,
				Message: fmt.Sprintf("SKILL.md mide %d bytes (> %d); exento por formato heredado de 5 secciones — mueve detalle a references/",
					s.Size, MainFileHardLimit),
			})
		default:
			result.Errors = append(result.Errors, Finding{
				Severity: SeverityError,
				Message:  fmt.Sprintf("SKILL.md mide %d bytes (> %d); mueve detalle a references/", s.Size, MainFileHardLimit),
			})
		}
	}

	// --- extra keys are informational ---

	for k := range s.Extra {
		result.Infos = append(result.Infos, Finding{
			Severity: SeverityInfo,
			Message:  "frontmatter: unknown key " + k + " (ignored by mneme; may be used by tooling)",
		})
	}

	// --- Automated Checks table (advisory) ---

	for _, sec := range s.Sections {
		if strings.ToLower(strings.TrimSpace(sec.Heading)) == "automated checks" {
			if msg := lintAutomatedChecksTable(sec.Content); msg != "" {
				result.Warnings = append(result.Warnings, Finding{
					Severity: SeverityWarning,
					Message:  "body: Automated Checks: " + msg,
				})
			}
			break
		}
	}

	result.Passed = len(result.Errors) == 0
	return result
}

// lintAutomatedChecksTable checks whether the section content contains a
// markdown table with exactly the three required headers. Returns an empty
// string on success or a human-readable error message on failure.
func lintAutomatedChecksTable(content string) string {
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			continue
		}
		// This looks like a table header row. Split by | and inspect cells.
		cells := strings.Split(trimmed, "|")
		// A pipe-delimited row: |A|B|C| splits as ["", "A", "B", "C", ""].
		var headers []string
		for _, c := range cells {
			t := strings.TrimSpace(c)
			if t != "" {
				headers = append(headers, t)
			}
		}

		if len(headers) < 3 {
			continue // not a data row; skip separator rows
		}

		// Check that the first 3 headers match exactly.
		if len(headers) >= 3 {
			for i, want := range automatedChecksHeaders {
				if strings.ToLower(headers[i]) != want {
					return "table header column " + headers[i] + " does not match required " + titleCase(want)
				}
			}
			return "" // valid table found
		}
	}

	return "no markdown table found (expected columns: Check | What it verifies | How to fix)"
}

// titleCase capitalises the first letter of each word. Used for human-readable
// output of the required section/header names.
func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}
