#!/bin/sh
# hunt-bug/validation/run.sh
#
# Deterministic structural check: confirms this SKILL.md still points at
# backlog_add/backlog_refine, still mentions lane, and never names the
# deprecated .claude/bugs path (SPEC-141 D4 — naming it, even to forbid it,
# would teach the very path this rewrite abandons).
set -e

SKILL_FILE="SKILL.md"

if [ ! -f "$SKILL_FILE" ]; then
  echo "hunt-bug: $SKILL_FILE not found in $(pwd)" >&2
  exit 1
fi

for tool in backlog_add backlog_refine; do
  if ! grep -q "$tool" "$SKILL_FILE"; then
    echo "hunt-bug: missing reference to required tool: $tool" >&2
    exit 1
  fi
done

if ! grep -q "lane" "$SKILL_FILE"; then
  echo "hunt-bug: missing reference to lane" >&2
  exit 1
fi

if grep -q '\.claude/bugs' "$SKILL_FILE"; then
  echo "hunt-bug: must not name the deprecated .claude/bugs path" >&2
  exit 1
fi

echo "hunt-bug: validation passed"
exit 0
