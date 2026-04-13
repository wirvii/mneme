# mneme — Claude Code Hooks Integration

mneme can be automatically triggered by Claude Code hooks to capture memories
without the agent explicitly calling mem_save.

## Setup

Add to `~/.claude/settings.json`:

```json
{
  "hooks": {
    "SessionStart": [
      {
        "type": "command",
        "command": "mneme mcp-hook session-start"
      }
    ],
    "SessionEnd": [
      {
        "type": "command",
        "command": "mneme mcp-hook session-end"
      }
    ]
  }
}
```

## How it works

### SessionStart hook
When Claude Code starts a new session, mneme automatically provides project
context by calling `mem_context`. This gives the agent immediate awareness of:
- Previous session summaries
- Project architecture decisions
- Known conventions and patterns
- Recent discoveries and bug fixes

### SessionEnd hook
When a session ends, mneme prompts the agent to save a session summary
via `mem_session_end`, capturing what was accomplished and what's next.

### PostCompaction hook
After context compaction, mneme recovers lost context by loading the most
recent session summary and high-importance memories.

## Manual trigger

You can also trigger context loading manually:
```bash
mneme context --budget 4000
```
