# Codex 0.147.0 hook capture

Captured from a real isolated `codex exec --ephemeral` run on 2026-08-14.

- `PreToolUse` fired for `collaborationspawn_agent`.
- The requested role appeared only at `tool_input.agent_type`; caller identity
  fields `agent_id` and top-level `agent_type` were absent.
- No `SubagentStart` hook fired.
- The subsequent collaboration wait contained no receiver thread id.

Consequently this run is evidence of an attempted spawn, not evidence that a
delegated role executed. Release QA must keep the delegated Codex E2E as
`not-run` until the runtime produces a real child and identity-bearing hook
payload. The JSON fixture beside this note is sanitized; encrypted prompt
bytes, local paths, UUIDs, and tool-use ids were removed.
