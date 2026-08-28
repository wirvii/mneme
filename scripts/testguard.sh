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

# SPEC-091 §1 AC13: internal/profile's host-level store
# (Config.ProfilesDir(), ~/.mneme/profiles) is filesystem, not SQLite — a
# leak here would not show up in the two checks above. Every test that
# touches profile.Store/service.ProfileService injects its own t.TempDir()
# (or an isolated --data-dir), so this directory must never appear inside
# the sandboxed HOME either.
if [ -d "$TEST_HOME/.mneme/profiles" ]; then
  leaked+=("$TEST_HOME/.mneme/profiles")
fi

# SPEC-130 §2a AC25: the SDD git-native mechanism (internal/sddfile,
# internal/service/sdd_{export,state,enable}.go) writes under
# <repoRoot>/.mneme/sdd, and D38 requires repoRoot to ALWAYS be a
# caller-supplied parameter — never resolved from the process's own HOME
# or working directory. This check stays INSIDE the sandboxed test HOME
# on purpose, matching the posture the header above already declares for
# the whole script (it does not inspect the real repository's own
# .mneme/sdd, which this repo may legitimately carry once the owner opts
# in — that would be a false positive, the same reasoning that keeps this
# script off the real ~/.mneme and <repo>/.mneme/shared/): if a test ever
# resolved $TEST_HOME itself as an SDD repoRoot, this is where the
# resulting files would land, and their presence here is exactly the
# leak SPEC-085's own DB/profile-store checks above exist to catch for
# their own subsystems.
if [ -d "$TEST_HOME/.mneme/sdd" ]; then
  leaked+=("$TEST_HOME/.mneme/sdd")
fi

if [ "${#leaked[@]}" -gt 0 ]; then
  echo "testguard: found production-shaped DB file(s)/directory(ies) inside the sandboxed test HOME:" >&2
  for f in "${leaked[@]}"; do
    echo "  $f" >&2
  done
  echo "testguard: a test resolved a real config.Config path (DB or profile store) instead of db.OpenMemory()/t.TempDir()." >&2
  echo "testguard: see the \"Testing\" section of CLAUDE.md and docs/ARCHITECTURE.md." >&2
  exit 1
fi

echo "testguard: OK — no test wrote to the sandboxed HOME's production DB/profile-store paths."
