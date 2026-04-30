package rules

import (
	"fmt"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// ValidatePattern checks whether a single applies_to entry is syntactically
// valid before it is persisted. It catches common mistakes early so users get
// immediate feedback rather than discovering broken patterns at hook runtime.
//
// Accepted forms:
//   - "**"                    — global wildcard
//   - "tool:Name"             — tool selector (Name must be non-empty)
//   - "path/glob/**"          — doublestar path glob
//   - "tool:Name+path/glob"   — combined (AND) selector
//   - "!pattern"              — negation of any valid positive pattern
//
// Rejected forms:
//   - "" (empty string)
//   - "tool:X+tool:Y" (two tool selectors — logically impossible AND)
//   - "tool:" (empty tool name)
//   - any path with an invalid doublestar glob syntax
//   - "tool:Edit+" (empty path after the plus)
func ValidatePattern(entry string) error {
	if entry == "" {
		return fmt.Errorf("pattern must not be empty")
	}

	// Strip negation prefix and validate the positive form.
	positive := entry
	if strings.HasPrefix(entry, "!") {
		positive = strings.TrimPrefix(entry, "!")
		if positive == "" {
			return fmt.Errorf("invalid pattern %q: negation requires a non-empty pattern after '!'", entry)
		}
	}

	return validatePositiveEntry(positive, entry)
}

// validatePositiveEntry validates a single positive (non-negated) applies_to
// entry and returns a descriptive error when the entry is malformed.
// rawEntry is the original (possibly negated) entry used only for error messages.
func validatePositiveEntry(entry, rawEntry string) error {
	// Global wildcard is always valid.
	if entry == "**" {
		return nil
	}

	if strings.Contains(entry, "+") {
		return validateCombinedEntry(entry, rawEntry)
	}

	if strings.HasPrefix(entry, "tool:") {
		return validateToolSelector(entry, rawEntry)
	}

	// Everything else is treated as a path glob.
	return validatePathGlob(entry, rawEntry)
}

// validateCombinedEntry validates "tool:Name+path/glob" entries.
func validateCombinedEntry(entry, rawEntry string) error {
	parts := strings.SplitN(entry, "+", 2)
	left, right := parts[0], parts[1]

	if right == "" {
		return fmt.Errorf("invalid pattern %q: path is empty after '+'", rawEntry)
	}

	// Both sides must be valid independently, but two tool selectors are not allowed.
	leftIsTool := strings.HasPrefix(left, "tool:")
	rightIsTool := strings.HasPrefix(right, "tool:")

	if leftIsTool && rightIsTool {
		return fmt.Errorf("invalid pattern %q: combined entries cannot have two tool selectors", rawEntry)
	}

	if leftIsTool {
		if err := validateToolSelector(left, rawEntry); err != nil {
			return err
		}
		return validatePathGlob(right, rawEntry)
	}

	// Non-tool left side: both are treated as path globs (unusual but not prohibited
	// by the matching engine — entryMatch handles it via doublestar).
	if err := validatePathGlob(left, rawEntry); err != nil {
		return err
	}
	return validatePathGlob(right, rawEntry)
}

// validateToolSelector validates a "tool:Name" entry.
func validateToolSelector(entry, rawEntry string) error {
	name := strings.TrimPrefix(entry, "tool:")
	if name == "" {
		return fmt.Errorf("invalid pattern %q: tool name must not be empty after 'tool:'", rawEntry)
	}
	return nil
}

// validatePathGlob validates that the entry is a syntactically valid doublestar
// glob. It does not check whether the glob matches anything — that is a semantic
// concern handled by mneme rule test.
func validatePathGlob(entry, rawEntry string) error {
	// doublestar.Match returns an error only for invalid glob syntax (e.g. "[[bad").
	// We probe with a dummy path; the match result is irrelevant.
	if _, err := doublestar.Match(entry, "probe"); err != nil {
		return fmt.Errorf("invalid pattern %q: %w", rawEntry, err)
	}
	return nil
}
