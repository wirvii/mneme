# Profiles §1: manifest, pin, and host-level store (v1.29.0)

> SPEC-091. First of 7 specs in the EPIC `profiles`. Design reference:
> `docs/profiles-design.md` (§1–§4, decisions #2/#5/#9/#10/#11). This
> document covers only what §1 implements — the foundation. Activation,
> lockfile, provenance, the `use`/`default` verbs, SessionStart integration,
> team-memory exclusion, assisted creation, and scaffolding are later specs
> (§2–§7); do not expect them here.

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
service/profile.go   (ProfileService)
        │
        ▼
internal/profile      (leaf: Manifest, Pin, Store, ResolvePin — stdlib + go-toml/v2 only)
```

- `internal/profile` is a **leaf package**: it imports only the standard
  library plus `github.com/pelletier/go-toml/v2` — never `internal/model`,
  `internal/store`, `internal/service`, or any other `internal/*` package.
  Same perimeter as `internal/skill`, `internal/conflicts`, `internal/subagents`.
  A dedicated import-guard test enforces this.
- `service.ProfileService` is the only thing `cli`/`mcp` ever call — it wraps
  the leaf's `profile.Store` and translates its sentinel errors
  (`profile.ErrProfileExists`, `profile.ErrProfileNotFound`,
  `profile.ErrProfileNameMismatch`, `profile.ErrInvalidManifest`,
  `profile.ErrInvalidPin`) into their `model.Err*` equivalents, the same
  translation posture `SkillsService`/`ConflictsService` already establish
  for their own leaves.
- §1 needs **no database access** — profiles, pins, and manifests are
  entirely filesystem + git state. No SQLite migration is added. The seam a
  later spec (§2) will need — a `*MemoryService` reference, to record
  provenance on rules/memories a profile writes — is documented here but
  intentionally not wired (YAGNI until that spec lands).

## MCP tools (65→69)

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

## Anti-scope (each is a later spec)

| Topic | Spec |
|-------|------|
| Hybrid materialization (agents/skills/blocks → disk), runtime re-pointing | §2 |
| `.mneme/profile.lock` (lockfile) | §2 |
| `source=profile:<name>` provenance, `Forget` of marked rules | §2 |
| `use`/`default` verbs (writing the pin, global default in config) | §3 |
| Precedence (pin > global default > vanilla) | §3 |
| SessionStart hook + actionable nudge/gate | §3 |
| Team-memory integration (provenance exclusion, anti-zombie) | §4 |
| Assisted creation (`profile new`) + `mneme-init` integration | §5 |
| Migrating the default OSS profile (assets → profile format) | §6 |
| Scaffolding (`/new-project`, `/new-app`, `scaffolds/`, `_blueprints/`) | §7 |

## Testing notes

`internal/profile`'s tests use only `t.TempDir()` and local git fixtures
(`git init` + local `git config user.name/user.email`, never `--global`,
never network) — the leaf never resolves `HOME` or the process git identity,
so no test needs `gitident.Reset()`. `scripts/testguard.sh` additionally
checks that no test leaves a `profiles/` directory behind in the sandboxed
test `HOME` (SPEC-091 §1 AC13), the same invariant it already enforces for
`projects/*.db`/`global.db`.
