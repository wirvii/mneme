package install

import (
	"os"
	"path/filepath"
)

// mnemeInitMarkdown is the content of the /mneme-init Claude Code slash command.
// It instructs the agent to scan the current project and seed mneme with
// foundational knowledge extracted from well-known project files.
const mnemeInitMarkdown = `# /mneme-init — Initialize project memory

Scan the current project and seed mneme with foundational knowledge.

## Steps

1. Detect the project from git remote
2. Read these files if they exist and extract knowledge:
   - CLAUDE.md → conventions, rules, project overview → save as type:convention
   - package.json → dependencies, scripts, framework → save as type:architecture
   - go.mod → Go modules, dependencies → save as type:architecture
   - Cargo.toml → Rust crates → save as type:architecture
   - tsconfig.json / next.config.* → framework config → save as type:config
   - docker-compose.yml → services, infrastructure → save as type:architecture
   - .env.example → environment variables needed → save as type:config
3. Scan directory structure (top 2 levels) → save project layout as type:architecture
4. Check for existing memories with mem_context:
   - If memories exist, skip already-known facts
   - Only save NEW knowledge not yet in memory
5. Save each piece of knowledge using mem_save with appropriate type and topic_key

## Rules
- Do NOT ask the user before saving. This is autonomous.
- Use topic_keys like "architecture/tech-stack", "architecture/project-structure", "convention/commits"
- Keep each memory focused — one fact per save, not a dump of the entire file
- If CLAUDE.md has team rules, save them as convention scope:project
- If you detect the user's preferences, save as preference scope:global
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
