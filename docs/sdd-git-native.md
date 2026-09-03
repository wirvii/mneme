# SDD git-native — the archive, the write, and the read (SPEC-130 §2a + SPEC-131 §2b)

> This mechanism was cut into three parts (BL-194, approved by the owner
> 2026-08-28):
>
> | Part | Where it lives | What it delivers |
> |---|---|---|
> | §2a — the file and the write | SPEC-130 | The format, write-through, enable/disable |
> | **§2b — the read** | **this document, SPEC-131** | The importer, git hooks, numbering from files |
> | §2c — the collision | BL-202 | Reconciling two items with the same correlative |
>
> **This document does not describe §2c.** Two machines that independently
> create the SAME correlative produce a collision the import below
> **detects and reports, but does not resolve** — reconciling is BL-202,
> still pending.

## Overview

mneme's backlog items and specs live in a local SQLite database. This
mechanism ADDS a second representation of the same data: plain Markdown
files, committed to the repository, under `.mneme/sdd/`. It exists for one
reason — a backlog item or a spec becomes something you can **review in a
pull request**, the same way you review a code change, instead of only being
visible through `mneme backlog get`/`spec status`.

It is **opt-in per repository** (D3), the same posture team-memory uses: the
presence of `<repo>/.mneme/sdd/.mneme-sdd` (a small, committed JSON marker)
is the only flag. **A repository that never runs `mneme sdd enable` is
completely unaffected — verifiably so**: with the marker absent, an entire
cycle of `mneme backlog add` → `refine` → `promote` → `spec advance` →
`pushback` → `resolve` leaves `git status --porcelain` exactly as it was.

## What this mechanism does — and its one remaining limit

This is not a caveat buried at the bottom — it is the single most important
thing to understand before enabling this mechanism:

- **Files DO read back into the database — on a machine that has the
  hooks installed.** If a teammate edits `.mneme/sdd/backlog/BL-050.md` in
  a pull request and merges it, that edit reaches YOUR database the next
  time you `git pull`/`git merge`/`git checkout`, in the background,
  without you doing anything else.
- **A git hook IS installed for this mechanism** — `mneme sdd enable
  --apply` installs it on the machine that turns the mechanism on;
  everyone else runs `mneme sdd hooks install` once, the same one-time
  step team-memory's own hooks already require.
- **A teammate's `git clone` gives them the mechanism already turned ON**
  (the marker is committed) — they only need `mneme sdd hooks install` to
  start receiving imports. Until they run it, `mneme sdd status` tells
  them so.
- **What is still unresolved: two people creating the SAME correlative at
  the same time.** The import **detects and reports** that collision — it
  never silently picks a winner — but does not yet **reconcile** it; that
  is BL-202.

`mneme sdd enable`'s own preview output says this explicitly, every time,
and so does the `mneme-init` onboarding skill's step for this mechanism —
it is not something you have to already know to avoid a surprise.

## Activation

```bash
mneme sdd enable            # preview: what would be exported, and why to be careful
mneme sdd enable --apply    # actually write it
```

Run from anywhere inside the git repository. Without `--apply`, NOTHING is
written — not even a probe file — the command only reports the plan (how
many backlog items and specs would be exported) and four warnings:

1. Publishing to git is not undone by deleting a later commit — the content
   stays reachable in the repository's history.
2. mneme cannot tell whether the remote is public or private without making
   a network call it deliberately never makes; it only shows you the remote
   git already knows about locally.
3. mneme has not scanned the content of these files for anything sensitive
   — that review is yours to do.
4. These files sync into a pull request today, AND into a teammate's own
   database on every `git pull`/checkout **once that machine has the hooks
   installed** (`mneme sdd hooks install`); two people creating the same
   correlative at the same time produce a collision this import detects and
   reports, but does not yet resolve (see the section above).

With `--apply`, the command:

1. Exports EVERY backlog item and spec (including archived items and
   already-completed specs, D8) as Markdown records.
2. Writes the marker `.mneme/sdd/.mneme-sdd` — **committed**, so anyone who
   clones the repository afterward has the mechanism turned on for them too,
   without doing anything themselves (the same "enabling is a team decision"
   posture as team-memory).
3. Adds `sdd.off` to `.mneme/.gitignore` (the entry `mneme sdd disable`
   later uses — see below).
4. Installs THIS machine's own git hooks (`post-merge`, `post-checkout`) —
   see "Reading: the importer and its hooks" below. A teammate who clones
   afterward only needs `mneme sdd hooks install` once.

Re-running `enable --apply` is idempotent: given the same database state, it
produces byte-identical files, so a second run leaves `git status
--porcelain` unchanged.

**Refuses, before writing a single byte**, when the repository already
carries SDD records this database cannot make sense of — an unreadable
file, or one anchored to a different machine's item (see "Convergence"
below). The refusal names the offending files and says to run `mneme sdd
import` first; a genuine correlative collision is still BL-202.

### Already enabled, new machine (SPEC-140)

The refusal above is reserved for a repository whose `.mneme/sdd/.mneme-sdd`
marker does not exist yet — a genuine first activation. A repository that
was already activated by someone on the team, cloned onto a machine that has
never run `mneme sdd import`, is a DIFFERENT, ordinary situation: `mneme sdd
enable` (without `--apply`) detects the committed marker and, instead of the
plan/warnings above, reports what THIS machine is missing:

```
$ mneme sdd enable
SDD is already enabled for this team (since 2026-09-01T00:00:00Z).
This machine is missing:
  - this machine's git hooks are not installed — run `mneme sdd hooks install`
  - 3 record(s) committed to this repository are unknown to this machine's local database — run `mneme sdd import`
```

Records this machine's local database does not recognize are the ordinary
state of a fresh clone, never a rejection here — running the two named
commands is all that is needed. `mneme sdd enable --apply` is completely
unaffected by this: with `--apply`, the command behaves exactly as documented
above in every case, marker present or not — this is strictly a change to
what the plain preview reports.

## Exporting (repair)

```bash
mneme sdd export
```

Re-materializes every backlog item and spec from the current database state
— the idempotent repair path for a file that was deleted or hand-edited
into something wrong. Requires the mechanism to already be enabled (this
repairs an enabled repository; it is not a second way to turn one on), and
applies the same convergence guard `enable` does.

## Reading: the importer and its hooks

```bash
mneme sdd import               # reads .mneme/sdd/, EXECUTES by default
mneme sdd import --dry-run     # preview without writing
mneme sdd hooks install        # install this machine's own git hooks
mneme sdd hooks remove         # remove only the SDD block (team-memory's own survives)
```

`mneme sdd import` walks the ENTIRE `.mneme/sdd/` directory every time —
not just what git reports as changed — and creates or updates the local
database accordingly. It executes by default (D13 already guarantees
nothing under `.mneme/sdd/` is ever deleted by this mechanism, so there is
nothing a preview protects here that `--dry-run` does not already cover),
and exits **1** when anything was skipped (a broken file, a record with no
title, or a genuine collision) — **0** otherwise. The exact same read path
runs automatically, in the background, after every `git pull`/merge/
checkout once `mneme sdd hooks install` has run — `mneme sdd hooks
run-import` (hidden, invoked by the hook itself) is identical except it
**always exits 0**, skips silently during a rebase/merge/cherry-pick in
progress, and logs its own outcome to `~/.mneme/sdd-hooks.log` (a record of
what happened, never a source of truth — `mneme sdd status` never reads it,
and deleting it changes no answer).

**Decides by ANCHOR, never by correlative.** Every record carries its own
permanent identity (a UUIDv7 anchor, the same one `mem_get`'s `sdd_refs`
already resolves against) — importing by anchor is what makes a `BL-050.md`
a teammate wrote update the SAME row your own `BL-050` already is, instead
of silently overwriting it with theirs (or vice versa) just because the
correlative matches. Three outcomes:

- **The anchor is new here** → the record is created, minting an anchor if
  the file does not bring one (a hand-authored file needs only a `title`
  and a description — everything else mneme fills in and, if anything was
  missing, rewrites the file to show it — this is the ONLY thing this
  mechanism ever writes back to a file on its own).
- **The anchor matches what this correlative already holds** → the record
  is updated. Children (refinements, spec history, pushbacks) are MERGED by
  their own key, never replaced wholesale — a refinement written locally
  and not yet committed survives an import that does not mention it.
- **The correlative is already claimed by a DIFFERENT anchor** → the record
  is **skipped and reported**, never overwritten. This is the collision
  BL-202 will one day reconcile; today it is made visible, never resolved.
  Two people creating the same correlative at the same time is normal on a
  git-native mechanism — this is where mneme is honest about the limit
  instead of guessing.

**The importer never compares timestamps.** Every SDD write already passed
through a file before reaching here, so a genuinely conflicting local edit
would already have produced a merge conflict in git before an import ever
runs — the file the importer just read is always the current word. (Compare
with team-memory's own shared-vault importer, which DOES compare
`updated_at`: a memory's local row can carry edits that never passed
through a file at all, so there timestamp is the only available arbiter.)

**A frozen spec never moves, even if the file says otherwise.** A spec
whose originating backlog item was archived (SPEC-125) cannot change status
again by design — there is no unarchive. If an incoming file brings a
DIFFERENT status for such a spec, that one field is skipped and reported;
every other field on that spec still updates normally.

**Numbering**, once the mechanism is on, becomes the LARGER of the
database's own next id and one past whatever correlative a file under
`.mneme/sdd/` already reserves — a teammate's committed-but-not-yet-imported
`BL-205.md` reserves `BL-205` for everyone the moment it exists, not only
once someone runs `mneme sdd import`. A repository that never enabled this
mechanism keeps computing the next id exactly as it always did.

## Disabling — locally, never for the team

```bash
mneme sdd disable            # preview
mneme sdd disable --apply    # actually write it
```

This is deliberately **local only** (D3/D19): it never touches the
committed marker, so every other teammate's clone keeps the mechanism on.

`--apply` does three things, in this exact order: (1) imports once more, so
anything a teammate already published and this machine has not yet read is
not lost; (2) writes `.mneme/sdd.off` — a file `.mneme/.gitignore` already
excludes, so it never gets committed by accident — after which this
machine's own writes to the SDD mechanism become inert; (3) removes this
machine's own git hooks for the mechanism.

**`mneme sdd disable` never deletes anything under `.mneme/sdd/`.** If you
want those files out of the repository, that is a separate, explicit
decision of your own — `git rm -r .mneme/sdd` — mneme never makes it for
you.

## Status

```bash
mneme sdd status
mneme sdd status --json
```

Reports whether the mechanism is on or off, how many backlog items/specs
the database has, what git reports as pending under `.mneme/sdd`, and —
without refusing anything, unlike `enable`/`export` — which files (if any)
are broken or carry an anchor this database does not recognize.

**A row this database could read the identity of but not the content of
(SPEC-133) is a separate, narrower case from a broken FILE above**: the
backlog/spec count stays exact (it is a plain SQL `COUNT(*)`), but that one
row is missing from whatever this command lists, and `enable`/`export`
never materialize a file for it. `status`, `enable`, and `export` all name
it instead of silently shrinking a total or a list — the same tolerance
`backlog list`/`spec list`/`lane stats` already apply, extended to this
mechanism's own three commands.

## Why the filename is the correlative

Every record's file (or, for a spec, its directory) is named by its
human-readable correlative — `.mneme/sdd/backlog/BL-050.md`,
`.mneme/sdd/specs/SPEC-130/record.md` — never by its UUID.

This is a **deliberate inversion** of `internal/vault`'s own naming rule
(SPEC-053 D1). There, a shared memory is named by its UUID
(`notes/<uuid>.md`) so that two teammates who each create a new memory at
the same moment can NEVER collide — their files simply land at different
paths, and git never even has to notice. Here, the opposite property is
what matters: if two machines each independently create a "BL-050" — one
because they never synced, one because a network partition let both number
forward from the same base — that collision needs to become VISIBLE the
moment their branches meet, not silently resolved by two files quietly
coexisting under different names. Naming by correlative is what makes git
itself flag the collision as a merge conflict on the SAME path, instead of
two harmless-looking new files.

This same explanation lives in two other places, deliberately, so a reader
who is only looking at one of them still finds it: the godoc of
`internal/sddfile.BacklogPath`/`SpecRecordPath`, and a cross-referencing
sentence added to `internal/vault.UUIDPath`'s own godoc. The concern this
guards against is someone reading `vault.UUIDPath` in isolation and
"fixing" the SDD mechanism to match it — which would be exactly backwards.

## File format

A record has three parts: a frontmatter block (`schema`, `kind`, `id`,
`uuid`, `title`, and the rest of the item/spec's own fields — see
`internal/sddfile`'s package doc for the exact field list), a body (the
description, for a backlog item; always empty for a spec, which has none),
and zero or more marked sections (refinements for a backlog item; history
entries and pushbacks, each with its own questions and resolution, for a
spec).

Two properties make this format safe to hand-edit and safe to regenerate:

- **The schema is a hard, range-checked gate.** A record whose `schema` is
  higher than what this mneme understands is refused outright — never
  parsed partially and never silently stripped of a section it doesn't
  recognize, which would otherwise be silent data loss on the next
  rewrite.
- **Any content that happens to look like a section marker is escaped, and
  the escape is verified.** A description that literally contains the text
  `<!-- mneme:refinement ... -->` — which genuinely happens in this
  repository's own backlog — is written with a leading backslash, and every
  write re-parses its own output and refuses to return anything at all if
  what it would produce does not match the record it started from.

## Compatibility

A repository that has never run `mneme sdd enable` is unaffected in every
way that matters: no file is written, the next backlog/spec correlative is
computed exactly the same way it always was, and the store's own read paths
behave identically. **Once enabled**, the next correlative becomes the
larger of the database's own answer and one past whatever a file already
reserves (a teammate's not-yet-imported `BL-205.md` counts) — see "Reading:
the importer and its hooks" below.

## See also

- BL-202 — the collision: reconciling two items with the same correlative
  (still pending; the import below detects it and reports it, never
  resolves it).
- `docs/team-memory.md` — the sibling git-native mechanism this one borrows
  its opt-in/marker/preview-then-apply shape from, for a different kind of
  content (memories, not backlog/specs) and a different naming rule (see
  "Why the filename is the correlative" above).
