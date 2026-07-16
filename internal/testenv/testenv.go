// Package testenv provides shared test-process isolation for every mneme
// package whose tests touch environment-derived paths (HOME, git config,
// ~/.mneme, the git-native team-memory vault). It is the SPEC-085 D5b
// complement to the Makefile's HOME sandbox (G2, `make test`/`make
// test-race`): that sandbox only protects the canonical entrypoint, so a
// bare `go test ./...` — exactly how an agent normally runs the suite —
// still ran with the developer's real HOME. Isolate closes that gap at the
// test-binary level, independent of how the binary was invoked.
//
// internal/testenv is a leaf package: it imports only the standard library.
package testenv

import (
	"fmt"
	"os"
	"testing"
)

// Isolate redirects HOME and USERPROFILE to a private temporary directory
// for the lifetime of the test binary, then runs m and returns its exit
// code. Call it from TestMain in every package that resolves an
// environment-derived path (directly, or transitively through a
// production constructor such as internal/cli.initService):
//
//	func TestMain(m *testing.M) { os.Exit(testenv.Isolate(m)) }
//
// The sandbox directory is created fresh for this process and removed when
// Isolate returns — it never touches the developer's real ~/.mneme or the
// real git-native team-memory vault at <repo>/.mneme/shared/. Individual
// tests remain free to further override HOME (e.g. via t.Setenv) for a
// narrower scope; Isolate only establishes the outer, process-wide default.
//
// Isolate does not need to preserve GOCACHE/GOMODCACHE the way the
// Makefile's G2 sandbox does (SPEC-085 R2): by the time TestMain runs, the
// test binary has already been compiled and every module already resolved
// — `go test` performed both using the ambient (real) environment before
// ever invoking this function. Overriding HOME here has no effect on either
// cache.
func Isolate(m *testing.M) int {
	home, err := os.MkdirTemp("", "mneme-testenv-home-*")
	if err != nil {
		// TestMain has no *testing.T to report through; a sandbox that can't
		// be created means every test in this binary would silently run
		// against the real environment instead, which is worse than failing
		// loudly here.
		fmt.Fprintf(os.Stderr, "testenv: create sandbox HOME: %v\n", err)
		return 1
	}
	defer os.RemoveAll(home)

	if err := os.Setenv("HOME", home); err != nil {
		fmt.Fprintf(os.Stderr, "testenv: set HOME: %v\n", err)
		return 1
	}
	if err := os.Setenv("USERPROFILE", home); err != nil {
		fmt.Fprintf(os.Stderr, "testenv: set USERPROFILE: %v\n", err)
		return 1
	}

	return m.Run()
}
