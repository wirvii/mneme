package mcp

import "github.com/wirvii/mneme/internal/model"

// allTools returns the full list of ToolDefinitions exposed by the mneme MCP
// server. Each tool maps directly to a method on MemoryService. Schemas are
// defined inline as map[string]any following the JSON Schema draft-07 subset
// understood by MCP clients.
func allTools() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "speech_emit",
			Description: "Resolve the current spoken-response turn. Emit only a concise semantic result, decision, useful explanation, question, or blocker; use skip when speech adds no value. Speech is local and opt-in.",
			InputSchema: map[string]any{
				"type": "object", "required": []string{"disposition", "session_id"},
				"properties": map[string]any{
					"disposition": map[string]any{"type": "string", "enum": []string{"emit", "skip"}},
					"mode":        map[string]any{"type": "string", "enum": []string{"brief", "full"}},
					"text":        map[string]any{"type": "string", "description": "Useful spoken text; required for emit and omitted for skip."},
					"language":    map[string]any{"type": "string", "description": "Detected BCP-47 language or short locale such as es or en."},
					"session_id":  map[string]any{"type": "string", "description": "Opaque session id supplied by the speech hook."},
				},
			},
		},
		{
			Name: "speech_control", Description: "Enable, disable, stop, inspect, or change the mode of mneme's entirely local spoken-response channel.",
			InputSchema: map[string]any{"type": "object", "required": []string{"action"}, "properties": map[string]any{
				"action":          map[string]any{"type": "string", "enum": []string{"on", "off", "stop", "status", "voices", "setup", "set_mode", "set_voice"}},
				"mode":            map[string]any{"type": "string", "enum": []string{"brief", "full"}},
				"model":           map[string]any{"type": "string", "description": "Existing local Piper model path; required for setup."},
				"sha256":          map[string]any{"type": "string", "description": "Expected SHA-256 digest; required for setup."},
				"language":        map[string]any{"type": "string", "description": "BCP-47 language preference, such as es or es-MX."},
				"engine":          map[string]any{"type": "string", "enum": []string{"auto", "system", "say", "sapi", "piper"}},
				"voice":           map[string]any{"type": "string"},
				"fallback_engine": map[string]any{"type": "string", "enum": []string{"auto", "system", "say", "sapi", "piper"}},
				"fallback_voice":  map[string]any{"type": "string"},
			}},
		},
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
						"description": "Memory type. Defaults to discovery. Use 'rule' to create a binding constraint with applies_to and severity.",
						"enum": []string{
							"decision", "discovery", "bugfix", "pattern",
							"preference", "convention", "architecture", "config",
							"session_summary", "rule", "synthesis",
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
						"description": "Agent session ID to associate this memory with. The SessionStart hook's mneme:session block announces the current session's id — pass it here so mem_session_end can later report real memories_created/session_duration for this session.",
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
					"applies_to": map[string]any{
						"type":        "array",
						"description": "Patterns this rule applies to. Required when type is 'rule'. Supports path globs (internal/**/*.go), tool selectors (tool:Edit), combined (tool:Edit+internal/**), negations (!docs/**), and global wildcard (**).",
						"items":       map[string]any{"type": "string"},
					},
					"severity": map[string]any{
						"type":        "string",
						"description": "Enforcement level for rules. Defaults to 'warn' when type is 'rule'. Ignored for non-rule types.",
						"enum":        []string{"info", "warn", "block"},
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
							"preference", "convention", "architecture", "config",
							"session_summary", "rule", "synthesis",
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
					"include_graph": map[string]any{
						"type":        "boolean",
						"description": "Enable 1-hop graph expansion. Augments BM25+vector results with topologically related memories from the knowledge graph. Defaults to true (use false to disable for this request only).",
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
					"include_graph": map[string]any{
						"type":        "boolean",
						"description": "Enable graph expansion for focus matching. When true and focus is provided, topologically related memories receive the same +0.3 boost as text-matched focus results. Uses PPR or 1-hop depending on config.graph_mode. Default: true (config default).",
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
					"applies_to": map[string]any{
						"type":        "array",
						"description": "Replaces the applies_to pattern list. Only valid when the memory is of type 'rule'.",
						"items":       map[string]any{"type": "string"},
					},
					"severity": map[string]any{
						"type":        "string",
						"description": "Replaces the enforcement level. Only valid when the memory is of type 'rule'.",
						"enum":        []string{"info", "warn", "block"},
					},
				},
			},
		},
		{
			Name:        "mem_session_end",
			Description: "End the current session and save a summary. Pass session_id (the one announced by the SessionStart hook's mneme:session block) to get real memories_created/session_duration back — omit it and both fields are absent from the response, since mneme cannot attribute prior work to an id it just generated.",
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
						"description": "Session ID to close. Generated when omitted — but generating one here forfeits memories_created/session_duration in the response. The SessionStart hook's mneme:session block announces the current session's id; use it here and in mem_save.",
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
			Description: "Suggest a topic_key for a new memory. Searches existing topic keys and unresolved knowledge gaps to find the best match. Returns scored suggestions from both existing memories (Jaccard similarity) and gap analysis (Jaccard + urgency boost). Gap matches signal that the project already needs this key.",
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
						"description": "Project slug used to search for existing similar keys and gaps.",
					},
				},
			},
		},
		{
			Name:        "mem_relate",
			Description: "Create or update a relationship between two graph endpoints. Each endpoint is a string resolved in this order: (1) memory UUID full or 8+ hex prefix, (2) memory topic_key (when *_kind is omitted or 'concept'), (3) entity name. When resolution lands on a memory, the memory is auto-linked to its proxy entity so the relation is reachable from mem_explore. Pass an explicit non-default *_kind (e.g. 'service', 'library') to force entity-only semantics.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"source", "target", "relation"},
				"properties": map[string]any{
					"source": map[string]any{
						"type":        "string",
						"description": "Source endpoint: memory UUID, UUID prefix (8+ hex), topic_key, or entity name.",
					},
					"target": map[string]any{
						"type":        "string",
						"description": "Target endpoint: memory UUID, UUID prefix (8+ hex), topic_key, or entity name.",
					},
					"relation": map[string]any{
						"type":        "string",
						"description": "Type of relationship between the two entities.",
						"enum": []string{
							"depends_on", "implements", "supersedes",
							"related_to", "part_of", "uses", "conflicts_with",
							"references",
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
					"weight": map[string]any{
						"type":        "number",
						"description": "Override the default weight for this relation type. Must be between 0.0 and 1.0. When omitted, a type-specific default is used (e.g. depends_on=0.9, related_to=0.5).",
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
		{
			Name:        "mem_promote",
			Description: "Mark a memory as team-curated (shared=2) and persist it in the database. Materializes it to the shared git vault immediately when team-memory is active. Idempotent.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "UUIDv7 of the memory to promote.",
					},
				},
			},
		},

		// --- BACKLOG TOOLS ---

		{
			Name:        "backlog_add",
			Description: "Add a new item to the project backlog. lane is required; scope is required when lane=trivial.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"title", "lane"},
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
					"lane": map[string]any{
						"type":        "string",
						"description": "SDD workflow lane. trivial: ≤3 files, ≤20 lines, no public API change, no SQL/cmd. standard: everything else.",
						"enum":        []string{"trivial", "standard"},
					},
					"scope": map[string]any{
						"type":        "string",
						"description": "Glob pattern for files this item may touch (e.g. internal/store/**). Required when lane=trivial.",
					},
				},
			},
		},
		{
			Name: "backlog_list",
			Description: "List backlog items for the current project. Descriptions are returned as a " +
				"200-character `excerpt` with a `truncated` flag — call `backlog_get` for the full " +
				"description. `total` is the number of matches before `limit` was applied. Items beyond " +
				"`limit` (max 50) are not reachable by listing: narrow with `status`, or fetch by ID with " +
				"`backlog_get`. `refinements` is how many refinements each item has (an item accepts N — " +
				"SPEC-110). An empty `excerpt` with `refinements` > 0 does NOT mean an empty item — the " +
				"detail lives in the refinements: call `backlog_get`.",
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
					"limit": map[string]any{
						"type":        "integer",
						"description": "Max items returned (default 20, max 50). `total` always reports the true number of matches before the limit.",
						"minimum":     1,
						"maximum":     model.ListMaxLimit,
					},
				},
			},
		},
		{
			Name: "backlog_get",
			Description: "Get one backlog item by ID with its FULL description, plus ALL of its " +
				"refinements — no excerpt, no limit. This is the only way to read a grill ledger " +
				"over MCP: spec_status does not include the backlog item and the specs table has " +
				"no description column. Returns {item, refinements}.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Backlog item ID (e.g. BL-001).",
					},
				},
			},
		},
		{
			Name: "backlog_refine",
			Description: "Append a refinement to a raw or refined backlog item. An item accepts N " +
				"refinements: each one is stored as its own row and the item's description never grows.",
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
					"by": map[string]any{
						"type":        "string",
						"description": "Who appends the refinement (e.g. orchestrator, architect). Optional.",
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
			Description: "Create a new spec in draft status. lane is required; scope is required when lane=trivial.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"title", "lane"},
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
					"lane": map[string]any{
						"type":        "string",
						"description": "SDD workflow lane. trivial: ≤3 files, ≤20 lines, no public API change, no SQL/cmd. standard: everything else.",
						"enum":        []string{"trivial", "standard"},
					},
					"scope": map[string]any{
						"type":        "string",
						"description": "Glob pattern for files this spec may touch. Required when lane=trivial.",
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
			Description: "Advance a spec to its next lifecycle state. Returns {spec, executor}: executor is an advisory ExecutorResolution for the stage just entered — delegate to a manifest subagent when executor.delegate is true, or supply the stage yourself as a conscious fallback when executor.degraded is true (SPEC-068).",
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
			Description: "Resolve the latest pushback on a spec, returning it to speccing (standard) or rationale (trivial).",
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
			Name:        "spec_doc_write",
			Description: "Write a spec entregable (spec/plan/qa-report/changes) to its workflow directory. The destination directory and filename are never caller-supplied: the directory is derived from the persisted spec record and the filename from a closed set of kinds.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"id", "kind", "content"},
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Spec ID (e.g. SPEC-001).",
					},
					"kind": map[string]any{
						"type":        "string",
						"description": "Which document to write.",
						"enum":        []string{"spec", "plan", "qa-report", "changes", "criteria", "budget"},
					},
					"content": map[string]any{
						"type":        "string",
						"description": "Full document content, written verbatim.",
					},
				},
			},
		},
		{
			Name: "spec_list",
			Description: "List specs for the current project. `total` is the number of matches before " +
				"`limit` was applied.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"status": map[string]any{
						"type":        "string",
						"description": "Filter by status.",
						"enum": []string{
							"draft", "speccing", "needs_grill", "specced",
							"planning", "planned", "implementing", "qa", "done",
							"rationale", "audit",
						},
					},
					"project": map[string]any{
						"type":        "string",
						"description": "Project slug. Defaults to detected project.",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Max specs returned (default 20, max 50). `total` always reports the true number of matches before the limit.",
						"minimum":     1,
						"maximum":     model.ListMaxLimit,
					},
				},
			},
		},
		// --- LANE TOOLS ---

		{
			Name:        "spec_quick",
			Description: "Advance a trivial-lane spec from draft to implementing in one step by recording a rationale. Rejected for standard-lane specs.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"id", "rationale", "by"},
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Spec ID (must be trivial lane, draft status).",
					},
					"rationale": map[string]any{
						"type":        "string",
						"description": "1–3 sentence justification for the trivial classification.",
					},
					"by": map[string]any{
						"type":        "string",
						"description": "Who triggers the advance (e.g. orchestrator).",
					},
				},
			},
		},
		{
			Name:        "lane_audit",
			Description: "Run the deterministic post-implementation auditor for a trivial-lane spec in audit status. Checks file count, line count, forbidden paths, scope, and public-symbol changes against the declared scope. On pass: advances to done. On fail: stays in audit, saves discovery memory.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Spec ID to audit (must be trivial lane, audit status).",
					},
					"base_ref": map[string]any{
						"type":        "string",
						"description": "Git ref to diff against. Defaults to merge-base with the default branch.",
					},
				},
			},
		},
		{
			Name:        "lane_reclassify",
			Description: "Reclassify a spec's lane from trivial to standard. Only trivial→standard is allowed. Moves the spec to speccing so the full SDD workflow can proceed.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"id", "lane", "by"},
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Spec ID to reclassify.",
					},
					"lane": map[string]any{
						"type":        "string",
						"description": "Target lane (only 'standard' is allowed).",
						"enum":        []string{"standard"},
					},
					"scope": map[string]any{
						"type":        "string",
						"description": "Updated scope glob (optional when moving to standard).",
					},
					"by": map[string]any{
						"type":        "string",
						"description": "Who triggers the reclassification.",
					},
				},
			},
		},
		{
			Name:        "lane_override",
			Description: "Override a failed lane audit and advance a trivial-lane spec from audit to done. Requires a documented reason. Persists a discovery memory.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"id", "reason", "by"},
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Spec ID to override (must be trivial lane, audit status).",
					},
					"reason": map[string]any{
						"type":        "string",
						"description": "Justification for bypassing the audit (required, persisted as memory).",
					},
					"by": map[string]any{
						"type":        "string",
						"description": "Who triggers the override.",
					},
				},
			},
		},
		{
			Name:        "lane_status",
			Description: "Show the lane classification, scope, and latest audit summary for a spec.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Spec ID to inspect.",
					},
				},
			},
		},
		{
			Name:        "spec_reject",
			Description: "Reject a spec from qa (standard lane), audit (trivial lane), or done (either lane, SPEC-087 D6) back to implementing. Records the rejection reason in history. Distinct from spec_pushback which models ambiguity → needs_grill.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"id", "reason", "by"},
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Spec ID to reject.",
					},
					"reason": map[string]any{
						"type":        "string",
						"description": "Why the spec was rejected (required; persisted in history).",
					},
					"by": map[string]any{
						"type":        "string",
						"description": "Who triggers the rejection (e.g. qa-agent, orchestrator).",
					},
				},
			},
		},
		{
			Name:        "lane_stats",
			Description: "Return lane compliance statistics for the project: trivial spec count, audit-fail count and rate, override count, reclassify count.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project": map[string]any{
						"type":        "string",
						"description": "Project slug. Defaults to the detected project.",
					},
				},
			},
		},

		// Quality tools (SPEC-115 EPIC-calidad S1)
		{
			Name:        "quality_verify",
			Description: "Run the gates declared in .mneme/quality.toml for a spec and emit (or deny) a certificate bound to the current commit. Valid only while the spec is implementing or qa (qa admits recertification when HEAD moved during QA).",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Spec ID to verify (must be implementing or qa status).",
					},
				},
			},
		},
		{
			Name:        "quality_status",
			Description: "Report the quality constitution's state (path, hash, enabled, declared gates) and, when a spec ID is given, its latest certificate and checks. Never executes anything.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Spec ID to report the latest certificate for. Omit to report only the constitution's own state.",
					},
				},
			},
		},
		{
			Name:        "quality_ack",
			Description: "Record a human's justified approval of a quality finding, converting it from 'finding' to 'acked' without re-running anything. The certificate's verdict is recalculated in the same operation.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"cert_id", "seq", "by", "justification"},
				"properties": map[string]any{
					"cert_id": map[string]any{
						"type":        "string",
						"description": "Certificate ID the finding belongs to.",
					},
					"seq": map[string]any{
						"type":        "integer",
						"description": "Seq of the finding within the certificate's checks.",
					},
					"by": map[string]any{
						"type":        "string",
						"description": "Who is acknowledging the finding — never the author of the change under review.",
					},
					"justification": map[string]any{
						"type":        "string",
						"description": "Why the finding is acceptable (required, non-empty).",
					},
				},
			},
		},
		{
			Name:        "quality_sign",
			Description: "Record a qa-tester's attestation that a criterion row genuinely holds, converting it from 'finding' to 'acked'. Distinct from quality_ack (an absolution): a criterion is ATTESTED, never absolved. Only accepts rows whose kind starts with 'criterion'. Restricted to the qa-tester role for a subagent caller.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"cert_id", "seq", "by", "evidence"},
				"properties": map[string]any{
					"cert_id": map[string]any{
						"type":        "string",
						"description": "Certificate ID the criterion row belongs to.",
					},
					"seq": map[string]any{
						"type":        "integer",
						"description": "Seq of the criterion row within the certificate's checks.",
					},
					"by": map[string]any{
						"type":        "string",
						"description": "Who is signing — the qa-tester.",
					},
					"evidence": map[string]any{
						"type":        "string",
						"description": "What was verified and how (required, non-empty).",
					},
				},
			},
		},
		{
			Name:        "quality_report",
			Description: "Generate the QA report from the spec's latest certificate and write it via spec_doc_write's qa-report kind. Renders from the certificate's persisted rows, never from criteria.toml — an edit to that document after certification cannot change what the report says.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Spec ID to generate the report for.",
					},
					"force": map[string]any{
						"type":        "boolean",
						"description": "Overwrite an existing qa-report.md even if it lacks mneme's generation marker.",
					},
				},
			},
		},

		// mem_gaps
		{
			Name:        "mem_gaps",
			Description: "List knowledge gaps — unresolved [[wikilink]] references. Shows topic_keys that are mentioned in memories but don't have a corresponding memory yet. Use this to discover what knowledge is missing and decide what to create next.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scope": map[string]any{
						"type":        "string",
						"description": "Query scope: project (default), global, or all.",
						"enum":        []string{"project", "global", "all"},
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum number of gaps to return. Default: 20, max: 100.",
						"minimum":     1,
						"maximum":     100,
					},
					"min_mentions": map[string]any{
						"type":        "integer",
						"description": "Minimum total_mentions to include a gap. Default: 1.",
						"minimum":     1,
					},
					"include_samples": map[string]any{
						"type":        "boolean",
						"description": "Include sample source memories for each gap. Default: true. Set false for faster aggregate-only output.",
					},
					"project": map[string]any{
						"type":        "string",
						"description": "Project slug. Defaults to the detected project.",
					},
				},
			},
		},
		// mem_explore
		{
			Name:        "mem_explore",
			Description: "Explore the knowledge graph starting from a seed memory. Performs a prioritised BFS traversal following strong relations, returning connected memories with their distance and accumulated path weight.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"seed"},
				"properties": map[string]any{
					"seed": map[string]any{
						"type":        "string",
						"description": "Starting memory: full UUID, short UUID prefix (8+ hex chars), or topic_key (e.g. 'architecture/auth-model').",
					},
					"depth": map[string]any{
						"type":        "integer",
						"description": "Maximum hops from seed. Default: 2. Range: 0-5.",
						"minimum":     0,
						"maximum":     5,
					},
					"budget": map[string]any{
						"type":        "integer",
						"description": "Maximum token budget for returned memories. Default: 4000.",
						"minimum":     1,
					},
					"threshold": map[string]any{
						"type":        "number",
						"description": "Minimum relation weight to follow. Default: 0.3. Range: 0.0-1.0.",
						"minimum":     0.0,
						"maximum":     1.0,
					},
					"project": map[string]any{
						"type":        "string",
						"description": "Project slug. Defaults to the detected project.",
					},
				},
			},
		},

		// --- CODEGRAPH TOOLS ---

		{
			Name:        "codegraph_search",
			Description: "Search code symbols by name using full-text search. Returns functions, types, methods matching the query.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"query"},
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Search query for symbol names.",
					},
					"kind": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Filter by node kind (function, struct, interface, method, etc).",
					},
					"language": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Filter by language (go, typescript, javascript).",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Max results (default 20, max 50).",
					},
				},
			},
		},
		{
			Name:        "codegraph_context",
			Description: "Get the full context of a code symbol: definition, callers, callees, and containing file.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"symbol"},
				"properties": map[string]any{
					"symbol": map[string]any{
						"type":        "string",
						"description": "Symbol name (short name, qualified name, or partial match).",
					},
					"depth": map[string]any{
						"type":        "integer",
						"description": "How many hops of callers/callees to include. Default: 1.",
					},
				},
			},
		},
		{
			Name:        "codegraph_callers",
			Description: "Find all functions/methods that call a given symbol.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"symbol"},
				"properties": map[string]any{
					"symbol": map[string]any{
						"type":        "string",
						"description": "Symbol name to find callers of.",
					},
					"depth": map[string]any{
						"type":        "integer",
						"description": "Traversal depth (default 1, max 5).",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Max results (default 20).",
					},
				},
			},
		},
		{
			Name:        "codegraph_callees",
			Description: "Find all functions/methods that a given symbol calls.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"symbol"},
				"properties": map[string]any{
					"symbol": map[string]any{
						"type":        "string",
						"description": "Symbol name to find callees of.",
					},
					"depth": map[string]any{
						"type":        "integer",
						"description": "Traversal depth (default 1, max 5).",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Max results (default 20).",
					},
				},
			},
		},
		{
			Name:        "codegraph_impact",
			Description: "Analyze the impact radius of changing a symbol — what transitively depends on it.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"symbol"},
				"properties": map[string]any{
					"symbol": map[string]any{
						"type":        "string",
						"description": "Symbol to analyze impact for.",
					},
					"depth": map[string]any{
						"type":        "integer",
						"description": "Traversal depth (default 3, max 10).",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Max results (default 50).",
					},
				},
			},
		},
		{
			Name:        "codegraph_node",
			Description: "Get detailed information about a specific code symbol including its source code.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"symbol"},
				"properties": map[string]any{
					"symbol": map[string]any{
						"type":        "string",
						"description": "Symbol name to look up.",
					},
				},
			},
		},
		{
			Name:        "codegraph_explore",
			Description: "Explore multiple symbols at once: get their source code and relationships.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"symbols"},
				"properties": map[string]any{
					"symbols": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "List of symbol names to explore (max 10).",
					},
					"budget": map[string]any{
						"type":        "integer",
						"description": "Maximum output length in characters (default 30000).",
					},
				},
			},
		},
		{
			Name:        "codegraph_trace",
			Description: "Find the call path between two symbols.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"from", "to"},
				"properties": map[string]any{
					"from": map[string]any{
						"type":        "string",
						"description": "Source symbol name.",
					},
					"to": map[string]any{
						"type":        "string",
						"description": "Target symbol name.",
					},
					"max_depth": map[string]any{
						"type":        "integer",
						"description": "Maximum path length (default 5).",
					},
				},
			},
		},
		{
			Name:        "codegraph_status",
			Description: "Show the status of the code graph index: counts, languages, last update.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "codegraph_files",
			Description: "List indexed files, optionally filtered by language or glob pattern.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{
						"type":        "string",
						"description": "Glob pattern to filter file paths.",
					},
					"language": map[string]any{
						"type":        "string",
						"description": "Filter by language (go, typescript, javascript).",
					},
				},
			},
		},

		// --- SKILLS TOOLS ---

		{
			Name:        "skills_list",
			Description: "List all available skills (bundled and installed), showing name, version, installed status, pinned status, and lint result.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "skills_install",
			Description: "Install a bundled skill to ~/.claude/skills/. Respects pin protection unless force is true.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"name"},
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Skill name to install (must be in the bundled set).",
					},
					"force": map[string]any{
						"type":        "boolean",
						"description": "When true, overwrite even if the installed skill is pinned. Default: false.",
					},
				},
			},
		},
		{
			Name:        "skills_pin",
			Description: "Set pinned:true in the installed SKILL.md to protect it from overwrite or removal.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"name"},
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Skill name to pin.",
					},
				},
			},
		},
		{
			Name:        "skills_unpin",
			Description: "Set pinned:false in the installed SKILL.md, allowing future installs to overwrite it.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"name"},
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Skill name to unpin.",
					},
				},
			},
		},
		{
			Name:        "skills_remove",
			Description: "Remove an installed skill directory. Refuses if the skill is pinned unless force is true.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"name"},
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Skill name to remove.",
					},
					"force": map[string]any{
						"type":        "boolean",
						"description": "When true, remove even if the skill is pinned. Default: false.",
					},
				},
			},
		},
		{
			Name: "skills_lint",
			Description: "Run the deterministic structural linter on a skill or all installed skills. " +
				"Returns a list of LintResult objects. On lint error, result is returned with IsError:true " +
				"(not a protocol error) so the caller receives the full finding list.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Skill name to lint. When omitted, all installed skills are linted.",
					},
				},
			},
		},
		{
			Name: "skills_validate",
			Description: "Run the validation/run.sh script for a skill. " +
				"Returns a ValidateResult. On failure (non-zero exit or missing script), " +
				"result is returned with IsError:true so the caller receives the full output.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"name"},
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Skill name to validate.",
					},
				},
			},
		},

		// --- MODEL TOOLS (SPEC-038) ---

		{
			Name:        "model_list",
			Description: "List the effective model for each bundled agent, showing origin (default or override).",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "model_set",
			Description: "Set the model alias for a specific agent. Writes an override to config.toml. Validates that the agent is a bundled agent (error if not). Accepts any non-empty string as the model alias; warns (does not error) when the alias is not in the known-aliases list (opus/sonnet/haiku/inherit). Returns a hint to run `mneme install claude-code` to apply.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"agent", "model"},
				"properties": map[string]any{
					"agent": map[string]any{
						"type":        "string",
						"description": "Agent name (e.g. bug-hunter, architect).",
					},
					"model": map[string]any{
						"type":        "string",
						"description": "Model alias to assign (e.g. opus, sonnet, haiku). Must not be empty.",
					},
				},
			},
		},
		{
			Name:        "model_reset",
			Description: "Remove the model override for a specific agent, or for all agents when agent is omitted. Restores default models. Returns a hint to run `mneme install claude-code` to apply.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent": map[string]any{
						"type":        "string",
						"description": "Agent name to reset. Omit to reset all agents.",
					},
				},
			},
		},

		// --- CONFLICTS TOOLS (SPEC-039) ---

		{
			Name:        "conflicts_candidates",
			Description: "Find candidate memories that may conflict with the given memory using deterministic FTS5 term matching. No LLM involved. Use conflicts_scan to judge candidates.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Memory ID to find conflict candidates for.",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum number of candidates to return (default 5).",
					},
				},
			},
		},
		{
			Name: "conflicts_scan",
			Description: "Scan memories for conflicts using the local Claude CLI as judge (subprocess, $0 cost). " +
				"Dry-run by default — set apply:true to persist judgments. " +
				"Returns ErrCLIUnavailable (IsError:true) when the Claude CLI is not installed.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project": map[string]any{
						"type":        "string",
						"description": "Project slug to scan. Defaults to the current project.",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum number of candidate pairs to judge (default 5, max 10).",
					},
					"apply": map[string]any{
						"type":        "boolean",
						"description": "When true, persist judged relations. Default: false (dry-run).",
					},
				},
			},
		},
		{
			Name:        "conflicts_link",
			Description: "Manually create a relation between two memories. Relation must be one of: supersedes, conflicts_with, unrelated. Manual links always win over CLI-judged links.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"from_id", "to_id", "relation"},
				"properties": map[string]any{
					"from_id": map[string]any{
						"type":        "string",
						"description": "Source memory ID.",
					},
					"to_id": map[string]any{
						"type":        "string",
						"description": "Target memory ID.",
					},
					"relation": map[string]any{
						"type":        "string",
						"description": "Relation type: supersedes (from supersedes to), conflicts_with, or unrelated.",
						"enum":        []string{"supersedes", "conflicts_with", "unrelated"},
					},
					"rationale": map[string]any{
						"type":        "string",
						"description": "Optional one-line explanation for the relation.",
					},
				},
			},
		},
		{
			Name:        "conflicts_unlink",
			Description: "Remove a memory relation between two memories (in either direction). Also clears superseded_by when applicable.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"from_id", "to_id"},
				"properties": map[string]any{
					"from_id": map[string]any{
						"type":        "string",
						"description": "First memory ID of the pair.",
					},
					"to_id": map[string]any{
						"type":        "string",
						"description": "Second memory ID of the pair.",
					},
				},
			},
		},
		{
			Name: "conflicts_list",
			Description: "List all memory conflict relations (conflicts_with and unrelated edges) for the given " +
				"project. `count` is the number of relations returned; `total` is the number of matches before " +
				"`limit` was applied.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project": map[string]any{
						"type":        "string",
						"description": "Project slug to filter results. Defaults to the current project.",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Max relations returned (default 20, max 50). `total` always reports the true number of matches before the limit.",
						"minimum":     1,
						"maximum":     model.ListMaxLimit,
					},
				},
			},
		},
		// --- PROFILE TOOLS (SPEC-091 §1) ---

		{
			Name:        "profile_new",
			Description: "Scaffold a brand-new profile REPOSITORY (structure §3 of docs/profiles-design.md + mneme-profile.toml + git init) — the source a profile author curates, commits, and pushes BEFORE any consumer ever runs profile_add against it. Never touches the host-level store (~/.mneme/profiles/). Errors if the destination directory exists and is not empty, or if name is not a safe-slug.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"name"},
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "The new profile's name (safe-slug ^[a-z0-9][a-z0-9-]*$); becomes the manifest's name and, when dir is omitted, the destination subdirectory.",
					},
					"dir": map[string]any{
						"type":        "string",
						"description": "Destination directory. Defaults to <cwd>/<name>.",
					},
				},
			},
		},
		{
			Name:        "profile_add",
			Description: "Clone a profile's git repository into the host-level store (~/.mneme/profiles/<name>/), shared by every project on this host. The name is derived from the profile's mneme-profile.toml manifest unless name is passed explicitly (in which case it must match).",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"source"},
				"properties": map[string]any{
					"source": map[string]any{
						"type":        "string",
						"description": "Git URL to clone the profile from.",
					},
					"name": map[string]any{
						"type":        "string",
						"description": "Override the profile name. Must match the manifest's declared name.",
					},
					"ref": map[string]any{
						"type":        "string",
						"description": "Tag/branch/commit to check out after cloning.",
					},
					"force": map[string]any{
						"type":        "boolean",
						"description": "Overwrite an existing installation. Default: false.",
					},
				},
			},
		},
		{
			Name:        "profile_update",
			Description: "Fetch and check out the latest state of an installed profile. When name is omitted, the current repository's pin (.mneme-profile) is resolved and its name (and, absent ref, its ref) is used instead.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Profile name in the host-level store. Defaults to the current repo's pinned profile.",
					},
					"ref": map[string]any{
						"type":        "string",
						"description": "Tag/branch/commit to check out. Defaults to the current repo's pinned ref, or a fast-forward pull of the current branch.",
					},
				},
			},
		},
		{
			Name:        "profile_list",
			Description: "List profiles installed in the host-level store (~/.mneme/profiles/): name, version, current ref, and path. Directories with an invalid or missing manifest are reported as invalid rather than omitted.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "profile_status",
			Description: "Report the pin resolution (.mneme-profile) for the current (or given) repository: installed, missing (needs profile_add), default (mneme's internal profile), or absent (no pin at all). Read-only — never writes anything.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project_root": map[string]any{
						"type":        "string",
						"description": "Absolute path to the repository root. Defaults to current working directory.",
					},
				},
			},
		},
		{
			Name:        "profile_use",
			Description: "Activate an already-installed profile for the current (or given) repository, immediately: reconstructs a self-describing pin from the profile's checkout in the host-level store (name + origin remote + exact tag/commit), writes it to .mneme-profile, and materializes it right away (agents/skills/blocks/rules). Never clones — the profile must already be installed via profile_add. Preserves a preexisting 'scaffold' field on the pin.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"name"},
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Profile name in the host-level store (must already be installed).",
					},
					"project_root": map[string]any{
						"type":        "string",
						"description": "Absolute path to the repository root. Defaults to current working directory.",
					},
				},
			},
		},
		{
			Name:        "profile_default",
			Description: "Set, clear, or (with neither name nor clear) print the HOST-level default profile (~/.mneme/config.toml's [profiles].default): the profile a session activates at SessionStart when the repository has NO .mneme-profile pin. Never materializes anything and never re-points sessions already running — use profile_use to activate a profile in the current repo now.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Profile name to set as the host-level default (must already be installed).",
					},
					"clear": map[string]any{
						"type":        "boolean",
						"description": "When true, clears the default (reverts to vanilla). Takes precedence over name.",
					},
				},
			},
		},
		{
			Name:        "profile_deactivate",
			Description: "Compute the plan to undo THIS repo's active profile's materialization and, with apply:true, execute it: every displaced agent/skill is restored (or removed if it belonged to the profile), the managed block is removed from CLAUDE.md, every rule with this profile's provenance is purged (project-scoped and any orphaned global rows), and the activation lock is deleted. Never touches .mneme-profile (the pin) — a committed, team-shared file; if the pin or the host default still point at this profile, the NEXT SessionStart reactivates it, and the returned NextSession field says so before anything is applied. Without apply:true, returns the plan and mutates nothing.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project_root": map[string]any{
						"type":        "string",
						"description": "Absolute path to the repository root. Defaults to current working directory.",
					},
					"apply": map[string]any{
						"type":        "boolean",
						"description": "When true, executes the plan. Default false: returns the plan without mutating anything.",
					},
				},
			},
		},

		// --- PROJECT TOOLS (SPEC-098 §7a) ---
		{
			Name:        "project_new",
			Description: "Grow a brand-new project repository from a scaffold in the ACTIVE profile's catalog (pin > host default > vanilla). Copies the scaffold's skeleton with {{var}} substitution, runs `git init` (no commit, no remote), and writes the fresh repo's .mneme-profile pin with scaffold=<name> plus the active profile's identity — so the repo is born pointing at the profile that generated it. Does NOT commit, set a remote, or activate: the /new-project skill chains mneme-init over the fresh repo. Errors if the destination is not empty, the scaffold is not in the active profile's catalog, or (today) the layout is monorepo (arrives in a later mneme).",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"scaffold", "dir"},
				"properties": map[string]any{
					"scaffold": map[string]any{
						"type":        "string",
						"description": "The scaffold name to assemble (a scaffolds/<name>/ entry of the active profile).",
					},
					"dir": map[string]any{
						"type":        "string",
						"description": "Destination directory for the new project. Must be empty or absent.",
					},
					"vars": map[string]any{
						"type":                 "object",
						"description":          "Substitution variables as a flat string->string map, merged over the scaffold's declared [vars] defaults.",
						"additionalProperties": map[string]any{"type": "string"},
					},
					"project_root": map[string]any{
						"type":        "string",
						"description": "Directory from which the ACTIVE profile is resolved (NOT the destination). Defaults to the current working directory.",
					},
				},
			},
		},
		{
			Name:        "app_add",
			Description: "Add a composable app to an existing MONOREPO grown from the active profile's scaffold (SPEC-099 §7b). Reads the monorepo's pin to learn which scaffold generated it, then copies the named blueprint (a _blueprints/<name> archetype the scaffold offers) into the scaffold's apps directory under `name` with {{var}} substitution, and auto-wires it: the Turborepo built-in adapter updates pnpm-workspace.yaml (a no-op when a glob already covers apps/*, and it never touches turbo.json), or a custom toolchain applies its declared [wiring] actions (workspace:/json-merge:/copy:). Does NOT run git init, commit, or set a remote. Errors if the layout is single (no apps to add), the blueprint is not offered by the scaffold, or the target app directory is not empty.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"blueprint", "name"},
				"properties": map[string]any{
					"blueprint": map[string]any{
						"type":        "string",
						"description": "The blueprint archetype to instantiate (a _blueprints/<name> entry the monorepo's scaffold declares).",
					},
					"name": map[string]any{
						"type":        "string",
						"description": "Name the new app takes under the scaffold's apps directory (a safe-slug).",
					},
					"dir": map[string]any{
						"type":        "string",
						"description": "Monorepo root the app is added to. Defaults to the current working directory.",
					},
					"vars": map[string]any{
						"type":                 "object",
						"description":          "Substitution variables as a flat string->string map, merged over the scaffold's declared [vars] defaults.",
						"additionalProperties": map[string]any{"type": "string"},
					},
					"scaffold": map[string]any{
						"type":        "string",
						"description": "Override which scaffold archetype supplies the blueprint catalog (defaults to the monorepo pin's scaffold field).",
					},
				},
			},
		},
		// --- SCAFFOLD TOOL (SPEC-100 §7c) ---
		{
			Name:        "scaffold_capture",
			Description: "Capture an existing exemplar repository into a DRAFT scaffold within a profile repo the author is curating (SPEC-100 §7c — the deterministic half of the mneme-profile-author §15.6 authoring grill). Auto-detects the exemplar's structure (apps/, packages/, turbo.json, pnpm-workspace.yaml) to infer the scaffold's layout (single | monorepo) and toolchain (turborepo | custom), and reads go.mod / package.json for its identity. Writes scaffolds/<name>/scaffold.toml (a valid draft) plus the captured trees (shell/ + overlay/ for a monorepo, or a flat skeleton/ for a single layout, and each app under _blueprints/), rewriting the exemplar's project name and Go module path to {{PROJECT_NAME}} / {{MODULE_PATH}} placeholders. Never bootstraps, runs git, or activates — the author curates the draft (prune legacy, refine [vars]/[wiring]) and commits it. Errors if the exemplar holds nothing to capture, the name is not a safe-slug, the target scaffold already exists, or --into is not an existing directory.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"repo"},
				"properties": map[string]any{
					"repo": map[string]any{
						"type":        "string",
						"description": "Path to the exemplar repository to capture from.",
					},
					"name": map[string]any{
						"type":        "string",
						"description": "Scaffold catalog name (a safe-slug ^[a-z0-9][a-z0-9-]*$). Defaults to the exemplar's directory basename.",
					},
					"into": map[string]any{
						"type":        "string",
						"description": "Profile repository to write the scaffold into (its scaffolds/<name>/ and _blueprints/). Defaults to the current working directory.",
					},
				},
			},
		},
		{
			Name:        "init",
			Description: "Initialise a project with mneme managed blocks and report drift. Applies the global operating manual and a minimal repo block to CLAUDE.md files, then runs the drift detector. Pass check=true for report-only mode (no writes). Returns drift findings and a summary of blocks applied.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repo_root": map[string]any{
						"type":        "string",
						"description": "Absolute path to the repository root. Defaults to current working directory.",
					},
					"check": map[string]any{
						"type":        "boolean",
						"description": "When true, report-only mode: drift is reported but no managed blocks are written. Defaults to false.",
					},
				},
			},
		},

		// --- SUBAGENT TOOLS (SPEC-057 / EPIC agnostic-agents SS-4) ---
		{
			Name:        "subagent_fingerprint",
			Description: "Deterministic, read-only detection of a project's root, apps/packages, and stack markers, plus which subagents typed-memory records (project-profile/manifest) already exist. Phase 0 of the subagents grill.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repo_root": map[string]any{
						"type":        "string",
						"description": "Absolute path to start the project-root search from. Defaults to the current working directory.",
					},
				},
			},
		},
		{
			Name:        "subagent_profile_get",
			Description: "Read the project-profile (repo/org knowledge + app->role mapping) elicited by the subagents grill. Returns an empty profile when none has been saved yet.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project": map[string]any{
						"type":        "string",
						"description": "Project slug. Defaults to the detected project.",
					},
				},
			},
		},
		{
			Name:        "subagent_profile_save",
			Description: "Upsert the project-profile (repo/org knowledge + app->role mapping) elicited by the subagents grill.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"profile_json"},
				"properties": map[string]any{
					"project": map[string]any{
						"type":        "string",
						"description": "Project slug. Defaults to the detected project.",
					},
					"profile_json": map[string]any{
						"type":        "object",
						"description": "The project profile payload: {schema_version, repo:{commits,lang,layout,cross_rules[]}, org, mapping:[{app,role}]}.",
					},
				},
			},
		},
		{
			Name: "subagent_compose",
			Description: "Assemble a subagent profile preview: Go-authored frontmatter+permissions (selected via archetype) and layer-1 managed block, plus layer-2 (profile_json) and layer-3 (areas_layer3_md) content. " +
				"areas_layer3_md is treated as untrusted grill-provided data: it is wrapped and escaped against prompt injection before being embedded, since the composed file becomes the subagent's own system prompt. " +
				"Validates the result and returns it WITHOUT writing to disk.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"role", "archetype", "areas_layer3_md"},
				"properties": map[string]any{
					"role": map[string]any{
						"type":        "string",
						"description": "Subagent role name used for the frontmatter `name:` and destination filename (may differ from archetype for custom roles). Must match ^[a-z][a-z0-9-]*$.",
					},
					"archetype": map[string]any{
						"type":        "string",
						"description": "Built-in role whose Go-authored permission envelope and agent-fixed sections this profile inherits. An LLM never generates permissions directly — custom roles must map to one of these.",
						"enum":        []string{"architect", "backend", "frontend", "qa-tester", "bug-hunter", "diagnostician"},
					},
					"model": map[string]any{
						"type":        "string",
						"description": "Frontmatter `model:` alias (e.g. sonnet, opus). Defaults to sonnet.",
					},
					"description": map[string]any{
						"type":        "string",
						"description": "Frontmatter `description:` value. Defaults to a generic one-liner when omitted. Must not contain newlines.",
					},
					"areas_layer3_md": map[string]any{
						"type":        "string",
						"description": "Layer-3 (role x area x stack) markdown content drafted during the grill. Treated as untrusted data — wrapped and escaped before embedding.",
					},
					"profile_json": map[string]any{
						"type":        "object",
						"description": "The project profile (layer-2 repo/org knowledge) to render into the profile body.",
					},
				},
			},
		},
		{
			Name: "subagent_write",
			Description: "Atomically write a composed subagent profile to .claude/agents/<role>.md and update the manifest. Rolls back the file write if the manifest update fails. " +
				"role must be a safe slug (lowercase letters/digits/hyphens) — rejects path traversal. composed_md is re-validated against archetype's Go-authored permission envelope before anything is written, " +
				"so a hand-crafted composed_md can never grant a role more capability than its archetype allows.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"role", "archetype", "composed_md"},
				"properties": map[string]any{
					"role": map[string]any{
						"type":        "string",
						"description": "Subagent role name; determines the destination filename .claude/agents/<role>.md. Must match ^[a-z][a-z0-9-]*$.",
					},
					"archetype": map[string]any{
						"type":        "string",
						"description": "Built-in role whose Go-authored permission envelope composed_md is validated against before writing.",
						"enum":        []string{"architect", "backend", "frontend", "qa-tester", "bug-hunter", "diagnostician"},
					},
					"composed_md": map[string]any{
						"type":        "string",
						"description": "Full composed profile content, as returned by subagent_compose's preview.",
					},
					"enforcement_hook": map[string]any{
						"type":        "boolean",
						"description": "Whether the project's delegation-enforcement hook is enabled. Recorded in the manifest.",
					},
					"project": map[string]any{
						"type":        "string",
						"description": "Project slug. Defaults to the detected project.",
					},
					"repo_root": map[string]any{
						"type":        "string",
						"description": "Absolute repo root the profile is written under. Defaults to the current working directory.",
					},
					"engine": map[string]any{
						"type":        "string",
						"description": "GenerationEngine identifier used to draft layer-3 content (e.g. passthrough, cli-claude). Defaults to passthrough.",
					},
					"areas": map[string]any{
						"type":        "array",
						"description": "App/package paths this profile's role/area sections cover.",
						"items":       map[string]any{"type": "string"},
					},
					"areas_complete": map[string]any{
						"type":        "boolean",
						"description": "Certifies `areas` as an exhaustive list of every path this role may write to, which is what activates role containment (SPEC-086 D4/D5/D11). Set true ONLY as the direct answer to the mneme-init grill's explicit completeness question, reviewed by a human. NEVER infer it, never default it to true, and never backfill it for an existing role: an uncertified role is reported by `mneme subagents doctor` as `not_verified`, and that is the correct and safe state until a human certifies it. Omit it when unknown.",
					},
				},
			},
		},
		{
			Name:        "subagent_manifest_list",
			Description: "List the current subagent manifest (generated profile files and their metadata) for drift/status reporting.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project": map[string]any{
						"type":        "string",
						"description": "Project slug. Defaults to the detected project.",
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
