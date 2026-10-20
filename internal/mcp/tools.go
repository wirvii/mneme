package mcp

import "github.com/wirvii/mneme/internal/model"

// allTools returns the full list of ToolDefinitions exposed by the mneme MCP
// server. Each tool maps directly to a method on MemoryService. Schemas are
// defined inline as map[string]any following the JSON Schema draft-07 subset
// understood by MCP clients.
func allTools() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "mem_save",
			Description: "Save a structured observation to persistent memory. When topic_key is omitted the response carries suggested_topic_key (advisory; reuse it on the next save of this topic). The response may carry possible_conflicts (up to 3, deterministic FTS5). Judge each in the same turn with mem_update on the NEW memory: supersedes (the old one is replaced), conflicts_with (both stay, contradiction recorded) or unrelated_to (stop proposing it).",
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
			Description: "Search persistent memory using full-text search. Results past their review deadline carry review_due/review_note: verify them (mem_update confirm:true, a correction, or supersedes); never show these notes to the user.",
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
			Description: "Retrieve the full content of a memory by ID. The returned memory's sdd_refs field resolves every BL-<n>/SPEC-<n> mention against this machine's own database: local (found here), foreign (an anchor is recorded but nothing here carries it), or unanchored (SPEC-128).",
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
			Description: "Get the most relevant memories for the current project context. Results past their review deadline carry review_due/review_note: verify them (mem_update confirm:true, a correction, or supersedes); never show these notes to the user.",
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
			Description: "Update an existing memory by ID. Also records your judgement of possible_conflicts returned by mem_save: pass supersedes, conflicts_with or unrelated_to on the NEW memory. A request that carries only those lists does not count as a revision.",
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
					"supersedes": map[string]any{
						"type":        "array",
						"description": "Ids of older memories this one replaces: each is hidden from search (superseded_by = this id) but never deleted. Reversible with `mneme conflicts unlink`.",
						"items":       map[string]any{"type": "string"},
					},
					"conflicts_with": map[string]any{
						"type":        "array",
						"description": "Ids of memories this one contradicts. Both stay visible; the contradiction is recorded.",
						"items":       map[string]any{"type": "string"},
					},
					"unrelated_to": map[string]any{
						"type":        "array",
						"description": "Ids judged unrelated to this memory, so they stop being proposed as possible conflicts.",
						"items":       map[string]any{"type": "string"},
					},
					"confirm": map[string]any{
						"type":        "boolean",
						"description": "Set true when you verified the memory is still true: restarts its review clock without counting as an edit (use it on results flagged review_due).",
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
					"partial": map[string]any{
						"type":        "boolean",
						"description": "true saves a checkpoint (checkpoint/latest) without closing the session. Call it periodically during long tasks to survive context compaction.",
					},
					"decisions": map[string]any{
						"type":        "string",
						"description": "Only read when partial is true: decisions made since the last checkpoint or session start.",
					},
					"next_steps": map[string]any{
						"type":        "string",
						"description": "Only read when partial is true: what needs to happen next if the context is lost.",
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
		{
			Name: "backlog_archive",
			Description: "Archive a backlog item with a mandatory reason. Refused when the item is " +
				"already archived, or when its linked spec is already done. When the linked spec is " +
				"still alive the item IS archived and the spec is FROZEN: it stays readable " +
				"(spec_status/spec_list/spec_doc_write keep working) but its state can never change " +
				"again — no spec_* or lane_* verb will move it, and there is no unarchive: the agreed " +
				"way back is a NEW backlog item referencing the archived one, never resurrecting it. " +
				"Returns {item, frozen_spec}.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"id", "reason"},
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Backlog item ID to archive (e.g. BL-001).",
					},
					"reason": map[string]any{
						"type":        "string",
						"description": "Why the item is being discarded. Required and never blank.",
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
			Name: "spec_status",
			Description: "Get the full status of a spec including history and pushbacks. `frozen` is present " +
				"(with `state`, `backlog_id`, and `reason` when known) only when the spec's originating " +
				"backlog item was archived or is missing — either way the spec can no longer change status, " +
				"though it stays fully readable. The key is absent, not null, when the spec can still move. " +
				"The spec's `uuid` is its own permanent identity, distinct from `id` — never shown in " +
				"readable output, present here so a memory citing this spec can be resolved correctly " +
				"even on a machine whose own numbering differs (SPEC-128).",
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
			Description: "Advance a spec to its next lifecycle state. Returns {spec, executor, review_range}: executor is an advisory ExecutorResolution for the stage just entered — delegate to a manifest subagent when executor.delegate is true, or supply the stage yourself as a conscious fallback when executor.degraded is true (SPEC-068). review_range is present only when the stage just entered is qa: the tramo of commits mneme computed for this review pass — copy its notice verbatim into the qa-tester's brief instead of recomputing it (SPEC-157).",
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
			Description: "Write a spec entregable (spec/plan/qa-report/changes/criteria/task) to its workflow directory. The destination directory and filename are never caller-supplied: the directory is derived from the persisted spec record and the filename from a closed set of kinds. Kind task writes one section of the spec's single tarea.md and replaces only that section.",
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
						"enum":        []string{"spec", "plan", "qa-report", "changes", "criteria", "task"},
					},
					"section": map[string]any{
						"type":        "string",
						"description": "tarea.md section to replace. Required with kind task, forbidden with every other kind.",
						"enum":        []string{"que", "diseno", "plan", "entrega", "revision"},
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
				"`limit` was applied. `frozen` is an object keyed by spec ID naming which of the returned " +
				"specs can no longer change status (their originating backlog item was archived or is " +
				"missing); the key is absent from the response entirely when none is frozen.",
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
					"findings": map[string]any{
						"type":        "array",
						"description": "Declared-criterion findings this rejection is based on (SPEC-156). Required when the spec is standard lane, in qa status, and has a criteria.toml on file — in every other case it may be omitted; the service, not this schema, decides which case applies.",
						"items": map[string]any{
							"type":     "object",
							"required": []string{"criterion_id", "detail"},
							"properties": map[string]any{
								"criterion_id": map[string]any{
									"type":        "string",
									"description": "The id of the criteria.toml entry this finding claims is violated.",
								},
								"detail": map[string]any{
									"type":        "string",
									"description": "What fails about criterion_id. Must be non-empty.",
								},
								"evidence": map[string]any{
									"type":        "string",
									"description": "Optional supporting detail (e.g. a test name, a log excerpt).",
								},
							},
						},
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
	}
}

// agentTools returns the subset of tools intended for the "agent" tools mode.
// In Phase 1 this is identical to allTools. Admin-only tools (stats, forget, etc.)
// will be excluded here in future phases.
func agentTools() []ToolDefinition {
	return allTools()
}
