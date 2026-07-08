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

The `pre-tool-use` hook fires before every `Edit`, `Write`, `MultiEdit`, and
`NotebookEdit` tool call. It:

1. Reads the tool invocation JSON from stdin (Claude Code provides this).
2. Resolves the **invoking role** (orchestrator vs subagent) from the five known
   `agent_id` payload fields (see "Role resolution" below).
3. Queries active rules from the mneme databases (project + global) in read-only
   mode.
4. Matches rules against the tool name, file path, and caller role.
5. Emits a markdown reminder to stdout for Claude Code to inject as a system
   reminder (info/warn/block rules all appear; degraded rules show as WARN).
6. Exits with code 2 **only if an effective block** rule matched — block rules
   are automatically degraded to warn for subagents unless an explicit `agent:`
   selector targets them.

### Role resolution (SPEC-043)

Claude Code may inject `agent_id` in different payload locations depending on
version. The hook checks all five known locations (first non-empty wins):

```json
{
  "agent_id": "...",
  "session":  { "agent_id": "..." },
  "subagent": { "agent_id": "..." },
  "context":  { "agent_id": "..." },
  "metadata": { "agent_id": "..." }
}
```

- Any non-empty value → **subagent** (exit 0 for block rules degraded to warn).
- All empty / null / absent → **orchestrator** (block rules exit 2 as usual).

### Role-aware degradation (SPEC-043)

A `block`-severity rule **without** an `agent:` selector is automatically
degraded to `warn` for subagents:

| Rule | Caller | Effective | Exit |
|------|--------|-----------|------|
| `block`, no agent: selector | orchestrator | block | 2 |
| `block`, no agent: selector | subagent | warn (degraded) | 0 |
| `block`, `agent:*` | subagent | block | 2 |
| `block`, `agent:orchestrator` | subagent | (no match) | — |

The degraded rule still appears in stdout with a `[WARN — degraded from BLOCK
for subagent]` annotation so the subagent has full context.

### tool:Bash is context-only

`Bash` is **not** in `mutatingTools`. A rule with `tool:Bash` never triggers in
the Go pre-tool-use engine. Bash enforcement is the exclusive responsibility of
`enforce_delegation.sh` (Layer 2). Adding Bash to the Go engine is out of scope.

### Stdin format

Claude Code passes the following JSON to the hook (all five `agent_id` locations
may appear simultaneously; only one needs to be non-empty to signal a subagent):

```json
{
  "tool_name": "Edit|Write|MultiEdit|NotebookEdit|Bash",
  "tool_input": {
    "file_path": "/absolute/path/to/file.go",
    "notebook_path": "/absolute/path/to/notebook.ipynb"
  },
  "agent_id":  "abc-123",
  "session":   { "agent_id": "..." },
  "subagent":  { "agent_id": "..." },
  "context":   { "agent_id": "..." },
  "metadata":  { "agent_id": "..." }
}
```

For `NotebookEdit`, `tool_input.file_path` may be absent; the hook falls back to
`tool_input.notebook_path`.

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

| Pattern                                    | Matches                                                   |
|--------------------------------------------|-----------------------------------------------------------|
| `**`                                       | Everything — any tool, any path, any caller               |
| `tool:Edit`                                | Any Edit call, regardless of path or caller               |
| `agent:orchestrator`                       | Only when caller is the orchestrator (no agent_id)        |
| `agent:subagent`                           | Only when caller is a subagent (agent_id present)         |
| `agent:*`                                  | Any caller (orchestrator or subagent)                     |
| `internal/**/*.go`                         | Any Go file under internal/, regardless of tool           |
| `tool:Edit+internal/**`                    | Edit AND path inside internal/                            |
| `agent:orchestrator+tool:Edit+internal/**` | Edit AND internal/ AND caller is orchestrator (3 parts)   |
| `!docs/**`                                 | Negation: excludes paths in docs/                         |
| `["**", "!agent:subagent"]`                | Applies to all callers except subagents                   |

**Notes:**
- Tool selectors are case-sensitive (`tool:Edit` ≠ `tool:edit`).
- Combined entries support N parts separated by `+` (not limited to 2).
- Negation (`!`) only works at the top-level array entry, not inside a `+` combined entry.
- Paths are relative to the project working directory.
- Paths outside the project tree only match tool selectors, agent selectors, and `**`.
- Symlinks are not resolved; matching uses the literal path from `tool_input.file_path`.
- `agent:<other>` (e.g. `agent:backend`) is reserved / deferred — never matches.

### Performance

The hook is designed to complete in under 50ms:
- Opens the database in read-only mode (`mode=ro`) — no migrations, no WAL writer.
- Single `SELECT` query with `LIMIT 200` against a partial index.
- In-memory matching — no I/O after the query.
- Busy timeout of 1s; if the DB is locked, the hook allows rather than blocking.

---

## Delegation Enforcement (bash hook)

### What it does

`mneme install claude-code` writes an executable bash script to
`~/.claude/hooks/enforce_delegation.sh` and registers it as a second
`PreToolUse` hook alongside the Go `pre-tool-use` hook. The two hooks
complement each other:

| Hook | Language | Reads | Purpose |
|------|----------|-------|---------|
| `mneme hook pre-tool-use` | Go | mneme DB rules | Context injection — warn/block via stored rules |
| `enforce_delegation.sh` | Bash | stdin JSON | Hard-block orchestrator from editing source files |

### Why a separate bash hook

The bash hook enforces a **structural constraint** that cannot be expressed as a
DB rule: the orchestrator (main Claude Code session) must never edit source code
directly — it must delegate that work to a specialised subagent. The hook
detects the difference via the `agent_id` field that Claude Code injects into
the PreToolUse JSON payload only when a subagent fires the tool.

### Agent detection

Claude Code includes `"agent_id": "<uuid>"` in stdin when a tool fires inside a
subagent (Task/Agent tool call). The hook reads this field:

- **`agent_id` present** → subagent → allow (exit 0).
- **`agent_id` absent** → orchestrator → apply whitelist check.

### Whitelist (what the orchestrator _can_ touch)

| Pattern | Matches |
|---------|---------|
| `CLAUDE.md` | Any file named `CLAUDE.md` |
| `.claudeignore` | Root-level ignore file |
| `**/docs/*.md` | Markdown files under any `docs/` directory |
| `.claude/**` | Project-local Claude config directory |
| `~/.claude/**` | Global Claude config directory (absolute path check) |
| `~/.mneme/**` | mneme workflow and spec files (SDD scratch) |
| `/tmp/**` | Temporary scratch files (orchestrator planning, etc.) |
| `/private/tmp/**` | macOS equivalent of `/tmp` |

**CONDICIÓN 2 — `/tmp` is not a bridge to the repo:** the whitelist allows
writing TO `/tmp`. However, `cp /tmp/x.go internal/store/x.go` is still
BLOCKED because the destination (`internal/store/x.go`) is a protected path.
The hook's `_find_last_word_target` heuristic takes the **last** non-flag word
as the target of `cp`/`mv`, so the destination is what is checked.

### Tools intercepted

- `Write`, `Edit`, `MultiEdit`, `NotebookEdit` — file path extracted from
  `tool_input.file_path`.
- `Bash` — redirect targets (`>`, `>>`, `2>`), `sed -i`, `perl -i`, `mv`, `cp`,
  `rm`, `touch`, `chmod`, `chown`, `ln`, `tee`, `dd of=`, here-docs, and inline
  Python/Node scripts with file writes.

### Shell command tokenizer (added in v1.3.0)

The hook uses `mneme hook tokenize` as its shell command parser. This subcommand
uses a real bash AST parser (`mvdan.cc/sh/v3`) to tokenize commands into
structured tokens, eliminating false positives caused by quoted arguments or
heredoc content.

**Examples fixed by the Go tokenizer:**

| Command | Old parser (awk) | New parser (Go) |
|---------|-----------------|-----------------|
| `gh pr create --title "feat(install): X"` | BLOCK (install in title) | ALLOW (quoted) |
| `echo "rm /tmp"` | BLOCK (rm in quoted arg) | ALLOW (quoted) |
| `echo content > docs/test.md` | BLOCK or ALLOW (fragile) | ALLOW (whitelisted target) |
| `rmdir .claude/tmp 2>/dev/null` | Fragile | ALLOW (correct redirect handling) |

**Token types:**

| Type | Description | Hook action |
|------|-------------|-------------|
| `word` | Command name or argument | Check if it is a dangerous command (only when `quoted: false`) |
| `redirect` | Redirect operator (`>`, `>>`, `2>`, etc.) | Emit; next token is the target |
| `redirect_target` | File path after a redirect | Validate against whitelist |
| `heredoc_body` | Content of a `<<EOF` block | **Skip entirely** — not a command |
| `command_substitution` | Content of `$(...)` | Re-tokenize 1 level |

#### `mneme hook tokenize`

```bash
# Usage: read a shell command from stdin, emit JSON tokens to stdout
printf 'rm $(date +%s).txt' | mneme hook tokenize
```

Output:
```json
{"tokens":[
  {"value":"rm","type":"word"},
  {"value":"date +%s","type":"command_substitution"},
  {"value":".txt","type":"word"}
]}
```

The `quoted` field (omitted when false) indicates the token came from a
single- or double-quoted string and must not be treated as an executable command.

#### Fallback to legacy parser

At startup the hook probes for the Go tokenizer:

```bash
printf '' | mneme hook tokenize >/dev/null 2>&1
```

- **Succeeds (exit 0)** → `USE_GO_TOKENIZER=1` — use Go tokenizer.
- **Fails (exit 1, e.g. old binary without tokenize)** → `USE_GO_TOKENIZER=0` —
  fall back to the original awk/regex parser with a warning to stderr:

```
[enforce_delegation] WARNING: mneme hook tokenize not available, using legacy parser (may produce false positives)
```

The legacy parser (`check_bash_legacy`) is preserved unchanged so the hook
continues to work after `mneme upgrade` if the binary has not been replaced yet.

### Fail-open behaviour

Any error — malformed JSON, unreadable stdin — causes the hook to exit 0
(allow). A broken hook must never prevent the agent from working.

**`jq` absent**: when `jq` is not found in PATH the hook emits a WARNING to
stderr and exits 0 (fail-open). The WARNING is deliberate — jq absent means
the hook provides **no enforcement at all**. Unlike a silent fail-open, the
WARNING makes this visible in the agent output stream.

### Inherent limits (Layer 2 scope)

`enforce_delegation.sh` is Layer 2: it stops the **cooperative orchestrator**
from accidentally editing source code. It is **not a sandbox**.

Bypass patterns that are **out of scope by design**:

| Pattern | Why not closed |
|---------|---------------|
| `echo <b64> \| base64 -d \| bash` | Arbitrary indirection; requires a full sandbox to close |
| Custom binaries writing files | Hook cannot introspect arbitrary executables |
| `ruby -e`, `php -r`, etc. | Unlisted interpreters |

**Primary defense** against subagent misbehavior is **Layer 1** (capability
`tools:` allowlist in `agents/*.md`). Claude Code enforces allowlists natively
before the hook is invoked.

### Runtime dependency: `jq`

The hook requires `jq` for JSON parsing. macOS ships with `jq`; on Linux install
it via the system package manager. If `jq` is absent, `mneme install claude-code`
prints a warning to stderr:

```
  [warn] jq not found in PATH; the delegation hook will fail-open until jq is installed
```

The hook itself also emits a richer WARNING at runtime so the gap is visible
even if installation did not detect the absence.

### Installation and updates

```bash
# Install or update (no-op if content is already identical):
mneme install claude-code

# Force reinstall even when content matches (e.g. after a mneme upgrade):
mneme install claude-code --reinstall-hooks
```

The hook script is embedded in the mneme binary. On upgrade the new version is
distributed automatically when `mneme install claude-code` is re-run; the old
script is backed up as `enforce_delegation.sh.bak-YYYYMMDD-HHMMSS`.

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
- [API reference: CLI](api/cli.md) → — full flag reference for `mneme hook <event>`
