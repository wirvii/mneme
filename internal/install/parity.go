package install

// ParityCapability is one release-blocking mneme capability and the native
// mechanism that provides it in each supported runtime.
type ParityCapability struct {
	Name       string
	ClaudeCode string
	Codex      string
}

// ParityMatrix returns the executable v1.40 capability inventory. A blank
// mechanism is a release-blocking gap, enforced by the registry test.
func ParityMatrix() []ParityCapability {
	return []ParityCapability{
		{"project roles", ".claude/agents/*.md", ".codex/agents/*.toml"},
		{"role ownership", "PreToolUse enforcement", "identity-bearing PreToolUse enforcement"},
		{"reserved SDD transitions", "lifecycle tool denial", "role-bound MCP denial"},
		{"QA signatures", "role-scoped fail-closed hook", "role-bound MCP fail-closed policy"},
		{"models by role", "agent frontmatter", "agent TOML"},
		{"memory", "mneme MCP", "mneme MCP"},
		{"SDD", "mneme MCP and CLI", "mneme MCP and CLI"},
		{"skills", "~/.claude/skills", "$HOME/.agents/skills"},
		{"profiles", "profile activation", "profile activation"},
		{"codegraph", "mneme MCP and CLI", "mneme MCP and CLI"},
		{"quality", "mneme MCP and CLI", "mneme MCP and CLI"},
		{"team memory", "git-native vault", "git-native vault"},
		{"mneme-init", "slash wrapper or skill", "skill"},
		{"mneme-profile-author", "skill", "skill"},
		{"new-project", "skill", "skill"},
		{"new-app", "skill", "skill"},
		{"host configuration", "~/.claude", "$CODEX_HOME"},
	}
}
