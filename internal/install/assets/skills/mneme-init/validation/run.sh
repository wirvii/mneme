#!/bin/sh
# mneme-init/validation/run.sh
#
# Deterministic structural check (no network, no LLM calls): confirms this
# SKILL.md still documents the subagent_* MCP tools the grill depends on.
# Guards against silent drift between the prose workflow and the real tool
# names exposed by the MCP server.
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

echo "mneme-init: validation passed"
exit 0
