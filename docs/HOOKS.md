# mneme — Claude Code Hooks Integration

mneme integrates with Claude Code's hook system to provide two complementary
capabilities:

1. **Session lifecycle hooks** — load/save context at session boundaries.
2. **Pre-tool-use hook** — evaluate rules just-in-time before file edits.

## Quick Setup

Run once to configure all hooks automatically:

```bash
mneme install claude-code
```

This adds the following to `~/.claude/settings.json`:

```json
{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "mneme hook session-start"}]
      }
    ],
    "Stop": [
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "mneme hook session-end"}]
      }
    ],
    "PreToolUse": [
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "mneme hook pre-tool-use"}]
      }
    ]
  }
}
```

---

## Session Lifecycle Hooks

### `mneme hook session-start`

Fires at `SessionStart`. Loads project context (last session summary,
architecture decisions, conventions, active rules) and prints it to stdout so
Claude Code injects it into the agent's context window.

**Manual equivalent:** `mneme context --budget 4000`

### `mneme hook session-end`

Fires at `Stop`. Prints a reminder prompt that instructs the agent to call
`mem_session_end` before the session closes.

---

## Pre-Tool-Use Hook: `mneme hook pre-tool-use`

### What it does

The `pre-tool-use` hook fires before every `Edit`, `Write`, and `MultiEdit`
tool call. It:

1. Reads the tool invocation JSON from stdin (Claude Code provides this).
2. Queries active rules from the mneme databases (project + global) in read-only
   mode.
3. Matches rules against the current tool name and file path.
4. Emits a markdown reminder to stdout for Claude Code to inject as a system
   reminder (info/warn/block rules all appear).
5. Exits with code 2 if any `block`-severity rule matched, causing Claude Code
   to reject the tool call.

### Stdin format

Claude Code passes the following JSON to the hook:

```json
{
  "tool_name": "Edit",
  "tool_input": {
    "file_path": "/absolute/path/to/file.go"
  }
}
```

### Stdout format (when rules match)

```markdown
<!-- mneme:rules:start -->
## mneme — Rules for this action

**Tool:** Edit | **File:** internal/store/memory.go

### [BLOCK] Never store plain passwords
Always use bcrypt with cost >= 12.
_Applies to: tool:Edit+internal/**/*.go_

---

**Action: BLOCKED** — 1 block rule matched. The agent must find an alternative approach.
<!-- mneme:rules:end -->
```

When no rules match, stdout is empty (no noise).

### Exit codes

| Code | Meaning    | When                                       |
|------|------------|--------------------------------------------|
| 0    | Allow      | No rules matched, or only info/warn matched |
| 2    | Block      | At least one `block`-severity rule matched  |

The hook never exits with code 1 — all internal errors result in exit 0 (fail
open) so a broken hook never prevents the agent from working.

### Creating rules

Rules are stored in the mneme database as memories of type `rule`. Create them
via MCP or CLI:

**Via MCP:**
```
mem_save({
  type: "rule",
  severity: "block",
  applies_to: ["tool:Edit+internal/**", "tool:Write+internal/**"],
  title: "Protect internal package",
  content: "Delegate edits in internal/ to the backend subagent."
})
```

**Via CLI:**
```bash
mneme save --type rule --severity block \
  --applies-to "tool:Edit+internal/**" \
  --applies-to "tool:Write+internal/**" \
  --title "Protect internal package" \
  "Delegate edits in internal/ to the backend subagent."
```

### Pattern syntax

| Pattern                   | Matches                                           |
|---------------------------|---------------------------------------------------|
| `**`                      | Everything — any tool, any path                   |
| `tool:Edit`               | Any Edit call, regardless of path                 |
| `internal/**/*.go`        | Any Go file under internal/, regardless of tool   |
| `tool:Edit+internal/**`   | Edit AND path inside internal/ (AND logic)        |
| `!docs/**`                | Negation: excludes paths in docs/                 |
| `["**", "!docs/**"]`      | Everything except docs/ paths                     |

**Notes:**
- Tool selectors are case-sensitive (`tool:Edit` ≠ `tool:edit`).
- Negation (`!`) only works at the top-level array entry, not inside a `+` combined entry.
- Paths are relative to the project working directory.
- Paths outside the project tree (e.g. `/etc/hosts`) only match tool selectors and `**`, not path globs.
- Symlinks are not resolved; matching uses the literal path from `tool_input.file_path`.

### Performance

The hook is designed to complete in under 50ms:
- Opens the database in read-only mode (`mode=ro`) — no migrations, no WAL writer.
- Single `SELECT` query with `LIMIT 200` against a partial index.
- In-memory matching — no I/O after the query.
- Busy timeout of 1s; if the DB is locked, the hook allows rather than blocking.

---

## Legacy Hook: `mneme hook enforce-delegation` (deprecated)

The legacy hook uses `DelegationConfig` in `config.toml` (static path lists)
instead of rules from the database. It continues to work but emits a deprecation
warning to stderr.

**Migration to `pre-tool-use`:**

```bash
# Replace the legacy hook with the new one in settings.json:
mneme install claude-code --reinstall-hooks
```

This removes all existing `PreToolUse` entries and registers
`mneme hook pre-tool-use`. After migration, recreate your delegation rules:

```bash
mneme save --type rule --severity block \
  --applies-to "tool:Edit+cmd/**" \
  --applies-to "tool:Write+cmd/**" \
  --applies-to "tool:MultiEdit+cmd/**" \
  --applies-to "tool:Edit+internal/**" \
  --applies-to "tool:Write+internal/**" \
  --applies-to "tool:MultiEdit+internal/**" \
  --title "Delegation: protect source paths" \
  "Delegate code edits in protected paths to the appropriate subagent (backend, frontend, etc.)."
```

You can keep `delegation.enabled=false` in `config.toml` once the new rules are
verified.

---

## FAQ

**Q: Can I use both hooks simultaneously?**
A: Yes. `enforce-delegation` uses `config.toml` and `pre-tool-use` uses DB rules.
Both run independently. If either exits with code 2, the action is blocked.

**Q: What if I have no rules in the DB?**
A: `pre-tool-use` exits with code 0 (allow) — it does nothing when there are no
rules to evaluate.

**Q: How do I temporarily disable the hook?**
A: Remove or comment out the `PreToolUse` entry in `~/.claude/settings.json`.
Alternatively, set `applies_to=[]` on individual rules or soft-delete them with
`mem_forget`.

**Q: The hook is slow — what can I do?**
A: The hook targets <50ms. If it's slower, check that the project DB is not
unusually large and that no other process holds a long write lock. The busy
timeout is 1s; if it fires frequently, investigate lock contention.

**Q: Why two separate hooks (`session-start`/`session-end` vs. `pre-tool-use`)?**
A: They serve different purposes. Session hooks are observational — they load
context in bulk at session boundaries and never block. The pre-tool-use hook is
active enforcement — it fires on every file mutation and can block the action.
Combining them would muddle the semantics and hurt performance.

---

## See Also

- [Rules System](RULES.md) — applies_to syntax, severity levels, examples by stack
- [Architecture](ARCHITECTURE.md) — overall system design and graph layer
- [Knowledge Graph](GRAPH.md) — weighted relations, Hebbian learning, decay
