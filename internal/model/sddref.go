package model

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// sddRefPattern matches bare BL-<n> / SPEC-<n> mentions anywhere in a
// memory's text, INCLUDING inside backticks and fenced code blocks (D4).
// This is a deliberate departure from internal/wikilink, whose [[...]]
// machinery skips fenced/quoted text because a link there is "an example,
// not a reference" — a rule that would be false and destructive for a bare
// identifier: the dominant citation form in this repo's own memories is
// `SPEC-125` between backticks, and skipping code spans would discard the
// majority of real mentions.
//
// \b boundaries come for free from the fixed BL-/SPEC- prefix: "TABLE-001"
// never matches (no boundary between the two word characters "A" and "B"),
// and "EPIC-calidad" never matches (wrong prefix, no digits after it).
var sddRefPattern = regexp.MustCompile(`\b(BL|SPEC)-(\d+)\b`)

// ParseSDDRefs extracts every BL-<n> / SPEC-<n> mention from text, normalizes
// each number to the same minimum-3-digit form the store produces when it
// mints a new correlative (fmt.Sprintf("%s-%03d", prefix, n) — so "BL-1" and
// "BL-001" collapse to the same mention, while "SPEC-1234" keeps its four
// digits since %03d only enforces a MINIMUM width), and deduplicates while
// preserving the order of first appearance.
//
// This is the SINGLE definition of "what counts as a mention" — the live
// write path (service.bakeSDDRefs) and the one-shot backfill
// (service.BackfillSDDRefs) both call this exact function rather than each
// growing their own notion of a mention, which is a correctness property,
// not a convenience (SPEC-128 D7): two independent definitions would
// eventually disagree on the same text.
func ParseSDDRefs(text string) []string {
	matches := sddRefPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(matches))
	refs := make([]string, 0, len(matches))
	for _, m := range matches {
		n, err := strconv.Atoi(m[2])
		if err != nil {
			// Unreachable in practice: \d+ only ever captures digits. Skip
			// defensively rather than panic on a match the regex itself
			// guarantees is numeric.
			continue
		}
		ref := fmt.Sprintf("%s-%03d", m[1], n)
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}
	return refs
}

// SDDRefKind reports which SDD table a normalized reference ID names, purely
// from its prefix. Returns "" for anything that isn't a recognised SDD
// reference — callers treat that as "not an SDD reference", never an error:
// this is a pure classification helper with no failure mode of its own.
func SDDRefKind(refID string) string {
	switch {
	case strings.HasPrefix(refID, "BL-"):
		return "backlog"
	case strings.HasPrefix(refID, "SPEC-"):
		return "spec"
	default:
		return ""
	}
}

// SDDRefStatus is the closed vocabulary MemoryService.Get assigns to a
// resolved SDD reference (SPEC-128 D8): the single point that checks a
// mention against the LOCAL project database.
type SDDRefStatus string

const (
	// SDDRefLocal means a row in THIS database carries the anchor: LocalID
	// holds that row's current, possibly-renumbered correlative.
	SDDRefLocal SDDRefStatus = "local"

	// SDDRefForeign means an anchor IS registered for this mention, but no
	// row in this database carries it. This is the honest failure this spec
	// exists to produce in place of yesterday's silent false match — never
	// fall back to looking up the bare correlative when this happens.
	SDDRefForeign SDDRefStatus = "foreign"

	// SDDRefUnanchored means the text names an item that was never anchored
	// at write time — e.g. authored by someone else (D5), or written before
	// this mechanism existed and not yet reached by the backfill.
	SDDRefUnanchored SDDRefStatus = "unanchored"
)

var validSDDRefStatuses = map[SDDRefStatus]struct{}{
	SDDRefLocal:      {},
	SDDRefForeign:    {},
	SDDRefUnanchored: {},
}

// Valid reports whether the SDDRefStatus is one of the recognised constants,
// following the same shape as Lane.Valid, Priority.Valid and
// SpecFreezeState's own validation.
func (s SDDRefStatus) Valid() bool {
	_, ok := validSDDRefStatuses[s]
	return ok
}

// SDDRef is one BL-<n>/SPEC-<n> mention a memory's text carries — the twin of
// Files (SPEC-128 D3): a model field backed by a lateral table
// (memory_sdd_refs), the same shape memory_files already proves in
// production.
type SDDRef struct {
	// RefID is the mention as normalized text, e.g. "SPEC-125".
	RefID string `json:"ref_id"`

	// TargetUUID is the anchor the mention resolved to AT WRITE TIME, on the
	// machine that wrote the mention (SPEC-128 D5). Empty means the mention
	// was never anchored — still worth recording, as an unanchored
	// reference.
	TargetUUID string `json:"target_uuid,omitempty"`

	// Status is set ONLY by MemoryService.Get, the single path that resolves
	// a reference against the local database (D8). An empty Status means
	// "not evaluated on this read path" — NEVER "correct" or "resolved
	// nowhere". Search, mem_context, and every other multi-memory read path
	// leave this empty on purpose: resolving would cost one query per
	// reference per result, for a value the caller isn't looking at yet.
	Status SDDRefStatus `json:"status,omitempty"`

	// LocalID is the CURRENT correlative (e.g. "SPEC-125") of the row this
	// reference resolved to in the LOCAL database. Only ever set when Status
	// is SDDRefLocal.
	LocalID string `json:"local_id,omitempty"`
}
