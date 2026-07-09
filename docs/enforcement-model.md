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
5. The orchestrator (principal) MUST NOT edit source paths directly. The bash
   hook blocks this at the capability level and logs every attempt — except a
   non-whitelisted path with no implementer subagent to own it, which the
   manifest-aware `path-owned` check (SPEC-068) allows through as a conscious
   fallback. See "Manifest-aware path ownership (SPEC-068)" below.

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
  (multi-key, same logic as the bash hook).
- Supports `agent:orchestrator|subagent|*` selectors in `applies_to`.
- **Degrades block→warn** for subagents on rules without an explicit `agent:`
  selector: the rule appears as context but does not exit 2.
- `tool:Bash` is **context-only** in this layer — Bash does not appear in
  `mutatingTools`; a rule with `tool:Bash` never triggers from pre-tool-use Go.

This means `block` delegation rules (e.g. the historic `019e4261` rule) now
correctly block only the orchestrator and degrade for legitimate subagent
implementers, eliminating the need to keep them at `warn` permanently.

### Layer 2b — Bash hook (defense in depth, orchestrator-only)

`~/.claude/hooks/enforce_delegation.sh` is a `PreToolUse` hook that inspects
the Claude Code hook payload from stdin. It detects whether the caller is the
orchestrator by the multi-key `agent_id` resolution (introduced in SPEC-042)
and checks all five known payload locations.

When the orchestrator attempts a file-mutating tool (`Write`, `Edit`,
`MultiEdit`, `NotebookEdit`, or a protected `Bash` command) against a path
outside the static whitelist, the hook no longer blocks unconditionally
(SPEC-068 D6/D9): it calls `mneme hook path-owned <target>` (see "Manifest-aware
path ownership" below) and only blocks when that subcommand exits 2. When it
blocks, the hook:

1. Prints a BLOQUEADO message to stderr, naming the owning subagent role when
   `path-owned` reported one.
2. If `mneme` is on PATH, calls `mneme save --type discovery` to log the
   attempt as a queryable discovery memory.
3. Exits with code 2, which causes Claude Code to reject the tool call.

The hook does NOT discriminate by agent role (only by the presence of
`agent_id`). Role boundaries for subagents are enforced entirely by Layer 1.

### Manifest-aware path ownership (SPEC-068)

`mneme hook path-owned <path>` is the Go subcommand the bash hook now
consults for every non-whitelisted target instead of blocking directly. It
reads the project's `subagents/manifest` memory (read-only, same lightweight
pattern as the Go rules engine's DB access) and decides:

| Manifest state | Path state | Result |
|---|---|---|
| Present, non-empty | Owned by an implementer subagent's `areas` glob | **BLOCK** (exit 2) — names the owning role |
| Present, non-empty | Not owned by any implementer | **ALLOW** (exit 0) — legitimate orchestrator fallback |
| Absent, or present but empty `[]` | (any) | **BLOCK** (exit 2, "legacy") — deny-by-default, protects projects that have not run the `mneme-init` grill yet |
| Hard failure (config/DB unreadable, corrupt JSON) | (any) | **ALLOW** (exit 0) — fail-open, consistent with the rest of this hook |

The bash side (`check_target_or_block` in `enforce_delegation.sh`) treats any
exit code other than 2 as allow — including a crash, or `mneme` predating this
subcommand — which is why the contract is exit-code-based rather than parsing
output. If `mneme` is not on PATH at all, the bash hook cannot ask Go anything
and blocks unconditionally rather than opening a silent bypass.

This makes retiring the six global subagents (a future release) non-destructive:
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
claude-code` registers both Layer 2 hooks (the Go rules engine and the bash
script) in `~/.claude/settings.json`, and — during the agnostic-agents
transition (SPEC-052 §9) — it still does so **unconditionally, every
release**, so the repos that have not migrated to per-project subagents keep
working exactly as before.

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

The bash script itself is **not** duplicated per project — the project-scope
entry still points at the global `~/.claude/hooks/enforce_delegation.sh`,
written once by `mneme install claude-code`. Only the *registration* (the
`settings.json` PreToolUse entry) becomes project-scoped.

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
| **Hook installed** | `enforce_delegation.sh` is registered as a `PreToolUse` hook | Run `mneme install claude-code` |
| **Hook logs to mneme** | Blocked attempts produce a `discovery` memory | Ensure `mneme` is on PATH; check stderr for save errors |

## Adding a new subagent

1. Decide its role: read-only or implementer.
2. Create `internal/install/assets/agents/<name>.md`.
3. Use the appropriate `tools:` template from the table above.
4. If read-only: omit `permissionMode: bypassPermissions`.
5. If implementer: add `permissionMode: bypassPermissions` with the comment
   explaining why it is intentional.
6. Update this document with the new agent.
7. Run `mneme install claude-code` to deploy the updated agents.
8. Add tests in `internal/install/install_test.go` following the patterns of
   `TestAgentAssets_ReadOnlyAllowlists` and `TestAgentAssets_ImplementerAllowlists`.

Note: the bash hook blocks the orchestrator by detecting the absence of
`agent_id`. No change to the hook source is required when adding subagents,
because their capability boundary is the `tools:` allowlist.

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

### Bash hook (Layer 2b — orchestrator only)

```
read stdin (PreToolUse JSON payload)
  if agent_id present (any of 5 locations) → exit 0  (subagent)
  if tool not in {Write, Edit, MultiEdit, NotebookEdit, Bash} → exit 0
  if Bash → check command for protected paths/patterns
  if file_path in whitelist → exit 0
  else:
    set TARGET_PATH
    if mneme not on PATH → log + exit 2  (D9 guard: no bypass)
    else:
      run `mneme hook path-owned $TARGET_PATH`
      if exit code == 2 → log (naming the owning role) + exit 2
      else → allow (fall through)
```

Whitelist (always-allow fast-path, never reaches `path-owned`): `.claude/**`,
`~/.claude/**`, `~/.mneme/**`, `CLAUDE.md` (any location), `**/docs/*.md`,
`.claudeignore`, `/tmp/**`, `/private/tmp/**`.
