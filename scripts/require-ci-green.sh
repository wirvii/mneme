#!/usr/bin/env bash
# require-ci-green.sh — SPEC-135: refuses to let a release tag through
# unless the "CI" workflow has actually run to completion and passed for
# the EXACT commit the tag points to.
#
# This exists because CI on main went red at v1.42.0 (SPEC-129) and stayed
# red through two more tagged releases (v1.42.0, v1.43.0) before anyone
# noticed — the release workflow never looked. See BL-218/SPEC-135.
#
# The one design rule this script exists to enforce: "red" and "not yet
# known" are two different outcomes and must never be reported the same
# way. A commit CI has not finished checking yet is not a failure — it is
# an absence of information, and treating an absence as a failure is
# exactly the bug family this repository has fixed three times already
# (a zero that cannot be told apart from a real failure). So this script
# actively WAITS (bounded) for CI to finish before it will call anything
# red.
#
# Usage: require-ci-green.sh <commit-sha>
# Requires: gh (GitHub CLI, preinstalled on GitHub-hosted runners),
#           GH_TOKEN in the environment, GITHUB_REPOSITORY set (both are
#           set automatically inside a GitHub Actions job).
set -euo pipefail

SHA="${1:?require-ci-green.sh: commit SHA required}"
REPO="${GITHUB_REPOSITORY:?require-ci-green.sh: GITHUB_REPOSITORY must be set}"
WORKFLOW_FILE="ci.yml"

# How long to actively wait for CI to finish before refusing to guess.
# CI on this repo (windows + ubuntu jobs) typically finishes in a few
# minutes; 20 minutes gives real headroom on a busy runner queue without
# leaving a release tag blocked indefinitely on a genuine stall.
MAX_WAIT_SECONDS=1200
POLL_INTERVAL_SECONDS=20

elapsed=0
while true; do
  # A transient API hiccup (rate limit, network blip) is an absence of
  # information too, not a red CI — fall through to the same "keep
  # waiting" path as "no run found yet" instead of hard-failing the gate
  # on a problem that has nothing to do with the commit being checked.
  runs_json=$(gh api "repos/${REPO}/actions/workflows/${WORKFLOW_FILE}/runs?head_sha=${SHA}&per_page=10" \
    --jq '.workflow_runs' 2>/dev/null || echo '[]')

  # Most recent run for this exact commit, if any.
  latest=$(echo "$runs_json" | jq -c 'sort_by(.run_started_at) | reverse | .[0] // empty' 2>/dev/null || echo '')

  if [ -n "$latest" ]; then
    status=$(echo "$latest" | jq -r '.status')
    conclusion=$(echo "$latest" | jq -r '.conclusion // ""')
    url=$(echo "$latest" | jq -r '.html_url')

    if [ "$status" = "completed" ]; then
      if [ "$conclusion" = "success" ]; then
        echo "CI verified green for ${SHA}: ${url}"
        exit 0
      fi
      echo "::error::CI is RED for commit ${SHA} (conclusion=${conclusion}). Release refused. Run: ${url}"
      exit 1
    fi
    # status is queued/in_progress/etc — known to exist, not finished yet.
    echo "CI run for ${SHA} is still ${status} (${url}); waiting (${elapsed}s/${MAX_WAIT_SECONDS}s)..."
  else
    echo "No CI run found yet for ${SHA}; waiting (${elapsed}s/${MAX_WAIT_SECONDS}s)..."
  fi

  if [ "$elapsed" -ge "$MAX_WAIT_SECONDS" ]; then
    echo "::error::CI status for commit ${SHA} is NOT YET KNOWN after waiting ${MAX_WAIT_SECONDS}s (no completed run). This is NOT a red CI — it means CI has not finished reporting on this commit. Release refused. Push the tag again once CI has completed."
    exit 1
  fi

  sleep "$POLL_INTERVAL_SECONDS"
  elapsed=$((elapsed + POLL_INTERVAL_SECONDS))
done
