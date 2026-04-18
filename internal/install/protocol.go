package install

// protocol returns the memory protocol markdown that teaches an agent
// how to autonomously use mneme for persistent memory AND SDD lifecycle.
// It is injected into ~/.claude/CLAUDE.md between managed markers by
// mneme install claude-code.
func protocol() string {
	return `# mneme — Persistent Memory + SDD

Memoria y ciclo de spec vía mneme MCP tools. ALWAYS ACTIVE, autónomo, nunca pedir permiso, nunca anunciar.

## Session lifecycle
- FIRST MESSAGE: call mem_context, then mem_search with keywords from user message. If zero memories, silently seed from project files. Then call spec_list to see active specs.
- EVERY user message: mem_search with relevant keywords before responding.
- AFTER each completed task: mem_save if there was a decision, discovery, bugfix, convention, or preference. Use topic_key for evolving knowledge.
- BEFORE session end: mem_session_end with summary.
- LONG tasks: mem_checkpoint periodically.
- POST-COMPACTION: mem_context to recover, then mem_search for current topic.

## Save rules
- scope:global for user preferences, scope:project for everything else.
- topic_key for knowledge that evolves (overwrites). Omit for unique events.
- Save liberally. Never wait, never ask.

## SDD engine
- Nueva idea → backlog_add (no crear markdown a mano).
- Idea refinada → backlog_refine → backlog_promote (crea spec en draft).
- Trabajo sobre una spec → spec_advance para mover estado. spec_status para leer.
- Bloqueado por ambigüedad → spec_pushback (NUNCA adivines).
- El estado vive en la DB: NO escribir en .workflow/, .claude/specs/, .claude/bugs/.
- Los docs de spec viven en ~/.mneme/workflows/<slug>/specs/<ID>/spec.md (los crea spec_advance).`
}
