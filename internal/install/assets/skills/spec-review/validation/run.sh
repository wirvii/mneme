#!/bin/sh
# spec-review/validation/run.sh
#
# Deterministic structural check (no network, no LLM), run from the skill's
# root as `sh validation/run.sh`: SKILL.md stays within its size budget and
# keeps its anchors, the report reference keeps the five roles and the five
# outcomes, every reference file exists, and the Apache-2.0 attribution is in
# place (SPEC-181).
set -e

SKILL_FILE="SKILL.md"
REPORT_FILE="references/report.md"
MAX_BYTES=4096
ORIGIN="google/elephant-goldfish-model"

fail() {
  echo "spec-review: $1" >&2
  exit 1
}

[ -f "$SKILL_FILE" ] || fail "$SKILL_FILE not found in $(pwd)"

size=$(wc -c < "$SKILL_FILE" | tr -d ' ')
if [ "$size" -gt "$MAX_BYTES" ]; then
  fail "$SKILL_FILE is $size bytes, over the $MAX_BYTES limit"
fi

for anchor in revision-spec.toml revision-plan.toml no_corrio spec-reviewer; do
  grep -q -- "$anchor" "$SKILL_FILE" || fail "missing anchor in $SKILL_FILE: $anchor"
done

[ -f "$REPORT_FILE" ] || fail "missing $REPORT_FILE"
for anchor in corregir refutar escalar descartar riesgo_aceptado comprension diseno hechos preparacion; do
  grep -q -- "$anchor" "$REPORT_FILE" || fail "missing anchor in $REPORT_FILE: $anchor"
done

for ref in comprension.md diseno.md hechos.md preparacion.md report.md NOTICE.md LICENSE-apache-2.0.txt; do
  [ -f "references/$ref" ] || fail "missing references/$ref"
done

for file in NOTICE.md comprension.md diseno.md hechos.md preparacion.md; do
  grep -q -- "$ORIGIN" "references/$file" || fail "references/$file does not cite $ORIGIN"
done

echo "spec-review: validation passed"
exit 0
