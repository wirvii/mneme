#!/bin/sh
# bug/validation/run.sh
#
# Deterministic structural check (no network, no LLM): size budget, anchors,
# and the absence of the pre-mneme .claude/bugs workflow that the two skills
# this one replaced used to carry.
set -e

SKILL_FILE="SKILL.md"
MAX_BYTES=4096

[ -f "$SKILL_FILE" ] || { echo "bug: $SKILL_FILE not found in $(pwd)" >&2; exit 1; }

size=$(wc -c < "$SKILL_FILE" | tr -d ' ')
if [ "$size" -gt "$MAX_BYTES" ]; then
  echo "bug: $SKILL_FILE is $size bytes, over the $MAX_BYTES limit" >&2
  exit 1
fi

for anchor in backlog_add backlog_refine backlog_promote "diagnosis, not a fix" present-spec references/diagnosis.md; do
  if ! grep -q -- "$anchor" "$SKILL_FILE"; then
    echo "bug: missing anchor: $anchor" >&2
    exit 1
  fi
done

if grep -rq '\.claude/bugs' "$SKILL_FILE" references; then
  echo "bug: mentions .claude/bugs; bugs live in the mneme backlog" >&2
  exit 1
fi

if grep -Eq '3 fi''les|20 li''nes' "$SKILL_FILE"; then
  echo "bug: SKILL.md states lane thresholds; they belong in mneme-flow's references/depth.md" >&2
  exit 1
fi

[ -f references/diagnosis.md ] || { echo "bug: missing references/diagnosis.md" >&2; exit 1; }

echo "bug: validation passed"
exit 0
