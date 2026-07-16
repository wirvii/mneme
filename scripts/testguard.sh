#!/usr/bin/env bash
# testguard.sh — SPEC-085 G2: defense-in-depth check that runs after `make
# test`/`make test-race`. It does NOT inspect the developer's real ~/.mneme
# or <repo>/.mneme/shared/ (a snapshot of either would false-positive: this
# repo dogfoods team-memory, and an agent saving memories while `make test`
# happens to be running writes there legitimately — see SPEC-085 design
# §D5 "Por qué el guard NO vigila los directorios reales").
#
# Instead it inspects ONLY the private, per-invocation HOME sandbox the
# Makefile points HOME/USERPROFILE at ($TEST_HOME). If a production-shaped
# database file (projects/<slug>.db or global.db) shows up there, some test
# resolved the real config.DefaultPath()-style DB location instead of using
# db.OpenMemory()/t.TempDir() — exactly the class of leak SPEC-085 exists to
# catch, one guard layer removed from the test assertions themselves.
set -euo pipefail

TEST_HOME="${TEST_HOME:?testguard.sh: TEST_HOME must be set (the Makefile exports it)}"

leaked=()

if [ -d "$TEST_HOME/.mneme/projects" ]; then
  while IFS= read -r -d '' f; do
    leaked+=("$f")
  done < <(find "$TEST_HOME/.mneme/projects" -maxdepth 1 -name '*.db' -print0 2>/dev/null)
fi

if [ -f "$TEST_HOME/.mneme/global.db" ]; then
  leaked+=("$TEST_HOME/.mneme/global.db")
fi

if [ "${#leaked[@]}" -gt 0 ]; then
  echo "testguard: found production-shaped DB file(s) inside the sandboxed test HOME:" >&2
  for f in "${leaked[@]}"; do
    echo "  $f" >&2
  done
  echo "testguard: a test resolved the real DB path instead of db.OpenMemory()/t.TempDir()." >&2
  echo "testguard: see the \"Testing\" section of CLAUDE.md and docs/ARCHITECTURE.md." >&2
  exit 1
fi

echo "testguard: OK — no test wrote to the sandboxed HOME's production DB paths."
