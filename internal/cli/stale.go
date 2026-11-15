package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/wirvii/mneme/internal/model"
)

// staleSectionMaxRows caps the spec rows of the STALE section of `mneme
// status` (SPEC-186 D2a); the rest are summarised in one "and N more" line.
const staleSectionMaxRows = 10

// staleTitleMaxRunes is how much of a title a STALE row shows, the width
// BACKLOG and RECENTLY COMPLETED already use.
const staleTitleMaxRunes = 40

// renderStaleSection prints the STALE section of `mneme status` (SPEC-186
// §5): a headline with the count per status, at most staleSectionMaxRows
// rows ordered by days descending, an "and N more" line, and one footer
// each for frozen specs and unreadable rows, which are not counted as
// stale. The caller prints it only when report.Count > 0. Every string is
// in English, like the rest of the dashboard.
func renderStaleSection(w io.Writer, report model.StaleReport) {
	fmt.Fprintln(w, section("STALE", 50))

	specWord, verb := "specs", "have"
	if report.Count == 1 {
		specWord, verb = "spec", "has"
	}
	fmt.Fprintf(w, "  %d %s %s not moved for more than %d days (%s):\n",
		report.Count, specWord, verb, report.ThresholdDays, staleByStatusText(report.ByStatus))

	for i, s := range report.Specs {
		if i == staleSectionMaxRows {
			break
		}
		fmt.Fprintf(w, "  %-10s  %-16s  %4d d  %s\n",
			s.ID, statusTag(string(s.Status)), s.DaysSinceLastMove, truncate(s.Title, staleTitleMaxRunes))
	}
	if report.Count > staleSectionMaxRows {
		fmt.Fprintf(w, "  and %d more (mneme spec list marks them all)\n", report.Count-staleSectionMaxRows)
	}

	switch {
	case report.FrozenCount == 1:
		fmt.Fprintln(w, "  1 frozen spec (backlog item archived or missing) is not counted here: it can never move.")
	case report.FrozenCount > 1:
		fmt.Fprintf(w, "  %d frozen specs (backlog item archived or missing) are not counted here: they can never move.\n",
			report.FrozenCount)
	}
	switch {
	case report.UnreadableCount == 1:
		fmt.Fprintln(w, "  1 spec row could not be read and was not classified (see UNREADABLE above).")
	case report.UnreadableCount > 1:
		fmt.Fprintf(w, "  %d spec rows could not be read and were not classified (see UNREADABLE above).\n",
			report.UnreadableCount)
	}
	fmt.Fprintln(w)
}

// staleByStatusText renders ByStatus as "status count" pairs joined by ", ",
// ordered by count descending and, on a tie, by status name ascending, so
// the same data always prints the same headline.
func staleByStatusText(byStatus map[model.SpecStatus]int) string {
	statuses := make([]model.SpecStatus, 0, len(byStatus))
	for status := range byStatus {
		statuses = append(statuses, status)
	}
	sort.Slice(statuses, func(i, j int) bool {
		a, b := statuses[i], statuses[j]
		if byStatus[a] != byStatus[b] {
			return byStatus[a] > byStatus[b]
		}
		return a < b
	})
	parts := make([]string, len(statuses))
	for i, status := range statuses {
		parts[i] = fmt.Sprintf("%s %d", status, byStatus[status])
	}
	return strings.Join(parts, ", ")
}

// staleNoticeMaxIDs caps how many stale specs the session-start line names
// (SPEC-186 D2b); the rest are summarised as "y N más".
const staleNoticeMaxIDs = 5

// staleNoticeMaxBytes is the session-start line's byte budget (SPEC-186 D5).
const staleNoticeMaxBytes = 400

// staleNoticeLine is the one line session-start prints when something is
// stale (SPEC-186 §6), in Spanish like the session block beside it, without
// a trailing newline. It names at most staleNoticeMaxIDs specs in the
// report's order and, should the line exceed staleNoticeMaxBytes whatever
// the data, drops entries from the end (growing "y N más") until it fits,
// falling back to the count alone.
func staleNoticeLine(report model.StaleReport) string {
	const tail = ". Detalle: `mneme status`."
	prefix := fmt.Sprintf("%d specs llevan más de %d días sin moverse", report.Count, report.ThresholdDays)
	if report.Count == 1 {
		prefix = fmt.Sprintf("1 spec lleva más de %d días sin moverse", report.ThresholdDays)
	}

	entries := make([]string, 0, staleNoticeMaxIDs)
	for i, s := range report.Specs {
		if i == staleNoticeMaxIDs {
			break
		}
		entries = append(entries, fmt.Sprintf("%s (%s, %d d)", s.ID, s.Status, s.DaysSinceLastMove))
	}
	for n := len(entries); n > 0; n-- {
		line := prefix + ": " + strings.Join(entries[:n], ", ")
		if rest := report.Count - n; rest > 0 {
			line += fmt.Sprintf(" y %d más", rest)
		}
		line += tail
		if len(line) <= staleNoticeMaxBytes {
			return line
		}
	}
	return prefix + tail
}

// maybeEmitStaleSpecsNotice writes staleNoticeLine to w when the current
// project has at least one stale spec (SPEC-186 D2b). It opens the SDD
// service through initSDDService, the same composition point `mneme status`
// uses. Fail-open, like the pending-session notice (SPEC-108 D15): an
// error goes to errW and nothing to w, and the context block that follows
// prints regardless. Nothing stale (or the announcement disabled) writes
// nothing at all, not even an empty line.
func maybeEmitStaleSpecsNotice(ctx context.Context, w, errW io.Writer) {
	sddSvc, cleanup, err := initSDDService()
	if err != nil {
		fmt.Fprintf(errW, "[mneme] session-start hook: stale specs error: %v\n", err)
		return
	}
	defer cleanup()

	report, err := sddSvc.StaleSpecs(ctx, "")
	if err != nil {
		fmt.Fprintf(errW, "[mneme] session-start hook: stale specs error: %v\n", err)
		return
	}
	if report.Count == 0 {
		return
	}
	fmt.Fprintln(w, staleNoticeLine(report))
}
