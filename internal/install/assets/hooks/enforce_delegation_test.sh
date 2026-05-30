#!/usr/bin/env bash
# enforce_delegation_test.sh — Smoke tests for enforce_delegation.sh (SPEC-042)
#
# Usage: bash enforce_delegation_test.sh /path/to/enforce_delegation.sh
#
# Exits 0 if all cases pass, 1 if any case fails.
# Each test case sends a synthetic PreToolUse JSON payload and checks exit code.

set -euo pipefail

HOOK="${1:-$(dirname "$0")/enforce_delegation.sh}"

if [[ ! -f "$HOOK" ]]; then
  printf 'ERROR: hook not found: %s\n' "$HOOK" >&2
  exit 1
fi
if [[ ! -x "$HOOK" ]]; then
  chmod +x "$HOOK"
fi

PASS=0
FAIL=0

# run_case NAME JSON WANT_EXIT
run_case() {
  local name="$1"
  local json="$2"
  local want="$3"

  local got
  got="$(printf '%s' "$json" | bash "$HOOK" 2>/dev/null; printf '%s' "$?")"
  # bash exits with last command; capture exit code via $?
  set +e
  printf '%s' "$json" | bash "$HOOK" >/dev/null 2>/dev/null
  got=$?
  set -e

  if [[ "$got" -eq "$want" ]]; then
    printf '[PASS] %s (exit %d)\n' "$name" "$got"
    PASS=$((PASS+1))
  else
    printf '[FAIL] %s — want exit %d, got exit %d\n' "$name" "$want" "$got"
    FAIL=$((FAIL+1))
  fi
}

# ---------------------------------------------------------------------------
# B-cases: subagent detection (AC1)
# ---------------------------------------------------------------------------

# B1: agent_id at root (non-empty) → allow
run_case "B1_agent_id_root_nonempty" \
  '{"agent_id":"abc-123","tool_name":"Edit","tool_input":{"file_path":"internal/foo.go"}}' \
  0

# B2: agent_id empty string at root → block (orchestrator)
run_case "B2_agent_id_root_empty" \
  '{"agent_id":"","tool_name":"Edit","tool_input":{"file_path":"internal/foo.go"}}' \
  2

# B3: agent_id null at root → block (orchestrator)
run_case "B3_agent_id_root_null" \
  '{"agent_id":null,"tool_name":"Edit","tool_input":{"file_path":"internal/foo.go"}}' \
  2

# B4: agent_id nested in session → allow (D1 multi-key)
run_case "B4_agent_id_session_nested" \
  '{"session":{"agent_id":"abc-456"},"tool_name":"Edit","tool_input":{"file_path":"internal/foo.go"}}' \
  0

# B5: agent_id nested in context → allow (D1 multi-key)
run_case "B5_agent_id_context_nested" \
  '{"context":{"agent_id":"abc-789"},"tool_name":"Edit","tool_input":{"file_path":"internal/foo.go"}}' \
  0

# ---------------------------------------------------------------------------
# A-cases: orchestrator bypass hardening (AC2)
# ---------------------------------------------------------------------------

# A1: python shutil.copy with protected path → block
run_case "A1_python_shutil_copy" \
  '{"tool_name":"Bash","tool_input":{"command":"python3 -c \"import shutil; shutil.copy('"'"'src.go'"'"', '"'"'internal/dst.go'"'"')\""}}' \
  2

# A2: python subprocess.run with cp to protected path → block
run_case "A2_python_subprocess_cp" \
  '{"tool_name":"Bash","tool_input":{"command":"python3 -c \"import subprocess; subprocess.run(['"'"'cp'"'"', '"'"'src'"'"', '"'"'internal/dst.go'"'"'])\""}}' \
  2

# A3: python os.rename with protected path → block
run_case "A3_python_os_rename" \
  '{"tool_name":"Bash","tool_input":{"command":"python3 -c \"import os; os.rename('"'"'src.go'"'"', '"'"'internal/dst.go'"'"')\""}}' \
  2

# ---------------------------------------------------------------------------
# Non-regression: safe commands still allowed (AC3)
# ---------------------------------------------------------------------------

# NR1: python print(2+2) — no paths, no APIs → allow
run_case "NR1_python_print_only" \
  '{"tool_name":"Bash","tool_input":{"command":"python3 -c \"print(2+2)\""}}' \
  0

# NR2: Edit to .claude/ path → allow
run_case "NR2_edit_dotclaude" \
  '{"tool_name":"Edit","tool_input":{"file_path":".claude/settings.json"}}' \
  0

# NR3: no tool_name → allow (not our concern)
run_case "NR3_no_tool_name" \
  '{"tool_name":"Read","tool_input":{"file_path":"internal/foo.go"}}' \
  0

# NR4: Edit to docs/*.md → allow
run_case "NR4_edit_docs_md" \
  '{"tool_name":"Edit","tool_input":{"file_path":"docs/ARCHITECTURE.md"}}' \
  0

# NR7: SPEC-042 I-1 fix: node writeFileSync to CLAUDE.md (whitelist) → allow
# Previously the elif fallback blocked this because it only checked .claude/
run_case "NR7_node_write_CLAUDE_md" \
  '{"tool_name":"Bash","tool_input":{"command":"node -e \"fs.writeFileSync('\''CLAUDE.md'\'','\''x'\'')\""}}' \
  0

# NR8: SPEC-042 I-1 fix: python open to docs/*.md (whitelist) → allow
# Previously the elif fallback blocked this because it only checked .claude/
run_case "NR8_python_open_docs_md" \
  '{"tool_name":"Bash","tool_input":{"command":"python3 -c \"open('\''docs/x.md'\'','\''w'\'')\""}}' \
  0

# NR9: python open('.claude/x','w') → allow (existing behavior preserved)
run_case "NR9_python_open_dotclaude" \
  '{"tool_name":"Bash","tool_input":{"command":"python3 -c \"open('\''.claude/x'\'','\''w'\'')\""}}' \
  0

# NR5: SPEC-033/040 non-regression: redirect to internal → block
run_case "NR5_redirect_to_internal" \
  '{"tool_name":"Bash","tool_input":{"command":"echo hello > internal/foo.go"}}' \
  2

# NR6: SPEC-033/040 non-regression: redirect to .claude → allow
run_case "NR6_redirect_to_dotclaude" \
  '{"tool_name":"Bash","tool_input":{"command":"echo hello > .claude/settings.json"}}' \
  0

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
printf '\n=== Smoke test results: %d passed, %d failed ===\n' "$PASS" "$FAIL"
[[ "$FAIL" -eq 0 ]]
