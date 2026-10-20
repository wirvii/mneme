#!/bin/sh
# mneme-flow/validation/run.sh
#
# Deterministic structural check (no network, no LLM): SKILL.md stays within
# its size budget, keeps its anchors, and does not carry the numeric lane
# thresholds (those live only in references/depth.md, SPEC-169 D5).
set -e

SKILL_FILE="SKILL.md"
MAX_BYTES=4096

[ -f "$SKILL_FILE" ] || { echo "mneme-flow: $SKILL_FILE not found in $(pwd)" >&2; exit 1; }

size=$(wc -c < "$SKILL_FILE" | tr -d ' ')
if [ "$size" -gt "$MAX_BYTES" ]; then
  echo "mneme-flow: $SKILL_FILE is $size bytes, over the $MAX_BYTES limit" >&2
  exit 1
fi

for anchor in spec_advance criteria.toml review_range references/depth.md "this skill never relaxes it"; do
  if ! grep -q -- "$anchor" "$SKILL_FILE"; then
    echo "mneme-flow: missing anchor: $anchor" >&2
    exit 1
  fi
done

if grep -Eq '3 fi''les|20 li''nes' "$SKILL_FILE"; then
  echo "mneme-flow: SKILL.md states lane thresholds; they belong in references/depth.md" >&2
  exit 1
fi

for ref in references/depth.md references/lifecycle.md references/criteria.md; do
  [ -f "$ref" ] || { echo "mneme-flow: missing $ref" >&2; exit 1; }
done

echo "mneme-flow: validation passed"
exit 0
