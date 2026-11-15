#!/bin/sh
# mneme-init/validation/run.sh
#
# Deterministic structural check (no network, no LLM calls): confirms this
# SKILL.md still documents the `mneme subagents ...` CLI commands the grill
# depends on (SPEC-163 moved it off the retired subagent_* MCP tools),
# AND (SPEC-090 D4) that it still documents every SDD lifecycle token the
# layer 2/3 boundary forbids from areas_layer3_md, plus the tools:/
# permissionMode: capability-key prohibition. This deliberately couples the
# prose to the mechanism: adding a token to
# internal/subagents.Layer23ForbiddenLifecycleTokens without also
# documenting it here (both in this script AND in SKILL.md's prose) must be
# caught before it ships silently out of sync.
set -e

SKILL_FILE="SKILL.md"

if [ ! -f "$SKILL_FILE" ]; then
  echo "mneme-init: $SKILL_FILE not found in $(pwd)" >&2
  exit 1
fi

for cmd in "mneme subagents fingerprint" "mneme subagents profile save" "mneme subagents compose" "mneme subagents write" "mneme subagents manifest-list"; do
  if ! grep -q "$cmd" "$SKILL_FILE"; then
    echo "mneme-init: missing reference to required command: $cmd" >&2
    exit 1
  fi
done

# SPEC-163: the subagent_* MCP tools were retired from the server; a SKILL.md
# that names them again would send the grill to tools that no longer exist.
for retired in subagent_compose subagent_write; do
  if grep -q "$retired" "$SKILL_FILE"; then
    echo "mneme-init: still references the retired MCP tool: $retired" >&2
    exit 1
  fi
done

# SPEC-090 D4/G5: mirrors internal/subagents.Layer23ForbiddenLifecycleTokens.
# Adding a 4th token there without adding it here too must fail this check.
for token in spec_advance spec_quick spec_reject; do
  if ! grep -q "$token" "$SKILL_FILE"; then
    echo "mneme-init: missing reference to required layer 2/3-forbidden token: $token" >&2
    exit 1
  fi
done

if ! grep -q 'tools:' "$SKILL_FILE" || ! grep -q 'permissionMode:' "$SKILL_FILE"; then
  echo "mneme-init: missing reference to the tools:/permissionMode: capability-key prohibition" >&2
  exit 1
fi

# SPEC-121: the plain-language rule (rule 13) must stay documented in this
# skill. These are the three anchors shared with both operating manuals — a
# skill that drops one has silently stopped teaching the rule.
for anchor in "Plain language" "Channels that reach a person" "The exemption never travels with the text"; do
  if ! grep -q "$anchor" "$SKILL_FILE"; then
    echo "mneme-init: missing plain-language anchor: $anchor" >&2
    exit 1
  fi
done

# SPEC-140 D14/T5.2: Step 0.6 (diagnose before offering) must stay
# documented — without this anchor the step could be deleted from prose
# with nothing to notice.
if ! grep -q 'Step 0.6' "$SKILL_FILE"; then
  echo "mneme-init: missing reference to Step 0.6 (diagnose before offering anything)" >&2
  exit 1
fi

# SPEC-189: the size caps of SPEC-188, measured the same way as
# internal/install/skills_budget_test.go. SKILL.md is at most 4096 bytes, and
# the worst loading path (SKILL.md plus the largest references/*.md, in
# characters) is at most 20000, where Claude Code trims a loaded file.
main_bytes=$(wc -c < "$SKILL_FILE" | tr -d ' ')
if [ "$main_bytes" -gt 4096 ]; then
  echo "mneme-init: $SKILL_FILE is $main_bytes bytes, over 4096" >&2
  exit 1
fi
main_chars=$(LC_ALL=C.UTF-8 wc -m < "$SKILL_FILE" | tr -d ' ')
largest=0
largest_name="-"
for ref in references/*.md; do
  [ -f "$ref" ] || continue
  n=$(LC_ALL=C.UTF-8 wc -m < "$ref" | tr -d ' ')
  if [ "$n" -gt "$largest" ]; then
    largest=$n
    largest_name=$ref
  fi
done
if [ $((main_chars + largest)) -gt 20000 ]; then
  echo "mneme-init: worst loading path $SKILL_FILE ($main_chars) + $largest_name ($largest) = $((main_chars + largest)) characters, over 20000" >&2
  exit 1
fi

# SPEC-189: each reference exists and keeps the anchors its step depends on.
check_ref() {
  ref="references/$1"
  shift
  if [ ! -f "$ref" ]; then
    echo "mneme-init: missing reference: $ref" >&2
    exit 1
  fi
  for anchor in "$@"; do
    if ! grep -q -e "$anchor" "$ref"; then
      echo "mneme-init: $ref is missing anchor: $anchor" >&2
      exit 1
    fi
  done
}
check_ref core.md "mneme sdd status" "mneme team-memory status" "mneme sdd import" "mneme team-memory import" "AGENTS.md"
check_ref subagents.md "foreign_agents" "--areas-stdin" "areas_complete" "--composed-file" "backup_path" "mneme delegation-hook enable" "spec-reviewer"
check_ref opt-ins.md "mneme codegraph index" "mneme codegraph hooks install" "mneme team-memory enable" "mneme sdd enable" "--apply" "verbatim"
check_ref report.md "mneme-init complete for" "mneme-flow"

echo "mneme-init: validation passed"
exit 0
