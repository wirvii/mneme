#!/usr/bin/env bash
# cleanup-test-pollution.sh — SPEC-085 D7: one-off cleanup of the test-fixture
# pollution the SPEC-085 root cause wrote into wirvii-mneme.db and
# .mneme/shared/notes/ before it was fixed (commits 1-7 of this spec).
#
# This is NOT a general-purpose "purge project" capability. See the design's
# D7 rationale for why that capability is deliberately not being built: an
# owner-facing `mneme purge-project <slug>` would also delete the ~312
# orphaned-slug REAL memories tracked separately by BL-102. This script only
# ever touches the 14 exact test-project slugs enumerated below, in ONE
# specific database.
#
# Usage:
#   scripts/cleanup-test-pollution.sh              # dry-run (default, safe)
#   scripts/cleanup-test-pollution.sh --apply       # back up, then delete
#
# Overrides (mainly for testing this script itself against a throwaway
# fixture DB/vault instead of the real ones):
#   MNEME_CLEANUP_DB=<path>          default: $HOME/.mneme/projects/wirvii-mneme.db
#   MNEME_CLEANUP_VAULT_NOTES=<dir>  default: <repo-root>/.mneme/shared/notes
#
# Order (per the design, not swappable): the leak must already be closed
# (SPEC-085 commits 1-7, merged before this script is ever meant to run) ->
# purge the vault -> purge the DB. Purging the DB first is pointless: the
# next post-merge/post-checkout git hook (mneme team-memory hooks
# run-import) would re-import every note right back from the vault.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DB_PATH="${MNEME_CLEANUP_DB:-$HOME/.mneme/projects/wirvii-mneme.db}"
VAULT_NOTES_DIR="${MNEME_CLEANUP_VAULT_NOTES:-$REPO_ROOT/.mneme/shared/notes}"
REAL_PROJECT="wirvii/mneme"
APPLY=0

for arg in "$@"; do
  case "$arg" in
    --apply) APPLY=1 ;;
    -h|--help)
      grep -E '^# ' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "cleanup-test-pollution: unknown argument: $arg" >&2
      exit 2
      ;;
  esac
done

# Explicit denylist of the 14 exact test-project slugs identified during
# SPEC-085 diagnosis (mem_get 019f6839, 019f678d — hallazgo C1). NO globs,
# NO LIKE patterns, NO heuristics: anything not enumerated here survives, by
# design. This is what keeps the ~312 orphaned-slug REAL memories (BL-102,
# in OTHER databases entirely) categorically out of reach.
DENYLIST=(
  "test/project"
  "test/graph"
  "test-project"
  "test/ppr"
  "test/commpack"
  "test/community"
  "test/synthesis"
  "test/ctx-graph"
  "test/autopack"
  "test/flatmode"
  "other/project"
  "test/rebuild"
  "test-subagents-profile"
  "test-subagents-write"
)

command -v sqlite3 >/dev/null 2>&1 || {
  echo "cleanup-test-pollution: sqlite3 CLI not found on PATH — required by this one-off script." >&2
  exit 1
}

if [ ! -f "$DB_PATH" ]; then
  echo "cleanup-test-pollution: DB not found at $DB_PATH (override with MNEME_CLEANUP_DB=<path>)." >&2
  exit 1
fi

echo "== cleanup-test-pollution: SPEC-085 D7 =="
echo "DB path:         $DB_PATH"
echo "Vault notes dir: $VAULT_NOTES_DIR"
echo "Mode:            $([ "$APPLY" -eq 1 ] && echo APPLY || echo DRY-RUN)"
echo

# sql_quote doubles single quotes so a slug can be safely interpolated into a
# SQL string literal. None of the 14 denylist entries actually contain a
# quote today, but every value that reaches sqlite3 goes through this.
sql_quote() {
  printf '%s' "$1" | sed "s/'/''/g"
}

# --- 1. Precondition: every distinct project in the DB must be a subset of
#        {wirvii/mneme} u denylist. Any unknown project aborts immediately,
#        before ANYTHING is touched — dry-run or not.
echo "-- Precondition: project census (active memories) --"
CENSUS="$(sqlite3 -separator '|' "$DB_PATH" \
  "SELECT project, COUNT(*) FROM memories WHERE deleted_at IS NULL GROUP BY project ORDER BY project;")"
echo "$CENSUS"
echo

is_known_project() {
  local p="$1"
  [ "$p" = "$REAL_PROJECT" ] && return 0
  local d
  for d in "${DENYLIST[@]}"; do
    [ "$p" = "$d" ] && return 0
  done
  return 1
}

unknown_found=0
while IFS='|' read -r project count; do
  [ -z "${project:-}" ] && continue
  if ! is_known_project "$project"; then
    echo "cleanup-test-pollution: ABORT — unknown project '$project' ($count rows) is not $REAL_PROJECT and not in the denylist." >&2
    unknown_found=1
  fi
done <<< "$CENSUS"

if [ "$unknown_found" -eq 1 ]; then
  echo "cleanup-test-pollution: the DB no longer matches the SPEC-085 diagnosis snapshot — refusing to guess. Aborting without touching anything." >&2
  exit 1
fi
echo "Precondition OK: every project in the DB is either $REAL_PROJECT or an enumerated test slug."
echo

# --- 2. Enumerate what would be purged (always printed, dry-run or apply).
echo "-- Rows to purge, by slug --"
TOTAL_ROWS=0
for slug in "${DENYLIST[@]}"; do
  q="$(sql_quote "$slug")"
  n=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM memories WHERE deleted_at IS NULL AND project = '$q';")
  TOTAL_ROWS=$((TOTAL_ROWS + n))
  printf '  %-28s %6d rows\n' "$slug" "$n"
done
echo "Total memories rows to purge: $TOTAL_ROWS"
echo

VAULT_COUNT=0
VAULT_FILES=()
if [ -d "$VAULT_NOTES_DIR" ]; then
  for slug in "${DENYLIST[@]}"; do
    while IFS= read -r f; do
      [ -z "$f" ] && continue
      VAULT_FILES+=("$f")
      VAULT_COUNT=$((VAULT_COUNT + 1))
    done < <(grep -x -l "project: $slug" "$VAULT_NOTES_DIR"/*.md 2>/dev/null || true)
  done
fi
echo "Total vault notes to purge: $VAULT_COUNT"
echo

if [ "$APPLY" -eq 0 ]; then
  echo "Dry-run complete. Nothing was modified. Re-run with --apply to perform the purge (a DB backup is taken first)."
  exit 0
fi

# --- 3. Backup the DB before anything is deleted (mandatory, verifiable on disk).
BACKUP_PATH="${DB_PATH}.bak-$(date -u +%Y%m%dT%H%M%SZ)"
cp "$DB_PATH" "$BACKUP_PATH"
if [ ! -f "$BACKUP_PATH" ]; then
  echo "cleanup-test-pollution: ABORT — backup verification failed, $BACKUP_PATH does not exist after cp." >&2
  exit 1
fi
echo "Backup written: $BACKUP_PATH"
echo

# --- 4. Purge the vault FIRST. Order matters: purging the DB before the
#        vault is pointless — the next post-merge/post-checkout hook would
#        re-import every note right back (SPEC-085 §1.2 round-trip).
if [ "$VAULT_COUNT" -gt 0 ]; then
  echo "-- Purging $VAULT_COUNT vault note(s) --"
  for f in "${VAULT_FILES[@]}"; do
    rm -f "$f"
    echo "  removed $f"
  done
else
  echo "No vault notes to purge."
fi
echo

# --- 5. Purge the DB, in one transaction, in FK-safe order:
#        sessions (summary_id -> memories, NO ACTION, no cascade) first,
#        then memory_relations (013, no FK at all — explicit cleanup),
#        then memories itself (cascades memory_files/memory_entities/
#        embeddings/unresolved_references), then entities (cascades
#        relations/memory_entities), then communities (cascades
#        community_members).
#
#        specs needs special handling: migration 005 (spec_pk_by_project)
#        changed specs' PRIMARY KEY to the composite (project, id) and, as
#        a direct consequence, DROPPED the REFERENCES specs(id) FK from
#        spec_history/spec_pushbacks entirely — SQLite cannot declare a
#        single-column FK against a table whose PK is composite. spec_id in
#        those two tables is therefore the BARE id, which is NOT globally
#        unique (two different projects can both have a "SPEC-001"). A
#        naive "spec_id IN (SELECT id FROM specs WHERE project IN
#        (denylist))" could delete a REAL wirvii/mneme spec's history if
#        its id happens to collide with a purged test spec's id. We
#        therefore additionally exclude any spec_id that also belongs to a
#        wirvii/mneme spec — on a collision this leaves a handful of
#        orphaned test-spec history/pushback rows behind rather than ever
#        risking real data, matching this script's fail-safe posture.
echo "-- Purging the DB --"

# -bail: in non-interactive (stdin/heredoc) mode, sqlite3's default behaviour
# is to keep executing subsequent statements after one fails — which would
# let a mid-transaction error (e.g. an unexpected FK violation) skip straight
# past the failing DELETE to COMMIT, applying a partial purge. -bail makes
# sqlite3 stop and exit non-zero at the FIRST error, before COMMIT is ever
# reached; the still-open transaction is then rolled back implicitly when
# the connection closes. Combined with `set -euo pipefail` above, the script
# itself halts immediately too. This is meant to make a partial purge
# structurally impossible, not merely caught after the fact by the REMAINING
# check below.
IN_LIST="$(
  first=1
  for slug in "${DENYLIST[@]}"; do
    q="$(sql_quote "$slug")"
    if [ "$first" -eq 1 ]; then
      printf "'%s'" "$q"
      first=0
    else
      printf ", '%s'" "$q"
    fi
  done
)"

sqlite3 -bail "$DB_PATH" <<SQL
PRAGMA foreign_keys = ON;
BEGIN TRANSACTION;

DELETE FROM sessions WHERE project IN ($IN_LIST);

DELETE FROM memory_relations
  WHERE from_id IN (SELECT id FROM memories WHERE project IN ($IN_LIST))
     OR to_id   IN (SELECT id FROM memories WHERE project IN ($IN_LIST));

DELETE FROM memories WHERE project IN ($IN_LIST);

DELETE FROM entities WHERE project IN ($IN_LIST);

DELETE FROM communities WHERE project IN ($IN_LIST);

DELETE FROM spec_pushbacks
  WHERE spec_id IN (SELECT id FROM specs WHERE project IN ($IN_LIST))
    AND spec_id NOT IN (SELECT id FROM specs WHERE project = '$(sql_quote "$REAL_PROJECT")');

DELETE FROM spec_history
  WHERE spec_id IN (SELECT id FROM specs WHERE project IN ($IN_LIST))
    AND spec_id NOT IN (SELECT id FROM specs WHERE project = '$(sql_quote "$REAL_PROJECT")');

DELETE FROM specs WHERE project IN ($IN_LIST);

DELETE FROM backlog_items WHERE project IN ($IN_LIST);

COMMIT;
SQL

REMAINING=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM memories WHERE deleted_at IS NULL AND project != '$(sql_quote "$REAL_PROJECT")';")
if [ "$REMAINING" -ne 0 ]; then
  echo "cleanup-test-pollution: WARNING — $REMAINING non-$REAL_PROJECT memory row(s) remain after purge (unexpected — investigate before trusting this DB)." >&2
  exit 1
fi

echo
echo "Purge complete."
echo "  memories rows deleted (target): $TOTAL_ROWS"
echo "  vault notes deleted:            $VAULT_COUNT"
echo "  backup:                         $BACKUP_PATH"
echo
echo "Next step (not run automatically by this script):"
echo "  mneme graph cleanup-orphan-relations --apply --yes"
echo "This removes any relations left pointing at now-deleted test entities."
