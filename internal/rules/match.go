// Package rules provides the matching engine used by the pre-tool-use hook to
// evaluate which active rules apply to a given tool invocation. The package has
// zero I/O dependencies — it operates purely on in-memory data — so it can be
// tested and used without a database or service layer.
//
// Import direction: cli/ -> rules/ -> model/ (leaf).
package rules

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/wirvii/mneme/internal/model"
)

// Caller identifies the role of the hook invocator. orchestrator is the
// principal session (payload with no agent_id); subagent is any Task/Agent
// spawned by the orchestrator (agent_id present and non-empty).
type Caller string

const (
	// CallerOrchestrator represents the principal Claude Code session.
	// The hook payload has no agent_id (or all agent_id fields are empty/null).
	CallerOrchestrator Caller = "orchestrator"

	// CallerSubagent represents a sub-session launched via Task or Agent tool.
	// At least one of the known agent_id payload fields is present and non-empty.
	CallerSubagent Caller = "subagent"
)

// Invocation groups the context of a single tool call evaluated by the hook.
// Introducing this struct keeps the Match signature stable as new context fields
// (e.g. AgentType for agent:<name> selectors) are added later without breaking
// callers.
type Invocation struct {
	// Tool is the tool being invoked (e.g. "Edit", "Write", "MultiEdit", "NotebookEdit").
	Tool string

	// FilePath is the absolute or relative path of the file being accessed.
	// May be empty for tools that do not target a specific file.
	FilePath string

	// CWD is the working directory used to relativise FilePath. Must be absolute.
	CWD string

	// Caller identifies the invoking role (orchestrator or subagent).
	Caller Caller
}

// MatchResult holds the outcome of evaluating all active rules against a single
// tool invocation. Matched rules are sorted by effective severity descending so
// the most severe constraints appear first in any generated output.
type MatchResult struct {
	// Matched is the slice of rules that fired, sorted by effective severity desc.
	Matched []MatchedRule

	// MaxSev is the highest effective severity across all matched rules, or empty
	// string when no rules matched. Block rules degraded for subagents contribute
	// warn, not block, to MaxSev.
	MaxSev model.Severity
}

// MatchedRule pairs a rule with the specific applies_to entries that caused it
// to fire. Callers can use Entries to explain to the agent which patterns were
// responsible, and Effective/Degraded to render the role-adjusted severity.
type MatchedRule struct {
	// Rule is the full memory representing the fired rule.
	Rule model.Memory

	// Entries is the subset of Rule.AppliesTo that matched the invocation.
	Entries []string

	// Effective is the severity applied after considering the invoking role.
	// For subagents invoking a block rule that has no agent: selector, Effective
	// is downgraded to warn (see Degraded). In all other cases Effective equals
	// Rule.Severity.
	Effective model.Severity

	// Degraded is true when Effective is lower than Rule.Severity because the
	// invoking role is subagent and the rule has no agent: selector. This flag
	// lets callers annotate the output so subagents know the rule is block for
	// the orchestrator even though it only shows as a warning for them.
	Degraded bool
}

// Match evaluates all rules against the given tool invocation. It returns a
// MatchResult containing every rule whose applies_to patterns match, sorted by
// effective severity descending.
//
// Degradation rule (D-Q1): a rule with severity=block and no agent: selector
// is degraded to warn when the caller is a subagent. The rule still appears in
// Matched so the subagent sees it as context, but MaxSev is calculated on the
// effective (degraded) severity, and the hook will not exit 2 for subagents.
func Match(rules []model.Memory, inv Invocation) MatchResult {
	pathRel, outOfTree := normalisePath(inv.FilePath, inv.CWD)

	var matched []MatchedRule
	maxSev := model.Severity("")

	for _, rule := range rules {
		ok, entries := matchRule(rule.AppliesTo, inv.Tool, pathRel, outOfTree, inv.Caller)
		if !ok {
			continue
		}

		effective, degraded := computeEffective(rule, inv.Caller)
		mr := MatchedRule{
			Rule:      rule,
			Entries:   entries,
			Effective: effective,
			Degraded:  degraded,
		}
		matched = append(matched, mr)

		if severityOrder(effective) > severityOrder(maxSev) {
			maxSev = effective
		}
	}

	// Stable sort by effective severity descending so block rules always appear first.
	sort.SliceStable(matched, func(i, j int) bool {
		return severityOrder(matched[i].Effective) > severityOrder(matched[j].Effective)
	})

	return MatchResult{Matched: matched, MaxSev: maxSev}
}

// computeEffective calculates the effective severity for a matched rule given the
// invoking caller, implementing the degradation logic from D-Q1 / D3:
//
//  1. Rule has an agent: selector → Effective = declared severity, Degraded = false.
//  2. Rule has no agent: selector AND severity == block AND caller == subagent →
//     Effective = warn, Degraded = true.
//  3. All other cases → Effective = declared severity, Degraded = false.
func computeEffective(rule model.Memory, caller Caller) (effective model.Severity, degraded bool) {
	if ruleHasAgentSelector(rule.AppliesTo) {
		return rule.Severity, false
	}
	if rule.Severity == model.SeverityBlock && caller == CallerSubagent {
		return model.SeverityWarn, true
	}
	return rule.Severity, false
}

// ruleHasAgentSelector reports whether any entry in appliesTo contains an
// agent: selector, either as a standalone entry or as a part of a combined (+)
// entry, in positive or negative (! prefix stripped) form.
//
// This is used to decide whether D-Q1 degradation applies: rules that already
// express explicit agent targeting opt out of the automatic degradation.
func ruleHasAgentSelector(appliesTo []string) bool {
	for _, e := range appliesTo {
		// Strip negation prefix.
		s := strings.TrimPrefix(e, "!")
		// Check each + part.
		for _, part := range strings.Split(s, "+") {
			if strings.HasPrefix(part, "agent:") {
				return true
			}
		}
	}
	return false
}

// normalisePath converts filePath to a CWD-relative forward-slash path.
// It returns the relative path and whether the path is outside the CWD tree.
// An empty filePath produces ("", false) — no path glob will match an empty
// string, so rules with path patterns simply won't fire.
func normalisePath(filePath, cwd string) (rel string, outOfTree bool) {
	if filePath == "" {
		return "", false
	}

	// If filePath is not absolute, make it absolute relative to cwd so that
	// filepath.Rel behaves consistently.
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(cwd, filePath)
	}

	r, err := filepath.Rel(cwd, filePath)
	if err != nil || strings.HasPrefix(r, "..") {
		return "", true
	}
	return filepath.ToSlash(r), false
}

// matchRule decides whether a single rule fires for the given invocation.
// It returns true and the list of positive entries that matched when the rule
// applies, or false and nil when it does not.
//
// Algorithm:
//  1. Separate applies_to into positive and negative entries.
//  2. If any negative entry matches, the rule is excluded (veto).
//  3. If at least one positive entry matches, the rule fires.
func matchRule(appliesTo []string, toolName, pathRel string, outOfTree bool, caller Caller) (bool, []string) {
	positives, negatives := splitEntries(appliesTo)

	// Negatives veto first: a single matching negative suppresses the rule.
	for _, neg := range negatives {
		pattern := strings.TrimPrefix(neg, "!")
		if entryMatch(pattern, toolName, pathRel, outOfTree, caller) {
			return false, nil
		}
	}

	// At least one positive must match.
	var matched []string
	for _, pos := range positives {
		if entryMatch(pos, toolName, pathRel, outOfTree, caller) {
			matched = append(matched, pos)
		}
	}

	return len(matched) > 0, matched
}

// splitEntries partitions applies_to into positive entries (no leading "!") and
// negative entries (leading "!"). The "!" prefix is preserved in the returned
// negative slice so callers can strip it when needed.
func splitEntries(entries []string) (positives, negatives []string) {
	for _, e := range entries {
		if strings.HasPrefix(e, "!") {
			negatives = append(negatives, e)
		} else {
			positives = append(positives, e)
		}
	}
	return
}

// entryMatch checks whether a single positive applies_to entry matches the
// invocation. Supported entry formats:
//
//   - "**" — matches everything (any tool, any path).
//   - "agent:Name" — matches when the caller role matches the selector (see matchAgentSelector).
//   - "tool:Name" — matches when toolName == Name (case-sensitive).
//   - "path/glob/**" — matches when pathRel matches the doublestar glob.
//   - "part1+part2+..." — AND of all parts (N parts, not limited to 2).
//
// The "!" prefix for negation is NOT handled here; callers must strip it before
// calling entryMatch (see matchRule).
func entryMatch(entry, toolName, pathRel string, outOfTree bool, caller Caller) bool {
	if entry == "**" {
		return true
	}

	// Combined selector: split into N parts and require all of them to match.
	// This generalises the previous SplitN(2) to support three-part entries like
	// "agent:orchestrator+tool:Edit+internal/**".
	if strings.Contains(entry, "+") {
		for _, part := range strings.Split(entry, "+") {
			if !entryMatch(part, toolName, pathRel, outOfTree, caller) {
				return false
			}
		}
		return true
	}

	if strings.HasPrefix(entry, "agent:") {
		return matchAgentSelector(strings.TrimPrefix(entry, "agent:"), caller)
	}

	if strings.HasPrefix(entry, "tool:") {
		return toolName == strings.TrimPrefix(entry, "tool:")
	}

	// Path glob — cannot match if the path is out of the project tree or empty.
	if outOfTree || pathRel == "" {
		return false
	}

	matched, _ := doublestar.Match(entry, pathRel)
	return matched
}

// matchAgentSelector reports whether the selector name matches the given caller.
//
// Selector table (from D2):
//
//	agent:orchestrator → matches CallerOrchestrator only
//	agent:subagent     → matches CallerSubagent only
//	agent:*            → matches both
//	agent:<other>      → no match (DEFERRED — reserved for future agent_type targeting)
func matchAgentSelector(name string, caller Caller) bool {
	switch name {
	case "orchestrator":
		return caller == CallerOrchestrator
	case "subagent":
		return caller == CallerSubagent
	case "*":
		return true
	default:
		// agent:<agent_type> is deferred (e.g. agent:backend, agent:frontend).
		// The format is reserved for extensibility; for now it never matches so
		// that adding type-specific rules in future versions does not silently
		// grant access to unrecognised types.
		return false
	}
}

// severityOrder returns a numeric rank for a severity so they can be compared.
// block(3) > warn(2) > info(1) > ""(0). This replicates the ordering used by
// internal/service/context.go without creating a cross-package dependency.
func severityOrder(s model.Severity) int {
	switch s {
	case model.SeverityBlock:
		return 3
	case model.SeverityWarn:
		return 2
	case model.SeverityInfo:
		return 1
	default:
		return 0
	}
}
