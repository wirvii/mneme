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
//   - "**"                          — global wildcard
//   - "tool:Name"                   — tool selector (Name must be non-empty)
//   - "agent:orchestrator|subagent|*|<other>" — agent selector (empty name invalid)
//   - "path/glob/**"                — doublestar path glob
//   - "part1+part2+..."             — N-part combined (AND) selector
//   - "!pattern"                    — negation of any valid positive pattern
//
// Rejected forms:
//   - "" (empty string)
//   - "tool:X+tool:Y" (two tool selectors — logically impossible AND)
//   - "agent:X+agent:Y" (two agent selectors — logically ambiguous AND)
//   - "tool:" (empty tool name)
//   - "agent:" (empty agent name)
//   - any path with an invalid doublestar glob syntax
//   - "tool:Edit+" (empty part after the plus)
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

	if strings.HasPrefix(entry, "agent:") {
		return validateAgentSelector(entry, rawEntry)
	}

	if strings.HasPrefix(entry, "tool:") {
		return validateToolSelector(entry, rawEntry)
	}

	// Everything else is treated as a path glob.
	return validatePathGlob(entry, rawEntry)
}

// validateCombinedEntry validates N-part "part1+part2+..." entries.
//
// Rules:
//   - No part may be empty.
//   - No two parts may both be tool: selectors (logically impossible AND).
//   - No two parts may both be agent: selectors (logically ambiguous AND).
//   - Each part must be individually valid.
func validateCombinedEntry(entry, rawEntry string) error {
	parts := strings.Split(entry, "+")

	toolCount := 0
	agentCount := 0

	for i, part := range parts {
		if part == "" {
			if i == len(parts)-1 {
				// Trailing + — the common "tool:Edit+" mistake.
				return fmt.Errorf("invalid pattern %q: path is empty after '+'", rawEntry)
			}
			return fmt.Errorf("invalid pattern %q: empty part in combined entry", rawEntry)
		}

		if strings.HasPrefix(part, "tool:") {
			toolCount++
		}
		if strings.HasPrefix(part, "agent:") {
			agentCount++
		}
	}

	if toolCount >= 2 {
		return fmt.Errorf("invalid pattern %q: combined entries cannot have two tool selectors", rawEntry)
	}
	if agentCount >= 2 {
		return fmt.Errorf("invalid pattern %q: combined entries cannot have two agent selectors", rawEntry)
	}

	// Validate each part individually.
	for _, part := range parts {
		if err := validatePositiveEntry(part, rawEntry); err != nil {
			return err
		}
	}
	return nil
}

// validateAgentSelector validates an "agent:Name" entry.
//
// Name must be non-empty. Known values (orchestrator, subagent, *) are
// accepted without warning. Unknown values (e.g. agent:backend) are accepted
// without error for forward-compatibility — the matching engine currently
// returns no-match for unknown agent types, which is the correct DEFERRED
// behaviour. This mirrors the "unknown model alias" policy in model set.
func validateAgentSelector(entry, rawEntry string) error {
	name := strings.TrimPrefix(entry, "agent:")
	if name == "" {
		return fmt.Errorf("invalid pattern %q: agent name must not be empty after 'agent:'", rawEntry)
	}
	// All non-empty names are accepted (unknown names are forward-compat).
	return nil
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
