package laneaudit_test

import (
	"os"
	"testing"

	"github.com/wirvii/mneme/internal/testenv"
)

// TestMain isolates this package's test binary from the developer's real
// HOME/USERPROFILE for the entire run (SPEC-085 D5b) — CollectSymbols'
// tests build real git repos under t.TempDir().
//
// This file deliberately lives in the external laneaudit_test package, not
// laneaudit: leaf_test.go's import guard scans pkg.Imports + pkg.TestImports
// (in-package test files) but NOT pkg.XTestImports (external _test package
// files).
func TestMain(m *testing.M) {
	os.Exit(testenv.Isolate(m))
}
