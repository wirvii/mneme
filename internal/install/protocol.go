package install

// protocol returns the memory protocol markdown that teaches an agent
// how to autonomously use mneme for persistent memory.
//
// This is the most critical piece of the install package — it defines the
// agent's behaviour contract with mneme. Every section is mandatory; do not
// trim or paraphrase when injecting into agent configurations.
func protocol() string {
	return `# mneme — Persistent Memory Protocol

This protocol is MANDATORY and ALWAYS ACTIVE. You have persistent memory via mneme MCP tools. This memory survives across sessions, context compactions, and project switches.

## FIRST MESSAGE — MANDATORY (before responding to the user)

When the user sends their FIRST message in a session, BEFORE you respond:

1. Call ` + "`mem_context`" + ` to load project knowledge and last session summary.
2. If mem_context returns zero memories for this project, silently initialize it:
   - Read the project's CLAUDE.md, package.json/go.mod/Cargo.toml, and directory structure
   - Save key facts as memories (architecture, conventions, tech stack)
   - This is automatic — do not ask the user or announce what you're doing.
3. If there is a last session summary, review it to understand where work left off.
4. Call ` + "`mem_search`" + ` with keywords from the user's message to find relevant prior knowledge.
5. ONLY THEN respond to the user, informed by what you remembered.

This is not optional. Every session starts with memory recall. Never skip this step.

## PROACTIVE search — on EVERY user message

On any user message that references a topic, feature, file, bug, or decision:
- Call ` + "`mem_search`" + ` with keywords from the message BEFORE responding.
- If the user says "remember", "recall", "what did we do": call ` + "`mem_context`" + ` first, then ` + "`mem_search`" + `.
- Never respond to a technical question without checking memory first.

## Self-check after EVERY completed task

After completing any task, ask yourself:
- Did I make a decision the user should remember? → ` + "`mem_save`" + ` type:decision
- Did I discover something non-obvious? → ` + "`mem_save`" + ` type:discovery
- Did I fix a bug? → ` + "`mem_save`" + ` type:bugfix
- Did I learn a project convention? → ` + "`mem_save`" + ` type:convention
- Did I observe a user preference? → ` + "`mem_save`" + ` type:preference scope:global
If YES to any, call ` + "`mem_save`" + ` NOW. Do not wait. Do not ask.

## When to Save (Proactive — Do Not Ask)

Save a memory whenever you encounter or produce any of these:

| Trigger | Type | Example |
|---------|------|---------|
| Architecture decision | ` + "`decision`" + ` | "Switched to Redis for session storage" |
| Bug root cause found | ` + "`bugfix`" + ` | "N+1 query in UserList caused by missing DataLoader" |
| New pattern discovered | ` + "`pattern`" + ` | "Use errgroup for concurrent API calls with shared context" |
| Project convention learned | ` + "`convention`" + ` | "All API responses wrapped in {data, error, meta}" |
| System design documented | ` + "`architecture`" + ` | "Auth flow: JWT issued by gateway, verified by each service" |
| User preference observed | ` + "`preference`" + ` | "User prefers concise responses without summaries" |
| Configuration knowledge | ` + "`config`" + ` | "CI requires GOFLAGS=-tags=fts5 for SQLite FTS5" |
| Important discovery | ` + "`discovery`" + ` | "The legacy API returns 200 for errors with error in body" |

### How to Save

` + "```" + `
mem_save({
  title: "Verb + what (e.g. 'Fixed N+1 in UserList')",
  type: "decision|bugfix|pattern|convention|architecture|preference|config|discovery",
  scope: "project|global",  // global for user preferences, project for everything else
  topic_key: "category/name",  // for upserts — same key overwrites
  content: "## What\n...\n\n## Why\n...\n\n## Where\n...\n\n## Learned\n..."
})
` + "```" + `

Use ` + "`topic_key`" + ` for evolving knowledge (e.g. "architecture/auth-model") so updates overwrite
rather than duplicate. Omit topic_key for unique events (specific bugs, one-time discoveries).

## When to Search

- When you need context about how something works in this project
- When the user asks about past decisions or changes
- When you're about to modify code and want to know if there are conventions
- When you encounter something unfamiliar in the codebase

` + "```" + `
mem_search({ query: "authentication flow" })
// Returns truncated previews — use mem_get(id) for full content
` + "```" + `

## When to Relate (Knowledge Graph)

When you discover relationships between components:
` + "```" + `
mem_relate({
  source: "auth-service",
  target: "redis",
  relation: "depends_on",
  source_kind: "service",
  target_kind: "service"
})
` + "```" + `

## Session End (Mandatory)

Before ending ANY session, call mem_session_end:
` + "```" + `
mem_session_end({
  summary: "## Goal\n...\n\n## Accomplished\n...\n\n## Next Steps\n...\n\n## Relevant Files\n..."
})
` + "```" + `

This is NOT optional. The next session depends on this summary to pick up where you left off.

## Checkpoints (Compaction Insurance)

During long tasks, call mem_checkpoint periodically to snapshot your work state.
If context compaction occurs, the checkpoint ensures you can recover without losing progress.

` + "```" + `
mem_checkpoint({
  summary: "Brief description of what you're doing and current progress",
  decisions: "Key decisions made so far (optional)",
  next_steps: "What to do next if context is lost (optional)"
})
` + "```" + `

When to checkpoint:
- Before starting a multi-step operation (build, refactor, migration)
- After completing a significant sub-task within a larger task
- When you've accumulated important context that hasn't been saved as individual memories

You do NOT need to checkpoint on every message — only when losing context would cost real work.

## Post-Compaction Recovery

If you notice your context has been compacted (you've lost earlier conversation details):
1. Immediately call ` + "`mem_context`" + ` to reload project knowledge and last checkpoint
2. Call ` + "`mem_search`" + ` for any specific topic you were working on
3. Look for a checkpoint memory (topic_key "checkpoint/latest") in the context — it contains your last saved work state
4. Continue working from where the checkpoint left off

## Principles

- **Never ask permission** to save or search. Memory is autonomous.
- **Never announce** memory operations to the user ("I'm saving this to memory..."). Just do it.
- **Save liberally** — the consolidation pipeline handles cleanup. Better to save too much than too little.
- **Use topic_keys** for knowledge that evolves. Each save with the same key overwrites cleanly.
- **scope: global** for user preferences and personal patterns. These follow the user across projects.
- **scope: project** for everything specific to the current codebase.`
}
