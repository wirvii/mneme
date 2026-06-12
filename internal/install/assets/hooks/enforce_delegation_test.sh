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
# F-cases: SPEC-043 regression — 4 fixes (Fix 1..4)
# ---------------------------------------------------------------------------

# Fix 1 — strip redirect operator pegado al candidato (2>/dev/null)

# F1: python with 2>/dev/null and no protected path → allow
run_case "F1_python_redirect_2devnull_no_path" \
  '{"tool_name":"Bash","tool_input":{"command":"python3 -c \"print(2+2)\" 2>/dev/null"}}' \
  0

# F2: python opens whitelisted CLAUDE.md with stderr redirect → allow
run_case "F2_python_open_CLAUDE_md_with_redirect" \
  '{"tool_name":"Bash","tool_input":{"command":"python3 -c \"open('"'"'CLAUDE.md'"'"')\" 2>/dev/null"}}' \
  0

# F3: python opens protected path with stderr redirect → still block (exit 2)
run_case "F3_python_open_protected_path_with_redirect" \
  '{"tool_name":"Bash","tool_input":{"command":"python3 -c \"open('"'"'internal/x.go'"'"','"'"'w'"'"')\" 2>/dev/null"}}' \
  2

# F4: node with 2>/dev/null and no protected path → allow
run_case "F4_node_redirect_2devnull_no_path" \
  '{"tool_name":"Bash","tool_input":{"command":"node -e \"console.log(1)\" 2>/dev/null"}}' \
  0

# Fix 2 — tilde expansion + whitelist ~/.mneme/** and ~/.claude/**

# F5: Write to ~/.mneme/... with literal tilde → allow (SDD workflow dir)
run_case "F5_write_tilde_mneme" \
  '{"tool_name":"Write","tool_input":{"file_path":"~/.mneme/workflows/x/specs/SPEC-043/spec.md"}}' \
  0

# F6: Write to ~/.claude/... with literal tilde → allow
run_case "F6_write_tilde_dotclaude" \
  '{"tool_name":"Write","tool_input":{"file_path":"~/.claude/settings.json"}}' \
  0

# F7: Bash redirect to ~/.mneme/... with literal tilde → allow
run_case "F7_redirect_tilde_mneme" \
  '{"tool_name":"Bash","tool_input":{"command":"echo x > ~/.mneme/scratch.txt"}}' \
  0

# F8: Write to ~/.config/foo (tilde, NOT in whitelist) → block
run_case "F8_write_tilde_config_not_whitelisted" \
  '{"tool_name":"Write","tool_input":{"file_path":"~/.config/foo"}}' \
  2

# Fix 3 — /tmp/** and /private/tmp/** scratch

# F9: Write to /tmp/... → allow (orchestrator scratch)
run_case "F9_write_tmp" \
  '{"tool_name":"Write","tool_input":{"file_path":"/tmp/hook_smoke.sh"}}' \
  0

# F10: Bash redirect to /tmp/... → allow
run_case "F10_redirect_tmp" \
  '{"tool_name":"Bash","tool_input":{"command":"echo x > /tmp/out.log"}}' \
  0

# F11: Write to /private/tmp/... (macOS real path) → allow
run_case "F11_write_private_tmp" \
  '{"tool_name":"Write","tool_input":{"file_path":"/private/tmp/x"}}' \
  0

# F12: Bash redirect to /var/foo (NOT tmp, NOT whitelist) → block
run_case "F12_redirect_var_not_whitelisted" \
  '{"tool_name":"Bash","tool_input":{"command":"echo x > /var/foo"}}' \
  2

# Fix 4 — process substitution < <(cmd) excluded

# F13: < <(jq ...) is not a redirect to a file path → allow
run_case "F13_process_substitution_not_redirect" \
  '{"tool_name":"Bash","tool_input":{"command":"while read l; do echo $l; done < <(jq -r .x /tmp/a.json)"}}' \
  0

# ---------------------------------------------------------------------------
# S-cases: SPEC-043 scratch / CONDICIÓN 2 — /tmp is not a repo bridge
# ---------------------------------------------------------------------------

# S1: cp from /tmp TOWARD protected path → block (destination is protected)
# _find_last_word_target takes the last non-flag word (the destination).
run_case "S1_cp_tmp_to_protected" \
  '{"tool_name":"Bash","tool_input":{"command":"cp /tmp/x.go internal/store/x.go"}}' \
  2

# S2: mv from /tmp TOWARD apps/ → block (destination is protected)
run_case "S2_mv_tmp_to_apps" \
  '{"tool_name":"Bash","tool_input":{"command":"mv /tmp/x apps/web/x.ts"}}' \
  2

# S3: cp TOWARD /tmp (destination is scratch) → allow
run_case "S3_cp_to_tmp" \
  '{"tool_name":"Bash","tool_input":{"command":"cp internal/x.go /tmp/x.go"}}' \
  0

# S4: tee to /tmp/... → allow
run_case "S4_tee_tmp" \
  '{"tool_name":"Bash","tool_input":{"command":"echo x | tee /tmp/out"}}' \
  0

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
printf '\n=== Smoke test results: %d passed, %d failed ===\n' "$PASS" "$FAIL"
[[ "$FAIL" -eq 0 ]]
