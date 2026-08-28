# SDD git-native — the archive and the write (SPEC-130 §2a)

> SPEC-130 is titled "Etapa 2" because `mneme spec` has no verb to rename a
> spec, but its actual scope is **only the first of three parts** the etapa
> was cut into (BL-194, approved by the owner 2026-08-28):
>
> | Part | Where it lives | What it delivers |
> |---|---|---|
> | **§2a — the file and the write** | **this document** | The format, write-through, enable/disable |
> | §2b — the read | BL-201 | The importer, git hooks, numbering from files |
> | §2c — the collision | BL-202 | Reconciling two items with the same correlative |
>
> **This document does not describe §2b or §2c.** Nothing in this repository
> today imports a file back into the database, installs a git hook for the
> SDD mechanism, or reconciles a numbering collision. If that is what you
> came looking for, it does not exist yet — see BL-201/BL-202.

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

## What this part does NOT do yet

This is not a caveat buried at the bottom — it is the single most important
thing to understand before enabling this mechanism:

- **Nothing reads these files back into the database.** If a teammate edits
  `.mneme/sdd/backlog/BL-050.md` in a pull request and merges it, that edit
  has no effect on anyone's database until BL-201 exists.
- **No git hook is installed for this mechanism.** Nothing runs
  automatically after `git pull`/`git merge`/`git checkout` — contrast with
  team-memory's `post-merge`/`post-checkout` hooks, which DO exist today for
  shared memories, but not for this.
- **A teammate's `git clone` does not give them your backlog and specs in
  their own local database.** They will SEE the files (once you push and
  they pull) — that is the whole point of §2a — but their own `mneme
  backlog list` will not include your items until BL-201 lands.

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
4. Today these files exist to be reviewed in a pull request, not yet to
   sync into a teammate's own database (see the section above).

With `--apply`, the command:

1. Exports EVERY backlog item and spec (including archived items and
   already-completed specs, D8) as Markdown records.
2. Writes the marker `.mneme/sdd/.mneme-sdd` — **committed**, so anyone who
   clones the repository afterward has the mechanism turned on for them too,
   without doing anything themselves (the same "enabling is a team decision"
   posture as team-memory).
3. Adds `sdd.off` to `.mneme/.gitignore` (the entry `mneme sdd disable`
   later uses — see below).

Re-running `enable --apply` is idempotent: given the same database state, it
produces byte-identical files, so a second run leaves `git status
--porcelain` unchanged.

**Refuses, before writing a single byte**, when the repository already
carries SDD records this database cannot make sense of — an unreadable
file, or one anchored to a different machine's item (see "Convergence"
below). The refusal names the offending files and points at BL-201/BL-202.

## Exporting (repair)

```bash
mneme sdd export
```

Re-materializes every backlog item and spec from the current database state
— the idempotent repair path for a file that was deleted or hand-edited
into something wrong. Requires the mechanism to already be enabled (this
repairs an enabled repository; it is not a second way to turn one on), and
applies the same convergence guard `enable` does.

## Disabling — locally, never for the team

```bash
mneme sdd disable            # preview
mneme sdd disable --apply    # actually write it
```

This is deliberately **local only** (D3/D19): it never touches the
committed marker, so every other teammate's clone keeps the mechanism on.
`--apply` writes `.mneme/sdd.off` — a file `.mneme/.gitignore` already
excludes, so it never gets committed by accident — after which this
machine's own writes to the SDD mechanism become inert.

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
computed exactly the same way it always was, and the store's own read
paths behave identically. Enabling this mechanism adds a second, reviewable
representation of data that already exists — it does not change what that
data means or how it is numbered.

## See also

- BL-201 — the read: importing files, git hooks, numbering from files.
- BL-202 — the collision: reconciling two items with the same correlative.
- `docs/team-memory.md` — the sibling git-native mechanism this one borrows
  its opt-in/marker/preview-then-apply shape from, for a different kind of
  content (memories, not backlog/specs) and a different naming rule (see
  "Why the filename is the correlative" above).
