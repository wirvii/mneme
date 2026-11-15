// Package risk holds mneme's closed list of six risk items and the reader of
// the one-line "Risk:" declaration a delivery closes with (SPEC-184).
//
// The list is the contract three consumers share: the trivial-lane auditor
// (internal/laneaudit) checks a diff against the path patterns of items 1, 2
// and 5; the delivery block of SPEC-182 validates its declared item number;
// and the role denials of SPEC-185 read the same patterns. Keeping the list
// in one package means the agent-facing text (mneme-flow/references/risk.md,
// pinned against this package by a drift test) and the code that enforces it
// cannot diverge.
//
// This package is a leaf of the standard library only, with no I/O: it
// imports neither os nor os/exec nor any other internal/* package, so that
// internal/enforcement — whose own leaf test admits only internal/shell —
// can import it without a cycle and without dragging file or process access
// into the decision layer. Glob matching with ** stays in
// internal/laneaudit (doublestar); this package only owns the patterns.
package risk
