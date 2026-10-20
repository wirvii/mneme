#!/usr/bin/env bash
# codegraph-token-bench.sh — measure how many tokens the code graph saves.
#
# Runs the same structure questions twice per repetition: arm A with the four
# codegraph MCP tools allowed, arm B with them (and `mneme codegraph` through
# Bash) denied. Reports, per task and arm, the median tokens/cost/turns and
# how many answers were correct, then the total token saving of A over B.
# See "Medir el ahorro de tokens" in docs/codegraph.md (SPEC-165).
#
# Usage: scripts/codegraph-token-bench.sh [--tasks F] [--runs N] [--model M]
#                                         [--out DIR] [--dry-run]
#   --tasks F    TSV with columns id<TAB>prompt<TAB>expected
#                (default: scripts/codegraph-bench/tasks.tsv)
#   --runs N     repetitions per task and arm (default 3)
#   --model M    model passed to `claude --model` (default: the user's own)
#   --out DIR    directory for the raw JSON of every run (default: a new temp dir)
#   --dry-run    print the commands that would run, execute nothing, need
#                neither `claude` nor `jq`
#
# Exit codes: 0 ok, 2 missing requirement or bad usage.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
tasks="$script_dir/codegraph-bench/tasks.tsv"
runs=3
model=""
out=""
dry_run=0

graph_tools="mcp__mneme__codegraph_search mcp__mneme__codegraph_context mcp__mneme__codegraph_callers mcp__mneme__codegraph_impact"
base_tools="Read Grep Glob"
denied_tools="$graph_tools Bash(mneme codegraph:*)"

die() {
  echo "codegraph-token-bench: $*" >&2
  exit 2
}

while [ $# -gt 0 ]; do
  case "$1" in
    --tasks) [ $# -ge 2 ] || die "--tasks needs a value"; tasks="$2"; shift 2 ;;
    --runs) [ $# -ge 2 ] || die "--runs needs a value"; runs="$2"; shift 2 ;;
    --model) [ $# -ge 2 ] || die "--model needs a value"; model="$2"; shift 2 ;;
    --out) [ $# -ge 2 ] || die "--out needs a value"; out="$2"; shift 2 ;;
    --dry-run) dry_run=1; shift ;;
    -h|--help) sed -n '2,20p' "${BASH_SOURCE[0]}"; exit 0 ;;
    *) die "unknown argument: $1" ;;
  esac
done

case "$runs" in
  ''|*[!0-9]*|0) die "--runs must be a positive integer, got: $runs" ;;
esac
[ -f "$tasks" ] || die "tasks file not found: $tasks"

if [ "$dry_run" -eq 0 ]; then
  command -v claude >/dev/null 2>&1 || die "the claude CLI was not found in PATH (use --dry-run to only print the commands)"
  command -v jq >/dev/null 2>&1 || die "jq was not found in PATH (use --dry-run to only print the commands)"
fi

if [ -z "$out" ]; then
  if [ "$dry_run" -eq 1 ]; then
    out="<temp dir created at run time>"
  else
    out="$(mktemp -d "${TMPDIR:-/tmp}/codegraph-bench.XXXXXX")"
  fi
elif [ "$dry_run" -eq 0 ]; then
  mkdir -p "$out"
fi

# print_cmd prints the command in `cmd` so it can be pasted into a shell:
# arguments with spaces or shell-special characters are double-quoted.
print_cmd() {
  local arg
  for arg in "${cmd[@]}"; do
    case "$arg" in
      *[[:space:]\(\)\*\?\"]*) printf '"%s" ' "${arg//\"/\\\"}" ;;
      *) printf '%s ' "$arg" ;;
    esac
  done
  printf '\n'
}

# build_cmd ARM PROMPT fills the global array `cmd` with the claude invocation.
build_cmd() {
  local arm="$1" prompt="$2"
  cmd=(claude -p "$prompt" --output-format json)
  if [ -n "$model" ]; then
    cmd+=(--model "$model")
  fi
  if [ "$arm" = "A" ]; then
    cmd+=(--allowedTools "$base_tools $graph_tools")
  else
    cmd+=(--allowedTools "$base_tools" --disallowedTools "$denied_tools")
  fi
}

if [ "$dry_run" -eq 0 ]; then
  echo "codegraph-token-bench: raw results in $out" >&2
  echo "codegraph-token-bench: index the graph first (mneme codegraph index) and run from the repo root" >&2
  runs_tsv="$out/runs.tsv"
  : > "$runs_tsv"
fi

while IFS=$'\t' read -r id prompt expected || [ -n "${id:-}" ]; do
  [ -n "${id:-}" ] || continue
  case "$id" in \#*) continue ;; esac
  for arm in A B; do
    for run in $(seq 1 "$runs"); do
      build_cmd "$arm" "$prompt"
      if [ "$dry_run" -eq 1 ]; then
        printf '%s/%s/run%s: ' "$id" "$arm" "$run"
        print_cmd
        continue
      fi
      raw="$out/$id.$arm.$run.json"
      if ! "${cmd[@]}" > "$raw" 2> "$raw.err"; then
        echo "codegraph-token-bench: $id/$arm/run$run failed (see $raw.err), skipped" >&2
        continue
      fi
      if ! row="$(jq -r --arg id "$id" --arg arm "$arm" --arg run "$run" --arg exp "$expected" '
          [ $id, $arm, $run,
            ((.usage.input_tokens // 0) + (.usage.cache_creation_input_tokens // 0)
              + (.usage.cache_read_input_tokens // 0) + (.usage.output_tokens // 0)),
            (.total_cost_usd // 0),
            (.num_turns // 0),
            (if ((.result // "") | ascii_downcase | contains($exp | ascii_downcase)) then 1 else 0 end)
          ] | @tsv' "$raw")"; then
        echo "codegraph-token-bench: $id/$arm/run$run: unreadable JSON, skipped" >&2
        continue
      fi
      printf '%s\n' "$row" >> "$runs_tsv"
    done
  done
done < "$tasks"

if [ "$dry_run" -eq 1 ]; then
  exit 0
fi

# Report: median per task and arm, then totals and relative saving.
jq -Rrs '
  def median: sort | if length == 0 then 0 else ((.[((length - 1) / 2) | floor] + .[(length / 2) | floor]) / 2) end;
  split("\n") | map(select(length > 0) | split("\t"))
  | map({task: .[0], arm: .[1], tokens: (.[3] | tonumber), cost: (.[4] | tonumber),
         turns: (.[5] | tonumber), ok: (.[6] == "1")})
  | group_by([.task, .arm])
  | map({task: .[0].task, arm: .[0].arm,
         tokens: (map(.tokens) | median), cost: (map(.cost) | median),
         turns: (map(.turns) | median),
         correct: (map(select(.ok)) | length), runs: length}) as $rows
  | ($rows | map(select(.arm == "A")) | map(.tokens) | add // 0) as $a
  | ($rows | map(select(.arm == "B")) | map(.tokens) | add // 0) as $b
  | ($rows | map(select(.arm == "A")) | map(.correct) | add // 0) as $ca
  | ($rows | map(select(.arm == "B")) | map(.correct) | add // 0) as $cb
  | (["task", "arm", "median_tokens", "median_cost_usd", "median_turns", "correct"] | @tsv),
    ($rows[] | [.task, .arm, .tokens, .cost, .turns, "\(.correct)/\(.runs)"] | @tsv),
    ("TOTAL\tA\t\($a)\t-\t-\t\($ca)"),
    ("TOTAL\tB\t\($b)\t-\t-\t\($cb)"),
    ("SAVING\t" + (if $b > 0 then ((1 - ($a / $b)) * 100 | . * 10 | round / 10 | tostring) + "%" else "n/a" end))
' "$runs_tsv"

echo "Decision rule (owner decides): saving >= 20% with no fewer correct answers -> keep the graph in mneme; otherwise candidate to split or retire." >&2
