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
   hook blocks this at the capability level and logs every attempt.

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
outside the whitelist, the hook:

1. Prints a BLOQUEADO message to stderr.
2. If `mneme` is on PATH, calls `mneme save --type discovery` to log the
   attempt as a queryable discovery memory.
3. Exits with code 2, which causes Claude Code to reject the tool call.

The hook does NOT discriminate by agent role (only by the presence of
`agent_id`). Role boundaries for subagents are enforced entirely by Layer 1.

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
    log to mneme (if mneme on PATH)
    exit 2
```

Whitelist: `.claude/**`, `~/.claude/**`, `~/.mneme/**`, `CLAUDE.md` (any
location), `**/docs/*.md`, `.claudeignore`, `/tmp/**`, `/private/tmp/**`.
