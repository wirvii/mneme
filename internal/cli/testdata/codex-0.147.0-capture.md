# Codex 0.147.0 hook capture

Captured from a real isolated `codex exec --ephemeral` run on 2026-08-14.

- `PreToolUse` fired for `collaborationspawn_agent`.
- The requested role appeared only at `tool_input.agent_type`; caller identity
  fields `agent_id` and top-level `agent_type` were absent.
- No `SubagentStart` hook fired.
- The subsequent collaboration wait contained no receiver thread id.

An interactive follow-up proved that 0.147.0 does execute project roles, but
the child still receives neither project/global hooks nor an identity-bearing
`PreToolUse` payload. It also inherits the parent's workspace permission
profile instead of narrowing it from the role TOML. Therefore 0.147.0 is an
explicit incompatible fixture, not the v1.40 minimum.

The first verified compatible build is 0.148.0-alpha.19. In a real interactive
run it emitted `SubagentStart` with `agent_id` + `agent_type`, then a child
`PreToolUse` carrying the same identity when the architect called
`mem_search`. The two adjacent 0.148 fixtures are sanitized from that run;
encrypted prompts, local paths, UUIDs, and tool-use ids were replaced.
