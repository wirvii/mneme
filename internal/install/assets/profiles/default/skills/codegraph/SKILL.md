---
name: codegraph
description: "Use when this repo has an indexed code graph (mneme codegraph status shows symbols) and you need to find where a symbol lives, who calls it, or what a change affects, before opening many files. Trigger: codegraph, who calls, blast radius, where is X defined."
version: 1.0.0
pinned: false
---

## When to Use
- A structure question (where is X, who calls X, what breaks if X changes) would otherwise mean reading 4+ files.
- `mneme codegraph status` reports symbols for this repo.
Skip it for literal text you already located (Read is right), for repos with no index, and for languages the graph says it does not cover (a one-line notice tells you).

## Critical Rules
1. The graph is optional. It saves tokens on structure questions; it never replaces reading the code you will change.
2. Over MCP there are 4 tools: `codegraph_search`, `codegraph_context`, `codegraph_callers`, `codegraph_impact`.
3. Everything else is CLI via Bash: `mneme codegraph callees <sym>`, `trace <from> <to>`, `node <sym>`, `explore <sym>...`, `files [pattern]`, `status`, `index`.
4. `codegraph_impact` and `callees` miss method calls (`x.Foo()`) and cross-package/stdlib calls. Before a refactor, confirm with Grep; never conclude "nobody calls X" from the graph alone.
5. Stale graph (old last-indexed time in `status`): run `mneme codegraph index`, or fall back to Grep/Read.

## Workflow
1. `codegraph_search` the name; pick the exact symbol.
2. `codegraph_context` for definition plus neighbours, or `codegraph_callers` for who uses it.
3. Before changing a shared symbol: `codegraph_impact`, then Grep the name to catch what the graph misses.
4. Read only the files the graph pointed to.

## Automated Checks
| Check | What it verifies | How to fix |
|---|---|---|
| Graph exists | `mneme codegraph status` shows symbols before results are trusted | Run `mneme codegraph index` or use Grep/Read |
| Impact confirmed | A refactor's impact list was cross-checked with Grep | Grep the symbol name before editing |
| Right surface | Only the 4 MCP tools were called over MCP; the rest went through `mneme codegraph …` | Use the CLI command from rule 3 |

## Verification
- `mneme skills lint codegraph` and `mneme skills validate codegraph` pass.
- `mneme codegraph adoption` shows graph use versus plain reads over time.
