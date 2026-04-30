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

	"github.com/juanftp/mneme/internal/model"
)

// MatchResult holds the outcome of evaluating all active rules against a single
// tool invocation. Matched rules are sorted by severity descending so the most
// severe constraints appear first in any generated output.
type MatchResult struct {
	// Matched is the slice of rules that fired, sorted by severity desc.
	Matched []MatchedRule

	// MaxSev is the highest severity across all matched rules, or empty string
	// when no rules matched.
	MaxSev model.Severity
}

// MatchedRule pairs a rule with the specific applies_to entries that caused it
// to fire. Callers can use Entries to explain to the agent which patterns were
// responsible.
type MatchedRule struct {
	// Rule is the full memory representing the fired rule.
	Rule model.Memory

	// Entries is the subset of Rule.AppliesTo that matched the invocation.
	Entries []string
}

// Match evaluates all rules against the given tool invocation. It returns a
// MatchResult containing every rule whose applies_to patterns match, sorted by
// severity descending.
//
// Parameters:
//   - rules: the complete set of active rules to evaluate.
//   - toolName: the tool being invoked (e.g. "Edit", "Write", "MultiEdit").
//   - filePath: the absolute or relative path of the file being accessed. May be
//     empty for tools that don't target a specific file.
//   - cwd: the working directory used to relativise filePath. Must be an absolute
//     path.
func Match(rules []model.Memory, toolName, filePath, cwd string) MatchResult {
	pathRel, outOfTree := normalisePath(filePath, cwd)

	var matched []MatchedRule
	maxSev := model.Severity("")

	for _, rule := range rules {
		ok, entries := matchRule(rule.AppliesTo, toolName, pathRel, outOfTree)
		if !ok {
			continue
		}
		matched = append(matched, MatchedRule{Rule: rule, Entries: entries})
		if severityOrder(rule.Severity) > severityOrder(maxSev) {
			maxSev = rule.Severity
		}
	}

	// Stable sort by severity descending so block rules always appear first.
	sort.SliceStable(matched, func(i, j int) bool {
		return severityOrder(matched[i].Rule.Severity) > severityOrder(matched[j].Rule.Severity)
	})

	return MatchResult{Matched: matched, MaxSev: maxSev}
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
func matchRule(appliesTo []string, toolName, pathRel string, outOfTree bool) (bool, []string) {
	positives, negatives := splitEntries(appliesTo)

	// Negatives veto first: a single matching negative suppresses the rule.
	for _, neg := range negatives {
		pattern := strings.TrimPrefix(neg, "!")
		if entryMatch(pattern, toolName, pathRel, outOfTree) {
			return false, nil
		}
	}

	// At least one positive must match.
	var matched []string
	for _, pos := range positives {
		if entryMatch(pos, toolName, pathRel, outOfTree) {
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
//   - "tool:Name" — matches when toolName == Name (case-sensitive).
//   - "path/glob/**" — matches when pathRel matches the doublestar glob.
//   - "tool:Name+path/glob/**" — AND: both parts must match.
//
// The "!" prefix for negation is NOT handled here; callers must strip it before
// calling entryMatch (see matchRule).
func entryMatch(entry, toolName, pathRel string, outOfTree bool) bool {
	if entry == "**" {
		return true
	}

	if strings.Contains(entry, "+") {
		// Combined selector: split into exactly two parts and require both.
		parts := strings.SplitN(entry, "+", 2)
		return entryMatch(parts[0], toolName, pathRel, outOfTree) &&
			entryMatch(parts[1], toolName, pathRel, outOfTree)
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
