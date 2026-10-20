<p align="center"><img src="assets/logo.svg" width="96" alt="mneme"></p>

# mneme

Persistent memory for AI coding agents, a spec-driven workflow with a human approval gate, and per-role permissions for the agents that do the work. One binary, no runtime dependencies.

[![License](https://img.shields.io/badge/License-Apache%202.0-0d8f80.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8.svg)](https://go.dev)
[![Release](https://img.shields.io/github/v/release/wirvii/mneme?label=release)](https://github.com/wirvii/mneme/releases)
[![MCP Tools](https://img.shields.io/badge/MCP%20tools-31-0d8f80.svg)](#mcp-tools)

## What is mneme?

Agents forget everything between sessions: the decision you made last week, the bug you already chased, the convention nobody wrote down. mneme keeps that knowledge in a local SQLite database (full-text search plus a weighted knowledge graph) and hands the relevant part back at the start of each session.

It also gives the work itself a shape:

- **Memory.** Decisions, discoveries, bugfixes and conventions are saved as they happen and retrieved by relevance, not by recency alone.
- **A flow with a human gate.** Ideas become backlog items, then specs. A spec is designed, then presented to the person in plain words, and nothing is built until they say an explicit yes.
- **Roles with permissions.** The architect designs and cannot edit code; implementers edit inside their own area; the QA tester verifies and cannot edit code. Capability lists and hooks enforce this, so it does not depend on the agent behaving.

It works with Claude Code and with OpenAI Codex; both get the same roles, skills and guarantees.

## Quickstart

```bash
# Install (needs Go 1.25+, no C compiler)
go install github.com/wirvii/mneme/cmd/mneme@latest
# or a pre-built binary:
curl -sSL https://raw.githubusercontent.com/wirvii/mneme/main/install.sh | sh

# Wire it into your agent: MCP server, hooks, operating manual, skills
mneme install claude-code      # or: mneme install codex

# Use it
mneme save --type decision --title "Auth uses JWT RS256" \
  --content "Switched to RS256 for asymmetric key verification."
mneme search "authentication"
mneme status
```

Restart any running agent session after installing. Then, inside a project, ask the agent to run the `mneme-init` skill: it seeds memories, writes the project's managed blocks and, if you want it, creates the per-project role files (`.claude/agents/*.md` and `.codex/agents/*.toml`).

## Skills

Skills are short instruction files an agent loads when it needs them, so the always-loaded manual stays around 2.7 KB. mneme installs them for Claude Code (`~/.claude/skills`) and Codex (`$HOME/.agents/skills`).

| Skill | Use it when |
|---|---|
| `mneme-flow` | any spec work: the cycle, who may move it, lanes, criteria |
| `feature` | the person wants something new |
| `bug` | something that worked now fails (investigate, then turn into work) |
| `trivial` | a tiny change in a declared scope |
| `grill-me` | refining a standard backlog item, one question at a time |
| `present-spec` | a spec reaches the human approval gate |
| `review-brief` | a spec goes to QA, or you are the one reviewing it |
| `mneme-init` | setting up a project's roles and memory |
| `codegraph` | the repository has an indexed code graph |

`mneme skills list|install|pin|lint|validate|usage` manages them. Pinning a skill (`pinned: true`) protects your edited copy from upgrades. `mneme skills usage` shows which skills agents actually loaded.

## The flow

```
backlog_add -> refine (grill-me) -> backlog_promote -> spec: draft -> speccing
  -> specced -> HUMAN APPROVAL -> planning -> planned -> implementing -> qa -> done
```

Every item has a lane. A **trivial** lane item (a tiny, low-risk change in a declared scope) is quick-started and checked by a deterministic auditor; everything else is **standard** and goes through every stage. The architect writes a `criteria.toml` for each standard spec: the measurable promises QA may reject against. Anything QA finds outside those promises becomes a new backlog item instead of bouncing the spec. Only the orchestrator advances a spec; subagents never do.

## Memory

- `mem_save` returns a `suggested_topic_key` when it can tell the note updates an earlier one, and `possible_conflicts` when it resembles existing notes. The agent judges them in the same turn with `mem_update` (`supersedes`, `conflicts_with`, `unrelated_to`).
- Notes carry a review date by type; an old note comes back from search with a `review_due` marker for the agent, and `confirm` renews it.
- `mem_session_end` closes a session, or checkpoints one with `partial:true`.
- Scopes: `global`, `org` and `project`. They never leak between projects.

## CodeGraph

Optional. `mneme codegraph index` builds a symbol-and-call graph of the repository so an agent can ask "who calls this" without reading ten files. Four tools are on MCP (`codegraph_search`, `codegraph_context`, `codegraph_callers`, `codegraph_impact`); the rest is the CLI. See [docs/codegraph.md](docs/codegraph.md).

## MCP Tools

mneme exposes **31** tools over JSON-RPC stdio:

| Family | Count | Tools |
|---|---|---|
| `mem_*` | 6 | `mem_save`, `mem_search`, `mem_get`, `mem_context`, `mem_update`, `mem_session_end` |
| `backlog_*` | 6 | `backlog_add`, `backlog_list`, `backlog_get`, `backlog_refine`, `backlog_promote`, `backlog_archive` |
| `spec_*` | 9 | `spec_new`, `spec_status`, `spec_advance`, `spec_pushback`, `spec_resolve`, `spec_doc_write`, `spec_list`, `spec_quick`, `spec_reject` |
| `lane_*` | 5 | `lane_audit`, `lane_reclassify`, `lane_override`, `lane_status`, `lane_stats` |
| `codegraph_*` | 4 | `codegraph_search`, `codegraph_context`, `codegraph_callers`, `codegraph_impact` |
| `init` | 1 | `init` |

Everything else (skills, models, conflicts, SDD files, per-project roles, the rest of the memory and graph tooling) is the CLI: `mneme --help`. Per-tool reference: [docs/api/](docs/api/).

## Team features, opt-in per repository

- **Team memory** (`mneme team-memory enable`): durable notes travel through git as one Markdown file each, in `.mneme/shared/`. No server, no account.
- **SDD files** (`mneme sdd enable`): backlog items and specs are also written as reviewable Markdown under `.mneme/sdd/`, and imported by teammates after every pull.

Both are off until you enable them. Read the privacy notice they print before you commit those directories to a public repository.

## Documentation

Start at [docs/README.md](docs/README.md), the index of every current document. The most useful: [docs/GUIDE.md](docs/GUIDE.md), [docs/skills.md](docs/skills.md), [docs/lanes.md](docs/lanes.md), [docs/enforcement-model.md](docs/enforcement-model.md), [docs/codex.md](docs/codex.md), [docs/CONFIG.md](docs/CONFIG.md).

## Windows

`go install github.com/wirvii/mneme/cmd/mneme@latest` is the supported install and upgrade path (`mneme upgrade` runs it for you and needs `go` on `PATH`). `mneme skills validate` and the git hooks need a POSIX `sh`, which [Git for Windows](https://gitforwindows.org/) provides.

## Upgrading from 1.x

Version 2.0 removes many MCP tools and several features. After `mneme upgrade`: restart your agent sessions, run `mneme install claude-code` (and/or `codex`) again, run `mneme upgrade` to be offered cleanup of leftovers, and run `mneme subagents regen --all` in each project. The full list is in [CHANGELOG.md](CHANGELOG.md).

## Development

```bash
make build         # go build -o mneme ./cmd/mneme
make test          # sandboxed go test ./... plus the test guard
make test-race
golangci-lint run  # zero warnings required
```

Fork, branch as `type/short-description`, use [Conventional Commits](https://www.conventionalcommits.org/), and open a PR. Architecture and conventions: [CLAUDE.md](CLAUDE.md).

## License

[Apache License 2.0](LICENSE)
