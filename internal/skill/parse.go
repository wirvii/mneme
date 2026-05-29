// Package skill provides deterministic parsing, linting, and validation for
// mneme skill directories. It is a leaf package — it intentionally imports no
// internal mneme packages so it can be reused without coupling to the store
// or model layers. Frontmatter is parsed with a hand-written line scanner
// (no yaml dependency).
package skill

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Metadata holds the frontmatter fields of a SKILL.md file.
// Only the fields defined here are meaningful; unknown keys are preserved in
// Extra for forward compatibility but otherwise ignored by the linter.
type Metadata struct {
	// Name is the canonical skill identifier. Must equal the directory name.
	Name string

	// Description is a 1-3 sentence human-readable summary of when to use the skill.
	Description string

	// Version is the skill version string. Must be semver (X.Y.Z).
	Version string

	// Pinned, when true, prevents mneme install from overwriting the skill
	// without --force. It also prevents remove without --force.
	Pinned bool

	// License is an optional SPDX license identifier.
	License string

	// Extra holds any frontmatter keys not recognised by this parser.
	// Preserved verbatim for forward compatibility.
	Extra map[string]string
}

// Section represents a single H2 section from the SKILL.md body.
type Section struct {
	// Heading is the text of the H2 heading, without the "## " prefix.
	Heading string

	// Content is the raw Markdown content under the heading, trimmed of
	// leading/trailing blank lines.
	Content string
}

// Skill is the fully parsed representation of a SKILL.md file.
type Skill struct {
	Metadata

	// Sections contains all H2 sections found in the body, in order.
	Sections []Section

	// RawBody is the full Markdown body after the closing frontmatter delimiter,
	// preserved verbatim for callers that need to rewrite or display it.
	RawBody string
}

// ParseFile reads the SKILL.md at path and parses it. It is a convenience
// wrapper around Parse.
func ParseFile(path string) (*Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("skill: parse file: read %q: %w", path, err)
	}
	s, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("skill: parse file: %q: %w", path, err)
	}
	return s, nil
}

// Parse parses a SKILL.md byte slice and returns a Skill. It returns an error
// if the frontmatter delimiters are absent or structurally broken. Missing or
// malformed individual fields are NOT errors here — they are reported by Lint.
func Parse(data []byte) (*Skill, error) {
	s := string(data)
	lines := strings.Split(s, "\n")

	if len(lines) == 0 || lines[0] != "---" {
		return nil, fmt.Errorf("skill: missing opening --- delimiter")
	}

	meta, bodyStart, err := parseFrontmatter(lines)
	if err != nil {
		return nil, err
	}

	// Body is everything after the closing ---\n.
	var rawBody string
	if bodyStart < len(lines) {
		rawBody = strings.Join(lines[bodyStart:], "\n")
		rawBody = strings.TrimPrefix(rawBody, "\n")
	}

	return &Skill{
		Metadata: meta,
		Sections: parseSections(rawBody),
		RawBody:  rawBody,
	}, nil
}

// parseFrontmatter scans lines between the opening and closing "---" delimiters
// and builds a Metadata value. It returns the index of the first body line
// (i.e., the line after the closing ---).
func parseFrontmatter(lines []string) (Metadata, int, error) {
	var meta Metadata
	closingLine := -1

	for i := 1; i < len(lines); i++ {
		line := lines[i]

		if line == "---" {
			closingLine = i
			break
		}

		colonIdx := strings.Index(line, ": ")
		var key, value string
		if colonIdx >= 0 {
			key = strings.TrimSpace(line[:colonIdx])
			value = strings.TrimSpace(line[colonIdx+2:])
		} else if strings.HasSuffix(strings.TrimSpace(line), ":") {
			key = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), ":"))
			value = ""
		} else {
			// blank or malformed — skip for forward compat
			continue
		}

		switch key {
		case "name":
			meta.Name = value
		case "description":
			meta.Description = value
		case "version":
			meta.Version = value
		case "pinned":
			b, err := strconv.ParseBool(value)
			if err == nil {
				meta.Pinned = b
			}
		case "license":
			meta.License = value
		default:
			if meta.Extra == nil {
				meta.Extra = make(map[string]string)
			}
			meta.Extra[key] = value
		}
	}

	if closingLine < 0 {
		return Metadata{}, 0, fmt.Errorf("skill: missing closing --- delimiter")
	}

	return meta, closingLine + 1, nil
}

// parseSections extracts all H2 headings from the Markdown body and returns
// them as a slice of Section values. Content between consecutive H2 headings
// is associated with the preceding heading.
func parseSections(body string) []Section {
	lines := strings.Split(body, "\n")
	var sections []Section
	var current *Section

	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			if current != nil {
				current.Content = strings.TrimSpace(current.Content)
				sections = append(sections, *current)
			}
			heading := strings.TrimPrefix(line, "## ")
			current = &Section{Heading: heading}
			continue
		}
		if current != nil {
			current.Content += line + "\n"
		}
	}

	if current != nil {
		current.Content = strings.TrimSpace(current.Content)
		sections = append(sections, *current)
	}

	return sections
}

// WriteFrontmatter serialises a Metadata value to a frontmatter block in a
// deterministic field order: name, description, version, pinned, license, then
// any extra keys in sorted order. The returned bytes include the opening and
// closing "---" delimiters followed by a newline.
func WriteFrontmatter(m Metadata) []byte {
	var buf bytes.Buffer
	buf.WriteString("---\n")
	buf.WriteString("name: " + m.Name + "\n")
	buf.WriteString("description: " + strconv.Quote(m.Description) + "\n")
	buf.WriteString("version: " + m.Version + "\n")
	if m.Pinned {
		buf.WriteString("pinned: true\n")
	} else {
		buf.WriteString("pinned: false\n")
	}
	if m.License != "" {
		buf.WriteString("license: " + m.License + "\n")
	}
	// Extra keys: sorted for determinism.
	if len(m.Extra) > 0 {
		keys := sortedKeys(m.Extra)
		for _, k := range keys {
			buf.WriteString(k + ": " + m.Extra[k] + "\n")
		}
	}
	buf.WriteString("---\n")
	return buf.Bytes()
}

// RewritePinned rewrites the pinned field in an existing SKILL.md byte slice
// while preserving all other content (body and all other frontmatter fields).
// It returns the updated bytes or an error if the file cannot be parsed.
func RewritePinned(data []byte, pinned bool) ([]byte, error) {
	s, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("skill: rewrite pinned: %w", err)
	}
	s.Metadata.Pinned = pinned

	fm := WriteFrontmatter(s.Metadata)

	var buf bytes.Buffer
	buf.Write(fm)
	if s.RawBody != "" {
		buf.WriteByte('\n')
		buf.WriteString(s.RawBody)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

// sortedKeys returns the keys of a map[string]string in sorted order.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Simple insertion sort — maps are small.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
