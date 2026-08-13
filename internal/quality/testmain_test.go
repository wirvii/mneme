package quality_test

import (
	"os"
	"testing"

	"github.com/wirvii/mneme/internal/testenv"
)

// TestMain isolates this package's test binary from the developer's real
// HOME/USERPROFILE for the entire run (SPEC-085 D5b) — the complement to the
// Makefile's HOME sandbox (G2), which only protects `make test`/`make
// test-race`. internal/quality's own tests build real git repos under
// t.TempDir() and, in runner_test.go, re-exec this test binary — isolating
// HOME keeps any of that from ever touching a real developer environment.
//
// This file deliberately lives in the external quality_test package, not
// quality: leaf_test.go's import guard scans pkg.Imports + pkg.TestImports
// (in-package test files) but NOT pkg.XTestImports (external _test package
// files) — the same reason internal/profile's own TestMain-less leaf never
// had to reconcile this. Importing internal/testenv from an in-package test
// file would trip the leaf guard the moment it runs; from quality_test it
// never enters that scan at all.
func TestMain(m *testing.M) {
	os.Exit(testenv.Isolate(m))
}
