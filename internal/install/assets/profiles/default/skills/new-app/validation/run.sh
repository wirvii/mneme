#!/bin/sh
# new-app/validation/run.sh
#
# Deterministic structural check (no network, no LLM calls): confirms this
# SKILL.md still defers the deterministic copy + wiring to the app_add command
# (never a hand-rolled copy/edit flow), reads the originating scaffold from the
# monorepo's pin, and never instructs an unpinned @latest bootstrap (the
# determinism invariant, SPEC-099 §7b). This couples the prose to the mechanism,
# the same posture new-project/mneme-init validation scripts already established.
set -e

SKILL_FILE="SKILL.md"

if [ ! -f "$SKILL_FILE" ]; then
  echo "new-app: $SKILL_FILE not found in $(pwd)" >&2
  exit 1
fi

if ! grep -q 'app_add' "$SKILL_FILE"; then
  echo "new-app: missing reference to the deterministic command app_add" >&2
  exit 1
fi

# The skill must read the originating scaffold from the monorepo's pin.
if ! grep -qi 'pin' "$SKILL_FILE"; then
  echo "new-app: missing reference to the pin (app_add reads scaffold=<name> from .mneme-profile)" >&2
  exit 1
fi

if ! grep -qi 'blueprint' "$SKILL_FILE"; then
  echo "new-app: missing reference to blueprints (the archetype's composable apps)" >&2
  exit 1
fi

# The determinism invariant: the skill must never instruct an unpinned bootstrap.
if grep -q '@latest' "$SKILL_FILE"; then
  echo "new-app: found @latest — scaffolds must be version-pinned (never @latest)" >&2
  exit 1
fi

echo "new-app: validation passed"
exit 0
