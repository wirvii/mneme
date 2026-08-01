// Package e2e contains a single black-box end-to-end test that compiles the
// real mneme binary and drives it over stdio the way a real MCP client
// would (SPEC-104 DD11): it is the only test that exercises the actual
// process boundary and the real 10 MiB per-message limit, which no unit
// test inside internal/mcp can observe.
//
// This lives in its own package rather than inside internal/mcp on
// purpose. internal/mcp's TestMain sandboxes HOME/USERPROFILE for the whole
// test BINARY via internal/testenv.Isolate (SPEC-085). A `go build`
// subprocess launched from inside that binary would inherit the
// already-sandboxed HOME and resolve GOCACHE/GOMODCACHE inside a temporary
// directory — forcing a full rebuild (including modernc.org/sqlite) and a
// full module re-download on every test run. That is exactly the trap the
// project's own Makefile already works around for `make test` itself (see
// its REAL_GOCACHE/REAL_GOMODCACHE comment) — this package must not
// reintroduce it one level down.
//
// Accordingly, this package declares NO TestMain and isolates nothing about
// its own process: it resolves no environment-derived path itself, so there
// is nothing here for internal/testenv.TestAllIsolatedPackagesDeclareTestMain
// to require. All isolation happens in the SUBPROCESS it launches, whose
// environment the test fixes explicitly — a temp HOME/USERPROFILE, a temp
// --data-dir, a dedicated --project, and a non-git working directory — see
// stdio_test.go.
package e2e
