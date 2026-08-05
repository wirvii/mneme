# mneme — Claude Code Hooks Integration

mneme integrates with Claude Code's hook system to provide three complementary
capabilities:

1. **Session lifecycle hooks** — load/save context at session boundaries.
2. **Pre-tool-use hook** — evaluate rules just-in-time before file edits.
3. **Delegation guard (`enforce-delegation`)** — block the orchestrator from
   editing/running Bash against protected source paths.

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
    "PreToolUse": [
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "mneme hook pre-tool-use"}]
      },
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "mneme hook enforce-delegation"}]
      }
    ]
  }
}
```

Both `PreToolUse` entries are portable `mneme` subcommands — neither embeds a
path to the home directory (SPEC-069), so a committed `.claude/settings.json`
works on any machine with `mneme` on `PATH`, without requiring `mneme install`
to have run there first.

---

## Session Lifecycle Hooks

### `mneme hook session-start`

Fires at `SessionStart`. Loads project context (last session summary,
architecture decisions, conventions, active rules) and prints it to stdout so
Claude Code injects it into the agent's context window.

**Manual equivalent:** `mneme context --budget 4000`

#### stdin contract (SPEC-108)

Since SPEC-108, `session-start` also reads a JSON payload from stdin:

```json
{"session_id": "019fce58-6c7a-..."}
```

Only `session_id` is read. Claude Code's real SessionStart payload also
carries `source` and `transcript_path`, but neither has a consumer here — a
field without a consumer would be dead code.

#### The `mneme:session` block

When the payload carries `session_id`, the hook emits a block **before** the
`mneme:context` block, announcing the current session and — when a
*previous* session left work without a summary — asking the agent to close
it:

```
<!-- mneme:session:start -->
## Sesión anterior sin resumen

La sesión `019fce58-6c7a-…` dejó 7 memorias sin resumen (última actividad 2026-08-04T21:32:19Z).
Redáctalo tú y ciérrala con `mem_session_end` (`session_id=019fce58-6c7a-…`) — mneme no inventa el contenido.
Otras 2 sesiones más antiguas están igual (no se enumeran).

Sesión actual: `019fd0aa-…` — pásala como `session_id` en `mem_save` y `mem_session_end`.
<!-- mneme:session:end -->
```

The "Sesión actual" line is **always** printed whenever `session_id` is
present in the payload — independent of whether there is an orphan to
report. Without it the by-`session_id` detector is inert: `mem_save` has
always accepted `session_id`, but almost nothing has ever passed it (in
mneme's own production database, 1729 memories have no `session_id` and the
only 21 that do are the 21 pre-existing `session_summary` rows). Announcing
the id here — and the updated `mem_save`/`mem_session_end` tool descriptions
(SPEC-108 step 8) — is what starts closing that gap.

The block never repeats an orphan's title or content, and never enumerates
more than the single most recent orphaned session — only a count of how many
older ones are in the same state ("Otras N sesiones más antiguas…"). This
keeps the block's size roughly constant (a handful of lines) regardless of
how many orphaned sessions actually exist.

Two "work" definitions matter here and are asymmetric on purpose (SPEC-108
D7/D8): a session counts as having left *work* when it has memories that are
neither soft-deleted nor superseded (same filters as `mem_search`/
`mem_stats`, so the reported count matches what the user sees); a session
counts as *closed* the moment its summary memory exists, even if that
summary was later superseded by a synthesis — a superseded summary still
proves the session was closed. Filtering the closure check by `superseded_by`
too would leave the notice nagging forever once a synthesis absorbs the
summary.

#### Fail-open matrix

| stdin | Result |
|---|---|
| empty (`io.EOF`) | No `mneme:session` block, no stderr output, context block unchanged. The common case: a manual invocation, or an agent that hasn't started sending the payload yet. |
| invalid JSON | WARN on stderr, no block, context block still prints. |
| valid JSON without `session_id` | No block, **no** WARN — an absent `session_id` is indistinguishable from "this session genuinely has none", and warning would falsely accuse the current session. |
| `PendingSessionSummaries` store error | WARN on stderr, no block, context block still prints — the summary lookup failing must never suppress the context load that follows. |

#### Manual invocation

Running `mneme hook session-start` by hand from an interactive terminal
returns immediately instead of blocking on stdin: the hook detects a
character device (`os.ModeCharDevice`) and substitutes an empty reader before
decoding, exactly like the empty-stdin row above. From a script, `< /dev/null`
(or piping a JSON payload) works as expected either way.

### `mneme hook session-end` (retired, SPEC-106)

This subcommand still exists — it emits `{}` and exits 0 — but `mneme install`
no longer registers it against any event, and any pre-existing registration in
`~/.claude/settings.json` or `~/.codex/hooks.json` is actively purged on the
next `mneme install`.

It never delivered a working reminder. It was registered against `Stop`, which
is a **per-turn** event, not a per-session one — `SessionStart`/`SessionEnd`
are the once-per-session pair. On top of that, Claude Code discards a `Stop`
hook's stdout with exit 0 (only `UserPromptSubmit`, `UserPromptExpansion`, and
`SessionStart` inject stdout as context), so the reminder text never reached
the agent. Codex is stricter: it rejects plain-text stdout for `Stop` outright,
which is what surfaced the defect as a visible `Stop hook (failed)` error.

The subcommand survives purely as a safe no-op for hosts that have not
reinstalled yet. The session-end discipline that actually works is the one
described in the operating manual (`mem_session_end` before you stop) — see
`operating-manual.md` §7 / `operating-manual-codex.md` §5.

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

### Scope filter on the global DB (SPEC-105)

The project DB's query (`rulesQueryProject`) is unfiltered by scope — that
file only ever contains rows belonging to this one project. The **global**
DB's query (`rulesQueryGlobal`) adds `AND scope IN ('global', 'org')` —
fixing a cross-repo leak (BL-132): `initService` aliases `global.db` as the
project store whenever the cwd doesn't resolve a git-remote slug, and a
project-scoped rule that landed there with no project used to be evaluated
by this hook in **every repo on the host**, including a `severity=block`
rule that had nothing to do with the repo currently being edited. See
`docs/RULES.md`'s "Invariant R" section and `docs/profiles.md` §8 for the
full incident and fix — `queryRulesFromDB` now takes the query as a
parameter precisely so the project and global databases can be held to
different scope filters.
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
`mneme hook enforce-delegation` (Layer 2). Adding Bash to the rules engine is
out of scope.

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

## Delegation Guard: `mneme hook enforce-delegation`

### What it does

`mneme install claude-code` registers `mneme hook enforce-delegation` — an
in-process Go subcommand — as a second `PreToolUse` hook alongside the Go
`pre-tool-use` hook. The two hooks complement each other:

| Hook | Reads | Intercepts | Purpose |
|------|-------|------------|---------|
| `mneme hook pre-tool-use` | mneme DB rules | Edit/Write/MultiEdit/NotebookEdit | Context injection — warn/block via stored rules |
| `mneme hook enforce-delegation` | manifest + static whitelist | Edit/Write/MultiEdit/NotebookEdit/**Bash** | Hard-block orchestrator from editing/running Bash against protected source paths |

### History (SPEC-069)

Through v1.20.0 this guard was an embedded bash script
(`~/.claude/hooks/enforce_delegation.sh`, ~640 lines) registered with an
**absolute path to the home directory** — not portable, and dependent on
`mneme install` having already run on that machine. SPEC-069 ported the
script's decision logic in-process to Go:

- **`internal/enforcement`** (leaf package: stdlib + `internal/shell` only) —
  the pure decision functions `IsWhitelisted`, `EvaluateFileTool`,
  `EvaluateBash`. No I/O, no `os.Exit`.
- **`internal/cli/hook.go`**'s `runHookEnforceDelegation` — the I/O wiring:
  parses stdin, resolves the caller and the manifest, and injects an
  in-process `OwnershipFunc` closure over `resolvePathOwnership` (the same
  function `mneme hook path-owned` uses, SPEC-068) — no subprocess spawn.

The registered command is now the portable `mneme hook enforce-delegation`
subcommand (no path at all), and the embedded
`enforce_delegation.sh` asset is a ~6-line compat shim
(`exec mneme hook enforce-delegation`) kept only so that a pre-existing
per-repo `settings.json` entry with the old absolute path keeps working until
it is re-registered — see "Installation and updates" below.

### Why a separate hook

This guard enforces a **structural constraint** that cannot be expressed as a
DB rule: the orchestrator (main Claude Code session) must never edit source
code directly — it must delegate that work to a specialised subagent. The hook
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

### Manifest-aware ownership bridge (SPEC-068, in-process since SPEC-069)

The whitelist above is only the **fast-path**: paths matching it never consult
the manifest at all. For everything else, `runHookEnforceDelegation` calls an
`OwnershipFunc` closure over `resolvePathOwnership` — the exact same pure
function `mneme hook path-owned <path>` exposes as a standalone subcommand —
**in-process**, not as a subprocess. It answers whether an **implementer**
subagent (`backend`, `frontend`, `bug-hunter`) owns the candidate path via its
declared `areas` globs.

**Decision table** (`resolvePathOwnership`, `internal/cli/hook.go`):

| Manifest state | Path state | Result |
|---|---|---|
| Present, non-empty | Matches an implementer's `areas` entry (`areaMatches`, SPEC-084 D2) | **BLOCK**, names the first matching role in manifest order |
| Present, non-empty | No implementer's `areas` matches | **ALLOW** |
| Absent (no row), or present but `[]` | any | **BLOCK**, owner `legacy` — deny-by-default so projects that have not run the `mneme-init` grill keep today's protection |
| Path is empty or falls outside the project tree | any | **ALLOW** — a path that cannot be normalised relative to the project cannot be owned |
| Hard failure: config unreadable, DB unreadable/corrupt, manifest JSON unparsable | — | **ALLOW** — fail-open |

**`areas` glob semantics (SPEC-084 D2):** each entry is interpreted
recursively, not just as a literal glob — `areaMatches(area, path)` is
`Match(cleaned, path) || Match(cleaned+"/**", path)`, where `cleaned` trims
whitespace and a leading `./` / trailing `/`. A bare directory entry
(`apps/web-ui`) therefore owns everything underneath it, matching what the
`mneme-init` grill has always generated; an entry that is already a glob
(`internal/**`) is unaffected (the union is idempotent). An empty or
whitespace-only entry is ignored rather than expanding to `**`; `.` or `./`
explicitly resolve to `**`. See `docs/enforcement-model.md`'s "`areas` glob
semantics" section for the full rationale.

**Manifest lookup is project-scoped (SPEC-084 D4):** the query is `WHERE
topic_key = 'subagents/manifest' AND project = ? AND scope = 'project' AND
deleted_at IS NULL ORDER BY updated_at DESC, id DESC LIMIT 1`, not a bare
`topic_key` lookup — a project's database can contain manifest rows
belonging to other projects (test runs, an imported/merged database), and
without the `project`/`scope` filter the query could return one of those
instead of the caller's own manifest. `project` and `scope` together
complete `idx_memories_upsert`'s real unique key (`topic_key, project,
scope`) — `scope` is not decorative.

**Cost:** one read-only SQLite open + one indexed `SELECT` by
`topic_key`+`project`+`scope`, per invocation — no subprocess spawn at all (the
process running the hook already **is** `mneme`). Most orchestrator writes
never reach this path (they land in `.claude/`, `~/.mneme/`, or `docs/*.md`,
all covered by the static whitelist).

The standalone `mneme hook path-owned <path>` subcommand still exists as
general-purpose surface / backward compatibility, but `enforce-delegation` no
longer invokes it as a subprocess.

### Tools intercepted

- `Write`, `Edit`, `MultiEdit`, `NotebookEdit` — file path extracted from
  `tool_input.file_path`.
- `Bash` — redirect targets (`>`, `>>`, `2>`), `sed -i`, `perl -i`, `mv`, `cp`,
  `rm`, `touch`, `chmod`, `chown`, `ln`, `tee`, `dd of=`, here-docs, and inline
  Python/Node scripts with file writes.

### Shell command tokenizer

`EvaluateBash` (in `internal/enforcement`) tokenizes commands by calling
`shell.Tokenize` **in-process** — a real bash AST parser (`mvdan.cc/sh/v3`)
that eliminates false positives caused by quoted arguments or heredoc content.
There is no subprocess spawn and no fallback parser: `shell.Tokenize` is
always available to the guard (the guard's own binary links the package
directly), so the legacy awk/regex parser and the `USE_GO_TOKENIZER` probe
from the bash-script era no longer exist.

**Token types:**

| Type | Description | Hook action |
|------|-------------|-------------|
| `word` | Command name or argument | Check if it is a dangerous command (only when `quoted: false`) |
| `redirect` | Redirect operator (`>`, `>>`, `2>`, etc.) | Emit; next token is the target |
| `redirect_target` | File path after a redirect | Validate against whitelist |
| `heredoc_body` | Content of a `<<EOF` block | **Skip entirely** — not a command |
| `command_substitution` | Content of `$(...)` | Re-tokenize 1 level |

The standalone `mneme hook tokenize` subcommand still exists (general-purpose
surface / backward compatibility), but `enforce-delegation` no longer invokes
it as a subprocess — see [`mneme hook tokenize`](api/cli.md) for its own
usage as a standalone tool.

### Fail-open behaviour

Any error — malformed JSON, unreadable stdin, unresolvable `cwd`, unreadable
config, a hard manifest-DB failure — causes the hook to exit 0 (allow). A
broken hook must never prevent the agent from working. Since SPEC-069 the
guard runs entirely in-process (no `jq`, no bash interpreter, no subprocess
round-trip), so there is no longer a runtime-dependency failure mode to
document — the only prerequisite is that `mneme` itself is on `PATH`.

### Lifecycle-tool denial (SPEC-087 D5)

`enforce-delegation` also intercepts two MCP tool names — `spec_advance` and
`spec_quick` — for any resolved subagent, unconditionally: a subagent calling
either is blocked (exit 2), no mode, no config, no ramp. This closed the gap
that let a subagent prematurely mark a spec `done` (SPEC-063).

```
mcp__mneme__spec_advance   -> exit 2 (blocked)
mcp__mneme__spec_quick     -> exit 2 (blocked)
mcp__mneme__spec_pushback  -> exit 0 (allowed)
mcp__mneme__spec_reject    -> exit 0 (allowed)
mcp__mneme__spec_doc_write -> exit 0 (allowed)
```

Three properties distinguish this guard from the subagent-containment guard
above:

- **Exact-match, never a prefix.** `lifecycleTools` (`internal/cli/hook.go`)
  is a two-entry map. A `strings.HasPrefix(tool, "mcp__mneme__spec_")` would
  also catch `spec_pushback` and `spec_doc_write` — the two SDD tools a
  subagent is specifically meant to use.
- **Runs before everything else in `runHookEnforceDelegation`** — before the
  `delegationTools` filter (MCP tool names are never file/Bash tools, so
  that filter would otherwise skip past them entirely) and before the
  `RoleSource=="unresolved"` short-circuit. It only needs
  `identity.IsSubagent` (i.e. `agent_id` present), never `agent_type`: if
  Claude Code ever stops sending `agent_type` and subagent-containment loses
  its signal (see "Role resolution" above), this block keeps working.
- **No discovery memory on block** — the "orchestrator bypassed SDD"
  narrative (SPEC-069) does not apply to a contained subagent; only a
  best-effort `enforcelog` event is recorded (`Reason:
  "lifecycle_tool_denied_to_subagent"`).

**The block message is load-bearing**: a subagent profile generated before
SPEC-087 D4 still instructs the agent to call `spec_advance` in its own
system prompt (layer-1 "agent-fixed" block, version 1). The message tells
the agent its profile is stale and names the fix directly:

```
⛔ mneme: spec_advance es del orquestador, no de un subagente. Si tu perfil
te pide avanzar la spec, está desactualizado (regenéralo: mneme subagents
regen). Reporta tu resultado y termina; el orquestador avanzará.
```

`mneme subagents doctor` reports `stale_agent_fixed` for any manifest entry
whose `Version` is behind `subagents.AgentFixedVersion` (currently 2); `mneme
subagents regen [--role R] [--all] [--dry-run]` regenerates the file(s) in
place, preserving any hand-authored area sections byte-for-byte. See
[`docs/subagents.md`](subagents.md).

### Inherent limits (Layer 2 scope)

`mneme hook enforce-delegation` is Layer 2: it stops the **cooperative
orchestrator** from accidentally editing source code. It is **not a sandbox**.

Bypass patterns that are **out of scope by design**:

| Pattern | Why not closed |
|---------|---------------|
| `echo <b64> \| base64 -d \| bash` | Arbitrary indirection; requires a full sandbox to close |
| Custom binaries writing files | Hook cannot introspect arbitrary executables |
| `ruby -e`, `php -r`, etc. | Unlisted interpreters |

**Primary defense** against subagent misbehavior is **Layer 1** (capability
`tools:` allowlist in `agents/*.md`). Claude Code enforces allowlists natively
before the hook is invoked.

### Installation, updates, and migration from the legacy `.sh` (SPEC-069)

```bash
# Install or update (idempotent; migrates a legacy .sh registration if found):
mneme install claude-code

# Force replace-all of PreToolUse entries (also rewrites the shim on disk):
mneme install claude-code --reinstall-hooks
```

Both the default path and `--reinstall-hooks` perform a **strip-then-add**:
any pre-existing `PreToolUse` entry whose command ends in
`enforce_delegation.sh` (any machine's home directory) is removed before the
portable `mneme hook enforce-delegation` entry is added, so re-running install
after an upgrade never leaves a stale absolute-path entry alongside the new
one. `mneme upgrade`'s post-upgrade install re-run uses the default (non
`--reinstall-hooks`) path, which is why the strip lives there rather than only
in the reinstall branch.

For a **per-repo** registration (`.claude/settings.json` inside a specific
project instead of the global `~/.claude/settings.json`), the same
strip-then-add runs in `mneme delegation-hook enable` — see
[Enforcement Model](enforcement-model.md) for the per-repo opt-in workflow.

The embedded `enforce_delegation.sh` asset itself is still written by
`WriteDelegationHook` (checksum-based idempotency, `.bak-YYYYMMDD-HHMMSS`
backup on change) — it is now the ~6-line compat shim described above, kept
so that any settings.json still pointing at the old absolute path keeps
working (forwarding to the same in-process Go logic) until it is
re-registered with the portable command.

### Hook registration identity (SPEC-107)

`mneme install` writes hook registrations into third-party JSON files
(`~/.claude/settings.json`, `~/.codex/hooks.json`,
`<repoRoot>/.claude/settings.json`) using an append-if-absent merge. The
question "is this hook already registered?" no longer requires the
registered command to be the exact literal string mneme would write — it is
answered by **identity**: the basename of the executable (`mneme` or
`mneme.exe`, case-insensitive, exact name — never a suffix or prefix match)
plus the `mneme hook <subcommand>` subcommand, ignoring the path used to
invoke it and any extra arguments. These three registrations are therefore
the SAME hook:

```
mneme hook session-start
/Users/x/.local/bin/mneme hook session-start
C:\Users\x\go\bin\mneme.exe hook session-start
```

This matters in practice when a user customises a registered command —
most commonly to an absolute path, because the shell that launches the
agent doesn't resolve `mneme` on its PATH (see "PATH troubleshooting"
below). Before this identity comparison existed, the next `mneme install`
did not recognise the customised entry and appended its own canonical one
alongside it — a silent duplicate, confirmed live 2026-08-04 against a
hand-edited `~/.codex/hooks.json` (double context injection every
session). Personalising the command with a path no longer produces a
duplicate.

What this identity check explicitly does **NOT** recognise as mneme's own
(and therefore never touches):

- a wrapper script of your own, even one whose *contents* happen to invoke
  `mneme hook <sub>` internally (e.g. `/Users/x/.codex/mi-script.sh`) — the
  identity is textual over the registered command string, never over what a
  script does when it runs;
- `sh -c "mneme hook session-start"`, `echo "mneme hook session-start"`, or
  any other shell wrapper — the first word of the command must itself be
  the `mneme`/`mneme.exe` executable;
- any command containing a shell pipeline, redirection, or substitution
  outside quotes (`|`, `&`, `;`, `<`, `>`, `$`, `` ` ``, `(`, `)`) — these are
  compound expressions mneme cannot safely reason about, so the safe
  posture is to leave them alone entirely;
- any other executable basename, even one that looks close —
  `mnemex hook session-start` and `my-mneme hook session-start` do **not**
  match (exact basename equality, never a suffix or prefix).

**A note on the destructive side.** The same identity now governs what
mneme's own purges (retiring a stale hook, disabling a per-repo delegation
hook) remove. When mneme retires a hook it wrote in the past, the purge
takes the customised variant with it too — **including any extra flags you
had added** to that registration. This is the correct semantics (the hook
is dead either way), but it is a silent loss of a personalisation that is
worth knowing about upfront rather than discovering after the fact.

If a registered entry doesn't match any of the forms above — for example
`mneme --data-dir /tmp hook session-start`, where a global flag sits before
`hook` — the identity check intentionally does not recognise it (skipping
global flags would require knowing their arity, coupling this code to the
CLI's flag set). `--reinstall-hooks` is the sanctioned way to sanitise such
an entry: it replaces the entire `PreToolUse` list unconditionally,
regardless of what identity check would or wouldn't recognise.

### Troubleshooting: `mneme` not found on PATH

If a hook fails to run because the agent's process can't resolve `mneme`,
the durable fix is a symlink into a directory that IS on the system PATH:

```bash
sudo ln -sf "$(command -v mneme)" /usr/local/bin/mneme
```

`/usr/local/bin` is on the system PATH by default on macOS, so an agent
launched from anywhere (including Finder/Dock, not just a terminal) will
resolve `mneme` afterwards.

mneme deliberately does **not** write your shell profile and does **not**
run a preflight check for this at install time. Both were considered and
rejected:

- **A preflight check would run in the terminal**, where `mneme install`
  itself is invoked — and the binary always resolves there, by definition
  (you just ran it). It would never catch the case that actually matters:
  an agent launched by a process that does NOT source your shell profile.
- **Writing the PATH into your shell profile (`.zshrc`, etc.) doesn't cover
  that case either.** Verified: `/etc/paths` does not contain
  `~/.local/bin`, and `launchctl getenv PATH` is empty on a stock macOS
  install — so a process launched from Finder/Dock (as many agent runtimes
  are) never reads `.zshrc` and never sees a profile-only PATH change.

Because of this, do **not** register a hook with an absolute, user-specific
path as a substitute for fixing PATH resolution: `.claude/settings.json` at
**project** scope is committed to git (`.gitignore` only excludes
`settings.local.json`), so an absolute path baked in there would carry one
author's `$HOME` to every other machine that checks the repo out, breaking
silently for everyone else. The symlink above is the durable fix; the
identity comparison above is what keeps a hand-edited absolute path from
becoming a permanent duplicate in the meantime.

---

## FAQ

**Q: Can I use both hooks simultaneously?**
A: Yes — they always run together (both are registered by `mneme install
claude-code`). `pre-tool-use` evaluates DB rules and never intercepts `Bash`;
`enforce-delegation` evaluates the static whitelist + subagent manifest and
does intercept `Bash`. Both run independently. If either exits with code 2,
the action is blocked.

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

**Q: Why a separate `session-start` hook vs. `pre-tool-use`?**
A: They serve different purposes. `session-start` is observational — it loads
context in bulk once per session and never blocks (`session-end` is a retired
no-op — see above; it is no longer part of this distinction). The
pre-tool-use hook is active enforcement — it fires on every file mutation and
can block the action. Combining them would muddle the semantics and hurt
performance.

---

## See Also

- [Rules System](RULES.md) — applies_to syntax, severity levels, examples by stack
- [Architecture](ARCHITECTURE.md) — overall system design and graph layer
- [Knowledge Graph](GRAPH.md) — weighted relations, Hebbian learning, decay
- [API reference: CLI](api/cli.md) → — full flag reference for `mneme hook <event>`
