// Package specreview parses and counts the fresh-review report that SPEC-181
// requires before a standard spec crosses the human approval gate and before
// it is implemented.
//
// The report is a TOML document (revision-spec.toml for the spec moment,
// revision-plan.toml for the plan moment) that records which reviewers were
// asked, whether each one ran, and every finding with its severity and the
// way the architect resolved it. mneme validates the record; it cannot prove
// that the review happened.
//
// The package is a leaf on purpose, like internal/acceptance: it imports only
// the standard library and go-toml/v2, never internal/model or any other
// internal/* package, so the model, service and CLI layers can all depend on
// it without creating a cycle.
package specreview
