package install

import (
	"os"
	"path/filepath"
)

// mnemeInitMarkdown is the content of the /mneme-init Claude Code slash command.
// It instructs the agent to scan the current project and seed mneme with
// foundational knowledge extracted from well-known project files.
const mnemeInitMarkdown = `# /mneme-init — Initialize project memory

Scan the current project, seed mneme with foundational knowledge, and migrate
knowledge from CLAUDE.md files into persistent memory.

## Phase 1: Load existing memories

1. Call ` + "`mem_context`" + ` to check what mneme already knows about this project.
2. If memories exist, note what's already known to avoid duplicates.

## Phase 2: Scan project files and seed knowledge

Read these files if they exist and extract knowledge. Save each piece as a
SEPARATE memory (one fact per mem_save, not a dump):

| File | What to extract | Memory type | topic_key pattern |
|------|----------------|-------------|-------------------|
| CLAUDE.md | Project overview, what it does | architecture | architecture/overview |
| CLAUDE.md | Build/test/run commands | config | config/commands |
| CLAUDE.md | Commit conventions | convention | convention/commits |
| CLAUDE.md | Team rules and constraints | convention | convention/{rule-name} |
| package.json | Framework, key deps, scripts | architecture | architecture/tech-stack |
| go.mod | Go version, key modules | architecture | architecture/tech-stack |
| Cargo.toml | Rust edition, key crates | architecture | architecture/tech-stack |
| tsconfig.json | TypeScript config | config | config/typescript |
| docker-compose.yml | Services, infrastructure | architecture | architecture/infrastructure |
| .env.example | Required env vars | config | config/env-vars |
| Directory structure (2 levels) | Project layout | architecture | architecture/project-structure |

## Phase 3: Migrate CLAUDE.md knowledge to mneme

This is the critical step. For each CLAUDE.md file in the project (root and nested):

1. Read the entire file
2. Classify each section as either:
   - **Behavior instruction** — tells the agent HOW to behave (keep in CLAUDE.md)
     Examples: "Always respond in Spanish", "Never edit code directly", "Use conventional commits"
   - **Project knowledge** — facts ABOUT the project (migrate to mneme)
     Examples: "The API uses PostgreSQL", "Auth is handled by Firebase", "The monorepo has 3 apps"
3. For each **project knowledge** section:
   - Save to mneme with appropriate type and topic_key
   - The knowledge now lives in mneme and doesn't need to be in CLAUDE.md
4. DO NOT modify CLAUDE.md — just migrate the knowledge. The user can clean up CLAUDE.md later if they want.

## Phase 4: Detect and save cross-project patterns

If this project uses libraries, patterns, or tools you've seen in other projects
(check with mem_search scope:global):
- Save a relation: ` + "`mem_relate(source: \"this-project\", target: \"pattern-name\", relation: \"uses\")`" + `

## Phase 5: Report

After completing all phases, report to the user:
` + "```" + `
mneme initialized for {project}

Memories created: N
  - architecture: N
  - convention: N
  - config: N
  - discovery: N

Knowledge migrated from CLAUDE.md: N sections
Existing memories preserved: N (not duplicated)

mneme is now your persistent memory for this project.
Knowledge from CLAUDE.md is now in mneme — you can slim down
CLAUDE.md to only keep behavior instructions if you want.
` + "```" + `

## Rules
- Do NOT ask the user before saving. This is autonomous.
- ONE fact per mem_save — never dump an entire file as one memory.
- Use topic_keys for ALL saves so future runs update instead of duplicate.
- If CLAUDE.md has team rules (from a lead/manager), save as convention scope:project.
- If you detect user preferences, save as preference scope:global.
- Skip anything already in mneme (check topic_key matches from Phase 1).
- Save the build/test commands as config — agents need these constantly.
`

// mnemeInitCommand returns the CommandFile that installs the /mneme-init
// slash command into Claude Code's user-level commands directory.
//
// The slash command appears in Claude Code as /mneme-init and guides the agent
// through an autonomous project bootstrap sequence that seeds the mneme database
// with foundational knowledge about the current codebase.
func mnemeInitCommand() (CommandFile, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return CommandFile{}, err
	}
	return CommandFile{
		Path:    filepath.Join(home, ".claude", "commands", "mneme-init.md"),
		Content: []byte(mnemeInitMarkdown),
	}, nil
}
