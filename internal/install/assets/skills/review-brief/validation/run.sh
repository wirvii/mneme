#!/bin/sh
# review-brief/validation/run.sh
#
# Deterministic structural check (no network, no LLM): size budget, anchors
# and the report template reference.
set -e

SKILL_FILE="SKILL.md"
MAX_BYTES=4096

[ -f "$SKILL_FILE" ] || { echo "review-brief: $SKILL_FILE not found in $(pwd)" >&2; exit 1; }

size=$(wc -c < "$SKILL_FILE" | tr -d ' ')
if [ "$size" -gt "$MAX_BYTES" ]; then
  echo "review-brief: $SKILL_FILE is $size bytes, over the $MAX_BYTES limit" >&2
  exit 1
fi

for anchor in review_range verbatim spec_reject backlog_add "delivered end" references/report.md; do
  if ! grep -q -- "$anchor" "$SKILL_FILE"; then
    echo "review-brief: missing anchor: $anchor" >&2
    exit 1
  fi
done

for section in "## Dentro del contrato" "## Fuera del contrato"; do
  if ! grep -q -- "$section" references/report.md; then
    echo "review-brief: references/report.md lacks section: $section" >&2
    exit 1
  fi
done

if grep -Eq '3 fi''les|20 li''nes' "$SKILL_FILE"; then
  echo "review-brief: SKILL.md states lane thresholds; they belong in mneme-flow's references/depth.md" >&2
  exit 1
fi

echo "review-brief: validation passed"
exit 0
