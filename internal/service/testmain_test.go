package service_test

import (
	"os"
	"testing"

	"github.com/wirvii/mneme/internal/testenv"
)

// TestMain isolates this package's test binary from the developer's real
// HOME/USERPROFILE for the entire run (SPEC-085 D5b) — the complement to the
// Makefile's HOME sandbox (G2), which only protects `make test`/`make
// test-race`. internal/service is the package where the SPEC-085 root cause
// lived (NewMemoryService's team-memory auto-detection), so it is the first
// and most important package in the D5b isolation set.
func TestMain(m *testing.M) {
	os.Exit(testenv.Isolate(m))
}
