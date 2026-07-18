package service

import "github.com/wirvii/mneme/internal/model"

// refinementAdvisoryText is the single source of truth for the refinement
// nudge attached to backlog_add (SPEC-103 D6). It exists as a constant
// (rather than being inlined at each call site) so both frontends — MCP and
// CLI — quote the exact same prose: duplicating it as a literal in
// internal/mcp and internal/cli would let the two drift apart silently.
//
// The doctrine it encodes (SPEC-103 D1-D5): refine a standard-lane backlog
// item with `grill-me` — a one-question-at-a-time interrogation that
// recommends an answer at every branch — never with
// `superpowers:brainstorming`. brainstorming clashes with the mneme SDD
// flow: its own pipeline writes a design doc under docs/superpowers/specs/
// and invokes writing-plans, stepping on the @architect's job and saving the
// spec to the wrong path; it also doesn't ship with mneme, so not every
// teammate has it installed.
const refinementAdvisoryText = "Refine this item with `grill-me` (one question at a time, " +
	"recommending an answer at each step) before calling `backlog_refine` — do NOT use " +
	"`superpowers:brainstorming` to refine it: brainstorming writes its own design doc under " +
	"docs/superpowers/specs/ and invokes writing-plans, which steps on the @architect's job and " +
	"saves the spec to the wrong path; it also doesn't ship with mneme. Once the grill is done, " +
	"pour it into `backlog_refine`, then `backlog_promote`."

// RefinementAdvisory returns the refinement-doctrine nudge for a backlog
// item's lane, or "" when no nudge applies (SPEC-103 D4).
//
// It mirrors ResolveStageExecutor (SPEC-068): a pure, I/O-free function whose
// output is advisory only — it never mutates SDD state and never gates
// anything by itself. Its only effect is being attached to the backlog_add
// response (see internal/mcp handleBacklogAdd and internal/cli
// newBacklogAddCmd) so the caller is reminded of the correct refinement tool
// at the moment the item is created.
//
// The nudge is gated to model.LaneStandard: the grill is mandatory there
// (SPEC-103 D2). model.LaneTrivial and any other/empty value return "" — a
// trivial item's grill is optional (SPEC-103 D2: grill it or reclassify to
// standard only if it turns out to be ambiguous), so nudging on every
// trivial add would be noise rather than signal.
func RefinementAdvisory(lane model.Lane) string {
	if lane == model.LaneStandard {
		return refinementAdvisoryText
	}
	return ""
}
