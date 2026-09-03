# Team Memory — git-native shared knowledge (SPEC-053)

> SPEC-053 (EPIC, SS-A..SS-F) / SPEC-065 (SS-F: activation + docs). Closes
> "my teammate already figured this out, but their mneme never told mine."

## Overview

Team memory lets a repository's human knowledge — decisions, discoveries,
conventions, architecture notes, patterns, bugfixes, rules, config, and
preferences — flow between teammates through the repository itself, with no
server, no account, and no network call. It is
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
2. **Bakes** `shared=1` onto every pre-existing memory of an auto-shared type
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
| `1` (auto-shared) | Shared because its **type** is human-authored, persistent knowledge. | `mem_save`/`svc.Save`, automatically, when team-memory is active. |
| `2` (team-curated) | Explicitly, deliberately shared regardless of type. | `mneme promote <id>` / `mem_promote`. |

**Auto-shared types** — the policy is **share-by-default** (SPEC-071): every
project-scoped memory auto-shares to `shared=1` when team-memory is active,
*except* two auto-generated / ephemeral types. A real shared brain means the
team inherits all durable human knowledge — decisions, discoveries, and the
discussions around them — not just a curated subset.

| Type | Auto-shared? | Why |
|------|:---:|------|
| `decision` | ✅ | |
| `discovery` | ✅ | |
| `config` | ✅ | |
| `preference` | ✅ | |
| `convention` | ✅ | |
| `architecture` | ✅ | |
| `pattern` | ✅ | |
| `bugfix` | ✅ | |
| `rule` | ✅ | |
| `session_summary` | ❌ | Ephemeral, verbose, fast-decaying. |
| `synthesis` | ❌ | Auto-generated cluster overviews — every peer regenerates them from its own graph, so sharing is noise. |

Global- and org-scoped memories (personal preferences, cross-project config)
are **never** auto-shared, regardless of type — team-memory only concerns a
single project's knowledge.

**Opting an excluded memory in:** call `mneme promote <id>` (or the
`mem_promote` MCP tool) to mark any individual memory as team-curated
(`shared=2`) and materialize it immediately, independent of its type.

**Opting a memory out:** pass an explicit `shared: 0` on `mem_save`
to keep one particular entry local-only even though its type would otherwise
auto-share.

**Retroactivity (repos enabled before SPEC-071):** a repository that turned
team-memory on under the old 6-type policy still has its pre-existing
`discovery`/`config`/`preference` memories at `shared=0`. Re-run
`mneme team-memory enable` — it is idempotent and its bake step reuses the same
share-by-default criterion, so it re-marks those memories `shared=1` and exports
them to the vault. No dedicated backfill command is needed.

## WRITE — materialize on save

When team-memory is active for the current process — `initService` opted in
via `service.WithTeamMemory(service.DetectTeamMemory())` at construction
(SPEC-085 D1/D2; see below), or `mneme team-memory enable` just activated it
for the rest of this process — `mem_save`/`svc.Save` and `svc.Update`
**synchronously** write the memory to
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

> **Windows:** the installed `post-merge`/`post-checkout` hooks are
> `#!/bin/sh` scripts that resolve the binary via `command -v mneme` — they
> run correctly on Windows once [Git for Windows](https://gitforwindows.org/)
> is installed, since its bundled `sh` is what git invokes to run hooks.
> Without Git for Windows there is no `sh` to run them, so the background
> import silently never fires (git itself is never blocked either way).

Author and shared-level attribution round-trip through the file's
frontmatter and are **never** overwritten by the importing peer's own git
identity — the note keeps the identity of whoever originally shared it.

### Importing by hand, and checking this machine's own state (SPEC-140)

Before SPEC-140, `ImportFromShared` had exactly one caller: the hidden
`hooks run-import` subcommand the installed git hooks invoke. A machine that
cloned a repository with team memory already active — but had not yet run a
`git pull`/checkout after installing the hooks — had no visible way to pull
in what teammates had already shared. Two commands close that gap:

```bash
mneme team-memory import              # reads .mneme/shared/, EXECUTES by default
mneme team-memory import --dry-run    # preview without writing
mneme team-memory status              # is the vault present? are this machine's hooks installed?
mneme team-memory status --json
```

`mneme team-memory import` runs the same `ImportFromShared` the git hooks
use — same merge-by-`updated_at` strategy, same one-file-per-UUID id
preservation described above — but on demand, without waiting for the next
`git pull`. `--dry-run` reports what would be created/updated/skipped
without writing anything.

`mneme team-memory status` never writes anything: it reports whether
`.mneme/shared/` is present and whether THIS machine's own import hooks are
installed, naming `mneme team-memory hooks install` when they are not — the
missing half of team-memory's own diagnostic surface (SDD already has
`mneme sdd status`).

`mneme team-memory enable` itself only ever EXPORTS this machine's own
memories to the vault — it never imports a teammate's. When `enable` runs
against an already-active vault, it now also reports whether this machine
is still missing memories already present there, pointing at `mneme
team-memory import` — without removing or shortening anything it already
printed, including the privacy notice below, which keeps printing every
time regardless.

### SDD reference anchors (SPEC-128)

A note's frontmatter may carry `sdd_refs:` — one `REF=UUID` line per
`BL-<n>`/`SPEC-<n>` mention the memory's text carries and that resolved to
a real anchor on the machine that wrote it (e.g. `SPEC-125=<uuid>`).
Written last in the frontmatter and omitted entirely when the memory
anchors nothing, so a memory with no such mentions still produces a file
byte-identical to one written before this field existed. Import **forces**
whatever the file says onto the local row — it never re-derives the
anchors from the imported text — so the reference always means what it
meant on the machine that wrote it, even when the importing machine's own
`SPEC-125` names different work entirely (see `mem_get`'s `sdd_refs` field
and `mneme get`'s foreign-reference warning). An older mneme reading a note
with `sdd_refs:` simply ignores the field (forward compatibility).

## Profile provenance exclusion (SPEC-094 §4)

An [mneme profile](profiles.md) can inject its rules (commits/PRs/branches/
conventions) as `rule` memories into the active repository's database
(SPEC-092 §2, `SaveProfileRule`). Those rules carry a provenance stamp,
`source = "profile:<name>"`, distinguishing them from hand-authored
knowledge. Team-memory treats provenance as an invariant, systemic exclusion
from the shared vault — a profile's standard travels through **its own**
git repository (reviewed, versioned there), never through this project's
`.mneme/shared/` vault. The two channels are deliberately orthogonal:

| Channel | Carries | Shared via |
|---------|---------|------------|
| Profile repository | The team's methodology: rules, agents, skills, templates | The profile's own git remote (private, one PR to update) |
| Team-memory vault | This repo's accumulated knowledge: decisions, discoveries, bugfixes | `.mneme/shared/` in **this** project's git repository |

If a profile's rules leaked into the vault, two problems would follow: the
rule would live in two places at once (duplicated source of truth), and —
worse — a **profile switch** (`DeactivateProfileRules`, which hard-deletes
the rows by provenance) would leave the file still committed in
`.mneme/shared/notes/`, so the very next `git pull` would resurrect it as an
active rule via the import hook. This is the "rule zombie" bug the
exclusion closes.

**Write guard (never materializes):** both `bakeTeamMemoryFields` (the
`Save` bake step) and `materializeTeamMemory` (the single function that ever
writes to `vault.Writer`) check `model.IsProfileSource(m.Source)` — the bake
step forces `Shared=0` even against an explicit override, and materialize
early-returns unconditionally, independent of `Shared`. Two independent
checks, on purpose: either one failing does not let a profile rule slip
through.

**Promote guard (rejects elevation):** `mneme promote <id>` / `mem_promote`
refuses to elevate a profile-provenance memory to `shared=2`, returning
`ErrProfileMemoryNotShareable` before the row is touched — promoting one
would be an operator error, not a silent no-op.

**Read guard (anti-zombie, never resurrects):** `importSharedNote` skips any
note whose frontmatter carries `source: profile:*`, unconditionally — the
same pattern already used to skip the subagent manifest. This is
defense-in-depth against a repo that carries an orphaned profile note from a
pre-§4 state, or from a teammate running an older mneme: the note is never
imported as an active rule. `source` round-trips through the vault
frontmatter (`internal/vault/frontmatter.go`, `omitempty`) purely so this
guard can read it back.

**Non-regression:** a hand-authored memory (`source == ""`, the default for
everything saved before this field existed, and for every memory `mem_save`
creates) is unaffected by any of these guards — share-by-default (SPEC-071)
behaves exactly as before.

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
shared memory should never contain credentials or other secrets in the first
place — and note that share-by-default (SPEC-071) now materializes
`discovery`/`config`/`preference` too, so this discipline matters more than
before.

Global- and org-scoped memories are excluded from auto-sharing regardless of
type, and the two auto-generated / ephemeral project types (`session_summary`,
`synthesis`) are excluded as well — precisely so personal or throwaway notes
cannot accidentally leak into a committed, potentially-public file.

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
mneme team-memory import [--dry-run]      # import from the vault on demand (SPEC-140)
mneme team-memory status [--json]         # vault presence + this machine's hook state (SPEC-140)
mneme promote <id>                        # mark one memory as team-curated (shared=2)
```

## Test isolation (SPEC-085)

Team-memory's write-through path (constructor-time detection + synchronous
materialization on `Save`) is exactly the mechanism that, before SPEC-085,
let this repo's own test suite corrupt its own real database: any test whose
process cwd sat inside this dogfooding repo previously activated team-memory
automatically, materialized test fixtures to the real `.mneme/shared/notes/`,
and the `post-merge` hook re-imported them into the real
`~/.mneme/projects/wirvii-mneme.db` on the next pull — 7752 of 9058 rows were
test pollution before the cleanup.

The fix: `service.NewMemoryService` never resolves team-memory state itself
anymore. It defaults OFF and only activates when a caller opts in explicitly
via `service.WithTeamMemory(service.DetectTeamMemory())` — the one production
call site is `internal/cli.initService`. A test that needs to exercise real
detection (chdir into a fixture repo with a marker) must opt in the same
explicit way; see `newRepoTestService` in
`internal/service/teammemory_test.go` and the "Test isolation from the real
environment" section of `CLAUDE.md` for the full four-layer contract.

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
