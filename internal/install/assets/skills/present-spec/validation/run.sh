#!/bin/sh
# present-spec/validation/run.sh
#
# Deterministic structural check (no network, no LLM): size budget and the
# approval-gate anchors. The gate must stay stated here as well as in the
# operating manuals (SPEC-169 D2).
set -e

SKILL_FILE="SKILL.md"
MAX_BYTES=4096

[ -f "$SKILL_FILE" ] || { echo "present-spec: $SKILL_FILE not found in $(pwd)" >&2; exit 1; }

size=$(wc -c < "$SKILL_FILE" | tr -d ' ')
if [ "$size" -gt "$MAX_BYTES" ]; then
  echo "present-spec: $SKILL_FILE is $size bytes, over the $MAX_BYTES limit" >&2
  exit 1
fi

for anchor in "explicit yes" "is not approval" "never inherited" "this skill never relaxes it" "Plain language"; do
  if ! grep -q -- "$anchor" "$SKILL_FILE"; then
    echo "present-spec: missing anchor: $anchor" >&2
    exit 1
  fi
done

echo "present-spec: validation passed"
exit 0
