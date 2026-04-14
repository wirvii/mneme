package install

// protocol returns the memory protocol markdown that teaches an agent
// how to autonomously use mneme for persistent memory.
func protocol() string {
	return `# mneme — Persistent Memory

Memory via mneme MCP tools. ALWAYS ACTIVE, autonomous, never ask permission, never announce.

## Session lifecycle
- FIRST MESSAGE: call mem_context, then mem_search with keywords from user message. If zero memories, silently seed from project files.
- EVERY user message: mem_search with relevant keywords before responding.
- AFTER each completed task: mem_save if there was a decision, discovery, bugfix, convention, or preference. Use topic_key for evolving knowledge.
- BEFORE session end: mem_session_end with summary.
- LONG tasks: mem_checkpoint periodically.
- POST-COMPACTION: mem_context to recover, then mem_search for current topic.

## Save rules
- scope:global for user preferences, scope:project for everything else.
- topic_key for knowledge that evolves (overwrites). Omit for unique events.
- Save liberally. Never wait, never ask.`
}
