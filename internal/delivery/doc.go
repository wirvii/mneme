// Package delivery parses the delivery contract block that SPEC-182 (BL-287)
// requires inside the entrega section of a spec's tarea.md.
//
// The block is a small TOML document between two delimiter lines. It says how
// the implementer's turn ended (one of four states), which committed tree the
// checks ran on, one evidencia entry per verification command with its exit
// code and an output excerpt, every existing test that went red and what was
// done about it, and the declared risk. Everything outside the block stays free
// prose.
//
// mneme validates the shape, never the truth: Parse does not run any command,
// compare any output, or look at the repository. Whether the evidence is real
// is for the qa-tester (and SPEC-183's stop hooks) to check.
//
// The package is a leaf on purpose, like internal/specreview: it imports only
// the standard library and go-toml/v2, never internal/model or any other
// internal/* package, so the service and CLI layers and the hooks of SPEC-183
// can all depend on it without creating a cycle.
package delivery
