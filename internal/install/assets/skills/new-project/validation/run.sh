#!/bin/sh
# new-project/validation/run.sh
#
# Deterministic structural check (no network, no LLM calls): confirms this
# SKILL.md still defers the deterministic assembly to the project_new command
# (never a hand-rolled copy/generator flow), never instructs an unpinned
# @latest bootstrap (the determinism invariant, SPEC-098 §7a), and chains
# mneme-init over the fresh repo (docs/profiles-design.md §15.7). This couples
# the prose to the mechanism, the same posture mneme-init/mneme-profile-author
# validation scripts already established.
set -e

SKILL_FILE="SKILL.md"

if [ ! -f "$SKILL_FILE" ]; then
  echo "new-project: $SKILL_FILE not found in $(pwd)" >&2
  exit 1
fi

if ! grep -q 'project_new' "$SKILL_FILE"; then
  echo "new-project: missing reference to the deterministic command project_new" >&2
  exit 1
fi

if ! grep -q 'mneme-init' "$SKILL_FILE"; then
  echo "new-project: missing reference to mneme-init (must chain it over the fresh repo)" >&2
  exit 1
fi

# The determinism invariant: the skill must never instruct an unpinned bootstrap.
if grep -q '@latest' "$SKILL_FILE"; then
  echo "new-project: found @latest — scaffolds must be version-pinned (never @latest)" >&2
  exit 1
fi

if ! grep -qi 'pin' "$SKILL_FILE"; then
  echo "new-project: missing reference to the pin (project_new writes scaffold=<name> into .mneme-profile)" >&2
  exit 1
fi

echo "new-project: validation passed"
exit 0
