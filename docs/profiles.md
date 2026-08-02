# Profiles §1–§3, §5–§6: manifest/pin/store, activation, precedence + use/default, assisted creation, default OSS profile

> SPEC-091 (§1) + SPEC-092 (§2) + SPEC-093 (§3) + SPEC-095 (§5) + SPEC-096
> (§6), 5 of 7 specs in the EPIC `profiles`. Design reference:
> `docs/profiles-design.md` (§1–§8, §9–§11, §13–§14, §15.6–§15.7,
> §16.2–§16.3/§16.5–§16.6, decisions #2–#13/#18). This document covers §1
> (the foundation: manifest, pin, host-level store, read-only pin
> resolution), §2 (the activation engine: hybrid materialization,
> `.mneme/profile.lock`, `source=profile:<name>` provenance, switch), §3
> (precedence, the two write verbs `use`/`default`, and the SessionStart
> integration), §5 (assisted creation: the `profile new` scaffolder, the
> `mneme-profile-author` skill, and `mneme-init`'s profile-detection
> integration), and §6 (the embedded OSS default profile that `PinDefault`
> materializes, closing the hole §3 left open). Team-memory exclusion (§4)
> and project scaffolding (§7) are later specs; do not expect them here.

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
internal/profile   (leaf: Manifest, ParseManifestFS, Pin, Store, ResolvePin,
                     Contents, LoadContents/LoadContentsFS,
                     DefaultProfileName, Lock, ParseLock/RenderLock, Snapshot,
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

## MCP tools (65→69→71→72→...→76)

§2 added no new CLI/MCP verbs — `Activate`/`Switch`/`Deactivate`/
`DetectStaleness`/`ActiveLock` are `ProfileService` methods §3 consumes
(`use`) or exposes indirectly (SessionStart). §3 adds the two write verbs:
`profile_use`/`profile_default` (69→71). §5 adds one: `profile_new` (71→72).
SPEC-105 §8 adds one more: `profile_deactivate` (75→76, `profile_*` 7→8).

| Tool | Params | Returns |
|------|--------|---------|
| `profile_new` | `{name, dir?}` | `NewProfileResult` (name/path/manifest_path) |
| `profile_add` | `{source, name?, ref?, force?}` | `AddResult` (name/version/ref/path) |
| `profile_update` | `{name?, ref?}` | `UpdateResult` (name/old_ref/new_ref/version) |
| `profile_list` | `{}` | `[]ProfileInfo` |
| `profile_status` | `{project_root?}` | `Resolution` (state/pin/manifest/path) |
| `profile_use` | `{name, project_root?}` | `UseResult` (name/source/ref/project_root/materialized/action/warnings) |
| `profile_default` | `{name?, clear?}` | `DefaultResult` (default) |
| `profile_deactivate` | `{project_root?, apply?}` | `DeactivateResult` (applied/profile/commit/ref/activated_at/artifacts/rule_ids/orphan_rule_ids/lock_path/next_session/residual_backups/warnings) |

## HTTP: no endpoints (decision, AC12)

HTTP does not get profile endpoints in §1, and the endpoint count (8) is
unchanged. `profile add`/`update` are local-host operations that clone into
*this machine's* `~/.mneme/profiles` and depend on the developer's own
interactive git credentials — a REST endpoint on a shared server has no clean
semantics for "clone a private repo on behalf of a remote caller." Profiles
also activate at SessionStart (§3), a CLI/agent-session lifecycle, not an
HTTP server's. Consistent with the existing "HTTP lacks SDD tools" precedent;
re-evaluated if a genuine server-side use case appears. §8 (SPEC-105 DD22)
reaffirms this for `deactivate`: it's a host-local operation over the
filesystem of the caller's own repo — "whose `project_root`?" has no REST
answer — so the endpoint count stays 8, unchanged.

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

### Switch A→B — `ProfileService.Switch` (no longer dead code, SPEC-105 §8)

Pre-SPEC-105, `Switch` was implemented and tested but **never called by any
production code path** (verified with `codegraph_callers`). SPEC-105 §8
rewires the three production call sites onto `Reconcile` instead (see §8
below) and reimplements `Switch` itself as a thin adapter over it:

```go
func (s *ProfileService) Switch(ctx context.Context, repoRoot string, to ActivationInput) (*ActivateResult, error) {
    result, err := s.Reconcile(ctx, repoRoot, to)
    ...
    if result.Activation != nil { return result.Activation, nil }
    // Action == noop: already exactly `to` — reconstruct an ActivateResult
    // view from the untouched lock instead of redoing any I/O.
}
```

Conceptually the steps are unchanged:

1. Read A's lock (`ActiveLock`). Absent → equivalent to a plain `Activate(B)`.
2. `Deactivate(A)`: remove every artifact A's lock lists — restoring a
   backed-up dev file when one exists (§8/DD5), `os.RemoveAll` for
   skill directories otherwise, `managedblock.Remove(path, "profile")` for
   the block (removing *only* the marked region) — and
   `PurgeProfileRules(A)` (hard delete by provenance, plus the orphan sweep,
   §8/DD10).
3. `Activate(B)` — materializes B, inserts B's rules, writes a fresh lock.

**New in §8:** if `to` is already exactly what's active (same profile, same
commit, nothing drifted), `Reconcile`'s guard reports `noop` and `Switch`
skips steps 2-3 entirely — there is nothing to switch away from.

**Invariant (unchanged):** the switch only ever touches what A's own lock
lists, plus rows carrying A's exact provenance stamp. Hand-authored agent
files (never in any lock), rules without a `profile:*` source, and
`CLAUDE.md` prose outside the `"profile"` block are structurally invisible
to it — there is no code path that could reach them.

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

## §3: Precedence, `use`/`default`, and SessionStart

§3 is the consistency spine of the nvm model (`docs/profiles-design.md` §5,
§6, §10, decisions #5/#6): the two verbs that activate a profile, the
precedence rule that decides which wins, and the SessionStart integration
that lets a repo auto-activate on open.

### The pin gains a writer — `WritePin` + `Store.PinFromStore`

§1 only *read* the pin (`ResolvePin`). §3 adds the write path:

```go
func WritePin(projectRoot string, pin *Pin) error
func (s *Store) PinFromStore(name string) (*PinFromStoreResult, error)
func (s *Store) HeadCommit(name string) (string, error)
```

- `WritePin` validates (rejecting an invalid pin with `ErrInvalidPin` before
  touching disk) and writes atomically (temp file + `os.Rename`). If a pin
  already exists at `projectRoot` and the new one carries no `Scaffold`, the
  existing pin's `Scaffold` (the `/new-project` provenance field, §7) is
  carried over — everything else is a pure replacement.
- `PinFromStore` reconstructs a self-describing pin from a profile's
  checkout in the store, without ever cloning: `Name` = the requested name,
  `Source` = `git remote get-url origin` (empty + a warning when the
  checkout has no origin — e.g. it was hand-placed rather than cloned),
  `Ref` = the exact tag when HEAD sits on one (`git describe --tags
  --exact-match`), otherwise the full commit SHA. It also resolves `Commit`
  (`git rev-parse HEAD`) in the same round-trip, for `ActivationInput.Commit`.
- `HeadCommit` is the standalone counterpart used when the caller already
  has a `Pin` from `ResolvePin`/`ResolveActive` (which never carries a commit
  field) rather than one just built by `PinFromStore`.

### `mneme profile use <name>` — "= `nvm use`"

`ProfileService.Use(ctx, projectRoot, name)`:

1. `Store.PinFromStore(name)` — **never clones**; `name` must already be
   installed (`model.ErrProfileNotFound` otherwise, pointing at `profile
   add`). This keeps the `add`/`use` frontier strict.
2. `profile.WritePin(projectRoot, pin)` — writes `.mneme-profile` at the repo
   root.
3. `ProfileService.Activate(ctx, ActivationInput{...})` — materializes
   **immediately** (§2). Unlike the SessionStart path below, a
   materialization failure here **is** propagated as an error: the caller
   explicitly asked to activate a profile and must know if it failed.

`mneme profile use <name>` (CLI) and `profile_use` (MCP) are thin wiring over
this one method — both require a ProfileService fully wired with
`WithProfileMemoryService`/`WithProfileSubagentService`/`WithProfileSkillsDir`
(same construction as the SessionStart integration and as
`internal/service/subagents_test.go`'s pattern), since `use` invokes
`Activate` directly.

### `mneme profile default [<name>] [--clear]` — "= `nvm alias default`"

A new `[profiles]` section in `~/.mneme/config.toml`:

```toml
[profiles]
default = "chatea-pro"   # "" (or absent) = vanilla
```

`ProfileService.SetDefault`/`ClearDefault`/`Default` wrap
`config.SetProfilesDefault(path, name)` (the same atomic
load/mutate/marshal/rename-into-place pattern as `SetModelsOverrides`).
`SetDefault` fail-fasts with `model.ErrProfileNotFound` when `name` is not in
the host-level store — a default that resolves to nothing is a footgun the
dev can fix with `profile add` first (design decision A1). Crucially: this
verb **never materializes anything** and **never re-points a session already
running** — it only affects sessions started *after* the call, in repos with
*no pin of their own*.

### Precedence — `Store.ResolveActive`

```go
type ActiveSource int // SourceVanilla | SourcePin | SourceGlobalDefault
type ActiveResolution struct { Source ActiveSource; Resolution Resolution }
func (s *Store) ResolveActive(projectRoot, globalDefault string) (ActiveResolution, error)
```

Pure replacement, never a merge (decision #5): a project's own pin — in
**any** of its three non-absent states (`PinDefault`/`PinInstalled`/
`PinMissing`) — wins outright and `globalDefault` is never even consulted.
Only when the project has **no pin at all** does the host default apply
(itself resolving to `PinInstalled` or `PinMissing` against the store).
`globalDefault` is injected as a plain string by the caller
(`ProfileService.ResolveActive`) — the leaf never imports `internal/config`,
keeping the SPEC-056 D5 import-guard green.

### Read-once-not-live (§3.7, AC10)

`Config.Profiles.Default` is consulted in **exactly one** production
call-site in the whole runtime: `ProfileService.ResolveActive`, invoked once
per session by `runHookSessionStart`. `TestProfilesDefault_SingleReadPath`
(`internal/service/profile_default_readonce_test.go`) enforces this
structurally — it walks `internal/` and fails if the field selector
`.Profiles.Default` shows up anywhere outside `internal/config` (which owns
the field) except that one file. This is what makes `profile default`
"sessions started after this call only": nothing else in mneme re-reads the
default mid-session, and nothing re-derives already-materialized files from
it later. `profile use`, by contrast, is an **explicit** in-session action
and re-materializes on purpose — the read-once rule applies to the *default*,
never to `use`.

### SessionStart integration — `maybeActivateProfile`

`runHookSessionStart` calls `maybeActivateProfile` **before** the context
block, with the exact fail-open contract `maybeEmitCodegraphNudge` already
established (exit 0 always; every failure degrades to a `stderr` WARN):

1. Resolve the project root (`git rev-parse --show-toplevel`, or `cwd` when
   not a git repo).
2. `ProfileService.ResolveActive(root)` — the one-time read described above.
3. Branch on `Resolution.State`:
   - `PinAbsent` (vanilla) → emit nothing (no noise on non-profile repos).
   - `PinInstalled` → resolve the checkout's commit (`ResolveCommit`), call
     `Activate`, and print a short confirmation block naming the source
     (`via pin` or `via default global`). A failure here WARNs and returns —
     it never aborts the session.
   - `PinDefault` (mneme's internal default profile — no `Source`) →
     **materializes** the embedded OSS default profile (§6,
     `activateDefaultProfileForSession`) and prints the `mneme-default (OSS
     built-in)` confirmation block. Before §6 this branch only printed a
     pending-confirmation message; §6 closes that hole.
   - `PinMissing` → print the actionable nudge/gate (below). **Never
     clones.**

```
<!-- mneme:profile:start -->
## Profile no instalado

Este repo usa el profile `chatea-pro@v3` (source `git@github.com:chateapro/mneme-profile.git`),
que no está instalado en este host.

**Para instalarlo ahora** (con tu confirmación — mneme nunca clona sin OK):
    mneme profile add git@github.com:chateapro/mneme-profile.git --ref v3
    mneme profile use chatea-pro

Hasta entonces la sesión corre en modo **vanilla** (sin el profile del equipo).
<!-- mneme:profile:end -->
```

When `PinMissing` comes from a host *default* naming an uninstalled profile
(rather than a repo's own pin), there is no known `Source` to print an exact
`add` command for — the block instead asks for the git URL explicitly.

## §5: Assisted creation — scaffolder + grill + `mneme-init` integration

§5 is "the other end of the lifecycle" (`docs/profiles-design.md` §15.7): §1–§3
covered how a profile is *installed*/*activated*/*precedence-resolved*; §5
covers how one is **created** in the first place, and how an existing repo
**onboards** to one. Creation is, deliberately, the same "two halves" pattern
project scaffolding uses (§15.1): a **deterministic command** for structure,
and an **assisted skill** for content.

### The scaffolder — `mneme profile new <name>`

```bash
mneme profile new <name> [--dir <path>]
```

`profile.Scaffold(dest, ScaffoldInput{Name})` (the leaf, reusing
`Manifest.Validate`'s safe-slug check and `runGit`) creates:

```
<dest>/
  mneme-profile.toml        # name=<name>, version="0.1.0" — RenderManifest
  README.md                 # what this is + next step + how a team consumes it
  agents/.gitkeep
  skills/.gitkeep
  blocks/.gitkeep
  templates/.gitkeep
  scaffolds/_blueprints/.gitkeep   # left EMPTY — populated by §7, never here
  rules.jsonl                # empty file (0 rules)
  models.toml                # commented stub
  policy.toml                # commented stub
```

...then runs `git init` (no commit, no remote — the author commits/pushes
once they've curated real content). `Scaffold` is a **free function of the
leaf, not a `Store` method**: it never touches `~/.mneme/profiles/`. That
frontier matters — `profile new` produces a **source repo** the author
fills in and pushes; only *then* does a consumer install it via `profile add`
(§1). `RenderManifest` is the write-path counterpart of `ParseManifest`
(symmetric to `WritePin`/`PinFromStore`, §3).

`ProfileService.NewProfile(NewProfileInput{Name, Dir})` wraps `Scaffold`:
`Dir` defaults to `<cwd>/<Name>`; a non-empty destination fails with the same
`ErrProfileExists` sentinel `Store.Add` uses for an already-installed name
(translated to `model.ErrProfileExists`); an unsafe-slug `Name` fails via
`Manifest.Validate` (`model.ErrInvalidProfile`) — both rejections happen
**before any filesystem write**.

`mneme profile new` (CLI) and `profile_new` (MCP, `{name, dir?}` →
`NewProfileResult{name, path, manifest_path}`) are thin wiring over this one
method. MCP parity here is not cosmetic: `profile_new` is the
**mneme-profile-author skill's own first step**, and skills only ever call
MCP tools. HTTP gets nothing new (consistent with §1/§2/§3 — this is a
host-local, developer-session operation).

### The grill — `mneme-profile-author` skill

A sibling of `mneme-init` (not a sub-phase of it — see decision #3 and A3):
`mneme-init` **onboards a single repo** to consume a profile; this skill
**authors the profile repo's content**, independent of any one project.
Embedded at `internal/install/assets/skills/mneme-profile-author/`, picked up
by `BundledSkillEntries`'s existing `assets/skills` walk with **no new
wiring** (the precedent SPEC-058 established: dropping a subdirectory is
enough, `TestBundledSkills_AllLintClean` validates it instantly).

Its workflow: scaffold (`profile_new`) → identity (`mneme-profile.toml`) →
capa-1 per role (`subagent_compose(archetype=...)`, **always** Go-authored —
never hand-written `tools:`/`permissionMode:` — written to
`<profile-repo>/agents/<role>.md` via `Write`) → rules (`rules.jsonl`, one
`RuleSpec` JSON object per line) → blocks/models/policy/templates → skills →
close out (the author runs `git add`/`commit`/`tag`/`push` themselves — the
skill never does this on their behalf). `scaffolds/_blueprints/` is
explicitly left empty; project scaffolding is a separate, later spec (§7),
and the skill's own `validation/run.sh` greps for that deferral alongside the
`profile_new`/`subagent_compose` tool references and the
Go-authored-capa-1 invariant.

### `mneme-init` integration — profile detection precedes the subagent grill

`mneme-init`'s SKILL.md (now v1.6.0) gained a "Step 0.5 — Profile detection"
phase, prose-only (no new Go, uses the **existing** `profile_status`/
`profile_add`/`profile_use` MCP tools from §1/§3), inserted right after the
core step and before the subagent grill is offered:

- **`PinInstalled`** → tell the user this repo's capa-1 comes from the
  installed profile; run the grill in **profile-active mode**.
- **`PinMissing`** → offer the **same gate** the SessionStart integration
  (§3) already uses: `profile_add <source> --ref <ref>` then
  `profile_use <name>`, **only on explicit confirmation** — never clones
  without OK. Declines → grill runs in **vanilla mode** for this session.
- **`PinDefault`**/**`PinAbsent`** → **vanilla mode**, byte-for-byte
  identical to pre-§5 behavior (zero regression for repos with no profile).

**Vanilla mode** is unchanged: `subagent_compose` → `subagent_write` per
role, exactly as before. **Profile-active mode** changes Phase 4 of the
grill: it authors and **persists** capa-2/3 via `subagent_profile_save`
(including a `ProjectProfileArea` entry — this role's `areas_layer3_md`
draft — in `profile_json.areas`), then — critically — **never calls
`subagent_write`** for that role (doing so would bake a second,
archetype-generated capa-1 on top of the profile's own, R4/AC7). Instead it
calls `profile_use <name>` once per session to trigger the **fusion**
(`ProfileService.Activate` → `fuseAgent`, §2) of the profile's capa-1 with
the freshly-saved capa-2/3. The areas-completeness question (SPEC-086 D11,
`areas_complete`) is asked in **both** modes — it feeds the delegation hook's
containment regardless of which mode wrote the manifest entry.

### `ProjectProfile.Areas` — making capa-3 queryable (§3.6, R2)

Before §5, `ProjectProfile` (the capa-2 typed-memory record, SPEC-052 D4)
persisted `Repo`/`Org`/`Mapping` — but the capa-3 doctrine
(`areas_layer3_md` per role) was only ever baked directly into the composed
`.claude/agents/<role>.md` file `subagents.Compose` produces. That is fine in
vanilla mode (the composed file IS the artifact), but breaks down in
profile-active mode: Phase 4 there never calls `subagent_write`, so nothing
would ever persist the capa-3 draft anywhere `fuseAgent` could read it back.

```go
type ProjectProfile struct {
    SchemaVersion int
    Repo          ProjectProfileRepo
    Org           string
    Mapping       []ProjectProfileMapping
    Areas         []ProjectProfileArea `json:"areas,omitempty"` // NEW (§5)
}

type ProjectProfileArea struct {
    Role     subagents.Role `json:"role"`
    Layer3MD string         `json:"layer3_md"`
}
```

`Areas` round-trips through the existing `subagent_profile_save`/
`subagent_profile_get` typed-memory JSON path — **no SQLite migration**, the
same self-describing-JSON posture `ProjectProfile.SchemaVersion` already
documents. A `ProjectProfile` saved before this field existed reads back with
`Areas == nil` (`json:"omitempty"`, additive) — no reader breaks. This is a
change to `internal/service` only; it does **not** touch the leaf
`internal/subagents`' public contract (no new `Compose` mode, no changed
signature) — see R1/§3.5 in the design memory for why that seam stayed
untouched: the AUTHORING of a profile's capa-1 (the `mneme-profile-author`
skill, above) uses `subagent_compose` exactly as it exists today; only the
**render of the fusion** belongs to §2's `fuseAgent`.

## §6: The embedded OSS default profile

§6 provides the assets `PinDefault` (§1/§3) anticipated but never had:
`SPEC-091 §3.5` defines `PinDefault` as a **resolution state**, and
`SPEC-093 §3.6/A3` explicitly punts real materialization to "when §6
provides it". §6 provides it, and does so fulfilling `docs/profiles-design.md`
§14's OSS deliverable: **the engine (§1–§3, §5) + one "default profile"** —
mneme's own current assets (agents/skills/models/templates), migrated into
profile format.

### Decision — embedded, never materialized to `~/.mneme/profiles/_default`

The default profile is an `fs.FS` packaged with `//go:embed`, **never**
written to `~/.mneme/profiles/`. Four reasons, all consequences of lessons
this EPIC already learned:

1. **Reproducibility.** Embedded = exactly what the binary shipped. A
   `~/.mneme/profiles/_default` on disk would be state that drifts from the
   binary the moment `mneme upgrade` ships new agents (SPEC-089's lesson:
   never persist machine-local derived state that can go stale).
2. **No network, no install step, always present.** A real profile installs
   via `profile add <url>` (a clone). The default has no URL (`Source ==
   ""`) — embedded, it needs no install step and works offline on a
   freshly-installed host.
3. **The store stays a pure "git checkouts" abstraction.** `Store.List`/
   `Update`/`ResolvePin` all assume a `.git` checkout; a `_default` entry
   with no remote would be a permanent special case. The default is never
   consulted through the store — it resolves purely by `PinState`.
4. **No new corruption surface** (SPEC-089's exact lesson, generalised).

### Where the assets live — `internal/install`, not the leaf or `service`

```go
// internal/install/default_profile.go
//go:embed assets/profiles/default
var defaultProfileFS embed.FS

func DefaultProfileFS() fs.FS   // re-rooted via fs.Sub — mneme-profile.toml at the FS root
```

`internal/install` already owns the OSS assets (`builtinAgents`/
`builtinSkills`/`builtinTemplates`, `assets.go`) and their `embed.FS`
machinery — the default profile tree is a **parallel, byte-parity-guarded
copy** under `internal/install/assets/profiles/default/`, not a move. The
global installer keeps reading its own assets exactly as before (R1) —
`installSteps`/`ClaudeCode()`/`WriteSkills`/`InjectManual`/`WriteTemplates`/
`ApplyAgentModels`/`RemoveInstalledBuiltinAgents` are **untouched**.

`internal/profile` (the leaf) and `internal/service` **never import
`internal/install`** — the `fs.FS` is injected by the frontend
(`cli.initService`'s callers, `mcp`'s handler construction) via
`service.WithDefaultProfileFS(install.DefaultProfileFS())`, the same
functional-option pattern `WithTeamMemory` established (SPEC-085 D1):

```
cli/hook.go, cli/profile.go, mcp/handlers.go   (import install)
        │  WithDefaultProfileFS(install.DefaultProfileFS())
        ▼
service.ProfileService{ defaultFS fs.FS }      (Activate reads it in the default branch)
        │
        ▼
profile.LoadContentsFS(fsys fs.FS)             (leaf: parses disk checkouts AND the embed identically)
```

### One parse path — `LoadContentsFS`

§2's `LoadContents(dir)` became a one-line wrapper:

```go
func LoadContents(dir string) (*Contents, error) {
	return LoadContentsFS(os.DirFS(dir))
}

func LoadContentsFS(fsys fs.FS) (*Contents, error)   // the real parser now
```

Every helper (`loadAgents`, `loadBlocks`, `loadSkillNames`, `loadRules`) was
rewritten from `os.ReadDir`/`os.ReadFile`/`os.Stat` to `fs.ReadDir`/
`fs.ReadFile`/`fs.Stat` against the injected `fsys` — a disk checkout
(`os.DirFS`) and the embedded default now share **one** parse path, so the
default can never silently parse differently than a git-cloned profile.
`Contents.SkillsDir`/`ModelsPath`/`PolicyPath`/`TemplatesDir` changed
meaning accordingly: they are now **`fsys`-relative** paths (`"skills"`,
`"models.toml"`, …) instead of absolute disk paths — `Contents.FS` carries
the filesystem they are relative to, so a caller reopens them via
`fs.ReadFile(c.FS, c.ModelsPath)` rather than the `os` package directly.
`ProfileService.materializeSkills`'s skill-directory copy was rewritten the
same way — `copyDir` (disk→disk) became `copyFSDir(fsys fs.FS, src, dst
string)` (fs.FS→disk) — so a profile's `skills/<name>/` materializes
identically whether its bytes live on disk or in the binary.

`profile.DefaultProfileName = "mneme-default"` is the reserved name a
sourceless pin always resolves to, and `profile.ParseManifestFS(fsys)` is
the `fs.FS` counterpart of `ParseManifestFile` used to read the default's
own manifest (`ProfileService.DefaultManifest()`).

### Wiring `PinDefault` into `Activate`

`ActivationInput` gained one field: `Default bool`. `Activate` branches on
`in.Default || in.Name == profile.DefaultProfileName`:

```go
if isDefault {
    profileName = profile.DefaultProfileName   // the pin's own Name is informational only
    contents, err = profile.LoadContentsFS(s.defaultFS)   // ErrDefaultProfileUnavailable if unwired
} else {
    contents, err = profile.LoadContents(store.ProfilePath(in.Name))   // §2, unchanged
}
```

Everything downstream of that branch point — agent fusion, skill copy, block
upsert, rule insertion, lock write — is **§2 unchanged**. The lock records
`profile="mneme-default"`, `source=""`, and a caller-built **synthetic,
version-locked commit**: `"bundled:<mneme-version>+<manifest-version>"`.
Activate itself never resolves a version string for any profile (default or
otherwise) — `activateDefaultProfileForSession` (`internal/cli/hook.go`)
builds it from `ProfileService.DefaultManifest()` + the CLI's own `Version`.
After a `mneme upgrade`, `<mneme-version>` changes → `Lock.StalenessAgainst`
(§2, unchanged) flags the workspace stale → the next SessionStart
re-materializes the new default. No `~/.mneme/profiles/_default` ever
existed to go stale in the first place.

### Asset mapping and the empty `blocks/`

| Today (`internal/install/assets/…`) | Default profile piece | Rule |
|---|---|---|
| `agents/<role>.md` ×6 | `agents/<role>.md` | byte copy |
| `skills/{example-skill,mneme-init,mneme-profile-author}/` | `skills/…` | byte copy, full tree |
| `defaults.go:defaultAgentModels` | `models.toml` `[models]` | 1:1 serialisation |
| `assets/templates/spec-template.md` | `templates/spec.md` | byte copy (only entregable existing today; plan/qa join when they exist) |
| `assets/operating-manual.md` | **NOT migrated** | see below |
| — | `rules.jsonl` | absent (0 rules) |
| enforcement/lane defaults | `policy.toml` | informative only, not consumed |

**The operating manual is never a profile block.** It is host-global
infrastructure the installer injects directly into `~/.claude/CLAUDE.md`
(`InjectManual`) — not per-project. Migrating it into `blocks/` would make
`PinDefault` upsert a **second** copy into the *project's* `CLAUDE.md`,
diverging from vanilla. The default OSS profile's `blocks/` directory ships
only a non-`.md` keep file (`blocks/README`, since `go:embed` cannot embed an
empty directory) — `LoadContentsFS` parses it to zero blocks (its loader
only picks up `*.md` files). One observable consequence: activating
`mneme-default` in a repo only ever adds the 6 layer-1 agents to
`.claude/agents/` — skills/models already match what the global install
leaves on the host (idempotent).

### `TestDefaultProfile_DriftAgainstAssets` — the parity guard

A white-box test in `internal/install` byte-compares every migrated piece
against its origin (`builtinAgents`, `BundledSkillEntries`,
`defaultAgentModels`, `builtinTemplates`) on every test run. Editing one copy
without the other breaks CI immediately — the same guard
`internal/subagents`' own archetype copy already established as precedent.

### No-regression — the vanilla path never routes through the default

**`installSteps`/`ClaudeCode()` are untouched.** A `PinAbsent` repo resolves
to `SourceVanilla` (§3.5) unconditionally — SessionStart emits nothing, and
`mneme install claude-code` runs exactly the pre-§6 sequence: MCP config,
hooks, the global manual, `/mneme-init`, skills, templates, the delegation
hook. `TestClaudeCodeInstall_VanillaGolden`/`TestClaudeCodeInstall_Idempotency`
(`internal/install`) freeze that contract — including an explicit assertion
that `.claude/agents/` and `.mneme/profile.lock` never appear after a
vanilla `Install()` call. `TestInstallSteps_DefaultSequence` (pre-existing)
needed zero changes.

### No new surface

§6 added **zero** MCP tools (71 unchanged), zero CLI commands, zero HTTP
endpoints — `PinDefault` activates through the existing SessionStart hook and
`ResolveActive`/`Activate` (§3), with a `Default: true`/`profile.DefaultProfileName`
`ActivationInput`, nothing more.

## §8: Reconciliation, backup, and deactivation (SPEC-105)

An incident surfaced the gap §2-§6 left open: `Activate` was an
**unconditional** materialization event, not idempotent even against the
same commit. Every SessionStart called it unguarded, so 215 rows accumulated
across 8 real repos (up to 9 tandas per repo, all against the same profile
commit — the tandas were *sessions*, not commits). A second, independent bug
let project-scoped rules leak into `global.db` (via `initService`'s
`global.db`-as-projectStore aliasing when no git remote resolves a slug) and
be served to **every repo on the host**, including the `PreToolUse` hook,
which can `exit 2` and block a tool call for a repo with nothing to do with
the profile that leaked it. §8 fixes both, adds a supported way to undo an
activation, and contains the rules-leak's blast radius without redesigning
`initService`.

### The core fix: activation becomes convergence, not an event

```go
func Converged(lock *Lock, want Desired, obs Observation) (bool, []Divergence)
func (s *ProfileService) Reconcile(ctx context.Context, repoRoot string, in ActivationInput) (*ReconcileResult, error)
```

`Converged` (leaf, pure, `internal/profile/converge.go`) decides whether a
workspace already matches `want` — comparing profile/commit identity, every
artifact's presence (and a "block" artifact's **digest**, not just its
marker's presence — DD13, catches a dev editing inside the managed block by
hand), and the rule id **set** the database actually has against the
lock's declared set. The comparison is against the **database**, not just
the lock — deliberately: it's what lets a single `Reconcile` call
self-repair one of the 215-row-contaminated repos (lock says 3 ids, DB has
9 → divergent → purge & reinsert → DB has 3) with no migration script.

`Reconcile` (impure, `internal/service/profile_reconcile.go`) is the
orchestrator: read the lock → if present, `observe()` the real world (stat
files, read the block, query rule ids) → ask `Converged` → **noop** and
return immediately if it agrees (the hot SessionStart path: no `Contents`
loaded, no git touched) → otherwise `preflightDeactivate` → `Deactivate` →
`Activate(in)`. `ReconcileAction` reports which of `noop` / `activated` /
`repaired` / `switched` / `blocked` happened. All three production call
sites (`Use`, `activateProfileForSession`, `activateDefaultProfileForSession`)
now call `Reconcile`, never a bare `Activate`.

**Defense in depth (DD4):** independently of the guard, `materializeRules`
now purges by provenance **before its first insert, every time** — so
"activating N times is idempotent in rules" is true by construction, even
if the lock is deleted by hand or a future bug breaks `Converged` itself.
The four mutation tests below prove the guard actually does the work (not
just DD4's purge).

### Lock schema v2: backups and digests

`LockArtifact` gains three optional fields (schema_version 1→2,
`Lock.Validate` widened from strict equality to a **range** `1..2` so a v1
lock still parses and validates — only a version this build has never heard
of is rejected):

```go
Backup  string // pre-activation copy of a dev's own displaced file, if any
Created bool   // Path did not exist before this activation
Digest  string // sha256 of a "block" artifact's content, for drift detection
```

Before overwriting a path, `Activate` checks it against the **previous
lock's own artifact set**: owned by that lock → overwrite freely (no
backup); exists but NOT owned → copy it to
`<repoRoot>/.mneme/backups/<UTC>/<relative path>` first (or
`backups/<UTC>/skills/<name>/` for a skill directory, which lives outside
the repo) and record `Backup`; does not exist → `Created: true`. Never
overwrites an existing backup destination — collisions get a `-1`, `-2`...
suffix, and the lock records the path actually used. `Deactivate` restores
a `Backup` byte-for-byte and deletes the backup (and its now-empty run
directory) instead of just removing the profile's file. A `CLAUDE.md`
the activation itself `Created` gets deleted on deactivate only if removing
the block leaves it empty/whitespace-only; a pre-existing `CLAUDE.md` is
never deleted, however empty the remaining prose looks.

`.mneme/.gitignore` gains a second scoped entry, `backups/`, alongside the
existing `profile.lock` — backups are copies of files that in several repos
ARE tracked (`.claude/agents/*.md`); committing a copy is noise, and
potentially a dev's own content leaking into shared history.

### Rules sin slug: contained, not just documented (DD8)

`initService` still aliases `global.db` as the project store when no git
remote resolves a slug — that redesign is out of scope for a patch — but
its consequences are now contained at every layer:

1. **Write:** `SaveProfileRule` rejects with `model.ErrProjectSlugRequired`
   when `!svc.HasProject()`. `Activate` doesn't fail on this — it degrades:
   agents/skills/blocks still materialize, `ActivateResult.Degradations`
   names the cause and remedy.
2. **Read:** `ListRules`/`loadActiveRules` stop serving `scope=project` rows
   from the global store (the global branch is `scope IN (global, org)`,
   fused from two queries so org rules — already served today — don't
   silently disappear). `mneme rule list` from a repo with no profile no
   longer returns another repo's rules.
3. **The hook (the surface with actual teeth):** `internal/cli/hook.go`'s
   `rulesQuery` splits into `rulesQueryProject` (unchanged) and
   `rulesQueryGlobal` (`+ AND scope IN ('global', 'org')`). This is the one
   that matters most: a `severity=block` rule leaking through here used to
   `exit 2` a tool call in **every repo on the host**.
4. **The sweep:** `PurgeProfileRules` now ALSO deletes matching rows from
   the global store with an empty project (`HardDeleteBySource`'s clause
   fixed to treat `project=""` as "match `project IS NULL`" — SQLite
   persists an empty `Project` as `NULL`, so the old `project = ''` clause
   silently matched nothing). Runs from **any** repo — a row with
   `scope=project`/`project=NULL` is unattributable garbage no repo could
   ever legitimately claim.

### `mneme profile deactivate` — dry-run by default

```bash
mneme profile deactivate            # prints the plan, mutates nothing
mneme profile deactivate --apply    # executes it
mneme profile deactivate --json     # same object, either mode
```

`ProfileService.DeactivateProject` builds one `DeactivateResult` regardless
of `Apply` (`Applied` distinguishes them): per-artifact plan (`remove` /
`restore` / `remove-file`), the rule ids about to be purged (project-scoped
and orphaned-global, separately), residual backup directories from OTHER
activations (left untouched), and — computed **before** anything mutates —
`NextSession`, one of three messages:

- pin present → *"reactivará X (pin); elimina el pin si quieres
  desactivarlo permanentemente"*.
- no pin, host default set → *"reactivará X (default global); ejecuta
  `mneme profile default --clear`"*.
- neither → *"correrá en modo vanilla"*.

**Deliberately never touches `.mneme-profile`** — it's a committed,
team-shared file; a local "undo the materialization" op silently rewriting
it would create a diff every teammate sees. `NextSession` exists precisely
because of this: the 8 contaminated repos have no pin at all — their
contamination came from the host **default** — so `deactivate --apply`
alone does not close the loop; the operator still has to
`mneme profile default --clear` (or repoint it) before opening a new
session.

### Lock huérfano (DD20)

`maybeActivateProfile`'s `ProfilePinAbsent` branch used to return in silence
unconditionally — correct for the common vanilla case, wrong for a repo
that still carries an activation lock with nothing pointing at it (the pin
was deleted, or never committed). It now checks `ActiveLock` first: present
→ emit an actionable-but-passive block (profile, `ActivatedAt`, how many
artifacts are still alive, rule counts by provenance, and the exact
`mneme profile deactivate --apply` command) and return — **never
deactivates on its own**. The silence of an absent pin is not consent to
delete files from the workspace.

### Preflight, adjacent to each mutation (DD16)

`preflightActivate` (inside `Activate`, before `materializeAgents`) and
`preflightDeactivate` (inside `Reconcile`/`DeactivateProject`, before
`Deactivate`) each check every filesystem precondition their own mutation
phase needs — via a **real, transient write probe** (create+remove a file),
not a permission-bit inspection, so it behaves the same on Windows as on
Unix. Either failing aborts with zero mutations. A failure that happens
mid-flight anyway (preflight passed, the write failed regardless) is
reported on **stdout** — the `<!-- mneme:profile:start -->` block the agent
actually reads as context — not just stderr, which it never sees.

### Mutation testing (verification discipline, precedent SPEC-104)

Four deliberate mutations were applied to `Converged`/`PurgeProfileRules`
and reverted before this spec's implementation was considered complete: the
guard forced to always converge, forced to always diverge, its rule-set
comparison downgraded to a length check, and the orphan sweep deleted.
Each one made a specific, predicted subset of tests fail — proof the
convergence tests actually exercise the guard, not merely DD4's
independent purge-before-insert side effect (the exact gap that let a
previous spec's QA reject a superficially-passing implementation).

## Anti-scope (each is a later spec)

| Topic | Spec |
|-------|------|
| Enforcing the vault exclusion (forcing `shared=0` in team-memory's own write path) + anti-zombie guard | §4 |
| Scaffolding (`/new-project`, `/new-app`, `scaffolds/`, `_blueprints/`); the pin's `scaffold` field is preserved by `WritePin`, never acted upon; §5 only leaves `scaffolds/_blueprints/` in the skeleton | §7 |
| Runtime consumption of `models.toml`/`policy.toml`/`templates/` by scoring/lanes/`spec_doc_write` | follow-up |
| A persistent "profile disabled in this repo" state (today `deactivate` doesn't stop the pin/host-default from reactivating on the next SessionStart — `NextSession` only reports it) | follow-up (§8) |
| Redesigning `initService`'s `global.db`-as-projectStore aliasing when no git remote resolves a slug (§8 contains the consequences via `HasProject()`; does not eliminate the aliasing) | follow-up (§8) |
| Running `scripts/cleanup-test-pollution.sh` against the historical contamination beyond what convergence self-repairs | follow-up (§8) |
| Validating `rules.jsonl` for duplicate `topic_key`s at `profile add`/`update` time (today they silently collapse via upsert — DD3 tolerates it, a warning would be kinder) | follow-up (§8) |

## Testing notes

`internal/profile`'s tests use only `t.TempDir()` and local git fixtures
(`git init` + local `git config user.name/user.email`, never `--global`,
never network) — the leaf never resolves `HOME` or the process git identity,
so no test needs `gitident.Reset()`. `scripts/testguard.sh` additionally
checks that no test leaves a `profiles/` directory, a `profile.lock`, or
materialized skills behind in the sandboxed test `HOME` (SPEC-091 §1 AC13,
extended by SPEC-092 §2), the same invariant it already enforces for
`projects/*.db`/`global.db`. `internal/service`'s activation tests
(`profile_activate_test.go`, `profile_use_test.go`) inject every path
(`profilesDir`, `repoRoot`, `skillsDir`, and — new in §3 — `configPath`) via
`t.TempDir()` and build the `MemoryService`/`SubagentService` seam the same
way `internal/service/subagents_test.go` already does — no test resolves
`HOME` or a real project database.

§3's `Use` tests need a git-BACKED fixture (unlike §2's plain-file
`newActivationTestEnv`), since `PinFromStore` shells out to `git remote
get-url origin`/`git describe`/`git rev-parse` against the checkout —
`profile_use_test.go`'s `newUseTestEnv` `git init`s the fixture profile
directory directly (local identity, no network) so those commands have real
state to read.

`internal/cli`'s SessionStart tests (`profile_sessionstart_test.go`) drive
`runHookSessionStart(ctx, w, errW)` directly with `bytes.Buffer`s — the
function's signature changed from hardcoded `os.Stdout`/`os.Stderr` to
explicit `io.Writer` parameters specifically so this is possible without
capturing real OS stdout (mirrors `runHookPreToolUse`'s existing shape).
Each test `os.Chdir`s into an isolated fixture and calls `gitident.Reset()`
(SPEC-085 §5.3/§5.4 note 3) defensively — `initService()`'s
`DetectTeamMemory()` resolves the real process cwd via `git rev-parse
--show-toplevel`, and none of these fixtures ever create the team-memory
marker file, so `gitident.Author()` is never actually invoked, but the reset
guards against that changing later. CLI-level tests of `profile
default`/`SessionStart`'s `[profiles].default` reads write to
`config.DefaultPath()` under the sandboxed test `HOME` (shared by the whole
`internal/cli` test binary run, per SPEC-085 G2) — each such test cleans up
its own default via `t.Cleanup` to avoid bleeding into unrelated tests in the
same run; `scripts/testguard.sh` does not need extending for this, since it
only checks for stray SQLite/profile-store artifacts, not `config.toml`
contents.

§5's `Scaffold`/`NewProfile` tests inject `dest` via `t.TempDir()` exclusively
— `Scaffold` never resolves `HOME`, and `git init` (no commit) needs no
identity, so no test needs `gitident.Reset()` here either (same posture §1
already established for `Store.Add`'s clones). `TestScaffold_DestinationNotEmpty`
asserts the destination is untouched beyond the pre-existing fixture file when
`ErrProfileExists` fires, and `TestScaffold_UnsafeName` asserts the
destination is never even created for an unsafe-slug `Name` — both guard the
"validate before any write" ordering AC2 requires. `scripts/testguard.sh`
needed no changes for §5: a scaffolded profile repo written under `t.TempDir()`
was never going to land inside the sandboxed test `HOME` in the first place
(unlike `Store.Add`, `Scaffold` never touches `profilesDir`).

§6's `internal/service` tests (`profile_activate_default_test.go`) build a
small `testing/fstest.MapFS` shaped like the embedded default (manifest, one
agent, one skill) instead of importing `internal/install` — the leaf/service
layering guard means `service_test` cannot depend on `install`, and a
`MapFS` exercises the exact same `LoadContentsFS` entry point the real
`embed.FS` does. `internal/install`'s own tests (`default_profile_test.go`)
exercise `DefaultProfileFS()` directly. `internal/cli`'s
`TestMaybeActivateProfile_PinDefault` (`profile_sessionstart_test.go`) is the
one test in the whole suite that materializes the *real* embedded skills
(`example-skill`/`mneme-init`/`mneme-profile-author`) — relying entirely on
`internal/cli`'s package-level `TestMain(testenv.Isolate(m))` (SPEC-085 D5b)
to sandbox `os.UserHomeDir()`, since `maybeActivateProfile`'s `skillsDir`
resolution is not independently test-injectable the way `--data-dir` is;
verified manually against a real, populated `~/.claude/skills/` that no file
there changes when running `go test ./internal/cli/...` without an explicit
`HOME` override.
