#!/bin/sh
# mneme-init/validation/run.sh
#
# Deterministic structural check (no network, no LLM calls): confirms this
# SKILL.md still documents the subagent_* MCP tools the grill depends on,
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

for tool in subagent_fingerprint subagent_profile_save subagent_compose subagent_write subagent_manifest_list; do
  if ! grep -q "$tool" "$SKILL_FILE"; then
    echo "mneme-init: missing reference to required tool: $tool" >&2
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

echo "mneme-init: validation passed"
exit 0
