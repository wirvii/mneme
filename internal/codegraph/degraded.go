// Package codegraph — this file defines the declared-degradation contract
// (SPEC-142): when a language's extractor toolchain is present but unusable,
// the graph no longer aborts the whole index run. Instead it records WHICH
// languages could not be indexed and lets every query surface declare that
// fact. The guiding rule (SPEC-142 §0): a partial graph must never be
// readable as a complete one.
package codegraph

import (
	"encoding/json"
	"sort"
	"strings"
)

// MetaKeyDegradedLanguages is the project_metadata key under which the set of
// languages this graph could NOT index is recorded (SPEC-142 D1). It lives in
// the same project_metadata table as MetaKeyLastIndexedSHA — a general-purpose
// "per-project settings and state flags" store — rather than a new table or
// column, so a binary predating this spec simply ignores the key instead of
// needing a schema migration to tolerate it.
const MetaKeyDegradedLanguages = "degraded_languages"

// maxReasonRunes bounds DegradedLanguage.Reason (SPEC-142 D2). Reason is
// diagnostic, not the text a query surface shows on every response — the
// subprocess's stderr message can run to hundreds of characters, and that
// belongs in "mneme codegraph status", never in the one-line Notice.
const maxReasonRunes = 500

// NoticeToken is the fixed, unique-in-the-repository prefix every Notice
// starts with (SPEC-142 D10). It is the anchor a caller checks with
// strings.HasPrefix — never strings.Contains, which would risk matching an
// unrelated substring elsewhere in a longer message.
const NoticeToken = "[mneme:graph-incomplete]"

// DegradedCause names WHY a language is missing from the graph. This is a
// CLOSED set (SPEC-142 D2): a new cause requires a new constant here, never a
// caller-invented string.
type DegradedCause string

const (
	// CauseToolchainIncompatible marks a language whose extractor toolchain is
	// present but unusable — today, ErrExtractorIncompatible. The only real
	// cause this spec's indexer ever writes.
	CauseToolchainIncompatible DegradedCause = "toolchain-incompatible"

	// CauseUnreadableMark marks a language record produced in memory by the
	// reader (never persisted under this cause) when the stored degraded-
	// languages value exists but cannot be parsed (SPEC-142 D16). Declaring
	// incompleteness beats staying silent about a mark nobody can read.
	CauseUnreadableMark DegradedCause = "unreadable-mark"
)

// DegradedLanguage records one language this graph could not fully index, and
// the little diagnostic context a human needs at "mneme codegraph status"
// (SPEC-142 D2). FilesSkippedLastRun is scoped to the MOST RECENT indexing
// pass, never the whole repository — a scoped (git-hook-driven) pass only
// ever sees its own delta, so publishing that count as if it were the
// repository total would be exactly the kind of half-true claim this spec
// exists to prevent. That is also why it never appears in Notice's one-line
// output (see Notice) and is shown only where it can be labelled "last run".
type DegradedLanguage struct {
	// Language is the extractor language identifier (e.g. "typescript").
	Language string `json:"language"`

	// Cause is why the language is degraded. Closed vocabulary (DegradedCause).
	Cause DegradedCause `json:"cause"`

	// Reason is a short, bounded diagnostic (e.g. the extractor's own error
	// text), clamped to maxReasonRunes. Diagnostic only — never shown in the
	// one-line Notice.
	Reason string `json:"reason"`

	// FilesSkippedLastRun is the number of files of this language skipped in
	// the MOST RECENT indexing pass — not a repository-wide total. A scoped
	// pass only sees its own delta and must never be read as "the whole
	// repository has this many skipped files".
	FilesSkippedLastRun int `json:"files_skipped_last_run"`

	// FirstSeenUnix is the Unix timestamp (seconds) this language was first
	// recorded as degraded. Preserved across a full-scan replacement (D6) as
	// long as the language remains degraded; reset only once the language
	// stops being degraded (the record disappears and a later re-degradation
	// starts a new history).
	FirstSeenUnix int64 `json:"first_seen_unix"`

	// LastSeenUnix is the Unix timestamp (seconds) this language was most
	// recently observed as still degraded.
	LastSeenUnix int64 `json:"last_seen_unix"`
}

// clampReason truncates s to at most maxReasonRunes runes. Truncation is
// silent by design — Reason is diagnostic, not a contract callers parse.
func clampReason(s string) string {
	r := []rune(s)
	if len(r) <= maxReasonRunes {
		return s
	}
	return string(r[:maxReasonRunes])
}

// ParseStoredDegradedLanguages deserializes the raw project_metadata value
// stored under MetaKeyDegradedLanguages (SPEC-142 D16). An empty value means
// "no degraded languages recorded" and returns nil. A non-empty value that
// fails to parse returns a single synthetic record with
// Cause=CauseUnreadableMark instead of an error — an unreadable mark must
// still declare incompleteness, never resolve to silence. Shared by
// Store.GetDegradedLanguages (a live write-capable connection) and
// ProbeDegraded (a standalone read-only connection), so the two never
// disagree on how to interpret the same bytes.
func ParseStoredDegradedLanguages(value string) []DegradedLanguage {
	if value == "" {
		return nil
	}
	var langs []DegradedLanguage
	if err := json.Unmarshal([]byte(value), &langs); err != nil {
		return []DegradedLanguage{{
			Language: "unknown",
			Cause:    CauseUnreadableMark,
			Reason:   clampReason(err.Error()),
		}}
	}
	return langs
}

// sortDegradedLanguages sorts langs in place by Language, ascending. Called
// before every serialization (SPEC-142 D2/D18): two runs that end in the same
// degraded state must produce byte-identical canonical JSON, which is what
// lets a caller compare against the stored value and skip a write when
// nothing changed (D18), and what lets tests compare state deterministically.
func sortDegradedLanguages(langs []DegradedLanguage) {
	sort.Slice(langs, func(i, j int) bool { return langs[i].Language < langs[j].Language })
}

// Notice renders the one-line, always-identical banner every query surface
// prepends when the graph is known to be incomplete (SPEC-142 D10). Pure: no
// I/O, no clock, no randomness — this is the ONLY place in the product that
// composes this text, so every surface says exactly the same thing about the
// same fact.
//
// readErr is the error (if any) a caller's own attempt to read the degraded-
// languages record produced (e.g. a database-level failure surfaced by
// ProbeDegraded) — distinct from an unreadable STORED VALUE, which
// GetDegradedLanguages already turns into a synthetic CauseUnreadableMark
// entry in langs rather than a Go error (SPEC-142 D16). When readErr is
// non-nil, Notice fails CLOSED: it cannot know which languages are degraded,
// so it says so and shows anyway, rather than staying silent because the
// question itself could not be answered.
//
// show is false, and line is "", only when there is nothing to declare: no
// error and an empty langs slice.
func Notice(langs []DegradedLanguage, readErr error) (line string, show bool) {
	if readErr != nil {
		return NoticeToken + " degraded-language state could not be read — this graph may be incomplete. Details: mneme codegraph status", true
	}
	if len(langs) == 0 {
		return "", false
	}

	sorted := make([]DegradedLanguage, len(langs))
	copy(sorted, langs)
	sortDegradedLanguages(sorted)

	names := make([]string, len(sorted))
	cause := sorted[0].Cause
	for i, l := range sorted {
		names[i] = l.Language
		if l.Cause != cause {
			cause = "mixed"
		}
	}

	var b strings.Builder
	b.WriteString(NoticeToken)
	b.WriteString(" ")
	b.WriteString(strings.Join(names, ", "))
	b.WriteString(" NOT indexed (")
	b.WriteString(string(cause))
	b.WriteString(") — a symbol missing from this answer may still exist. Details: mneme codegraph status")
	return b.String(), true
}
