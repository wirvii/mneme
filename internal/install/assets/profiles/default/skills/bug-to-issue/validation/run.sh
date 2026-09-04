#!/bin/sh
# bug-to-issue/validation/run.sh
#
# Deterministic structural check: confirms this SKILL.md still points at
# backlog_promote/spec_quick and the human approval gate, and never names
# the deprecated .claude/templates path (SPEC-141 D4).
set -e

SKILL_FILE="SKILL.md"

if [ ! -f "$SKILL_FILE" ]; then
  echo "bug-to-issue: $SKILL_FILE not found in $(pwd)" >&2
  exit 1
fi

for token in backlog_promote spec_quick; do
  if ! grep -q "$token" "$SKILL_FILE"; then
    echo "bug-to-issue: missing reference to required token: $token" >&2
    exit 1
  fi
done

if ! grep -qi "approval" "$SKILL_FILE"; then
  echo "bug-to-issue: missing reference to the human approval gate" >&2
  exit 1
fi

if grep -q '\.claude/templates' "$SKILL_FILE"; then
  echo "bug-to-issue: must not name the deprecated .claude/templates path" >&2
  exit 1
fi

echo "bug-to-issue: validation passed"
exit 0
