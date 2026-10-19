package gitrepo_test

import (
	"os"
	"testing"

	"github.com/wirvii/mneme/internal/testenv"
)

// TestMain isolates this package's test binary from the developer's real
// HOME/USERPROFILE for the entire run (SPEC-085 D5b) — internal/gitrepo's
// own tests build real git repos under t.TempDir().
//
// This file deliberately lives in the external gitrepo_test package, not
// gitrepo: leaf_test.go's import guard scans pkg.Imports + pkg.TestImports
// (in-package test files) but NOT pkg.XTestImports (external _test package
// files). Importing internal/testenv from an in-package test file would
// trip the leaf guard the moment it runs; from gitrepo_test it never enters
// that scan at all.
func TestMain(m *testing.M) {
	os.Exit(testenv.Isolate(m))
}
