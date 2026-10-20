#!/bin/sh
# codegraph/validation/run.sh
#
# Deterministic structural check (no network, no LLM calls): confirms this
# SKILL.md stays within its size budget, still names the four codegraph tools
# that remain on MCP, and still points at the CLI commands that replaced the
# other six (SPEC-165).
set -e

SKILL_FILE="SKILL.md"
MAX_BYTES=4096

if [ ! -f "$SKILL_FILE" ]; then
  echo "codegraph: $SKILL_FILE not found in $(pwd)" >&2
  exit 1
fi

size=$(wc -c < "$SKILL_FILE" | tr -d ' ')
if [ "$size" -gt "$MAX_BYTES" ]; then
  echo "codegraph: $SKILL_FILE is $size bytes, over the $MAX_BYTES limit" >&2
  exit 1
fi

for tool in codegraph_search codegraph_context codegraph_callers codegraph_impact; do
  if ! grep -q "$tool" "$SKILL_FILE"; then
    echo "codegraph: missing MCP tool name $tool" >&2
    exit 1
  fi
done

for cmd in callees trace node explore files status; do
  if ! grep -Eq "(mneme codegraph |\`)${cmd}[ \`]" "$SKILL_FILE"; then
    echo "codegraph: missing CLI command $cmd" >&2
    exit 1
  fi
done

echo "codegraph: validation passed"
exit 0
