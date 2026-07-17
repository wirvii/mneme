#!/bin/sh
# mneme-profile-author/validation/run.sh
#
# Deterministic structural check (no network, no LLM calls): confirms this
# SKILL.md still documents the tools it depends on (profile_new,
# subagent_compose, scaffold_capture), the Go-authored capa-1 invariant
# (tools:/permissionMode: are NEVER hand-written, only produced via
# archetype), and the capture-based scaffold authoring flow (scaffolds are
# captured + curated, never hand-written). This deliberately couples the prose
# to the mechanism, the same posture mneme-init/validation/run.sh established.
set -e

SKILL_FILE="SKILL.md"

if [ ! -f "$SKILL_FILE" ]; then
  echo "mneme-profile-author: $SKILL_FILE not found in $(pwd)" >&2
  exit 1
fi

for tool in profile_new subagent_compose scaffold_capture; do
  if ! grep -q "$tool" "$SKILL_FILE"; then
    echo "mneme-profile-author: missing reference to required tool: $tool" >&2
    exit 1
  fi
done

if ! grep -q 'tools:' "$SKILL_FILE" || ! grep -q 'permissionMode:' "$SKILL_FILE"; then
  echo "mneme-profile-author: missing reference to the tools:/permissionMode: capability keys" >&2
  exit 1
fi

if ! grep -qi 'archetype' "$SKILL_FILE"; then
  echo "mneme-profile-author: missing reference to 'archetype' (the Go-authored capa-1 invariant)" >&2
  exit 1
fi

if ! grep -q 'scaffolds/' "$SKILL_FILE"; then
  echo "mneme-profile-author: missing reference to scaffolds/ (project scaffold authoring)" >&2
  exit 1
fi

# The scaffold authoring flow must be capture-based, never hand-written.
if ! grep -qi 'capture' "$SKILL_FILE"; then
  echo "mneme-profile-author: missing reference to scaffold capture (capture + curation flow)" >&2
  exit 1
fi

echo "mneme-profile-author: validation passed"
exit 0
