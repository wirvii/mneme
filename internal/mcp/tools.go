package mcp

// allTools returns the full list of ToolDefinitions exposed by the mneme MCP
// server. Each tool maps directly to a method on MemoryService. Schemas are
// defined inline as map[string]any following the JSON Schema draft-07 subset
// understood by MCP clients.
func allTools() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "mem_save",
			Description: "Save a structured observation to persistent memory.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"title", "content"},
				"properties": map[string]any{
					"title": map[string]any{
						"type":        "string",
						"description": "Short, searchable summary of the memory.",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "Full knowledge body, typically structured Markdown.",
					},
					"type": map[string]any{
						"type":        "string",
						"description": "Memory type: decision, discovery, bugfix, pattern, preference, convention, architecture, config, session_summary. Defaults to discovery.",
						"enum": []string{
							"decision", "discovery", "bugfix", "pattern",
							"preference", "convention", "architecture", "config", "session_summary",
						},
					},
					"scope": map[string]any{
						"type":        "string",
						"description": "Storage scope: global, org, or project. Defaults to project.",
						"enum":        []string{"global", "org", "project"},
					},
					"topic_key": map[string]any{
						"type":        "string",
						"description": "Stable dot-delimited key enabling idempotent upserts (e.g. architecture/auth-model).",
					},
					"project": map[string]any{
						"type":        "string",
						"description": "Project slug. Defaults to the detected project when omitted.",
					},
					"session_id": map[string]any{
						"type":        "string",
						"description": "Agent session ID to associate this memory with.",
					},
					"created_by": map[string]any{
						"type":        "string",
						"description": "Identifier of the saving agent (e.g. claude-code).",
					},
					"files": map[string]any{
						"type":        "array",
						"description": "Source file paths related to this memory.",
						"items":       map[string]any{"type": "string"},
					},
					"importance": map[string]any{
						"type":        "number",
						"description": "Initial importance score (0.0–1.0). Defaults to type-based value.",
						"minimum":     0.0,
						"maximum":     1.0,
					},
				},
			},
		},
		{
			Name:        "mem_search",
			Description: "Search persistent memory using full-text search.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"query"},
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Full-text search query string.",
					},
					"project": map[string]any{
						"type":        "string",
						"description": "Restrict results to this project slug.",
					},
					"scope": map[string]any{
						"type":        "string",
						"description": "Filter by scope: global, org, or project.",
						"enum":        []string{"global", "org", "project"},
					},
					"type": map[string]any{
						"type":        "string",
						"description": "Filter by memory type.",
						"enum": []string{
							"decision", "discovery", "bugfix", "pattern",
							"preference", "convention", "architecture", "config", "session_summary",
						},
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum number of results. Defaults to the configured default limit.",
						"minimum":     1,
						"maximum":     50,
					},
					"include_superseded": map[string]any{
						"type":        "boolean",
						"description": "Include memories superseded by newer versions. Defaults to false.",
					},
				},
			},
		},
		{
			Name:        "mem_get",
			Description: "Retrieve the full content of a memory by ID.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "UUIDv7 of the memory to retrieve.",
					},
				},
			},
		},
		{
			Name:        "mem_context",
			Description: "Get the most relevant memories for the current project context.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project": map[string]any{
						"type":        "string",
						"description": "Project slug. Defaults to the detected project.",
					},
					"budget": map[string]any{
						"type":        "integer",
						"description": "Maximum token budget for returned memories. Defaults to config value.",
						"minimum":     1,
					},
					"focus": map[string]any{
						"type":        "string",
						"description": "Optional topic or question that biases memory selection.",
					},
				},
			},
		},
		{
			Name:        "mem_update",
			Description: "Update an existing memory by ID.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "UUIDv7 of the memory to update.",
					},
					"title": map[string]any{
						"type":        "string",
						"description": "New title to replace the existing one.",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "New content to replace the existing body.",
					},
					"type": map[string]any{
						"type":        "string",
						"description": "New memory type.",
						"enum": []string{
							"decision", "discovery", "bugfix", "pattern",
							"preference", "convention", "architecture", "config", "session_summary",
						},
					},
					"importance": map[string]any{
						"type":        "number",
						"description": "New importance score (0.0–1.0).",
						"minimum":     0.0,
						"maximum":     1.0,
					},
					"confidence": map[string]any{
						"type":        "number",
						"description": "New confidence score (0.0–1.0).",
						"minimum":     0.0,
						"maximum":     1.0,
					},
					"files": map[string]any{
						"type":        "array",
						"description": "Replacement list of associated source file paths.",
						"items":       map[string]any{"type": "string"},
					},
				},
			},
		},
		{
			Name:        "mem_session_end",
			Description: "End the current session and save a summary.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"summary"},
				"properties": map[string]any{
					"summary": map[string]any{
						"type":        "string",
						"description": "Human-readable description of what was accomplished this session.",
					},
					"session_id": map[string]any{
						"type":        "string",
						"description": "Session ID to close. Generated when omitted.",
					},
					"project": map[string]any{
						"type":        "string",
						"description": "Project slug. Defaults to the detected project.",
					},
				},
			},
		},
		{
			Name:        "mem_suggest_topic_key",
			Description: "Suggest a topic_key for a new memory.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"title"},
				"properties": map[string]any{
					"title": map[string]any{
						"type":        "string",
						"description": "Title of the memory for which to suggest a topic key.",
					},
					"project": map[string]any{
						"type":        "string",
						"description": "Project slug used to search for existing similar keys.",
					},
				},
			},
		},
		{
			Name:        "mem_relate",
			Description: "Create or update a relationship between two entities in the knowledge graph.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"source", "target", "relation"},
				"properties": map[string]any{
					"source": map[string]any{
						"type":        "string",
						"description": "Name of the source entity.",
					},
					"target": map[string]any{
						"type":        "string",
						"description": "Name of the target entity.",
					},
					"relation": map[string]any{
						"type":        "string",
						"description": "Type of relationship between the two entities.",
						"enum": []string{
							"depends_on", "implements", "supersedes",
							"related_to", "part_of", "uses", "conflicts_with",
						},
					},
					"source_kind": map[string]any{
						"type":        "string",
						"description": "Entity kind for the source when it needs to be created. Defaults to concept.",
						"enum": []string{
							"module", "service", "library", "concept", "person", "pattern", "file",
						},
					},
					"target_kind": map[string]any{
						"type":        "string",
						"description": "Entity kind for the target when it needs to be created. Defaults to concept.",
						"enum": []string{
							"module", "service", "library", "concept", "person", "pattern", "file",
						},
					},
					"project": map[string]any{
						"type":        "string",
						"description": "Project slug. Defaults to the detected project when omitted.",
					},
				},
			},
		},
		{
			Name:        "mem_timeline",
			Description: "Get memories around a specific point in time, ordered chronologically.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"around"},
				"properties": map[string]any{
					"around": map[string]any{
						"type":        "string",
						"description": "A memory UUID or ISO 8601 timestamp to use as the centre of the timeline window.",
					},
					"project": map[string]any{
						"type":        "string",
						"description": "Project slug. Defaults to the detected project when omitted.",
					},
					"window": map[string]any{
						"type":        "string",
						"description": "Time range to search (e.g. '7d', '24h', '30d'). Defaults to '7d'.",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum number of results. Defaults to 20.",
						"minimum":     1,
						"maximum":     100,
					},
				},
			},
		},
		{
			Name:        "mem_stats",
			Description: "Return aggregate statistics about the project memory store.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project": map[string]any{
						"type":        "string",
						"description": "Project slug. Defaults to the detected project. Pass empty string for global stats.",
					},
				},
			},
		},
		{
			Name:        "mem_checkpoint",
			Description: "Save a checkpoint of the current work state. Call periodically during long tasks to prevent knowledge loss on context compaction. Overwrites the previous checkpoint automatically.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"summary"},
				"properties": map[string]any{
					"summary": map[string]any{
						"type":        "string",
						"description": "Brief summary of current work state and progress.",
					},
					"decisions": map[string]any{
						"type":        "string",
						"description": "Decisions made since last checkpoint or session start.",
					},
					"next_steps": map[string]any{
						"type":        "string",
						"description": "What needs to happen next if the context is lost.",
					},
					"project": map[string]any{
						"type":        "string",
						"description": "Project slug. Defaults to the detected project.",
					},
				},
			},
		},
		{
			Name:        "mem_forget",
			Description: "Mark a memory for accelerated decay. Sets its decay rate to 1.0 so importance drops to near zero on the next scoring pass.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "UUIDv7 of the memory to forget.",
					},
					"reason": map[string]any{
						"type":        "string",
						"description": "Optional reason why the memory should be forgotten.",
					},
				},
			},
		},

		// --- BACKLOG TOOLS ---

		{
			Name:        "backlog_add",
			Description: "Add a new item to the project backlog.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"title"},
				"properties": map[string]any{
					"title": map[string]any{
						"type":        "string",
						"description": "Short description of the idea.",
					},
					"description": map[string]any{
						"type":        "string",
						"description": "Detailed explanation of the idea.",
					},
					"priority": map[string]any{
						"type":        "string",
						"description": "Priority level. Defaults to medium.",
						"enum":        []string{"critical", "high", "medium", "low"},
					},
					"project": map[string]any{
						"type":        "string",
						"description": "Project slug. Defaults to detected project.",
					},
				},
			},
		},
		{
			Name:        "backlog_list",
			Description: "List backlog items for the current project.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"status": map[string]any{
						"type":        "string",
						"description": "Filter by status.",
						"enum":        []string{"raw", "refined", "promoted", "archived"},
					},
					"project": map[string]any{
						"type":        "string",
						"description": "Project slug. Defaults to detected project.",
					},
				},
			},
		},
		{
			Name:        "backlog_refine",
			Description: "Refine a raw backlog item with additional details.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"id", "refinement"},
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Backlog item ID (e.g. BL-001).",
					},
					"refinement": map[string]any{
						"type":        "string",
						"description": "Refinement content to add to the item.",
					},
				},
			},
		},
		{
			Name:        "backlog_promote",
			Description: "Promote a refined backlog item to a spec. The item must have status 'refined'.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Backlog item ID to promote (e.g. BL-001).",
					},
				},
			},
		},

		// --- SPEC TOOLS ---

		{
			Name:        "spec_new",
			Description: "Create a new spec in draft status.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"title"},
				"properties": map[string]any{
					"title": map[string]any{
						"type":        "string",
						"description": "Title of the spec.",
					},
					"backlog_id": map[string]any{
						"type":        "string",
						"description": "Originating backlog item ID, if any.",
					},
					"project": map[string]any{
						"type":        "string",
						"description": "Project slug. Defaults to detected project.",
					},
				},
			},
		},
		{
			Name:        "spec_status",
			Description: "Get the full status of a spec including history and pushbacks.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Spec ID (e.g. SPEC-001).",
					},
				},
			},
		},
		{
			Name:        "spec_advance",
			Description: "Advance a spec to its next lifecycle state.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"id", "by"},
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Spec ID to advance.",
					},
					"by": map[string]any{
						"type":        "string",
						"description": "Who triggers the advance (e.g. orchestrator, architect, backend).",
					},
					"reason": map[string]any{
						"type":        "string",
						"description": "Optional reason for the transition.",
					},
				},
			},
		},
		{
			Name:        "spec_pushback",
			Description: "Register a pushback from an agent, transitioning the spec to needs_grill.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"id", "from_agent", "questions"},
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Spec ID to push back on.",
					},
					"from_agent": map[string]any{
						"type":        "string",
						"description": "Agent raising the pushback (e.g. architect, backend, qa).",
					},
					"questions": map[string]any{
						"type":        "array",
						"description": "Questions blocking progress.",
						"items":       map[string]any{"type": "string"},
						"minItems":    1,
					},
				},
			},
		},
		{
			Name:        "spec_resolve",
			Description: "Resolve the latest pushback on a spec, returning it to speccing.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"id", "resolution"},
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Spec ID whose pushback to resolve.",
					},
					"resolution": map[string]any{
						"type":        "string",
						"description": "Answer to the pushback questions.",
					},
				},
			},
		},
		{
			Name:        "spec_list",
			Description: "List specs for the current project.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"status": map[string]any{
						"type":        "string",
						"description": "Filter by status.",
						"enum": []string{
							"draft", "speccing", "needs_grill", "specced",
							"planning", "planned", "implementing", "qa", "done",
						},
					},
					"project": map[string]any{
						"type":        "string",
						"description": "Project slug. Defaults to detected project.",
					},
				},
			},
		},
	}
}

// agentTools returns the subset of tools intended for the "agent" tools mode.
// In Phase 1 this is identical to allTools. Admin-only tools (stats, forget, etc.)
// will be excluded here in future phases.
func agentTools() []ToolDefinition {
	return allTools()
}
