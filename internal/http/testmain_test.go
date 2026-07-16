package http_test

import (
	"os"
	"testing"

	"github.com/wirvii/mneme/internal/testenv"
)

// TestMain isolates this package's test binary from the developer's real
// HOME/USERPROFILE for the entire run (SPEC-085 D5b) — the complement to the
// Makefile's HOME sandbox (G2), which only protects `make test`/`make
// test-race`.
func TestMain(m *testing.M) {
	os.Exit(testenv.Isolate(m))
}
