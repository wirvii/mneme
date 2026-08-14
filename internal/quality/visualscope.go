// Package quality — this file implements the visual mechanism's PURE
// evaluation logic (SPEC-120 EPIC-calidad S6 D3/D5/D6/D8/D10): reconciling
// declared targets against a parsed VisualReport (ScopeTargets), judging
// which targets fail and why (EvaluateVisual), and filtering a changed-file
// list down to one declared under a directory (FilterUnderDir, the visual
// reference-drift primitive's own filter). Every function here is a pure
// function of already-collected facts — no I/O, no git, no filesystem —
// mirroring mutscope.go's own separation from the constitution parser.
package quality

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// MaxVisualTargetRows bounds how many `visual-target/<id>` rows a single
// certificate may emit (D10) — the visual mechanism's own version of
// MaxSurvivorRows (mutscope.go, S5): a storage cota on the REGISTRY, never a
// quality threshold a project tunes. Past this many failing targets the
// remedy is not reading fifty rows, it is fixing the interface — mneme
// stops emitting rows and names the real total in visual/render's summary
// instead.
const MaxVisualTargetRows = 50

// ScopeTargets reconciles declared (a project's `[visual].targets`, D3)
// against rep's own reported target ids: missing lists every declared id
// ABSENT from the report (the arnés verified LESS than it declared —
// visual/scope's own `fail`, G8a); extra lists every reported id NOT
// declared (the arnés verified something nobody wrote down — visual/scope's
// own `finding` target-drift, G8b). Both are returned in ascending,
// deterministic order (AC9/AC23) — never map iteration order. A nil rep is
// treated as an empty report (every declared id is missing, nothing is
// extra) rather than a special case the caller must guard against.
func ScopeTargets(declared []string, rep *VisualReport) (missing, extra []string) {
	reported := map[string]bool{}
	if rep != nil {
		reported = make(map[string]bool, len(rep.Targets))
		for _, t := range rep.Targets {
			reported[t.ID] = true
		}
	}

	declaredSet := make(map[string]bool, len(declared))
	for _, id := range declared {
		declaredSet[id] = true
		if !reported[id] {
			missing = append(missing, id)
		}
	}

	if rep != nil {
		for _, t := range rep.Targets {
			if !declaredSet[t.ID] {
				extra = append(extra, t.ID)
			}
		}
	}

	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}

// VisualThresholds is the pure evaluation config EvaluateVisual reads —
// deliberately NARROWER than the full `[visual]` constitution table
// (VisualConfig.Enabled/Format/Command/ReportPath/Timeout/Targets live in
// the constitution's own type, parsed in constitution.go, P5): the two
// fields here are the only ones a PURE function of an already-parsed report
// ever needs, and keeping them separate — the SAME separation
// MutationThresholds (mutscope.go, S5) already establishes from
// MutationConfig — is what lets this file stay a pure function with NO
// forward reference to the constitution parser that lands two steps later
// in the plan.
type VisualThresholds struct {
	// FailOnConsoleError mirrors [visual].fail_on_console_error (D5):
	// whether a target's console.error count above zero degrades the
	// verdict. An uncaught exception (PageErrors) fails regardless of this
	// flag — see EvaluateVisual's own G10a/G10b split.
	FailOnConsoleError bool

	// A11yFailImpacts mirrors [visual].a11y_fail_impacts (D6) — the closed
	// set of A11yImpact values that degrade the verdict. Empty is a
	// legitimate, explicit choice: accessibility is measured and recorded
	// but never blocks.
	A11yFailImpacts []A11yImpact
}

// VisualOutcome is EvaluateVisual's pure verdict over one VisualReport —
// everything the service layer's row-assembly (P8) needs to decide
// visual/render, visual/console, visual/a11y, and the per-target
// `visual-target/<id>` rows, without re-deriving any classification itself.
// Every id-list is returned in ascending order (D10/AC23) — never map
// iteration order — and mirrors what a target's OWN row must report:
// Breaches is a map of target id -> ordered list of every reason THAT
// target failed (D10's "una fila por objetivo, con TODOS sus
// incumplimientos"), never a single boolean per target.
type VisualOutcome struct {
	// RenderFailed lists every reported target whose rendered==false.
	RenderFailed []string

	// PageErrorFailed lists every reported target with at least one
	// uncaught exception or unhandled promise rejection (D5) — ALWAYS
	// populated regardless of FailOnConsoleError (G10a): this is a fact,
	// never an opinion.
	PageErrorFailed []string

	// ConsoleErrorFailed lists every reported target with console.error>0
	// WHEN FailOnConsoleError is true (G10b) — empty whenever the flag is
	// false, however many console.error entries a report declares.
	ConsoleErrorFailed []string

	// A11yFailed lists every reported target with at least one violation
	// whose impact is in A11yFailImpacts (G11a/G11b).
	A11yFailed []string

	// A11yNotReported lists every reported target with NO a11y block at
	// all, WHEN A11yFailImpacts is non-empty (G11c): "declared and not
	// measured" degrades the verdict exactly like a measured violation
	// would — a check that was never run must never look like one that
	// passed.
	A11yNotReported []string

	// Breaches maps a failing target's id to every reason it failed, in
	// evaluation order (render, then page errors, then console, then
	// accessibility) — the single source `visual-target/<id>`'s Detail is
	// built from (D10).
	Breaches map[string][]string
}

// BreachedTargetIDs returns outcome.Breaches' keys in ascending order
// (D10/AC23/G19) — the deterministic order MaxVisualTargetRows' truncation
// and two separate Verify runs over the SAME report both rely on: a Go map
// has no defined iteration order, so this is the ONE place that order is
// fixed, rather than leaving every caller to re-sort it (or forget to).
func BreachedTargetIDs(outcome VisualOutcome) []string {
	ids := make([]string, 0, len(outcome.Breaches))
	for id := range outcome.Breaches {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// EvaluateVisual is the pure classification D5/D6/D10 rest on: it never
// consults the constitution directly (only the narrower VisualThresholds),
// never touches a file, and produces the SAME VisualOutcome for the SAME
// (rep, cfg) pair every time — the property BreachedTargetIDs' ascending
// order depends on being meaningful at all.
func EvaluateVisual(rep *VisualReport, cfg VisualThresholds) VisualOutcome {
	outcome := VisualOutcome{Breaches: make(map[string][]string)}
	if rep == nil {
		return outcome
	}

	failImpacts := make(map[A11yImpact]bool, len(cfg.A11yFailImpacts))
	for _, imp := range cfg.A11yFailImpacts {
		failImpacts[imp] = true
	}

	for _, t := range rep.Targets {
		var reasons []string

		if !t.Rendered {
			outcome.RenderFailed = append(outcome.RenderFailed, t.ID)
			reasons = append(reasons, fmt.Sprintf("no renderiza: %s", t.Error))
		}

		// G10a: an uncaught exception / unhandled rejection ALWAYS fails —
		// a fact, never conditioned on fail_on_console_error.
		if len(t.PageErrors) > 0 {
			outcome.PageErrorFailed = append(outcome.PageErrorFailed, t.ID)
			reasons = append(reasons, fmt.Sprintf("excepcion no capturada: %s", strings.Join(t.PageErrors, "; ")))
		}

		// G10b: console.error only degrades the verdict when the project
		// DECLARED it should (an opinion, not a fact).
		if cfg.FailOnConsoleError && t.Console.Error > 0 {
			outcome.ConsoleErrorFailed = append(outcome.ConsoleErrorFailed, t.ID)
			reasons = append(reasons, fmt.Sprintf("console.error=%d con fail_on_console_error=true", t.Console.Error))
		}

		if len(failImpacts) > 0 {
			switch {
			case !t.A11y.Reported:
				// G11c: declared-and-not-measured is a fail, never a pass
				// by omission.
				outcome.A11yNotReported = append(outcome.A11yNotReported, t.ID)
				reasons = append(reasons, "accesibilidad declarada (a11y_fail_impacts no vacio) y no medida")
			default:
				var a11yReasons []string
				for _, v := range t.A11y.Violations {
					if failImpacts[v.Impact] {
						a11yReasons = append(a11yReasons, fmt.Sprintf("%s (%s)", v.Rule, v.Impact))
					}
				}
				if len(a11yReasons) > 0 {
					sort.Strings(a11yReasons)
					outcome.A11yFailed = append(outcome.A11yFailed, t.ID)
					reasons = append(reasons, fmt.Sprintf("accesibilidad: %s", strings.Join(a11yReasons, "; ")))
				}
			}
		}

		if len(reasons) > 0 {
			outcome.Breaches[t.ID] = reasons
		}
	}

	sort.Strings(outcome.RenderFailed)
	sort.Strings(outcome.PageErrorFailed)
	sort.Strings(outcome.ConsoleErrorFailed)
	sort.Strings(outcome.A11yFailed)
	sort.Strings(outcome.A11yNotReported)

	return outcome
}

// FilterUnderDir returns the subset of paths that fall UNDER dir — compared
// with a trailing separator (G5b), never as a bare string prefix: without
// it, ".mneme/visual/reference-old/a.png" would count as being "under"
// ".mneme/visual/reference", attributing a sibling directory's drift to the
// wrong one. Both dir and every path are normalized with filepath.ToSlash
// first (R-G): the report, git diff, and constitution all speak in
// forward-slash relative paths, and comparing them as OS-native paths on
// Windows would make nothing match. Returned in ascending order.
func FilterUnderDir(paths []string, dir string) []string {
	prefix := filepath.ToSlash(dir) + "/"
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if strings.HasPrefix(filepath.ToSlash(p), prefix) {
			out = append(out, filepath.ToSlash(p))
		}
	}
	sort.Strings(out)
	return out
}
