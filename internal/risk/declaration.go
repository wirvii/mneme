package risk

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ErrMalformedDeclaration is returned for a line that should be a risk
// declaration but does not follow the grammar. A malformed declaration is
// never read as "none": the auditor reports it and the person fixes it.
var ErrMalformedDeclaration = errors.New("risk: malformed declaration")

// minFenceLen is the shortest run of backticks or tildes that opens a
// fenced code block (CommonMark 4.5).
const minFenceLen = 3

// Declaration is a parsed "Risk:" line: either None, or one or more items
// with an optional reason.
type Declaration struct {
	// None is true for "Risk: none" (or its Spanish forms).
	None bool
	// Items holds the declared items, sorted and without duplicates; empty
	// when None.
	Items []Item
	// Reason is the text inside the trailing parentheses, if any.
	Reason string
	// Line is the original line, kept so messages can quote it verbatim.
	Line string
}

// ParseLine parses one declaration line. Accepted forms (keyword
// case-insensitive, free spacing, an optional leading "-" or "*" list
// marker): "Risk: none", "Riesgo: ninguno", "Riesgo: none", and
// "Risk: item N[, M ...] [(reason)]" where "item" may be written "ítem" and
// repeated before each number, and every number is 1..6. Any other line —
// including one that does not start with the keyword — returns an error
// wrapping ErrMalformedDeclaration that quotes the line, so a caller never
// mistakes a typo for a valid "none".
func ParseLine(line string) (Declaration, error) {
	malformed := fmt.Errorf("%w: %q", ErrMalformedDeclaration, line)

	rest, ok := afterKeyword(line)
	if !ok {
		return Declaration{}, malformed
	}
	rest = strings.TrimSpace(rest)

	if strings.EqualFold(rest, "none") || strings.EqualFold(rest, "ninguno") {
		return Declaration{None: true, Line: line}, nil
	}

	head, reason := rest, ""
	if open := strings.Index(rest, "("); open >= 0 {
		if !strings.HasSuffix(rest, ")") {
			return Declaration{}, malformed
		}
		head = rest[:open]
		reason = strings.TrimSpace(rest[open+1 : len(rest)-1])
	}

	items, ok := parseItemList(head)
	if !ok {
		return Declaration{}, malformed
	}
	return Declaration{Items: items, Reason: reason, Line: line}, nil
}

// parseItemList parses "item 1, 5" / "item 1, item 5" / "ítem 3" into a
// sorted, de-duplicated item list. The first entry must carry the "item"
// word; later entries may repeat it.
func parseItemList(head string) ([]Item, bool) {
	parts := strings.Split(head, ",")
	seen := make(map[Item]bool)
	var items []Item
	for i, part := range parts {
		part = strings.TrimSpace(part)
		num, hadWord := stripItemWord(part)
		if i == 0 && !hadWord {
			return nil, false
		}
		n, err := strconv.Atoi(num)
		if err != nil {
			return nil, false
		}
		item := Item(n)
		if !item.Valid() {
			return nil, false
		}
		if !seen[item] {
			seen[item] = true
			items = append(items, item)
		}
	}
	if len(items) == 0 {
		return nil, false
	}
	sort.Slice(items, func(a, b int) bool { return items[a] < items[b] })
	return items, true
}

// stripItemWord removes a leading "item"/"ítem" word (case-insensitive)
// followed by whitespace and reports whether it was there.
func stripItemWord(part string) (string, bool) {
	for _, word := range []string{"item", "ítem"} {
		if len(part) > len(word) && strings.EqualFold(part[:len(word)], word) {
			rest := part[len(word):]
			trimmed := strings.TrimLeft(rest, " \t")
			if len(trimmed) < len(rest) {
				return trimmed, true
			}
		}
	}
	return part, false
}

// afterKeyword returns what follows "Risk:"/"Riesgo:" on a declaration
// line, after trimming and an optional list marker.
func afterKeyword(line string) (string, bool) {
	s := strings.TrimSpace(line)
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "*") {
		s = strings.TrimSpace(s[1:])
	}
	for _, kw := range []string{"risk:", "riesgo:"} {
		if len(s) >= len(kw) && strings.EqualFold(s[:len(kw)], kw) {
			return s[len(kw):], true
		}
	}
	return "", false
}

// isDeclarationLine reports whether a line claims to be a declaration, so
// FindDeclaration only parses (and only reports malformation for) lines
// that start with the keyword.
func isDeclarationLine(line string) bool {
	_, ok := afterKeyword(line)
	return ok
}

// FindDeclaration returns the LAST declaration line of text that sits
// outside fenced code blocks. The last one wins because a delivery may
// restate an earlier line; a malformed last line is reported (found=true
// with an error wrapping ErrMalformedDeclaration) rather than skipped in
// favour of an earlier valid one, so a typo never silently becomes the
// older answer.
//
// Fences follow CommonMark 4.5: a trimmed line of three or more backticks
// or tildes opens a block; only a trimmed line made solely of at least as
// many of the same character closes it (a closing line with text after it
// does not close); an unclosed fence runs to the end of the text. Lines
// inside a block are never declarations, which is what lets the lane
// auditor and the SPEC-185 hook read the same line from the same section.
func FindDeclaration(text string) (Declaration, bool, error) {
	var (
		fenceChar byte
		fenceLen  int
		last      string
		found     bool
	)
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)

		if fenceChar != 0 {
			if isClosingFence(trimmed, fenceChar, fenceLen) {
				fenceChar, fenceLen = 0, 0
			}
			continue
		}
		if ch, n := openingFence(trimmed); n > 0 {
			fenceChar, fenceLen = ch, n
			continue
		}
		if isDeclarationLine(line) {
			last, found = line, true
		}
	}
	if !found {
		return Declaration{}, false, nil
	}
	decl, err := ParseLine(last)
	return decl, true, err
}

// openingFence returns the fence character and run length when trimmed
// opens a fenced code block, or a zero length otherwise.
func openingFence(trimmed string) (byte, int) {
	if trimmed == "" || (trimmed[0] != '`' && trimmed[0] != '~') {
		return 0, 0
	}
	ch := trimmed[0]
	n := 0
	for n < len(trimmed) && trimmed[n] == ch {
		n++
	}
	if n < minFenceLen {
		return 0, 0
	}
	return ch, n
}

// isClosingFence reports whether trimmed is made only of at least n
// repetitions of ch.
func isClosingFence(trimmed string, ch byte, n int) bool {
	if len(trimmed) < n {
		return false
	}
	for i := 0; i < len(trimmed); i++ {
		if trimmed[i] != ch {
			return false
		}
	}
	return true
}
