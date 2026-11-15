// Package laneaudit implements the trivial-lane auditor's engine (`lane
// audit`) — the deterministic post-implementation check of what a
// trivial-lane spec's diff shows: every changed file inside the declared
// scope, no path of risk items 1, 2 or 5, no exported symbol added or
// removed (the visible part of item 3), and a "Risk:" declaration that
// says none and agrees with all of that (SPEC-184). File and line counts
// never decide: size is not what makes a change risky. No certificate of
// any kind is involved (SPEC-158 D2).
//
// This package is a leaf: it imports only the standard library,
// github.com/bmatcuk/doublestar/v4 for glob matching, and its sibling
// leaves internal/gitrepo (the git primitives the symbol collector needs)
// and internal/risk (the closed list of six risk items and its path
// table). A leaf importing a sibling leaf has precedent in this repository
// — internal/enforcement imports internal/shell. internal/laneaudit itself
// never imports internal/model or any other internal/* package.
package laneaudit
