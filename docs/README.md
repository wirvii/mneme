# mneme documentation

Index of the current documents. Start with the [README](../README.md) for what mneme is, and [CLAUDE.md](../CLAUDE.md) for how the code is organised.

## Using mneme

- [GUIDE.md](GUIDE.md): end-to-end guide: saving, searching, sessions, the backlog and specs, troubleshooting.
- [skills.md](skills.md): the skill format, the nine bundled skills, pinning, the usage log.
- [lanes.md](lanes.md): trivial and standard lanes and the auditor.
- [init.md](init.md): `mneme init`, managed blocks and the drift report.
- [codex.md](codex.md): using mneme with OpenAI Codex.
- [codegraph.md](codegraph.md): the optional code graph, its git hooks and how to measure token savings.
- [team-memory.md](team-memory.md): sharing durable knowledge through git.
- [sdd-git-native.md](sdd-git-native.md): backlog and specs as reviewable files under `.mneme/sdd/`.
- [conflicts.md](conflicts.md): memory conflicts at save time and how they are judged.
- [models.md](models.md): per-role model assignments.
- [CONFIG.md](CONFIG.md): every configuration key and its environment override.
- [VAULT.md](VAULT.md), [OBSIDIAN.md](OBSIDIAN.md): the Markdown mirror of the database and using Obsidian on it.
- [RULES.md](RULES.md): rules that block or warn on edits.
- [GRAPH.md](GRAPH.md): the weighted knowledge graph.
- [MEMORY-MANIFEST.md](MEMORY-MANIFEST.md): the portable manifest export format.

## Roles and enforcement

- [enforcement-model.md](enforcement-model.md): capability lists, hooks, containment and the lifecycle tools subagents cannot call.
- [HOOKS.md](HOOKS.md): every hook mneme registers for Claude Code and Codex.
- [skill-comply.md](skill-comply.md): the harness that measures whether an agent follows the flow.

## Reference

- [API.md](API.md): index of the API documents.
- [api/memory.md](api/memory.md), [api/sdd.md](api/sdd.md), [api/codegraph.md](api/codegraph.md): the 31 MCP tools by family.
- [api/cli.md](api/cli.md): the CLI commands and flags.
- [api/skills.md](api/skills.md), [api/models.md](api/models.md), [api/conflicts.md](api/conflicts.md), [api/subagents.md](api/subagents.md): administration that moved from MCP to the CLI in 1.49.
- [api/http.md](api/http.md): the REST API.
- [ARCHITECTURE.md](ARCHITECTURE.md), [mneme-internals-map.md](mneme-internals-map.md): design and a map of the internals.

## History (not maintained)

- [new/](new/): design notes and implementation reports of early specs (v0.9 to v1.9).
- [specs/](specs/): early spec documents.
- [superpowers/](superpowers/): design notes from outside the mneme flow.
- [PROGRESS.md](PROGRESS.md), [SPEC.md](SPEC.md): the original progress log and technical specification.
- [BRANDING.md](BRANDING.md): logo and naming notes.
