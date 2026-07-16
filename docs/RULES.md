# mneme -- Rules System

Rules are memories of type `rule` that establish binding constraints for AI agents. Unlike conventions (which are guidelines), rules are **actively enforced** via hooks and are mandatorily injected into context.

Introduced in SPEC-001..004 (EPIC-1).

---

## Table of Contents

1. [Mental model](#mental-model)
2. [applies_to syntax](#applies_to-syntax)
3. [Severity tradeoffs](#severity-tradeoffs)
4. [CLI: mneme rule](#cli-mneme-rule)
5. [How the hook works](#how-the-hook-works)
6. [Context injection](#context-injection)
7. [Examples by stack](#examples-by-stack)
8. [FAQ](#faq)

---

## Mental model

```
Rule = Memory + applies_to + severity
```

| Aspect | Convention | Rule |
|---------|-----------|------|
| Type | `convention` | `rule` |
| Enforcement | Passive (the agent reads it if it finds it) | Active (injected into context + hook) |
| Decay | Normal (0.005/day) | Immune (decay_rate = 0) |
| applies_to | Not present | Required |
| severity | Not present | info / warn / block |
| Hook integration | No | Yes (pre-tool-use) |

A rule exists until it is explicitly revoked with `mem_forget` or modified with `mem_update`.

---

## applies_to syntax

The `applies_to` field is an array of patterns that determine when the rule applies. Each pattern can be:

### Path globs

Match against the file path relative to the project directory.

```
internal/**/*.go     Any .go file under internal/ (recursive)
*.test.ts            TypeScript test files at the root
src/api/**           Everything under src/api/
vendor/**            Everything under vendor/
```

Use doublestar (`**`) for recursion and single star (`*`) for one level.

### Tool selectors

Match against the tool name (case-sensitive).

```
tool:Edit            Any call to Edit
tool:Write            Any call to Write
tool:MultiEdit       Any call to MultiEdit
```

### Agent selectors (role-aware, SPEC-043)

Match against the **role of the hook caller**: orchestrator (main session, no `agent_id`) or subagent (Task/Agent spawned, `agent_id` present).

```
agent:orchestrator   Applies only to the orchestrator (main session)
agent:subagent       Applies only to subagents (Task/Agent calls)
agent:*              Applies to all roles (wildcard)
agent:<other>        Reserved -- does not match yet (see note below)
```

**`agent:<agent_type>` (e.g. `agent:backend`, `agent:frontend`) is NOT YET implemented in this rules engine**, but is no longer blocked on missing evidence: a real captured PreToolUse payload (2026-07-15, SPEC-086, memory `enforcement/payload-pretooluse-agent-type-capturado`) confirmed Claude Code DOES expose a top-level `agent_type` field carrying the literal subagent role name — the premise this selector was deferred on ("Claude Code does not expose `agent_type`") is false. SPEC-086 used this same field to build role-aware subagent containment in the delegation-enforcement hook (`internal/cli/hook.go`'s `CallerIdentity`/`resolveIdentity` — see `docs/enforcement-model.md`'s "Subagent containment" section), but did NOT extend it to this rules engine's `applies_to` matcher (`internal/rules/match.go`) — that is a separate, independent workstream (BL-106). Until BL-106 lands, any name other than `orchestrator`, `subagent`, and `*` still does not match either role here.

### Combined selectors (AND)

Parts separated by `+`. **All** parts must match (AND). Supports N parts, not just 2.

```
tool:Edit+internal/**                              Edit AND path in internal/
tool:Write+**/*.sql                                Write AND .sql files
agent:orchestrator+tool:Edit+internal/**           Edit AND path in internal/ AND caller is the orchestrator
agent:*+tool:Write+cmd/**                          Write AND path in cmd/ AND any caller
```

### Negations

Prefix `!` vetoes the rule when the pattern matches. Useful for exceptions.

```
!docs/**             Does not apply if the path is under docs/
!*.md                Does not apply to markdown files
!agent:subagent      Vetoes the rule when the caller is a subagent
```

### Global wildcard

```
**                   Applies to everything: any tool, any path, any caller
```

### Combinations

The `applies_to` array evaluates as OR between positive entries, with veto by negative entries:

```json
["internal/**/*.go", "cmd/**/*.go", "!*_test.go"]
```

This means: "applies to Go files in internal/ OR cmd/, EXCEPT test files".

### Important notes

- Tool selectors are **case-sensitive**: `tool:Edit` is not `tool:edit`
- Negation (`!`) only works as a top-level entry, not inside a `+` combined selector
- Paths are relative to the project's working directory
- Paths outside the project tree only match tool selectors, agent selectors, and `**`
- Symlinks are not resolved; matching uses the literal path
- `tool:Bash` is **context-only** in the Go rules engine: Bash is not in `mutatingTools`, so a rule with `tool:Bash` never fires from the Go `pre-tool-use` hook. Bash enforcement is the exclusive territory of `enforce_delegation.sh` (Layer 2). See [HOOKS.md](HOOKS.md).
- In the `session-start` hook (`mem_context`), `agent:` entries are shown literally as informational -- there is no concrete caller to evaluate against at that point.

---

## Severity tradeoffs

| Severity | Effect on hook | Effect on context | When to use it |
|----------|---------------|-------------------|---------------|
| `info` | Exit 0, stdout with reminder | Injected with tag `[INFO]` | Gentle guidance, best practices, reminders |
| `warn` | Exit 0, stdout with warning | Injected with tag `[WARN]` | Rules the agent should consider but can override |
| `block` | **Exit 2** for the orchestrator; **degraded to warn** for subagents (without an `agent:` selector) | Injected with tag `[BLOCK]` or `[WARN — degraded from BLOCK]` | Absolute prohibitions, delegation enforcement |

### Block→warn degradation for subagents (SPEC-043)

A `block` rule **without** an `agent:` selector is **automatically degraded to warn** when the caller is a subagent:

- The rule **still appears** in the output -- it is not a silent skip.
- The tag is shown as `[WARN — degraded from BLOCK for subagent]`.
- The final action shows `ALLOWED` (not `BLOCKED`).
- An informational note indicates how many rules are `BLOCK` for the orchestrator and were degraded.
- The hook **does not exit with code 2** for the subagent.

**Reason:** the orchestrator must never edit code directly (delegation); subagents (backend, frontend, etc.) are the legitimate implementers. The degradation lets path-protection rules block the orchestrator **without interfering with legitimate subagent work**.

To block a subagent specifically, use an explicit `agent:` selector:
```bash
mneme rule add -t "Subagent cannot touch migrations" \
  -c "Use only dbmate to modify migrations." \
  -a "agent:subagent+internal/db/migrations/**" \
  -s block
```

To block everyone (orchestrator AND subagents), use `agent:*`:
```bash
mneme rule add -t "Never edit vendor" \
  -c "Files in vendor/ are managed by go mod." \
  -a "agent:*+vendor/**" -s block
```

### When to use block

- Protect source-code paths for delegation
- Prevent editing of generated files
- Prohibit dangerous code patterns
- Compliance enforcement (passwords, secrets, etc.)

### When to use warn

- Remind about style conventions
- Suggest preferred patterns
- Warn about high-risk areas

### When to use info

- Document context the agent should know
- Remind about dependencies between modules
- Performance or security tips

---

## CLI: mneme rule

### `mneme rule add`

Creates a rule. Auto-generates a `topic_key` from the title for idempotent upserts.

```bash
mneme rule add \
  --title "No vendor edits" \
  --content "Never edit files under vendor/. They are managed by dependency tools." \
  --applies-to "vendor/**" \
  --severity block

# Multiple patterns
mneme rule add \
  --title "Protect internal package" \
  --content "Delegate edits to the backend subagent." \
  --applies-to "tool:Edit+internal/**" \
  --applies-to "tool:Write+internal/**" \
  --applies-to "tool:MultiEdit+internal/**" \
  --severity block

# Global scope (applies to all projects)
mneme rule add \
  --title "Always use error wrapping" \
  --content "Wrap errors with fmt.Errorf and %w, never swallow." \
  --applies-to "**/*.go" \
  --severity warn \
  --scope global

# Read content from stdin
echo "Detailed instruction..." | mneme rule add \
  --title "My rule" \
  --applies-to "**" \
  --stdin
```

**Flags:**

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--title` | `-t` | required | Rule title |
| `--content` | `-c` | required | Rule content/instruction |
| `--applies-to` | `-a` | required | Pattern(s), repeatable |
| `--severity` | `-s` | `warn` | info, warn, or block |
| `--scope` | | `project` | project or global |
| `--topic-key` | `-k` | auto-generated | Topic key for upserts |
| `--importance` | `-i` | `0.95` | Importance override |
| `--stdin` | | false | Read content from stdin |

### `mneme rule list`

Shows all active rules in a table color-coded by severity.

```bash
mneme rule list
mneme rule list --scope global
mneme rule list --severity block
mneme rule list --json | jq '.rules[].title'
```

Example output:
```
SEV    ID        TITLE                           APPLIES_TO                      SCOPE
-----  --------  ------------------------------  ------------------------------  -------
BLOCK  019de100  No vendor edits                 vendor/**                       project
WARN   019de101  Always use error wrapping        **/*.go                         global
INFO   019de102  Auth module documentation        internal/auth/**                project

3 rules (1 block, 1 warn, 1 info)
```

### `mneme rule test`

Evaluates rules against a simulated invocation, without executing anything.

```bash
mneme rule test --tool Edit --path vendor/foo/bar.go
mneme rule test --tool Write --path internal/store/memory.go
mneme rule test --tool Edit  # no path, only tool selectors and ** match
mneme rule test --tool Edit --path docs/README.md --json
```

Example output:
```
Testing: tool=Edit          path=vendor/foo/bar.go

Evaluated: 3 rules
Matched:   1 rules

  [BLOCK] No vendor edits
         Never edit files under vendor/. They are managed by dependency tools.
         Matched by: vendor/**

Effective severity: block
Result: BLOCKED
```

---

## How the hook works

The `mneme hook pre-tool-use` hook is registered as a `PreToolUse` hook in Claude Code via `mneme install claude-code`.

### Flow

```
Claude Code wants to Edit(file)
        |
        v
Invokes hook: mneme hook pre-tool-use
        |
        v
Hook reads stdin JSON: {"tool_name":"Edit","tool_input":{"file_path":"..."}}
        |
        v
Opens project + global DB in read-only mode
        |
        v
SELECT rules WHERE type='rule' AND deleted_at IS NULL (LIMIT 200)
        |
        v
Match rules against tool + path (in-memory, <50ms)
        |
        v
  +-----------+     +-----------+     +-----------+
  | No match  |     | info/warn |     |   block   |
  |           |     |           |     |           |
  | stdout:   |     | stdout:   |     | stdout:   |
  | (empty)   |     | markdown  |     | markdown  |
  |           |     | reminder  |     | + action  |
  | exit 0    |     | exit 0    |     | exit 2    |
  +-----------+     +-----------+     +-----------+
```

### Output format (when there's a match)

```markdown
<!-- mneme:rules:start -->
## mneme -- Rules for this action

**Tool:** Edit | **File:** internal/store/memory.go

### [BLOCK] Never store plain passwords
Always use bcrypt with cost >= 12.
_Applies to: tool:Edit+internal/**/*.go_

---

**Action: BLOCKED** -- 1 block rule matched. The agent must find an alternative approach.
<!-- mneme:rules:end -->
```

### Exit codes

| Code | Meaning | When |
|------|-------------|--------|
| 0 | Allow | No rules matched, or only info/warn |
| 2 | Block | At least one block rule matched |

The hook **never** exits with code 1 -- all internal errors result in exit 0 (fail open).

### Performance

Target: <50ms. Mechanisms:
- DB opened in read-only mode (`mode=ro`) -- no migrations, no WAL writer
- Single `SELECT` with `LIMIT 200` against a partial index
- In-memory matching after the query
- Busy timeout of 1s; if the DB is locked, the hook allows

---

## Context injection

`mem_context` (SPEC-002) always injects rules from the active scope BEFORE general memories. Rules have a token budget separate from the general budget.

This ensures the LLM sees the constraints (especially `block`) before any other content, maximizing the likelihood that it respects them.

The order in the output is:

1. Last Session (if any)
2. **Active Rules** (always first, separate budget)
3. Loaded Memories

---

## Examples by stack

### Go

```bash
# Enforce error wrapping
mneme rule add -t "Always wrap errors" \
  -c "Use fmt.Errorf(\"context: %w\", err). Never swallow errors." \
  -a "**/*.go" -s warn

# Protect generated code
mneme rule add -t "Do not edit generated files" \
  -c "Files with //go:generate or _gen.go suffix are auto-generated." \
  -a "**/*_gen.go" -a "**/*_generated.go" -s block

# Architecture: no store from CLI
mneme rule add -t "CLI must not import store" \
  -c "CLI commands go through the service layer. Never import store directly." \
  -a "tool:Edit+internal/cli/**" -s warn
```

### Next.js / TypeScript

```bash
# No direct DB calls from components
mneme rule add -t "Components must use API routes" \
  -c "React components must never call Prisma or DB directly. Use API routes." \
  -a "tool:Edit+src/components/**" -a "tool:Write+src/components/**" -s warn

# Protect generated types
mneme rule add -t "Do not edit Prisma types" \
  -c "Types in node_modules/.prisma are generated. Run prisma generate instead." \
  -a "node_modules/.prisma/**" -s block
```

### Python

```bash
# Enforce type hints
mneme rule add -t "Use type hints" \
  -c "All function signatures must have type annotations." \
  -a "**/*.py" -a "!tests/**" -s info

# Protect migrations
mneme rule add -t "Do not edit migrations manually" \
  -c "Use alembic to generate migrations. Never edit migration files directly." \
  -a "alembic/versions/**" -s block
```

### Delegation (multi-agent)

```bash
# Protect source code from orchestrator — subagents can edit (degraded to warn for them)
mneme rule add -t "Delegation: protect source paths" \
  -c "Delegate code edits in protected paths to the appropriate subagent." \
  -a "tool:Edit+cmd/**" -a "tool:Write+cmd/**" -a "tool:MultiEdit+cmd/**" \
  -a "tool:Edit+internal/**" -a "tool:Write+internal/**" -a "tool:MultiEdit+internal/**" \
  -s block

# With explicit selector: only blocks the orchestrator (semantically identical to the previous one)
mneme rule add -t "Delegation: protect source paths (explicit)" \
  -c "Delegate code edits in protected paths to the appropriate subagent." \
  -a "agent:orchestrator+tool:Edit+internal/**" \
  -a "agent:orchestrator+tool:Write+internal/**" \
  -a "agent:orchestrator+tool:MultiEdit+internal/**" \
  -s block

# Protect generated files from EVERYONE (including subagents)
mneme rule add -t "Do not edit generated files" \
  -c "Files matching *_gen.go are auto-generated. Run go generate instead." \
  -a "agent:*+**/*_gen.go" -s block
```

---

## FAQ

**Q: Are rules injected on every turn?**
A: Rules are injected into `mem_context` at the start of the session (via the `session-start` hook) and evaluated on every call to Edit/Write/MultiEdit (via the `pre-tool-use` hook). They are not injected on every conversation turn -- that's handled by the hook when it applies.

**Q: Are rules overridable?**
A: `info` and `warn` rules are advisory -- the agent receives them but can proceed. `block` rules are absolute: the hook rejects the tool call with exit code 2 and Claude Code cancels the action. There is no runtime override; to disable a rule, use `mem_forget` or `mem_update`.

**Q: Do they block the architect/backend subagent?**
A: It depends. `block` rules **without** an `agent:` selector are automatically degraded to `warn` for subagents -- the subagent sees them as context but is not blocked (exit 0). Rules with `agent:*+...` or `agent:subagent+...` DO block subagents (exit 2). Rules with `agent:orchestrator+...` never apply to subagents. See the "Block→warn degradation for subagents" section.

**Q: What happens if there are no rules in the DB?**
A: `pre-tool-use` exits with code 0 (allow) -- it does nothing. `mem_context` omits the "Active Rules" section.

**Q: How do I temporarily disable the hook?**
A: Remove or comment out the `PreToolUse` entry in `~/.claude/settings.json`. Alternatively, use `mem_forget` on individual rules.

**Q: The hook is slow -- what can I do?**
A: Target is <50ms. If it's slower, check that the project DB isn't unusually large and that no other process holds a long write lock. The busy timeout is 1s.

**Q: Can I have both global and project rules?**
A: Yes. Global rules (`--scope global`) are stored in `global.db` and apply to all projects. Project rules (`--scope project`, default) apply only to the current project. The hook evaluates both.

**Q: How does this differ from the legacy `enforce-delegation` hook?**
A: `enforce-delegation` uses static paths defined in `config.toml`. The new `pre-tool-use` uses dynamic rules stored in the DB, with more expressive patterns (globs, negations, tool selectors) and 3 severity levels. Migration via `mneme install claude-code --reinstall-hooks` is recommended.

---

## See also

- [API reference: Memory tools](api/memory.md) → -- Full contract for `mem_save` (type `rule`), `mem_context` (rule injection)
- [API reference: CLI](api/cli.md) → -- `mneme rule add/list/test` flags
- [HOOKS.md](HOOKS.md) -- Hook system details (`session-start`, `session-end`, `pre-tool-use`)
- [CONFIG.md](CONFIG.md) -- Configuration reference including rule-related settings
