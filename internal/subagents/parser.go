package subagents

import (
	"fmt"
	"strings"
)

// GeneratedSection is a single "## " section parsed out of raw LLM output.
type GeneratedSection struct {
	// Heading is the section title, without the leading "## ".
	Heading string

	// Content is the section body, trimmed of leading/trailing blank lines.
	Content string
}

// GeneratedBody is the normalized result of parsing raw markdown produced by
// a GenerationEngine. It holds enough structure for ProfileComposer to seed
// ComposeInput.Body while still keeping Content available for callers that
// just want the full cleaned markdown.
type GeneratedBody struct {
	// Title is the first "# " heading found in the raw output.
	Title string

	// Sections lists every "## " section found, in document order.
	Sections []GeneratedSection

	// Content is the full output after fence-stripping and trimming — this
	// is the value ProfileComposer should use to seed ComposeInput.Body.
	Content string
}

// ParseGenerated normalizes raw output from a GenerationEngine: it strips
// any wrapping Markdown code fence, extracts the H1 title and H2 sections,
// and validates that the output is non-empty and contains at least a title.
//
// ParseGenerated does not require H2 sections to be present — a
// GenerationEngine may return prose without subheadings — but Sections will
// be empty in that case, and ComposeInput.Body/Validate's area-section check
// will then need those sections to come from elsewhere.
func ParseGenerated(raw string) (*GeneratedBody, error) {
	cleaned := strings.TrimSpace(stripCodeFences(raw))
	if cleaned == "" {
		return nil, fmt.Errorf("subagents: parse generated: empty content after stripping code fences")
	}

	title, err := extractH1(cleaned)
	if err != nil {
		return nil, err
	}

	return &GeneratedBody{
		Title:    title,
		Sections: extractH2Sections(cleaned),
		Content:  cleaned,
	}, nil
}

// stripCodeFences removes a single leading and trailing Markdown code fence
// (e.g. "```markdown" / "```") if the ENTIRE raw output is wrapped in one —
// a common LLM habit when asked to "generate a markdown file". Fences that
// appear mid-document (e.g. as part of a code example inside the generated
// content) are left untouched.
func stripCodeFences(raw string) string {
	trimmed := strings.TrimSpace(raw)
	lines := strings.Split(trimmed, "\n")
	if len(lines) < 2 {
		return raw
	}

	firstLine := strings.TrimSpace(lines[0])
	if !strings.HasPrefix(firstLine, "```") {
		return raw
	}

	lastLine := strings.TrimSpace(lines[len(lines)-1])
	if lastLine != "```" {
		return raw
	}

	return strings.Join(lines[1:len(lines)-1], "\n")
}

// extractH1 returns the text of the first "# " heading line in content.
func extractH1(content string) (string, error) {
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# ")), nil
		}
	}
	return "", fmt.Errorf("subagents: parse generated: missing '# Title' heading")
}

// extractH2Sections walks content line by line, splitting it into
// GeneratedSection values at each "## " heading. Content before the first
// "## " heading (e.g. the H1 title and any intro prose) is not included in
// any section.
func extractH2Sections(content string) []GeneratedSection {
	var sections []GeneratedSection
	var current *GeneratedSection
	var body strings.Builder

	flush := func() {
		if current == nil {
			return
		}
		current.Content = strings.TrimSpace(body.String())
		sections = append(sections, *current)
		body.Reset()
	}

	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "## ") {
			flush()
			heading := strings.TrimSpace(strings.TrimPrefix(line, "## "))
			current = &GeneratedSection{Heading: heading}
			continue
		}
		if current != nil {
			body.WriteString(line)
			body.WriteString("\n")
		}
	}
	flush()

	return sections
}
