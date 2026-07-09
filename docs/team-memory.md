# Team Memory — git-native shared knowledge (SPEC-053)

> SPEC-053 (EPIC, SS-A..SS-F) / SPEC-065 (SS-F: activation + docs). Closes
> "my teammate already figured this out, but their mneme never told mine."

## Overview

Team memory lets a repository's durable knowledge — decisions, conventions,
architecture notes, patterns, bugfixes, rules — flow between teammates through
the repository itself, with no server, no account, and no network call. It is
entirely **git-native**: the shared knowledge lives as committed `.md` files
under `.mneme/shared/` in the repo, and it moves between machines exactly the
way any other file does — `git push`/`git pull`/`git clone`.

It is **opt-in per repository** (SPEC-053 D3). Nothing changes for a project
until someone runs `mneme team-memory enable` in it.

## Activation

```bash
mneme team-memory enable
```

Run once, from anywhere inside the git repository. This single, idempotent
command:

1. Creates `<repo>/.mneme/shared/` with its `.mneme-vault` marker file if it
   does not already exist. The marker's presence is the **only** flag that
   turns team-memory on for a repository — there is no separate config file
   or environment variable (SPEC-053 D3).
2. **Bakes** `shared=1` onto every pre-existing memory of a durable type
   (see the table below) that is still local-only, so knowledge saved before
   you ran `enable` does not stay stranded on your machine.
3. **Exports** every shared memory (freshly baked or already shared) to
   `.mneme/shared/notes/<uuid>.md`.
4. Installs the `post-merge`/`post-checkout` git hooks that import teammates'
   shared knowledge automatically after every pull, merge, or branch switch
   (the same mechanism `mneme team-memory hooks install` provides on its own
   — `enable` just runs it for you).
5. Prints an explicit **privacy notice** (see below) — always, on every run.

Re-running `enable` is safe: an already-shared memory is not re-baked, an
already-installed hook is not duplicated, and a pre-existing marker is left
untouched.

```
$ mneme team-memory enable
Team memory enabled at /path/to/repo/.mneme/shared
Baked 3 pre-existing memories to shared, 3 exported to the vault.
Installed import hooks: post-merge, post-checkout

PRIVACY NOTICE: .mneme/shared/ is committed to this repository.
Every memory materialized there (decisions, conventions, architecture,
patterns, bugfixes, rules) becomes visible to anyone who can read this repo,
including its full commit history once pushed.
Remote: git@github.com:you/your-repo.git
mneme cannot determine offline whether this remote is public — if it is
(or might become public), review .mneme/shared/ before pushing.
```

You still need to `git add .mneme/shared && git commit` and `git push` for
teammates to actually receive the knowledge — `enable` prepares the vault and
hooks, it does not make git commits on your behalf.

## What gets shared

Sharing is controlled by a per-memory `shared` level (0/1/2), not a new scope
— it layers on top of the existing `project` scope (SPEC-053 D2):

| `shared` | Meaning | Set by |
|----------|---------|--------|
| `0` (default) | Local-only. Never materialized to the vault. | Anything saved before team-memory was active, or an explicit opt-out. |
| `1` (auto-shared) | Shared because its **type** is considered durable knowledge. | `mem_save`/`svc.Save`, automatically, when team-memory is active. |
| `2` (team-curated) | Explicitly, deliberately shared regardless of type. | `mneme promote <id>` / `mem_promote`. |

**Durable types** (auto-share to `shared=1` when team-memory is active):

| Type | Auto-shared? |
|------|:---:|
| `decision` | ✅ |
| `convention` | ✅ |
| `architecture` | ✅ |
| `pattern` | ✅ |
| `bugfix` | ✅ |
| `rule` | ✅ |
| `discovery` | ❌ |
| `config` | ❌ |
| `session_summary` | ❌ |
| `synthesis` | ❌ |

Global- and org-scoped memories (personal preferences, cross-project config)
are **never** auto-shared, regardless of type — team-memory only concerns a
single project's knowledge.

**Opting an ephemeral memory in:** call `mneme promote <id>` (or the
`mem_promote` MCP tool) to mark any individual memory as team-curated
(`shared=2`) and materialize it immediately, independent of its type.

**Opting a durable memory out:** pass an explicit `shared: 0` on `mem_save`
to keep one particular decision/convention/etc. local-only even though its
type would otherwise auto-share.

## WRITE — materialize on save

When team-memory is active for the current process (the marker was found at
service construction, or `mneme team-memory enable` just activated it),
`mem_save`/`svc.Save` and `svc.Update` **synchronously** write the memory to
`.mneme/shared/notes/<uuid>.md` immediately after persisting it to SQLite —
there is no background sync, no polling, no watcher. This is a deliberate
correction of an earlier design assumption: mneme has no live filesystem
watcher between the vault and the database (see [VAULT.md](VAULT.md)); write-
through materialization is implemented directly inside the save/update path.

Materialization is **best-effort**: a disk failure (permission denied, full
disk, `.mneme/shared/notes` blocked by a non-directory file) is logged and
never fails the save itself — the same philosophy already used for embedding
and wikilink resolution elsewhere in `service.Save`.

Each memory gets its own file, named by its immutable UUID
(`notes/<uuid>.md`), not by `topic_key` — the personal vault
([VAULT.md](VAULT.md)) mirrors `topic_key` as nested directories, but the
shared vault deliberately does not: two teammates creating unrelated memories
at the same moment can never collide on the same path at the git level. Only
edits to the exact same memory can conflict, and git resolves that the normal
way (see Conflicts below).

## READ — import via git hooks

After `mneme team-memory hooks install` (or `enable`, which includes it),
every `git merge` (including `git pull`) and `git checkout` triggers, in the
background, an import of `.mneme/shared/` into your local database:

```bash
mneme team-memory hooks install   # post-merge + post-checkout, idempotent
mneme team-memory hooks remove    # strip only the mneme-managed block
```

The import itself runs as a hidden subcommand (`mneme team-memory hooks
run-import`) that the installed hooks invoke detached (`&`) so the git
operation is never slowed down, and it **never fails the git command** — any
error is appended to `~/.mneme/team-memory-hooks.log` instead of being
surfaced. It is skipped entirely while a rebase, merge, or cherry-pick is
already in progress, to avoid firing dozens of redundant imports during a
single interactive rebase.

Merge strategy: a note updates the local memory only when its file
`updated_at` is strictly newer than the local database's `updated_at` for
that id; otherwise it is skipped. This is idempotent — importing the same
vault state twice in a row produces zero changes the second time.

Every memory materialized by write-through carries its own id
(SPEC-053 D1's one-file-per-UUID layout); import preserves that id exactly
rather than assigning a fresh one, so a re-import of the same note is always
recognized — even for memories with no `topic_key`.

Author and shared-level attribution round-trip through the file's
frontmatter and are **never** overwritten by the importing peer's own git
identity — the note keeps the identity of whoever originally shared it.

## Conflicts

Two independent layers, matching the two ways a shared vault can disagree:

1. **Git-level.** Because every memory has its own file (`notes/<uuid>.md`),
   two teammates creating unrelated memories concurrently can never produce a
   git conflict — only two edits to the *same* memory can, and git resolves
   that with its normal merge/rebase conflict-resolution UI, same as any
   other tracked file.
2. **Semantic.** After every import, mneme runs the existing deterministic
   FTS5 candidate-detection (the same one behind `mneme conflicts
   candidates`) against every memory the import created or updated, and
   reports a count if any are found:

   ```
   [2026-07-09T00:00:00Z] repo=/path/to/repo event=conflict_report count=2 hint="run `mneme conflicts scan`"
   ```

   This is a **count only** — no LLM judgment ever runs automatically. Judging
   whether two memories actually contradict each other remains the separate,
   explicit, manual step it already is (see [conflicts.md](conflicts.md)):

   ```bash
   mneme conflicts scan            # dry-run judgment via local Claude CLI
   mneme conflicts scan --apply    # persist judged relations
   ```

## Privacy

`.mneme/shared/` is a directory **committed to the repository**. Once pushed,
its contents — and its full commit history — are visible to anyone who can
read the repository, exactly like any other tracked file.

Team-memory is intentionally **offline and git-native**: mneme never makes a
network call to ask GitHub, GitLab, or any other host whether a repository's
remote is public or private. Because of that, `mneme team-memory enable`
**always** prints its privacy notice — it can never determine visibility for
you, so it never silently assumes the repo is private. Review
`.mneme/shared/` before pushing to a remote that is, or might become, public.
Secret-scanning the shared vault's content is out of scope for mneme; a
durable-type memory should never contain credentials or other secrets in the
first place.

Personal preferences (`scope=global`) and ephemeral working notes
(`session_summary`, `synthesis`, ordinary `discovery`/`config` entries) are
excluded from auto-sharing precisely so they cannot accidentally leak into a
committed, potentially-public file.

## Offline / no-cloud

Every mechanism described here — write-through materialization, the import
hooks, and the FTS5 conflict-candidate report — is pure local filesystem and
git I/O. No mneme feature in this document makes a network call, calls an
LLM, or depends on any external service being reachable. This matches mneme's
broader no-cloud design: the SQLite databases, the personal vault, and now
the shared vault are all plain files under your control.

## Coordination with `mneme-init`

The `mneme-init` skill's Step 3 (shared team memory) is the conversational,
opt-in entry point for onboarding a repository: it asks the user's
permission, then invokes `mneme team-memory enable` on their behalf and
relays its output — including the privacy notice — verbatim. It never
reimplements vault export or hook installation itself. See the skill's
`SKILL.md` (`internal/install/assets/skills/mneme-init/SKILL.md`) for the
exact wording it uses.

## CLI reference

```bash
mneme team-memory enable                  # activate: marker + bake/export + hooks
mneme team-memory hooks install           # install only the import hooks
mneme team-memory hooks remove            # remove only the mneme-managed hook block
mneme promote <id>                        # mark one memory as team-curated (shared=2)
```

## Anti-scope

- No fsnotify-style live watcher — materialization is synchronous write-
  through inside `Save`/`Update`; import is triggered by git hooks, not
  polling.
- No automatic LLM conflict judgment — only the deterministic FTS5 candidate
  count runs automatically; `mneme conflicts scan` remains manual.
- No secret-scanning of the shared vault.
- No network calls anywhere in this feature, including the privacy notice.
- No changes to the personal per-user vault (`mneme vault export/import`,
  [VAULT.md](VAULT.md)) — team memory is a separate, git-native vault at
  `.mneme/shared/`, not a mode of the personal one.

---

## Related docs

- [VAULT.md](VAULT.md) — the personal, non-git filesystem mirror this feature
  is deliberately distinct from.
- [conflicts.md](conflicts.md) — the two-phase FTS5 detection / LLM judgment
  workflow reused here for import-time conflict reporting.
