# Per-Agent Model Assignment (v1.8.0)

mneme assigns a model alias to each bundled agent at install time. This lets you
tune the cost/quality balance per SDD phase without editing agent files manually.

## Defaults and rationale

| Agent | Default | Rationale |
|---|---|---|
| `architect` | `opus` | Its output (the spec) propagates to all other agents; errors are expensive |
| `backend` | `sonnet` | Implementation follows a well-defined spec; efficient and sufficient |
| `frontend` | `sonnet` | Same as backend |
| `qa-tester` | `sonnet` | Verification against fixed criteria; strong enough for checklist-based review |
| `bug-hunter` | `sonnet` | Investigation in a well-scoped area; first candidate to upgrade if quality drops |

**Upgrade signal:** if `qa-tester` or `bug-hunter` misses too many issues, run
`mneme model set bug-hunter opus` before reinstalling.

## Config format

Overrides live in `~/.mneme/config.toml`:

```toml
[models.overrides]
bug-hunter = "opus"
```

This section is NOT an asset — `mneme install claude-code` never overwrites it.

## Known aliases

| Alias | Meaning |
|---|---|
| `opus` | Claude Opus series |
| `sonnet` | Claude Sonnet series |
| `haiku` | Claude Haiku series |
| `inherit` | Inherit from Claude Code's default |

Any non-empty string is accepted (open model string). Unknown aliases trigger a
WARNING but are never rejected — this keeps the field forward-compatible with
new Claude model families without requiring a mneme upgrade.

## Commands

```bash
mneme model list                     # show effective model + origin per agent
mneme model list --json              # JSON output
mneme model set bug-hunter opus      # override one agent
mneme model set backend banana       # sets banana with a warning
mneme model reset bug-hunter         # remove override, restore default
mneme model reset                    # remove all overrides
mneme install claude-code            # apply current assignments to ~/.claude/agents/
```

MCP equivalents (for agent use):

```
model_list                           # no parameters
model_set(agent, model)              # ErrUnknownAgent / ErrInvalidModel on bad input
model_reset(agent?)                  # optional agent; omit to reset all
```

## How apply-on-install works

1. `mneme install claude-code` calls `WriteAgents` to write bundled agent *.md files.
2. Immediately after, `ApplyAgentModels` reads `config.Models.Overrides`, resolves
   effective models (`ResolveEffectiveModels`), and calls `SetModelInFrontmatter` for
   each installed agent file.
3. Only the `model:` line changes; all other frontmatter fields (description, tools,
   permissionMode, YAML comments, body) are preserved byte-for-byte.

## Cross-provider note (deferred)

mneme does NOT support proxy, base-url, or cross-provider routing. The `model:`
field is a hint to Claude Code's agent runner — its semantics are defined by
Claude Code, not by mneme. Provider-agnostic support is deferred to a future spec.

---

## API reference

Full contract (params, returns, errors, examples) for the 3 `model_*` MCP
tools: [docs/api/models.md](api/models.md) →
