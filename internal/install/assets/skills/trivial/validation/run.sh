#!/bin/sh
# trivial/validation/run.sh
#
# Deterministic structural check (no network, no LLM): size budget, anchors,
# and no numeric lane thresholds (they live only in mneme-flow's
# references/depth.md, SPEC-169 D5).
set -e

SKILL_FILE="SKILL.md"
MAX_BYTES=4096

[ -f "$SKILL_FILE" ] || { echo "trivial: $SKILL_FILE not found in $(pwd)" >&2; exit 1; }

size=$(wc -c < "$SKILL_FILE" | tr -d ' ')
if [ "$size" -gt "$MAX_BYTES" ]; then
  echo "trivial: $SKILL_FILE is $size bytes, over the $MAX_BYTES limit" >&2
  exit 1
fi

for anchor in spec_quick lane_audit lane_reclassify "Stay strictly within the declared scope" references/depth.md; do
  if ! grep -q -- "$anchor" "$SKILL_FILE"; then
    echo "trivial: missing anchor: $anchor" >&2
    exit 1
  fi
done

if grep -Eq '3 fi''les|20 li''nes|≤ ?3 |≤ ?20' "$SKILL_FILE"; then
  echo "trivial: SKILL.md states lane thresholds; they belong in mneme-flow's references/depth.md" >&2
  exit 1
fi

echo "trivial: validation passed"
exit 0
