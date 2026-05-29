# mneme v0.9 — Permission Enforcement by Capability

**Status:** Ready for implementation
**Target release:** v0.9.0
**Estimated effort:** 1–2 days
**Authoring context:** Distilled from architecture design session, May 2026

---

## 1. Context & Motivation

In the current state of mneme (v0.8.x), role boundaries between subagents are declared in **prose** inside each agent's markdown file but are not enforced at the **capability** level.

Concrete evidence in the current codebase:

- `internal/install/assets/agents/architect.md` contains prose rules of the form "NUNCA implementar código", yet its YAML frontmatter is:

  ```yaml
  permissionMode: bypassPermissions
  # tools: field is ABSENT → inherits ALL tools from the main thread,
  # including Edit, Write, MultiEdit, Bash, NotebookEdit
  ```

  The architect is **physically capable** of editing code. The rule is a request, not a restriction.

- The orchestrator (main thread, governed by repo-level `CLAUDE.md`) has the same problem: instructed not to edit code directly, but has full tool access. Empirically, it does break the rule when it judges a change "trivial".

A `pre-tool-use` hook was implemented in a previous iteration that reads `agent_type` from the `PreToolUse` payload (absent for the principal, present for subagents) and blocks edits from the principal with exit code 2. **That hook is shipped and working.** However:

1. Without subagent allowlists, **architect** and **qa-tester** still have edit capability. Only the orchestrator is constrained.
2. Blocked attempts are silent. There is no telemetry on which agents attempt to violate rules, against which files, and how often.

This release closes both gaps.

## 2. Goal

Make role boundaries enforced **by capability** for all roles (not only the orchestrator), and **log every blocked attempt** as a `discovery` memory in mneme so the founder has queryable data on rule violations.

After this release:

- The architect and qa-tester subagents are physically incapable of editing code.
- The orchestrator remains blocked (status quo, no regression).
- Every blocked attempt produces a queryable `discovery` memory in mneme.
- The enforcement model is documented in `docs/enforcement-model.md` and summarised in `CLAUDE.md`.

## 3. Non-goals (explicit out-of-scope)

To prevent scope creep, **do not** include any of the following in this release. Each item below has its own future release:

- Skills framework (`SKILL.md` discovery, pinning, validators) → **v0.11**.
- Lane classifier (trivial vs standard SDD routing) → **v0.10**.
- Model-per-phase SDD profiles → **v0.13**.
- Memory conflict surfacing → **v0.14**.
- New MCP tools or changes to existing `mem_*` tools.
- Changes to the SDD command flow (`backlog → spec → ...`).
- Changes to the storage layer (SQLite schema, FTS5 indices, graph).
- Changes to retrieval, embeddings, or scoring.
- Subagent prompt rewrites beyond YAML frontmatter.
- New CLI top-level commands.
- New install targets or supported agents beyond what mneme already supports.

If during implementation a new abstraction feels needed, **stop and ask the founder**. Do not silently expand scope.

## 4. Background: code Claude Code must read before starting

Before writing any code, the implementing agent must read and understand:

| File | Why |
|---|---|
| `internal/install/assets/agents/architect.md` | Read-only agent, miswired today. Confirm current frontmatter. |
| `internal/install/assets/agents/qa-tester.md` | Read-only agent, similar issue. |
| `internal/install/assets/agents/backend.md` | Implementer. Confirm what it currently has. |
| `internal/install/assets/agents/frontend.md` | Implementer. |
| `internal/install/assets/agents/bug-hunter.md` | Implementer. |
| The current `pre-tool-use` hook | Located under `internal/hooks/` or `internal/install/assets/hooks/`. Locate it. It already blocks the orchestrator; we are extending it, not replacing it. |
| `CLAUDE.md` (repo root) | mneme's own agent instructions. New section will be added. |
| Existing `mem_save` CLI surface | Hook will invoke the mneme CLI to save blocked-attempt memories. Confirm the exact CLI signature. |

External references (consult only as needed):

- **Claude Code subagent docs**: the `tools:` field is an allowlist; when omitted, the subagent inherits all tools from the main thread.
- **Claude Code hook payload docs**: `PreToolUse` events include optional `agent_id` and `agent_type` fields, populated only when the call originates from a subagent dispatched via the Task tool. For calls from the principal, these fields are absent.
- **Gentleman-Programming/gentle-ai**, `skills/branch-pr/SKILL.md`: reference for the **"Critical Rules + Automated Checks + How to Fix"** documentation pattern we will apply to `docs/enforcement-model.md`.
- **Gentleman-Programming/engram**, `mem_save` MCP tool: reference for the structural shape of `discovery` memories.

## 5. Detailed Design

### 5.1 Subagent tool allowlists

Every subagent file under `internal/install/assets/agents/*.md` MUST declare an explicit `tools:` allowlist in its YAML frontmatter.

#### 5.1.1 Read-only agents

Applies to: `architect.md`, `qa-tester.md`.

**Before (current state of `architect.md`):**

```yaml
---
name: architect
description: ...
permissionMode: bypassPermissions
# tools: field absent → inherits Edit/Write/MultiEdit/Bash/NotebookEdit
---
```

**After:**

```yaml
---
name: architect
description: ...
tools: Read, Grep, Glob, NotebookRead, BashOutput, mcp__mneme__*
---
```

Rationale:

- `Read, Grep, Glob` — needed to navigate the codebase.
- `NotebookRead, BashOutput` — read-only inspection of notebooks and bash output from prior tool calls.
- `mcp__mneme__*` — mneme MCP tools (`mem_save`, `mem_search`, etc.) MUST remain available. The wildcard pattern matches all mneme tools.
- **NOT included**: `Edit`, `Write`, `MultiEdit`, `NotebookEdit`, `Bash` (execution).
- **Remove `permissionMode: bypassPermissions`**. Read-only agents have no need to bypass anything. Setting `permissionMode: default` is equivalent to omitting the field; prefer omission.

#### 5.1.2 Implementer agents

Applies to: `backend.md`, `frontend.md`, `bug-hunter.md`.

**After:**

```yaml
---
name: backend
description: ...
tools: Read, Grep, Glob, NotebookRead, NotebookEdit, BashOutput, Edit, Write, MultiEdit, Bash, mcp__mneme__*
# permissionMode: bypassPermissions  ← keep ONLY if currently set and document why
---
```

Rationale:

- Full edit + execution capability — they are the writers.
- If `permissionMode: bypassPermissions` is currently present, it may be retained for autonomous-run scenarios. **If retained, the agent's prose body must document explicitly that this is intentional for autonomous execution.** If unsure, ask the founder before adding or removing.

#### 5.1.3 MCP tool naming verification

Before finalising, confirm the actual MCP server name. The wildcard pattern `mcp__<server-name>__*` is Claude Code's convention. The mneme MCP server is currently registered as `mneme` (verify in `internal/install/` or by running `claude mcp list` after install).

**Fallback**: if the wildcard does not match all mneme tools at runtime (e.g., due to tool registration quirks), list each tool explicitly:

```yaml
tools: Read, Grep, Glob, NotebookRead, BashOutput,
       mcp__mneme__mem_save, mcp__mneme__mem_search,
       mcp__mneme__mem_context, mcp__mneme__mem_timeline,
       mcp__mneme__mem_judge, mcp__mneme__mem_update,
       mcp__mneme__mem_delete, mcp__mneme__mem_doctor
       # ... all currently exposed mneme tools
```

Document the chosen pattern in `docs/enforcement-model.md`.

### 5.2 Hook enhancement: log blocked attempts as memories

The existing `pre-tool-use` hook currently:

1. Reads the `PreToolUse` payload from stdin.
2. Inspects `agent_type` and the requested tool name.
3. Blocks (exit code 2) when an unauthorized edit is attempted by the principal.

This release extends it with one new responsibility: **log every blocked attempt to mneme as a `discovery` memory**.

#### 5.2.1 Implementer allowlist (decision rule)

Define a constant in the hook source:

```
IMPLEMENTER_AGENT_TYPES = {"backend", "frontend", "bug-hunter"}
EDIT_TOOLS = {"Edit", "Write", "MultiEdit", "NotebookEdit"}
```

Block rule (pseudo-code):

```
if tool in EDIT_TOOLS:
    if agent_type is None or agent_type not in IMPLEMENTER_AGENT_TYPES:
        log_blocked_attempt(...)
        exit(2)
exit(0)
```

#### 5.2.2 Memory schema for blocked attempts

Use the existing `discovery` memory shape exposed by `mneme mem save`. Populate fields as follows:

| Field | Value template |
|---|---|
| `type` | `discovery` |
| `title` | `Blocked edit: <agent_label> → <tool> → <basename(target_path)>` |
| `what` | `Attempted <tool> on <full target_path>. Agent label: <agent_label>. Session: <session_id if available, else "unknown">.` |
| `why` | `Capability rule fired: <agent_label> is not in implementer allowlist [backend, frontend, bug-hunter]. Edit tools require implementer role.` |
| `learned` | One of the variants in §5.2.3 |
| `tags` | `["enforcement", "blocked", "<agent_label>"]` |
| `project` | Inferred from current working directory using existing mneme project resolution; fall back to `unknown` if resolution fails |

Where `<agent_label>` is:

- `principal` when `agent_type` is absent in the payload.
- The value of `agent_type` otherwise.

#### 5.2.3 `learned` field variants

Pick the variant matching `<agent_label>`:

| Label | `learned` text |
|---|---|
| `principal` | `Pattern to watch: orchestrator attempted to edit directly instead of delegating. Likely cause: agent judged change "trivial" and bypassed SDD. Consider whether the task should have been routed via a lane classifier.` |
| `architect` | `Pattern to watch: architect attempted implementation instead of producing a spec. Likely cause: prompt drift or insufficient delegation back to the orchestrator. Reconsider architect prompt boundaries.` |
| `qa-tester` | `Pattern to watch: qa-tester attempted to modify code instead of reporting findings. Likely cause: prompt drift toward "fixer" role. Reconsider qa-tester prompt boundaries.` |
| any other | `Pattern to watch: unauthorized role attempted edit. Check whether the agent's tools allowlist matches its intended role.` |

#### 5.2.4 Invocation method

The hook is implemented per the existing approach (shell script and/or Go binary — preserve whichever is in use). To save the memory, call the mneme CLI as a subprocess:

```bash
mneme mem save \
  --type discovery \
  --title "Blocked edit: principal → Edit → settings.json" \
  --what "Attempted Edit on /path/to/settings.json. Agent label: principal. Session: abc123." \
  --why "Capability rule fired: principal is not in implementer allowlist [backend, frontend, bug-hunter]. Edit tools require implementer role." \
  --learned "Pattern to watch: orchestrator attempted to edit directly ..." \
  --tag enforcement --tag blocked --tag principal \
  || true
```

**Critical constraint:** memory-save failures MUST NOT block the actual block decision. The hook exits 2 regardless of whether the memory save succeeded. Save failures should log to stderr (visible to the user) but never alter the exit code from the block path.

Confirm `mneme mem save` supports all the CLI flags above (`--type`, `--title`, `--what`, `--why`, `--learned`, `--tag`). If the current CLI does not, **add the missing flags as part of this release** (they are minor surface additions, not new tools).

#### 5.2.5 Deduplication

If the same agent attempts the same edit on the same file multiple times within a short window (retry loop), duplicate memories will be created. **This is acceptable for v0.9** — it provides raw signal. A future release may add deduplication. **Do NOT implement dedup here.**

### 5.3 Documentation updates

#### 5.3.1 `CLAUDE.md` (repo root) — add "Enforcement Model" section

Add a new section to mneme's root `CLAUDE.md` (insert before the existing sections on subagent usage, or at the most natural insertion point):

```markdown
## Enforcement Model

mneme enforces role boundaries at two layers:

1. **Capability (primary)**: every subagent declares an explicit `tools:`
   allowlist in its YAML frontmatter. Read-only agents (architect,
   qa-tester) physically cannot edit code because they lack `Edit`,
   `Write`, `MultiEdit`, `NotebookEdit`, and `Bash`.

2. **Hook (defense in depth)**: the `pre-tool-use` hook inspects the
   `PreToolUse` payload from stdin. When `agent_type` is absent
   (orchestrator) or not in `[backend, frontend, bug-hunter]`, edit
   attempts are blocked with exit code 2.

Every blocked attempt is logged as a `discovery` memory and is
queryable via `mneme mem search "Blocked edit"` or
`mneme mem search --tag enforcement`. Use these memories to identify
patterns of rule violation and refine subagent prompts or process
design.

To add a new subagent or debug a blocked edit, see
`docs/enforcement-model.md`.
```

#### 5.3.2 New file: `docs/enforcement-model.md`

Create a new doc following the **gentle-ai `branch-pr/SKILL.md` pattern** (Critical Rules + Automated Checks table + How to Fix column).

Structure:

```markdown
# Enforcement Model

## When to read this
- Adding a new subagent to mneme
- Debugging why an edit was blocked
- Onboarding to mneme's role-based architecture

## Critical Rules
1. Read-only agents (architect, qa-tester) MUST NOT include `Edit`,
   `Write`, `MultiEdit`, `NotebookEdit`, or `Bash` in their `tools:`
   allowlist.
2. Implementer agents (backend, frontend, bug-hunter) MUST include the
   full edit toolset.
3. No agent MAY use `permissionMode: bypassPermissions` for read-only
   purposes — that flag is reserved for implementers in autonomous
   runs.
4. All subagents MUST include `mcp__mneme__*` (or explicit per-tool
   listings) to retain access to mneme memory tools.

## Automated Checks

| Check                         | What it verifies                                                              | How to fix                                                        |
|-------------------------------|-------------------------------------------------------------------------------|-------------------------------------------------------------------|
| **Subagent allowlist exists** | Every agent `.md` under `internal/install/assets/agents/` has a `tools:` line | Add an explicit `tools:` line to the YAML frontmatter             |
| **Read-only agents minimal**  | architect & qa-tester have no edit tools                                      | Remove `Edit`, `Write`, `MultiEdit`, `NotebookEdit`, `Bash`       |
| **Implementer agents full**   | backend, frontend, bug-hunter have full edit toolset                          | Add the missing tools                                             |
| **mcp__mneme__\* present**    | Every agent retains mneme memory access                                       | Add `mcp__mneme__*` (or explicit per-tool list)                   |
| **No stray bypassPermissions**| Read-only agents do not set bypassPermissions                                 | Remove the line from their frontmatter                            |
| **Hook installed**            | `pre-tool-use` hook is registered in Claude Code settings                     | Run `mneme install claude-code`                                   |
| **Hook logs to mneme**        | Blocked attempts produce a `discovery` memory                                 | Ensure `mneme` is on PATH; check stderr of the hook for save errors |

## Adding a new subagent
1. Decide its role: read-only or implementer.
2. Create `internal/install/assets/agents/<name>.md`.
3. Use the appropriate frontmatter template from §5.1 of the v0.9 spec.
4. If implementer, add the agent name to `IMPLEMENTER_AGENT_TYPES` in
   the hook source.
5. Update this doc with the new agent.
6. Run `mneme install claude-code` and validate.

## Debugging a blocked attempt

| Symptom                                       | Likely cause                       | Fix                                              |
|-----------------------------------------------|------------------------------------|--------------------------------------------------|
| Architect can't read a file                   | Missing `Read` in allowlist        | Add `Read`                                       |
| Backend can't run tests                       | Missing `Bash` in allowlist        | Add `Bash`                                       |
| Orchestrator edited a file despite the hook   | Hook not installed                 | Run `mneme install claude-code`, restart agent   |
| Edit blocked but no memory saved              | `mneme` CLI not on PATH            | Verify `which mneme`; reinstall binary           |
| Subagent edit blocked unexpectedly            | Agent name not in IMPLEMENTER list | Add to `IMPLEMENTER_AGENT_TYPES` in hook source  |

## How the hook decides

(Pseudo-code mirroring §5.2.1 of the spec.)
```

### 5.4 Tests

#### 5.4.1 Unit tests

Add Go unit tests for the payload parser and the decision rule (assuming the hook is implemented in Go or has a Go core). Place under `internal/hooks/pretool/parser_test.go` or equivalent.

Scenarios:

| # | Input                                                         | Expected outcome                          |
|---|---------------------------------------------------------------|-------------------------------------------|
| 1 | Valid payload, `agent_type=backend`, `tool=Edit`              | Allow (exit 0), no memory save           |
| 2 | Valid payload, `agent_type=architect`, `tool=Edit`            | Block (exit 2), memory save attempted    |
| 3 | Valid payload, no `agent_type`, `tool=Edit`                   | Block (exit 2), memory save attempted, label=`principal` |
| 4 | Valid payload, `agent_type=backend`, `tool=Read`              | Allow (exit 0), no memory save           |
| 5 | Valid payload, `agent_type=qa-tester`, `tool=Write`           | Block (exit 2), memory save attempted    |
| 6 | Malformed JSON                                                | Graceful error, non-zero exit, no crash, stderr message |
| 7 | Valid payload, `agent_type=architect`, `tool=Read`            | Allow (exit 0), no memory save           |
| 8 | Valid payload, `agent_type=bug-hunter`, `tool=MultiEdit`      | Allow (exit 0), no memory save           |

#### 5.4.2 Integration tests

Place under `internal/hooks/pretool/integration_test.go` or equivalent. These spin up a temporary mneme SQLite DB (or use mneme's existing test harness for the DB), invoke the hook binary with stdin payloads, and assert outcomes.

Scenarios:

1. Principal `Edit` attempt → blocked, memory created with `tag=principal`.
2. Architect `Edit` attempt → blocked, memory created with `tag=architect`.
3. Backend `Edit` attempt → allowed, no memory created.
4. Backend `Read` attempt → allowed, no memory created.
5. mneme CLI unavailable (PATH manipulation pointing to a nonexistent binary) → blocked, exit code still 2, stderr warning visible, no crash.

For each "memory created" assertion, query the test SQLite via mneme's existing read APIs and assert:

- `type == "discovery"`
- `title` starts with `"Blocked edit: "`
- All required tags are present
- `what`, `why`, `learned` fields are populated and non-empty.

## 6. File map

| File                                                  | Action  |
|-------------------------------------------------------|---------|
| `internal/install/assets/agents/architect.md`         | EDIT — frontmatter (tools allowlist, remove bypassPermissions) |
| `internal/install/assets/agents/qa-tester.md`         | EDIT — frontmatter (same as architect) |
| `internal/install/assets/agents/backend.md`           | EDIT — frontmatter (explicit full toolset) |
| `internal/install/assets/agents/frontend.md`          | EDIT — frontmatter (explicit full toolset) |
| `internal/install/assets/agents/bug-hunter.md`        | EDIT — frontmatter (explicit full toolset) |
| Hook source (`internal/hooks/pretool/*.go` or `internal/install/assets/hooks/pre-tool-use.sh`) | EDIT — add memory-save logic per §5.2 |
| Possibly `cmd/mneme/mem.go` or equivalent              | EDIT — ensure `mneme mem save` CLI supports `--type`, `--title`, `--what`, `--why`, `--learned`, `--tag` flags; add any missing ones |
| `CLAUDE.md` (root)                                    | EDIT — add "Enforcement Model" section per §5.3.1 |
| `docs/enforcement-model.md`                           | NEW — full reference per §5.3.2 |
| `internal/hooks/pretool/parser_test.go`               | NEW — unit tests per §5.4.1 |
| `internal/hooks/pretool/integration_test.go`          | NEW — integration tests per §5.4.2 |
| `CHANGELOG.md`                                        | EDIT — add v0.9.0 entry summarising the changes |

## 7. Acceptance criteria

A reviewer (the founder) will walk this checklist before merging. Every box must be checked.

### Code — subagents
- [ ] All 5 subagent `.md` files have an explicit `tools:` field in their YAML frontmatter.
- [ ] `architect.md` and `qa-tester.md` lack `Edit`, `Write`, `MultiEdit`, `NotebookEdit`, and `Bash` in their allowlist.
- [ ] `bypassPermissions` is removed from `architect.md` and `qa-tester.md`.
- [ ] All implementer agents (backend, frontend, bug-hunter) have the full edit toolset listed.
- [ ] `mcp__mneme__*` (or equivalent explicit listing) is in every agent's `tools:`.

### Code — hook
- [ ] Hook reads `agent_type` from `PreToolUse` payload (already in place; verify it still works).
- [ ] Hook calls `mneme mem save` on every block with the schema in §5.2.2.
- [ ] Hook exit code is 2 on block regardless of memory-save outcome.
- [ ] Hook exit code is 0 on allow.
- [ ] Failed memory-save logs to stderr but does not abort the block path.
- [ ] `IMPLEMENTER_AGENT_TYPES` set is defined in one place and exported/named for easy future modification.

### Code — CLI
- [ ] `mneme mem save` supports all flags used by the hook (`--type`, `--title`, `--what`, `--why`, `--learned`, `--tag`).

### Tests
- [ ] All 8 unit-test scenarios in §5.4.1 pass.
- [ ] All 5 integration-test scenarios in §5.4.2 pass.
- [ ] Existing mneme test suite still passes (no regression).

### Docs
- [ ] `CLAUDE.md` has the new "Enforcement Model" section per §5.3.1.
- [ ] `docs/enforcement-model.md` exists and includes all subsections from §5.3.2.
- [ ] `CHANGELOG.md` has a v0.9.0 entry.

## 8. Manual validation (post-merge)

After Claude Code reports completion and the founder merges, the founder runs:

1. `make build && go install ./cmd/mneme` — rebuild mneme.
2. `mneme install claude-code` — re-deploy agents and hooks.
3. Open Claude Code in a **test repo** (not mneme itself).
4. **Test 1 — Principal block.** Ask Claude (the orchestrator) directly: *"Edit `test.txt` and append the line 'hello'."* Expect: edit blocked with hook message visible. Memory created.
5. **Test 2 — Architect block.** Dispatch the architect via Task tool with the same request. Expect: blocked. Memory created with `tag=architect`.
6. **Test 3 — Backend allow.** Dispatch backend with the same request. Expect: edit succeeds.
7. **Test 4 — Architect read allow.** Dispatch architect with: *"Read `test.txt` and summarise it."* Expect: succeeds.
8. Run `mneme mem search "Blocked edit"` — expect at least 2 memories (one from Test 1, one from Test 2).
9. Run `mneme mem search --tag enforcement` — same expectation.
10. If any test fails, file an issue. Do not tag the release.

## 9. References

- **gentle-ai** (`Gentleman-Programming/gentle-ai`), `skills/branch-pr/SKILL.md` — template pattern for "Critical Rules + Automated Checks + How to Fix" documentation. Used as the structural model for `docs/enforcement-model.md`.
- **engram** (`Gentleman-Programming/engram`), `mem_save` MCP tool — reference for the shape of `discovery` memories (title / what / why / learned / tags pattern). mneme's existing schema already aligns with this; the spec only formalises field usage for blocked-attempt records.
- **Claude Code subagent docs** — behavior of the `tools:` allowlist (least-privilege when present, inherit-all when omitted), and the `PreToolUse` payload structure with `agent_id` / `agent_type` fields populated only for Task-dispatched subagents.
- **Existing mneme code** — the `pre-tool-use` hook that already blocks orchestrator edits. This release **extends, does not replace**.

## 10. Anti-scope reminders for the implementing agent

Before writing any code, the implementing agent **must internally confirm each of the following is true**:

- [ ] I am NOT introducing a skills framework.
- [ ] I am NOT introducing a lane classifier.
- [ ] I am NOT introducing model-per-phase SDD profiles.
- [ ] I am NOT adding memory conflict surfacing.
- [ ] I am NOT adding new MCP tools or modifying existing ones beyond the CLI flag additions in §6.
- [ ] I am NOT changing the SDD command flow.
- [ ] I am NOT touching storage, graph, retrieval, or scoring code.
- [ ] I am NOT rewriting subagent prompt bodies — only their YAML frontmatter.
- [ ] I am ONLY: editing 5 frontmatter blocks, extending one hook, ensuring CLI flags exist, updating two doc files, and adding tests.

If at any point during implementation a new abstraction "feels needed", **stop and surface the question to the founder** before introducing it. This release's value is its narrow scope.

---

**End of spec.**
