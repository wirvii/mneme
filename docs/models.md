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
mneme init                           # projects effective models to both runtimes
```

> Global install does not create role files. `mneme model set/list/reset`
> manages `[models.overrides]`; `mneme-init` and profile activation apply the
> effective assignment to each project's Claude Code and Codex projections.
> Claude aliases are mapped deliberately (`opus`/`sonnet`/`haiku` to verified
> Codex model ids); an unsafe or unknown cross-runtime mapping fails visibly.

MCP equivalents (for agent use):

```
model_list                           # no parameters
model_set(agent, model)              # ErrUnknownAgent / ErrInvalidModel on bad input
model_reset(agent?)                  # optional agent; omit to reset all
```

## How apply-on-install works

Global install **does not write role files**. The apply-on-install machinery
(`ApplyAgentModels`, `ResolveEffectiveModels`, `SetModelInFrontmatter`) is
preserved as generic capacity. Project role generation now applies the same
effective assignment to both native projections. In Claude Markdown, only the
`model:` line changes; all other frontmatter fields
(description, tools, permissionMode, YAML comments, body) are preserved
byte-for-byte.

## Cross-provider note (deferred)

mneme does NOT support proxy, base-url, or cross-provider routing. The `model:`
field is rendered in each runtime's native vocabulary. mneme maps only known
safe aliases and refuses silent cross-provider guesses.

---

## API reference

Full contract (params, returns, errors, examples) for the 3 `model_*` MCP
tools: [docs/api/models.md](api/models.md) →
