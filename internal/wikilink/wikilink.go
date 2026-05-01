// Package wikilink provides a Markdown-aware parser for [[topic_key]] wikilinks.
// It extracts structured link information from text content while correctly
// skipping fenced code blocks (``` and ~~~) and inline code spans.
//
// The package has zero external dependencies — it operates purely on strings.
// Import direction: service/ -> wikilink/ (leaf).
package wikilink

import (
	"regexp"
	"strings"
)

// Link represents a single parsed wikilink extracted from text content.
type Link struct {
	// Topic is the topic_key referenced by the wikilink. Always non-empty for
	// a valid link.
	Topic string

	// Anchor is the optional section anchor after # (e.g. "jwt" from
	// "[[topic#jwt]]"). Empty when no anchor is present.
	Anchor string

	// Alias is the optional display text after | (e.g. "Auth" from
	// "[[topic|Auth]]"). Empty when no alias is present.
	Alias string

	// Line is the 0-based line number where the wikilink was found.
	Line int
}

var (
	// reWikilink extracts [[topic]], [[topic#anchor]], [[topic|alias]], and
	// [[topic#anchor|alias]] forms. Group 1 is the topic (required), group 2
	// is the anchor (optional), group 3 is the alias (optional).
	reWikilink = regexp.MustCompile(`\[\[([^\]|#]+?)(?:#([^\]|]+?))?(?:\|([^\]]+?))?\]\]`)

	// reInlineCode matches a single-backtick inline code span. It is stripped
	// from each line before wikilink extraction to prevent links inside inline
	// code from being parsed. Paired multi-backtick spans (`` ` ``) are not
	// handled in v1 — they are rare enough that the complexity is not justified.
	reInlineCode = regexp.MustCompile("`[^`]+`")

	// reFenceOpen detects the opening marker of a fenced code block: three or
	// more backticks or tildes at the start of a trimmed line, optionally
	// followed by an info string. The first capture group contains only the
	// repeated fence character so we can match the closing fence correctly
	// (CommonMark 4.5: closing fence must use the same character and be at
	// least as long as the opening fence).
	reFenceOpen = regexp.MustCompile("^(`{3,}|~{3,})")
)

// Parse extracts all wikilinks from content, skipping fenced code blocks
// (``` and ~~~) and inline code spans. Returns an empty slice when no
// wikilinks are found or the content is empty.
//
// The state machine implements CommonMark section 4.5 fenced code block rules:
// a closing fence must use the same character (backtick or tilde) and must
// consist of at least as many repetitions as the opening fence.
func Parse(content string) []Link {
	if content == "" {
		return nil
	}

	var links []Link
	lines := strings.Split(content, "\n")

	var fenceChar byte // '`' or '~', zero when not in a code block
	var fenceLen int   // length of the opening fence marker

	for lineNum, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Check whether this line opens or closes a fenced code block.
		if m := reFenceOpen.FindStringSubmatch(trimmed); m != nil {
			marker := m[1]
			ch := marker[0]
			ln := len(marker)

			if fenceChar == 0 {
				// Not currently in a code block — open one.
				fenceChar = ch
				fenceLen = ln
				continue
			}

			// Inside a code block: close only if same character and long enough.
			if ch == fenceChar && ln >= fenceLen {
				fenceChar = 0
				fenceLen = 0
			}
			continue
		}

		if fenceChar != 0 {
			// Inside a fenced code block — skip wikilink extraction.
			continue
		}

		// Strip inline code spans before extracting wikilinks so that links
		// appearing inside backtick spans are not captured.
		cleaned := reInlineCode.ReplaceAllString(line, "")

		for _, match := range reWikilink.FindAllStringSubmatch(cleaned, -1) {
			// match[1] = topic (always present by regex design)
			// match[2] = anchor (may be empty string when group did not match)
			// match[3] = alias  (may be empty string when group did not match)
			links = append(links, Link{
				Topic:  match[1],
				Anchor: match[2],
				Alias:  match[3],
				Line:   lineNum,
			})
		}
	}

	return links
}
