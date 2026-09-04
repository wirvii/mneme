#!/bin/sh
# grill-me/validation/run.sh
#
# Deterministic structural check (no network, no LLM calls): confirms this
# SKILL.md still points the interview at backlog_refine, still forbids
# superpowers:brainstorming, and still carries the "one question at a time"
# discipline in its own words.
set -e

SKILL_FILE="SKILL.md"

if [ ! -f "$SKILL_FILE" ]; then
  echo "grill-me: $SKILL_FILE not found in $(pwd)" >&2
  exit 1
fi

if ! grep -q "backlog_refine" "$SKILL_FILE"; then
  echo "grill-me: missing reference to backlog_refine" >&2
  exit 1
fi

if ! grep -q "superpowers:brainstorming" "$SKILL_FILE"; then
  echo "grill-me: missing the prohibition of superpowers:brainstorming" >&2
  exit 1
fi

if ! grep -q "one question at a time" "$SKILL_FILE"; then
  echo "grill-me: missing the 'one question at a time' phrase" >&2
  exit 1
fi

echo "grill-me: validation passed"
exit 0
