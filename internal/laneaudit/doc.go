// Package laneaudit implements the trivial-lane auditor's engine (`lane
// audit`) — the deterministic post-implementation check that a trivial-lane
// spec's diff stays within its declared file/line/scope/forbidden-path
// budget, with no certificate of any kind involved (SPEC-158 D2: after the
// old EPIC-calidad certification mechanism was retired, `lane audit` runs
// this engine directly, always).
//
// This package is a leaf: it imports only the standard library,
// github.com/bmatcuk/doublestar/v4 for glob matching, and its sibling leaf
// internal/gitrepo for the git primitives the symbol collector needs (a
// leaf importing a sibling leaf has precedent in this repository —
// internal/enforcement imports internal/shell). internal/laneaudit itself
// never imports internal/model or any other internal/* package.
package laneaudit
