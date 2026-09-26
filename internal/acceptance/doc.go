// Package acceptance is the surviving READER half of SPEC-156's
// criteria.toml contract, after SPEC-158 retired the executable-criteria
// evaluator entirely (EPIC-calidad S3, internal/quality's assert-mode
// verbs). A spec's criteria.toml is still obligatory before the human
// approval gate (speccing -> specced) — the qa-tester now verifies each
// criterion by hand, running whatever the criterion's own text names
// (SPEC-156/157) — but mneme itself no longer evaluates any of it, so this
// package only parses and structurally validates the document: id + text,
// nothing more.
//
// Every legacy shape ParseCriteria (the old, retired internal/quality
// parser) used to accept still parses here: the three modes (assert,
// command, manual) and their mode-specific keys (mode, command, timeout,
// evidence_required, and the [[criterion.assert]] table with any content)
// are accepted and silently discarded — never validated, never evaluated.
// Only id and text are read. This package is a leaf: it imports only the
// standard library plus github.com/pelletier/go-toml/v2.
package acceptance
