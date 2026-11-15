# Enforcement Model

## When to read this

- Adding a new subagent to mneme
- Debugging why an edit was blocked
- Onboarding to mneme's role-based architecture

## Critical Rules

1. Read-only agents MUST NOT include `Edit`, `Write`, `MultiEdit`, or
   `NotebookEdit` in their `tools:` allowlist. `architect` additionally
   excludes `Bash`; `qa-tester` and `diagnostician` include `Bash` (to run
   gates / read logs respectively) without ever gaining an edit tool — see
   `IsImplementer` (SPEC-087 D1): the capability barrier is the presence of
   an edit tool, not `Bash`, and not `permissionMode`.
2. Implementer agents (`backend`, `frontend`, `bug-hunter`) MUST include the
   full edit toolset in their `tools:` allowlist.
3. `permissionMode: bypassPermissions` is reserved for roles that need
   autonomous, unattended tool calls (implementers, and — since SPEC-087
   D2b — `qa-tester`, so its own gates run without a human permission
   prompt on every `Bash` call). It is never, on its own, what makes a role
   an implementer: `IsImplementer` reads the `tools:` allowlist, not this
   flag (SPEC-087 D1). It stays on deliberately (SPEC-185 D5):
   without `bypassPermissions` every Bash call would prompt for approval and the hook is the layer that denies.
   The factory would otherwise stop being autonomous; Claude Code still runs
   the `PreToolUse` hook in this mode: `mneme hook
   enforce-delegation` rejects writes under mneme's data (layer A) and writes
   on risk paths, dependency changes and test removals (layer B) whatever
   the permission mode.
4. All subagents MUST include `mcp__mneme__*` (wildcard) to retain access to
   mneme memory tools (`mem_save`, `mem_search`, `mem_context`, etc.).
5. The orchestrator (principal) MUST NOT edit source paths directly. The
   `mneme hook enforce-delegation` guard blocks this and logs every attempt —
   except a non-whitelisted path with no implementer subagent to own it,
   which the manifest-aware ownership bridge (SPEC-068, in-process since
   SPEC-069) allows through as a conscious fallback. See "Manifest-aware path
   ownership (SPEC-068)" below.

## Two-Layer Enforcement

### Layer 1 — Native capability projection (primary)

Every canonical project role is rendered into both native formats. Claude Code
receives an explicit `tools:` allowlist in `.claude/agents/*.md` and enforces it
directly. Codex receives the role's sandbox intent in
`.codex/agents/*.toml`, but mneme does not count that declaration as a security
boundary: real Codex 0.147.0 testing showed that a child can inherit the
parent's workspace permissions. mneme therefore permits installation on the
stable 0.147.0 release but warns that native multi-agent containment is not
verified there. Codex 0.148.0-alpha.19 or newer supplies identity-bearing
`SubagentStart` and child `PreToolUse` events, which let the same Go guard
enforce the role and its declared ownership areas.

Each Codex role also receives its own local, role-bound mneme MCP server. The
server filters the advertised tools and repeats the authorization check when a
tool is called: delegated roles cannot advance lifecycle state; and only
`architect` can write the criteria document. Those allowed calls are
pre-approved for that local server because a child running under
`approval_policy = "never"` cannot answer an MCP approval prompt. Pre-approval
therefore follows, and never replaces, the role filter.

| Role | `tools:` allowlist |
|---|---|
| `architect` | `Read, Grep, Glob, NotebookRead, BashOutput, Skill, WebSearch, WebFetch, mcp__mneme__*` |
| `qa-tester` | `Read, Grep, Glob, NotebookRead, BashOutput, Bash, Skill, WebSearch, WebFetch, mcp__chrome-live__*, mcp__plugin_chrome-devtools-mcp_chrome-devtools__*, mcp__plugin_playwright_playwright__*, mcp__mneme__*` — Bash + `permissionMode: bypassPermissions` since SPEC-087 D2/D2b, so its own gates (`go test`, lint, build) run unattended; still no Edit/Write/MultiEdit/NotebookEdit — the capability barrier stays the allowlist, not the permission mode (see `IsImplementer`, SPEC-087 D1). The three `mcp__*` browser patterns are SPEC-132 D1/D2/D3: qa-tester can now open a real screen and look at it, and is no longer read-only over DATA (though it stays read-only over CODE) |
| `diagnostician` | `Read, Grep, Glob, NotebookRead, BashOutput, Bash, Skill, mcp__mneme__*` — Bash for log reading; NO Edit/Write/MultiEdit. SPEC-087 D2/decision-3 deliberately does NOT add WebSearch/WebFetch here |
| `backend` | `Read, Grep, Glob, NotebookRead, NotebookEdit, BashOutput, Edit, Write, MultiEdit, Bash, Skill, WebSearch, WebFetch, mcp__mneme__*` |
| `frontend` | `Read, Grep, Glob, NotebookRead, NotebookEdit, BashOutput, Edit, Write, MultiEdit, Bash, Skill, WebSearch, WebFetch, mcp__chrome-live__*, mcp__plugin_chrome-devtools-mcp_chrome-devtools__*, mcp__plugin_playwright_playwright__*, mcp__mneme__*` — the three `mcp__*` browser patterns are SPEC-132 D1/D2: frontend can open the screen it just built. backend and bug-hunter deliberately do NOT get them |
| `bug-hunter` | `Read, Grep, Glob, NotebookRead, NotebookEdit, BashOutput, Edit, Write, MultiEdit, Bash, Skill, WebSearch, WebFetch, mcp__mneme__*` |
| `spec-reviewer` | `Read, Grep, Glob, NotebookRead, Skill, mcp__mneme__codegraph_search, mcp__mneme__codegraph_context, mcp__mneme__codegraph_callers, mcp__mneme__codegraph_impact` — the SPEC-181 fresh-context reviewer: read-only, no shell, no web, no memory tools and no `spec_doc_write` (it reports in its final message and the architect writes the report). In Codex the `CallerPolicy` derives the same four tools from this table (`subagents.MCPToolAllowlist`) |

All seven roles carry `Skill` (SPEC-169 D7): a subagent with an explicit
`tools:` list cannot load a skill otherwise, and layer 1 now points each role
at its skill. `Skill` only loads instructions — it edits and runs nothing, so
it never counts toward `IsImplementer`.

`IsImplementer(role)` (`internal/subagents/permissions.go`) reports edit
capability by reading this actual toolset — `Edit`/`Write`/`MultiEdit`/
`NotebookEdit` — never `permissionMode` (SPEC-087 D1): `qa-tester` and
`backend` now share `permissionMode: bypassPermissions` without sharing
edit capability, so the old `PermissionMode == bypassPermissions` proxy
would have misclassified `qa-tester` as an implementer the moment D2b
landed. `Bash` never counts toward "implementer" either way.

**Browser capability (SPEC-132).** Only `qa-tester` and `frontend` carry the
three browser-server MCP patterns above (D1) — `backend`, `bug-hunter`,
`architect`, and `diagnostician` do not, and never gained web navigation to
begin with (`architect`'s own `WebSearch`/`WebFetch` is SPEC-087 D2, a
different, older grant). Granting it made `qa-tester` stop being read-only
over **data**, even though it stays read-only over **code**: no edit tool
was added to its allowlist, but a browser can submit a form or press a
delete button in whatever application it points at, and nothing in mneme
today stops it from pointing at a real one (D3 — the only protection is the
warning written into both roles' profile text, see
`internal/subagents/assets/agent-fixed.md`'s `visual-certification`
section; a technical barrier over navigation targets is tracked separately,
BL-208). This capability is **Claude Code only** (D5): Codex's own
projection (`RenderCodex`) never emits a tools list at all, so there is
nothing to widen there, and the profile text says so explicitly rather than
letting an agent promise a screen it cannot open on that runtime.

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

Through SPEC-084 the guard did not discriminate by agent role at all — it
only ever ran against the orchestrator (`agent_id` absent); a subagent
invocation short-circuited to allow immediately. **SPEC-086 changed this**:
a real captured PreToolUse payload (2026-07-15,
[[enforcement/payload-pretooluse-agent-type-capturado]]) confirmed Claude
Code DOES send a top-level `agent_type` field carrying the literal subagent
role name (`"backend"`, `"qa-tester"`, ...), settling a question that had
been open since SPEC-042/043. The guard now resolves a full
`CallerIdentity{IsSubagent, AgentID, Role, RoleSource}` (`resolveIdentity`,
`internal/cli/hook.go`) and, for a subagent, contains it to its own
manifest-declared areas — see "Subagent containment" below. Layer 1 (the
capability allowlist) remains the primary, always-on defense regardless of
this Layer 2 addition.

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

### Subagent containment (SPEC-086 D5)

Since SPEC-086, a subagent invocation is evaluated by the **same**
`evaluateDelegation` → `enforcement.EvaluateFileTool`/`EvaluateBash` pipeline
as the orchestrator's — only the injected `OwnershipFunc` closure differs
(`subagentOwnershipFunc` instead of `resolvePathOwnership`). This is
deliberately NOT the orchestrator's deny-by-default lookup: a subagent never
inherits "legacy" blocking just because a project has no manifest, or
because its role is absent from one (e.g. `general-purpose`) — mneme never
contains an agent it did not generate.

| Condition | Result |
|---|---|
| Path whitelisted, out of tree, or empty | **ALLOW** |
| No manifest for the project | **ALLOW** — no legacy inheritance for subagents |
| `agent_type` absent (`agent_id` present, `RoleSource="unresolved"`) | **ALLOW** + a mandatory stderr warning on every such invocation (never silent — see the D2 precedent below) |
| Role absent from the manifest | **ALLOW** — logged |
| Manifest entry has `areas_complete` false/absent | **ALLOW** — logged as `would_block` for future evidence, never a real block regardless of mode |
| `areas_complete: true` and the path matches the role's own declared areas | **ALLOW** |
| `areas_complete: true` and no match | `would_block` in **warn** mode (the default) / **BLOCK** in **block** mode, naming the role that DOES own the path when one exists |

Two independent knobs, never conflated: `areas_complete` (set once, by a
human, in response to the `mneme-init` grill's explicit completeness
question — see `internal/install/assets/skills/mneme-init/SKILL.md`)
certifies the **data**; `[delegation] subagent_containment` in
`~/.mneme/config.toml` (`off`/`warn`/`block`, global default `warn`, with
optional per-project `[delegation.projects."<slug>"]` overrides via
`Config.SubagentContainmentMode`) controls whether the project **acts** on
it yet. Incomplete data never blocks, no matter what the mode is set to.

`agent_type` is a runtime-observed contract. It is present in the repository's
minimum-version fixtures for Claude Code and Codex and is checked by install
diagnostics. A future version could still stop sending it. That is exactly what
`RoleSource="unresolved"` detects: the guard fails open (never blocks 8+
repos' worth of subagents over a version bump) but never silently — the
precedent is SPEC-042 D2's "noisy jq guard" for the equivalent situation
with `agent_id` resolution. Every containment decision (and the unresolved
case) is recorded to `internal/enforcelog` (local JSONL, 0600, never shared
to the team vault — see its own package doc for the explicit privacy
contract), which `mneme delegation-hook report` and `mneme delegation-hook
promote` read to decide when a project has enough evidence to move from
`warn` to `block`.

### Lifecycle-tool denial (SPEC-087 D5)

A subagent-containment allow does not mean a subagent may do anything MCP
exposes. `enforce-delegation` separately, unconditionally denies five SDD
lifecycle tools to any resolved subagent — total block, no mode:

| Tool | Result |
|---|---|
| `mcp__mneme__spec_advance` | **BLOCK** — the observed defect (SPEC-063's premature `done`) |
| `mcp__mneme__spec_quick` | **BLOCK** — disguised advance (draft→rationale→implementing) and an orchestrator-only operation |
| `mcp__mneme__backlog_archive` | **BLOCK** — discarding work and irreversibly freezing the spec it governs is the owner's call, channelled by the orchestrator (SPEC-125 D11/DD11) |
| `mcp__mneme__lane_override` | **BLOCK** — bypassing the trivial-lane auditor is decided by the orchestrator or the owner, never by the author of the change (SPEC-175) |
| `mcp__mneme__lane_reclassify` | **BLOCK** — changing the lane of the spec being implemented is the orchestrator's call; a failed audit's `Remedy` invites a reclassification and the author of the change must not act on it (SPEC-184) |
| `mcp__mneme__spec_pushback`, `mcp__mneme__spec_reject`, `mcp__mneme__spec_doc_write`, `mcp__mneme__mem_*`, `mcp__mneme__codegraph_*` | **ALLOW** |

`lifecycleTools` (`internal/cli/hook.go`) is an **exact-match** set —
`strings.HasPrefix(tool, "mcp__mneme__spec_")` would also catch
`spec_pushback` and `spec_doc_write`. The check runs **before** the
`delegationTools` filter (MCP tool names are never file/Bash tools) and
**before** the `RoleSource=="unresolved"` short-circuit above: it only
needs `identity.IsSubagent`, never `agent_type`, so it keeps working even
if Claude Code ever stops sending `agent_type` and subagent containment
loses its signal. No discovery memory on block (a contained subagent did
not bypass SDD); a best-effort `enforcelog` event is recorded instead
(`Reason: "lifecycle_tool_denied_to_subagent"`).

**The same verbs over Bash (SPEC-175).** Denying only the MCP names left the
terminal open: a subagent with `Bash` could type `mneme spec advance`. A second
guard, right after the one above, runs `enforcement.EvaluateLifecycleCommand`
(pure, over `shell.SimpleCommands`) on a subagent's Bash command and rejects
`mneme spec advance|quick`, `mneme sdd import`, `mneme backlog archive`,
`mneme lane override`, `mneme lane reclassify` (SPEC-184) and
`mneme mem synthesis-sweep` (SPEC-187, dry run included: the matcher does not
read flags) (exit 2; `Reason: "lifecycle_command_denied_to_subagent"`,
no discovery memory). A command whose own simple command names an isolated data
directory (`--data-dir`, `HOME=`, `USERPROFILE=`, `MNEME_DATA_DIR=`, non-empty
and not the real one) is allowed, so mneme itself can be tested. Limits, all
accepted: R1 it is a heuristic, not a sandbox (`eval`, a variable holding the
binary, a renamed binary, `xargs`, script files, unlisted wrappers pass); R2 a
hand-written path to the real directory that is not a known literal passes; R3
the git hooks of `mneme sdd hooks install` run `mneme sdd import` against the
real database on `git pull`/`git checkout` and the hook cannot see that
subprocess (the risk behind it, importing files an agent wrote by hand, is
closed at its source by the `.mneme/sdd` guard below, SPEC-179). Details and the
literal message: `docs/HOOKS.md`.

**Codex.** Children carry `agent_id`/`agent_type` and send the shell tool as
`Bash`, so the hook guards apply unchanged. The role-bound MCP server
(`CallerPolicy` in `internal/mcp/caller_policy.go`) is the second layer and
denies the same five tools: `spec_advance`, `spec_quick`, `backlog_archive`,
`lane_override` and `lane_reclassify` (SPEC-184). `backlog_archive` was a parity
gap before SPEC-175.

This closes the gap `spec_doc_write` (SPEC-087 D3) opens on purpose: a
subagent can now write its own entregable (`spec.md`/`plan.md`/
`qa-report.md`/`changes.md`) without ever being able to advance the spec
that entregable belongs to. See `docs/HOOKS.md`'s own "Lifecycle-tool
denial" section for the exact block message and `docs/subagents.md` for
`mneme subagents regen` (the remediation the message names).

### `.mneme/sdd` guard (SPEC-179)

Nobody writes by hand in `.mneme/sdd/**` (orchestrator, subagent with a role,
subagent without one; Claude Code and Codex). The importer applies those files
as written, with no human approval gate and no rules about who moves a spec, so
an agent-written `status: done` would be imported by the next `git pull` of
anyone. Only the mneme binary writes them, and it never passes through hooks.

The guard is the **first** one in `runHookEnforceDelegation`
(`checkSDDFilesGuard`), before the lifecycle guards above, the `delegationTools`
filter (Codex's `apply_patch` is not in it), the unresolved-role short-circuit,
`evaluateDelegation`, the trivial-inline grant (SPEC-171) and area containment
(SPEC-086). It reads neither identity, manifest, config nor database, so a
project with no manifest, containment `off`, or a trivial spec whose scope covers
`.mneme/**` is blocked all the same. It judges `Write`, `Edit`, `MultiEdit`,
`NotebookEdit`, Bash (redirections, `sed -i`, `tee`, `cp`, `mv`, `rm`,
`truncate`, `touch`, `rmdir`, `ln`, `install`, `dd`, `perl -i`, inline
`python`/`node` with a write signal, `git checkout|restore --ours|--theirs|
--source`, ancestors such as `rm -rf .mneme`, globs, `cd` into the tree) and
Codex's `apply_patch`, through the leaf `internal/enforcement` (`sddfiles*.go`),
with symlink resolution and the "does this directory hold `.mneme/sdd`" question
injected by `internal/cli`.

`IsWhitelisted` is unchanged: the whitelist still allows `.mneme/**`, except
that this guard runs before it is consulted, so in practice `.mneme/sdd/**` is
the exception to that entry. Rejections go to the `enforcelog`
(`Reason: "sdd_files_write_denied"`, `Caller` orchestrator or subagent), never to
a discovery memory.

Limits, all accepted: it is a second barrier, not a sandbox (`eval`, variables
holding the path, `xargs`, `find -exec`, `rsync`, `git apply`/`git am`, script
files, unlisted interpreters, `git checkout <commit> -- <path>`); the Codex
`apply_patch` payload is not verified live; a conflict in these files is
resolved by a person. Details, the form table and the literal message:
`docs/HOOKS.md`.

### Data-directory guard (SPEC-185 A)

No subagent, with a resolved role or not, writes by hand under mneme's own
data. Three roots, deduplicated (they usually coincide in `~/.mneme`): the data
directory (`[storage] data_dir`, `MNEME_DATA_DIR`: project databases,
`global.db`, the enforcelog), the workflow directory (`[workflow] dir`,
`MNEME_WORKFLOW_DIR`: every `tarea.md`) and the folder of `config.toml`
(`filepath.Dir(config.DefaultPath())`, always `~/.mneme`). Specs, memory, the
risk declarations layer B trusts and the hook's own log live there; a subagent
changes them only through the MCP tools, which validate and record who writes.

The guard (`checkDataDirGuard`, pure decision in
`internal/enforcement/datadir.go`) runs right after the `.mneme/sdd` guard,
before the lifecycle guards, the `delegationTools` filter (to see Codex's
`apply_patch`), the whitelist (which allows `/.mneme/`) and the
unresolved-role short-circuit, which it also covers. It judges file tools by
their path, `apply_patch` by its headers, and Bash with three rules, one level
into `bash -c`: (a) a write redirection under a root; (b) any argument under a
root, whatever the program (`sqlite3`, `cat`, `cp`, `python`: Bash does not
tell readers from writers; a subagent reads with `Read`), except `mneme` itself,
`go run ./cmd/mneme`, and `cd`/`pushd`; (c) `cd`/`pushd` into a root when the
same line runs any other command, `mneme` included. Paths are normalised like
the `.mneme/sdd` guard (quotes, `~`, `$HOME`, Windows separators, the cwd,
symlinks) and compared without regard to case. If `config.toml` does not load,
the roots come from the defaults with `MNEME_DATA_DIR`/`MNEME_WORKFLOW_DIR`
applied: the guard never switches itself off. The orchestrator is not
restricted (it edits `config.toml` as the person's delegate and makes the
`sqlite3 .backup` copies tests need). Rejections go to the enforcelog
(`Reason: "data_dir_write_denied"`), never to a discovery memory.

### Risk denial (SPEC-185 B)

`checkRiskGuard` runs as step 13, after delegation, containment and the trivial
inline grant allowed the call: a call they block already has its message. It
judges, per write, three subjects:

- an **implementer** (archetype backend, frontend or bug-hunter, custom roles
  included) on file tools;
- **any subagent whose archetype carries Bash** (implementers, `qa-tester`,
  `diagnostician`) on Bash; `architect` and `spec-reviewer` never reach it;
- the **orchestrator** on any write that is not on its whitelist: a path no
  implementer owns (the SPEC-068 fallback) or one the trivial inline grant let
  through.

What it denies: a path that matches items 1, 2 or 5 of the shared table
(`risk.DefaultTable()` plus `[lane.risk_globs]`, the same list the lane auditor
detects and the inline grant excludes); a Bash command that adds, removes or
updates a named dependency (`go get <m>`, `npm install <p>`, `pip install <p>`,
`cargo add`...; installing from the lockfile — `npm ci`, `npm install` with no
package, `pip install -r`, `go mod tidy|download|verify` — is not); and item 6,
the test watch: an edit that adds a skip or focus marker to a test file, or
leaves it without test functions, and a Bash `rm`, `git rm` (not `--cached`),
`rmdir`, `> file`, or `mv` of a test file to a non-test name or out of the
tree.

Which writes are judged is decided **per write**, not by whether the ownership
bridge was consulted (Dp1c): `EvaluateBash` does consult it for `git rm` and for
any form with the word `install`, but never for `go get`, `npm add` or `cargo
add`, so "the bridge was asked" cannot tell a whitelisted write from the rest.
For the orchestrator the whole whitelist exempts a write (`IsWhitelisted`); for
a subagent only scratch space (`/tmp`, `/private/tmp`, the Windows `TempDir`)
and the data-directory roots do (`IsRiskExempt`), because the rest of the
whitelist would exempt a subagent working in `.claude/worktrees/`. An `mv`
destination is judged with the cwd: absolute, home-relative or `..` leaves the
tree; an existing directory receives the file's name.

| Subject | Write | Declaration | Decision | Enforcelog |
|---|---|---|---|---|
| Orchestrator, whitelisted | any | — | not judged | — |
| Orchestrator, unowned path | item 1/2/5 path, dependency command, test harm | declared / not | allow / block (names `spec_doc_write`) | `allowed_by_declaration` / `denied_risk` |
| Orchestrator, inline grant | item 1/2/5 path or test harm | not declarable | block | `denied_risk` |
| Subagent, unresolved role | any | — | allow with warning (layer A still applies) | `agent_type_unresolved` |
| Subagent, archetype not resolvable | any | — | allow with warning | `risk_archetype_unresolved` |
| Implementer, file tool | item 1/2/5 path or test harm | declared / not | allow / block | idem |
| Subagent with Bash | write form on a 1/2/5 path, dependency command, test removal | declared / not | allow / block | idem |
| Any | lockfile installs, reads, scratch, `mv a_test.go b_test.go` | — | allow, no event | — |
| Any | declarations unreadable (a `tarea.md` that is a directory or malformed, a database without `specs`) | — | allow with warning | `risk_lookup_failed` |
| Any | no project database, no open standard spec, no `tarea.md` | nobody declared | block | `denied_risk` |

**Where a declaration comes from.** Every spec with `status = implementing`,
`lane = standard`, not frozen by an archived backlog item; any of them is
enough. Per spec, in order: the last `Risk: item N (reason)` line outside code
fences of the `plan` section of its `tarea.md`, then of `diseno` (both written
only by the architect or the orchestrator), bounded by the spec's scope when it
has one; then the scope itself, which declares items 1, 2 and 5 for the paths it
covers but never item 6. A dependency command is never bounded by the scope. The
delivery section never declares: the implementer writes it. A trivial spec
declares nothing.

Each verdict is logged with `risk_item`, `spec`, `source` (`plan`, `diseno`,
`scope`) and `detail` (the marker, `loses all tests`, `removed`, `truncated` or
the Bash form). Messages and the full form table: `docs/HOOKS.md`.

### Role-scoped document-kind denial

A sibling rule, `model.SpecDocKindAllows` (consulted by `checkSpecDocKindScope`
in `internal/cli/hook_doc_kind.go` and by the Codex `CallerPolicy`), restricts
`spec_doc_write`'s `criteria`, `review-spec` and `review-plan` kinds to the
`architect` archetype — an implementer writing `criteria` would be rewriting
the exam it is judged by. The archetype is resolved through the project
manifest for a custom role name. Unlike
`lifecycleTools`, it fails OPEN when the caller's role cannot be resolved:
closing it would leave the architect unable to write the document at all
the moment `agent_type` stopped arriving in the payload.

The EPIC-calidad certification mechanism used to add a SECOND, tool-scoped map here, restricting `quality_sign` to the `qa-tester`
role (`roleScopedTools`) — **the first, and only, rule in the repo that failed CLOSED** when a
subagent's role could not be resolved, a deliberate exception to this whole
model's fail-open posture (a signature whose signer cannot be identified is
worse than no signature at all). SPEC-158 retired that map whole, along with `quality_sign` and the rest of the certification mechanism.

#### Task-section denial (SPEC-170)

`spec_doc_write` with `kind: "task"` replaces one section of a spec's
`tarea.md` (`docs/api/sdd.md`). Which subagent may write which section is
decided by `model.TaskSectionAllows` — the single table both enforcement
points consult: `que`/`diseno`/`plan` for `architect`; `entrega` for
`backend`, `frontend`, `bug-hunter`; `revision` for `qa-tester`;
`diagnostician` none; the orchestrator any.

- **Claude Code**: `checkTaskSectionScope` (`internal/cli/hook_task_section.go`),
  called once from `runHookEnforceDelegation` right after the `criteria`
  check. A denied call exits 2 with a message naming the section and the roles
  that may write it, and logs an enforcelog event
  (`role_scoped_task_section_denied`, target `task:<section>`).
- **Archetype resolution**: if `agent_type` is one of the seven archetype
  names it is used directly with no I/O; a custom role is looked up in the
  project manifest (`archetype` field, falling back to the role name).
- **Fail-open**: empty or unknown `section` (the service rejects it), an
  unresolved role (`agent_type` absent) and an unresolvable archetype
  (manifest unreadable or role not listed) are all allowed with a warning on
  stderr — the same posture as the `criteria` rule.
- **Codex**: `CallerPolicy.authorizeCall` applies the same table using the
  process's archetype.

#### Orchestrator trivial-inline (SPEC-171, on by default since SPEC-184)

The one exception to "the orchestrator never edits code": while a
**trivial-lane** spec is in `implementing`, the orchestrator may edit files
inside that spec's declared `scope` itself. **On by default since SPEC-184**,
a default chosen by the orchestrator on the owner's behalf and reversible.

- **Flag**: `[delegation] orchestrator_trivial_inline` in
  `~/.mneme/config.toml` (default `true`; `false` turns it off);
  `MNEME_DELEGATION_TRIVIAL_INLINE` (`1`/`true`, `0`/`false`) overrides the
  global value; a per-project `[delegation.projects."<slug>"]
  orchestrator_trivial_inline` (declared, true or false) beats both. There is
  no CLI command to flip it; `mneme install <runtime>` writes the global key
  explicitly as `true` when it is not declared, with a comment saying how to
  turn it off, and prints an `[info]` line when it finds it declared `false`.
- **Which specs count**: lane `trivial`, status `implementing`, non-empty
  scope, and not frozen (its backlog item is neither archived nor missing —
  the query mirrors `service.specFreeze`). `draft`, `rationale`, `audit` and
  `done` grant nothing; after a failed `lane_audit` the spec is rejected back
  to `implementing` and the permission returns. Several open trivial specs
  grant the union of their scopes.
- **What is granted**: only `Edit`, `Write` and `MultiEdit`, only where the
  ownership check would otherwise block, and only for a path that matches the
  scope (same doublestar matcher `lane_audit` uses) relative to the hook's
  cwd. Paths matching any pattern of risk items 1, 2 and 5 (the risk table
  of `internal/risk` plus `[lane.risk_globs]`: SQL, migrations, schemas,
  secrets and keys, `cmd/**`, `internal/install/assets/**`, CI, deploy,
  release, manifests and lockfiles) and paths outside the cwd tree are never
  granted. A scope that matches everything or uses braces
  (`laneaudit.IsCatchAllScope`: `**`, `*`, `**/*.go`, `{a,b}`) is refused at
  creation and by `spec_quick`, and ignored here for specs opened before that
  rule, so it never grants anything. A cwd below the repository root does not
  match root-relative scopes, so it blocks (safe direction). Residual
  exposure, accepted: a legitimate but wide scope (`internal/**`) still grants
  that whole area; the enforcement-log trail below is the mitigation.
- **Not relaxed**: `Bash` (redirections, `sed -i`, `cp` ...), `NotebookEdit`,
  subagents (their containment is unchanged), the standard lane, and
  `lane_audit` itself.
- **Fail closed**: any error opening or reading the project database yields
  exactly the decision the check had without the feature. With the flag off
  the database is never opened; with it on (the default), it is opened
  (read-only, at most once per call) only when the call was going to be
  blocked anyway.
- **Audit trail**: each granted call appends an enforcement-log event
  (`caller: orchestrator`, `decision: allow`, `reason: trivial_inline_scope`,
  target: the relative path). Code: `internal/cli/hook_trivial_inline.go`;
  `internal/enforcement` is unchanged.

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

### Stop hooks (SPEC-183)

A third kind of hook acts not on a tool call but on the moment an agent ends
its turn. In Claude Code, `mneme hook subagent-stop` (`SubagentStop`) keeps an
implementer (backend, frontend, bug-hunter) working once per spec when a
standard spec in `implementing` has no valid, current delivery contract block,
and `mneme hook stop` (`Stop`) keeps the orchestrator working once per work
window when the session has memories older than `[hooks] stop_reminder_after`
after its last summary or checkpoint. Both answer with
`{"decision":"block","reason":...}` and exit 0; both fail open.

The frontier is **remind, never judge**: they check shape and state, never
truth, they name only the tool whose call resolves the block
(`spec_doc_write`, `mem_session_end`), and after one reminder they stay quiet.
No lifecycle transition gains a check. Codex does not get them. Reference:
[HOOKS.md](HOOKS.md#stop-and-subagentstop-hooks-spec-183).

### Querying blocked attempts

Every orchestrator block produces a discovery memory. Query them with:

```bash
mneme search "Blocked edit"
```

The stop hooks write their reminders (`tool` `Stop` or `SubagentStop`) to the
same per-project enforcement log, but the three consumers of the containment
telemetry exclude them: `mneme delegation-hook report`,
`mneme delegation-hook promote`, and the reminder line of `mneme status` /
`mneme delegation-hook status`. A delivery reminder is not a blocked edit.

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
  project operates without delegated roles (the zero-role fallback, which
  never installs this hook).
- The choice is recorded per role in the subagent manifest's
  `enforcement_hook` field (`mneme subagents write --enforcement-hook`),
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

`report`, `promote` and the status reminder read only containment events:
the `Stop`/`SubagentStop` reminders of SPEC-183 share the log file but are
excluded from all three.

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
| **No stray bypassPermissions on read-only** | `architect`, `spec-reviewer` and `diagnostician` do not set `permissionMode: bypassPermissions`; `qa-tester` does since SPEC-087 D2b (`permissions.go`), to run its gates unattended | Remove the line from the read-only role |
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

### `enforce-delegation` guard (Layer 2b — orchestrator and subagents, in-process since SPEC-069)

```
read stdin (PreToolUse JSON payload)                        fail-open on bad input
  identity := resolveIdentity()      agent_id → subagent; agent_type → role
  checkSDDFilesGuard                 SPEC-179, any caller: no hand write in .mneme/sdd → exit 2
  checkDataDirGuard                  SPEC-185 A, any subagent: no hand write under mneme's data → exit 2
  lifecycleTools (MCP)               SPEC-087 D5, subagent → exit 2
  EvaluateLifecycleCommand (Bash)    SPEC-175, subagent → exit 2
  checkSpecDocKindScope              SPEC-181 D4, spec_doc_write
  checkTaskSectionScope              SPEC-170 D6, spec_doc_write kind task
  if tool not in {Write, Edit, MultiEdit, NotebookEdit, Bash} → exit 0
  resolve cwd (fail-open)
  if subagent without agent_type → warn, exit 0              SPEC-086 D2
  decision := evaluateDelegationWith(input, cwd, &facts)     delegation (orchestrator),
                                                             containment (subagent),
                                                             trivial inline grant (SPEC-171)
  if decision.Block:
    subagent → containment message, exit 2
    orchestrator → BLOQUEADO message, discovery memory, exit 2
  checkRiskGuard(input, identity, cwd, facts)                SPEC-185 B: risk paths 1/2/5,
                                                             dependency commands, test watch
    → undeclared → risk message, exit 2
  exit 0
```

Whitelist (always-allow fast-path, never reaches the ownership bridge):
`.claude/**`, `~/.claude/**`, `~/.mneme/**`, `CLAUDE.md` (any location),
`**/docs/*.md`, `.claudeignore`, `/tmp/**`, `/private/tmp/**`.
