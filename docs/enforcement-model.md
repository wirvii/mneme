# Enforcement Model

## When to read this

- Adding a new subagent to mneme
- Debugging why an edit was blocked
- Onboarding to mneme's role-based architecture

## Critical Rules

1. Read-only agents (`architect`, `qa-tester`) MUST NOT include `Edit`,
   `Write`, `MultiEdit`, `NotebookEdit`, or `Bash` in their `tools:` allowlist.
2. Implementer agents (`backend`, `frontend`, `bug-hunter`) MUST include the
   full edit toolset in their `tools:` allowlist.
3. No read-only agent MAY use `permissionMode: bypassPermissions`. That flag is
   reserved for implementers in autonomous runs where Claude Code permission
   prompts would interrupt the workflow.
4. All subagents MUST include `mcp__mneme__*` (wildcard) to retain access to
   mneme memory tools (`mem_save`, `mem_search`, `mem_context`, etc.).
5. The orchestrator (principal) MUST NOT edit source paths directly. The
   `mneme hook enforce-delegation` guard blocks this and logs every attempt —
   except a non-whitelisted path with no implementer subagent to own it,
   which the manifest-aware ownership bridge (SPEC-068, in-process since
   SPEC-069) allows through as a conscious fallback. See "Manifest-aware path
   ownership (SPEC-068)" below.

## Two-Layer Enforcement

### Layer 1 — Capability allowlist (primary)

Every subagent `.md` file under `internal/install/assets/agents/` declares an
explicit `tools:` allowlist in its YAML frontmatter. Claude Code enforces this
natively: when a tool is absent from the allowlist, the subagent cannot invoke
it, regardless of what the prompt says.

| Role | `tools:` allowlist |
|---|---|
| `architect` | `Read, Grep, Glob, NotebookRead, BashOutput, mcp__mneme__*` |
| `qa-tester` | `Read, Grep, Glob, NotebookRead, BashOutput, mcp__mneme__*` |
| `diagnostician` | `Read, Grep, Glob, NotebookRead, BashOutput, Bash, mcp__mneme__*` — Bash for log reading; NO Edit/Write/MultiEdit |
| `backend` | `Read, Grep, Glob, NotebookRead, NotebookEdit, BashOutput, Edit, Write, MultiEdit, Bash, mcp__mneme__*` |
| `frontend` | `Read, Grep, Glob, NotebookRead, NotebookEdit, BashOutput, Edit, Write, MultiEdit, Bash, mcp__mneme__*` |
| `bug-hunter` | `Read, Grep, Glob, NotebookRead, NotebookEdit, BashOutput, Edit, Write, MultiEdit, Bash, mcp__mneme__*` |

### Layer 2a — Go rules engine (role-aware, SPEC-043)

`mneme hook pre-tool-use` (Go binary) evaluates **DB rules** against every
`Edit`, `Write`, `MultiEdit`, and `NotebookEdit` call. Since SPEC-043 the engine
is **role-aware**:

- Resolves the caller role from the payload's five `agent_id` locations
  (multi-key, same `resolveCaller` logic the enforce-delegation guard uses).
- Supports `agent:orchestrator|subagent|*` selectors in `applies_to`.
- **Degrades block→warn** for subagents on rules without an explicit `agent:`
  selector: the rule appears as context but does not exit 2.
- `tool:Bash` is **context-only** in this layer — Bash does not appear in
  `mutatingTools`; a rule with `tool:Bash` never triggers from pre-tool-use Go.

This means `block` delegation rules (e.g. the historic `019e4261` rule) now
correctly block only the orchestrator and degrade for legitimate subagent
implementers, eliminating the need to keep them at `warn` permanently.

### Layer 2b — `mneme hook enforce-delegation` guard (defense in depth, orchestrator-only)

`mneme hook enforce-delegation` is a `PreToolUse` hook — a Go subcommand
registered as `mneme hook enforce-delegation`, portable (no path to the home
directory) — that reads the Claude Code hook payload from stdin. It detects
whether the caller is the orchestrator by the multi-key `agent_id` resolution
(introduced in SPEC-042) and checks all five known payload locations.

Through v1.20.0 this guard was an embedded ~640-line bash script
(`~/.claude/hooks/enforce_delegation.sh`) registered with an absolute path.
SPEC-069 ported its decision logic in-process to Go: `internal/enforcement`
(a leaf package: stdlib + `internal/shell` only) implements the pure decision
functions (`IsWhitelisted`, `EvaluateFileTool`, `EvaluateBash`);
`internal/cli/hook.go`'s `runHookEnforceDelegation` does the I/O wiring
(stdin parsing, caller resolution, manifest lookup) and injects an in-process
`OwnershipFunc` closure over `resolvePathOwnership` — no subprocess spawn to
`mneme hook tokenize` or `mneme hook path-owned` remains. The embedded
`enforce_delegation.sh` asset is now a ~6-line compat shim
(`exec mneme hook enforce-delegation`), kept only so a pre-existing
absolute-path registration keeps working until it is re-registered.

When the orchestrator attempts a file-mutating tool (`Write`, `Edit`,
`MultiEdit`, `NotebookEdit`, or a protected `Bash` command) against a path
outside the static whitelist, the guard consults the manifest-aware ownership
bridge (see "Manifest-aware path ownership" below) in-process and only blocks
when it reports a block. When it blocks, the guard:

1. Prints a BLOQUEADO message to stderr, naming the owning subagent role when
   the ownership bridge reported one.
2. Calls `service.Save` (in-process, best-effort) to log the attempt as a
   queryable discovery memory — a logging failure never changes the exit code.
3. Exits with code 2, which causes Claude Code to reject the tool call.

The guard does NOT discriminate by agent role (only by the presence of
`agent_id`). Role boundaries for subagents are enforced entirely by Layer 1.

### Manifest-aware path ownership (SPEC-068)

`resolvePathOwnership` (`internal/cli/hook.go`) is the pure decision function
the enforce-delegation guard consults, in-process, for every non-whitelisted
target instead of blocking directly. It is the same function the standalone
`mneme hook path-owned <path>` subcommand exposes. It reads the project's
`subagents/manifest` memory (read-only, same lightweight pattern as the Go
rules engine's DB access) and decides:

| Manifest state | Path state | Result |
|---|---|---|
| Present, non-empty | Owned by an implementer subagent's `areas` glob | **BLOCK** — names the owning role |
| Present, non-empty | Not owned by any implementer | **ALLOW** — legitimate orchestrator fallback |
| Absent, or present but empty `[]` | (any) | **BLOCK** ("legacy") — deny-by-default, protects projects that have not run the `mneme-init` grill yet |
| Hard failure (config/DB unreadable, corrupt JSON) | (any) | **ALLOW** — fail-open, consistent with the rest of this hook |

#### Manifest lookup is project-scoped (SPEC-084 D4)

The manifest read is `WHERE topic_key = 'subagents/manifest' AND project = ?
AND scope = 'project' AND deleted_at IS NULL ORDER BY updated_at DESC, id
DESC LIMIT 1` — a project's SQLite database can legitimately contain manifest
rows for *other* projects (e.g. test runs, an imported/merged database), and
the `project`/`scope` filter is what keeps the lookup from picking one of
those up instead of the caller's own manifest. `project`+`scope` complete
`idx_memories_upsert`'s real unique key (`topic_key, project, scope`), so the
`ORDER BY` is a deterministic-on-its-own fallback rather than the load-
bearing part of the guarantee.

#### `areas` glob semantics (SPEC-084 D2)

A manifest `areas` entry is matched against the target path as the union of
two readings — the entry itself as a literal glob, **and** the entry treated
as a directory whose descendants it also owns:

```
areaMatches(area, path) := Match(cleaned, path) || Match(cleaned+"/**", path)
```

where `cleaned` trims surrounding whitespace and a leading `./` / trailing
`/`. This means a bare directory area (`apps/web-ui`, the shape the
`mneme-init` grill has always generated) owns every path underneath it, not
just a literal path equal to the area string — `Match("apps/web-ui",
"apps/web-ui/lib/version.ts")` alone is `false`, which is why this needed
fixing. An area that is already a glob (`internal/**`) is unaffected — the
union is idempotent. An empty or whitespace-only area is ignored (it can
never expand into `**`, which would own the whole repository); `.` or `./`
explicitly resolve to `**`, owning the whole repository on purpose. The
normalisation lives in the matcher (`internal/cli/hook.go`), not in manifest
generation (`internal/service/subagents.go`) — every manifest already
written keeps its original `areas` content and checksum; only how the hook
interprets it at match time changed.

This made retiring the six global subagents (done in SPEC-073) non-destructive:
a project that never ran the grill has no manifest, so every non-whitelisted
edit keeps blocking exactly as it does today; a project with a partial manifest
only blocks the areas that actually have a delegate, letting the orchestrator
supply the rest as a documented fallback (see the operating manual's
"Orchestrator fallback" section).

Note on `agent_type`: Claude Code injects `agent_id` into the hook payload for
subagents but does NOT inject `agent_type`. The hook therefore cannot identify
which subagent role is attempting an action — only whether the caller is the
principal or a subagent. This is by design: Layer 1 handles role discrimination.

#### Inherent limits of Layer 2

Layer 2 is designed to stop the **cooperative orchestrator** from accidentally
editing source code instead of delegating. It is NOT a sandbox. The following
bypass patterns are out of scope by design (closing them would require a full OS
sandbox, which is beyond mneme's scope):

- **base64 / eval**: `echo <b64> | base64 -d | bash` — the hook sees an
  innocuous-looking command and cannot decode arbitrary indirection.
- **Arbitrary binaries**: custom binaries or scripts that write files are not
  in the hook's detection set.
- **Pipe to unlisted interpreters**: `ruby -e`, `perl -e`, `php -r`, etc.

The **primary defense** against subagent misbehavior remains **Layer 1**
(capability allowlist in `agents/*.md`). If a subagent's `tools:` allowlist
does not include `Edit`/`Write`/`MultiEdit`, Claude Code will reject those tool
calls before the hook is even invoked.

### Querying blocked attempts

Every orchestrator block produces a discovery memory. Query them with:

```bash
mneme search "Blocked edit"
```

## Project-scoped opt-in registration (EPIC agnostic-agents, SS-6)

Everything above describes the **global** installation: `mneme install
claude-code` registers both Layer 2 hooks (`mneme hook pre-tool-use` and
`mneme hook enforce-delegation`, both portable subcommands since SPEC-069) in
`~/.claude/settings.json`, and — during the agnostic-agents transition
(SPEC-052 §9) — it still does so **unconditionally, every release**, so the
repos that have not migrated to per-project subagents keep working exactly
as before.

SPEC-052 (§5.2/§8.2) also introduces a second, **independent, opt-in**
registration path at **project scope**, for repos that generate per-project
subagents via the `mneme-init` skill (SPEC-058):

- If `mneme-init` generates **implementer** subagents (`backend`, `frontend`,
  `bug-hunter` archetypes), it **offers** the delegation hook. If the user
  accepts, the skill runs `mneme delegation-hook enable`, which merges the
  same two PreToolUse commands into **`<repo>/.claude/settings.json`**
  instead of the global file.
- If no implementer subagents exist, the hook is **not** offered — the
  project operates single-agent (same precedent as Codex/SPEC-049, which
  never installs this hook).
- The choice is recorded per role in the subagent manifest's
  `enforcement_hook` field (`subagent_write`'s `enforcement_hook` parameter),
  independent of whether the project-level registration command was actually
  run.

### Verification: does Claude Code respect project-scope `PreToolUse` hooks?

**Yes — confirmed against the official Claude Code docs (`hooks` and
`settings` reference pages, fetched 2026-07-08).** The hook-locations table
states plainly:

| Location | Scope | Shareable |
|---|---|---|
| `~/.claude/settings.json` | All your projects | No, local to your machine |
| `.claude/settings.json` | Single project | **Yes, can be committed to the repo** |
| `.claude/settings.local.json` | Single project | No, gitignored |

The settings-precedence page confirms the same location is used for
"Settings" (which include hooks) at Project scope, distinct from User scope
(`~/.claude/settings.json`) and Local scope
(`.claude/settings.local.json`). No additional per-hook trust gate beyond
Claude Code's normal folder-trust onboarding is documented for committed
project hooks (unlike, e.g., `allow` permission rules or
`autoMemoryDirectory`, which the docs explicitly call out as requiring the
workspace-trust step).

Given this, **no self-disabling global-hook fallback was needed** — the
opt-in mechanism is a straightforward project-scoped `settings.json` patch,
using the exact same append-if-absent merge logic (`PatchHooks`) the global
path already uses.

### `mneme delegation-hook` commands

```bash
mneme delegation-hook enable [path]   # register both PreToolUse entries in <path>/.claude/settings.json (default: cwd)
mneme delegation-hook disable [path]  # remove them, leaving every other setting untouched
mneme delegation-hook status [path]   # report whether both entries are currently registered
```

Nothing is duplicated per project: both entries are the same portable
`mneme` subcommand strings the global installation registers (SPEC-069), so
enabling the project-scope entry only ever patches `settings.json` — there is
no script to write or keep in sync. `mneme delegation-hook enable` also
strips any pre-existing legacy `enforce_delegation.sh` absolute-path entry
from the repo's `settings.json` before adding the portable one (the same
strip-then-add migration `PatchDelegationHook` performs globally).

Retiring the *global* registration entirely (so `mneme install claude-code`
stops shipping it by default) is **SS-7**, a separate release — not done
here, to avoid breaking the repos that have not yet migrated to per-project
subagents.

## Automated Checks

| Check | What it verifies | How to fix |
|---|---|---|
| **Subagent allowlist exists** | Every agent `.md` has a `tools:` line in its YAML frontmatter | Add an explicit `tools:` line |
| **Read-only agents minimal** | `architect` and `qa-tester` have no edit tools | Remove `Edit`, `Write`, `MultiEdit`, `NotebookEdit`, `Bash` |
| **Implementer agents full** | `backend`, `frontend`, `bug-hunter` have the full edit toolset | Add the missing tools |
| **mcp__mneme__\* present** | Every agent retains mneme memory access | Add `mcp__mneme__*` to the `tools:` line |
| **No stray bypassPermissions on read-only** | `architect` and `qa-tester` do not set `permissionMode: bypassPermissions` | Remove the line |
| **Hook installed** | `mneme hook enforce-delegation` is registered as a `PreToolUse` hook | Run `mneme install claude-code` |
| **Hook logs to mneme** | Blocked attempts produce a `discovery` memory | Check stderr for a "log discovery: save failed" warning |

## Adding a new subagent

1. Decide its role: read-only or implementer.
2. Create `internal/install/assets/agents/<name>.md`.
3. Use the appropriate `tools:` template from the table above.
4. If read-only: omit `permissionMode: bypassPermissions`.
5. If implementer: add `permissionMode: bypassPermissions` with the comment
   explaining why it is intentional.
6. Update this document with the new agent.
7. Rebuild mneme (`make build`). The asset is the canonical role/permission
   source consumed by per-project subagent generation (`internal/subagents`);
   as of SPEC-073 `mneme install claude-code` no longer deploys agent files
   globally, so there is nothing to "install" — the new role becomes available
   to the `mneme-init` grill.
8. Add tests in `internal/install/install_test.go` following the patterns of
   `TestAgentAssets_ReadOnlyAllowlists` and `TestAgentAssets_ImplementerAllowlists`.

Note: the enforce-delegation guard blocks the orchestrator by detecting the
absence of `agent_id`. No change to the hook source is required when adding
subagents, because their capability boundary is the `tools:` allowlist.

## Debugging a blocked attempt

| Symptom | Likely cause | Fix |
|---|---|---|
| Architect cannot read a file | `Read` missing from allowlist | Add `Read` |
| QA tester cannot run `make test` | `Bash` is not in the read-only allowlist (by design) | Have the orchestrator run tests and pass output to qa-tester via context |
| Backend cannot run tests | `Bash` missing from implementer allowlist | Add `Bash` |
| Orchestrator edited a file despite the hook | Hook not installed or not running | Run `mneme install claude-code`, restart Claude Code |
| Edit blocked but no discovery memory saved | `mneme` CLI not on PATH at hook runtime | Verify `which mneme`; reinstall binary |
| Subagent edit blocked unexpectedly | Agent's `tools:` allowlist missing the tool | Add the tool to the agent's frontmatter and reinstall |

## How the hook decides

### Go rules engine (Layer 2a)

```
read stdin (PreToolUse JSON payload)
  resolve caller: check agent_id, session.agent_id, subagent.agent_id,
                  context.agent_id, metadata.agent_id
    → any non-empty = subagent; all empty = orchestrator
  if tool not in {Edit, Write, MultiEdit, NotebookEdit} → exit 0
  load rules from project + global DB (read-only)
  match rules against tool + path + caller role
    → rules with agent: selector: evaluated as-is
    → rules without agent: selector + block + caller=subagent: Effective=warn
  if MaxSev(effective) == block → exit 2
  else emit markdown context → exit 0
```

### `enforce-delegation` guard (Layer 2b — orchestrator only, in-process since SPEC-069)

```
read stdin (PreToolUse JSON payload)
  if agent_id present (any of 5 locations) → exit 0  (subagent)
  if tool not in {Write, Edit, MultiEdit, NotebookEdit, Bash} → exit 0
  resolve config/project/manifest once (fail-open on any hard error)
  own := closure over resolvePathOwnership(target, cwd, manifest)
  if Bash:
    decision := enforcement.EvaluateBash(command, home, own)
  else:
    decision := enforcement.EvaluateFileTool(file_path, home, own)
  if decision.Block:
    print BLOQUEADO message (naming the owning role when known) to stderr
    log discovery memory (best-effort, in-process service.Save)
    exit 2
  else:
    exit 0
```

Whitelist (always-allow fast-path, never reaches the ownership bridge):
`.claude/**`, `~/.claude/**`, `~/.mneme/**`, `CLAUDE.md` (any location),
`**/docs/*.md`, `.claudeignore`, `/tmp/**`, `/private/tmp/**`.
