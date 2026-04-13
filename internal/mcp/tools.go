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
	}
}

// agentTools returns the subset of tools intended for the "agent" tools mode.
// In Phase 1 this is identical to allTools. Admin-only tools (stats, forget, etc.)
// will be excluded here in future phases.
func agentTools() []ToolDefinition {
	return allTools()
}
