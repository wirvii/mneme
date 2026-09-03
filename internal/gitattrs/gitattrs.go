// Package gitattrs implements the pure half of SPEC-140's .gitattributes
// mechanism: rendering the exact D9 block, deciding what to do about it
// given what git already reports for the three covered path sets (D10),
// and upserting that block into an existing file's text.
//
// This package is a leaf — stdlib only, no imports of any other internal
// mneme package — following the "the leaf PLANS, the service EXECUTES"
// split SPEC-099/100 already established: internal/service runs
// `git check-attr` and writes the file; nothing in this package touches
// disk or spawns a process.
//
// It is a package of its own rather than an extension of
// internal/managedblock because a .gitattributes block is delimited by
// "#"-comment markers with no version number, unlike managedblock's
// "<!-- mneme:<marker>:start v=N -->" convention: widening that leaf's
// interface for a single consumer would cost more than a small new
// package.
package gitattrs

import "strings"

// startComment and endComment are the exact D9 marker lines. Kept as
// package-level constants (not derived from a "marker name" the way
// internal/managedblock parametrizes StartMarker/EndMarker) because
// .gitattributes carries exactly one mneme-owned block, never several —
// there is nothing here to distinguish by name.
const (
	startComment = "# >>> mneme (SPEC-140): finales de línea de los ficheros que mneme escribe y vuelve a leer >>>"
	endComment   = "# <<< mneme (SPEC-140) <<<"
)

// Pattern identifies one of the three path sets D9 covers: paths mneme
// itself writes and later re-reads with a line-ending-sensitive parser.
type Pattern string

// The three patterns D9 declares, in the exact order the rendered block
// lists them.
const (
	PatternSDD          Pattern = ".mneme/**"
	PatternClaudeAgents Pattern = ".claude/agents/**"
	PatternCodexAgents  Pattern = ".codex/agents/**"
)

// Patterns returns the three patterns D9 covers, in the block's own
// declared order — the single source callers iterate over instead of
// re-listing the set by hand (Forma 4 of the dead-criteria catalog).
func Patterns() []Pattern {
	return []Pattern{PatternSDD, PatternClaudeAgents, PatternCodexAgents}
}

// ProbePath returns a representative path under p suitable for
// `git check-attr eol -- <path>` (D10). The path need not exist — AC6
// verifies exactly that assumption, rather than this package assuming it.
func ProbePath(p Pattern) string {
	switch p {
	case PatternSDD:
		return ".mneme/sdd/probe.md"
	case PatternClaudeAgents:
		return ".claude/agents/probe.md"
	case PatternCodexAgents:
		return ".codex/agents/probe.toml"
	default:
		return ""
	}
}

// Block returns the exact D9 text, marker lines included, terminated by a
// single trailing newline. It is byte-stable: any change here is a change
// to what AC7/AC8/AC9 compare against.
func Block() string {
	return strings.Join([]string{
		startComment,
		".mneme/**        text eol=lf",
		".claude/agents/** text eol=lf",
		".codex/agents/**  text eol=lf",
		endComment,
	}, "\n") + "\n"
}

// Action is the three-way verdict D10's table assigns to the whole probed
// set.
type Action int

const (
	// ActionSkip means the file is already correct — either the user's own
	// rules already resolve every pattern to "lf", or a previous mneme
	// block already did. Nothing is written.
	ActionSkip Action = iota

	// ActionWrite means at least one pattern is "unspecified" and none is
	// an explicit value other than "lf": the block should be written
	// (or replaced).
	ActionWrite

	// ActionConflict means some pattern already resolves to an explicit
	// value other than "lf" — someone's deliberate rule. Nothing is
	// written; the caller reports it instead.
	ActionConflict
)

// Decision is Decide's pure verdict over a probed set.
type Decision struct {
	Action Action
	// Pattern and Value are populated only when Action == ActionConflict:
	// the first pattern (in Patterns() order) whose probed value is an
	// explicit value other than "lf".
	Pattern Pattern
	Value   string
}

// Decide implements D10's three-row table: probes maps each Pattern to
// whatever `git check-attr eol` answered for its ProbePath (e.g. "lf",
// "crlf", "unspecified"). Decide never runs git itself — it is a pure
// function of answers the caller already collected — so the git-execution
// half (internal/service) stays the only place a real process is spawned.
func Decide(probes map[Pattern]string) Decision {
	for _, p := range Patterns() {
		v := probes[p]
		if v != "" && v != "unspecified" && v != "lf" {
			return Decision{Action: ActionConflict, Pattern: p, Value: v}
		}
	}
	for _, p := range Patterns() {
		if probes[p] == "unspecified" {
			return Decision{Action: ActionWrite}
		}
	}
	return Decision{Action: ActionSkip}
}

// Upsert returns the result of upserting D9's block into existing text.
// Pure: no I/O. If existing already contains the block's start marker (any
// prior mneme-written copy), the entire block — from the start marker
// through the end marker — is replaced in place; otherwise the block is
// appended at the end (preceded by a blank line when existing is
// non-empty). Upsert never removes or edits a line outside the block: a
// deliberate user rule earlier in the file, or a rule mneme is not
// authorized to touch per Decide's ActionConflict verdict, is preserved
// byte-for-byte — callers only reach Upsert on ActionWrite.
func Upsert(existing string) string {
	block := Block()

	startIdx := strings.Index(existing, startComment)
	if startIdx == -1 {
		trimmed := strings.TrimRight(existing, "\n")
		if trimmed == "" {
			return block
		}
		return trimmed + "\n\n" + block
	}

	endIdx := strings.Index(existing, endComment)
	if endIdx == -1 || endIdx < startIdx {
		// A start marker with no matching end marker is not a shape this
		// package ever wrote itself — treat it as unrelated content and
		// append a fresh block rather than guessing at repair.
		trimmed := strings.TrimRight(existing, "\n")
		return trimmed + "\n\n" + block
	}

	before := strings.TrimRight(existing[:startIdx], "\n")
	after := strings.TrimLeft(existing[endIdx+len(endComment):], "\n")

	var b strings.Builder
	if before != "" {
		b.WriteString(before)
		b.WriteString("\n\n")
	}
	b.WriteString(block)
	if after != "" {
		b.WriteString("\n")
		b.WriteString(after)
		if !strings.HasSuffix(after, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}
