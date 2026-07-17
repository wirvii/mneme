# Profiles §1–§2: manifest/pin/store, activation, lockfile, provenance

> SPEC-091 (§1) + SPEC-092 (§2), 2 of 7 specs in the EPIC `profiles`. Design
> reference: `docs/profiles-design.md` (§1–§4, §7, §9, §11, §12, §16.2,
> decisions #2/#4/#5/#7/#8/#9/#10/#11/#12/#13). This document covers §1 (the
> foundation: manifest, pin, host-level store, read-only pin resolution) and
> §2 (the activation engine: hybrid materialization, `.mneme/profile.lock`,
> `source=profile:<name>` provenance, switch). The `use`/`default` verbs,
> precedence, SessionStart integration, team-memory exclusion, assisted
> creation, and scaffolding are later specs (§3–§7); do not expect them here.

## Overview

A **profile** is a team's working methodology — agents, skills, rules,
templates — packaged as a portable git repository, activated with **nvm**
semantics: a host-level store installed once, plus per-project pointers with
precedence. The engine is OSS; the *content* of a private team's profile
lives in their own git repository.

§1 lays the foundation three formats/mechanisms every later spec in the EPIC
builds on:

1. **The manifest** (`mneme-profile.toml`) — a profile's identity. Lives at
   the root of the profile's own git repository.
2. **The pin** (`.mneme-profile`) — a committed, self-describing pointer at
   the root of a project's repository. Analogous to `.nvmrc`/`package.json`'s
   `engines` field.
3. **The store** (`~/.mneme/profiles/<name>/`) — each profile cloned exactly
   once, shared by every project on the host.
4. **Pin resolution** — reading the pin and cross-referencing it against the
   store to report one of four states. Read-only: §1 never writes a pin and
   never materializes anything to a project's disk.

## The manifest — `mneme-profile.toml`

Lives at the root of the **profile's own repository**.

```toml
name        = "chatea-pro"                 # required; safe-slug (^[a-z0-9][a-z0-9-]*$)
version     = "3.1.0"                      # required; string (semver recommended, not enforced)
description = "Chatea Pro's methodology"   # optional

[compat]
mneme = ">=1.28.0"                         # optional; version constraint

# extends = "base-profile"                 # RESERVED — parsed but NOT implemented (decision #5)
```

- `name`/`version` missing, or `name` failing the safe-slug check, fail
  `Manifest.Validate()` with the sentinel `profile.ErrInvalidManifest`
  (translated to `model.ErrInvalidProfile` by `ProfileService`).
- `extends` is parsed and preserved for forward-compat but never acted upon —
  `Manifest.Warnings()` reports it as an advisory, never an error. Profile
  inheritance is YAGNI in v1.
- `Manifest.CheckCompat(mnemeVersion)` evaluates `[compat].mneme` with a
  minimal single-operator (`>=`/`>`/`<=`/`<`/`=`) semver comparison. It is
  purely informative: `profile add`/`list` show a warning on mismatch, never
  block. A full range-constraint engine is a follow-up if the pain justifies it.

## The pin — `.mneme-profile`

Lives at the root of a **project's repository**, **committed** to git. No
file extension (like `.nvmrc`) but the content is TOML (unlike `.nvmrc`'s
plain text) for richer, self-describing fields.

```toml
name     = "chatea-pro"                                    # required; safe-slug
source   = "git@github.com:chateapro/mneme-profile.git"    # optional; no source => internal default profile
ref      = "v3"                                            # optional; pinned tag/branch/commit
scaffold = "saas-multitenant"                              # optional; only set by /new-project (§7)
```

- `name` missing, or failing the safe-slug check (`^[a-z0-9][a-z0-9-]*$`) —
  this is the primary anti-path-traversal defense, since `name` feeds a
  lookup under the host-level store — fails `Pin.Validate()` with the
  sentinel `profile.ErrInvalidPin`.
- `source` present without `ref` is not an error — `Pin.Warnings()` reports
  it: the pin will always resolve to whatever HEAD of the default branch
  happens to be, which is not reproducible.
- `scaffold` is parsed and preserved but not acted upon in §1 (scaffolding is
  §7).
- **§1 never writes this file.** The verbs that write a project's pin
  (`profile use`/`profile default`) are §3.

## The store — `~/.mneme/profiles/<name>/`

Resolved via `Config.ProfilesDir()` (`<DataDir>/profiles`, no new TOML field
or env var). Each profile is a plain git checkout.

```bash
mneme profile add <git-url> [--name N] [--ref R] [--force]
mneme profile update [<name>] [--ref R]
mneme profile list
```

- **`add`** clones `source` into a temp directory **inside** `profilesDir`
  (not the OS-wide temp dir, so the final `os.Rename` is always
  same-filesystem and therefore atomic — an interrupted clone never leaves a
  partial directory at the destination), reads and validates its manifest,
  and only then renames it into place. The clone is full (not `--depth=1`) so
  any tag or commit named by `--ref` is reachable. The name is derived from
  the manifest unless `--name` is passed explicitly, in which case it must
  match — a mismatch fails with `ErrProfileNameMismatch` so a store directory
  can never disagree with the content it actually holds. Re-running `add`
  against an already-installed name fails with `ErrProfileExists` unless
  `--force` is passed.
- **`update`** fetches (`--tags --prune`), checks out the given `--ref` (or,
  with none given, keeps whatever ref is checked out), and pulls
  fast-forward-only when the profile is currently on a branch. Fails with
  `ErrProfileNotFound` when the name is not in the store.
- **`list`** enumerates every directory in the store. A directory without a
  valid manifest is reported `invalid` with its error — it never breaks the
  rest of the listing.

### Git execution and credential prompts (R1)

`profile add`/`update` shell out to the real `git` binary. The **CLI**
frontend allows git's normal interactive credential prompt (a developer is
present at a terminal — design decision #11). The **MCP** frontend always
sets `GIT_TERMINAL_PROMPT=0` on the git subprocess's environment: an
unattended agent session must never hang forever waiting on a prompt no human
will ever see. When a private repository requires credentials MCP can't
supply, the call fails fast with the git error plus a hint pointing the
caller at running `mneme profile add <url>` from an interactive terminal
instead.

## Pin resolution — `mneme profile status`

```bash
mneme profile status [--json]
```

Read-only. Reads `.mneme-profile` at the current repository's root and
cross-references it against the store, reporting one of four states:

| State | Meaning |
|-------|---------|
| `absent` | No `.mneme-profile` in this repo at all. |
| `default` | Pin present, no `source` — this project uses mneme's internal default profile. |
| `installed` | Pin has a `source`, and the profile is present and valid in the store. |
| `missing` | Pin has a `source`, but the profile is **not** in the store yet — run `profile add`. |

`missing` is exactly the signal a later spec (§3, SessionStart) turns into an
actionable nudge/gate ("this repo uses `chatea-pro@v3`, not installed, install
it?"). §1 only reports it.

`mneme profile list` marks which installed profile (if any) matches the
current repo's pin.

## Architecture

```
cli/profile.go   mcp/handlers_profile.go
        │
        ▼
service/profile.go + service/profile_activate.go   (ProfileService)
        │       │                    │
        │       ▼                    ▼
        │  *MemoryService       *SubagentService
        │  (insert/purge         (ReadProfile: capa-2/3
        │   provenance-marked     for agent fusion;
        │   rules)                WriteAgentProfiles:
        │                         atomic agent writes)
        ▼
internal/profile   (leaf: Manifest, Pin, Store, ResolvePin, Contents,
                     LoadContents, Lock, ParseLock/RenderLock, Snapshot,
                     StalenessAgainst — stdlib + go-toml/v2 only)
internal/managedblock (leaf: Upsert/Read/RemoveText/Remove — stdlib only)
```

- `internal/profile` is a **leaf package**: it imports only the standard
  library plus `github.com/pelletier/go-toml/v2` — never `internal/model`,
  `internal/store`, `internal/service`, or any other `internal/*` package.
  Same perimeter as `internal/skill`, `internal/conflicts`, `internal/subagents`.
  A dedicated import-guard test enforces this and automatically covers §2's
  additions (`contents.go`, `lock.go`, `staleness.go`) — no separate guard
  needed.
- `service.ProfileService` is the only thing `cli`/`mcp` ever call — it wraps
  the leaf's `profile.Store` and translates its sentinel errors
  (`profile.ErrProfileExists`, `profile.ErrProfileNotFound`,
  `profile.ErrProfileNameMismatch`, `profile.ErrInvalidManifest`,
  `profile.ErrInvalidPin`) into their `model.Err*` equivalents, the same
  translation posture `SkillsService`/`ConflictsService` already establish
  for their own leaves.
- §1 needed **no database access**. §2 cables the seam §1 left documented and
  dormant: `ProfileService` optionally holds a `*MemoryService` (to
  insert/purge provenance-marked rule memories) and a `*SubagentService` (to
  read a project's capa-2/3 for the agent-fusion step). Both are wired via a
  **functional-option pattern** — `NewProfileService(profilesDir, noPrompt,
  opts ...ProfileOption)` with `WithProfileMemoryService`/
  `WithProfileSubagentService`/`WithProfileSkillsDir` — mirroring
  `MemoryService`'s own `Option` pattern (`service.WithTeamMemory`). This
  means every existing `NewProfileService` call site (`cli/profile.go`,
  `mcp/handlers.go`) compiles and behaves **unchanged**: they only ever call
  `Add`/`Update`/`List`/`ResolvePin` (the §1 surface), which need neither
  seam. `Activate`/`Switch`/`Deactivate` return
  `model.ErrProfileServiceNotConfigured` if called on a `ProfileService`
  missing the memory/subagent seam. §2 adds **no new CLI/MCP verbs** — the
  engine is exercised at the service+leaf test level; wiring an actual
  `profile use`/`profile default`/SessionStart caller with these options is
  §3's job.
- The materialization step deliberately writes files **in the service
  layer** (`os.WriteFile`/`os.MkdirAll`/`filepath.WalkDir`), the same
  precedent `SubagentService.WriteAgentProfiles` already established —
  `internal/service` never imports `internal/install`. Agent writes reuse
  `WriteAgentProfiles`'s existing atomic batch-write-with-rollback as-is;
  skill copying and the CLAUDE.md block upsert are new, self-contained
  service-layer code (`copyDir`, `managedblock.Upsert`).

## MCP tools (65→69, unchanged by §2)

§2 adds no new CLI/MCP verbs — `Activate`/`Switch`/`Deactivate`/
`DetectStaleness`/`ActiveLock` are `ProfileService` methods consumed by a
later spec (§3: `use`/`default`/SessionStart). The tool count below is
unchanged from §1.

| Tool | Params | Returns |
|------|--------|---------|
| `profile_add` | `{source, name?, ref?, force?}` | `AddResult` (name/version/ref/path) |
| `profile_update` | `{name?, ref?}` | `UpdateResult` (name/old_ref/new_ref/version) |
| `profile_list` | `{}` | `[]ProfileInfo` |
| `profile_status` | `{project_root?}` | `Resolution` (state/pin/manifest/path) |

## HTTP: no endpoints (decision, AC12)

HTTP does not get profile endpoints in §1, and the endpoint count (8) is
unchanged. `profile add`/`update` are local-host operations that clone into
*this machine's* `~/.mneme/profiles` and depend on the developer's own
interactive git credentials — a REST endpoint on a shared server has no clean
semantics for "clone a private repo on behalf of a remote caller." Profiles
also activate at SessionStart (§3), a CLI/agent-session lifecycle, not an
HTTP server's. Consistent with the existing "HTTP lacks SDD tools" precedent;
re-evaluated if a genuine server-side use case appears.

## §2: Activation, lockfile, and provenance

Claude Code has no `PATH`-style indirection the way `nvm` does, so
"activating" a profile is **hybrid** (`docs/profiles-design.md` §7):

- **Repunta runtime** — what mneme resolves in memory: rules are inserted
  into the project's memory database (project-scoped); models/policy/
  templates are left resoluble from the profile's own store directory
  (registered on the lock via `profile`+`commit`) — their runtime *consumption*
  by each subsystem (scoring, lanes, `spec_doc_write`) is a later follow-up,
  not §2.
- **Materializa file-based** — what Claude Code needs as real files: `agents/`
  → `.claude/agents/`, `skills/` → `~/.claude/skills/`, `blocks/` → a single
  managed block in `CLAUDE.md`.

### The profile store's layout

Every piece under a profile's directory (`<profilesDir>/<name>/`) is
**optional** — `profile.LoadContents` parses whichever of these exist and
never errors on a minimal profile (manifest only):

```
<profilesDir>/<name>/
├── mneme-profile.toml     # required (§1)
├── agents/
│   ├── backend.md         # capa-1: already fully composed (frontmatter + tools + …)
│   └── architect.md
├── skills/
│   └── new-project/       # a full skill directory, copied as-is
├── blocks/
│   └── profile.md         # concatenated into ONE "profile" managed block in CLAUDE.md
├── rules.jsonl            # one JSON RuleSpec per line
├── models.toml            # path recorded on the lock; NOT parsed/consumed by §2
├── policy.toml            # path recorded on the lock; NOT parsed/consumed by §2
└── templates/             # dir path recorded on the lock; NOT parsed/consumed by §2
```

`rules.jsonl` — one line per rule, each mapped to a `model.SaveRequest` only
at the service boundary (the leaf's `RuleSpec` never imports `internal/model`):

```json
{"title": "No CGO", "content": "Pure Go, no CGO, no build tags.", "applies_to": ["**"], "severity": "warn"}
```

A malformed line fails with the offending 1-indexed line number
(`rules.jsonl line N: ...`); `severity` outside `info`/`warn`/`block` or an
empty `applies_to` both fail `RuleSpec.Validate()` (`profile.ErrInvalidRuleSpec`).

### Provenance — `source=profile:<name>`

Migration `015_memory_source_provenance.sql` mirrors SPEC-061's `shared`/
`author` migration exactly: `ALTER TABLE memories ADD COLUMN source TEXT NOT
NULL DEFAULT ''`. A non-indexed column — no trigger touches `memories_fts`,
so the pure-Go `-tags`-free FTS5 invariant is untouched. `model.Memory.Source`
round-trips through every SELECT site (24 columns now, up from 23); a memory
saved before this column existed (or by the public `mem_save` path) always
resolves `source=""`.

**Only the activation path can stamp it.** `MemoryService.SaveProfileRule`
is a dedicated method — never exposed by the `mem_save` MCP tool — that
forces `Type=rule`, `Scope=project`, `Shared=0`, and
`Source="profile:<name>"` before delegating to the normal `Save` pipeline
(validation, upsert, embedding, wikilinks — the exact same path every other
memory takes). `model.SaveRequest.Source` is `json:"-"`: even a raw JSON
payload with a `"source"` key can never populate it via `json.Unmarshal`,
so an agent calling `mem_save` directly has no way to forge provenance —
this is a *structural* guarantee (the field literally cannot be
unmarshaled), not merely an undocumented-schema convention.

### Hard delete, not `Forget`, not soft-delete

The original design (§12 of `docs/profiles-design.md`) said "deactivating a
profile's rules reuses `Forget`". Two things surfaced that as unworkable
during SPEC-092's design:

1. **`Forget` doesn't remove anything.** It only sets `decay_rate=1.0`
   (`MemoryStore.SetDecayRate`) — it never touches `deleted_at`. Meanwhile
   `loadActiveRules` (the function that injects rules into `mem_context`)
   filters on `deleted_at IS NULL`, not importance. A "forgotten" rule keeps
   being injected forever.
2. **Soft-delete leaves a tombstone.** `MemoryStore.SoftDelete` would exclude
   the rule from `loadActiveRules`, but the row survives with `deleted_at`
   set — recoverable, auditable. The owner's call: a profile's rules are
   **derived, regenerable** state — the profile's own git repository is the
   source of truth, not the SQLite row. Re-activating the profile
   re-materializes them from scratch. There is nothing worth keeping a
   tombstone for.

So deactivation/switch uses a genuine **hard delete, scoped by provenance**:

```go
// store — sibling of HardDelete (which purges by retention/deleted_at age);
// this one is scoped by source, not age, and needs no prior soft-delete.
func (s *MemoryStore) HardDeleteBySource(ctx context.Context, project, source string) ([]string, error)

// service — the only caller PurgeProfileRules needs
func (svc *MemoryService) PurgeProfileRules(ctx context.Context, project, profileName string) ([]string, error)
```

`HardDeleteBySource` collects matching ids and issues the `DELETE` inside one
transaction. The `DELETE` runs through the **existing** `memories_ad AFTER
DELETE` trigger (migration 001) — the same FTS5-consistency path
`HardDelete`'s retention purge already relies on, so no new trigger/migration
is needed and the FTS5 index never drifts.

**`Forget`/`SoftDelete` remain exclusively for hand-authored memories**
(`source=""`) — they are untouched by §2 and keep their existing recoverable
semantics. `PurgeProfileRules`/`HardDeleteBySource` are exclusively for
`source="profile:*"` rows. Mixing the two up is exactly the bug this design
avoids.

### `.mneme/profile.lock` — the "receipt"

Gitignored (machine-local materialization state, same lesson SPEC-089
learned about the subagent manifest — never commit machine-local paths),
TOML, absolute paths (correct here precisely *because* it never travels).
This is a **code-level guarantee, not a documentation aspiration**: every
`Activate` call ensures a scoped `<repoRoot>/.mneme/.gitignore` exists
containing exactly the line `profile.lock` (`writeLock` →
`ensureLockGitignore`, idempotent, preserves any pre-existing hand-authored
content in that file). The entry is deliberately scoped to the lock file
alone, **not** a blanket `.mneme/` ignore — `.mneme/shared/` (the
team-memory vault, SPEC-053) must stay trackable once a repo opts into
team-memory, and the pin `.mneme-profile` lives at the repo root, outside
`.mneme/` entirely, so neither is ever affected by this entry. This repo's
own root `.gitignore` additionally carries a `.mneme/profile.lock` line for
the same reason, belt-and-suspenders.

```toml
# .mneme/profile.lock
schema_version = 1
profile        = "chatea-pro"
source         = "git@github.com:chateapro/mneme-profile.git"
ref            = "v3"
commit         = "a1b2c3d4e5f6..."
activated_at   = "2026-07-16T23:00:00Z"

[[artifact]]
kind = "agent"
path = "/Users/dev/repo/.claude/agents/backend.md"

[[artifact]]
kind = "skill"
path = "/Users/dev/.claude/skills/new-project"

[[artifact]]
kind   = "block"
path   = "/Users/dev/repo/CLAUDE.md"
marker = "profile"

[[rule]]
id     = "019f6d2a-5489-7fa4-a7e9-021dc73fe1b5"
source = "profile:chatea-pro"
```

The `[[rule]]` ids are an **audit cross-check only** — never the deletion
key. The deletion key is always `source`, so a rule that somehow drifted out
of the lock (partial failure, manual edit) still gets purged by
`PurgeProfileRules` on the next deactivate/switch.

### Activation algorithm — `ProfileService.Activate`

1. Resolve `<profilesDir>/<name>` (`profile.Store.ProfilePath`, safe-slug
   validated) → `profile.ErrProfileNotFound` if missing.
2. `profile.LoadContents(dir)`.
3. `SubagentService.ReadProfile` — the repo's capa-2/3 project profile, or
   `nil` if `mneme-init` hasn't run yet.
4. **Agents**: fuse each `agents/<role>.md` (see below) and write via
   `SubagentService.WriteAgentProfiles` — reusing its existing atomic
   batch-write-with-rollback, not reimplementing it.
5. **Skills**: copy each `skills/<name>/` to the configured skills directory,
   skipping any whose already-installed `SKILL.md` has `pinned: true` — same
   semantics `install.WriteSkills` already establishes.
6. **Blocks**: concatenate every `blocks/*.md` into one `"profile"` managed
   block upserted into `<repoRoot>/CLAUDE.md` (`internal/managedblock`).
7. **Rules**: `SaveProfileRule` for every `rules.jsonl` entry.
8. Write `.mneme/profile.lock` (atomic temp-file + rename) recording every
   artifact and rule id above, plus `profile`/`source`/`ref`/`commit`/
   `activated_at`.

No 2-phase-commit across the filesystem and the database is attempted — the
lock itself is the recovery mechanism: if a later step fails, the lock
reflects whatever completed, and a re-`Activate`/`Switch` recovers.

### Agent fusion — capa-1 (profile) + capa-2/3 (repo)

A profile's `agents/<role>.md` is **already a fully composed capa-1** — the
profile author wrote its frontmatter, tools, and permission envelope
directly (unlike `mneme-init`'s grill, which generates capa-1 from a
built-in archetype via `subagents.Compose`). Fusing in the repo's capa-2/3
(when `SubagentService.ReadProfile` returns something) is therefore a
**different, self-contained assembly** (`ProfileService.fuseAgent`,
`internal/service/profile_activate.go`) — not a call into
`subagents.Compose`, which assumes the archetype-generates-capa-1 shape §2
doesn't have.

`fuseAgent` renders the repo's org/commit-convention/stack/layout/
cross-cutting-rules facts plus the app→role mapping entries assigned to that
specific role as a `"## Contexto del proyecto"` section, then wraps it in the
same untrusted-data envelope `subagent_compose` already uses for
grill-provided content (`subagents.GrillContentWrapStart`/`GrillContentWrapEnd`
— the single already-exported source of truth `ExtractGrillRegion` also
reads) after escaping any literal `<!-- mneme:` or wrap-delimiter sequence.
This introduces **no change to `internal/subagents`'s public contract** —
the escape/wrap logic is a new, local reimplementation in `internal/service`,
not an export from the leaf. When the repo has no capa-2/3 yet, `fuseAgent`
degrades cleanly to capa-1 alone (the repo simply hasn't run `mneme-init`;
§5 wires the grill's capa-2/3 authoring later).

### Switch A→B — `ProfileService.Switch`

1. Read A's lock (`ActiveLock`). Absent → `Switch` degrades to a plain
   `Activate(B)`.
2. `Deactivate(A)`: remove every artifact A's lock lists (`os.Remove` for
   agent files, `os.RemoveAll` for skill directories,
   `managedblock.Remove(path, "profile")` for the block — removing *only*
   the marked region, never surrounding `CLAUDE.md` prose or a different
   marker's block) and `PurgeProfileRules(A)` (hard delete by provenance).
3. `Activate(B)` — materializes B, inserts B's rules, writes a fresh lock
   (overwriting A's).

**Invariant:** the switch only ever touches what A's own lock lists, plus
rows carrying A's exact provenance stamp. Hand-authored agent files (never in
any lock), rules without a `profile:*` source, and `CLAUDE.md` prose outside
the `"profile"` block are structurally invisible to it — there is no code
path that could reach them.

### Staleness detection — same-repo race

Two sessions on the same repo, one runs `Switch`: the other must **notice
and stop**, never silently re-materialize on top of a mid-flight or
already-changed workspace (`docs/profiles-design.md` §8/§9).

```go
func (l Lock) StalenessAgainst(cached Snapshot) (stale bool, msg string)
func (s *ProfileService) DetectStaleness(repoRoot string, cached profile.Snapshot) (stale bool, msg string, err error)
```

`Snapshot{Profile, Commit, ActivatedAt}` is what a caller caches right after
its own `Activate`/`Switch` call. `DetectStaleness` re-reads the lock from
disk and compares — any difference in `Profile`, `Commit`, or `ActivatedAt`
means some other activity changed the workspace's active profile since the
snapshot was taken. §2 only detects and reports; **when** to call this
(SessionStart, or before every mneme operation) and how to surface the
message are §3.

## Anti-scope (each is a later spec)

| Topic | Spec |
|-------|------|
| `use`/`default` verbs (writing the pin, global default in config) | §3 |
| Precedence (pin > global default > vanilla) that **chooses** A/B | §3 |
| SessionStart hook + actionable nudge/gate; **invoking** `Activate`/`DetectStaleness` | §3 |
| Enforcing the vault exclusion (forcing `shared=0` in team-memory's own write path) + anti-zombie guard | §4 |
| Assisted creation (`profile new`) + `mneme-init` integration (capa-2/3 authoring in the grill) | §5 |
| Migrating the default OSS profile (assets → profile format) | §6 |
| Scaffolding (`/new-project`, `/new-app`, `scaffolds/`, `_blueprints/`) | §7 |
| Runtime consumption of `models.toml`/`policy.toml`/`templates/` by scoring/lanes/`spec_doc_write` | follow-up |

## Testing notes

`internal/profile`'s tests use only `t.TempDir()` and local git fixtures
(`git init` + local `git config user.name/user.email`, never `--global`,
never network) — the leaf never resolves `HOME` or the process git identity,
so no test needs `gitident.Reset()`. `scripts/testguard.sh` additionally
checks that no test leaves a `profiles/` directory, a `profile.lock`, or
materialized skills behind in the sandboxed test `HOME` (SPEC-091 §1 AC13,
extended by SPEC-092 §2), the same invariant it already enforces for
`projects/*.db`/`global.db`. `internal/service`'s activation tests
(`profile_activate_test.go`) inject every path (`profilesDir`, `repoRoot`,
`skillsDir`) via `t.TempDir()` and build the `MemoryService`/
`SubagentService` seam the same way `internal/service/subagents_test.go`
already does — no test resolves `HOME` or a real project database.
