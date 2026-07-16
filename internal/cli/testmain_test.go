package cli

import (
	"os"
	"testing"

	"github.com/wirvii/mneme/internal/testenv"
)

// TestMain isolates this package's test binary from the developer's real
// HOME/USERPROFILE for the entire run (SPEC-085 D5b) — the complement to the
// Makefile's HOME sandbox (G2), which only protects `make test`/`make
// test-race`. internal/cli hosts initService, the sole production
// constructor that opts into team-memory, and several tests drive it (or the
// real cobra commands) directly.
func TestMain(m *testing.M) {
	os.Exit(testenv.Isolate(m))
}
