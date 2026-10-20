#!/bin/sh
# feature/validation/run.sh
#
# Deterministic structural check (no network, no LLM): size budget and anchors.
set -e

SKILL_FILE="SKILL.md"
MAX_BYTES=4096

[ -f "$SKILL_FILE" ] || { echo "feature: $SKILL_FILE not found in $(pwd)" >&2; exit 1; }

size=$(wc -c < "$SKILL_FILE" | tr -d ' ')
if [ "$size" -gt "$MAX_BYTES" ]; then
  echo "feature: $SKILL_FILE is $size bytes, over the $MAX_BYTES limit" >&2
  exit 1
fi

for anchor in grill-me present-spec review-brief references/depth.md "explicit yes"; do
  if ! grep -q -- "$anchor" "$SKILL_FILE"; then
    echo "feature: missing anchor: $anchor" >&2
    exit 1
  fi
done

if grep -Eq '3 fi''les|20 li''nes' "$SKILL_FILE"; then
  echo "feature: SKILL.md states lane thresholds; they belong in mneme-flow's references/depth.md" >&2
  exit 1
fi

echo "feature: validation passed"
exit 0
